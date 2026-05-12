# Release verification

Every push to a `v*.*.*` tag publishes:

- `mon-agent-linux-{amd64,arm64}` and `mon-server-linux-{amd64,arm64}`
  static binaries on the GitHub Release (plus rolling `latest` for `main`)
- `SHA256SUMS` for those four binaries
- detached `*.minisig` signatures (when the maintainer's
  `MONSYS_MINISIGN_*` secrets are configured in the repo)
- CycloneDX-JSON SBOMs:
  `SBOM-mon-{server,agent}-linux-{amd64,arm64}.cdx.json`, plus a single
  `SBOM-source.cdx.json` for the source tree
- a multi-arch container image at
  `ghcr.io/maltekiefer/monsys-server:<tag>` (and `:latest` on tagged
  pushes), keyless-signed via Sigstore/cosign
- `container-digest.txt` with the immutable digest of the signed image

## Verifying the binaries (minisign)

The `mon-agent` binary embeds the public key it expects to find on its
own updates; for first-install verification do it by hand:

```
curl -fsSLO https://github.com/MalteKiefer/MonSys/releases/download/<TAG>/mon-server-linux-amd64
curl -fsSLO https://github.com/MalteKiefer/MonSys/releases/download/<TAG>/mon-server-linux-amd64.minisig
minisign -V -P "<public-key-from-docs/SIGNING.md>" -m mon-server-linux-amd64
```

## Verifying the container image (cosign keyless)

The release workflow signs each pushed manifest digest via GitHub Actions'
OIDC identity. There is no shared secret; the signer is the GitHub
workflow itself, recorded in Sigstore's public transparency log
(Rekor).

```
TAG=v0.4.2 # whatever you intend to pull
IMAGE=ghcr.io/maltekiefer/monsys-server

cosign verify \
  --certificate-identity-regexp '^https://github\.com/MalteKiefer/MonSys/\.github/workflows/release\.ya?ml@refs/(tags|heads)/.*$' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  "${IMAGE}:${TAG}"
```

A `Verification ok` message means:

- the manifest was signed by a workflow run in this repo
- the signing certificate was issued by the public Sigstore Fulcio CA
- the signature is logged in Rekor (so revocation by transparency works)

After verifying, pin the digest from `container-digest.txt` in your
production compose file:

```
MONSYS_TAG=v0.4.2 docker compose \
  -f deploy/docker-compose.yaml \
  -f deploy/docker-compose.prod.yaml \
  up -d
```

The overlay refuses to start unless `MONSYS_TAG` is set, which is the
mechanism that forces an operator to consciously bump to a verified
version.

## Verifying the SBOM

The CycloneDX-JSON files attached to each release can be loaded into any
SCA tool (Grype, Trivy, OWASP Dependency-Track) for the usual vuln
queries:

```
curl -fsSLO https://github.com/MalteKiefer/MonSys/releases/download/<TAG>/SBOM-mon-server-linux-amd64.cdx.json
grype sbom:SBOM-mon-server-linux-amd64.cdx.json
```

`SBOM-source.cdx.json` covers the Go module graph + npm workspace before
build; the per-binary SBOMs cover what the linker actually pulled in.
