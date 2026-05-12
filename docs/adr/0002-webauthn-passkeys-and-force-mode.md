# ADR-0002: WebAuthn passkeys + admin force-mode policy

- Status: Accepted
- Date: 2026-05-12
- Deciders: maintainers
- Context tags: auth, security, ui

## Context and Problem Statement

MonSys already had TOTP as a second factor (since the initial 2FA push)
but TOTP alone is not phishing-resistant: it travels over the wire as a
six-digit code that AiTM-proxy kits replay in real time. Several
operators run MonSys as part of their security stack and asked for
WebAuthn so we can:

1. Add a phishing-resistant second factor on top of TOTP, not in place
   of it (don't strand users mid-migration).
2. Give admins a knob to *require* a strong factor across the org, with
   a humane onboarding ramp, not a hard cutover that locks people out.
3. Enforce that "rotating a credential" (admin reset of TOTP, deleting
   a passkey, changing a password) actually evicts attackers — not just
   the legitimate user's other tab.

This sits on top of ADR-0001's bearer-token model. WebAuthn is solving
"how the bearer is *minted*", not "what the bearer is".

Forces shaping the decision:

- WebAuthn Level 3 has stable browser support (Safari 17, Chrome 108+,
  Firefox 122 for conditional-mediation). Picking Level 2 would mean
  forgoing discoverable credentials, which kill the passwordless UX.
- TOTP cannot be removed: most users still don't own a passkey, and we
  ship to homelab / SMB operators where buying YubiKeys for everyone is
  not realistic.
- An "admin force-mode" needs a grace window or it bricks the next
  login when an admin clicks the wrong setting.
- A "delete my last passkey" / "admin reset 2FA" path that doesn't
  revoke existing sessions is a session-replay primitive — the audit
  finding F-2/F-3/F-7/F-8 (closed in `e0613da`) made this concrete.

## Considered Options

1. **TOTP only, harden it.** Add anti-phishing notes, enforce TOTP.
   Cheap. Doesn't address phishing-resistance.
2. **WebAuthn-only, deprecate TOTP.** Pure passkey. Best security
   posture but strands every existing TOTP user and creates a hard
   migration cliff for SMB operators.
3. **WebAuthn + TOTP side-by-side, no admin enforcement.** Users opt
   in. Risk: nobody opts in.
4. **WebAuthn + TOTP side-by-side, admin force-mode with grace
   period.** Tri-state policy: `off` / `2fa_any` / `passkey_required`.
   Per-user `force_grace_until` lets admins ramp.
5. **External SSO (Keycloak / Authentik) as the 2FA owner.** Punt the
   problem upstream. Adds an external dependency we explicitly refused
   in ADR-0001.

## Decision Outcome

Chosen: **option 4** — passkeys beside TOTP, with admin-tunable
force-mode and per-user grace.

Rationale:

- **Discoverable credentials + UV-required gives us "no usernames in
  the URL bar" UX.** `residentKey=required` lets the browser surface
  a passkey from autofill (conditional mediation), so login is "click
  the passkey" with no email typed. `userVerification=required` makes
  every assertion include a biometric/PIN check, which closes the
  "stolen phone, unlocked, attacker hits the passkey button" gap.
- **Side-by-side coexistence respects existing TOTP users.** A user
  with TOTP can enrol a passkey at leisure; once enrolled they can
  delete TOTP. A user with only TOTP keeps working.
- **Force-mode as a tri-state with grace.** `off` (default) preserves
  the legacy permissive surface. `2fa_any` says "you need TOTP *or*
  passkey", which is the realistic SMB position. `passkey_required`
  says "passkey only" and is what we recommend for new orgs. The
  grace window (default 7 days, admin-tunable per user) is critical:
  flipping the toggle doesn't kick anyone *now*, it sets a deadline.
- **Session revocation on every credential rotation.** Listed in detail
  in ADR-0010, but the design point: deleting a passkey, disabling
  TOTP, resetting a password, or admin-changing-an-email all call
  `RevokeUserSessions` (the actor's other sessions, anyway —
  `RevokeUserSessionsExcept` spares the live one to avoid self-DoS).

### Consequences

- Positive:
  - Phishing-resistant second factor available to anyone who wants it.
  - Admin can require it org-wide with a humane ramp.
  - The "must enrol" UI is a banner + countdown in the SPA
    (`EnforcementGuard`, `5463737`), so the policy is visible *before*
    it bites.
  - Session-revocation contract closes the "stolen session outlives a
    reset" class of bug (audit findings F-2/F-3/F-7/F-8, closed in
    `e0613da`).
  - WebAuthn user IDs are random `users.webauthn_handle` UUIDs, not
    PII — RP can't enumerate users from handles alone.
- Negative:
  - More auth-flow surface, more code, more places for bugs. F-9 in
    `80ae05e` (compliance-check cache that doesn't fail-open under DB
    latency) is a direct consequence of the new middleware layer.
  - Recovery has to be admin-side. The `mon` CLI gained
    `--reset-2fa`, `--reset-passkeys`, `--set-security-policy`
    (`c7fc5dd`) so an admin with shell access can always recover from
    a misconfigured force-mode.
  - `passkey_required` is a footgun if the admin enables it before any
    user has a passkey enrolled — they'll all hit the enrolment guard
    on next login. We mitigate with the warning callout in
    `AdminSecurity.tsx` (`5463737`) and the default 7-day grace.
- Follow-ups:
  - Cross-device passkey sync (iCloud Keychain, Google Password
    Manager) works out of the box — we don't need to do anything,
    but we should document the UX explicitly.
  - "Step-up" flows (re-prompt UV for high-risk actions) — not built
    yet. Tracked separately.

## More Information

- Implementation commits:
  - `2d85086` feat(auth): foundation for passkeys + security policy
    — migration 0029 `user_credentials`, `users.webauthn_handle`,
    `users.force_grace_until`; apitypes shapes.
  - `08f4dc0` feat(auth): webauthn package + passkey/policy store
    + session hardening — `internal/server/webauthn` wraps go-webauthn
    with `RPID`, `RPName`, `Origins`, `residentKey=required`,
    `userVerification=required`; `store/user_passkey.go` adds
    Begin/Finish register + login + list/rename/delete;
    `store/security_policy.go` adds `force_mode/grace_days/
    max_session_hours/idle_timeout_minutes` KV plus
    `RevokeAllSessions`, `RevokeUserSessions`,
    `UserCompliesWithPolicy`.
  - `c858b76` feat(api): webauthn endpoints + admin security +
    force-mode gate — 10 huma routes plus `requireMethodCompliance`
    middleware that gates `protected` routes on policy compliance.
  - `5463737` feat(web): passkey login + enrollment UI + admin
    security policy + force-mode guard — SPA wrappers around
    `navigator.credentials.create/.get`, conditional-mediation
    autofill, `EnforcementGuard` countdown banner.
  - `e0613da` security(auth): revoke sessions on credential rotation
    + close cross-flow token misuse — proves the contract.
  - `80ae05e` security(auth): close remaining audit findings —
    F-9 60s policy cache, F-10 reclassifies routes
    (avatar/language/email-request) from `openProtected` → `protected`
    because they're not enrolment paths.

- References:
  - W3C WebAuthn Level 3, §5.4 "Authenticator Selection Criteria".
  - WebAuthn Guide — discoverable credentials and conditional
    mediation.
  - OWASP ASVS 5.0 §2.8 "Single or Multi Factor One-Time Authenticator".
  - OWASP LLM/Agentic AI Top 10 — phishing-resistant auth is a
    prerequisite for any AI-augmented operator surface (relevant
    because MonSys exposes alert and rule mutation to admins).
  - go-webauthn v0.17.3 — `internal/server/webauthn/webauthn.go`.

- Related: ADR-0001 (bearer model these flows mint into), ADR-0010
  (credential-rotation summary).
