# macOS re-test of the 024 fixes

Run on 2026-11-21 at `8255fdf` (main, after the merge of spec 024), on the same work Mac as
`macos-v1.md`, by hand from a plain Claude Code session that was never routed through the
gateway. Every routed test used `claude -p` in a fresh `zsh -l -i -c` with the personal
login (`CLAUDE_CONFIG_DIR=$HOME/.claude-personal`), in the scratch repo
`~/Desktop/real/gw-mac-test`. No code was changed. Bug numbers B1–B14 are those of
`macos-v1.md`; B15 and B16 are new.

## Environment

| Item | Value |
|---|---|
| macOS | 26.5.2 (25F84) |
| Chip | Apple M4 Pro, arm64 |
| Go | 1.25.4 darwin/arm64 |
| Node | v26.12.0 (Homebrew, `/opt/homebrew/bin/node`) |
| Claude Code | 2.1.330 |
| bash (`/bin/bash`) | 3.2.57 |
| Docker | 29.6.1 (not used) |
| System locale | `LANG=en_IN.UTF-8`, `LC_ALL` unset |
| Gateway before the test | 4d778f4 (the first check's install), running under launchd |
| Gateway after the test | 8255fdf, running, doctor 20 pass |
| Config | `effects.network.sandbox: true`; settings `env` has `NODE_USE_ENV_PROXY=1` |

## Results

| Step | Expected | Seen | Match |
|---|---|---|---|
| 1 `make verify` | Fully green on macOS (B6, B7) | lint 0 issues. `make test`: **1 failing test**, `internal/effects` `TestRoots_LearnedAndRejected` (B16, new; every other package ok, including all 14 former 127.0.0.2 tests in `cmd/gateway`, the EADDRNOTAVAIL test and the install.sh hint test). `make verify` stops there (B9), so the rest was run on its own | no (B16) |
| 1 `make web-test`, default locale | Passes under `en_IN` | 114/114 with `LANG=en_IN.UTF-8` in the shell (the target pins `LC_ALL=en_US.UTF-8 TZ=UTC`). A bare `node --test` without the pin still fails `sessions-page.test.js` (`'10 Nov, 6:09 am'` vs `/^Nov 10, \d/`), as 024 research D9 says | yes |
| 1 B7 race | No race | `go test -race -count=50 -run TestBinary_SignalShutdown ./cmd/gateway`: ok (187 s); no race in the full `make test` either | yes |
| 1 `make kit-test` (B8) | Known Later item; not counted | 14 failed, all `loop.sh: line 91: syntax error near unexpected token ';;'` (94 such lines) | known (B8) |
| 2 `make dist`, serve | 4 archives + SHA256SUMS on 127.0.0.1:8000 | `llm-gateway_8255fdf_{linux,darwin}_{amd64,arm64}.tar.gz` + `SHA256SUMS`; GitHub's `install.sh` byte-identical to the local one | yes |
| 2 Reinstall 1 (4d778f4 → 8255fdf) | Succeeds; gateway never left down (sub-second gap accepted) | `Done: llm-gateway 8255fdf is installed`, exit 0. Plist unchanged, so only `launchctl kickstart -k` (AC1's kickstart-only path). Down 0.58 s by the log (stop 07:33:39.392, start 07:33:39.968); 3 polls refused | yes |
| 2 Reinstalls 2–5 (8255fdf → 8255fdf) | The same | All 4 succeeded, exit 0, each `kickstart -k` only, every file "unchanged". But the gateway was **down 8–9 s each time**: 37, 42, 42, 41 refused polls; log stop → start 8.05 s, 9.04 s, 9.05 s, 9.05 s. Starts are 10 s apart: launchd's spawn throttle (B15, new) | no (B15) |
| 2 Poll totals | — | 277 polls of `/healthz` every 200 ms over 60.1 s: 112 × 200, 165 × refused (`000`), nothing else. Each install waited at "Waiting for …/healthz" and only reported Done once healthy, so none of the 5 left the gateway down at exit | info |
| 3 doctor, agent booted out (B3) | "installed, not loaded" with the exact bootstrap command | `launchctl bootout gui/501/dev.llm-gateway` → `launchctl print` "Could not find service". doctor: `fail Login service installed, not loaded` / `fix: run: launchctl bootstrap gui/501 /Users/racit/Library/LaunchAgents/dev.llm-gateway.plist`; listeners fail "does not answer"; exit 1 | yes |
| 3 Run that command; doctor | Running | `launchctl bootstrap …` exit 0, `state = running`, `/healthz` ok. doctor: 20 pass, `Login service launchd: installed, running, starts at login`, "All required checks pass." | yes |
| 4 Sandbox on, Node fetch and curl (B5) | Both succeed | Only routed session (no other session active; this one is plain). Session `4245b08c`: `node fetch.mjs` → `status 200`, `curl` → `200` | yes |
| 4 Both as network effects of the session | Under that session, not unassigned | Effect 248 `example.com:443` under "#1 Bash node fetch.mjs" (`only one running`); effect 249 `example.com:443` under "#2 Bash curl" (`named`). Sites: `example.com:443`, 2 connections. Also 250 `registry.npmjs.org:443` in the session as "not caused by a tool call" (Claude Code's own). The 13 new unassigned rows (241–247, 251–256) are all Claude Code's harness traffic (api.anthropic.com, mcp-proxy.anthropic.com, Datadog intake), labelled `harness` | yes |
| 4 No rows for the gateway's own ports (B4) | No 127.0.0.1:7195/7197/7198/7199 | 0 rows in the session view and in the unassigned list, although every hook POST went through the sandbox proxy (the first check had 35) | yes |
| 5 Symlink (B1) | Edit shows once, no duplicate root | `/tmp/gw-link → ~/Desktop/real/gw-mac-test`; `claude -p` from `/tmp/gw-link`, Edit appended a line to `notes.txt`. Session `89a05cbe`: `~ modified notes.txt`, once, `named` by "#2 Edit notes.txt"; diff against HEAD is +2 −0 (the first check's own line was never committed). All 9 hook rows have `work_dir` `/Users/racit/Desktop/real/gw-mac-test`; the view has one folder and no `gw-link` spelling anywhere. As in the first check, Claude Code resolves its folder itself | yes |
| 6 `--check` wording (B10) | AC7's text | Ends "Ready to install. Nothing was installed." / "The archive was downloaded to a temporary folder for the check and removed." | yes |
| 6 Uninstall keep-list (B11) | This OS's dependencies only | "every dependency (Claude Code, curl, git, Docker)": no bubblewrap or socat. Run on a pty (`script`); the answer reached it as EOF (`^D` in the typescript), which reads as no: "Stopped. Nothing was changed."; plist and service still there, running | yes |
| 6 `gateway --help`, `gateway doctor --help` (B14) | Plain text | Both plain usage text, exit 0. Also plain: `sessions --help`, `timeline --help`, `dump --help`, `hooks print --help` | yes |
| 6 Port owner without lsof (B13) | Owner "unknown", never "another process" | With `PATH=~/.local/bin:/opt/homebrew/bin:/usr/bin:/bin` (no `/usr/sbin`): `✗ Ports 7195–7199 … 7195 held by an unknown process (lsof not found on PATH, so the owner cannot be named); 7197 …; 7198 …; 7199 …`. With the normal PATH: "held by the running gateway" | yes (see note) |

## Still failing

### B15 — Back-to-back reinstalls leave the gateway down 8–9 s (launchd throttle, new)

Reinstalls 2–5 each came within 10 s of the previous start. launchd waited before starting
the new process each time:

```
shutting down 07:33:39.392  started 07:33:39.968   (reinstall 1: 0.58 s)
shutting down 07:33:41.514  started 07:33:49.566   (reinstall 2: 8.05 s)
shutting down 07:33:51.109  started 07:34:00.152   (reinstall 3: 9.04 s)
shutting down 07:34:01.760  started 07:34:10.815   (reinstall 4: 9.05 s)
shutting down 07:34:12.382  started 07:34:21.434   (reinstall 5: 9.05 s)
```

The gateway itself stops in about 5 ms each time ("shutting down" → "gateway stopped").
Starts come 9.6–10.7 s apart, which matches launchd's default `ThrottleInterval` of 10 s:
a job is not respawned sooner than 10 s after its last start. The plist sets no
`ThrottleInterval`. Reinstall 1 came long after the previous start (2026-11-20 22:38) and
had a 0.58 s gap. What users see: an upgrade after a normal uptime has the accepted
sub-second gap. A second install within 10 s, or an install soon after a KeepAlive restart,
leaves every port refusing for up to about 10 s, though install waits and reports Done
correctly. Not confirmed from launchd's own log (`log show` gave nothing for the label).
Suspected fix: set a small `ThrottleInterval` in the plist (that changes the plist once,
and so takes the bootout/bootstrap path once), or accept and document it next to D13.

### B16 — `TestRoots_LearnedAndRejected` fails on macOS (tests, new)

```
--- FAIL: TestRoots_LearnedAndRejected (0.40s)
    checkpoint_test.go:279: adding /home/u/a: added=true folded=[/System/Volumes/Data/home/u/a/b], want /home/u/a/b folded
FAIL
FAIL	github.com/brutally-honest/llm-gateway/internal/effects	2.714s
```

Fails every run. On macOS `/home` is a symlink to `/System/Volumes/Data/home`. Since the
B1 fix (500a8d1), `rootSet.add` resolves paths (`resolve`, `internal/effects/roots.go:106`,
which resolves the longest existing prefix), so the fixture `/home/u/a/b` is stored as
`/System/Volumes/Data/home/u/a/b`, while the test (`checkpoint_test.go:275-279`) compares
against the literal spelling. The product behaviour is what AC5 asks for: one resolved
spelling. The test is wrong only on hosts where `/home` is a symlink. 024 was checked on
Linux only, so it was not seen there. Suspected fix: build the fixture paths under
`resolve(t.TempDir())`, or compare against `resolve("/home/u/a/b")`.

### B8 — Kit scripts on bash 3.2 (known, roadmap Later)

Unchanged: `make kit-test` 14 failed, `.claude/scripts/loop.sh: line 91: syntax error near
unexpected token ';;'`. Not counted against this re-test.

## Notes

- **B13**: the wording passes AC9. Without lsof the preflight still marks the ports row ✗
  ("stop the process holding the port, then run the installer again"), so with a PATH that
  lacks `/usr/sbin` an install over a running gateway would refuse at preflight. That is
  as specified (AC9 asks only for the wording), and rare: `/usr/sbin` is on the default
  macOS PATH.
- **B2 / AC1**: all five reinstalls took the kickstart-only path (plist unchanged even on
  the 4d778f4 → 8255fdf upgrade). The changed-plist path (bootout, wait, bootstrap with EIO
  retries) was not exercised against real `launchctl` in this run.
- **B9**: `make verify` still stops at the first failing target; each target was run on its
  own.
- Session `4245b08c` was still listed active during step 5. That has no effect on file
  effects, which follow each session's own hooks.
- `/tmp/gw-link` was removed after step 5; the scratch repo keeps the new line in
  `notes.txt`.

## Summary

Fixed on macOS: B1 (symlink), B3 (doctor states), B4 (own ports), B5 (Node under Seatbelt),
B6 (all but one test), B7 (race), B10, B11, B13, B14. Reinstall (B2) no longer fails or
leaves the agent unloaded: 5 of 5 succeeded. Still failing: B15, a launchd throttle that
gives an 8–9 s gap on back-to-back reinstalls, and B16, one macOS-only test failure the B1
fix introduced, which keeps `make verify` red on a Mac. B8 is known.
