# Changelog

All notable changes to MonSys are documented here. This file follows
[Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/) and
Semantic Versioning. Architectural decisions live in `docs/adr/`;
security audit findings live in `SECURITY_AUDIT_REPORT.md`.

The project does not yet cut tagged releases — every change on `main`
ships rolling via the `release (latest)` channel. Once tagging starts,
each `[X.Y.Z]` section will record the commit range.

## [Unreleased]

### Security

- Add WebAuthn passkey enrollment + discoverable login flow with admin-controlled force-mode policy (forward-ref AUDIT-201/2026-05-05; ADR-0002) (2d85086, 08f4dc0, c858b76, 5463737).
- Gate non-compliant users via a server-side `requireMethodCompliance` middleware; per-user 60-second result cache with last-known-good reuse on DB error closes the "induce DB latency, bypass enrollment gate" path (F-9, 80ae05e).
- Address every finding from the 2026-05-05 audit (29 fixes): minisign-verified self-update, semver downgrade refusal, `.prev` rollback on `try-restart` failure; OpenAPI `securitySchemes` / root `security` / `writeOnly` on secrets / `readOnly` on server-set fields / `maxLength` on ~200 string fields; `ReadHeaderTimeout=10s`; per-agent ingest quota; HSTS `preload`; audit-log hash-chain with `--verify-audit-chain` CLI; `.pre-commit-config.yaml` with gitleaks + golangci-lint; `docs/SIGNING.md` + `docs/PRIVACY.md` + `docs/TLS.md` (AUDIT-101/103/201/202/203/204/206/207/208/209/210/301/401/402/403/404/501/502/701/702/405/601, 009fb4d).
- Revoke active sessions on every credential-rotation path — password reset, admin password reset, password change (sparing the caller's token), 2FA reset/disable, email change, consume-reset (F-1/F-2/F-7/F-8, e0613da).
- Restrict `/v1/auth/consume-reset` to `password_reset` and `invite` token kinds; a leaked `email_change` / `login_2fa` / `webauthn_register` token can no longer pivot the bound user's password (F-19, near-critical close, e0613da).
- Verify avatar uploads by decoding bytes via `image.Decode` (PNG/JPEG/WebP only), derive stored content-type from the decoded format, send `X-Content-Type-Options: nosniff` on `GET /v1/users/{id}/avatar` (F-4, 80ae05e).
- WebAuthn `FinishPasskeyLogin` refuses counter rollback (`sign_count < new`); `cred.Authenticator.CloneWarning` or a no-op counter update emits `user.passkey.clone_warning` and refuses the session (F-12, F-13, e0613da).
- Reject C0/DEL control bytes in passkey names — closes terminal-injection on `mon-server --list-passkeys` (F-18, e0613da).
- Guard `audit_action` rules against regex DoS: actor/target patterns validated as RE2 + 256-char capped on write; evaluator query runs with `SET LOCAL statement_timeout = '2s'` (F-3, 152b929).
- Bound `loginNewIPSeen` at 100k entries with FIFO eviction; replace the linear per-event "have we seen any IP" scan with an O(1) `loginUserSeen[host:user]` map (F-6, F-20, 152b929).
- Drop alert rules for the tick when `condition_params` JSONB fails to unmarshal (was silently evaluating with `{}` defaults and firing `audit_action` on every event) (F-15, 152b929).
- Clamp `window_sec` / `for_sec` / `threshold_sec` / `min_age_hours` in every evaluator that reads them; `apitypes` gains `minimum`/`maximum` on `MetricThresholdParams` and `HostFlapParams` (F-5, 152b929).
- Move `/v1/auth/me/avatar` (POST/DELETE), `/v1/auth/me/language` (PUT), and `/v1/auth/me/email/request` (POST) from `openProtected` to `protected` so a non-compliant user past their grace window cannot hijack the email-change flow (F-10, 80ae05e).
- Enforce one outstanding email-change token per user via delete-before-mint; 60-second per-user cooldown on `RequestEmailChange` (F-11, 80ae05e).
- Run `Store.SetPassword` / `Store.SetPasswordByAdmin` through the active password policy; CLI `--reset-password` gains explicit `--force-weak-password` escape hatch that logs `slog.Warn` (F-17, 80ae05e).
- Refuse a security-policy change that would lock the calling admin out the instant it persists (force_mode != off, grace_days = 0, caller not yet compliant) via new `Store.UserCompliesWithPolicyKind` (F-20, 80ae05e).
- Emit `user.passkey.login.failed` audit rows on `ValidateDiscoverableLogin` error and on the user-disabled branch; new `truncate200` helper caps audit detail size (F-14, 80ae05e).
- Agent updater removes `mon-agent.prev` snapshot on the happy path; rollback path preserves it for hand-roll-forward (F-4.3.6, 14a5bb6).
- Mirror the forward path's `EXDEV` `copyReplace` fallback in the rollback path so a spool on tmpfs can roll back across filesystem boundaries (F-4.3.7, 14a5bb6).
- Refuse retries on permanent TLS/DNS errors (`tls.CertificateVerificationError`, `x509.UnknownAuthorityError`, `x509.CertificateInvalidError`, `x509.HostnameError`, `net.DNSError` with `IsNotFound`) (F-4.3.11, 14a5bb6).
- `buffer.Append` fsyncs the spool directory after atomic rename, closing the "durable file inside a non-durable directory entry" window on ext4 with non-default mount options (F-4.3.13, 14a5bb6).
- Spool tmp file opens with `O_EXCL` so a name collision between two agents pointed at the same dir fails loudly instead of silently truncating a peer's write (F-4.3.15, 14a5bb6).
- Insert `fail2ban-client` jail argument after `--` to block flag-injection via hostile jail names beginning with `-` (F-4.3.2, 14a5bb6).
- Per-release CycloneDX SBOM via `anchore/sbom-action@v0.17.9` — one per binary (mon-server + mon-agent, per linux/amd64 + linux/arm64) plus one for the source tree, attached to both tag and rolling releases (F-4.3.1.7, 91550d0).
- Signed multi-arch container image at `ghcr.io/maltekiefer/monsys-server:<tag>` via keyless cosign OIDC; `deploy/docker-compose.prod.yaml` overlay pulls the signed image with `pull_policy: always` (F-4.3.1.8, 91550d0).
- Harden TimescaleDB compose service: `security_opt: no-new-privileges:true`, `cap_drop: [ALL]`, scoped `cap_add` for Postgres' minimum (F-4.3.1.9, 91550d0).
- Close spec-drift gate's version-field bypass: `--print-spec` / `--dump-openapi` force `version.Version = "dev"` so ldflag stamping can no longer hide a stale committed spec (F-4.3.1.11, 91550d0).
- Pin `govulncheck@v1.3.0`, `gosec@v2.26.1`, `minisign@v0.11` (SHA256-pinned upstream tarball), `actions/checkout` and friends to SHAs, `go-version: '1.26.2'` in every job (F-4.3.1.12/13/14, 91550d0).
- Lower gosec severity floor from high to medium; preserve `G115/G123/G402/G703` rationale block (F-4.3.1.12, 91550d0).
- Add workflow concurrency control (release: cancel-in-progress false; CI: cancel-in-progress true) and `persist-credentials: false` on every checkout except the publish job (F-4.3.1.17, F-4.3.1.18, 91550d0).
- CI commit-signing gate with `docs/COMMIT-SIGNING.md` and advisory pre-commit hook (da612c7).
- Nightly Go fuzz workflow exercising the 9 fuzz targets (0b1c808).
- Loopback-bind, digest-pinned, TLS-overlay deploy stack with reproducible build flags (d9461bf).
- Pin every GitHub Actions reference to a commit SHA (1d66214).
- `SECURITY.md`, `CONTRIBUTING.md`, Dependabot config (b961b7a).
- `RequireAdmin` defence-in-depth guard on admin routes (1323117).
- Make `/v1/rules*` admin-only — drop privesc via foreign channel UUIDs (ffd8b6d).
- Server hardening: body cap, per-IP rate-limit on auth endpoints, security headers (CSP, HSTS, X-Frame-Options), generic 500 wrapper, account lockout, SSRF guard, error-detail leak fix (5be1f87).
- Agent TLS pin, spool dir chmod, gosec nolint annotations (0c3abf3).
- Opt-in PII redaction for shells, homes, source IPs — both server-side on ingest and agent-side at collection (0ab19a7, 6f6275c).
- DB retention policies + cardinality caps so unbounded growth from a runaway agent can no longer fill disk (3c3b901).
- Map `ErrUserLockedOut` to HTTP 429 in the login handler (was 401) (db9336b).
- Tighten `.gitignore` to keep coverage / snapshot / AppImage / openapi-tooling-cache artefacts out of the repo (acf9046).

### Added

- 23 alert `condition_type` evaluators including a single `metric_threshold` that dispatches across 22 metric kinds (CPU, memory, swap, load, disk usage, disk rate/util, NIC rate, NIC errors, drift, etc.) (03e70ca, 62f0bf0; ADR-0003).
- Rule groups — many conditions under one user-visible name — with an atomic `replace_existing_ids` endpoint (e44d309, 86ed1a9; ADR-0004).
- Three-step rule creation wizard (Detect / Scope / Notify) with live preview (821b45e; ADR-0005).
- Click-through `RuleForm` panes for every `condition_type` plus an Expert JSON escape hatch (4d46782).
- Multi-condition rule wizard + grouped rules display (c7b83eb).
- Per-rule repeat reminders and `notify_on_resolve` opt-out (fc5964c).
- Rule scoping by host / tag / group; CrowdSec parser fix (8657de6).
- Resolved-notifications + `alert_state` tracking (d50c537).
- Live audit log + `/admin/audit` screen (71c763a).
- Server-side global quiet hours; admin updates audited (21a155e, 19e5f17).
- WebAuthn registration + discoverable login endpoints; admin security-policy GET/PUT; force-mode gate (c858b76).
- WebAuthn store, session hardening, policy schema foundation (2d85086, 08f4dc0).
- Profile: avatar upload + delete with magic-byte verification (ef29540, 24894d5).
- Verified two-step email change (token to new address, all sessions revoked on confirm) (ef29540).
- DE/EN language switcher with browser-default + user-override; TopBar selector (b984dd3; ADR-0006).
- Sidebar shell with user card and language switcher (24894d5).
- Admin Users 3-dot kebab menu: reset-password (mail-only / show-once URL), reset-2FA, revoke-sessions, lock/unlock, delete (6c7bcba, 2065d36).
- Mail-only admin password reset that withholds the URL on mail success; per-user revoke-sessions endpoint (2065d36).
- CLI `--reset-password` for admin password recovery (5c61365).
- CLI recovery flags: `--disable-totp`, `--list-passkeys`, `--delete-all-passkeys`, `--get-security-policy`, `--set-security-policy`, `--revoke-all-sessions`, `--change-email` (c7fc5dd, ef29540).
- CLI `--verify-audit-chain` for offline audit-chain verification (009fb4d).
- CLI `--force-weak-password` escape hatch on `--reset-password` (80ae05e).
- 10 Architecture Decision Records under `docs/adr/` covering bearer-token auth, passkeys + force mode, alert condition_types + JSONB, rule groups, three-step wizard, i18n, signed self-updating agent, OpenAPI source-of-truth + drift gate, distroless signed multi-arch image, and user-facing security primitives (57f7393).
- Go fuzz harness with 9 targets (965aa45).
- `.golangci.yml` baseline (reduced 187 → 132 findings) and `eslint.config.js` baseline (reduced 895 → 186 warnings) wired into CI gates (965aa45, 645e09b, a9f41ea).
- Proxmox node detection with `qm` / `pct` VM discovery and bridge/bond NIC members (e51f628).
- `pve-firewall` status collector for Proxmox nodes (696c5e3).
- Pending OS updates column on the hosts list (b51689d).
- Docker image-update detection (b51689d).
- Service detection covers ~100 popular self-hosted workloads; detection from installed packages with styled overflow tooltip (edb8e5b, 910d2ba).
- Hosts overview caps service badges at 3 with hover `+N` for the full list (d1d4383).
- Self-service agent enrollment with one-shot install command (10e0568).
- Admin enrollments page + modal polish + display label + per-host alert filter (8d12110).
- Embed self-update unit + timer in the rendered installer; QR install code as a separate toggle (418bccc, 7ef5491).
- Agent self-update + startup self-heal + `GET /v1/agents/latest-version` (5505be0; ADR-0007).
- Agent `auto_update` opt-out and `safeexec /usr/local/bin` allow-list (16e672b).
- Connection-lost banner (a7e398b).
- Dark/light theme toggle (70a5808).
- Hosts list — search, status filter, keyboard-accessible rows (03191cb).
- SMTP readiness probe surfaced to the UI; non-admin alert visibility; inline JSON validation in admin agent-config (138cde2).
- Per-user notification channels CRUD + dispatch; Discord backend (0d587f8, da44643).
- Split outbound mail into global SMTP (alerts + invites) + per-user email channels (2527927).
- Live charts + global packages search + design refresh (8dde859).
- Host detail page + sub-resource endpoints (a5bd14c).
- Host tags + groups + service/distro icons + admin dropdown (1ab2b0e).
- Web-managed agent configuration (M19, 00ab7a9).
- Dashboard redo + host detail tabs (M18, 51165aa).
- Raw agent ingest payload capture; agent register + ingest summary logs; server log ring buffer + admin logs page (M17/a/b/c, 9290cdb, 9b9e253, ce9e7fe).
- Last-admin protection on user deletion (M16, 8ecd99a).
- Active monitors (cert + DB + http + tcp) (M8c, 844cd6b).
- Host liveness watcher + status field (M8b, f391014).
- Notification rules engine + alert history (M8d, 2530f76).
- Notification channels CRUD + dispatch (M8a, 0d587f8).
- Web user auth + session-protected read APIs (M7, bc255c7).
- Profile, admin users, security policy, invite reset (M10 frontend, 6a3a205).
- Profile + 2FA + admin user mgmt + password policy (M10 backend, 80fcc7d).
- Frontend scaffold (Vite + React + TS + Tailwind) + login + hosts list (M9a, d343e59).
- Persistence + read APIs (hosts list, system metrics range) (M3, 4fedf47).
- Agent core + bootstrap registration (M2, ed7608e).
- Package inventory collector + persistence (M5, e883874).
- Docker workload collector + `safeexec` helper (M4, 14faa23).
- Virt + identity + security collectors (M6, 3ae9f82).
- DB schema + OpenAPI scaffold (huma + chi) (M1, fbf91ba; ADR-0008).
- Agent persists NIC IPv4 + IPv6 addresses (d6d3cc0).
- Audit retention policy, quiet-hours CHECKs, SMTP `from_address` CHECK (2145759).
- Full install walkthrough + MIT LICENSE + agent config samples (1617777).

### Changed

- Full UI/UX overhaul: sidebar shell, page template, splits, polish (e4837fe).
- Tabs introduced across every admin page; profile gains tabs (b96f1bb).
- Consolidate `/admin/logs`, `/admin/ingests`, `/admin/audit` into a single `/admin/logs` page with tabs (b96f1bb).
- Stepper promoted to shared `ui` primitives (b96f1bb).
- Agent refactored per-package-manager: `packages_apt.go`, `packages_dnf.go`, `packages_pacman.go`, `packages_apk.go` (ad2e597).
- Agent refactored per-security-backend: `security_ufw.go`, `security_nft.go`, `security_iptables.go`, `security_pve.go`, `security_fail2ban.go`, `security_crowdsec.go` (ad2e597).
- Apply `//go:build linux` tags throughout the agent; structured `slog` components; documented surface (ad2e597).
- Frontend unified onto shared `ui` primitives (0364da7, ab0dfc7).
- Rename project to MonSys; redesigned rule + channel forms (6109bc8).
- Go module renamed to `github.com/MalteKiefer/MonSys`; commit-msg hook installed (e2e59b9).
- SMTP encryption mode is a mutually exclusive radio; confirm before wiping a stored password (90eed2c).
- Wire global SMTP into the alert engine and invite mail (85568a3).
- Sidebar + TopBar redesign with user card and language switcher (24894d5, b984dd3).
- Regenerate `openapi.yaml` + TypeScript types for rule groups, new condition_types, and WebAuthn endpoints (86ed1a9, 59efacd, 5baeeda).
- Distroless signed multi-arch container image is the deploy default (ADR-0009).
- OpenAPI spec is source of truth, regenerated and drift-gated in CI (ADR-0008).
- Bearer-token session auth — no cookies (ADR-0001).
- Drop `yq` from spec-drift pipeline; use `--print-spec` directly (376ec63).

### Fixed

- Agent: security-update detection for apt + dnf was silently 0 (7105285).
- `ListHosts`: corrected package summary table name to `metrics_packages_summary` (was `package_summary`) (5996d86).
- TopBar language preference now persists server-side (POST → PUT mismatch fixed) (ca44746).
- Rule wizard: atomic group replace via `replace_existing_ids` no longer leaves orphans on UUID collision; per-row names are collision-safe (e72c8a3).
- Close 79 ESLint async-handling errors in the SPA (423d2b9).
- A11y: DropdownMenu / Stepper roles, URL-synced tabs, plural i18n keys, TopBar strings (86607fa).
- A11y: ThemeToggle outline + AdminAudit pagination labels (e792c7c).
- A11y: admin menu + header-h CSS var (3c3c98e).
- Alerts engine correctness: throttle default, drop failed dispatches from dedup, NULL-guard liveness (c798d61).
- Alerts: resolve-retry, ctx propagation, quiet-hours, throttle prune, `ErrSmtp` wrap, JSON warn (80eac28).
- Alerts: `metricDiskRate` / `metricDiskUtil` / `metricNICRate` add `AND v >= prev_v` so counter resets from a reboot elide the sample pair instead of flipping `bool_and` to FALSE and suppressing real breaches (F-4 correctness, 152b929).
- Alerts: cache `fetchHostScope` results for 60s — alert storms over 500 hosts no longer issue 500 round-trips per tick (F-17, 152b929).
- Alerts: `loadQuietConfig` releases `quietMu` around the DB round-trip so slow Postgres no longer serialises every `fire()` / `resolve()` through one mutex (F-19, 152b929).
- RuleForm state-reset on key change; Tabs keyboard nav; SMTP cache invalidation (8e8752a).
- Order chi middleware before humachi route registration (3f8fb17).
- Coerce nil Labels map to `'{}'` on register to satisfy NOT NULL (804348b).
- Bound bootstrap call + null-safe host arrays in UI (2a7c4ed).
- Null-safe firewalls in SecurityPanel for freshly registered hosts (f8eb3c3).
- Enrollments: `used_at` pointer so JSON omits the zero value (c338e8a).
- CI gosec: exclude opt-in / safe rules; QR + cleanup (52f66cd).
- Agent updater retries with a fresh manifest on SHA mismatch (d465beb).
- Install retries agent update resolver with `fresh=true` on cache miss (a56342d).
- Banner sticky-in-flow + lazy admin pages + online refetch (23790dd).
- Drop `ProcSubset=pid` and `WatchdogSec` from `mon-agent.service` (5ee67c0).
- DB hardening: audit retention, quiet-hours CHECKs, SMTP `from_address` CHECK, GC contention (2145759).
- Warn instead of swallow on corrupt channel/rule JSONB (b308ef4).
- Housekeeping reaper bounds previously unbounded tables and in-memory state (f39d4b7).
- Agent-config preview no longer fires on dropdown open (794b80b).
- AdminAgentConfig: clarify upsert semantics (83968d0).
- Profile.tsx onto shared `ui` primitives — light-theme correct (ab0dfc7).

### Removed

- `/admin/ingests` and `/admin/audit` as separate routes — folded into `/admin/logs` tabs (b96f1bb).
- `yq` from the spec-drift pipeline (376ec63).

### Notes

Intentionally omitted from this changelog because they were pure
internal refactors / docs / test-only / merge-of-already-listed work
with no separate user-visible effect:

- `ee9b827` (close active TODOs across server CLI + web theme — cleanup-only).
- `01462b2` (refresh web lockfile after devDep add — tooling).
- `74fd7c9` (enrich enrollment audit trail + `hostDisplay` sweep — already covered by 8d12110).
- `b03a709` (install docs — `chgrp monagent` on CrowdSec config — doc-only).
- `b25785e`, `3efd5bf`, `9557979`, `167e584`, `49f425e`, `398bf81`, `571f39a`, `8b122a8`, `d5b7d9e`, `a0f753b`, `acae031`, `737b74a`, `26984c7`, `1d09e15`, `c4a89f3`, `5442f9b`, `b05bbc3`, `d05e7c3`, `0364da7`, `17794e8`, `ecf2cfd`, `1d20fa9`, `508928a` (wave-merge commits — their content is listed under the underlying feature/fix commits).
- `576ad3e`, `acae031` (embedded SPA dist sync after wave merges — build artefact only).
- `53c4eba` M0 project skeleton — pre-history.

[Unreleased]: https://github.com/MalteKiefer/MonSys/commits/main
