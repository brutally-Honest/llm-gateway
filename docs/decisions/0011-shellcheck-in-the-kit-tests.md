---
status: approved
date: 2026-11-01
---

# 0011 — shellcheck in the kit's tests

## Context
The agent-loop kit under `.claude/` is about 30 bash scripts: the loop, the approval
script, the hooks and their tests. Its behaviour tests run in `make kit-test`, which
`make verify` (ADR 0001) runs last. Nothing linted the scripts themselves. A
`shellcheck -S warning` pass found real defects, among them hooks that carried on in the
wrong directory when `cd` failed. AGENTS.md asks for an ADR before any new dependency.

## Decision
`make kit-test` runs `shellcheck -S warning` over every `.sh` under `.claude/` (except
session worktrees and install backups) before the tests, and any finding fails the run.
shellcheck is a system tool, so it is not pinned. `make setup` reports whether it is
installed, and the kit's test runner fails with an "install shellcheck" message rather
than skipping the lint. Suppressions need the owner's sign-off. The only ones today are a
file-level SC2034 in `lib.sh` and in `tests/helpers.sh`, for variables that the scripts
sourcing them read.

## Alternatives
- **Run shellcheck by hand.** It is free, but nothing would stop the warnings coming back.
- **Skip the lint when shellcheck is missing.** The gate would then pass on a machine
  without it and quietly check less. Failing makes the missing tool visible.
- **Pin shellcheck into `./bin`, as with golangci-lint.** That gives reproducible
  findings, but it is another download per platform for a tool every package manager
  ships. The cost is that a newer shellcheck can bring new findings. If that becomes a
  problem, pin it then.

## Consequences
- Shell defects in the kit are caught before a push, by the same gate as the Go code.
- `make verify` now needs shellcheck installed, as well as jq.
- shellcheck is not pinned to a version, so a newer one may add findings, which then have
  to be fixed or signed off before the gate passes. This is accepted for now; pin it into
  `./bin` if version drift starts to cost more than the download would.
- Reversing is cheap: drop the shellcheck block from `.claude/scripts/tests/run.sh`
  and the `setup-shellcheck` target.
