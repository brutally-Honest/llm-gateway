---
status: approved
date: 2026-11-02
---

# 0013 — Node test runner for the web client

## Context
The web client under `internal/ui/web/` is vanilla ES modules with no build step
(ADR 0009). Its logic (ordering, merging, filtering) was only checked by Go tests that
read the files as text. Spec 010 needs behaviour tests that run the real modules.
AGENTS.md asks for an ADR before any new dependency.

## Decision
`make verify` runs `node --test "internal/ui/webtest/*.test.js"` using Node's built-in
`node:test` runner. Node is a test-only runtime, like shellcheck in ADR 0011: no npm, no
`package.json`, no `node_modules`, and nothing of it ships in the binary.
- The glob is quoted so node expands it; the directory form exits 1 on node v25.
- Minimum Node version: 22.12. The test files import plain `export` modules from
  `internal/ui/web/` without a `package.json`, which relies on module-syntax detection
  being on by default (22.7 and later, unflagged in 22.12).
- Tests live in `internal/ui/webtest/`, not `internal/ui/web/`, because that directory is
  `go:embed`ed and walked by `web_test.go`.
- If node is missing, `make verify` fails with an "install node" message, as it does for
  shellcheck. `make setup` only reports whether node is installed.

## Alternatives
- **Keep testing JS as text from Go.** No new tool, but behaviour is never run.
- **A JS test framework from npm.** Adds node_modules and a lockfile for no gain.
- **Skip the tests when node is missing.** The gate would quietly check less.

## Consequences
- `make verify` needs Node 22.12 or later installed.
- Web client logic can be tested by importing the real files.
- Reversing is cheap: drop the `web-test` target and `setup-node`.
