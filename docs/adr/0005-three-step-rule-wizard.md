# ADR-0005: 3-step wizard for rule creation

- Status: Accepted
- Date: 2026-05-12
- Deciders: maintainers
- Context tags: ui, ux, alerting

## Context and Problem Statement

The legacy rule-creation form was one tall page with every field shown
at once: name, condition_type select, free-form `condition_params`
textarea (JSON), scope (hosts/tags/groups), channels, severity,
throttle, repeat-interval, enabled toggle. With the 23-type catalogue
(ADR-0003) and rule groups (ADR-0004), this form had three problems:

1. **Cognitive load.** A new user staring at "type:
   `crowdsec_decision_threshold`" and an empty JSON box has no idea
   what to put in. They needed example shapes per type.
2. **Hand-edited JSON is a footgun.** Operators routinely posted
   `condition_params` with the wrong types (string `"80"` instead of
   number `80`), wrong keys, or trailing-comma JSON. The engine then
   either fell back to `{}` defaults (which silently fired audit_action
   on every event — fixed in `152b929`) or 400'd the save.
3. **Multi-condition groups (ADR-0004) need an "add another leg" UX
   that doesn't fit on a single page.** A list of inline editors
   stacked vertically looked like a form's worth of forms.

Forces:

- We want the form to teach users what each condition_type *does* and
  what params it accepts.
- Power users still need raw JSON access ("Expert" mode) because the
  typed pane will always lag a new sub-key.
- Live preview is essential to close the loop ("Will this alert fire
  when X?").
- We do *not* want to take on a UI dependency for "stepper" alone —
  our component library is hand-rolled on top of Radix primitives and
  Tailwind; adding @-some-vendor/stepper for one form would be 200KB
  of bundle for a couple hundred lines of logic.

## Considered Options

1. **Keep the single-page form, add per-type typed panes** instead of
   raw JSON. Done first as a stepping stone (`4d46782`).
2. **Pull a stepper library** (e.g. `react-stepper-horizontal`,
   `material-ui-stepper`). Cheap to bolt on, but couples us to a
   library with its own a11y and theming quirks and a bundle cost.
3. **Build our own `Stepper` primitive** on top of our existing
   Radix-based UI kit, tailored to MonSys's 3-step + live-preview
   layout.
4. **Wizard as a modal vs as a full page.** Modal saves nav state,
   page gives more room for the LivePreview pane.

## Decision Outcome

Chosen: **option 3 + page (not modal)** — a hand-rolled
`Stepper` primitive driving a 3-step / Detect → Scope → Notify
wizard on a dedicated page, with a sticky `LivePreview` pane on the
right of every step. Per-condition typed panes from option 1
incorporated into Step 1.

Step layout:

- **Step 1 — Detect.** Pick one of six condition categories
  (Metrics / Availability / Updates / Security / Workloads /
  Inventory), each card filtered to its relevant `condition_type`s.
  Selecting a type reveals the typed pane (metric/comparator/
  value/window/for + scope inputs for `metric_threshold`;
  `days_threshold` for `cert_expiring`; subkind selects for
  `inventory_drift` / `firewall_state_change` /
  `vm_state_change` / `login_anomaly`; comma-separated actions +
  regex inputs for `audit_action`; toggle chips for container
  states; etc.). An **Expert (JSON)** toggle in the header swaps the
  typed pane for a raw `condition_params` editor with on-keystroke
  JSON validation. Step 1 holds an array of conditions; "Add another
  condition" reopens the inline editor and appends a leg.
- **Step 2 — Scope.** Pick targeting mode (all / tags / groups /
  hosts) with searchable multi-selects.
- **Step 3 — Notify.** Name, channels, severity pills
  (info/warning/critical), throttle, repeat-interval, enabled
  toggle.
- **Sticky LivePreview on every step.** Composes a human-readable
  sentence ("Alert when CPU usage > 80% for 2 minutes on any host,
  send to ops-mail with severity warning") plus a foldable JSON
  disclosure. With multi-leg groups it switches to "Alert when ANY
  of: …" with bullets.

Rationale:

- **Stepper as a primitive lives in `web/src/components/ui/`.** It
  promotes to a shared UI kit eventually (an `86607fa` follow-up
  moved Stepper into the shared `ui` folder with a11y fixes).
  Bundle cost is the few hundred lines we'd write anyway.
- **Typed panes + Expert toggle is the right tradeoff.** Users see
  the right fields for the type they picked; power users still get
  the JSON exit hatch. The Expert pane validates on every keystroke
  and a final `JSON.parse → stringify` happens on submit so we
  never POST a malformed `condition_params`.
- **LivePreview closes the "what does this rule do?" loop.** It runs
  the same humanisation we use elsewhere; users see immediately that
  ">" + "80" + "for 2m" maps to the English sentence they expected.
- **Page, not modal.** Three steps + a 320px-wide sticky preview
  pane doesn't fit comfortably in a modal. Page is the right shape;
  the user gets a `?step=N` URL we can deep-link and the browser back-
  button works.
- **Build-it-ourselves on Stepper.** A stepper is ~150 LOC including
  ARIA. Pulling a library would bring a peer-dep on a UI kit we
  don't otherwise use, plus theming churn whenever we change
  Tailwind tokens. Not worth it for one form.

### Consequences

- Positive:
  - New users learn the rule model from the wizard's vocabulary
    (Detect/Scope/Notify), not from a JSON cheat-sheet.
  - LivePreview catches type errors *before* the save: a user
    seeing "CPU > $threshold for $window" knows they haven't filled
    in the fields.
  - Expert (JSON) toggle preserves the power-user path.
  - Multi-condition groups are first-class: "Add another condition"
    is a button, not a "create a sibling rule with the same name"
    workaround.
  - Stepper primitive is reusable (already used by the wizard,
    candidate for future install-flow / migration UIs).
- Negative:
  - More TS / TSX than the old single-page form. Initial commit
    `821b45e` split the wizard into a `RuleWizard/` folder
    (catalogue, coerce, draft, Stepper, MultiSelectList, StepDetect,
    StepScope, StepNotify, LivePreview).
  - The typed-pane dispatch (17 sub-components) is one switch
    statement to maintain when the catalogue grows. Mitigated by
    the catalogue.ts central registry.
  - The Stepper is ours, so a11y bugs are ours. We caught one in
    `86607fa` (DropdownMenu/Stepper a11y, URL-synced tabs) — a
    library wouldn't have shipped that bug, but it would have
    shipped its own.
- Follow-ups:
  - Wizard step deep-linking via `?step=N` URL — partially in
    `86607fa`'s URL-synced tabs work.
  - Per-leg severity (see ADR-0004 follow-up) would add a step or
    fold severity into the leg pane.
  - i18n covered separately in ADR-0006 — the wizard strings are
    in the `notifications` namespace.

## More Information

- Implementation commits:
  - `4d46782` feat(web): RuleForm clicky-bunti panes for every
    condition_type + Expert JSON — first step. 17 per-condition-
    type components, on-keystroke JSON validation, final
    `JSON.parse → stringify` sanity pass on submit.
  - `821b45e` feat(web): rule creation wizard — 3-step Detect/
    Scope/Notify with live preview — splits into `RuleWizard/`
    folder; Stepper primitive; LivePreview composes humanised
    sentence + foldable JSON disclosure.
  - `c7b83eb` feat(web): multi-condition rule wizard + grouped
    rules display — Step 1 holds an array; LivePreview switches
    between empty-state / single-line / bulleted "Alert when ANY
    of:".
  - `86607fa` fix(web): DropdownMenu/Stepper a11y, URL-synced
    tabs, plural i18n, TopBar strings — pulls Stepper into
    shared UI, fixes a11y.

- References:
  - WAI-ARIA Authoring Practices — Wizard / Stepper pattern.
  - https://react.dev/reference/react/useReducer — used for the
    wizard's `draft` state machine across steps.

- Related: ADR-0003 (condition catalogue the wizard renders), ADR-
  0004 (rule groups the wizard composes), ADR-0006 (i18n
  namespace the wizard strings live in).
