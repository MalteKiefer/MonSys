# ADR-0012: Installable PWA via vite-plugin-pwa

- Status: Accepted
- Date: 2026-05-12
- Deciders: maintainers
- Context tags: ui, build, security, deployment

## Context and Problem Statement

The MonSys SPA was usable on mobile browsers but not *installable*.
Operators checking the dashboard during incidents on a phone had to
re-load the URL each time, with no home-screen affordance, no
standalone-app chrome, and no offline app shell. The natural mode for
"check the alert that just paged me" is a one-tap launcher icon, not
a tab buried in browser history.

Forces shaping the decision:

- The SPA is React 19 + Vite 6 (see commits `512` review baseline and
  `1597` code-splitting). Vite has a canonical PWA integration —
  `vite-plugin-pwa` — which wraps Workbox and handles the
  precache-revision bookkeeping that makes "did the user actually
  receive the new build?" reliable.
- Authentication is bearer-in-localStorage (ADR-0001). The bearer
  survives offline, so the SPA shell can render without a network
  round-trip; the first authenticated fetch refreshes state.
- API responses must **never** be cached by the service worker. A
  stale `/v1/me` response from a SW cache could let a revoked
  session appear valid; a cached `/v1/alerts` response would mask
  fresh firings during an incident. Bearer + CSRF state confusion
  through a third caching layer is exactly the kind of subtle
  security smell we want to refuse by construction.
- The SPA build already lives behind the spec-drift gate
  (ADR-0008); adding a SW does not change the wire contract.

## Considered Options

1. **`vite-plugin-pwa` with `autoUpdate`, app-shell precache,
   `NetworkOnly` for every API surface, denylist on the SPA
   navigation fallback.** Canonical Vite path.
2. **Hand-written service worker.** Tried during prototyping;
   reinvented half of Workbox in 80 lines and skipped the
   precache-revision bookkeeping — meaning we couldn't reliably
   answer "did this user pick up the new build?" after a deploy.
   Not worth the maintenance burden.
3. **Workbox standalone (no Vite plugin).** Same end state but more
   manual wiring (asset manifest generation, SW build step, dev
   stub). `vite-plugin-pwa` is the idiomatic Vite path; no reason
   to fight it.
4. **Skip PWA entirely.** Cost: no install button, no offline shell,
   no home-screen icon during incidents. Benefit: zero. The plugin
   adds ~3 KB gzipped of SW + Workbox runtime; no other footprint.

## Decision Outcome

Chosen: **option 1.** `vite-plugin-pwa` with:

- `registerType: "autoUpdate"` — new SW silently swaps on next
  navigation. The `registerSW` hooks are wired (`onNeedRefresh`,
  `onOfflineReady`) so a future "new version available" toast can be
  slotted in without touching `main.tsx`.
- Workbox under the hood: `skipWaiting()` + `clientsClaim()` +
  `cleanupOutdatedCaches()` are the implicit defaults of
  `autoUpdate`; outdated precache entries are pruned on activation.
- Precache (`globPatterns`): hashed JS/CSS/HTML/woff2/PNG/SVG +
  `manifest.webmanifest`. App shell renders offline from the cache.
- `navigateFallback: "/index.html"` with a `navigateFallbackDenylist`
  that excludes `/v1/*`, `/healthz`, `/readyz`, `/openapi.*`,
  `/docs/*`, `/metrics`, `/.well-known/*` — the SPA fallback can
  never swallow a server endpoint.
- Belt-and-braces `runtimeCaching` rule: any same-origin fetch
  matching the API URL set above is forced to `NetworkOnly`. Even
  if a `fetch()` inside the running app somehow targets an API URL,
  the SW refuses to cache the response.

Brand surface: `web/public/manifest.webmanifest` declares `MonSys`,
`standalone`, theme `#047857`, background `#0a0e1a`, plus three
icons (192, 512, maskable-512) under `web/public/icons/`.

### Consequences

- Positive:
  - Installable on iOS, Android, Chrome desktop. "Add to Home
    Screen" works and launches in standalone chrome.
  - App shell renders offline; the cached bearer token in
    localStorage means the UI hydrates immediately and the first
    network call refreshes server state.
  - Repeat visits load the shell from cache — perceptible TTI
    improvement on cold mobile networks.
  - API responses are never SW-cached. The denylist + the
    `NetworkOnly` rule are belt-and-braces redundant by design.
- Negative:
  - The service worker is now part of the deployable surface. A
    misconfigured Cache-Control on `index.html` from the server
    could pin users to an old shell version — mitigated by
    `cleanupOutdatedCaches` + `autoUpdate` swapping on every
    navigation, but worth keeping in mind.
  - First load is unchanged; the benefit kicks in on repeat visits.
    No win for one-shot users.
  - PWA install prompts are browser-driven and inconsistent across
    iOS Safari / Chrome / Firefox. We get the affordance where the
    browser offers it; we don't fight it where it doesn't.
- Follow-ups:
  - Surface the `onNeedRefresh` hook as an in-app toast — the wiring
    is in place in `registerSW.ts`.
  - Push notifications via the Web Push API for fired alerts — out
    of scope today; a SW is a prerequisite, which we now have.

## More Information

- Implementation commit: `227680a` feat(web): installable PWA via
  vite-plugin-pwa.
- Code references:
  - `web/vite.config.ts` — `VitePWA({...})` plugin block,
    manifest + workbox config including
    `navigateFallbackDenylist` and the `NetworkOnly` rule.
  - `web/public/manifest.webmanifest` — manifest committed as a
    static asset (vite-plugin-pwa also emits one from config;
    both align).
  - `web/src/lib/registerSW.ts` — `virtual:pwa-register`
    registration with `onNeedRefresh` / `onOfflineReady` hooks.
  - `web/public/icons/{icon-192.png,icon-512.png,
    icon-maskable-512.png}` — brand icons referenced from the
    manifest.
- References:
  - https://vite-pwa-org.netlify.app — vite-plugin-pwa docs.
  - https://developer.chrome.com/docs/workbox — Workbox strategies
    (NetworkOnly, navigateFallbackDenylist).
  - https://www.w3.org/TR/appmanifest/ — Web App Manifest spec.
  - OWASP ASVS 5.0 V8.2 "Client-side Data Protection" — sensitive
    data (bearer, API responses) must not leak into long-lived
    client caches.
- Related: ADR-0001 (bearer model — bearer survives offline because
  it lives in localStorage, not a SW cache), ADR-0008 (spec-drift
  gate is unaffected; SW caches assets, not API responses).
