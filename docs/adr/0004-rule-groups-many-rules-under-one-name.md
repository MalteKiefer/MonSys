# ADR-0004: Rule groups — many rules under one name

- Status: Accepted
- Date: 2026-05-12
- Deciders: maintainers
- Context tags: alerting, schema, ux

## Context and Problem Statement

ADR-0003 lays out a flat `notification_rules` table where each row is
one `condition_type` with one `condition_params` JSONB plus a shared
scope (hosts/tags/groups) and notify config (channels, severity,
throttle, etc.). That model works fine for "one rule, one trigger".
It falls apart when an operator wants to express:

> "On the `prod-db` group, alert me when **CPU > 80% for 2m** OR
> **RAM > 90% for 1m** OR **disk on /var > 90%**, all to the same
> on-call channel with the same severity."

That's three rows in the legacy model. Three rows means:

- Three names (`prod-db cpu`, `prod-db ram`, `prod-db disk`) — bound
  by the `UNIQUE(name)` constraint.
- Three places to update the scope when you add a host.
- Three rows to keep enabled / disabled together. The UI showed three
  rows; the user mental model was "one rule with three legs".
- A copy-edit nightmare on the rule list page.

Forces:

- We don't want to break the legacy single-rule API — operators have
  scripts that hit `POST /v1/notifications/rules`, and the CLI uses
  it.
- We don't want to invent a graph table either ("rule_groups (id,
  name); rule_group_members (group_id, rule_id)") because that adds
  joins to every alert-engine tick.
- The frontend wizard (ADR-0005) wants to render "the conditions of
  this group" as one entity with N legs.
- The unique-name constraint is load-bearing — operators rely on it
  for idempotent CLI updates by name.

## Considered Options

1. **New `rule_groups` table + `notification_rules.group_id` FK.**
   Cleanest relational shape; adds a JOIN to the engine's
   `loadRules`.
2. **Nullable `group_id UUID` column on the existing
   `notification_rules` table.** Same shape, no JOIN; a "group" is
   just N rows that happen to share a UUID. No new table.
3. **Embed conditions as a JSONB array on a single row.** One row
   per group. Breaks the evaluator (which works one-condition-per-row)
   and the spec.
4. **A side table for groups with a single FK column and no
   metadata.** Same as option 1 but the side table only carries
   `(id)`. Pure pointer.

## Decision Outcome

Chosen: **option 2** — nullable `group_id` on the existing
`notification_rules` table.

Plus: **a single atomic `replace_existing_ids` batch endpoint** so the
frontend can save an entire group in one transaction.

Plus: **per-row name suffix with a collision counter** so
`UNIQUE(name)` keeps working when two legs in one group share a
`condition_type`.

Rationale:

- **Migration 0030 is a single `ALTER TABLE` + partial index** — no
  data migration, no FK cycles. A legacy single-rule row has
  `group_id IS NULL`; a group of N rows shares one `group_id` UUID.
  Engine code doesn't change: `loadRules` reads every row exactly
  like before.
- **The list endpoint can fold groups in the SPA.** `RulesPage`
  groups by `group_id` and renders one card with N bullets. Server
  stays simple.
- **The batch endpoint solves the consistency problem.** Without it,
  the wizard had to do N DELETEs followed by one POST sequentially.
  A collision on the POST left the user with their old rules already
  gone (the DELETEs had succeeded) and a 400 on screen — and the
  retry then 404'd on the DELETEs because the rows were gone.
  `replace_existing_ids` does the DELETEs *and* the inserts in one
  transaction so a collision rolls everything back.
- **Per-row name suffix solves `UNIQUE(name)` without dropping the
  constraint.** When a group has N=1 condition we use the raw name.
  When N>1 we append ` — <condition_type>`, and when two legs share
  a condition_type we add a 1-based index: `"name — metric_threshold
  1"`, `"name — metric_threshold 2"`. The frontend strips the suffix
  on edit (`initialDraft` in `RuleForm.tsx`) so users never see it
  unless they go straight to psql.

### Consequences

- Positive:
  - One name, one severity, one channel set, one scope, N legs.
    Matches the operator's mental model.
  - Atomic save: a failed group-save leaves the *previous* group
    intact. No half-state.
  - Engine code is unchanged. The 23-condition_type catalogue (ADR-
    0003) keeps working row-by-row.
  - Audit trail: `rule.group.create` captures `name` +
    `condition_count` + `channel_ids` count — one log entry per
    user action, not N.
- Negative:
  - The `notification_rules.name` column is no longer a true human
    identifier when `group_id IS NOT NULL`. We accept the suffix
    leakage in psql / API responses; the SPA strips it.
  - Operators that hit the legacy single-rule endpoint and don't know
    about groups can accidentally pick a name that collides with a
    suffixed group leg. The UNIQUE constraint still catches this
    cleanly with a 409.
  - There's no "convert this legacy single rule into a group with one
    leg" migration path other than "delete it and save as a group".
    Acceptable: groups are an opt-in shape.
- Follow-ups:
  - If we ever want per-leg per-severity (one rule, leg A is
    warning, leg B is critical), we'd add a `severity` column to
    `notification_rules` and the engine emits per-leg. Not in scope
    today — the wizard insists on one severity per group.
  - The name-suffix scheme is opaque to a sufficiently determined
    psql user. We could move per-leg names into `condition_params`
    as a sub-key, but the schema-as-source-of-truth wins for now.

## More Information

- Implementation commits:
  - `e44d309` feat(rules): rule groups — N conditions under one
    name — migration 0030 adds nullable `group_id` UUID + partial
    index. `NotificationRule` + `NotificationRuleInput` gain
    `GroupID *uuid.UUID`. New `POST /v1/notifications/rules/batch`
    (adminOnly) backed by `Store.CreateRuleGroup` in one tx. Audit
    row `rule.group.create` captures name + condition_count +
    channel_ids count.
  - `e72c8a3` fix(rules): atomic group replace + collision-safe
    per-row names — `CreateRuleGroup` builds per-row names up
    front, counting `condition_type` occurrences and appending a
    1-based index when a type repeats. New optional field
    `replace_existing_ids` on the batch input atomically DELETEs
    the listed ids BEFORE the inserts run, all inside the same tx
    so a later collision rolls everything back. Frontend strips the
    suffix from initial.name when the rule belongs to a group.
  - `c7b83eb` feat(web): multi-condition rule wizard + grouped
    rules display — `RulesPage` folds rows by `group_id` into one
    card with leg bullets and group-wide Edit/Enable/Delete
    actions. The wizard Save mutation hands sibling ids to the
    batch endpoint via `replace_existing_ids` instead of looping
    N+1 round-trips.
  - `86ed1a9` chore(spec): regenerate openapi + ts types for rule
    groups.

- References:
  - PostgreSQL partial index on
    `notification_rules (group_id) WHERE group_id IS NOT NULL` —
    keeps the index small for the common single-rule case.
  - https://www.postgresql.org/docs/current/sql-altertable.html —
    why a nullable column is a near-instant migration.

- Related: ADR-0003 (the condition catalogue these groups compose
  over), ADR-0005 (the wizard that drives the batch endpoint),
  ADR-0008 (OpenAPI gate ensures the new shape is wire-checked).
