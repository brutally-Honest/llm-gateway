---
feature: 025-macos-retest-fixes
---
# Research & decisions — 025-macos-retest-fixes

## Open questions
<!-- Answer by changing "(open)" to "(answered)" and writing the answer underneath. -->

## Evidence (main at 0863913, 2026-11-21)

The bugs and their output are in `docs/manual-checks/macos-v1-retest.md` (B15, B16 and
the B13 note).

## Decisions
- D1 (2026-11-21) — Built by hand, no agent-loop kit and no /al-* commands, as in 022–024:
  spec.md stays a draft, no plan.md or tasks.md. Unclear details take the simplest reading
  that fits and get a D-entry here. Commit scopes as in 024 D1; `capture` for
  `internal/effects`, as in its earlier commits.
- D2 (2026-11-21) — B15: `ThrottleInterval` is 1 s. launchd spawns a job no more than once
  per `ThrottleInterval` (default 10 s), and `kickstart -k` was held by it: the re-test's
  reinstalls 2–5 each stopped the gateway about 1.5 s after its last start and waited
  8–9 s. With 1 s, a reinstall that comes 1 s or more after a start is not held at all,
  and one sooner waits under a second, inside the gap 024 D13 accepted. The cost is on
  the crash side: KeepAlive (`SuccessfulExit` false) restarts the gateway after any
  failing exit, and launchd, unlike systemd (`StartLimitBurst`, 5 in 10 s by default),
  never gives up. A gateway that fails at start (a broken config, a port taken) is
  restarted once a second instead of every 10 s, writing a few lines to `gateway.log`
  each time. That is accepted: one failed start a second is cheap, doctor
  shows the job loaded but not running and points at the log, and 0 (no throttle at all)
  was rejected because nothing would then bound a crash loop. 2 s was rejected because
  the re-test's own pattern (a reinstall 1.5 s after a start) would still wait.
- D3 (2026-11-21) — The plist changes (one new key), so the first upgrade of a Mac to this
  build finds a changed plist and takes 024 AC1's changed-plist path: `bootout`, the poll
  of `launchctl print` until the job is gone, `bootstrap` with its EIO retries, then
  `kickstart`. The re-test never ran that path against real `launchctl` (all five of its
  reinstalls were kickstart-only), so the next Mac upgrade is its first real run, and
  its result should be recorded. Later upgrades are kickstart-only again until the plist
  changes.
- D4 (2026-11-21) — B16: the test now expects `resolve("/home/u/a/b")`, the same function
  `rootSet.add` stores paths with. On Linux that is the literal path, on macOS
  `/System/Volumes/Data/home/u/a/b`. The other assertions in the test already used
  `resolve(t.TempDir())` or compare rejections only. This Linux machine has no symlinked
  `/home`, so the fix is confirmed on macOS only.
- D5 (2026-11-21) — Preflight's own-gateway check: only when no tool named the owner
  (lsof and ss both missing, or both answered nothing), preflight GETs
  `http://127.0.0.1:7197/healthz` and `http://127.0.0.1:7199/api/health` once each and
  compares the body with the gateway's exact one (`{"status":"ok"}`, `{"ok":true}`), so a
  status 200 from another service is not enough. 7197 is the gateway's if the API
  answers, 7199 if the UI does, and the proxy ports 7195 and 7198, which have no health
  route, if either answers. 7196 is never the gateway's: nothing binds it, so a holder
  there still fails. The row then reads "held by the running gateway: no lsof or ss to
  name the owner, but it answers its health routes". The URLs use the default ports
  because the checked port range is the fixed 7195–7199. Known limit: a process that
  holds 7195 or 7198 while this gateway answers on 7197 is taken for the gateway; the
  gateway then fails to bind at restart, and install reports it did not answer /healthz.
- D6 (2026-11-21) — Correction to D5, found in the Linux real check: right after a
  restart the UI holds 7199 but does not answer `/api/health` while it warms up (doctor:
  "warming up, about 4 s since the gateway started"), so D5's per-port rule read 7199 as
  an unknown process, and a second reinstall within the warm-up with no port tool would
  have refused. Now if either health route answers as this gateway, all four of its
  listener ports (7195, 7197, 7198, 7199) are its own; 7196 still never is. D5's known
  limit widens to 7199: a foreign process on it while this gateway answers on 7197 is
  taken for the gateway.

## Real check (Linux, 2026-11-21)

Ubuntu, systemd user service, gateway running before each step. `/healthz` was polled
every 200 ms in the background. "No lsof/ss" means a PATH whose system directory is a copy
of `/usr/local/{s,}bin` and `/usr/{s,}bin` as symlinks, minus `lsof` and `ss`
(`command -v` finds neither). Builds: ccb61ce (round 1, before D6) and e7aff24 (round 2,
the branch's final code).

| Step | Expected | Seen | Match |
|---|---|---|---|
| Round 1, reinstall 1 (7cd17e4 → ccb61ce) | installs, ports row names the running gateway | exit 0, "7195, 7197, 7198, 7199 held by the running gateway", binary copied, `Done` | yes |
| Round 1, reinstall 2 | the same | exit 0, the same row, `Done` | yes |
| Round 1, `gateway doctor` | green | exit 0, "All required checks pass."; warn: AppArmor/bwrap, hand-written function in `~/.bashrc`, UI warming up 4 s after restart | yes |
| Round 1, reinstall 3, no lsof/ss | goes on, saying it found its own gateway | exit 0, "… held by the running gateway: no lsof or ss to name the owner, but it answers its health routes", `Done` | yes |
| Round 1, `--check`, no lsof/ss, python on 7196, ~10 s after reinstall 3 | fails on 7196 only | ✗ on 7196 **and 7199** "held by an unknown process": the UI was still warming up | no → D6 |
| Round 1, polls | sub-second gaps | 690 polls, 689 × 200, 1 refused | yes |
| Round 2, reinstalls 1 and 2 (ccb61ce → e7aff24 → e7aff24), 1.5 s apart | install, gateway row | both exit 0, `Done`; stop → start 40 ms and 47 ms | yes |
| Round 2, `gateway doctor` | green | exit 0, all required pass, same three warns | yes |
| Round 2, reinstalls 3 and 4, no lsof/ss, 1.2 s apart (4 inside the UI warm-up) | both go on, saying they found their own gateway | both exit 0 with the health-routes row, `Done`; stop → start 42 ms and 50 ms | yes |
| Round 2, `gateway doctor` | green | exit 0, "All required checks pass." | yes |
| Round 2, `--check`, python on 7196, no lsof/ss | fails as an unknown process, gateway ports not flagged | exit 1, "7196 held by an unknown process (ss or lsof not found on PATH …)" only | yes |
| Round 2, the same with the normal PATH | owner named | "7196 held by python3 (pid 1663341)" | yes |
| Round 2, polls | sub-second gaps | 113 polls, 112 × 200, 1 refused | yes |

Only the Mac can confirm:
- B15: back-to-back reinstalls under launchd now restart within about a second (the plist
  key is pinned by the golden file; launchd's handling of it is not seen on Linux).
- D3: the first upgrade to this build takes the changed-plist path (bootout, wait,
  bootstrap with EIO retries, kickstart) against real `launchctl`, and doctor is green
  after it.
- B16: `TestRoots_LearnedAndRejected`, and so `make verify`, green where `/home` is a symlink.
- The own-gateway check with `/usr/sbin` off a macOS PATH (lsof only, the darwin wording).

## Wrap-up (2026-11-21)

AC1–AC3 are built, each with a test that failed before its fix (B16's only fails on a
host with a symlinked `/home`, so its failure is the re-test's own output). The Linux real
check (AC4) found one bug, the warming UI port (D6), now fixed and re-checked. `make
verify` is green. No open questions.
