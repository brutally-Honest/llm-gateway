---
feature: 026-github-actions
kind: chore
status: draft
version: 1
created: 2026-11-21
supersedes:
branch: ci/026-github-actions
---
# GitHub Actions CI

## Goal
`make verify` runs on GitHub Actions for every pull request and every push to main, on
Linux and macOS. The local gate (pre-push hook) runs only on the developer's Linux
machine, and 024/025 showed that macOS breaks in ways Linux never sees. ADR 0019 replaces
ADR 0001.

## Acceptance criteria
- **AC1** — `.github/workflows/verify.yml` runs on `pull_request` and on `push` to main,
  as a matrix of `ubuntu-latest` and `macos-latest` (Apple silicon). Each job installs
  what `make verify` needs: Go from `go.mod`, Node 22 (≥ 22.12), jq, shellcheck, and
  golangci-lint through `make setup`. Then it runs `make verify`.
- **AC2** — On macOS, Homebrew's bash comes first on PATH, so the kit's scripts run
  under bash ≥ 4 (B8). If they still fail, the failure is visible and explained in
  research.md, never skipped.
- **AC3** — Go modules, the Go build cache and the golangci-lint binary are cached.
  Every action is pinned to a full commit SHA, with its version in a comment.
  `permissions: contents: read`, and each job times out after 20 minutes.
- **AC4** — No test depends on things a CI runner lacks (a systemd user session, the
  launchd GUI domain, Docker, a real Claude Code, network beyond module downloads). Any
  test that does skips with a clear reason when the thing is missing, and still runs
  locally. Each one is listed in research.md.
- **AC5** — README.md shows the workflow's badge at the top.
- **AC6** — `docs/release-checklist.md` lists the steps before every release.
- **AC7** — Real check: the PR's two jobs pass, with their run times recorded.

## Out of scope
- Releases from CI (tags, `make dist` uploads). The release stays manual (AC6).
- A CI re-check of commit messages (ADR 0019 says why).
- Fixing B8 in the kit itself.
