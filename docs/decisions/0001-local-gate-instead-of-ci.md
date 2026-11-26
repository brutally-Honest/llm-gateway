---
status: superseded
date: 2026-09-23
---

# 0001 — Local gate instead of CI

> Superseded by [ADR 0019](0019-ci-on-linux-and-macos.md), which adds CI on Linux and macOS
> beside the local gate. The pre-push gate and the golangci-lint binary install still stand.
> Amended by [ADR 0022](0022-git-notes-stay-local.md): notes are no longer pushed, and the
> pre-push hook now rejects `refs/notes` pushes.

## Context
The gateway is a local, single-user tool: one developer, one machine, one remote.
PLAN.md §7 made CI part of Phase 0 ("CI green on an empty feature PR"), and §11 relied
on CI to re-check every commit message in a PR, as the backstop for a `commit-msg` hook
skipped with `git commit --no-verify`. There are no PRs from other people for CI to
guard. Everything CI would check can run on this machine before a push leaves it.

## Decision
There is no CI. A local gate replaces it:
- `make verify` runs lint, then `go test -race ./...`.
- A `pre-push` hook in `.githooks/` blocks the push if the tree is dirty, if a pushed sha
  is not `HEAD`, if `make verify` fails, or if any commit in the pushed range fails
  `.githooks/commit-msg`. That hook stays the only place the message rules live; the
  pre-push hook calls it once per commit. The checks apply only to branches
  (`refs/heads/*`); notes and tags are pushed unchecked.

golangci-lint is pinned to one version in the Makefile. `make setup` installs it as a
binary into `./bin` (gitignored), not through the `go.mod` `tool` directive, so its
dependency tree stays out of `go.mod`.

## Alternatives
- **Keep GitHub Actions.** It re-checks what the pre-push hook already checks, on the
  same code, for a single user. It adds a second place that pins the Go and lint
  versions, which can drift from the Makefile. It also adds a wait after every push, and
  catches nothing the local gate misses.
- **Drop CI with no backstop.** It is simpler, but a `git commit --no-verify` would then
  let a malformed message into history for good, and nothing would force the tests to
  run before a push.
- **Install golangci-lint through the `go.mod` `tool` directive.** Its version would then
  be pinned in `go.mod`, but its large dependency tree would land in `go.mod` and
  `go.sum` next to the gateway's own. The upstream project also recommends the binary
  install.

## Consequences
- Checks run before code leaves the machine, not after, with no second copy of the
  pipeline to keep in sync.
- `git push --no-verify` bypasses the whole gate. That is accepted: there is one user,
  and the gate is a backstop against mistakes, not against intent.
- The gate checks exactly what is pushed only because it refuses a dirty tree or a
  pushed sha that isn't `HEAD`. Pushing another branch from where you stand means
  checking it out first.
- PLAN.md §7 (Phase 0) and §11 (Enforcement) are updated in the same change.
- Cost of reversing: add a workflow that runs `make verify` plus the same range check
  over `.githooks/commit-msg`. The Makefile and hooks stay as they are, so this is
  cheap.

> Note (2026-11-26, spec 048): PLAN.md §10 and §11 moved to AGENTS.md (Workflow, Commits, Scopes, Enforcement). A citation of either section above now points there. Nothing else in this file changed.
