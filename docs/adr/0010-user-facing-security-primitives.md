# ADR-0010: User-facing security primitives — TOTP, passkeys, sessions,
MFA force, avatar, language, email-change, password reset

- Status: Accepted
- Date: 2026-05-12
- Deciders: maintainers
- Context tags: auth, security, ui, schema

## Context and Problem Statement

The user-account surface in MonSys has grown organically: TOTP first,
then admin password reset, then sessions table, then passkeys + force-
mode, then avatar, then email change, then language, then "revoke all
my other sessions", then per-user admin revoke. The primitives are
internally consistent but spread across ten commits and three ADRs
(0001, 0002, 0006). This ADR is the summary view — what the surface
*is*, what contract each primitive holds, and how they compose.

The forces driving this ADR:

- The contract for "what happens when a credential rotates" was never
  written down in one place. The audit (F-2, F-3, F-7, F-8, F-19) found
  six paths that didn't revoke sessions on rotation; closing them
  retroactively was painful. A future contributor adding a new
  rotation path needs a single document that says "if you rotate, you
  revoke".
- The user-profile UI grew across `5463737` (passkeys), `24894d5`
  (avatar + two-step email change), `b984dd3` (language), and
  `6c7bcba` (admin 3-dot menu) without one place describing what the
  whole surface looks like.
- The avatar magic-byte verification (`80ae05e` F-4) is a non-
  obvious requirement and needs to be documented as a *contract*,
  not a one-off security fix.

## Considered Options

This ADR is descriptive, not selective — it documents the existing
surface as the chosen design. The alternative was "let this surface
remain implicit across 10 commits", which is what we had and what we
explicitly rejected.

## Decision Outcome

The user-account surface comprises seven primitives, each with a
documented contract:

### 1. Password (legacy primary factor)

- Stored as argon2id hash, parameterised in `internal/server/auth`.
- Reset paths:
  - **User-driven**: `POST /v1/auth/reset/request` → email →
    `POST /v1/auth/consume-reset` with `kind=password_reset` or
    `invite`. **F-19 (`e0613da`)**: `handleConsumeReset` now
    requires the kind to be `password_reset` or `invite`
    explicitly; an empty `expectedKind` could let a leaked
    `email_change` / `login_2fa` / `webauthn_register` token
    pivot to a password change.
  - **Admin-driven**: `PUT /v1/admin/users/{id}/password`. Withholds
    the reset URL when an email was successfully sent (`2065d36`).
  - **CLI**: `mon --reset-password <email>` (`5c61365`).
- **Rotation contract**: every password change calls
  `RevokeUserSessions` (admin path) or `RevokeUserSessionsExcept`
  (user-driven path, sparing the caller's own session via
  `tokenFromContext`). F-2 in `e0613da`.

### 2. TOTP (second factor)

- Stored as encrypted secret. RFC 6238, 30s window, 6 digits.
- Setup: `POST /v1/auth/me/totp/begin` → user scans QR → `POST
  /v1/auth/me/totp/finish` with a current code.
- Disable: `DELETE /v1/auth/me/totp` (user) or admin reset.
- **Rotation contract**: disable / admin reset calls
  `RevokeUserSessions`. F-3 / F-7 in `e0613da`.

### 3. WebAuthn passkeys (second factor / primary factor under
   `passkey_required`)

- See ADR-0002.
- Storage: `user_credentials` (COSE pubkey, sign_count, transports,
  backup flags, AAGUID, name). `users.webauthn_handle` is the
  privacy-safe WebAuthn user id.
- Settings: `residentKey=required`, `userVerification=required`.
- **Rotation contract**: passkey delete / rename / register all
  call appropriate revocation. F-8 in `e0613da`. Last-passkey-
  under-`passkey_required` deletion returns 409 with
  `must_enroll_2fa` when grace is over.

### 4. Sessions

- See ADR-0001.
- Storage: `user_sessions` with `token_hash = sha256(secret)`,
  `created_at`, `last_seen_at`, `expires_at`, `idle_after_seconds`,
  `user_agent`, `ip`.
- TTL is `effectiveSessionTTL(ctx)` clamped to
  `security_policy.max_session_hours`.
- Idle timeout is enforced in `ValidateSession` via a `WHERE
  last_seen_at > now() - make_interval(secs => $1)` clause — see
  the MEMORY note on the pgx int-text concat gotcha that drove this.
- Revocation paths:
  - User: `POST /v1/auth/me/revoke-all` (revokes all *other*
    sessions, sparing the caller via `tokenFromContext`).
  - User: `DELETE /v1/auth/me` (logout).
  - Admin: `POST /v1/admin/users/{id}/revoke-sessions` (`2065d36`).
  - Admin global: `POST /v1/admin/security/revoke-all-sessions`
    (spares the admin's own session).
- **Audit row** emitted by `IssueSession` on success (`08f4dc0`).

### 5. MFA force-mode

- See ADR-0002.
- Storage: `Store.security_policy` under KV `settings`. Fields:
  `force_mode` (`off` / `2fa_any` / `passkey_required`),
  `grace_days`, `max_session_hours` (1–720),
  `idle_timeout_minutes` (0–10080), `force_grace_until` per user.
- Enforcement: `requireMethodCompliance` middleware on `protected`
  routes; `openProtected` skips the gate for endpoints a user
  needs in order to *remediate* (TOTP enrol, passkey enrol, logout).
- **F-9 (`80ae05e`)**: the middleware caches policy compliance
  per user for 60 seconds. On DB error, the last-known-good entry
  is reused without extending its expiry — closes the "induce DB
  latency, bypass enrollment gate" escalation path.
- **F-10 (`80ae05e`)**: `POST /v1/auth/me/avatar`, `DELETE
  /v1/auth/me/avatar`, `PUT /v1/auth/me/language`, `POST
  /v1/auth/me/email/request` moved from `openProtected` to
  `protected`. These are profile-customisation, not enrollment
  paths.

### 6. Avatar

- Storage: `users.avatar_bytes` (BYTEA) + `users.avatar_mime`.
- Upload: `POST /v1/auth/me/avatar` with multipart form data.
- **Magic-byte verification (`80ae05e` F-4)**: the bytes are
  decoded with `image.Decode` and the *actual* format must match
  the *claimed* content-type *and* be one of png / jpeg / webp.
  Stored `avatar_mime` is derived from the decoded format, never
  from the client header. New dep `golang.org/x/image` (for the
  webp decoder, blank import).
- `GET /v1/users/{id}/avatar` sets `X-Content-Type-Options:
  nosniff` so a sniffing client can't reinterpret the bytes.

### 7. Verified email change

- Two-step: `POST /v1/auth/me/email/request` → server sends a
  token to the **new** address (not the old one — that would let
  someone with read access to the old inbox claim the new
  address) → user clicks link → SPA `POST /v1/auth/consume-email-
  change` with token + kind=`email_change`.
- `Store.SetEmailUnconditional` (CLI `mon --change-email`)
  matches the HTTP path: `RETURNING id` + `RevokeUserSessions`.
- **Rotation contract**: email change revokes sessions (F-2 in
  `e0613da`).

### 8. Language preference

- See ADR-0006.
- `users.language TEXT NOT NULL DEFAULT 'auto'` with
  `CHECK (language IN ('auto', 'en', 'de'))`.
- `PUT /v1/auth/me/language` persists; `App.tsx` mirrors back to
  `i18n.changeLanguage` on `/me` refresh so a switch on device A
  picks up on device B.
- *No rotation contract* — language is not a credential.

### The credential-rotation contract (summary)

> Any path that mutates a credential (password, TOTP, passkey,
> email) MUST call `RevokeUserSessions` (admin path) or
> `RevokeUserSessionsExcept(tokenFromContext(ctx))` (user-driven
> path). A new such path is a bug if it skips this.

The store methods that enforce this:

- `Store.SetPassword` (CLI reset) — `RETURNING id` then
  `RevokeUserSessions`. F-1 added the case-insensitive email
  match so the CLI lines up with the HTTP path.
- `Store.SetPasswordByAdmin` — `RevokeUserSessions`.
- `Store.ChangePassword(ctx, …, exceptToken)` —
  `RevokeUserSessionsExcept`.
- `Store.SetEmailUnconditional` — `RETURNING id` then
  `RevokeUserSessions`.
- `Store.DisableTOTP` (paired with `RevokeUserSessions` in
  every caller).
- Passkey delete/rename — paired with appropriate revocation.

### Consequences

- Positive:
  - All credential-rotation paths revoke sessions. A future
    contributor adding a new such path has *this ADR* to point at.
  - The avatar upload contract is documented as a contract, not
    a one-off finding.
  - The verified-email-change-to-new-address pattern is captured
    so it doesn't regress to "send to old address".
  - The `users.language` column composes with `users.password_hash`,
    `users.email`, `users.webauthn_handle`,
    `users.force_grace_until`, `users.avatar_bytes` — the user
    schema is now a coherent surface, not an accretion.
- Negative:
  - This ADR is descriptive, so it can drift if the surface
    changes without an ADR update. Mitigated by referencing the
    underlying commits inline so a `git blame` walks back here.
  - The credential-rotation contract is enforced by code review,
    not by the type system. A future contributor *could* add a
    `Store.SetPassword2` that skips the revoke call and tests
    wouldn't catch it. Mitigation: integration tests on every
    rotation path that assert sessions are gone after.
- Follow-ups:
  - Step-up auth for high-risk admin actions (UV re-prompt) —
    not built.
  - Anomaly detection on session activity (geo / new IP) —
    partially in `login_anomaly` evaluator (ADR-0003).
  - Phishing-resistant primary auth (passkey-only enforced at
    org level) — supported but not the default. See ADR-0002.

## More Information

- Implementation commits (chronological):
  - `2d85086` feat(auth): foundation for passkeys + security
    policy.
  - `08f4dc0` feat(auth): webauthn package + passkey/policy store
    + session hardening.
  - `c858b76` feat(api): webauthn endpoints + admin security +
    force-mode gate.
  - `5463737` feat(web): passkey login + enrollment UI + admin
    security policy + force-mode guard.
  - `c7fc5dd` feat(cli): admin recovery flags for 2FA, passkeys,
    security policy.
  - `5c61365` feat(cli): add `--reset-password` flag for admin
    password recovery.
  - `2065d36` feat(admin): reset-password withholds URL on mail
    success; add per-user revoke-sessions.
  - `6c7bcba` feat(web): admin users 3-dot menu + mail-only reset
    + per-user revoke sessions.
  - `ef29540` feat(profile): avatar storage, verified email-
    change, CLI change-email.
  - `24894d5` feat(web): avatar upload, two-step email change,
    sidebar user card.
  - `b984dd3` feat(i18n): full DE/EN, browser default, user
    override, TopBar selector.
  - `ca44746` fix(i18n): language preference actually persists
    server-side.
  - `e0613da` security(auth): revoke sessions on credential
    rotation + close cross-flow token misuse.
  - `80ae05e` security(auth): close remaining audit findings —
    F-4/9/10/11/14/17/20.

- References:
  - OWASP ASVS 5.0 §2 "Authentication" (V2.1 password rules,
    V2.2 general auth, V2.7 OOB, V2.8 single/multi-factor
    one-time, V2.9 cryptographic).
  - OWASP ASVS 5.0 §3 "Session Management" (V3.2 session
    binding, V3.3 session termination).
  - OWASP Top 10:2025 A07 "Identification and Authentication
    Failures".
  - CWE-384 "Session Fixation" — addressed by row-level
    revocation on rotation.
  - CWE-613 "Insufficient Session Expiration" — addressed by
    `effectiveSessionTTL` + idle timeout.
  - CWE-434 "Unrestricted Upload of File with Dangerous Type"
    — addressed by magic-byte verification on avatar.

- Related: ADR-0001 (bearer model), ADR-0002 (WebAuthn + force-
  mode), ADR-0006 (language preference column).
