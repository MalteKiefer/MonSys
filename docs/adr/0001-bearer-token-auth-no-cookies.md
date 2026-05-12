# ADR-0001: Bearer-token auth, no cookies

- Status: Accepted
- Date: 2026-04-20
- Deciders: maintainers
- Context tags: auth, security, api

## Context and Problem Statement

MonSys is a single-page React app that talks to a Go (huma) JSON API
served from the same origin in production but from a separate dev origin
during local work. Operators routinely run the SPA behind reverse
proxies (Nginx, Caddy, Traefik), and a non-trivial fraction of installs
want a "no public DB, no public web, just a TLS endpoint" deployment.

When the project graduated from "single admin local toy" to "API that
will be hit by external clients (CLI, future mobile, future automation
bots)" we had to pick a primary session-binding mechanism. The forces:

- **CSRF surface.** Cookie auth means every state-changing endpoint
  needs CSRF protection (double-submit token, Origin/Referer checks,
  SameSite). A single missed route is a vulnerability.
- **SameSite alone is not enough.** `SameSite=Lax` still allows top-level
  GET navigations, and `SameSite=Strict` breaks legitimate
  cross-origin link flows (email-confirm-links to the SPA in another
  tab). The combinatorics of Lax-vs-Strict-per-route are non-trivial.
- **SPA simplicity.** A bearer header is one place to put one credential;
  no `withCredentials`, no per-route cookie attributes, no path-prefix
  cookies.
- **Mobile / CLI parity.** The `mon` CLI (`internal/cli`) and a future
  iOS / Android app would have to re-implement cookie-jar handling for
  what is essentially "a single opaque session string anyway".
- **Database storage shape.** Whatever we store has to be resistant to
  read-only DB compromise. Plaintext session ids in the DB lets anyone
  with a backup or read-replica replay sessions.

We must pick an approach that fits the SPA + CLI + future-mobile shape
without an XSRF middleware on every route.

## Considered Options

1. **HttpOnly + Secure + SameSite=Lax session cookie**, with
   double-submit CSRF tokens for state-changing routes.
2. **HttpOnly + Secure + SameSite=Strict session cookie**, no CSRF
   tokens needed but cross-origin link flows broken.
3. **Bearer token in `Authorization: Bearer <opaque>` header**, token
   stored as `sha256(secret)` in `user_sessions`. SPA holds the
   secret in localStorage.
4. **JWT in localStorage** with signed claims; stateless server.
5. **OAuth2/OIDC delegated to an external IdP** (Keycloak, Authentik).

## Decision Outcome

Chosen: **option 3** — opaque bearer tokens hashed in DB.

Rationale:

- **CSRF surface evaporates.** The browser does not auto-attach
  `Authorization` headers cross-origin the way it auto-attaches cookies.
  No double-submit, no Origin checks, no `SameSite` gymnastics. State-
  changing endpoints get the same `requireUser` middleware as reads.
- **SPA, CLI, and future mobile share one auth model.** They all set
  one header. The CLI's `mon login` writes the bearer to
  `~/.config/mon/session`; the SPA writes it to localStorage; a future
  app writes it to the platform keychain. The server doesn't care.
- **`sha256` storage means a DB leak doesn't immediately mint
  sessions.** An attacker with read access can revoke or analyse but
  cannot impersonate without the secret half — same shape as a properly
  hashed password.
- **Opaque tokens are revocable in O(1)** by deleting the row, which is
  what `Store.RevokeUserSessions` / `RevokeUserSessionsExcept` rely on.
  Stateless JWTs would force a denylist anyway, defeating their main
  advantage.
- **No external IdP dependency.** MonSys is supposed to install on a
  single VM with a single docker-compose; mandating Keycloak alongside
  it is operator-hostile.

### Consequences

- Positive:
  - No CSRF middleware to forget on a new route. Auth is one
    `requireUser` line per huma operation.
  - Same auth pipeline for browser, CLI, future mobile, future bot.
  - Session revocation is a `DELETE` — see ADR-0010 for the rotation
    contract that depends on this.
  - Idle-timeout / max-session-hours enforced server-side per
    `Store.security_policy` (`08f4dc0`) — clients cannot extend their
    own session by tampering with token payload (there is no payload).
- Negative:
  - **localStorage is reachable from any script in the SPA origin.**
    An XSS in our React tree leaks the session token directly. We
    accept this risk in exchange for the CSRF elimination. Mitigations:
    strict CSP, no third-party JS, ESLint async-handling baseline
    (`423d2b9`), and the credential-rotation contract that lets a user
    or admin kill a stolen session immediately (ADR-0010).
  - Bearer tokens don't auto-expire in the browser; the SPA must check
    `401 → /login` on every fetch. We do this via an axios-style
    response interceptor in `web/src/lib/api.ts`.
  - No native "remember me on this device" UX free with cookies; we
    re-issue on every login and rely on the policy-derived TTL.
- Follow-ups:
  - ADR-0002 (WebAuthn) layers passkeys on top of this same bearer
    pipeline — passkey login still issues a bearer.
  - ADR-0010 documents the full credential-rotation contract that
    leans on row-level revocation.

## More Information

- Implementation commits:
  - `08f4dc0` feat(auth): webauthn package + passkey/policy store +
    session hardening — introduces `effectiveSessionTTL`,
    `IssueSession`, `RevokeUserSessions[Except]`, `ValidateSession`
    idle-timeout via `make_interval(secs => $1)`.
  - `e0613da` security(auth): revoke sessions on credential rotation
    + close cross-flow token misuse — proves the bearer/DB-row model
    is what makes "rotate => evict" mechanically possible.
  - `80ae05e` security(auth): close remaining audit findings —
    F-9 adds the 60s `requireMethodCompliance` cache that compensates
    for the per-request DB hit a bearer model implies.

- References:
  - OWASP ASVS 5.0 §3 "Session Management" — opaque, server-issued,
    server-revocable session ids.
  - OWASP Top 10:2025 A07 "Authentication Failures" — favours opaque
    server-side sessions over self-validating tokens.
  - OWASP CSRF Cheat Sheet — explicit statement that custom-header
    auth (e.g. `Authorization`) is the simplest CSRF defence.
  - RFC 6750 §2.1 — Bearer token usage in `Authorization` header.

- Related: ADR-0002 (WebAuthn), ADR-0010 (security-primitives
  summary).
