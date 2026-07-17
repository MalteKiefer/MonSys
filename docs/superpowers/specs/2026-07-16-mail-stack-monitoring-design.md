# Mail-stack monitoring (postfix / dovecot / rspamd / greylist) — design

**Date:** 2026-07-16
**Status:** approved (design); scope = Phase 1 + Phase 2

## Problem

Hosts running a mail stack (postfix, dovecot, rspamd, greylisting) have no
first-class visibility in MonSys. Operators want the agent to detect the stack
and surface status + make key signals alertable, the same way disks/workloads
are handled today.

## Goals

- Auto-detect the mail stack on a host (agent-local); surface nothing when absent.
- Status: service up/down, postfix queue depth, rspamd/greylist stats,
  local port + TLS-cert reachability.
- A host-detail **Mail** tab, shown only when detected.
- Alerting: queue depth and service-down are alertable; when the stack is
  detected the UI suggests pre-filled rules.
- Fit the hardened agent (NoNewPrivileges, seccomp, `CAP_DAC_READ_SEARCH`,
  `monagent` user). `safeexec` is permitted for service state.

## Non-goals

- Server→host active probes (explicitly agent-local per decision).
- Parsing per-domain mail logs / per-user mailbox stats.
- Configuring the mail stack; MonSys only observes.

## Approach

Agent-local collection, shipped in the existing ingest snapshot, stored and
surfaced like the `Security` report. Two build phases.

### Agent — new collector `internal/agent/collector/mail`

Implements `collector.Source`; populates `batch.Mail *apitypes.MailReport`.
Emits `nil` (no report) when no mail component is detected, so non-mail hosts
are unaffected and the UI tab stays hidden.

Detection: presence of any of the systemd units `postfix.service`,
`dovecot.service`, `rspamd.service` (+ `postgrey.service` / rspamd greylist
module). Unit presence via `safeexec systemctl show -p LoadState,ActiveState <unit>`.

Signals (prefer exec-free; `safeexec` only where it is clearly simplest):
- **Service up/down** — `systemctl is-active` (via `safeexec`, allowed) per
  detected unit.
- **Postfix queue depth** — count regular files under
  `/var/spool/postfix/{active,deferred,hold,incoming}` (readable via
  `CAP_DAC_READ_SEARCH`; no `postqueue`, no setgid). Report per-queue counts + total.
- **rspamd / greylist** — HTTP `GET http://127.0.0.1:11334/stat` (controller);
  parse `scanned`, `actions.reject`, `actions.greylist`, `learned`. Port/host
  configurable (`mail.rspamd_stat_url`), default localhost:11334. No exec.
- **Port / TLS (local)** — dial `127.0.0.1:{25,587,465,143,993,110,995}` (only
  the ports whose service is detected); record open/closed, and for TLS ports
  (465/993/995 + STARTTLS on 25/587/143) do a handshake and record leaf-cert
  `NotAfter`. Timeouts bounded; failures are recorded, never fatal.

New config block (`internal/agent/config`): `mail.enabled` (default: auto =
emit when detected), `mail.rspamd_stat_url`, optional port overrides.

### Shared types (`internal/shared/apitypes`)

```
IngestRequest.Mail *MailReport `json:"mail,omitempty"`

MailReport {
  Services []MailService   // name, active(bool), sub_state
  Queue    *PostfixQueue    // active, deferred, hold, incoming, total (ints)
  Rspamd   *RspamdStat      // reachable(bool), scanned, rejected, greylisted, learned
  Ports    []MailPortCheck  // port, proto(smtp/imap/pop3/submission), open(bool), tls(bool), cert_not_after(*time)
}
```
All numeric fields bounded/validated; string fields length-capped (matches the
project's ingest hardening).

### Server — storage (`internal/server/store` + migration)

- Migration `0035_mail_status.sql`:
  - `host_mail_status` (host_id PK, updated_at, JSONB `report`) — latest snapshot
    for the Mail tab (upserted per ingest).
  - `metrics_mail` hypertable (time, host_id, queue_active/deferred/hold/total,
    rspamd_greylisted, rspamd_rejected) with a retention policy consistent with
    other metrics tables (30–90 d) so queue depth is a time series for charts +
    `metric_threshold` alerts.
- `SaveMailReport(ctx, hostID, report)` upserts status + inserts a `metrics_mail`
  row. Called from the ingest handler when `req.Mail != nil`.
- `MailStatus(ctx, hostID)` reads the latest report for the API/host-detail.

### Server — API

- Extend the host-detail response (or a dedicated `GET /v1/hosts/{id}/mail`,
  admin/owner gated like other host endpoints) to return the latest `MailReport`
  and whether the stack is detected.
- OpenAPI kept in sync (spec-drift gate); huma-typed structs.

### Server — alerting (Phase 2)

- Queue depth: exposed as metric names (`mail_queue_deferred`,
  `mail_queue_total`) so the existing `metric_threshold` condition works with no
  engine change beyond metric plumbing.
- New condition type `mail_service_down`: the alert engine checks the latest
  `host_mail_status`; fires when a detected service reports `active=false`
  (dedup `mail_service_down:<host_id>:<service>`), honouring throttle/quiet-hours
  like other conditions. Added to the condition enum + wizard catalogue.
- Cert expiry on mail TLS ports reuses the existing `cert_expiring` style check
  against `Ports[].cert_not_after`.

### Web UI (`web/src/pages/host-detail`)

- New `Mail.tsx` tab, registered in `HostLayout`/`index.ts`, shown only when the
  host's mail report is present. Panels: services (up/down pills), queue
  (active/deferred/hold/total + sparkline from `metrics_mail`), rspamd
  (scanned/greylisted/rejected), ports/TLS (open + cert-expiry countdown).
- When detected, a "Suggested alerts" affordance pre-fills the rule wizard
  (queue > N, service down, cert < 14 d).
- i18n keys added under a new `mail` namespace (en + de), matching the repo's
  i18n structure.

## Error handling

- Any probe/parse failure degrades to a recorded "unknown"/absent field; the
  collector never blocks the ingest tick or crashes.
- Server enrichment/alert lookups are best-effort; a missing mail status never
  blocks ingest or other alerts.

## Testing

- Agent: table tests for the parsers (queue file counting against a temp spool
  tree; rspamd `/stat` JSON fixture; TLS handshake against a local test
  listener; systemd state via a fake `safeexec`). Detection gate: no units → nil.
- Shared: JSON round-trip + validation bounds.
- Store: `SaveMailReport`/`MailStatus` against the testcontainers Postgres;
  migration round-trip (0035) in the existing migration test.
- Alerts: `mail_service_down` fires on active=false and respects throttle;
  `metric_threshold` on `mail_queue_deferred`.
- Web: component render with/without a mail report (tab hidden when absent).

## Rollout

- Backward compatible; non-mail hosts emit no `Mail` field. Ships in the normal
  release; agents pick it up via signed self-update. `metrics_mail` retention
  set in the migration.

## Phasing

- **Phase 1**: agent collector + shared types + storage/migration + API + Mail
  tab (status only).
- **Phase 2**: `mail_service_down` condition + queue metric-threshold plumbing +
  suggested-rule UX + cert-expiry alert.
