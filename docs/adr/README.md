# Architecture Decision Records

This directory captures the major architectural decisions taken in
MonSys, following the [MADR 3.0](https://adr.github.io/madr/)
template.

Each ADR is a self-contained, dated record of one decision: the
context that forced it, the alternatives considered, the option
chosen, and the consequences. ADRs are append-only — when a later
decision supersedes an earlier one, the earlier ADR is marked
**Superseded by ADR-XXXX** but not edited or deleted.

## Index

| ADR | Title | Status | Date | Tags |
|-----|-------|--------|------|------|
| [ADR-0001](./0001-bearer-token-auth-no-cookies.md) | Bearer-token auth, no cookies | Accepted | 2026-04-20 | auth, security, api |
| [ADR-0002](./0002-webauthn-passkeys-and-force-mode.md) | WebAuthn passkeys + admin force-mode policy | Accepted | 2026-05-12 | auth, security, ui |
| [ADR-0003](./0003-alert-engine-condition-types-and-jsonb-params.md) | Alert engine with 23 condition_types + JSONB `condition_params` | Accepted | 2026-05-12 | alerting, observability, schema, security |
| [ADR-0004](./0004-rule-groups-many-rules-under-one-name.md) | Rule groups — many rules under one name | Accepted | 2026-05-12 | alerting, schema, ux |
| [ADR-0005](./0005-three-step-rule-wizard.md) | 3-step wizard for rule creation | Accepted | 2026-05-12 | ui, ux, alerting |
| [ADR-0006](./0006-i18n-architecture.md) | i18n architecture — react-i18next + 11 namespaces + browser-default + server-override | Accepted | 2026-05-12 | i18n, ui, schema |
| [ADR-0007](./0007-signed-self-updating-linux-agent.md) | Signed self-updating Linux agent | Accepted | 2026-05-12 | agent, security, build, platform |
| [ADR-0008](./0008-openapi-source-of-truth-and-drift-gate.md) | OpenAPI as source of truth + spec-drift CI gate | Accepted | 2026-05-12 | build, api, types, ci |
| [ADR-0009](./0009-distroless-signed-multi-arch-image.md) | Distroless nonroot container + signed multi-arch image via ghcr.io | Accepted | 2026-05-12 | build, security, deployment, supply-chain |
| [ADR-0010](./0010-user-facing-security-primitives.md) | User-facing security primitives — TOTP, passkeys, sessions, MFA force, avatar, language, email-change, password reset | Accepted | 2026-05-12 | auth, security, ui, schema |
| [ADR-0011](./0011-pprof-and-benchmark-baselines.md) | Runtime profiling endpoints + benchmark baselines for hot paths | Accepted | 2026-05-12 | observability, performance, security, build |
| [ADR-0012](./0012-installable-pwa-via-vite-plugin-pwa.md) | Installable PWA via vite-plugin-pwa | Accepted | 2026-05-12 | ui, build, security, deployment |

## Cross-reference graph

- ADR-0001 (bearer model) — foundation for ADR-0002, ADR-0010.
- ADR-0002 (WebAuthn) — built on top of ADR-0001; summarised in
  ADR-0010.
- ADR-0003 (alert engine) — composed by ADR-0004 (groups) and
  driven by ADR-0005 (wizard); wire contract guarded by ADR-0008.
- ADR-0004 (rule groups) — extends ADR-0003; consumed by ADR-0005.
- ADR-0005 (wizard) — UI over ADR-0003 + ADR-0004; strings live in
  the namespace described in ADR-0006.
- ADR-0006 (i18n) — `users.language` is part of the schema
  surface documented in ADR-0010.
- ADR-0007 (signed Linux agent) — counterpart to ADR-0009's signed
  image: both halves of "everything we ship is verifiable".
- ADR-0008 (OpenAPI gate) — every commit referenced by ADR-0002 /
  ADR-0003 / ADR-0004 passes through it.
- ADR-0009 (signed container) — supply-chain peer of ADR-0007.
- ADR-0010 (user-account surface) — descriptive summary linking
  ADR-0001, ADR-0002, ADR-0006.
- ADR-0011 (pprof + benchmarks) — admin-bearer gate reuses the
  ADR-0001 / ADR-0010 trust anchor; `/debug/pprof/*` mounts next to
  the existing `/metrics` endpoint.
- ADR-0012 (installable PWA) — bearer-in-localStorage from ADR-0001
  is what lets the offline shell hydrate; SW explicitly refuses to
  cache `/v1/*` and the other API surfaces.

## Status legend

- **Accepted** — current best practice; new code should follow.
- **Superseded by ADR-XXXX** — a later ADR replaces this decision.
  The original is kept for historical context.
- **Proposed** — under discussion, not yet adopted.
- **Deprecated** — should not be followed in new code, but
  existing code still depends on it.

## Adding an ADR

1. Pick the next four-digit number.
2. Copy the format from any existing ADR (MADR 3.0 shape: Status,
   Date, Deciders, Context tags, Context and Problem Statement,
   Considered Options, Decision Outcome / Consequences, More
   Information with implementation commits + references +
   Related ADRs).
3. Add a row to the table above.
4. Update the cross-reference graph if the new ADR relates to an
   existing one.
5. Submit the ADR + the index update in the same PR.

## Template

```markdown
# ADR-NNNN: <short title>

- Status: Accepted | Superseded by ADR-XXXX
- Date: 2026-MM-DD
- Deciders: maintainers
- Context tags: auth, security, ui, agent, observability, build

## Context and Problem Statement

What is the problem? What are the forces shaping the decision?

## Considered Options

1. <option A>
2. <option B>
3. <option C>

## Decision Outcome

Chosen: option <X>, because <reason>.

### Consequences

- Positive: …
- Negative: …
- Follow-ups: links to issues / future ADRs.

## More Information

- Implementation commit(s): <hash> <subject>
- References: RFCs, OWASP, CWE, prior art
- Related: ADR-NNNN, ADR-NNNN
```
