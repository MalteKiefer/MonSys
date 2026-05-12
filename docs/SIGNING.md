# Signing

This document describes how `mon` signs commits and release artifacts and
what operator responsibilities follow from that. It pairs with the audit
findings AUDIT-102 (commit signing) and AUDIT-401 (release artifact
signing).

## Why we sign

Signing protects two distinct properties:

1. **Authorship integrity.** A signed commit proves that the named author
   pushed the change, and not somebody who guessed an email and a
   `--author` flag. The git log alone is trivially forgeable.
2. **Supply-chain integrity.** A signed release binary lets downstream
   operators verify that the artifact they pulled from a GitHub Release,
   a mirror, or a corporate cache is byte-for-byte the one the
   maintainers cut. SHA-256 sums published next to the binary protect
   against bit-flips, not against a compromised release pipeline; a
   detached signature does.

Both properties matter for an agent that runs as root on every monitored
host. A subverted `mon-agent` binary is a fleet-wide root compromise.

## Commit signing (GPG)

All merges into `main` MUST be signed. PRs from external contributors
are accepted unsigned but are squash-merged by a maintainer whose merge
commit is signed.

> For new contributors: SSH-based signing reuses your existing
> `id_ed25519` key and avoids the GPG keyring entirely. See
> [COMMIT-SIGNING.md](./COMMIT-SIGNING.md) for the SSH path, the CI
> gate that enforces signatures on PRs, and the advisory pre-commit
> hook. The GPG flow below remains supported for contributors who
> already have a key.

### One-time setup

```sh
# 1. Generate or import a key (4096-bit RSA or ed25519 are both fine).
gpg --full-generate-key

# 2. List secret keys and copy the long key id.
gpg --list-secret-keys --keyid-format=long

# 3. Tell git to use it.
git config --global user.signingkey <KEYID>
git config --global commit.gpgsign true
git config --global tag.gpgsign true

# 4. Export the public key and add it to your GitHub account
#    (Settings -> SSH and GPG keys -> New GPG key).
gpg --armor --export <KEYID>
```

Verify with `git log --show-signature -1` after your next commit. GitHub
must show the green "Verified" badge on the commit.

### Per-repo override

If you maintain multiple identities, scope the config to the repo:

```sh
cd /path/to/mon
git config user.signingkey <KEYID>
git config commit.gpgsign true
```

### Branch protection

`main` has branch protection enabled with **Require signed commits**.
Unsigned pushes are rejected by GitHub before they reach the tree.

## Release signing (minisign)

Release tarballs and the `mon-agent` / `mon-server` binaries are signed
with [minisign](https://jedisct1.github.io/minisign/) on top of the
SHA-256 manifest. Minisign was chosen over GPG for releases because the
keypair is single-purpose, the trust model is "trust on first use of
this exact public key" (no web of trust to mismanage), and the
verification command is one line on every supported platform.

The signing job is wired into `.github/workflows/release.yaml` (see
AUDIT-401 for the diff). The public key is committed at
`deploy/release.pub` so operators can verify offline.

### Maintainer setup (one-time, per release-key generation)

```sh
# 1. Generate the keypair locally on an air-gapped or trusted machine.
minisign -G -p release.pub -s release.key

# 2. Commit ONLY release.pub (under deploy/release.pub).
#    The secret key NEVER touches the repo or CI runners.

# 3. Add the secret key to the GitHub Actions secret RELEASE_MINISIGN_KEY
#    (paste the file contents). The release workflow writes it back to
#    a tmpfs file at runtime, signs, then deletes it.

# 4. Add the secret-key passphrase as RELEASE_MINISIGN_PASSWORD.
```

### Operator verification

Every published release ships:

- `mon-agent-linux-<arch>` and `mon-server-linux-<arch>`
- `SHA256SUMS`
- `SHA256SUMS.minisig`

Operators verify with:

```sh
# Fetch the public key once and pin it.
curl -sLO https://raw.githubusercontent.com/MalteKiefer/MonSys/main/deploy/release.pub

# Verify the manifest signature.
minisign -Vm SHA256SUMS -p release.pub

# Verify each binary against the manifest.
sha256sum -c SHA256SUMS --ignore-missing
```

A failed `minisign -Vm` MUST abort the rollout. Do not "just retry from
a different mirror" — that is exactly the threat model the signature
exists to defend against.

## Operator responsibilities

| Task                                    | Cadence                       |
|-----------------------------------------|-------------------------------|
| Rotate the minisign release key         | Every 24 months, or on compromise |
| Store the secret release key offline    | Always (hardware token / air-gapped USB) |
| Confirm `minisign -Vm` in CI before rollout | Every release pulled into your fleet |
| Rotate maintainer GPG keys              | Every 24 months, on suspected leak, or on hardware loss |
| Revoke compromised keys                 | Immediately; publish revocation cert |

When rotating the release key, publish the new public key in a tagged
release whose own `SHA256SUMS.minisig` is signed with the OLD key. This
chains trust through the rotation.

## Expected CI behaviour after AUDIT-401

The `release` job MUST fail closed if any of:

- `minisign` is not on `PATH`
- the secret key cannot be decoded
- the resulting `.minisig` is missing or zero-length

A green release run therefore proves both that the binaries built and
that they were signed. The operator-side check above is the second
half of the chain.
