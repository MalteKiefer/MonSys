# MonSys Operations Runbook

On-call quick reference. Assumes shell access to `home:/srv/mon/deploy`, root via
sudo, and the GitHub repo. All `docker compose` commands assume `cwd =
/srv/mon/deploy` unless stated otherwise.

---

## 1. Deployment topology

Public URL: <https://mon.kiefer-networks.de>. Host-level Caddy terminates TLS
and reverse-proxies to `127.0.0.1:8080` (the `mon-server` port exposed by
compose). The stack is two services under `deploy/docker-compose.yaml`:

- **`mon-server`** — distroless Go binary, listens on `:8080`, drops all
  caps, `read_only` rootfs, `tmpfs /tmp`.
- **`timescaledb`** — TimescaleDB on PostgreSQL 16, digest-pinned, data on
  the `tsdb` named volume, hardened with `no-new-privileges` and a tight
  cap allowlist.

Overlays layered on top of the base compose file:

- `deploy/docker-compose.prod.yaml` — pulls the signed image from GHCR
  (`ghcr.io/maltekiefer/monsys-server:${MONSYS_TAG}`) instead of building.
- `deploy/docker-compose.tls.yaml` — turns on TLS on the `mon-server` HTTP
  listener directly (only needed if Caddy is bypassed).

Agents (`mon-agent`) on every monitored host self-update on a tick: they
download the next signed binary from GitHub Releases and minisign-verify
it against the public key baked in at build time before replacing
`/usr/local/bin/mon-agent` and restarting via systemd.

```
+-----------------+        TLS         +-------------+        :8080         +------------+
|  internet (443) | -----------------> |    Caddy    | -------------------> | mon-server |
+-----------------+                    +-------------+                      +-----+------+
                                                                                  | 5432
                                                                                  v
                                                                          +---------------+
                                                                          |  timescaledb  |
                                                                          +---------------+

  mon-agent on each host  --(HTTPS bearer)-->  /v1/ingest
  mon-agent self-update    --(HTTPS, minisign-verified)-->  GitHub Releases
```

---

## 2. First-run boot

Bring up a fresh server.

```sh
# 0. install docker engine + compose v2, plus Caddy on the host for TLS.

# 1. clone
sudo mkdir -p /srv/mon && sudo chown "$(id -u)":"$(id -g)" /srv/mon
git clone https://github.com/MalteKiefer/MonSys /srv/mon
cd /srv/mon/deploy

# 2. configure WebAuthn relying-party in a local override file
cat > docker-compose.override.yaml <<'YAML'
name: mon
services:
  mon-server:
    environment:
      MON_RP_ID: "mon.kiefer-networks.de"
      MON_RP_ORIGIN: "https://mon.kiefer-networks.de"
YAML

# 3. DB password secret (read by both services)
mkdir -p secrets
openssl rand -base64 32 > secrets/db_pw
chmod 0600 secrets/db_pw
sudo chown 65532 secrets/db_pw   # distroless nonroot uid

# 4. bring it up
cd /srv/mon && make compose-up
docker compose -f deploy/docker-compose.yaml ps
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz

# 5. create the first admin (mon-server's CLI bypasses the web flow)
docker compose -f deploy/docker-compose.yaml exec mon-server \
  /mon-server --create-user \
    --user-email=ops@example.com \
    --user-password='replace-me' \
    --user-role=admin
```

---

## 3. Routine operations

### Update prod from `main`

```sh
cd /srv/mon
git fetch origin && git checkout main && git pull --ff-only
docker compose -f deploy/docker-compose.yaml build mon-server
docker compose -f deploy/docker-compose.yaml up -d mon-server
docker compose -f deploy/docker-compose.yaml logs -f --tail=200 mon-server
```

Smoke checks:

```sh
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/readyz
```

### Tail logs

```sh
docker compose -f deploy/docker-compose.yaml logs -f mon-server
docker compose -f deploy/docker-compose.yaml logs -f timescaledb
```

### Database backup

Before pulling a dump, run the preflight from inside the mon-server
container. It reports schema version, audit-chain integrity, and row
counts for the heavy tables, then prints the exact `pg_dump` invocation:

```sh
cd /srv/mon/deploy
docker compose exec -T mon-server /mon-server --backup-preflight
```

Example output:

```text
MonSys backup preflight
  timestamp:      2026-05-12T13:22:06Z
  schema version: 31
  audit chain:    OK (2 rows verified)

Row counts:
  hosts:                 1
  users:                 1
  audit_log:             2
  alert_history:         0
  notification_rules:    0

To capture the dump, run on the host:
  cd /srv/mon/deploy
  sudo docker compose exec -T timescaledb \
    pg_dump -U mon -d mon --clean --if-exists --no-owner --no-privileges \
    | gzip -c > backup-$(date -u +%Y%m%dT%H%M%SZ).sql.gz
```

Why split it this way: `pg_dump` is not in the distroless `mon-server`
image and re-implementing it in Go is fragile. The preflight gives you
the safety check (schema version, chain status, dump-size signal) inside
the binary; the actual dump runs from the `timescaledb` container that
already ships `pg_dump`.

Restore — always run the restore preflight first against the target DSN.
It refuses if the target already has live `users`/`audit_log` rows and
prints the verbatim `DROP SCHEMA` recipe when you want to overwrite:

```sh
cd /srv/mon/deploy
docker compose exec -T mon-server /mon-server --restore-preflight
```

Then, once the target is empty (or you have accepted the clobber):

```sh
gunzip -c backup-XXX.sql.gz | docker compose exec -T timescaledb \
  psql -U mon -d mon -v ON_ERROR_STOP=1
```

Migration round-trip tests run on every CI build against TimescaleDB
`latest-pg16` (same digest as `deploy/docker-compose.yaml`). Trust but
verify after every migration change with `make test-migrations` — it
exercises Up -> Down -> Up plus a per-migration Down-from-mid check
inside a real container, and is the cheapest way to catch a Down
statement that drops too much before it hits production. Migrations
flagged in `internal/server/store/migrations_test.go::knownUnreversible`
are exercised but skip the byte-identical schema comparison; the file
documents each entry.

### Open a `psql` shell

```sh
docker compose -f deploy/docker-compose.yaml exec timescaledb psql -U mon -d mon
```

---

## 4. Account recovery

All flags below are defined in `cmd/mon-server/main.go`. Invoke them via
`docker compose exec mon-server /mon-server <flags>`. They bypass any
web-side confirmation (current-password, current-2fa) because the operator
already has shell.

| Pain point | Command |
| --- | --- |
| Forgot admin password | `mon-server --reset-password --user-email=admin@x --user-password='new'` |
| Forgot password, weak policy needed | add `--force-weak-password` (logs a WARN). |
| Lost TOTP | `mon-server --disable-totp --user-email=admin@x` (also revokes that user's sessions) |
| Lost passkey (all of them) | `mon-server --delete-all-passkeys --user-email=admin@x` |
| List a user's passkeys | `mon-server --list-passkeys --user-email=admin@x` |
| Last admin locked out by force_mode | `mon-server --set-security-policy --force-mode=off` |
| Adjust other security policy fields | `--set-security-policy --grace-days=N --max-session-hours=N --idle-timeout-minutes=N` |
| Inspect current policy | `mon-server --get-security-policy` |
| Revoke every active session | `mon-server --revoke-all-sessions` |
| Change email when SMTP is broken | `mon-server --change-email --user-email=old@x --new-email=new@x` |
| Issue a one-shot agent enrollment token | `mon-server --new-token --token-description='host-42' --token-ttl=1h` |
| Create another user from shell | `mon-server --create-user --user-email=u@x --user-password='p' --user-role=user` |
| Verify the audit-log hash chain | `mon-server --verify-audit-chain` (exit 0 intact, 1 broken, 2 op error) |
| Backup preflight (schema + counts + cmd) | `mon-server --backup-preflight` |
| Restore preflight (target safety check) | `mon-server --restore-preflight` |

Full example (admin password reset against the running container):

```sh
docker compose -f deploy/docker-compose.yaml exec mon-server \
  /mon-server --reset-password \
    --user-email=ops@example.com \
    --user-password='choose-a-strong-one'
```

---

## 4a. Web frontend

The SPA is served by `mon-server` itself out of the embedded `dist/` bundle
(`internal/server/spa`). No separate web service to operate.

**Installable PWA.** The build ships a service worker (`dist/sw.js`) and a
web app manifest (`dist/manifest.webmanifest`) generated by
`vite-plugin-pwa`. End-users can install the SPA via Chrome's address-bar
install button on desktop, or via "Add to Home Screen" on mobile. After the
first visit the app shell loads from cache, so a flaky network only delays
the first API call — not the UI itself. Configuration:

- App-shell precache: hashed JS/CSS chunks, Inter + JetBrains Mono woff2
  fonts, `index.html`, the manifest, and the brand icons.
- **API surfaces are never cached.** `/v1/*`, `/healthz`, `/readyz`,
  `/openapi.*`, `/docs/*`, `/metrics`, and `/.well-known/*` are pinned to
  `NetworkOnly` and excluded from the SPA navigation fallback. Bearer
  tokens live in `localStorage`, so they survive an offline reload, but
  every authenticated request still hits the network fresh.
- Update strategy is `autoUpdate`: when a new build is deployed,
  `mon-server` ships the new `sw.js`, the SW calls `skipWaiting()` +
  `clientsClaim()` on the next page load, and `cleanupOutdatedCaches()`
  evicts the previous shell. No banner today; users get the new UI on
  their next navigation.

To verify after a deploy: open Chrome DevTools → Application → Manifest
(should validate) and Application → Service workers (should show
`/sw.js` activated). The "Install" button appears in the address bar
once the manifest is parsed.

---

## 5. Investigations

Open a `psql` shell first (see §3).

### "Why is this host offline?"

```sql
SELECT id, hostname, last_seen_at, agent_version
FROM hosts
WHERE hostname = 'web-01';
```

Liveness is derived from `last_seen_at`; the `liveness` goroutine reaps
hosts every 30s. If `last_seen_at` is recent but the UI says offline,
check `docker compose logs -f mon-server` for the liveness watcher.

### "Why did this alert fire?"

```sql
SELECT at, severity, subject, dedup_key, delivered_to, delivery_errors
FROM alert_history
WHERE rule_id = '00000000-0000-0000-0000-000000000000'
ORDER BY at DESC
LIMIT 20;
```

To find recent alerts for any rule:

```sql
SELECT at, rule_name, severity, subject
FROM alert_history
ORDER BY at DESC
LIMIT 50;
```

### "Was someone tampering?"

```sql
SELECT * FROM audit_log
ORDER BY at DESC
LIMIT 200;
```

Then verify the SHA-256 hash chain hasn't been broken:

```sh
docker compose -f deploy/docker-compose.yaml exec mon-server \
  /mon-server --verify-audit-chain
echo "exit=$?"   # 0 intact, 1 broken, 2 op error
```

A non-zero exit means a row was deleted, mutated, or inserted out of
order — treat as a security incident.

### "Who has open sessions?"

```sql
SELECT u.email, count(*) AS sessions
FROM user_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.revoked_at IS NULL
  AND s.expires_at > now()
GROUP BY u.email
ORDER BY sessions DESC;
```

### "Show me the most recent ingests / audit / server logs"

These all live under `/admin/logs?tab=…` in the UI:

- `/admin/logs?tab=server` — process logs (`/v1/admin/logs`)
- `/admin/logs?tab=audit`  — admin actions (`/v1/admin/audit`)
- `/admin/logs?tab=ingest` — most recent agent payloads (`/v1/admin/ingests`, `/v1/admin/ingests/{idx}`)

---

## 6. Rollback

### Bad release shipped

```sh
cd /srv/mon
git revert HEAD
git push origin main
make compose-up                  # rebuilds mon-server from the reverted main
```

If you're on the signed-image overlay (prod), pin the previous tag:

```sh
MONSYS_TAG=v0.4.1 \
  docker compose \
    -f deploy/docker-compose.yaml \
    -f deploy/docker-compose.prod.yaml \
    pull mon-server
MONSYS_TAG=v0.4.1 \
  docker compose \
    -f deploy/docker-compose.yaml \
    -f deploy/docker-compose.prod.yaml \
    up -d --no-build mon-server
```

Don't manually re-deploy from S3 / a workstation build. Always go through
the release pipeline so cosign + minisign signatures match what the fleet
will accept.

Agents will pick up the prior signed binary on their next self-update
tick (assuming the GitHub Releases artifact for the rolled-back tag is
still present, which it always is — releases are immutable).

### Agent self-update broken

The updater snapshots the live binary to `/usr/local/bin/mon-agent.prev`
before swapping, and rolls it back automatically if the post-swap
`systemctl restart mon-agent` fails (the rollback consumes the snapshot
and re-runs the restart). On the happy path the `.prev` is deleted.

Operator-side manual rescue (if the agent is dead and there is no `.prev`
because the rollback already consumed it, or the snapshot couldn't be
written):

```sh
# pick a known-good version from the releases page
VERSION=v0.4.1
ARCH=amd64
curl -fsSLO "https://github.com/MalteKiefer/MonSys/releases/download/${VERSION}/mon-agent-linux-${ARCH}"
curl -fsSLO "https://github.com/MalteKiefer/MonSys/releases/download/${VERSION}/mon-agent-linux-${ARCH}.minisig"
minisign -V -P "<key-from-docs/SIGNING.md>" -m "mon-agent-linux-${ARCH}"
sudo install -m 0755 "mon-agent-linux-${ARCH}" /usr/local/bin/mon-agent
sudo systemctl restart mon-agent
```

---

## 7. Monitoring + alerting

### Liveness endpoints

| Endpoint | Meaning |
| --- | --- |
| `GET /healthz` | Process is up. Returns `ok\n`. No DB check. Use for load balancer liveness. |
| `GET /readyz`  | Process up **and** DB ping succeeds within 2s. Returns `ready\n` or 503. Use for readiness gates. |

### Admin observability (UI consolidates under `/admin/logs?tab=…`)

| API path | UI tab |
| --- | --- |
| `/v1/admin/logs`        | `/admin/logs?tab=server` |
| `/v1/admin/audit`       | `/admin/logs?tab=audit`  |
| `/v1/admin/ingests`     | `/admin/logs?tab=ingest` |
| `/v1/admin/ingests/{idx}` | (drill-down from the ingest tab) |

### Fuzz crashes

`.github/workflows/fuzz.yaml` runs on a schedule and opens a GitHub issue
labelled `fuzz-crash` on every regression. Triage within 7 days. A
`fuzz-crash` left open longer than the artifact retention window
(14 days) loses its repro corpus.

---

## 8. Security incident playbook

If you suspect compromise:

```sh
# 1. cut every web session immediately
docker compose -f deploy/docker-compose.yaml exec mon-server \
  /mon-server --revoke-all-sessions

# 2. capture audit log + verify chain integrity
docker compose -f deploy/docker-compose.yaml exec timescaledb \
  pg_dump -U mon -t audit_log mon | gzip > "audit-$(date -u +%Y%m%dT%H%M%SZ).sql.gz"
docker compose -f deploy/docker-compose.yaml exec mon-server \
  /mon-server --verify-audit-chain ; echo "verify exit=$?"

# 3. rotate the DB password
openssl rand -base64 32 > deploy/secrets/db_pw.new
chmod 0600 deploy/secrets/db_pw.new
sudo chown 65532 deploy/secrets/db_pw.new
NEW_PW="$(cat deploy/secrets/db_pw.new)"
docker compose -f deploy/docker-compose.yaml exec timescaledb \
  psql -U mon -d mon -c "ALTER USER mon WITH PASSWORD '${NEW_PW}';"
mv deploy/secrets/db_pw{,.old}
mv deploy/secrets/db_pw{.new,}
docker compose -f deploy/docker-compose.yaml up -d --force-recreate mon-server timescaledb

# 4. cross-reference with host-side login events
docker compose -f deploy/docker-compose.yaml exec timescaledb \
  psql -U mon -d mon -c "SELECT time, host_id, username, success, remote_addr
                         FROM login_events
                         WHERE time > now() - interval '7 days'
                         ORDER BY time DESC LIMIT 100;"

# 5. cosign-verify the running image matches the latest signed release
#    (replace TAG with the tag the prod overlay is pinned to)
TAG=v0.4.2
cosign verify \
  --certificate-identity-regexp '^https://github\.com/MalteKiefer/MonSys/\.github/workflows/release\.ya?ml@refs/(tags|heads)/.*$' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  "ghcr.io/maltekiefer/monsys-server:${TAG}"
```

### Rotate the minisign release key

The agent fleet trusts whatever public key is embedded in the running
binary. Rotation must happen in two steps — first ship agents that trust
the new key (typically dual-trust during a transition release), then
flip releases to the new private key. **Back up the old public key
first** so you can still verify historical artifacts.

```sh
# in a trusted offline environment:
cp release.pub  release.pub.$(date -u +%Y%m%d).old
cp release.key  release.key.$(date -u +%Y%m%d).old.enc
minisign -G -p release.pub -s release.key
# upload release.key to GitHub Actions secret RELEASE_MINISIGN_KEY
# upload the passphrase   to GitHub Actions secret RELEASE_MINISIGN_PASSWORD
# update updater.PublicKey in internal/agent/updater/verify.go via -ldflags
# cut a release, wait for the fleet to roll forward
```

See `docs/SIGNING.md` for the full procedure.

---

## 9. Useful links

- [`https://mon.kiefer-networks.de/docs`](https://mon.kiefer-networks.de/docs) — live interactive OpenAPI viewer (Scalar). **Admin auth required**: log in to the SPA first, copy the session token from the `Authorization: Bearer …` header (or send the request with `curl -H "Authorization: Bearer $token"`). The raw spec lives at `/openapi.yaml` and `/openapi.json` and is gated by the same session check (AUDIT-066).
- `api/openapi.yaml` — committed copy of the spec; CI fails on drift versus `mon-server --print-spec`.
- `SECURITY_AUDIT_REPORT.md` — open security findings, severities, remediation status.
- `docs/adr/README.md` — index of all architectural decision records.
- `docs/COMMIT-SIGNING.md` — DCO + signed-commit requirements.
- `docs/SIGNING.md` — minisign release-signing procedure.
- `docs/RELEASE.md` — cutting a release; cosign + minisign verification commands.
- `docs/TLS.md` — TLS topology, cert provisioning, Caddy config.
- `docs/PRIVACY.md` — data retention + privacy posture.
- `.github/workflows/ci.yaml` — CI gates (lint, vet, tests, OpenAPI drift).
- `.github/workflows/release.yaml` — release pipeline (build, sign, publish).
- `.github/workflows/fuzz.yaml` — scheduled fuzz; opens `fuzz-crash` issues.

---

## Appendix: environment variables read by `mon-server`

| Var | Default | Purpose |
| --- | --- | --- |
| `MON_LISTEN_ADDR`     | `:8080` | HTTP listen address. |
| `MON_DSN`             | (required) | Postgres DSN; password injected from `MON_DSN_PASSWORD_FILE`. |
| `MON_DSN_PASSWORD_FILE` | `/run/secrets/db_pw` | Path to the file holding the DB password. |
| `MON_TLS_CERT`        | unset   | Enables direct TLS when set together with `MON_TLS_KEY`. |
| `MON_TLS_KEY`         | unset   | Private key for direct TLS. |
| `MON_RP_ID`           | `localhost` | WebAuthn RP ID — must be the bare host. |
| `MON_RP_ORIGIN`       | `http://localhost:5173` | WebAuthn RP origin — full scheme+host(+port). |
| `MON_LOG_BUFFER`      | `5000`  | In-memory log ring buffer size. |
| `MON_INGEST_BUFFER`   | `100`   | Captured-ingest ring buffer size. |
| `MON_INGEST_MAX_BYTES`| `1048576` | Max captured ingest body size. |
