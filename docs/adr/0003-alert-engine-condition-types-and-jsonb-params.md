# ADR-0003: Alert engine with 23 condition_types + JSONB `condition_params`

- Status: Accepted
- Date: 2026-05-12
- Deciders: maintainers
- Context tags: alerting, observability, schema, security

## Context and Problem Statement

The original alert engine shipped with five condition types
(`host_offline`, `monitor_failed`, `cert_expiring`,
`login_failed_threshold`, `security_updates_pending`). They were
hand-written evaluators, each with their own params shape. That worked
when the surface was small. Then the agent grew: NIC link-state,
firewall state, fail2ban jails, CrowdSec decisions, Proxmox VM
inventory, Docker container state, image-update detection, audit-log
actions, repo-metadata age, login anomaly, and the full TimescaleDB
metric family (CPU/RAM/swap/load/disk/NIC bytes/workload metrics/
fail2ban-banned/crowdsec-active/repo-age/monitor-latency).

That's ~22 *metric kinds* alone. Naïvely modelling each one as its own
`condition_type` would mean:

- 22 enum values + 22 evaluators + 22 params structs + 22 frontend
  panes for "compare a number to a threshold over a window".
- 22 places to forget the throttle / dedupe / quiet-hours logic.
- A `condition_type` enum that drifts every time the agent learns a
  new metric.

At the same time, *state-diff* evaluators (NIC link-down,
fail2ban-jail-disappeared, firewall-state-change, container/VM state
change, unexpected reboot, inventory drift) are fundamentally
different: they compare *current snapshot* against *previous snapshot*,
and they live in process memory, not in TimescaleDB.

Forces:

- 22 numeric-metric evaluators are 90% identical code.
- Operators write rules in the UI; "scroll through 22 entries to find
  CPU%" is bad UX.
- The OpenAPI surface needs to publish what params each
  `condition_type` accepts, but we don't want every type addition to
  break the wire format.
- Operator-supplied regex (`audit_action.actor_pattern`) is a classic
  ReDoS primitive if we hand it to `regexp.MustCompile` without
  bounds.
- State-diff evaluators need a state store, and the obvious choice is
  the DB, but writing every poll into the DB is silly and the engine
  is already a single-process singleton today.

## Considered Options

1. **22 distinct `condition_type`s, one evaluator each.** Maximum
   clarity at the cost of every change touching apitypes + frontend +
   backend + spec.
2. **One generic `metric_threshold` evaluator with a `metric` enum
   inside `condition_params`,** dispatching at runtime to the per-
   metric SQL. State-diff cases stay as their own types because they
   genuinely aren't threshold rules.
3. **A DSL** ("`cpu_used_pct > 80 for 2m`") parsed server-side. Most
   expressive, dramatically more code, and a worse story for
   form-based UI.
4. **Strict-typed Protobuf-style params** with `oneof` per type. Less
   wire flexibility, much more friction adding a new sub-type.
5. **Per-rule state rows in Postgres** for state-diff evaluators
   (vs in-memory).

## Decision Outcome

Chosen: **option 2 + option 5 rejected** — one generic
`metric_threshold` for all numeric-series rules, distinct
`condition_type`s for state-diff and event rules, **in-memory state**
for state-diff and login-anomaly evaluators.

Resulting catalogue (23 total):

- One generic: `metric_threshold` (dispatches across 22
  `MetricKind`s — CPU/RAM/Swap/Load, per-mountpoint disk, per-NIC
  rate via TimescaleDB `LAG` window, per-workload, fail2ban banned,
  crowdsec active, repo metadata age, monitor latency).
- Pre-existing event/state: `host_offline`, `monitor_failed`,
  `cert_expiring`, `login_failed_threshold`,
  `security_updates_pending`.
- State-diff: `container_state_change`, `vm_state_change`,
  `nic_link_down`, `nic_bond_degraded`, `firewall_state_change`,
  `unexpected_reboot`, `inventory_drift`, `fail2ban_jail_disappeared`.
- Event: `audit_action`, `login_anomaly` (subkinds:
  `new_source_ip` / `root_success` / `sudo_spike`),
  `crowdsec_decision_threshold`, `host_flap`, `agent_outdated`,
  `image_update_pending`, `package_update_available`,
  `pending_reboot`, `repo_metadata_stale`.

`condition_params` stays as JSONB, with documentation-only Go *Params
structs (`MetricThresholdParams`, `HostFlapParams`, etc.) emitted into
the OpenAPI spec so clients see the shape. The engine still reads from
the raw map, so adding a new sub-key is backwards compatible.

Rationale:

- **22-into-1 dispatch is mechanical.** `evalMetricThreshold` reads
  `params.metric`, picks the SQL template, parameterises the
  comparator + window + for_sec + per-scope key. One ReDoS / one
  throttle / one dedupe path instead of 22.
- **State lives in process for state-diff evaluators.** The engine
  is a single-process singleton today. Writing every "host X NIC eth0
  is up" event to the DB would be busywork. F-6 / F-20 in `152b929`
  forces the in-memory maps to be bounded (100k entries, FIFO
  eviction, O(1) per-event lookup) so a runaway estate can't OOM the
  engine.
- **`evalAuditAction` gets the most untrusted input** — the operator
  pastes a regex straight into the rule form. We RE2-compile-validate
  on write, cap pattern length at 256 chars, and run the query under
  `SET LOCAL statement_timeout = '2s'` so a pathological pattern
  can't pin the backend. (F-3 in `152b929`.)
- **Fail-closed on bad params.** `loadRules` drops a rule for the
  tick if its `condition_params` won't unmarshal, with a
  `slog.Error`. The previous fall-back-to-`{}` behaviour silently
  fired `audit_action` on every event. (F-15 in `152b929`.)

### Consequences

- Positive:
  - Adding a new metric is "add a `MetricKind` constant + add a SQL
    branch". No new `condition_type`, no spec churn, no new frontend
    pane.
  - State-diff evaluators share one mutex and one bounded-map
    invariant, so the engine's memory cost is bounded and analysable.
  - `evalAuditAction` is hardened against ReDoS by design, not by
    runtime trust.
  - One throttle + dedupe + quiet-hours path inside
    `metric_threshold` means a fix lands once, applies everywhere.
- Negative:
  - Operators reading the JSONB `condition_params` directly in psql
    have to know which sub-keys apply to which `metric` — the OpenAPI
    types help, but a raw `SELECT condition_params FROM
    notification_rules` is less self-describing than 22 distinct
    columns would be.
  - In-memory state means engine restarts lose the "last seen"
    baseline. On first tick after restart, state-diff evaluators emit
    nothing — only on the *second* tick once they have a baseline. We
    accept this; alerting on restart-induced phantom diffs would be
    worse.
  - `metric_threshold` rules are slightly slower per-evaluation than
    a hard-coded evaluator (one extra map lookup, one extra branch).
    Not measurable at our cardinality.
- Follow-ups:
  - If/when the engine becomes multi-process, state moves to Redis
    or Postgres advisory locks. Tracked as future work.
  - Per-host scoping is already implemented (the `LAG`-window NIC-
    rate evaluator joins on `host_id`); cross-host correlation rules
    (e.g. "≥3 hosts critical at once") are not in scope.

## More Information

- Implementation commits:
  - `62f0bf0` feat(apitypes): condition_type enum + per-type param
    shapes — declares the 23 `Condition*` constants and 22
    `Metric*` constants. Adds 14 documentation-only *Params helpers.
  - `03e70ca` feat(alerts): 18 new condition_type evaluators —
    adds the evaluators to `internal/server/alerts/alerts.go`,
    including the `MetricKind` dispatch and the state-diff maps under
    a single mutex. `runPeriodic` calls every new evaluator after
    the existing five.
  - `152b929` security(alerts): regex DoS guard, bounded state,
    fail-closed parsing — F-3 RE2 + statement_timeout for
    `audit_action`, F-5 clamps `window_sec/for_sec/threshold_sec/
    min_age_hours` everywhere, F-6/F-20 bounds login-anomaly maps,
    F-15 drops rules with bad `condition_params` instead of
    defaulting to `{}`.

- References:
  - OWASP CWE-1333 "Inefficient Regular Expression Complexity"
    (ReDoS) — drives the RE2-compile-on-write + statement_timeout
    pair.
  - OWASP CWE-400 "Uncontrolled Resource Consumption" — drives
    the bounded in-memory state maps.
  - TimescaleDB `LAG` window for per-NIC rate calculation —
    `https://docs.timescale.com/`.
  - Postgres `SET LOCAL statement_timeout` — per-transaction
    timeout that releases on COMMIT/ROLLBACK.

- Related: ADR-0004 (rule groups built on this surface), ADR-0005
  (3-step wizard that drives the 23-type catalogue).
