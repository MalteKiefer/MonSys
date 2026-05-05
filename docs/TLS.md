# Edge TLS Configuration

This document records what `mon` expects from the TLS-terminating
reverse proxy in front of `mon-server`. It pairs with audit findings
AUDIT-601 (TLS expectations) and AUDIT-502 (server-emitted HSTS).

## Topology

```
   Agent / Browser ──https──▶ Reverse proxy ──http (loopback)──▶ mon-server :8080
                              (Caddy / nginx / Traefik)
```

`mon-server` listens on **plain HTTP**, port `8080` by default. It
performs no TLS termination of its own. This is intentional:

- TLS material lives with the proxy where ACME is already solved.
- Operators can swap proxies without rebuilding the server.
- Cipher policy is owned by the proxy, which is updated on a faster
  cadence than the Go release cycle.

The server MUST NOT be reachable on a routable interface without a
proxy in front. Bind it to `127.0.0.1:8080` or to an internal-only
network segment. The shipped `deploy/docker-compose.yaml` already does
this.

## Required edge behaviour

| Property                | Required value                                                              |
|-------------------------|-----------------------------------------------------------------------------|
| Minimum TLS version     | TLS 1.2 (TLS 1.3 preferred)                                                 |
| Cipher suites           | Mozilla "Intermediate" or "Modern" profile only                             |
| Certificate             | Publicly-trusted (Let's Encrypt / ZeroSSL) or operator-pinned internal CA   |
| HSTS                    | Allow through; mon-server emits `Strict-Transport-Security: max-age=63072000; includeSubDomains; preload` |
| OCSP stapling           | Enabled (`stapling on` in nginx, automatic in Caddy/Traefik)                |
| ALPN                    | `h2`, `http/1.1`                                                            |
| Compression             | `gzip` for HTML/JSON OK; **disable** for any endpoint that ever reflects user input (BREACH) — `mon` JSON responses do not, but keep the rule of thumb |
| Forwarded headers       | Send `X-Forwarded-For` and `X-Forwarded-Proto` so the audit log records the real client |

`Strict-Transport-Security` is set by `mon-server` itself
(`internal/server/api/api.go`, after AUDIT-502). The edge can override
it but typically should not — overriding usually means weakening it.
Leave the server header in place and let the proxy pass it through.

## Recommended Caddy configuration

The simplest correct setup, which gets you all of the above for free:

```caddy
mon.example.com {
    encode gzip
    reverse_proxy 127.0.0.1:8080
}
```

Caddy gives you, automatically:

- Let's Encrypt certificate issuance and renewal.
- TLS 1.2+ with the Mozilla intermediate cipher list.
- OCSP stapling.
- HTTP/2 and HTTP/3 (QUIC) on `:443`.
- An `:80 → :443` redirect.

If you front `mon-server` with nginx or Traefik instead, mirror the
properties in the table above. There is a worked nginx snippet in
`deploy/` if you need a starting point.

## Verification: testssl.sh

Quarterly, run [testssl.sh](https://testssl.sh/) against the public
hostname and archive the report. The minimum acceptable result:

```sh
# Pull a tagged release, not main, so the test is reproducible.
docker run --rm -ti drwetter/testssl.sh \
    --severity HIGH --quiet \
    https://mon.example.com
```

Acceptance criteria:

- No `HIGH` or `CRITICAL` findings.
- `MEDIUM` findings are documented with a remediation date or an
  explicit accepted-risk note in the operator runbook.
- `Forward Secrecy` is `OK`.
- `OCSP stapling` is `OK`.

## Quarterly TLS review checklist

Run on the first business day of every calendar quarter:

- [ ] `testssl.sh` against every public `mon` hostname; no HIGH/CRITICAL.
- [ ] Confirm the issuing CA and chain depth match what was deployed.
- [ ] Confirm certificate renewal is automated and the next expiry is
      more than 30 days away (or that the renewal job has run within
      the last 30 days).
- [ ] Confirm the proxy is on a supported minor release (Caddy 2.x,
      nginx mainline, Traefik 3.x). Roll forward if not.
- [ ] Confirm HSTS is still being served end-to-end:
      `curl -sI https://mon.example.com | grep -i strict-transport`.
- [ ] Confirm `mon-server` is **not** directly reachable on the public
      address: `nc -zv mon.example.com 8080` MUST fail.
- [ ] File the report alongside the previous quarter's in your
      operator runbook.

A failure in any item is a Medium finding under the project's own
severity scale and must be remediated within 90 days.
