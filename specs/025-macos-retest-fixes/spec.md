---
feature: 025-macos-retest-fixes
kind: fix
status: draft
version: 1
created: 2026-11-21
supersedes:
branch: fix/025-macos-retest-fixes
---
# macOS re-test fixes

## Goal
The macOS re-test of the 024 fixes (`docs/manual-checks/macos-v1-retest.md`, 2026-11-21)
found B15 (back-to-back reinstalls leave the gateway down 8–9 s) and B16 (one test fails on
macOS). Its B13 note also showed that with no lsof on PATH a reinstall over the running
gateway refuses at preflight. This fix closes all three.

## Acceptance criteria
- **AC1 (B15)** — The launchd plist sets `ThrottleInterval`, low enough that a reinstall
  soon after a start is not held for launchd's default 10 s. A golden file pins the plist.
- **AC2 (B16)** — `TestRoots_LearnedAndRejected` expects paths resolved the way the code
  resolves them (EvalSymlinks), so it passes where `/home` is a symlink (macOS) and on Linux.
- **AC3 (preflight)** — When a required port is busy and no lsof or ss names its owner,
  preflight asks this gateway's health routes (API `/healthz`, UI `/api/health`). A port
  whose listener answers as this gateway counts as the running gateway's, the reinstall
  goes on, and the ports row says how it was found. Anything else still fails as held by
  an unknown process.
- **AC4** — Real check on Linux: two reinstalls of this build over the running gateway
  (doctor green after), and one with lsof and ss off PATH (goes on, saying it found its
  own gateway).

## Out of scope
- B8 (kit on bash 3.2) and B9 (`make verify` stops at the first failing target).
- Zero-gap upgrades (024 D13 stands).
- Naming a port owner some other way than lsof, ss or the health routes.
