# ADR-0011: Runtime profiling endpoints + benchmark baselines for hot paths

- Status: Accepted
- Date: 2026-05-12
- Deciders: maintainers
- Context tags: observability, performance, security, build

## Context and Problem Statement

MonSys already exposed `/metrics` (Prometheus) for operational
visibility, but two performance-investigation primitives were
missing:

- **No way to capture a CPU profile or heap snapshot from a running
  prod instance.** The only path was "redeploy with a debug flag",
  which means the symptom is gone by the time the binary is back up.
  A 30s CPU profile during an actual incident is the cheapest tool
  in the box and we didn't have it.
- **No objective baseline for "is this change a perf regression?"**
  PRs touching the ingest path, alert dispatch, bcrypt, or the
  audit-chain hash had no benchmark numbers to point at. Reviewers
  fell back on "looks fine" and we shipped regressions twice this
  quarter that we only caught from `process_resident_memory_bytes`
  drift in Grafana days later.

Forces shaping the decision:

- `net/http/pprof` is in the standard library — zero new dependency
  surface, identical capability to anything we'd roll ourselves.
- Heap dumps leak internal layout (struct addresses, slice
  capacities, string interns); CPU profiles can extend over
  arbitrary windows and tie up a goroutine. Same threat model as
  `/metrics`: useful to operators, dangerous to expose publicly.
- `go test -bench` is also in the standard library and produces
  comparable numbers across runs on the same hardware. The cost
  is one `Benchmark*` function per hot path and a Makefile target.

## Considered Options

1. **`net/http/pprof` behind admin-bearer auth + `Benchmark*`
   coverage for the documented hot paths + `make bench`.**
2. **Continuous profiling (Pyroscope / Parca).** Always-on profile
   ingestion, agent + server components, retention policy, dashboard.
   Real value at scale; massive setup cost for a single-binary
   self-hosted product. Punted.
3. **Expose pprof on a separate internal port.** Operationally
   cleaner (firewall the port off, done) but adds a second listener
   to deployment plumbing (Compose, systemd, k8s). Admin-bearer on
   the same listener is simpler and the auth gate is already proven.
4. **Run benchmarks in CI on every PR.** Too slow (the suite walks
   six packages, each iterating to a stable `b.N`) and too flaky
   (CI runners share hosts; numbers swing 15–30% run-to-run). Manual
   `make bench` for now; a `workflow_dispatch` button later if we
   want a one-button perf comparison.

## Decision Outcome

Chosen: **option 1.** `net/http/pprof` mounted at `/debug/pprof/*`
behind `requireAdminBearer` — the same gate that already protects
`/metrics`. Six `Benchmark*` files cover the documented hot paths:

- `internal/shared/apitypes/json_bench_test.go` — `IngestRequest`
  JSON marshal/unmarshal (every agent push).
- `internal/server/store/ingest_bench_test.go` — ingest encoding
  (per-host hot path).
- `internal/server/store/audit_chain_bench_test.go` — SHA-256 chain
  link (every audit row).
- `internal/server/store/auth_bench_test.go` — bcrypt compare (every
  password login).
- `internal/server/alerts/alerts_bench_test.go` — alerts dispatch
  evaluation loop.
- `internal/server/api/bearer_bench_test.go` — bearer header parse
  (every authenticated request).

`make bench` runs `go test -bench=. -benchmem -run='^$$' ./...`.

### Consequences

- Positive:
  - A 30s CPU profile during an incident is one `curl` away:
    `curl -H "Authorization: Bearer $TOKEN"
    https://host/debug/pprof/profile?seconds=30 > cpu.pprof`.
  - Heap, goroutine, block, mutex, allocs, trace endpoints all
    behind the same gate — full diagnostic surface available.
  - Reviewers can ask "did the bench number change?" on PRs that
    touch ingest, auth, alerts, or audit. The answer is now a
    command, not a guess.
  - Zero new dependencies — `net/http/pprof` and `testing.B` are
    standard library.
- Negative:
  - The admin-bearer gate is the single point of trust. If a session
    bearer leaks to a non-admin, the operator-only profiling surface
    becomes available too. Mitigated by the existing rotation
    contract (ADR-0010).
  - Benchmarks don't run in CI, so a regression can land if the PR
    author doesn't run `make bench` locally. Acceptable today;
    revisit when the suite stabilises.
  - Six bench files means six places to keep up to date as the hot
    paths evolve. Drift will happen; we'll find it when an incident
    forces us to re-run them.
- Follow-ups:
  - `workflow_dispatch` GitHub Action to run `make bench` against a
    PR's head SHA and post numbers — one-button comparison, no
    every-PR cost.
  - Continuous profiling (Pyroscope / Parca) if the deployment
    footprint ever grows past "one binary".

## More Information

- Implementation commit: `ca63e28` feat(perf): add pprof endpoints
  + benchmarks for hot paths.
- Code references:
  - `internal/server/api/api.go` lines 421–436 — the pprof mount
    block, identical admin-bearer gate as `/metrics` (line 419).
  - `Makefile` `bench` target (line 79) — `go test -bench=.
    -benchmem -run='^$$' ./...`.
  - The six `*_bench_test.go` files listed above.
- References:
  - https://pkg.go.dev/net/http/pprof — stdlib package docs.
  - https://go.dev/doc/diagnostics — Go diagnostics guide.
  - OWASP ASVS 5.0 V14.4 "Configuration" — operator-only
    diagnostic endpoints must be authenticated.
- Related: ADR-0001 (bearer model — same gate), ADR-0010 (admin
  session is the single trust anchor for operator surfaces).
