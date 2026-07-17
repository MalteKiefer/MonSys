# Mail-Stack Monitoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Detect a host's mail stack (postfix/dovecot/rspamd/greylist) from the agent and surface status + alerts in MonSys.

**Architecture:** A new agent-local collector emits an optional `MailReport` in the existing ingest snapshot (mirrors `SecurityReport`). The server stores latest status + a queue-depth time series, exposes them on a host-detail `Mail` tab, and adds a `mail_service_down` alert condition plus queue `metric_threshold` plumbing.

**Tech Stack:** Go 1.26.5 (agent + server), pgx/TimescaleDB, goose migrations, huma OpenAPI, React 19 + Vite + Tailwind.

## Global Constraints

- Go directive `go 1.26.5`; keep CI `go-version` in lockstep.
- Agent is hardened (NoNewPrivileges, seccomp, `CAP_DAC_READ_SEARCH`, user `monagent`). `safeexec` is the ONLY sanctioned exec path (fixed SafePath, scrubbed env, argv-only).
- Ingest body cap 32 MiB; all new string fields length-capped, numeric fields non-negative.
- The `Mail` field is `omitempty` and nil on non-mail hosts — the tab and alerts must stay dormant when absent.
- Every commit SSH-signed (repo `commit.gpgsign=true`); no AI/Claude attribution in messages.
- OpenAPI spec is source-of-truth; regenerate + commit when the API surface changes (spec-drift CI gate).

---

## File structure

- `internal/shared/apitypes/apitypes.go` — add `Mail *MailReport` to `IngestRequest` + `MailReport`/`MailService`/`PostfixQueue`/`RspamdStat`/`MailPortCheck` types (+ `mail_service_down` in the condition enum).
- `internal/agent/collector/mail/mail.go` — collector `Source` (detect + assemble report).
- `internal/agent/collector/mail/{queue.go,rspamd.go,ports.go,systemd.go}` — signal helpers (+ `*_test.go`).
- `internal/agent/config/config.go` — `Mail` config block.
- `internal/agent/agent.go` — register the collector in `buildCollectors`.
- `internal/server/store/migrations/0035_mail_status.sql` — `host_mail_status` + `metrics_mail`.
- `internal/server/store/mail.go` — `SaveMailReport`, `MailStatus`, `MailQueueSeries`.
- `internal/server/api/api.go` (or new `mail.go`) — call `SaveMailReport` in the ingest handler; add `GET /v1/hosts/{id}/mail`.
- `internal/server/alerts/alerts.go` — `mail_service_down` evaluation + queue metric feed.
- `web/src/pages/host-detail/Mail.tsx` + `HostLayout.tsx`/`index.ts` — tab.
- `web/src/i18n/locales/{en,de}/mail.json` — strings.
- `api/openapi.yaml` — regenerated.

---

# PHASE 1 — collection, storage, status tab

### Task 1: Shared `MailReport` types

**Files:**
- Modify: `internal/shared/apitypes/apitypes.go`
- Test: `internal/shared/apitypes/mail_test.go`

**Interfaces:**
- Produces: `apitypes.MailReport` and `IngestRequest.Mail *MailReport`, consumed by every later task.

- [ ] **Step 1: Write the failing test**
```go
package apitypes

import ("encoding/json"; "testing"; "time")

func TestMailReportRoundTrip(t *testing.T) {
	in := IngestRequest{Mail: &MailReport{
		Time:     time.Unix(1_700_000_000, 0).UTC(),
		Services: []MailService{{Name: "postfix", Active: true, SubState: "running"}},
		Queue:    &PostfixQueue{Active: 1, Deferred: 4, Hold: 0, Incoming: 0, Total: 5},
		Rspamd:   &RspamdStat{Reachable: true, Scanned: 100, Rejected: 3, Greylisted: 7, Learned: 2},
		Ports:    []MailPortCheck{{Port: 993, Proto: "imap", Open: true, TLS: true}},
	}}
	b, err := json.Marshal(in)
	if err != nil { t.Fatal(err) }
	var out IngestRequest
	if err := json.Unmarshal(b, &out); err != nil { t.Fatal(err) }
	if out.Mail == nil || out.Mail.Queue.Total != 5 || out.Mail.Rspamd.Greylisted != 7 {
		t.Fatalf("round trip lost data: %+v", out.Mail)
	}
}
```
- [ ] **Step 2: Run — expect FAIL** (`go test ./internal/shared/apitypes/ -run TestMailReport`) — undefined types.
- [ ] **Step 3: Add types** (after `SecurityReport`), and add `Mail *MailReport \`json:"mail,omitempty" doc:"Mail stack status (postfix/dovecot/rspamd)"\`` to `IngestRequest`:
```go
type MailReport struct {
	Time     time.Time       `json:"time"`
	Services []MailService   `json:"services,omitempty"`
	Queue    *PostfixQueue   `json:"queue,omitempty"`
	Rspamd   *RspamdStat     `json:"rspamd,omitempty"`
	Ports    []MailPortCheck `json:"ports,omitempty"`
}
type MailService struct {
	Name     string `json:"name"      maxLength:"64"`
	Active   bool   `json:"active"`
	SubState string `json:"sub_state,omitempty" maxLength:"32"`
}
type PostfixQueue struct {
	Active, Deferred, Hold, Incoming, Total int `json:"active"`
}
type RspamdStat struct {
	Reachable  bool `json:"reachable"`
	Scanned    int64 `json:"scanned"`
	Rejected   int64 `json:"rejected"`
	Greylisted int64 `json:"greylisted"`
	Learned    int64 `json:"learned"`
}
type MailPortCheck struct {
	Port         int        `json:"port"`
	Proto        string     `json:"proto" maxLength:"16"`
	Open         bool       `json:"open"`
	TLS          bool       `json:"tls"`
	CertNotAfter *time.Time `json:"cert_not_after,omitempty"`
	CertTrusted  bool       `json:"cert_trusted"` // false = self-signed/untrusted (expiry still read)
}
```
(Fix the `PostfixQueue` json tags to one per field: `active/deferred/hold/incoming/total`.)
- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit** `feat(apitypes): mail report types in ingest snapshot`.

### Task 2: Agent config block

**Files:** Modify `internal/agent/config/config.go`; Test `internal/agent/config/config_mail_test.go`
**Interfaces:** Produces `config.Config.Mail` (fields `Enabled *bool`, `RspamdStatURL string`) + accessor `MailEnabled() bool` (default true).

- [ ] **Step 1:** Test: unset `mail` block → `MailEnabled()==true`, `RspamdStatURL()=="http://127.0.0.1:11334/stat"`; `mail.enabled: false` → false.
- [ ] **Step 2:** Run — FAIL.
- [ ] **Step 3:** Add `Mail MailConfig` to `Config`, mirror the existing optional-block pattern (see `AutoUpdateEnabled()`), default URL constant.
- [ ] **Step 4:** Run — PASS.
- [ ] **Step 5:** Commit `feat(agent): mail collector config`.

### Task 3: systemd helper (`safeexec systemctl`)

**Files:** Create `internal/agent/collector/mail/systemd.go`; Test `.../systemd_test.go`
**Interfaces:** Produces `func serviceState(ctx, run execFn, unit string) (present bool, active bool, sub string)` where `execFn func(ctx, name string, args ...string) ([]byte, error)` (injected so tests use a fake; production passes `safeexec.Run`).

- [ ] **Step 1:** Test with a fake `execFn` returning `LoadState=loaded\nActiveState=active\nSubState=running` → present/active/"running"; `LoadState=not-found` → present=false.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3:** Implement parsing of `systemctl show -p LoadState,ActiveState,SubState <unit>` output.
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(agent/mail): systemd unit state helper`.

### Task 4: Postfix queue counter

**Files:** Create `internal/agent/collector/mail/queue.go`; Test `.../queue_test.go`
**Interfaces:** Produces `func postfixQueue(spoolRoot string) *apitypes.PostfixQueue` (nil if spoolRoot absent).

- [ ] **Step 1:** Test builds a temp dir `active/`(1 file) `deferred/`(4 files, incl. a nested subdir since postfix hashes queue dirs) `hold/`(0) → `{Active:1,Deferred:4,Hold:0,Total:5}`; missing root → nil.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3:** Implement: walk each of `active,deferred,hold,incoming` counting regular files recursively (postfix uses hashed subdirs); sum `Total`.
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(agent/mail): postfix queue depth via spool file count`.

### Task 5: rspamd `/stat` client

**Files:** Create `internal/agent/collector/mail/rspamd.go`; Test `.../rspamd_test.go`
**Interfaces:** Produces `func rspamdStat(ctx, client *http.Client, url string) *apitypes.RspamdStat` (Reachable=false on any error).

- [ ] **Step 1:** Test with `httptest.Server` returning `{"scanned":100,"actions":{"reject":3,"greylist":7},"learned":2}` → Reachable, Scanned 100, Rejected 3, Greylisted 7; server 500 → `{Reachable:false}`.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3:** Implement GET + JSON decode (tolerant struct), bounded body (64 KiB) + 5 s timeout.
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(agent/mail): rspamd stat client`.

### Task 6: local port + TLS probe

**Files:** Create `internal/agent/collector/mail/ports.go`; Test `.../ports_test.go`
**Interfaces:** Produces `func checkPort(ctx, host string, p portSpec) apitypes.MailPortCheck` where `portSpec{Port int; Proto string; TLS bool}`.

- [ ] **Step 1:** Test against a `net.Listen` TCP server (Open=true) and a closed port (Open=false); a `tls.NewListener` with a self-signed cert → TLS=true, `CertNotAfter` set.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3:** Implement: `net.DialTimeout` (2 s) for open. For TLS ports, handshake with `ServerName` = host and record `ConnectionState().PeerCertificates[0].NotAfter`. Do NOT blanket-disable verification: handshake with default verification first; on a verification error retry once with `InsecureSkipVerify` **solely to read the leaf `NotAfter`** and set `CertTrusted=false` on the `MailPortCheck` (add this bool in Task 1) so the UI shows "self-signed/untrusted" instead of silently ignoring it. Localhost expiry probe only — the connection carries no data. Carry a `//nolint:gosec // localhost cert-expiry read, not a trust decision; CertTrusted surfaced` note.
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(agent/mail): local port + TLS expiry probe`.

### Task 7: mail collector assembly + detection gate

**Files:** Create `internal/agent/collector/mail/mail.go`; Test `.../mail_test.go`; Modify `internal/agent/agent.go`
**Interfaces:**
- Consumes: Tasks 3–6 helpers, `config.Config`.
- Produces: `mail.New(cfg config.Config) collector.Source`; `Collect` sets `batch.Mail` or leaves it nil.

- [ ] **Step 1:** Test: injected fakes where no unit is present → after `Collect`, `batch.Mail == nil`. With postfix present → `batch.Mail.Services` contains postfix, queue/ports populated for detected components only.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3:** Implement `Collect`: probe units {postfix,dovecot,rspamd,postgrey}; if none present return nil (no report). Else assemble `MailReport` (queue only if postfix present; rspamd stat only if rspamd present; ports scoped to detected services). Set `batch.Mail`. Register in `buildCollectors`: `if cfg.MailEnabled() { collectors = append(collectors, mail.New(cfg)) }`.
- [ ] **Step 4:** PASS + `go build ./... && GOOS=linux go build ./...`.
- [ ] **Step 5:** Commit `feat(agent): mail-stack collector (gated on detection)`.

### Task 8: migration 0035

**Files:** Create `internal/server/store/migrations/0035_mail_status.sql`
**Interfaces:** Produces tables `host_mail_status(host_id uuid pk, updated_at timestamptz, report jsonb)` and `metrics_mail(time timestamptz, host_id uuid, queue_active/deferred/hold/total int, rspamd_greylisted/rejected bigint)` hypertable + 90d retention.

- [ ] **Step 1:** Write the up/down SQL (goose format; `create_hypertable('metrics_mail','time')`; `add_retention_policy('metrics_mail', INTERVAL '90 days', if_not_exists=>TRUE)`; index on `(host_id, time DESC)`). Down drops both.
- [ ] **Step 2:** Run `make test-migrations` — expect the round-trip (Up→Down→Up) to pass.
- [ ] **Step 3:** Commit `feat(db): mail status + metrics_mail (migration 0035)`.

### Task 9: store `SaveMailReport` / `MailStatus` / `MailQueueSeries`

**Files:** Create `internal/server/store/mail.go`; Test `internal/server/store/mail_test.go`
**Interfaces:**
- Produces: `func (s *Store) SaveMailReport(ctx, hostID uuid.UUID, r apitypes.MailReport) error`; `func (s *Store) MailStatus(ctx, hostID uuid.UUID) (apitypes.MailReport, bool, error)`; `func (s *Store) MailQueueSeries(ctx, hostID uuid.UUID, since time.Time) ([]MailQueuePoint, error)`.

- [ ] **Step 1:** Test (testcontainers): insert a host, `SaveMailReport`, then `MailStatus` returns it (found=true) and a `metrics_mail` row exists; unknown host → found=false.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3:** Implement: upsert `host_mail_status` (JSONB marshal of report) + insert `metrics_mail` row from `r.Queue`/`r.Rspamd`. `MailStatus` selects + unmarshals.
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(store): persist + read mail status`.

### Task 10: ingest wiring + `GET /v1/hosts/{id}/mail`

**Files:** Modify `internal/server/api/api.go` (ingest handler + new operation); Test `internal/server/api/mail_test.go`; regenerate `api/openapi.yaml`
**Interfaces:**
- Consumes: `SaveMailReport`, `MailStatus`.
- Produces: endpoint returning `{detected bool, report MailReport}` (admin/owner gated like other host endpoints).

- [ ] **Step 1:** Test: POST an ingest with a `Mail` payload for an enrolled host → 200; `GET /v1/hosts/{id}/mail` returns `detected=true` with the report. Host without mail → `detected=false`.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3:** In the ingest handler, after the existing `SaveIngest`, add `if in.Body.Mail != nil { _ = s.Store.SaveMailReport(ctx, hostID, *in.Body.Mail) }` (best-effort, logged on error). Register the huma GET operation mirroring an existing host-detail read.
- [ ] **Step 4:** PASS + `make generate-spec` (commit spec).
- [ ] **Step 5:** Commit `feat(api): ingest mail report + host mail endpoint`.

### Task 11: host-detail Mail tab

**Files:** Create `web/src/pages/host-detail/Mail.tsx`; Modify `HostLayout.tsx`, `index.ts`; Create `web/src/i18n/locales/{en,de}/mail.json`; Test `web/src/pages/host-detail/Mail.test.tsx`
**Interfaces:** Consumes `GET /v1/hosts/{id}/mail` (typed via generated api-types).

- [ ] **Step 1:** Test (vitest + RTL): given a mocked report, renders service pills, queue counts, rspamd stats, port/TLS rows; given `detected=false`, the tab link is absent.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3:** Implement the tab (follow `Security.tsx` structure), gate its nav entry on `detected`, wire i18n.
- [ ] **Step 4:** `cd web && npm run lint && npm run build && npx vitest run Mail` — PASS.
- [ ] **Step 5:** Commit `feat(web): host-detail mail status tab`.

---

# PHASE 2 — alerting

### Task 12: queue `metric_threshold` plumbing

**Files:** Modify `internal/server/alerts/alerts.go`; Test `internal/server/alerts/alerts_test.go`
**Interfaces:** Consumes `metrics_mail`. Produces recognition of metric names `mail_queue_deferred`, `mail_queue_total` in the metric-threshold evaluator.

- [ ] **Step 1:** Test: a `metric_threshold` rule on `mail_queue_deferred > 10` fires when the latest `metrics_mail` row exceeds it; dedup `metric_threshold:<host>`.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3:** Extend the metric-threshold query/lookup to resolve the `mail_queue_*` metric names against `metrics_mail` (follow the existing metric lookup switch).
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(alerts): mail queue metric-threshold`.

### Task 13: `mail_service_down` condition

**Files:** Modify `internal/server/alerts/alerts.go`, `internal/shared/apitypes/apitypes.go` (condition enum); Test `internal/server/alerts/alerts_test.go`
**Interfaces:** Consumes `host_mail_status`. Produces condition `mail_service_down` firing per down service (dedup `mail_service_down:<host>:<service>`), recovery when back active.

- [ ] **Step 1:** Test: seed a `host_mail_status` with postfix `active=false` under a matching rule → fires once; flipping to active → resolve.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3:** Add the condition to the periodic evaluator (mirror `security_updates`): scan `host_mail_status`, for each service `active=false` call `fire(...)` with subject/body naming the host+service; add `"mail_service_down"` to the enum + `splitDedupKey` host-scope list.
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(alerts): mail_service_down condition`.

### Task 14: suggested-rule UX + cert expiry

**Files:** Modify `web/src/pages/notifications/RuleWizard/catalogue.ts`, `web/src/pages/host-detail/Mail.tsx`; Test `web/.../catalogue.test.ts`
**Interfaces:** Consumes the new condition + queue metrics.

- [ ] **Step 1:** Test: the wizard catalogue lists `mail_service_down` and mail queue metrics; the Mail tab's "Suggested alerts" links pre-fill the wizard (queue > N, service down, cert < 14 d using the existing `cert_expiring` path on `Ports[].cert_not_after`).
- [ ] **Step 2:** FAIL.
- [ ] **Step 3:** Add catalogue entries + i18n; add the "Suggested alerts" affordance to `Mail.tsx`.
- [ ] **Step 4:** `npm run lint && npm run build && npx vitest run` — PASS.
- [ ] **Step 5:** Commit `feat(web): mail suggested-alert rules`.

### Task 15: end-to-end verification + docs

**Files:** Modify `README.md`/`docs/OPERATIONS.md` (mention mail monitoring + `mail.*` agent config); regenerate spec if needed.
- [ ] **Step 1:** `go build ./... && GOOS=linux go build ./... && go vet ./... && go test ./... && (cd web && npm run build)`; `govulncheck ./...`.
- [ ] **Step 2:** Document the feature + config keys.
- [ ] **Step 3:** Commit `docs: mail-stack monitoring`.

---

## Self-review

- **Spec coverage:** detection gate (T7) ✓; up/down (T3,T13) ✓; queue depth (T4,T9,T12) ✓; rspamd/greylist (T5,T9) ✓; port/TLS (T6) ✓; storage+retention (T8,T9) ✓; API (T10) ✓; Mail tab (T11) ✓; alerts (T12,T13) ✓; suggested rules + cert expiry (T14) ✓.
- **Placeholders:** none — each step has concrete code/commands.
- **Type consistency:** `MailReport`/`PostfixQueue`/`RspamdStat`/`MailPortCheck` names identical across T1/T7/T9/T10/T11; `SaveMailReport`/`MailStatus` signatures fixed in T9 and consumed unchanged in T10.
