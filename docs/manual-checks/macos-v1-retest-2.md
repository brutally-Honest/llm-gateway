# macOS re-test of the 025 fixes

Run on 2026-11-21 at `e785afb` (main, after the merge of spec 025), on the same work Mac as
`macos-v1.md` and `macos-v1-retest.md`, from a plain Claude Code session that was never
routed through the gateway, with the personal login. No code was changed. Bug numbers are
those of the earlier records: B15 (launchd throttle), B16 (`TestRoots_LearnedAndRejected`),
the B13 note (preflight without lsof) and 025 AC5 (crash loop).

## Environment

| Item | Value |
|---|---|
| macOS | 26.5.2 (25F84) |
| Chip | Apple M4 Pro, arm64 |
| Go | 1.25.4 darwin/arm64 |
| Node | v26.12.0 |
| Claude Code | 2.1.330 |
| bash (`/bin/bash`) | 3.2.57 |
| System locale | `LANG=en_IN.UTF-8` |
| Gateway before the test | 8255fdf (the last re-test's install), running under launchd, plist without `ThrottleInterval` |
| Gateway after the test | e785afb, running, doctor 20 pass |

`/healthz` was polled every 200 ms (Python, 150 ms timeout) during steps 2 and 3. Gaps are
given both from the gateway log ("shutting down" → "gateway started") and from the polls.

## Results

| Step | Expected | Seen | Match |
|---|---|---|---|
| 1 `make verify` | Green apart from B8 | lint 0 issues; `make test` every package ok (`internal/effects` included); `make web-test` 114/114 under `en_IN`; `make kit-test` 14 failed, all `loop.sh: line 91: syntax error near unexpected token ';;'` (94 lines). `make verify` exit 2 at kit-test only | yes (B8 known) |
| 1 B16 | `TestRoots_LearnedAndRejected` passes | `go test -count=1 -run TestRoots_LearnedAndRejected -v ./internal/effects`: PASS (0.38 s) | yes |
| 2 `make dist`, serve | Archives + SHA256SUMS on 127.0.0.1:8000 | `llm-gateway_e785afb_{linux,darwin}_{amd64,arm64}.tar.gz` + `SHA256SUMS`; GitHub's `install.sh` byte-identical to the local one | yes |
| 2 Upgrade 8255fdf → e785afb | Plist changes, so bootout → wait → bootstrap | exit 0, `Done: llm-gateway e785afb is installed`, `Upgrade 8255fdf → e785afb`, `service updated …/dev.llm-gateway.plist`. Plan ran `launchctl bootout`, `enable`, `bootstrap`, `kickstart` (no EIO retry needed). New plist has `<key>ThrottleInterval</key><integer>1</integer>`. launchd after: `runs = 1`, running | yes |
| 2 Gap | Sub-second | Log: stop 08:23:21.578 → start 08:23:22.359, **0.78 s**. Polls: 38, 34 × 200, 4 × refused (one window, ~0.82 s) | yes |
| 3 Reinstalls 2–5 (e785afb → e785afb, back to back) | Each sub-second, never ~9 s | All 4 exit 0, plist "unchanged", `kickstart -k` only. Log gaps: **51 ms, 53 ms, 38 ms, 45 ms** (starts 1.6–2.6 s apart, so well inside launchd's old 10 s window). Polls: 52, 49 × 200, 3 × refused (one per reinstall 3–5; reinstall 2's 51 ms gap fell between polls) | yes |
| 4 Reinstall, no lsof on PATH | Goes on, says it found its own gateway | `PATH=~/.local/bin:/opt/homebrew/bin:/usr/bin:/bin:/sbin` (no `/usr/sbin`, so no `lsof`; no `ss` on macOS). exit 0: `✓ Ports 7195–7199 required free on 127.0.0.1 (7195, 7197, 7198, 7199 held by the running gateway: no lsof or ss to name the owner, but it answers its health routes)`; `kickstart -k`, Done; log gap 47 ms | yes |
| 5 Restart rate | Every ~10 s, not every second | Config backed up, `listen_addr: 0.0.0.0:7197`, `launchctl kickstart -k`. Over 62 s, `launchctl print` once a second: new pid at 08:24:09, :19, :29, :39, :49–50, :59, 08:25:09 (runs 7 → 13), i.e. **one start per ~10 s** | yes |
| 5 Error in the log | Once | 4 new lines in `gateway.log` over the 62 s (and over the whole ~90 s loop): the kickstart's clean stop (3 lines) and **one** `"invalid config","key":"listen_addr" … "not loopback"` error at 08:24:09.098 | yes |
| 5 `startup-error.json` | Exists | `~/.local/state/llm-gateway/startup-error.json`, mode 0600, 639 bytes: `error` = the loopback error, `logged_at` 08:24:09.098, `failed_at` 08:25:09.386 (updated on each retry, not re-logged) | yes |
| 5 doctor during the loop | "service is restarting after a startup error" with the loopback error | exit 1. `fail Login service service is restarting after a startup error: invalid config (key=listen_addr, line=6, reason=not loopback: …, value=0.0.0.0:7197)` / `fix: fix the error named (see the log: …); the service tries again every 10 s on its own`. Also `fail Config … listen_addr "0.0.0.0:7197": not loopback … (line 6)`; the four listeners and Hooks fail "fix the config first" / "start the gateway" | yes |
| 5 Restore, recovers by itself | Comes back on the next retry, doctor green, file gone | Backup restored 08:25:34 (`cmp` identical). `/healthz` 200 at 08:25:39 with no command run ("gateway started" 08:25:39.527; `runs = 16`, `last exit code = 2`). `startup-error.json` gone. doctor exit 0, 20 pass, `Login service launchd: installed, running, starts at login`, "All required checks pass." Still exactly 1 error line in the log | yes |

## Still failing

### B8 — Kit scripts on bash 3.2 (known, roadmap Later)

Unchanged and not counted:

```
.claude/scripts/loop.sh: line 91: syntax error near unexpected token `;;'
.claude/scripts/loop.sh: line 91: `	last=$(for t in $(task_ids "$(art tasks)"); do case $(state_get "$t") in PASS | NEEDS-HUMAN) echo "$t" ;; esac; done | tail -1)'
...
14 failed
make[1]: *** [kit-test] Error 1
make: *** [verify] Error 2
```

Nothing else fails.

## Notes

- **B15** is fixed: the upgrade took the changed-plist path (bootout, bootstrap) against
  real `launchctl` for the first time on this Mac, and it worked first try with a 0.78 s
  gap. The back-to-back reinstalls are now 38–53 ms, against 8–9 s before.
- The changed-plist path's gap (0.78 s) is larger than the kickstart path's (~50 ms), as
  expected from bootout plus bootstrap; within the accepted sub-second gap (024 D13).
- `startup-error.json`'s `key` field holds the error line's fields as a JSON string. That
  is its documented role (`internal/startup/startup.go:63`, the once-per-10-minutes key),
  not a leak: it holds only the same fixed fields as `error`.
- The crash loop's `launchctl print` showed `state = running` at every sample, since each
  failed start lives 10 s (AC5) before it exits.
- The config backup was removed after the restore; `config.yaml` is byte-identical to the
  one before step 5.

## Summary

All of 025 passes on macOS: B15 (reinstall gaps sub-second, on both the changed-plist and
the kickstart paths), B16 (the test passes, `make test` green), the preflight without lsof
(goes on, naming its own gateway by its health routes) and the crash-loop backoff (one start
per ~10 s, one log line, doctor names the error, recovers by itself). Only B8 remains, known.
