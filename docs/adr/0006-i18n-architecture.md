# ADR-0006: i18n architecture — react-i18next + 11 namespaces +
browser-default + server-override

- Status: Accepted
- Date: 2026-05-12
- Deciders: maintainers
- Context tags: i18n, ui, schema

## Context and Problem Statement

MonSys started English-only with strings inlined in JSX. As the SPA
grew (admin pages, profile, dashboard, hosts, packages, notifications,
host detail, logs, auth flows, common UI primitives) we needed:

1. A second language (German is the operator-language for our largest
   homelab/SMB user-base) without forking the codebase.
2. Concurrent development on multiple pages without merge conflicts
   on every string addition. We use parallel-agent code generation;
   one giant `en.json` is a guaranteed conflict surface.
3. A user preference that persists across devices — operators switch
   between desk + mobile + on-call laptop and don't want to re-pick
   the language each time.
4. A sane default for an unconfigured browser ("Auto") that isn't a
   missing-value-treated-as-English landmine.

Forces:

- react-i18next is the idiomatic React i18n library; the alternative
  is `react-intl` (FormatJS), which is heavier on the bundle and
  uses ICU MessageFormat by default — overkill for the depth of
  string interpolation we have.
- Namespacing prevents merge conflicts only if the split is along
  the lines where contributors actually work. "common/buttons" vs
  "common/forms" is the wrong cut — pages are.
- A `users.language` column has to encode "use the browser default"
  *as a value*, not as `NULL`. `NULL` means "no preference" but
  cohabits awkwardly with the CHECK constraint that enforces the
  whitelist.

## Considered Options

1. **react-i18next + 11 page-area namespaces** (common, nav, auth,
   dashboard, hosts, packages, profile, admin, notifications,
   hostDetail, ui), browser-default detection,
   `users.language TEXT NOT NULL DEFAULT 'auto'` with CHECK
   `auto|en|de`.
2. **react-i18next + flat namespace** (one big translations JSON per
   language). Simpler but merge-conflict-prone.
3. **react-intl / FormatJS.** ICU MessageFormat. Heavier bundle.
4. **Hand-rolled `t()` over a map.** Smallest dep surface, no
   detection, no plurals, no nested keys, no contributor tooling.
5. **`users.language NULL`-means-auto.** Saves an enum value but
   breaks the CHECK constraint or forces an awkward `IS NULL OR IN
   ('en','de')` pattern.

## Decision Outcome

Chosen: **option 1** — react-i18next + 11 namespaces split by page-
area + browser-default detector + server-stored `users.language`
column with `'auto'` as a real value.

Rationale:

- **Namespace-per-page-area is the merge-conflict-free cut.**
  Parallel agents working on the admin page edit `admin.json`; agents
  on hosts edit `hosts.json`. The `common` and `ui` namespaces hold
  shared strings (buttons, validation errors, empty-state text) and
  are touched rarely. Cross-namespace lookup is one line:
  `t("ns:key")`.
- **Browser-default with localStorage cache.** `i18next-browser-
  languagedetector` reads `localStorage["mon-lang"] →
  navigator.language` cascade. First load resolves to `en` or `de`
  based on the browser. The detector is the runtime authority when
  the stored preference is `'auto'`.
- **`'auto'` as a stored value, not absence.** `users.language TEXT
  NOT NULL DEFAULT 'auto'` with `CHECK (language IN ('auto', 'en',
  'de'))`. This gives us three honest states: "I haven't picked",
  "I picked English", "I picked German". `NULL` would force every
  read to nullable-handle and the CHECK constraint to a disjunction.
  `'auto'` is no-op on the client: the detector stays authoritative.
- **Cross-device persistence via `users.language` column.** The SPA
  calls `PUT /v1/auth/me/language` after a user toggles the
  switcher; the server validates the enum (defence-in-depth
  alongside the pg CHECK), writes the column, audits
  `user.language.set`. On `/me` refresh the SPA pulls
  `me.data.language` and calls `i18n.changeLanguage(en|de)`; an
  `'auto'` value is a no-op so the detector wins. This lets a user
  switch on desktop and have mobile pick it up on next login.
- **TypeScript-augmented `useT`.** `i18next.d.ts` declares the
  namespace resource shapes so `t("admin:users.delete.button")` is
  a compile error if the key doesn't exist — keys are now part of
  the type system, not stringly-typed.

### Consequences

- Positive:
  - Eleven JSON files instead of one. Parallel work on different
    pages doesn't conflict on the locale layer.
  - "Auto" is a real choice the user can pick *back to*, not just
    "I never set anything".
  - The language preference follows the user across devices on
    next `/me` refresh.
  - Compile-time key validation catches typos before runtime.
  - TopBar `LanguageSwitcher` is one place to switch and one place
    to render the current language — between avatar and theme
    toggle.
- Negative:
  - 11 namespaces means 11 import paths if you wire it naïvely.
    The `useT()` typed wrapper takes a namespace argument so
    components only mention one namespace.
  - Adding a third language (e.g. French) is 11 JSON files, not 1.
    Acceptable — it's still bounded work.
  - The "language was meant to persist but didn't" bug class is
    real and we hit it twice in one rollout. `ca44746` documents
    two pieces:
      1. The route is registered as `PUT` and the SPA was posting
         `POST` → 405 swallowed by a `.catch`.
      2. The "Auto" choice serialised as `{language: ""}` but the
         enum accepts only `{auto, en, de}` — the empty string was
         rejected by the CHECK constraint silently from the SPA's
         POV.
      3. App.tsx had no effect to feed `user.language` back into
         `i18n.changeLanguage` on `/me` refresh, so a second
         device stayed on `navigator.language`.
    All three are now closed; the audit was useful.
- Follow-ups:
  - Plural / interpolation forms are handled by i18next out of the
    box (`{{count}}` and ICU-style plural keys); `86607fa` fixed
    a couple of places that hardcoded "1 item" / "N items" string
    concatenation.
  - RTL is not in scope today; would require Tailwind direction-
    aware utilities everywhere.

## More Information

- Implementation commits:
  - `b984dd3` feat(i18n): full DE/EN, browser default, user
    override, TopBar selector — migration 0032 adds
    `users.language TEXT NOT NULL DEFAULT 'auto'` (CHECK
    auto|en|de). `CurrentUser.language` flows through
    `/v1/auth/me`; new `PUT /v1/auth/me/language` under
    `openProtected` (later reclassified to `protected` in `80ae05e`)
    persists the preference, audited `user.language.set`.
    `Store.SetLanguage` validates the enum defence-in-depth.
    react-i18next + i18next + browser-language-detector installed.
    11 namespaces, `useT()` typed wrapper, `i18next.d.ts` augments
    react-i18next so `t("…")` errors on unknown keys. TopBar
    `LanguageSwitcher` between avatar and ThemeToggle: Auto /
    English / Deutsch.
  - `ca44746` fix(i18n): language preference actually persists
    server-side — `POST` → `PUT`; serialise Auto as `"auto"` not
    `""`; App.tsx `useEffect` on `me.data.language` calls
    `i18n.changeLanguage`.
  - `86607fa` fix(web): DropdownMenu/Stepper a11y, URL-synced
    tabs, plural i18n, TopBar strings — closes a handful of i18n
    pluralisation gaps and string leaks.

- References:
  - https://www.i18next.com — react-i18next bindings.
  - https://www.i18next.com/principles/namespaces — namespace
    rationale.
  - PostgreSQL CHECK constraint — defence-in-depth for the enum.

- Related: ADR-0010 (the `users.language` column is part of the
  user-profile surface).
