---
status: approved
date: 2026-11-21
---

# 0019 — CI on Linux and macOS, beside the local gate

## Context
ADR 0001 dropped CI because everything it would check could run on this machine before
a push. That held while the gateway ran only on Linux. Since 023 it also installs on
macOS, and the two Mac checks (`docs/manual-checks/macos-v1.md`, specs 024 and 025) found
test failures that `make verify` on Linux never showed: B6 (locale and path portability),
B7 (a data race on a path that fails only on macOS), B8 (the kit on bash 3.2) and B16
(`/home` is a symlink). The local gate cannot run on an OS the developer is not on.

## Decision
A GitHub Actions workflow (`.github/workflows/verify.yml`) runs `make verify` on
`ubuntu-latest` and `macos-latest` for every pull request and every push to main. The
pre-push hook stays as the gate before a push. CI adds the second OS and a record on
the PR. The workflow takes the Go version from `go.mod` and golangci-lint through
`make setup`, so the Makefile and go.mod stay the only pins. This supersedes
[ADR 0001](0001-local-gate-instead-of-ci.md).

## Alternatives
- **Keep ADR 0001 (local gate only).** It means one place to maintain and no wait after
  a push. But macOS gets checked only when someone runs a manual Mac session, so Mac
  regressions show up late and in batches, as in 024 and 025.
- **A self-hosted Mac runner, or the Mac only before releases.** This means less CI
  time. But it needs a machine kept online, or it finds Mac breakage only at release
  time.
- **Also re-check commit messages in CI** (0001's reversal path). Not done: the
  pre-push hook already checks them, and every commit arrives through this machine's
  push. Add it if commits ever arrive another way (the GitHub web editor, a second
  contributor).

## Consequences
- Each PR waits for a Linux run and a slower macOS run. Lint and tests fail on the PR,
  not on a manual Mac check weeks later.
- Third-party actions are now part of the build. Each is pinned to a full commit SHA,
  and the job has `contents: read` only.
- Node 22 is pinned in the workflow, the one tool version outside the Makefile and
  go.mod. It must still satisfy ADR 0013.
- Cost of reversing: delete the workflow and the README badge. Nothing else depends on it.

2026-11-22: CI no longer installs the kit's tools (Homebrew bash and jq), since the kit was
removed in 031. It still installs shellcheck, which `TestInstallShShellcheck` needs
(031 D3).
