# ADR-0008: OpenAPI as source of truth + spec-drift CI gate

- Status: Accepted
- Date: 2026-05-12
- Deciders: maintainers
- Context tags: build, api, types, ci

## Context and Problem Statement

MonSys has a Go (huma) API on one side and a TypeScript SPA on the
other. Three places want to know the shape of the wire:

1. The server's request/response types (`internal/shared/apitypes`).
2. The committed `api/openapi.yaml` — used by external clients
   (CLI, docs, third-party tooling) and as the version-controlled
   contract.
3. The SPA's `web/src/lib/api/types.ts` — the SPA's view of the
   server.

Without a gate, these three drift. A developer adds a field to a
`apitypes` struct, forgets to regenerate the spec, ships a feature
that the SPA can't see because the TS types don't expose the new
field. Or the reverse: the SPA author hand-edits the TS types to
"the shape I want" and the server returns a different one.

Forces:

- huma generates an OpenAPI spec from struct tags. Source of truth
  lives next to the handlers.
- `openapi-typescript` (added as a dev-dep in `01462b2`)
  generates `web/src/lib/api/types.ts` from the spec.
- CI must fail on drift, not just regenerate-and-commit silently.
- The committed spec must be reproducible bit-for-bit from a clean
  build, or the drift gate is noise.
- ldflag-stamped version strings (release builds inject `version
  =vX.Y.Z`) leak into the spec's `info.version` field and produce
  spurious diffs.

## Considered Options

1. **Hand-written `openapi.yaml`** as source of truth, generate Go
   structs and TS types from it. Cleanest if you're API-first; we're
   not — handler code drives the contract.
2. **Hand-written Go structs as source of truth, generate spec +
   TS types.** Pick a Go-side framework that emits OpenAPI.
3. **Both sides hand-written, vendored.** Drift inevitable.
4. **Spec + TS generated, CI gate on `git diff --exit-code` after
   regen.** Lock the contract by reproducibility.
5. **`yq sort_keys` pass on the generated YAML** to canonicalise
   key order. (Tried; backfired — see below.)
6. **`--print-spec` directly with `version=dev` forced.** Bypass
   the version-injection problem.

## Decision Outcome

Chosen: **option 2 + option 4 + option 6** — huma generates the
spec from Go struct tags, `openapi-typescript` generates the TS
types from the spec, CI runs both regens and `git diff
--exit-code`s. The spec generator forces `version.Version = "dev"`
before constructing the API surface, so ldflag stamping cannot
produce a different committed spec.

Explicitly **dropped** `yq` normalisation (option 5).

Rationale:

- **Handler-driven types are how the team actually works.**
  Adding a field is "edit `apitypes`, run `make generate-spec`,
  commit both". API-first would invert the workflow and we'd
  drift in the other direction.
- **`--print-spec` is one command, deterministic.** No
  pre-processing, no normalisation, no environment-sensitive
  YAML pretty-printer. Huma emits sorted keys by default.
- **The `yq` normalisation step was a bug, not a fix
  (`376ec63`).** We were running `mikefarah-yq` with `sort_keys`
  to canonicalise. It was reordering *nothing* (huma already
  emits sorted keys) but it *was* rewriting `$ref` values from
  double-quoted to single-quoted. That produced a diff against
  any local regen that didn't use the same yq build. Worse, the
  Python `kislyuk/yq` that ships on most distros doesn't
  understand mikefarah's flags at all, so the Makefile target
  broke locally. Dropping `yq` makes `--print-spec` the single
  source of truth.
- **`--print-spec` + `--dump-openapi` force `version.Version =
  "dev"` (`91550d0` F-4.3.1.11).** The committed spec is
  reproducible regardless of which tag the developer is sitting
  on. A release build that stamps `version = v1.2.3` would
  otherwise embed `info.version: v1.2.3` in the YAML and produce
  drift against CI's bare `go run`. Forcing `dev` removes that.
- **Makefile target uses temp-file-plus-move
  (`376ec63`).** `go run … --print-spec > $tmp && mv $tmp
  api/openapi.yaml` so a failed regen no longer truncates the
  committed spec.

### Consequences

- Positive:
  - Three sources line up by construction. A new field requires
    a regen, and the regen is one command.
  - CI fails loudly on drift, not silently on "yeah it works
    locally".
  - Release builds and bare `go run` produce the *same* committed
    spec. ldflag stamping affects only binaries, not the
    contract.
  - External consumers of `api/openapi.yaml` can rely on it as
    the authoritative shape.
- Negative:
  - Every feature PR carries two generated files
    (`api/openapi.yaml` + `web/src/lib/api/types.ts`). Reviewers
    skip them; the gate catches "forgot to regen".
  - Tooling regression risk: an upgrade to huma or
    openapi-typescript can produce a different-but-equivalent
    YAML/TS, which we then commit and move on. Pinning the
    tooling in `go.mod` + `package.json` mitigates.
  - Hand-written aliases or vendor-extensions in the YAML are
    not possible — the spec is fully generated.
- Follow-ups:
  - Vendor-extensions (`x-` fields) for clients that want extra
    metadata — would require huma support or a post-process step
    we've explicitly rejected.
  - Schema-level Pact / consumer-driven contracts for the SPA —
    out of scope.

## More Information

- Implementation commits:
  - `376ec63` ci: drop yq from spec-drift pipeline; use
    `--print-spec` directly — drops yq from
    `.github/workflows/ci.yaml` and `Makefile`; Makefile target
    writes through a temp file + mv so a failed regen no longer
    truncates the committed spec.
  - `91550d0` security(ci): SBOM, signed container image,
    hardened DB compose, drift bypass closed — F-4.3.1.11 forces
    `version.Version = "dev"` in both `--print-spec` and
    `--dump-openapi`. Drift cannot be hidden behind ldflag
    stamping.
  - `86ed1a9` chore(spec): regenerate openapi + ts types for rule
    groups; `5baeeda` chore(spec): regenerate openapi.yaml + ts
    types for webauthn endpoints; `59efacd` chore(spec):
    regenerate openapi.yaml + ts types for new condition_types —
    representative regen commits that prove the workflow.

- References:
  - https://huma.rocks — Go framework, OpenAPI emission.
  - https://openapi-ts.dev — `openapi-typescript` generator.
  - OWASP API Security Top 10:2023 / 2025 — API1 "Broken Object-
    Level Authorization" is mitigated by typed wire contracts
    that don't drift from server expectations.

- Related: ADR-0004 (rule groups schema landed via this gate),
  ADR-0009 (SBOM + signed image are the deployable counterpart
  to the verifiable wire contract).
