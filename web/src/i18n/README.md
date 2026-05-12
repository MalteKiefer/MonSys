# i18n conventions

We use [react-i18next](https://react.i18next.com/) with bundled JSON resources. Two locales: `en` (fallback) and `de`.

## Architecture

- `index.ts` initialises the singleton, registers every namespace, configures
  the language detector (localStorage → navigator), and exports `setLocale()`.
- `useT.ts` is a thin wrapper around `useTranslation` — page code does
  `const { t } = useT("hosts")` (or `useT(["hosts", "common"])`).
- `i18next.d.ts` augments `react-i18next`'s `CustomTypeOptions` so `t("…")`
  returns `string` (not `string | object`) and key paths are typed.
- `main.tsx` imports `./i18n` at the very top so the singleton is initialised
  before any component renders.

## Namespaces (one JSON per page area)

Splitting per page area means many parallel agents can extract strings without
fighting over the same file:

| Namespace      | Owns                                              |
| -------------- | ------------------------------------------------- |
| `common`       | Shared actions, status badges, error labels, time |
| `nav`          | Sidebar, topbar, command palette                  |
| `auth`         | Login / reset / confirm-email                     |
| `dashboard`    | Dashboard screen                                  |
| `hosts`        | Hosts list                                        |
| `packages`     | Packages screen                                   |
| `profile`      | Profile screen                                    |
| `admin`        | All admin pages (sub-keyed inside)                |
| `notifications`| Notification rules, channels, alerts              |
| `hostDetail`   | Every `/hosts/:id/*` sub-route                    |
| `ui`           | Primitives (Empty state, Skeleton labels, etc.)   |

## Key naming

- Snake-cased dot path: `account.email.title`, `actions.save`,
  `rules.form.severity_label`.
- Group related keys under an object so the JSON tree mirrors the UI.
- Don't reach across namespaces; if a string is genuinely shared, put it in
  `common` and use `useT(["mine", "common"])`.

## Interpolation

- Variables: `"greeting": "Hello {{name}}"` → `t("greeting", { name })`.
- Pluralisation: `"items": "{{count}} item"` plus `"items_other": "{{count}} items"`
  → `t("items", { count })`.

## Adding strings (sub-agents read this)

1. Pick the namespace that owns your page.
2. Add the key to **both** `locales/en/<ns>.json` and `locales/de/<ns>.json`
   in the same commit — German translation should be sensible, not a TODO.
3. Use the key from your component: `const { t } = useT("hosts"); t("table.empty")`.
4. Run `npx tsc --noEmit` to confirm the key typechecks against the resources.

## Switching locales

```ts
import { setLocale } from "@/i18n";
setLocale("de");
```

Persists in `localStorage` (`mon-lang`). `useAuth().user?.language` should call
this on login so the profile preference wins over the browser default.
