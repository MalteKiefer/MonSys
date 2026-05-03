# Security Policy

Thank you for helping keep `mon` and its users safe. This document describes
how to report vulnerabilities, what is in scope, and what response timelines
you can expect from the maintainers.

## Reporting a Vulnerability

Please **do not** open public GitHub issues for security problems.

Instead, open a **private GitHub Security Advisory** at:

  https://github.com/pr0ph37/mon/security/advisories

Include, where possible:

- A clear description of the issue and its impact.
- Reproduction steps, proof-of-concept code, or a minimal failing test case.
- Affected version(s), commit hash, and deployment topology (single-node,
  multi-tenant proxy in front, etc.).
- Your preferred name / handle for the hall of fame (optional).

If you are unable to use GitHub Security Advisories, state that in a public
issue without disclosing details and a maintainer will provide an alternative
secure channel.

## Scope

In scope:

- The `mon-server` HTTP API, web SPA, and authentication flows.
- The `mon-agent` Linux binary, its bootstrap/registration flow, and its
  handling of `agent_config` payloads.
- Database migrations, default deployment manifests under `deploy/`, and
  shipped Docker images.
- Any cryptographic material handling (keys, tokens, secrets at rest or in
  transit).

Out of scope:

- **Agent has no built-in update mechanism.** This is by design. Operators
  are expected to verify release checksums and signatures before rolling
  out new agent binaries through their existing configuration management.
- **Single-tenant by design.** A `mon` deployment is intended for a single
  trust domain. Cross-tenant isolation is not a goal and is not a
  vulnerability class we accept.
- Findings that require an already-compromised admin account, physical
  access to the server host, or root on a monitored machine.
- Best-practice or hardening suggestions without a concrete attack.
- Denial of service via raw resource exhaustion (e.g. flooding the ingest
  endpoint from an authenticated agent).

## Response SLA

Once a report is acknowledged, we target the following remediation windows,
measured from triage to a fix being available in a tagged release:

| Severity | Target fix window |
|----------|-------------------|
| Critical | 7 days            |
| High     | 30 days           |
| Medium   | 90 days           |
| Low      | Best effort       |

Severity is assessed by the maintainers using CVSS v3.1 as a guideline,
adjusted for the single-tenant deployment model described above.

## Coordinated Disclosure

We follow a **coordinated disclosure** model with a hard cap of **90 days**
from the date a report is acknowledged. After a fix ships (or 90 days have
elapsed, whichever is sooner), the advisory will be made public and a CVE
requested where appropriate. We are happy to coordinate a longer embargo
for ecosystem-wide issues if all affected parties agree.

## Hall of Fame

Researchers who report valid issues will be credited here, unless they
prefer to remain anonymous.

<!-- Hall of fame placeholder. Add entries as: -->
<!-- - YYYY-MM-DD - Name / handle - short description (CVE-XXXX-YYYY) -->

_No entries yet._
