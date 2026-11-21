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
