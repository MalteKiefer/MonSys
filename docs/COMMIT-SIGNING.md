# Commit signing

This document covers Git **commit** signing for contributors. For release
artifact signing (minisign over `SHA256SUMS`) see
[SIGNING.md](./SIGNING.md), which is the authoritative reference for the
release pipeline and operator verification.

The two flows share a goal — proving that what landed in the tree or on
disk came from a maintainer — but they use different tools (Git/SSH or
GPG for commits, minisign for release tarballs) and they sit at
different points in the supply chain. Sign **both**.

## Why we sign commits

- **Audit trail.** The 2026-05-05 security audit flagged 0 of 117
  commits as signed. Authorship in plain git is forgeable with
  `git commit --author='Someone Else <them@example.com>'`. A signature
  binds the commit to a key whose public half lives on the contributor's
  GitHub account.
- **Attribution.** The GitHub "Verified" badge gives reviewers a
  one-glance answer to "did the person whose name is on this commit
  actually author it?" without leaving the PR view.
- **Supply chain.** A merge to `main` triggers release pipelines that
  build root-privileged agent binaries. Signed commits raise the bar
  for a stolen-credential commit-injection attack: an attacker now
  needs the signing key *and* the GitHub session, not just the session.

The CI gate documented below enforces this for new commits going
forward. Maintainers' historical commits are **not** required to be
retroactively re-signed.

## SSH signing (preferred)

SSH signing is the recommended setup for new contributors. It reuses
the SSH key you already have for `git push`, needs no separate keyring
or daemon, and GitHub recognises it for the "Verified" badge.

### One-time setup

```sh
# 1. Tell Git to sign with your existing ed25519 SSH key.
git config --global user.signingkey ~/.ssh/id_ed25519.pub
git config --global gpg.format ssh
git config --global commit.gpgsign true
git config --global tag.gpgsign true

# 2. Maintain an allowed-signers file so `git log --show-signature`
#    can verify your own commits locally. The format is one line:
#       <email> <key-type> <pubkey>
printf '%s %s\n' "$(git config user.email)" "$(cat ~/.ssh/id_ed25519.pub)" \
    >> ~/.git_allowed_signers
git config --global gpg.ssh.allowedSignersFile ~/.git_allowed_signers

# 3. Add the SAME public key to GitHub as a "Signing key" — separate
#    from the "Authentication key" slot, even if the key bytes match.
#    Settings -> SSH and GPG keys -> New SSH key -> Key type: Signing.
```

After your next commit, run:

```sh
git log --show-signature -1
```

You should see `Good "git" signature for <your email>` and the commit
should display the **Verified** badge on GitHub.

### Per-repo override

If your global identity is wrong for this repo, scope it locally:

```sh
cd /path/to/mon
git config user.signingkey ~/.ssh/id_ed25519.pub
git config gpg.format ssh
git config commit.gpgsign true
```

## GPG signing (alternative)

If you already maintain a GPG key — for example because you sign Debian
packages or Tor releases with it — keep using it. The setup is the same
one documented in [SIGNING.md](./SIGNING.md#commit-signing-gpg); the
short version:

```sh
gpg --list-secret-keys --keyid-format=long
git config --global user.signingkey <KEYID>
git config --global commit.gpgsign true
git config --global tag.gpgsign true
gpg --armor --export <KEYID>   # paste into GitHub -> SSH and GPG keys
```

Leave `gpg.format` unset (its default is `openpgp`). Do **not** set
both `gpg.format = ssh` and pass a GPG key id — Git will try to invoke
`ssh-keygen -Y sign` on a GPG-shaped key and fail in confusing ways.

## Local verification

```sh
# Show signature status on every commit in the current branch's history
# back to main:
git log --show-signature main..HEAD

# Quick one-line status per commit (G=good, U=good but unknown trust,
# N=no signature, B=bad, X=expired sig, Y=expired key, R=revoked key):
git log --pretty='format:%h %G? %s' main..HEAD
```

The CI gate accepts only `G` and `U`. Anything else fails the check.

## GitHub "Verified" badge

Once your signing key is registered with GitHub under
*Settings -> SSH and GPG keys*, GitHub re-verifies each pushed commit
server-side and renders a green **Verified** badge in the PR and commit
views. See GitHub's docs:
<https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification>.

Unverified or partially-verified states (yellow "Partially verified",
gray "Unverified") will fail the CI gate the same way an unsigned
commit will.

## CI enforcement

The `commit-signing` job in `.github/workflows/ci.yaml` runs on every
pull request and walks every commit between
`pull_request.base.sha..pull_request.head.sha`. Any commit whose
`%G?` is not `G` or `U` fails the job and blocks merge.

If you see the job fail:

1. Run `git log --pretty='format:%h %G? %s' origin/main..HEAD` locally.
2. For each unsigned commit, rebase and re-sign:
   `git rebase --exec 'git commit --amend --no-edit -S' origin/main`.
3. Force-push the cleaned branch to your fork.

## Advisory pre-commit hook

`make install-hooks` installs an advisory hook at
`.githooks/pre-commit-signing-check` that **warns** (does not block)
when `commit.gpgsign` is unset for the current checkout. It exists to
remind you on first-clone — the CI gate is the actual enforcement
point.
