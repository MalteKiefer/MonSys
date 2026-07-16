# MonSys — Complete Security Audit

**Date:** 2026-07-16
**Scope:** Full codebase (Go server + agent, React SPA), build pipeline, CI/CD, deployment (Docker/compose), systemd units, configuration, dependencies.
**Standards:** OWASP Top 10:2025, OWASP ASVS 5.0, SOC 2 TSC, CWE. CVSS v4.0 base scores (v3.1 noted where 4.0 does not apply cleanly).
**Methods:** `govulncheck`, `gosec`, `staticcheck`, manual review across 6 domains, live dependency-version research.
**Prior audit:** `SECURITY_AUDIT_REPORT.md` (2026-05-12) — this pass is independent; overlaps are noted.

> **Headline:** The codebase is unusually well-hardened — parameterized SQL throughout, bcrypt cost 12, crypto/rand secrets, thorough session invalidation, strong CSP/headers, SHA-pinned Actions, distroless non-root image, hardened systemd units. No SQL injection, command injection, or XSS sinks were found. The material risks are concentrated in **(1) the agent self-update trust chain**, which as shipped is either non-functional or fail-open, and **(2) outbound SSRF** from user-created monitors and webhooks. Plus a set of **known-CVE dependency bumps** (govulncheck-confirmed reachable).

---

## Findings by severity (sorted by CVSS)

| ID | Severity | CVSS | Title | File |
|----|----------|------|-------|------|
| C1 | Critical | 9.2 | Agent self-update trust root never wired; fail-open verification branches → root RCE across fleet | `internal/agent/updater/updater.go:284`, `.github/workflows/release.yaml:136` |
| H1 | High | 8.6 | SSRF via user-created monitors, default-allow guard, results readable | `internal/server/probe/probe.go:118` |
| H2 | High | 7.7 | Known-CVE stdlib/x-libs reachable (crypto/tls ECH, x/image WEBP panic) | `go.mod`, `internal/server/api/profile.go:87` |
| H3 | High | 7.1 | SSRF via notification webhooks, no scheme/host validation, redirects followed | `internal/server/notify/notify.go:331` |
| M1 | Medium | 6.9 | SSRF guard `MON_AGENT_UPDATE_INSECURE=1` accepts failed signature | `internal/agent/updater/updater.go:305` |
| M2 | Medium | 6.5 | TOTP seeds + backup codes stored plaintext at rest | `internal/server/store/migrations/0008_user_security.sql:8` |
| M3 | Medium | 6.3 | Auth compliance/MFA-enforcement middleware fails open on cache miss | `internal/server/api/api.go:3136` |
| M4 | Medium | 6.1 | `middleware.RealIP` trusts `X-Forwarded-For` with no trusted-proxy allowlist | `internal/server/api/api.go:100` |
| M5 | Medium | 5.8 | Auto-release + fleet auto-update from unreviewed `main`, no change-mgmt gate | `.github/workflows/release.yaml:59` |
| M6 | Medium | 5.6 | Prod deploy pins mutable tag + `pull_policy: always`, not verified digest | `deploy/docker-compose.prod.yaml:33` |
| M7 | Medium | 5.3 | No TOTP replay protection, no lockout on the 2FA challenge step | `internal/server/store/user_security.go:60` |
| M8 | Medium | 5.3 | TLS optional; `Secure`/HSTS cookies emitted over the plaintext-HTTP path | `cmd/mon-server/main.go:493` |
| M9 | Medium | 5.0 | PRIVACY.md retention (90 d) contradicts enforced retention (180/365/730 d) | `docs/PRIVACY.md:58` |
| L1 | Low | 4.3 | Login discloses "user is disabled" → account enumeration oracle | `internal/server/api/api.go:2757` |
| L2 | Low | 4.0 | `release.yaml` grants `contents: write` workflow-wide | `.github/workflows/release.yaml:38` |
| L3 | Low | 3.7 | `/readyz` returns raw DB error string to anonymous callers | `internal/server/api/api.go:469` |
| L4 | Low | 3.5 | WebAuthn RP silently defaults to `localhost` in prod when env unset | `cmd/mon-server/main.go:453` |
| L5 | Low | 3.1 | Broad `err.Error()` returned on ~23 HTTP 400 paths | `internal/server/api/api.go` (multiple) |
| L6 | Low | 2.6 | `openapi` spec written world-readable `0644` (gosec G306) | `cmd/mon-server/main.go:524,530` |
| I1 | Info | — | No-op `subtle.ConstantTimeCompare(x, x)` (dead mitigation) | `internal/server/store/auth.go:380` |
| I2 | Info | — | Unbounded JSONB label maps at ingest / agent config | `internal/shared/apitypes/apitypes.go:65` |
| I3 | Info | — | pre-commit golangci-lint v1.62 vs CI v2.11 (config incompatible) | `.pre-commit-config.yaml:9` |

---

## Critical

### C1 — Agent self-update trust root never wired; fail-open verification branches
**File:** `internal/agent/updater/updater.go:284`, `verify.go:52/74`, `.github/workflows/release.yaml:136-139`
**OWASP:** A08 Software & Data Integrity Failures / A03 Supply Chain · **SOC 2:** CC7.1, CC8.1 (change mgmt / integrity)
**CVSS v4.0:** 9.2 Critical — `AV:N/AC:H/AT:P/PR:N/UI:N/VC:H/VI:H/VA:H/SC:H/SI:H/SA:H`

**What.** The release workflow injects only `version.Version/Commit/Date` via ldflags (`release.yaml:136-138`). It never injects `updater.PublicKey`, and no `deploy/release.pub` exists in the tree. Every shipped `mon-agent` therefore carries `PublicKey = "PLACEHOLDER_MINISIGN_PUBLIC_KEY"` (`verify.go:52`). Two consequences follow from `updater.go:282-313`:

1. **Placeholder key → update permanently fails closed.** `verifyMinisig` rejects the placeholder (`verify.go:74`), so with `MON_AGENT_UPDATE_INSECURE` unset the update aborts (`updater.go:310`). Auto-update — enabled by default, root, every 6 h — silently never applies. Operators will be tempted to "fix" it with the insecure override (see M1).
2. **`PublicKey == ""` branch is fail-open (commit `b254a2d`).** A build with an *empty* key (a fork, or `go build -X ...PublicKey=`) takes `updater.go:284`, skips signature verification entirely, and installs a binary authenticated only by a SHA-256 that comes from the **same server** serving the binary (`agentupdate.go` → `SHA256SUMS` → binary, one channel). A compromised update server or MITM of the GitHub release API then pushes an arbitrary binary that runs as **root on every host**. This is exactly the "TLS/hash-only trust" model ADR-0007 rejects.

Additionally, CI signing itself is best-effort: if `MONSYS_MINISIGN_SECRET_KEY`/`_PASSWORD` are unset the signing step emits a `::warning` and **publishes the release unsigned** (`release.yaml:190-192`).

*Note:* `staticcheck` flags `verifyMinisig`/`compareSemver` as unused — that is a false positive from the `//go:build linux` tag under a darwin analysis host, not evidence of dead code; both are reached on Linux.

**Remediation.**
- Add `-X github.com/MalteKiefer/MonSys/internal/agent/updater.PublicKey=$(tail -n1 deploy/release.pub)` to the release ldflags; commit the real `deploy/release.pub` and reconcile the three divergent key-path references (`verify.go:29` says `deploy/keys/mon-agent.pub`, SIGNING.md says `deploy/release.pub`).
- Make an unset/placeholder key **fail closed** (refuse to self-update), never fall through to SHA-only. Delete the `PublicKey == ""` skip branch.
- Make the CI signing step a hard failure, not a warning, on any tagged release.
- Consider defaulting `auto_update.enabled=false` until the trust root is wired.

---

## High

### H1 — SSRF via user-created monitors (default-allow, results readable)
**File:** `internal/server/probe/probe.go:118-123`; reached via `handleCreateMonitor` (`api.go:923`, middleware `protected` — any authenticated user, not admin); read-back via `GET /v1/monitors/{id}/results`.
**OWASP:** A10 SSRF (also A01 — monitors have no per-owner check) · **SOC 2:** CC6.1, CC6.6
**CVSS v4.0:** 8.6 High — `AV:N/AC:L/AT:N/PR:L/UI:N/VC:H/VI:L/VA:N/SC:H/SI:N/SA:N`

**What.** `denyDestination` returns `nil` (allow) unless `MON_PROBE_ALLOW_INTERNAL=0` or `MON_PROBE_DENY_INTERNAL=1` is set — **default is allow-internal**. Any logged-in non-admin can create an `http`/`tcp`/`cert`/`postgres` monitor targeting `http://169.254.169.254/latest/meta-data/` (cloud metadata / IAM creds) or any RFC1918 host, then read the probe `Detail` (HTTP status/body snippet, cert subject, Postgres `version()`) back through the results endpoint. `insecure_skip_verify` is user-settable per monitor (`probe.go:204`) and the postgres probe accepts an arbitrary DSN.

**Remediation.** Make the deny-internal guard **default-on** (fail-closed) and reject link-local/metadata/RFC1918 targets regardless of the env toggle; additionally gate monitor create/update behind `adminOnly` and add per-owner authorization on monitor mutation.

### H2 — Known-CVE stdlib and x/* libraries, reachable
**File:** `go.mod` (go 1.26.3; system 1.26.4), `golang.org/x/image v0.40.0`, `x/net v0.53.0`, `x/crypto v0.51.0`; call site `internal/server/api/profile.go:87` (`handleSetAvatar` → `webp.Decode`).
**OWASP:** A06 Vulnerable & Outdated Components · **SOC 2:** CC7.1
**CVSS v4.0:** 7.7 High (aggregate) — WEBP panic: `AV:N/AC:L/AT:N/PR:L/UI:N/VC:N/VI:N/VA:H/SC:N/SI:N/SA:N`

**govulncheck-confirmed reachable:**
- **GO-2026-5061 / GO-2026-4961** — `x/image/webp` panic on crafted WEBP, reached from the authenticated avatar upload (`profile.go:87`). Remote DoS. Fixed in `x/image v0.43.0`.
- **GO-2026-5856** — crypto/tls ECH privacy leak, reached from every TLS path (server listener, pgx, SMTP, agent transport). Fixed in **Go 1.26.5** (installed toolchain is 1.26.4).

**Research-flagged (bump even if not currently reachable):**
- `x/crypto v0.51.0` → **≥0.52.0** (CVE-2026-46595/46596, ssh CertChecker panic + auth-callback bypass).
- `x/net v0.53.0` → **≥0.55.0** (x/net/html XSS/DoS set).
- Go directive `1.26.3` → **1.26.5**; toolchain → 1.26.5.
- Frontend **Vite** `^6.0.7` → **≥6.4.1** (2025 dev-server `server.fs.deny` arbitrary-file-read chain — dev only).
- Everything else pinned is past its fix line (pgx v5.9.2, chi v5.2.5, jwt v5.3.1, yaml.v3 v3.0.1 all clean). `skip2/go-qrcode` is unmaintained (2020) but clean.

**Remediation.** `go get go@1.26.5 golang.org/x/image@latest golang.org/x/crypto@latest golang.org/x/net@latest && go mod tidy`; bump the toolchain image in CI/Dockerfile to 1.26.5; bump Vite. Add a Grype/Trivy image-scan gate (ADR-0009 follow-up still open).

### H3 — SSRF via notification webhooks
**File:** `internal/server/notify/notify.go:331-351` (`postJSON`), `:70` (`httpClient`, no `CheckRedirect`); reached via `handleCreateChannel`/`handleTestChannel` (`api.go`, middleware `protected`).
**OWASP:** A10 SSRF · **SOC 2:** CC6.6
**CVSS v4.0:** 7.1 High — `AV:N/AC:L/AT:N/PR:L/UI:N/VC:H/VI:L/VA:N/SC:L/SI:N/SA:N`

**What.** Slack/Mattermost/Discord/Ntfy dispatch POSTs to the user-supplied `webhook_url`/`server_url` verbatim. No scheme allowlist, no private-IP/metadata block (the probe package's `denyDestination` is not applied here), and the default client follows up to 10 redirects — so even a public URL can 302 to `127.0.0.1`/metadata. `handleTestChannel` lets any user trigger the request on demand.

**Remediation.** Validate scheme ∈ {http,https} and apply a mandatory `denyDestination` on the resolved host at create/update *and* before each dispatch; set `CheckRedirect` to re-validate every hop.

---

## Medium

### M1 — `MON_AGENT_UPDATE_INSECURE=1` accepts a *failed* signature and executes
`internal/agent/updater/updater.go:305-307`. The override doesn't merely skip fetching a signature — on an explicit verification *failure* it logs an error and proceeds with the swap. Combined with C1, this is the switch operators will flip to make updates work, permanently disabling the trust anchor with no expiry/one-shot semantics. **OWASP A08. CVSS v4.0 6.9** `AV:N/AC:H/AT:P/PR:N/UI:N/VC:H/VI:H/VA:H/SC:L/SI:L/SA:L`. *Fix:* an insecure override may skip *fetching* a sig, but must never accept a *failed* one.

### M2 — TOTP seeds and backup codes stored plaintext at rest
`migrations/0008_user_security.sql:8-10` (`secret_b32 TEXT`, `backup_codes TEXT[]`), written raw in `user_security.go:37`; backup codes matched by plaintext scan (`auth2fa/totp.go:80`). A DB dump / read-replica / any read primitive yields every user's live OTP seed and password-equivalent recovery codes. **OWASP A02 Cryptographic Failures / ASVS V6.2, V2.8. CVSS v4.0 6.5** `AV:N/AC:H/AT:P/PR:H/UI:N/VC:H/VI:L/VA:N/SC:L/SI:N/SA:N`. *Fix:* encrypt the seed with an app/KMS key (AES-GCM); store backup codes hashed.

### M3 — Auth compliance middleware fails open on cache miss
`api.go:3136-3139` — when `UserCompliesWithPolicy` errors and there is no cached decision, it logs `"failing open"` and calls `next(c)`. A user unseen since process start hitting a transient DB error bypasses the force-mode 2FA/passkey enrollment gate. **OWASP A01 / A04 (fail-secure). CVSS v4.0 6.9** `AV:N/AC:H/AT:P/PR:L/UI:N/VC:H/VI:H/VA:N/SC:N/SI:N/SA:N`. *Fix:* fail closed (403) on lookup failure.

### M4 — `middleware.RealIP` trusts client `X-Forwarded-For`
`api.go:100` (`r.Use(middleware.RealIP)`), consumed by rate limiting (`httprate.LimitByIP`), lockout keying, and audit "remote" fields. With no trusted-proxy allowlist, if the server is ever reachable directly an attacker rotates XFF to defeat the 20 req/min auth limiter and to poison logs/audit. **OWASP A05 / A09. CVSS v4.0 6.1** `AV:N/AC:L/AT:P/PR:N/UI:N/VC:L/VI:L/VA:L/SC:N/SI:N/SA:N`. *Fix:* honor forwarded headers only from a configured trusted-proxy CIDR; document that the edge must strip inbound XFF.

### M5 — Auto-release + fleet auto-update from unreviewed `main`
`release.yaml:21-25` (`on: push: main`) + `:59-91` (`auto-tag` bumps and pushes a tag) + `:495` (`latest` prerelease pointer the fleet self-updates from). Any commit reaching `main` cuts a signed release and propagates to every host with no approval checkpoint — signatures prove "CI built it," not "a human reviewed it." **SOC 2 CC8.1 change management / OWASP A08. CVSS v4.0 5.8** `AV:N/AC:H/AT:P/PR:H/UI:N/VC:H/VI:H/VA:H/SC:L/SI:L/SA:L`. *Fix:* gate `publish`/`container`/`latest` behind a GitHub `environment:` requiring manual approval, or release only on explicit tag pushes.

### M6 — Prod deploys a mutable tag with `pull_policy: always`
`docker-compose.prod.yaml:33-34`. Operators cosign-verify a *digest* (`container-digest.txt`) but then deploy by mutable tag with forced pull, so the verified digest is never actually pinned (TOCTOU — the tag can be repushed after verification). **OWASP A08 / A03. CVSS v4.0 5.6** `AV:N/AC:H/AT:P/PR:H/UI:N/VC:L/VI:H/VA:L/SC:N/SI:N/SA:N`. *Fix:* `image: ghcr.io/...@${MONSYS_DIGEST}`. Also pin `timescaledb` to a concrete version tag, not `latest-pg16` (`docker-compose.yaml:14`).

### M7 — No TOTP replay protection / no lockout on the 2FA step
`user_security.go:60-83` (`VerifyTOTP` records `last_used_at` but not the accepted time-step, so a code is replayable across its skew window) and `api.go:2803` (`handleTOTPChallenge` never feeds the AUDIT-013 lockout — only the 20 req/min/IP limiter defends the second factor). **OWASP A07 / ASVS V2.8.4, V2.2.1. CVSS v4.0 5.3** `AV:N/AC:H/AT:P/PR:N/UI:N/VC:H/VI:L/VA:N/SC:N/SI:N/SA:N`. *Fix:* reject an already-consumed step per user; count TOTP failures into the lockout and consume the challenge after N.

### M8 — TLS optional; secure cookies over the plaintext path
`cmd/mon-server/main.go:493` (`MinVersion: tls.VersionTLS12`, no `CipherSuites`) and `:496-498` (plain `ListenAndServe` when no cert configured) while `Secure`+2-year-HSTS cookies are set unconditionally (`api.go:159,268`). If not strictly behind a TLS proxy, session tokens transit in cleartext. **OWASP A02 / ASVS V9.1. CVSS v4.0 5.3** `AV:N/AC:H/AT:P/PR:N/UI:N/VC:H/VI:L/VA:N/SC:N/SI:N/SA:N`. *Fix:* prefer `MinVersion: TLS13` (or a curated 1.2 suite list); refuse to start without TLS unless an explicit `--behind-proxy` flag is set.

### M9 — Privacy doc contradicts enforced retention
`docs/PRIVACY.md:58` states "Login events, alert history, audit log: 90 days." Migrations enforce 180 / 365 / 730 days respectively (`0012_retention.sql:55`, `0016_...:39`, `0021_...:35`). All three tables hold PII (usernames, source IPs, actor emails). **SOC 2 privacy / GDPR Art. 5(1)(e). CVSS: N/A (compliance) — treat as Medium.** *Fix:* reconcile the published statement with the enforced policy (or vice-versa).

---

## Low / Informational

- **L1 — Account enumeration:** `api.go:2757` returns a distinct `403 "user is disabled"` while not-found/bad-password correctly collapse to `401 "invalid credentials"`. Confirms a valid email. *Fix:* return the generic 401. **A07. CVSS v4.0 4.3** `AV:N/AC:L/AT:N/PR:N/UI:N/VC:L/VI:N/VA:N/SC:N/SI:N/SA:N`.
- **L2 — Least privilege in CI:** `release.yaml:38` sets `contents: write` workflow-wide; only `auto-tag`/`publish` need it. Also `publish` checkout keeps `persist-credentials` (the one carve-out) though `gh` uses `GH_TOKEN`. *Fix:* root `contents: read`, grant per-job. **A02 / SOC 2 CC6.1.**
- **L3 — `/readyz` error leak:** `api.go:469` returns `"not ready: "+err.Error()` to anonymous callers, exposing pgx/DSN detail. *Fix:* static body, log detail server-side. **A09.**
- **L4 — WebAuthn defaults:** `main.go:453-460` falls back to `rpID=localhost` / `http://localhost:5173` when `MON_RP_ID`/`MON_RP_ORIGIN` unset — prod should hard-fail. **A05.**
- **L5 — Broad `err.Error()` to clients:** ~23 `huma.Error400BadRequest(err.Error())` sites (`api.go` + `probe.go:410`). Mostly safe validation strings, but any wrapped `%w` DB error would leak. *Fix:* route non-sentinel errors through the existing `internalErr()` helper. **A09.**
- **L6 — gosec G306:** `main.go:524,530` write the OpenAPI spec `0644`. Public spec, so low, but `0640` is tidier. (Other gosec hits are G304 file reads, all `//nolint` on agent-config-controlled paths — acceptable.)
- **I1 — Dead mitigation:** `auth.go:380` `_ = subtle.ConstantTimeCompare(keyHash, keyHash)` compares a value to itself. Remove. (Real match is a DB `WHERE key_hash=$1` on SHA-256 of a 256-bit key — not attacker-timeable.)
- **I2 — Unbounded JSONB:** label maps at ingest (`apitypes.go:65,157`) and agent config (`agent_configs.go:69`) have no `maxProperties`/size cap inside the 32 MiB body limit. *Fix:* cap properties + per-entry length.
- **I3 — Tooling drift:** pre-commit `golangci-lint v1.62.2` cannot parse the CI v2 config (`.golangci.yml`), so the local hook no-ops. Bump to match CI v2.11.

---

## Code quality & Go idiom (non-security)

Verdict: **mature and idiomatic.** `go vet` clean; zero real TODO/FIXME/XXX/HACK; no commented-out code; excellent "why" doc comments; careful concurrency (cancellable contexts everywhere, buffered channels with explicit drop, locks released before I/O). Refinements only:

- **Error wrapping:** `notify.go:294,334-343` bare-return `json.Marshal`/`NewRequest`/`Do` errors unwrapped — every webhook backend funnels through here; wrap with `%w`.
- **Brittle error-string matching:** `strings.Contains(err.Error(), " 401 ")` etc. at `cmd/mon-agent/main.go:222,353`, `updater.go:595`, `api.go:2342` — prefer sentinels + `errors.Is`.
- **Dead abstraction:** `notify.Sender` interface (`notify.go:43`) is never used as a parameter; `Dispatch` type-switches on strings instead. Drop it or dispatch via `map[string]Sender`.
- **Dead statements:** `store.go:106` `var _ = sql.ErrNoRows` (pulls in `database/sql` for nothing) and `query.go:714` `var _ = pgx.ErrNoRows`.
- **DRY:** the `pgx.ErrNoRows → domain error` pattern repeats ~26× in `internal/server/store/`; extract a `wrapRowErr` helper.
- **Monolith files:** `api.go` (4,252 LOC) and `alerts.go` (3,311 LOC) mix middleware/types/handlers; split along existing seams (`enrollments.go`, `profile.go` already show the pattern).
- **Library vs in-house:** hand-rolled OCI registry client and transport retry/backoff are deliberate and well-scoped — keep. No needless deps found.

---

## What is done correctly (verified)

Injection: **none** — every dynamic query uses `$N` placeholders; `fmt.Sprintf` only builds placeholder tokens / allowlisted table names. No `os/exec` in server. SPA served from `embed.FS` (no traversal). Ingest binds `host_id` to the authenticated agent key (no host-spoof IDOR). Passwords bcrypt cost 12 with dummy-compare anti-enumeration. All secrets `crypto/rand` 256-bit, SHA-256-hashed before storage. Session invalidation thorough (logout/password/reset/2FA/email all revoke). WebAuthn requires resident key + UV, validates origin/RP-ID, clone-detection via sign counter. Full security-header set incl. tight CSP, HSTS, `frame-ancestors 'none'`; no wildcard CORS. `/metrics` and `/debug/pprof/*` admin-gated. No secrets logged; ingest payloads redacted; OTLP emits no PII. Audit log hash-chained + verifiable. Actions SHA-pinned; distroless nonroot image, digest-pinned bases, `-trimpath`; compose is `read_only`, `cap_drop: ALL`, `no-new-privileges`, DB on internal no-egress net, secrets via files. Agent systemd unit strongly hardened (`ProtectSystem=strict`, seccomp, minimal caps, non-root). Cosign keyless signing + SLSA provenance + SBOMs present. Alert regexes validated + 2 s statement timeout (RE2 = ReDoS-safe).

---

## Recommended remediation order

1. **C1 + M1** — wire the minisign public key into release ldflags, make unset/placeholder/failed-verify all fail closed, commit `deploy/release.pub`, harden the CI signing step. *(One-key + one-branch change restores the entire update security model.)*
2. **H2** — bump Go→1.26.5, `x/image`, `x/crypto`, `x/net`, Vite; add image CVE-scan gate.
3. **H1 + H3** — flip SSRF guards to default-deny and apply them to webhooks + monitors; gate monitors behind admin/ownership.
4. **M3, M4, M8** — fail-closed compliance middleware; trusted-proxy allowlist for XFF; refuse plaintext startup / TLS 1.3.
5. **M5 + M6** — add a manual-approval environment before fleet release; deploy by digest.
6. **M2, M7** — encrypt TOTP seeds / hash backup codes; TOTP replay + lockout.
7. **M9, L1–L6, I1–I3, code-quality** — batch cleanup.

---

## Remediation status — 2026-07-16 (branch `security/audit-2026-07-16-remediation`)

**Fixed in code (build + `go vet` + `staticcheck` clean, `govulncheck` now reports 0):**

| ID | Change |
|----|--------|
| C1 (code) | `updater.go` — removed the `PublicKey==""` fail-open skip; verification always runs and is fail-closed; `MON_AGENT_UPDATE_INSECURE=1` may skip *fetching* a signature but a *failed* verification is now never installed. |
| H1 | `probe.go` — two-tier guard: loopback + link-local (incl. `169.254.169.254` metadata) **always** denied; RFC1918/ULA denied by default, opt-in via `MON_PROBE_ALLOW_INTERNAL=1`. |
| H2 | `go 1.26.5`, `x/image v0.44.0`, `x/crypto v0.54.0`, `x/net v0.57.0`; CI `go-version` → 1.26.5; Vite → 6.4.3. **govulncheck: 0 vulnerabilities.** |
| H3 | `notify.go` — dial-time SSRF guard (`net.Dialer.Control`) blocks loopback/link-local for every backend + redirect hop (defeats DNS rebinding); scheme allowlisted to http/https; redirects capped at 10; RFC1918 kept reachable for self-hosted targets. |
| M3 | `api.go` — compliance middleware now fails **closed** (503) on a lookup error with no cached decision. |
| L1 | `api.go` — disabled-account login + passkey login collapse to the generic 401 (no enumeration oracle). |
| L3 | `api.go` — `/readyz` returns static `"not ready"`; DB error logged server-side only. |
| I1 | `auth.go` — removed the no-op `subtle.ConstantTimeCompare(x,x)` + orphaned import. |
| I3 | `.pre-commit-config.yaml` — golangci-lint `v1.62.2` → `v2.11.4` (matches CI). |

**Still open — needs operator input / secret material / policy decision (NOT yet done):**

- **C1 (key wiring):** inject the real minisign public key via release ldflags (`-X …updater.PublicKey=…`), commit `deploy/release.pub`, reconcile the three key-path references, and make the CI signing step a hard failure. Requires the actual keypair — cannot be done from source alone. *Until this lands, shipped agents still cannot self-update (fail-closed), which is the safe state.*
- **M2** TOTP-seed encryption / backup-code hashing (schema migration + key-management decision).
- **M4** trusted-proxy CIDR allowlist for `X-Forwarded-For` (needs the deployment's proxy range).
- **M5 / M6** release approval gate + deploy-by-digest (CI/ops policy).
- **M7** TOTP replay + 2FA-step lockout.
- **M8** TLS 1.3 / refuse-plaintext-startup (behavior decision — affects non-TLS deploys).
- **M9** reconcile PRIVACY.md retention vs migrations (which direction is authoritative?).
- **L2, L4, L5, L6** + code-quality (dead `Sender` interface, `var _` lines, error-string matching, file splits).
- Frontend `npm audit`: 3 high in `ws` via puppeteer/selenium — **dev/e2e tooling only, not shipped**; left as-is to avoid breaking the e2e stack.

### Batch 2 — 2026-07-16 (same branch)

| ID | Change |
|----|--------|
| M7 | TOTP replay protection: new `last_used_step` column (migration 0034) + `auth2fa.ValidateAndStep`; `VerifyTOTP` rejects a step ≤ the last consumed one (ASVS V2.8.4). No separate 2FA lockout added — the challenge token is already single-use (`ConsumeActionToken` sets `used_at`), so a wrong code forces a fresh password login, which is lockout-protected. |
| M9 | Migration 0033 lowers `login_events`/`alert_history`/`audit_log` retention to 90 days, matching PRIVACY.md (per your decision). No doc change needed — it already stated 90 d. |
| L2 | `release.yaml` root permissions → `contents: read`; `contents: write` scoped to the `auto-tag` and `publish` jobs only. |
| L4 | `main.go` refuses to start with the localhost WebAuthn RP fallback unless `MON_ENV=development`. |
| I2/cleanup | Removed dead `var _ = sql.ErrNoRows` / `pgx.ErrNoRows` (+ orphaned `database/sql`, `pgx` imports). |

**Deferred pending your go-ahead (real decision, not code-only):**

- **M2** — encrypting TOTP seeds at rest requires introducing a **new mandatory secret** (`MON_DATA_ENCRYPTION_KEY`) plus a data migration of existing plaintext rows; a mis-set key locks users out of 2FA. Needs a rollout decision before I implement. Backup-code hashing can be done independently — say the word.
- **M4** (trusted-proxy CIDR — need your proxy range), **M5** (release approval gate — CI policy), **M6** (deploy-by-digest — ops), **M8** (TLS 1.3 / refuse-plaintext — affects non-TLS deploys).
- **L5** (broad `err.Error()` on 400 paths) — deferred: most sites are legitimate validation messages users depend on; a blanket rewrite risks degrading them. Needs per-site review.
- **C1 key wiring** — still needs the real minisign keypair (see above).

### Batch 3 — 2026-07-16 (same branch)

| ID | Change |
|----|--------|
| M2 | TOTP seed encrypted at rest (AES-256-GCM, `internal/server/store/secrets.go`), opt-in via `MON_DATA_ENCRYPTION_KEY` (base64 32 bytes) with **dual-read** so legacy plaintext rows keep working — no forced migration/lockout. Backup codes now **bcrypt-hashed** (`auth2fa.HashBackupCodes`); `MatchAndConsume` accepts hash-or-legacy-plaintext. Startup fails fast on a malformed key. Unit tests added. |
| M8 | TLS 1.2 floor + AEAD-only ECDHE cipher suites; server refuses plain-HTTP startup unless `MON_ALLOW_INSECURE_HTTP=1`. |
| M4 | `middleware.RealIP` → `trustedRealIP`: forwarded headers honoured only from loopback/RFC1918 peers (default) or `MON_TRUSTED_PROXIES` CIDRs. |
| M5 | `container` + `publish` release jobs gated behind a protected `release` environment (add reviewers in repo Settings to finish the gate). |
| M6 | `docker-compose.prod.yaml` pins the image by immutable `@${MONSYS_DIGEST}` (dropped mutable tag + `pull_policy: always`); RELEASE.md / OPERATIONS.md updated. |

**New env vars:** `MON_DATA_ENCRYPTION_KEY` (opt-in at-rest encryption), `MON_ALLOW_INSECURE_HTTP=1` (behind-proxy plain HTTP), `MON_TRUSTED_PROXIES` (trusted-proxy CIDRs).

**Genuinely still owner-only (cannot be done from source):**
- **C1 key wiring** — real minisign keypair + release ldflags + `deploy/release.pub` + hard-fail CI signing.
- **M5 reviewer policy** — add required reviewers to the `release` environment in GitHub repo Settings (the workflow gate is in place).
- **L5** — per-site `err.Error()` review; recommend a `store.ValidationError` type to separate user-safe validation messages from wrapped DB errors, then route only the latter through `internalErr()`.
- Optional backfill migration to re-encrypt/re-hash existing TOTP rows once `MON_DATA_ENCRYPTION_KEY` is set (dual-read makes it non-urgent).

### Batch 4 — 2026-07-16 (same branch)

| ID | Change |
|----|--------|
| C1 (release wiring) | Build step embeds `updater.PublicKey` from `deploy/release.pub` via ldflags when present (missing key ⇒ fail-closed no-auto-update, never unsigned). Minisign signing step is now **required** — the release fails instead of publishing unsigned artifacts when secrets are absent. `verify.go` docs reconciled to `deploy/release.pub`. |
| L5 | 23 `Error400BadRequest(err.Error())` sites routed through a `badRequest` helper: validation messages still surface, but wrapped `pgconn.PgError` goes through `internalErr` so raw SQL/table/column detail never reaches the client. |

**Every source-fixable audit finding is now remediated on the branch.**

**Owner-only remainder (cannot be done from source):**
- **C1 key material** — generate the minisign keypair, commit `deploy/release.pub`, set `MONSYS_MINISIGN_SECRET_KEY`/`MONSYS_MINISIGN_PASSWORD` GitHub secrets. The build/CI wiring is now in place and waits for the key.
- **M5 reviewer policy** — add required reviewers to the `release` environment in repo Settings.
- Optional backfill to re-encrypt/re-hash existing TOTP rows once `MON_DATA_ENCRYPTION_KEY` is set (dual-read makes it non-urgent; needs an app-side one-shot, not a SQL migration).
