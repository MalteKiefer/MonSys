# ADR-0009: Distroless nonroot container + signed multi-arch image via
ghcr.io

- Status: Accepted
- Date: 2026-05-12
- Deciders: maintainers
- Context tags: build, security, deployment, supply-chain

## Context and Problem Statement

MonSys ships as a single Go binary + a TimescaleDB-backed database.
Two ways an operator can run it:

1. Download the signed Linux binary from a GitHub Release and run
   it under their own systemd unit + their own Postgres.
2. Pull a container image and run the supplied
   `docker-compose.prod.yaml`.

The container path is what 80% of new operators take. Forces:

- The image must be small. A Debian-based image with our binary is
  ~120 MB; distroless nonroot with the same binary is ~25 MB. Faster
  pulls on slow links matter for the SMB / homelab user.
- The image must be verifiable. Anyone pulling `ghcr.io/.../monsys-
  server:vX.Y.Z` should be able to prove it came from our CI and
  hasn't been swapped.
- TimescaleDB doesn't need to talk to the internet. It only
  talks to the API container. Putting it on a no-egress network is
  defence-in-depth.
- The database container needs file ownership / chmod / setuid for
  initdb and for the postgres user; it can't drop *all* capabilities.
- Release artefacts should include SBOMs so downstream audits (FedRAMP,
  CRA, etc.) can be answered without re-scanning.

## Considered Options

1. **Ship binaries only.** Operator builds their own container. Most
   "production" Docker compositions in the wild are bespoke; this is
   already the path for power users. But the default path can't be
   "compose your own".
2. **Debian-based distroless-less image** (`debian:stable-slim`).
   ~80 MB. Has a shell + libc + package manager. Bigger attack
   surface.
3. **Distroless nonroot** (`gcr.io/distroless/static-debian12:
   nonroot`). ~25 MB. No shell, no package manager, runs as UID
   65532. The Go binary is statically linked so glibc / musl don't
   apply.
4. **Alpine-based** (`alpine:3.19`). ~15 MB. Musl. Smaller still but
   has a shell + apk; we'd lose distroless's "no toolchain" guarantee.
5. **Unsigned image.** Smallest pipeline. Doesn't meet "verifiable
   supply chain".
6. **GPG-signed image (Notary v1).** Heavy infra (Notary server,
   delegations).
7. **Cosign keyless (Sigstore) via OIDC.** No long-lived key. CI
   identity is the trust root, attested in Rekor.
8. **TimescaleDB on a shared default network.** Easiest. Lets the DB
   reach the internet (e.g. for `apt update`). No business doing
   that.
9. **TimescaleDB on a no-egress `mon-internal` network with dropped
   capabilities.** Defence-in-depth.

## Decision Outcome

Chosen: **option 3 + option 7 + option 9** — distroless nonroot
runtime image, cosign keyless signing via GitHub OIDC, TimescaleDB
on a no-egress internal network with dropped capabilities.

Image pipeline:

- Multi-arch build (linux/amd64 + linux/arm64) of the server binary.
- COPY into `gcr.io/distroless/static-debian12:nonroot`.
- Push to `ghcr.io/maltekiefer/monsys-server:<tag>` and `:latest`.
- `cosign sign --yes --keyless` via the GitHub OIDC token. The
  resulting signature lives in Rekor; the public log is the trust
  root.
- A CycloneDX SBOM is generated for each binary (mon-server,
  mon-agent × linux/amd64, linux/arm64) plus one for the source
  tree, attached to both the tag release and the rolling
  `:latest` release. Uses `anchore/sbom-action@v0.17.9`,
  SHA-pinned.

Compose hardening (`deploy/docker-compose.prod.yaml`):

- TimescaleDB service on `mon-internal` network with no
  internet egress.
- `security_opt: ["no-new-privileges:true"]` on the DB.
- `cap_drop: ["ALL"]` then `cap_add: ["CHOWN", "SETUID",
  "SETGID", "DAC_OVERRIDE", "FOWNER"]` — the minimum set Postgres
  needs.
- `read_only` *intentionally not set* — Postgres writes
  `pg_stat_tmp`, sockets, WAL. Documented inline so the next
  reviewer doesn't "fix" it.
- `pull_policy: always` on the server image so a manual edit
  can't pin a stale digest.

Rationale:

- **Distroless nonroot is the cheapest meaningful hardening.** No
  shell means RCE escalation has nothing to pivot to (no `wget`,
  no `bash`, no `apt`). No package manager means no in-container
  update path — updates ship as new images, which is what we want.
  UID 65532 means a hypothetical container escape lands as
  unprivileged.
- **Keyless signing fits CI exactly.** No private key on a runner.
  The signature is "this was signed by the GitHub workflow at
  `MalteKiefer/MonSys/...` on commit `<sha>`", verifiable via
  `cosign verify --certificate-identity-regexp` + Rekor. Docs in
  `docs/RELEASE.md`. The verify command is the operator's contract.
- **SBOMs are cheap, audits are not.** Generating CycloneDX on
  every release is one CI step; not having an SBOM means the
  first auditor request blocks releases for days. Per-binary +
  per-source-tree SBOM means a downstream consumer can answer
  "what's in your stuff" without re-scanning.
- **TimescaleDB on a no-egress internal network.** The DB does
  not need to talk to the internet. Putting it on a network with
  no default route means a hypothetical SQLi-to-RCE chain can't
  exfiltrate to a remote C2 without first traversing the API
  container, which has its own egress controls.
- **Dropped caps + minimum re-add.** `CHOWN + SETUID + SETGID +
  DAC_OVERRIDE + FOWNER` is the minimum for initdb and for the
  Postgres process to drop its own privileges. We don't carry
  `SYS_ADMIN`, `NET_RAW`, `NET_ADMIN`, etc.

### Consequences

- Positive:
  - Image is ~25 MB. Pulls are fast on slow links.
  - Verifiable provenance (cosign + Rekor + SBOM) without a
    private signing key on a runner.
  - TimescaleDB compromise can't directly egress.
  - `no-new-privileges` plus minimum caps means a SETUID binary
    in the image (there shouldn't be any, but) can't escalate.
  - Operators on docker-compose get a sane default without
    bespoke compose tuning.
- Negative:
  - No shell in the image means debugging is "exec a `nicolaka/
    netshoot` sidecar with the same network namespace" rather
    than "docker exec sh". This is the deliberate tradeoff.
  - Cosign verification depends on Sigstore being up. Operators
    that pull at exactly the wrong moment may have to retry.
  - The compose hardening doc-inline-only comment for `read_only:
    false` is fragile — a future contributor who Doesn't Read The
    Comment may "harden" the DB into a startup loop.
- Follow-ups:
  - Image scanning (Grype) in CI on the built image, gated by
    severity threshold. Tracked separately.
  - Sigstore policy controller / Kyverno admission policies for
    Kubernetes operators — out of scope; we document the cosign
    verify CLI invocation.
  - Reproducible builds via `-trimpath` + pinned toolchain — partly
    in place; full bit-for-bit reproducibility tracked separately.

## More Information

- Implementation commit:
  - `91550d0` security(ci): SBOM, signed container image,
    hardened DB compose, drift bypass closed —
    - F-4.3.1.7: CycloneDX SBOMs via `anchore/sbom-action@v0.17.9`
      (SHA-pinned), one per binary plus source tree, attached to
      both tag and `:latest` releases.
    - F-4.3.1.8: new container job builds + pushes signed multi-
      arch image to `ghcr.io/maltekiefer/monsys-server:<tag>`.
      Keyless cosign via OIDC. Permissions scoped per-job.
      `deploy/docker-compose.prod.yaml` overlay pulls the signed
      image; `docs/RELEASE.md` documents the cosign verify
      command.
    - F-4.3.1.9: TimescaleDB compose service gains
      `security_opt: no-new-privileges:true`, `cap_drop: [ALL]`,
      `cap_add: [CHOWN, SETUID, SETGID, DAC_OVERRIDE, FOWNER]`.
      `read_only` intentionally NOT set; documented inline.
    - F-4.3.1.13: govulncheck pinned to v1.3.0, gosec to v2.26.1.
    - F-4.3.1.14: minisign install in `release.yaml` swapped from
      `apt-get install` to SHA256-pinned upstream v0.11 tarball
      download.
    - F-4.3.1.17: concurrency control added to both workflows.

- References:
  - https://github.com/GoogleContainerTools/distroless — image
    base and nonroot variant.
  - https://docs.sigstore.dev/cosign/keyless/ — keyless signing.
  - https://cyclonedx.org/ — SBOM format.
  - https://anchore.com/sbom-action — generator.
  - OWASP A08:2025 "Software & Data Integrity Failures",
    A05:2025 "Security Misconfiguration".
  - SLSA Level 3 — provenance + isolated build.

- Related: ADR-0007 (the agent counterpart: signed Linux
  binaries with minisign), ADR-0008 (OpenAPI gate is what makes
  the wire contract verifiable next to the deployable),
  ADR-0010 (the runtime auth surface the container exposes).
