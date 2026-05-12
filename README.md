# MonSys

[![Repo](https://img.shields.io/badge/github-MalteKiefer%2FMonSys-blue?logo=github)](https://github.com/MalteKiefer/MonSys)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

Self-hosted server-monitoring stack: a Go control-plane server, a Go Linux
agent, a React single-page app, and TimescaleDB for metric storage. Designed
for small to mid-sized fleets where you want full ownership of the data
plane without running a hosted SaaS.

- **Server** — Go 1.26 with chi + huma for an OpenAPI-typed REST surface,
  embeds the React SPA, talks to TimescaleDB via pgx.
- **Agent** — Go binary, tiny systemd-hardened footprint, ships metrics +
  inventory + login events + package state + firewall/CrowdSec snapshots.
- **Database** — TimescaleDB hypertables with retention policies for every
  metrics table; `audit_log` and `alert_history` are bounded too.
- **Web UI** — React 19 + Vite + Tailwind 3, dark/light themes, lazy-loaded
  admin pages.

The Go module is `github.com/MalteKiefer/MonSys`; the shipped binaries are
`mon-server` and `mon-agent`.

---

## Architecture at a glance

```
                  ┌──────────────┐
                  │   Web SPA    │  /                                 ┐
                  └──────┬───────┘                                    │
                         │ (https)                                    │
                         ▼                                            │
   ┌──────────┐    ┌──────────────┐    ┌──────────────────────┐      │
   │  Caddy   │───▶│  mon-server  │───▶│      TimescaleDB     │      │
   │  (TLS)   │    │  (chi+huma)  │    │   metrics_*, hosts,  │      │
   └────▲─────┘    └──────▲───────┘    │   alert_history, …   │      │
        │                 │            └──────────────────────┘      │
        │  /v1/ingest     │ /v1/ingest                                │
        │  (agent_key)    │                                           │
        ▼                 │                                           │
   ┌──────────┐    ┌──────┴───────┐                                   │
   │  Agent A │…   │   Agent B    │   (each runs as systemd service,  │
   └──────────┘    └──────────────┘    monagent user, host-local YAML)│
                                                                      ┘
```

**Live API docs.** The running `mon-server` ships an interactive OpenAPI
viewer (Scalar, vendored into the binary — no CDN at runtime) at
[`/docs`](https://mon.kiefer-networks.de/docs). Admin session required —
the route is gated by `requireSessionForDocs` (AUDIT-066). The raw spec
is served at `/openapi.yaml` and `/openapi.json` behind the same gate, and
the committed copy lives at [`api/openapi.yaml`](api/openapi.yaml).

---

## Server install

This is the long-form, hand-holding version. Skim the **Quick start** below
if you have docker/compose-fluency.

### Quick start

```sh
make web                                                          # build SPA
echo -n "$(openssl rand -base64 32 | tr -d '=+/' | head -c 32)" \
  > deploy/secrets/db_pw && chmod 0600 deploy/secrets/db_pw
docker compose -f deploy/docker-compose.yaml up -d
docker compose -f deploy/docker-compose.yaml exec mon-server \
  /mon-server --create-user --user-email you@example.com \
              --user-role admin --user-password 'CHANGEME'
```

That's the whole thing. Now follow with **Reverse proxy** below for TLS.

### Detailed walkthrough

1. **Prereqs.**

   - Docker engine 24+ with the compose plugin (`docker compose version`).
   - 4 GB RAM minimum (Timescale + Go server + a small SPA hit ~500 MB
     idle, much more under load).
   - Outbound 443 if you want public TLS via Caddy/Let's Encrypt.

2. **Clone + secrets.**

   ```sh
   git clone https://github.com/MalteKiefer/MonSys.git /srv/mon
   cd /srv/mon
   mkdir -p deploy/secrets
   openssl rand -base64 32 | tr -d '=+/' | head -c 32 \
     > deploy/secrets/db_pw
   chmod 0600 deploy/secrets/db_pw
   chown 65532:65532 deploy/secrets/db_pw   # distroless 'nonroot' uid
   ```

3. **Build the SPA into the server binary.**

   The SPA lives in `web/` and is embedded in the server binary via
   `internal/server/spa`. The `web` make target builds Vite output and
   stages it into the embed directory.

   ```sh
   make web
   ```

   You only need this on the *first* build and whenever the SPA source
   changes. The Dockerfile build chain in `deploy/Dockerfile.server`
   re-runs it on every `docker compose build`.

4. **Start the stack.**

   ```sh
   docker compose -f deploy/docker-compose.yaml up -d
   ```

   This brings up:

   - `timescaledb` (digest-pinned image, internal network only, db_pw
     secret mounted),
   - `mon-server` (distroless static, read-only rootfs, cap_drop ALL,
     no-new-privileges, listens on `127.0.0.1:8080` by default).

5. **Create the first admin.**

   ```sh
   docker compose -f deploy/docker-compose.yaml exec mon-server \
     /mon-server --create-user \
                 --user-email you@example.com \
                 --user-role admin \
                 --user-password 'pick-something-strong'
   ```

   Pick a strong password — there is no default account. Skip
   `--user-password` to read it from stdin instead.

6. **Reverse proxy + TLS (Caddy example).**

   The container only binds to loopback (`127.0.0.1:8080`); a reverse
   proxy in front of it terminates TLS and adds rate-limit/header
   shims. The shipped Caddy snippet looks like:

   ```caddy
   mon.example.com {
       tls you@example.com
       header {
           Strict-Transport-Security "max-age=63072000; includeSubDomains; preload"
           Referrer-Policy "strict-origin-when-cross-origin"
       }
       reverse_proxy 127.0.0.1:8080
   }
   ```

   Nginx, Traefik, HAProxy all work the same way.

7. **Configure SMTP + quiet hours (optional).**

   Sign in to the SPA, open **Admin → Mail (SMTP)** and fill the form.
   The PUT is non-fatal even if the test send fails — channels of type
   `email` will use these settings at dispatch. Server-side quiet hours
   live under **Admin → Quiet hours**.

### Upgrades

```sh
cd /srv/mon
git fetch origin && git reset --hard origin/main
docker compose -f deploy/docker-compose.yaml up -d --build mon-server
```

Migrations run automatically on startup (goose). The server logs
`goose: successfully migrated database to version: N` when one applies.
Migrations 0016 and 0021 convert tables to Timescale hypertables and
take an `AccessExclusiveLock` for the duration of `migrate_data => TRUE`
— schedule them outside business hours on large data sets.

### Backups

Timescale data lives in the `tsdb` named volume.

```sh
docker compose -f deploy/docker-compose.yaml exec timescaledb \
  pg_dump -U mon -d mon -Fc -f /tmp/mon.dump
docker cp $(docker compose -f deploy/docker-compose.yaml ps -q timescaledb):/tmp/mon.dump ./mon-$(date +%F).dump
```

The agent_key column is bcrypt-hashed; restoring the dump is enough,
agents reconnect with their existing key.

---

## Agent install

### Quick start (systemd, x86_64)

```sh
# 1. Issue a one-shot bootstrap token on the server
docker compose -f deploy/docker-compose.yaml exec mon-server \
  /mon-server --new-token --token-description "$(hostname)" --token-ttl 1h

# 2. On the host you want to monitor:
sudo useradd --system --no-create-home --shell /usr/sbin/nologin monagent
sudo install -m 0755 mon-agent-linux-amd64 /usr/local/bin/mon-agent
sudo install -d -o root -g monagent -m 0750 /etc/mon-agent
sudo install -d -o monagent -g monagent -m 0750 /var/lib/mon-agent
sudo install -m 0640 -o root -g monagent config.yaml /etc/mon-agent/config.yaml
sudo install -m 0644 mon-agent.service /etc/systemd/system/mon-agent.service

# 3. Bootstrap (one-shot, key file persists)
sudo -u monagent timeout 6 /usr/local/bin/mon-agent \
  --config=/etc/mon-agent/config.yaml \
  --bootstrap-token=mon_bs_…   # the token from step 1

# 4. Run + persist
sudo systemctl daemon-reload
sudo systemctl enable --now mon-agent.service
```

The agent re-uses its `agent_key` for every subsequent ingest. You can
revoke it from the SPA's host detail screen.

### Detailed walkthrough

1. **Pick the binary.**

   Pre-built statics live under `bin/` after `make build-all`:

   - `bin/mon-agent-linux-amd64` (most servers, including Hetzner Cloud,
     Hetzner dedicated, Linode, DO).
   - `bin/mon-agent-linux-arm64` (RPi 4/5, Hetzner ARM, AWS Graviton).

   Self-build:

   ```sh
   git clone https://github.com/MalteKiefer/MonSys.git
   cd MonSys
   make build-all
   ```

2. **Pick a config template.**

   `deploy/agent/` contains examples:

   - `config.example.yaml` — minimal prod config, well-commented.
   - `config.docker-host.yaml` — adds Docker engine + optional Proxmox.
   - `config.dev.yaml` — local mon-server over plain HTTP.

   Copy the closest one to `/etc/mon-agent/config.yaml` and adjust
   `server_url`, `labels`, and TLS pin.

3. **Bootstrap the agent_key.**

   The bootstrap token is one-shot and short-lived. After a successful
   register, the server returns an `agent_id:agent_key` and writes it
   to `/var/lib/mon-agent/agent.key` mode 0400 owned by monagent. The
   token then becomes useless.

   On scripted rollouts, the agent's `--bootstrap-token` flag is
   preferred over the `MON_BOOTSTRAP_TOKEN` env var because the env
   gets visible in `/proc/<pid>/environ` for a short window.

4. **systemd unit.**

   `deploy/systemd/mon-agent.service` is the canonical hardened unit.
   Highlights:

   - Runs as `monagent`, no shell, no home.
   - `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateDevices=yes`.
   - `AmbientCapabilities=CAP_NET_ADMIN CAP_DAC_READ_SEARCH` so it can
     read `nft list ruleset`, `iptables-save`, and `/var/log/wtmp`.
   - `SupplementaryGroups=docker libvirt systemd-journal fail2ban` —
     these need to exist on the host for the systemd unit to start; on
     hosts where they don't, drop the unknown ones from the line.
   - `WatchdogSec=60s` — agent pings systemd, which restarts it on
     stalls.

5. **Verify.**

   ```sh
   systemctl is-active mon-agent.service          # active
   journalctl -u mon-agent --since 1m --no-pager  # ingest accepted/snapshot_at
   ```

   On the server side the host appears under **Hosts** within ~30 s.

### Bulk rollout

The simplest bulk-rollout pattern is

```sh
for h in alpha beta gamma; do
  T=$(ssh server "/srv/mon/.../mon-server --new-token --token-description $h --token-ttl 30m" | tail -1)
  scp bin/mon-agent-linux-amd64 deploy/systemd/mon-agent.service deploy/agent/config.example.yaml $h:/tmp/
  ssh $h "bash" < deploy/agent/install.sh "$h" "$T"
done
```

A reference `install.sh` is at `deploy/agent/install.sh`; tailor the
hostname/labels/TLS-pin substitutions to your environment.

### Removing an agent

```sh
sudo systemctl disable --now mon-agent.service
sudo rm -rf /etc/mon-agent /var/lib/mon-agent /var/log/mon-agent \
            /etc/systemd/system/mon-agent.service /usr/local/bin/mon-agent
sudo userdel monagent
```

Then revoke the agent in the SPA so its `agent_key` stops being
honoured.

---

## Operating notes

- **Bootstrap tokens** are SHA-256 hashed at rest. Their plaintext is
  shown exactly once.
- **Agent keys** are bcrypt-cost-12 hashed at rest.
- **Audit log** records every admin-only mutation. Read it under
  **Admin → Audit log** or via `GET /v1/admin/audit`.
- **Alert resolution.** When a host transitions back to OK after firing
  a `host_offline` alert, MonSys emits an `[Resolved]` notification
  through the same channel set, scoped by `dedup_key`.
- **Quiet hours** suppress *outbound dispatch only*; alert_history is
  still written so the audit trail is intact.

---

## Release verification

Release artifacts (binaries, container image, SBOMs) ship with detached
minisign signatures and a Sigstore cosign keyless signature on the
container manifest. See [docs/RELEASE.md](./docs/RELEASE.md) for the
exact `cosign verify` and `minisign -V` commands, and for how to pin a
signed image via `deploy/docker-compose.prod.yaml`.

## Reporting vulnerabilities

See [SECURITY.md](./SECURITY.md) for the disclosure policy, scope, and
SLA targets. Please do not file public issues for security-relevant
findings.

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md). Run `make install-hooks` once
per checkout to enable the commit-msg hook.

## License

[MIT](./LICENSE).
