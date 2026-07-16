# HTML alert emails with host context — design

**Date:** 2026-07-16
**Status:** approved (design)

## Problem

Alert emails are plain text built per rule type via `fmt.Sprintf`. They are hard
to scan and identify the host only by UUID (e.g. `Host f1360f46-… sustained
ram_used_pct > 80.00`). Operators want a readable HTML email that names the host
and shows enough context to triage without opening the dashboard.

## Goals

- Render a clean, responsive HTML email for the SMTP channel.
- Resolve and show the **hostname** (not the UUID) plus host facts: IP, OS/kernel,
  arch/CPU/RAM, agent version, status + last-seen, uptime.
- Keep a plain-text alternative (accessibility, non-HTML clients).
- Leave Slack/Mattermost/Discord/ntfy unchanged (they already use plain/markdown).
- No new required config. Optional `MON_PUBLIC_URL` adds a "View host" button.

## Non-goals

- Redesigning non-email channels.
- Per-rule bespoke templates — one template driven by structured fields.
- Charts/images in the email (keep it lightweight and MIME-simple).

## Design

### 1. Structured message fields (`internal/server/notify`)
Extend `notify.Message` with optional fields; `Subject`/`Body` stay as the
canonical plain-text form used by every channel:

```
Severity   string              // existing
FiredAt    time.Time           // optional
RuleName   string              // optional
Metric     *MetricDetail       // optional: Name, Current, Threshold
Host       *HostContext        // optional: Name, IP, OS, Kernel, Arch, CPUCores, RAMBytes, AgentVersion, Status, LastSeen, UptimeSec
HostURL    string              // optional deep link (only when MON_PUBLIC_URL set)
```
When `Host`/`Metric` are nil, the HTML simply omits those blocks. This keeps every
existing caller valid (they set only Subject/Body/Severity today).

### 2. Enrichment at the single choke point (`internal/server/alerts`)
All alerts pass through `Engine.fire(ctx, r, subject, body, dedup)`. Enrich there:
- `splitDedupKey(dedup)` already yields the host UUID for host-scoped rules.
- New `store` method `HostContext(ctx, hostID) (apitypes.HostContext, error)` returns
  the facts from the `hosts` table (+ latest status / uptime). One query, read-only.
- Populate `Message.Host` when a host id is present; leave nil otherwise
  (monitor-only / global rules render without a host block).
- `HostURL` built from `MON_PUBLIC_URL` + `/hosts/<id>` when the env var is set.
- The `Metric` block is populated for the `metric_threshold` condition type, which
  already has name/value/threshold in scope; other types leave it nil.

### 3. HTML rendering (`internal/server/notify`)
- New `email_html.go`: a `html/template` (auto-escaping) producing an
  email-safe, table-based, inline-styled document. Severity → accent color
  (critical `#b91c1c`, warning `#e8a23a`, info `#3aa3e8`).
- `SMTP.Send` builds a `multipart/alternative` message: part 1 `text/plain`
  (existing `m.Body`), part 2 `text/html` (rendered template). `buildRFC5322`
  grows a multipart variant; the existing plain-only helper stays for callers
  that pass no HTML.
- All dynamic values flow through `html/template`, so host/rule strings are
  auto-escaped (no injection from hostnames/labels).

### 4. Error handling
- HTML render failure falls back to the plain-text-only email (never drops the
  alert). Logged at warn.
- `HostContext` lookup failure → send without the host block (best-effort
  enrichment must not block delivery).

## Testing
- `notify`: unit test that `SMTP` message building yields a `multipart/alternative`
  with both parts and that the HTML escapes a hostile hostname.
- `notify`: template renders with `Host == nil` / `Metric == nil` (no panic, blocks omitted).
- `alerts`: `HostContext` populated maps into `Message.Host` (existing engine tests
  extended or a focused test on the mapping helper).

## Rollout
- Backward compatible; no migration. Ships in the normal release.
- `MON_PUBLIC_URL` documented in OPERATIONS.md as optional (enables the deep link).
