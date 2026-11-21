---
feature: 024-macos-fixes
kind: fix
status: draft
version: 1
created: 2026-11-21
supersedes:
branch: fix/024-macos-fixes
---
# macOS v1 fixes

## Goal
The macOS v1 check (`docs/manual-checks/macos-v1.md`, 2026-11-20) found the v1 blockers
B1–B7, B10, B11, B13 and B14. This fix closes them, each with a test that fails before the
fix. B8 (the agent-loop kit on bash 3.2) and B12 (hooks-only sessions) go to the roadmap's
Later list.

## Acceptance criteria
- **AC1 (B2)** — Reinstall never leaves the gateway down. launchd: an unchanged, loaded plist
  gets `kickstart -k` only. A changed plist gets `bootout`, then a poll of
  `launchctl print gui/$UID/<label>` until the job is gone (bounded), then `bootstrap`,
  retried on EIO with a short backoff. If it still fails, install says the gateway is not
  running and prints the exact command to run.
- **AC2 (B3)** — doctor tells "not loaded", "loaded but not running" and "running" apart on
  launchd and systemd, each with its own exact fix command.
- **AC3 (B5)** — The Claude Code settings `env` block sets `NODE_USE_ENV_PROXY=1` on both
  OSes. Install's summary and `docs/install.md` say which Node versions get network effects.
- **AC4 (B4)** — Connections to the gateway's own listener addresses are not recorded as
  network effects. Other loopback traffic still is.
- **AC5 (B1)** — Effects compare symlink-resolved paths; one folder is never watched under
  two spellings.
- **AC6 (B6, B7)** — The Go and web tests pass on macOS: a second listener uses 127.0.0.1
  with its own port; refused dials accept ECONNREFUSED or EADDRNOTAVAIL; the install.sh
  hint is expected per OS; the web tests pin locale and time zone; the stderr race in
  `binary_test.go` is gone (50 runs under `-race`).
- **AC7 (B10)** — `--check` ends with "Nothing was installed. The archive was downloaded to a
  temporary folder for the check and removed."
- **AC8 (B11)** — Uninstall's keep-list names only this OS's dependencies.
- **AC9 (B13)** — With no port tool, a busy port's owner is "unknown", never another process.
- **AC10 (B14)** — Subcommands print plain text, never JSON log lines.

## Out of scope
- B8, B12 (roadmap Later). B9 (`make verify` stops at the first failing target) is noted only.
- Zero-gap binary upgrades (socket hand-over); a restart for a new binary stays a restart.
- The doc problems D1–D6 of the check, except D4 (B5 changes step 11's premise).
