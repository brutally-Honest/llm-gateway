---
feature: 031-remove-clutter
kind: chore
status: done
created: 2026-11-22
branch: chore/031-remove-clutter
---
# Remove clutter

## Problem
The repo carries tooling, tests and code it no longer uses: the agent-loop kit (its hooks,
agents, skills, scripts and shell tests, which `make verify` and CI run on every push), a
latency test that never runs under `-race`, the CLI viewer (`gateway sessions`,
`gateway timeline`) that the web UI replaced, the one-off `gateway backfill-sessions`
command, the 016 design mockups, and the gateway's own Docker image path. Each costs
verify time, CI time or reading time.

## Goal
Delete what is unused so `make verify` and CI run faster. Nothing that stays changes
behaviour.

Items: **A** agent-loop kit · **B** never-run latency test · **F** CLI viewer ·
**E** backfill-sessions · **H** 016 mockups · **D** gateway Docker path ·
**I** lint once in CI (ubuntu only). One commit per item, each revertible on its own.

## Non-goals
- Rate limiting, OpenTelemetry tracing and `deploy/observability/` (Prometheus, Grafana,
  Jaeger compose stack, `make observability-up/down`) stay untouched.
- `fillDrawer` (PR 029) and the digest cache (PR 030) stay untouched.
- No edits to existing files under `specs/`, except the one-line mockup notes in
  `specs/016-web-ui-v2/`.
- `make verify` keeps the same steps apart from `kit-test`; the pre-push hook still runs it.
- No new dependency; `deadcode` is run with `go run …@latest`, never added to `go.mod`.
- Schema migrations stay, including the ones backfill relied on.

## Acceptance criteria
- **AC1** The kit is gone: `.claude/{agents,hooks,scripts,skills,templates,templates.local}`
  and `.claude/loop.conf` deleted; `.claude/settings.json` has no hooks, no loop.sh allow
  lines, no deny list, and keeps `"includeCoAuthoredBy": false` and the other allow lines.
  The kit's settings are recorded in `research.md` under "Kit settings".
- **AC2** `make verify` runs lint, test and web-test only; `make setup` no longer checks
  shellcheck; CI installs no bash, jq or shellcheck.
- **AC3** `cmd/gateway/run_hooks_latency_test.go` is deleted; the AC it covered is named in
  `research.md` as having no automated check.
- **AC4** `gateway sessions` and `gateway timeline` are gone with their only-used renderers;
  `gateway dump`, `gateway hooks`, `testdata/sessions` and everything `internal/ui` uses
  stay; no test that stays is deleted.
- **AC5** `gateway backfill-sessions` and `internal/store/backfill*.go` are gone; the
  migrations stay.
- **AC6** `specs/016-web-ui-v2/mockups/` is gone; each 016 file that linked to it says
  "mockups removed in 031, see git history".
- **AC7** `Dockerfile`, `docker-compose.yml`, `.dockerignore`, `test/conventions/compose_test.go`
  and `make image` are gone; docs no longer describe running the gateway in Docker; the
  observability stack and its docs are unchanged.
- **AC8** CI runs golangci-lint only on ubuntu-latest; macos-latest runs `make test` and
  `make web-test`. The job names `verify (ubuntu-latest)` and `verify (macos-latest)` are
  unchanged.
- **AC9** `go vet ./...` and `make verify` are green after every commit. `time make verify`
  before and after (two runs each) and the tracked line count are in `research.md`.
- **AC10** Outside `specs/` and `docs/manual-checks/`, no file refers to a removed thing,
  except the kit-reinstall notes in `AGENTS.md` and `docs/roadmap.md`, or the hit is
  explained in `research.md`.
- **AC11** A fresh review of `git diff main...HEAD` against this spec finds no P1 or P2
  left open.

## Edge cases
- A test that stays uses a helper from a deleted test file → the helper moves to a shared
  `_test.go` file.
- `deadcode` lists functions unrelated to these removals → listed in `research.md`, not
  deleted.
- Exchanges stored before spec 003 have no session key → they stay on the UI's Ungrouped
  page (no backfill).
- Removing the `.gitignore` kit lines exposes local kit leftovers (`.agent-loop/`,
  `.claude/agent-loop-backup-*/`) as untracked → noted, not committed.
- Branch protection requires both CI job names → the matrix and `name:` line stay.
