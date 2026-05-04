# Contributing

Thanks for taking the time to contribute. This project favours small,
reviewable changes over large drops.

## Pull-request flow

- Keep PRs **small and focused** on one concern. Split refactors from
  behaviour changes.
- Open a draft PR early if you want feedback on direction before polishing.
- Rebase on `main` before requesting review; avoid merge commits in feature
  branches.

## Pre-flight checks

Before opening a PR, run the same checks CI runs locally:

```sh
go vet ./... && go test ./... && (cd web && npm run build)
```

PRs that fail these will not be merged.

## Commit messages

[Conventional Commits](https://www.conventionalcommits.org/) is preferred
(`feat:`, `fix:`, `chore:`, `refactor:`, etc.) but it is **not enforced**.
Whatever style you use, the subject line should describe the change in the
imperative mood and stay readable in `git log --oneline`.

## Security-relevant changes

If a PR fixes or touches a security-sensitive code path:

- Tag the PR with the `security` label.
- Reference the relevant private GitHub Security Advisory in the PR
  description (e.g. `Fixes GHSA-xxxx-xxxx-xxxx`).
- Do **not** include exploit details in the public PR title or commit
  messages until the embargo lifts. See [SECURITY.md](./SECURITY.md).

## Forbidden: AI attribution lines

Do **not** add AI co-authorship or attribution trailers to commits or files,
including but not limited to:

- `Co-Authored-By: Claude ...`
- `Generated with Claude Code` / `🤖 Generated with ...`
- Any `Co-Authored-By:` line referring to an AI assistant or model.

A `commit-msg` hook lives at `.githooks/commit-msg` and rejects these
trailers. Activate it once per checkout with `make install-hooks` (or
`git config core.hooksPath .githooks`). PRs whose history still contains
them will be asked to rebase clean.

## `agent_config` schema additions

The `agent_config` document is consumed by the agent on every poll. To keep
the agent's attack surface bounded, any new field added to the schema:

- Must be **data**, not **code**: numbers, enums, durations, allow-lists,
  bounded strings.
- Must **not** carry executable scripts, shell snippets, file paths, URLs,
  or anything the agent would dereference, fetch, or execute.
- Must have a documented type, range, and default in the schema definition.

This rule is tracked under Phase 6 AUDIT-074. If you genuinely need
behaviour that looks like code-shaped configuration, open a design issue
first instead of extending `agent_config`.
