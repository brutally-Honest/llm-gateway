---
feature: 036-macos-stream-test-hang
---
# Research & decisions — 036-macos-stream-test-hang

## Goroutine dump of the hung test (AC1)

Taken from the macOS job logs (`gh api repos/{owner}/{repo}/actions/jobs/<id>/logs`) of
jobs 137143761606 (run 45001477758, b4b2d6c), 137181661164 (run 45013800880, 5120e65)
and 137449351447 (run 45100294664, PR #65 at 24e0f0c). All three dumps have the same
shape. Line numbers are from b4b2d6c, which match main today.

| Goroutine | Blocked in | Meaning |
|---|---|---|
| test (5299) | `io.ReadAll` ← `rlPost` (`run_ratelimit_test.go:69`) ← `waitLimited` (`:83`) ← test (`:364`) | The test already closed the first stream and is reading the response of a **probe** call made by `waitLimited`, on a new client connection |
| gateway handler | `responseWatcher.Read` (`internal/core/meta.go:228`) ← `ReverseProxy.copyResponse` ← `Proxy.ServeHTTP` (`proxy.go:212`) ← ratelimit middleware (`middleware.go:45`) | The probe was **admitted** by the limiter and is copying an upstream stream |
| upstream handler (5367) | `select` at `run_ratelimit_test.go:339` | The fake upstream sent `message_start` for the probe and waits for its request context or the test's `done` |
| gateway server conn readers (5370, 5371) | `connReader.backgroundRead` | Both connections are open and idle; nobody is leaving |
| rest | accept loops, sinks, hub, sql openers | idle |

Counts in each dump: one upstream handler at `:339`, one `Proxy.ServeHTTP`, one
`responseWatcher.Read`, one `waitLimited`. So the **first** stream, the one the test
disconnected, was over: its upstream handler and its proxy goroutine are gone. The
goroutine stuck in `responseWatcher.Read` belongs to the probe, not to the disconnected
stream.

## When it started

macOS job of every verify run on main and PRs from 2026-11-21 (`gh run list --workflow
verify --limit 60`, then each job's log). Runner image `macos-26-arm64` 20261111.635
and go1.25.4 darwin/arm64 throughout the window, so neither changed.

| Run | Commit | macOS | `cmd/gateway` time |
|---|---|---|---|
| 44874849545 | 79e68a8 main | ok | 49.1 s |
| 44930187463 | 5ebd7ce main | ok | 46.2 s |
| 44994090337 | bb06223 PR 028 | ok | 45.2 s |
| 44994716991 | 3566577 main (028 merge) | ok | 40.6 s |
| 44999122518 | ab5860f PR 031 | ok | 28.9 s |
| 45000072768 | 659bc4a main (031 merge) | ok | 23.9 s |
| 45001101948 | 1bf5c0e PR 032 | ok | — |
| **45001477758** | **b4b2d6c main (032 merge)** | **hang** (first) | 600 s |
| 45010310488 | 92c9556 PR 029 | ok | — |
| 45010757947 | a5eade1 main | ok | 26.4 s |
| 45012460600, 45013325915 | PR 033 | ok | — |
| **45013800880** | **5120e65 main** | **hang** | 600 s |
| 45040753802 | f80a171 PR 034 | ok | 31.1 s |
| **45100294664** | **24e0f0c PR 034** | **hang** | 600 s |
| 45104006749 | 6761cc4 PR 034 | ok | 32.2 s |
| 45104601139 | a560277 main | fail, other test (`internal/ui` `TestStream_ReplayOf20CallsResendsAtMostThePreviousCall`) | 31.2 s ok |

The 026 PR failures (44862083462, 44863192822) have no timeout and are unrelated.

So the first hang is b4b2d6c, three runs after 028's merge (3566577, green on macOS) and
right after 031 and 032. The test itself is unchanged since 63eb676 (2026-10-13).
028 changed `requestWatcher`, which is not on the path that hangs (below). Before 028 an
admitted probe would have hung the same way, or failed when 028's bug cut its stream;
neither happened in the runs before, so 028 did not hide the race. It just was not
lost there. The `cmd/gateway` package got faster on macOS at 031 (≈45 s → ≈25 s, about
1,600 lines of viewer and backfill tests deleted from `cmd/gateway` and the timeline
package), which changes how much other work runs next to the test. That is a likely
reason the race is lost more often now, but not a cause. Rate: 0 of 7 macOS runs
before b4b2d6c, 3 of 10 from b4b2d6c on.

## The test and the chain that should unblock it

`TestRun_ClientDisconnectMidStreamRecordsSeenTokens` (`cmd/gateway/run_ratelimit_test.go:333`)
starts a gateway with `tpm: 50` and a fake upstream whose handler sends one
`message_start` (101 tokens) and then blocks until **its request context ends** or the
test's `done` closes (closed only in `t.Cleanup`). It applies to **every** call that
reaches upstream, not just the first. The test reads to the first `data:` line, closes
the body before EOF, then calls `waitLimited`, which posts again and again until a 429.
Each post is `rlPost`, which `io.ReadAll`s the response with no timeout.

Expected chain once the test closes the first body:

1. `res.Body.Close()` before EOF: the client transport closes the TCP connection (FIN).
2. The gateway's `http.Server` connection has a `backgroundRead` pending (the request
   body was read whole). It reads EOF and cancels the request context.
3. `ReverseProxy`'s upstream request carries that context. The transport closes the
   upstream connection, so `responseWatcher.Read` returns an error. `copyResponse`
   returns, and the access-log middleware's deferred `Meta.Settle` runs `finishUsage`,
   then `OnUsage`, then `Limiter.Record(key, 101)`. Only after that is the `request`
   access line written (`internal/server/middleware.go:58-69`).
4. In parallel, the test's `waitLimited` makes a new connection and posts. The limiter
   runs `Admit` against whatever has been recorded **so far**.

Steps 1-3 and step 4 race. If step 3's `Record` lands first, the probe gets 429 and the
test passes. That is what Linux nearly always does. If step 4 wins, the probe is
admitted, the fake upstream holds it open (step 5), and:

5. `rlPost`'s `io.ReadAll` ends only when the probe's stream ends. The stream ends only
   when its upstream context ends, which happens only when the gateway's client leaves,
   and that client is the test itself, still reading. `done` closes only in cleanup,
   after the test returns. Deadlock, then the 10-minute alarm.

**Which link macOS misses:** none of the product links. The dump shows steps 1-3
completed for the disconnected stream. macOS does not miss the closed peer. What it
loses is the race between step 3 and step 4: on the 3-core arm64 runner, the
FIN → EOF → cancel → upstream close → `Settle` → `Record` chain sometimes finishes
after the client has dialled again and been admitted. On TCP: the client does a full
`close(2)`, not `shutdown(SHUT_WR)`, so there is no half-open state. A FIN reads as
EOF on both kernels, and the gateway's pending `backgroundRead` sees it on both. The
Linux/macOS half-close difference does not come into it here.

`waitLimited`'s own comment says the first probes "may still be admitted". That holds for
the tests where upstream answers and ends. It is false for this test's upstream, which
never ends a stream. That makes this a **test bug**, not a product bug.

## Reproduction on Linux (before the fix)

Linux reproduces it, so the macOS draft-PR bench was not needed (D4). Command, from
`scratchpad/repro/run.sh` (12-core box, `nproc`=12; hogs are `nproc` busy loops):

```
[GOMAXPROCS=N] systemd-run --user --scope -p MemoryMax=6G -- \
  go test -race -v -run '^TestRun_ClientDisconnectMidStreamRecordsSeenTokens$' \
  -count=500 -timeout 3m ./cmd/gateway
```

| Cond | GOMAXPROCS | Load | Iterations until the hang |
|---|---|---|---|
| a | 1 | none | 22 (21 passed) |
| b | 4 | none | 249 (248 passed) |
| c | 1 | 12 hogs | 36 (35 passed) |
| d | 4 | 12 hogs | 3 (2 passed) |

4 hangs in 310 iterations in total (≈1.3%). Each run stops at its first hang, so these
are first-hang positions, not measured rates. Every hang has the CI shape: one upstream
handler at `:339`, one `Proxy.ServeHTTP` in `responseWatcher.Read`, the test in
`waitLimited` → `rlPost` → `io.ReadAll`. The one live upstream handler is the probe's.
The subagent's note that `:339` showed the first stream still open was wrong, since
there is exactly one handler and the test is past the first stream.

## Reproduction on Linux (after the fix, AC4)

Same command and box, `-timeout 5m`, three rounds per condition, run in sequence:

| Cond | GOMAXPROCS | Load | Round 1 | Round 2 | Round 3 |
|---|---|---|---|---|---|
| a | 1 | none | 500/500 | 500/500 | 500/500 |
| b | 4 | none | 500/500 | 500/500 | 500/500 |
| c | 1 | 12 hogs | 500/500 | 500/500 | 500/500 |
| d | 4 | 12 hogs | 500/500 | 500/500 | 500/500 |

Before: 4 hangs in 310 iterations. After: 0 hangs, failures or watchdog panics in
6000. Every iteration asserts that the disconnected call's access line has
`client_disconnected: true`, so that held 6000 times. The whole `cmd/gateway` package
also passed with `-race -count=3` (35 s), so the watchdog did not trip on any test.

## Decisions

- **D1** (2026-11-23) Spec number 036 as the brief says; `specs/` ends at 034 on main,
  and 035 is not used here.
- **D2** (2026-11-23) Fix location: test code.
  Thesis: the product chain is correct and the dump proves it ran; the hang needs a
  probe the test made, on an upstream the test wrote to never end.
  Antithesis: the gateway could record seen tokens earlier (on every usage event
  instead of at Settle) so the window shrinks. But it still would not close (Record
  would still follow a read on another goroutine). It would also change when the
  limiter counts, a product behaviour, to suit a test.
  Synthesis: fix the test; keep `Settle` as the one place usage is recorded.
  Flip: a dump where the disconnected stream's own proxy goroutine is still in
  `responseWatcher.Read` would mean the gateway missed the close, and the fix would
  then belong in core.
- **D3** (2026-11-23) The latest main failure (run 45104601139, `internal/ui`
  `TestStream_ReplayOf20CallsResendsAtMostThePreviousCall`) is a different test in a
  different package and is left out of scope (spec Non-goals); noted for a later fix.
- **D4** (2026-11-23) No macOS draft-PR bench. The brief allowed one only if Linux could
  not reproduce, and Linux hung in all four conditions with the CI stack. Before and
  after proof is therefore on Linux under load; macOS proof is the PR's own CI run.
- **D5** (2026-11-23) Test shape after the fix: the fake upstream holds open only the
  first call. Any later call gets an immediate, finished response with no usage, so a
  probe can never block, and a probe that gets through cannot add tokens that would
  fake the 429. The test waits for the first call's `request` access line, written
  after `Settle` has run `Record`. It checks that line has `client_disconnected: true`,
  then makes one probe, which must be 429. The upstream must have seen exactly one
  call. This replaces the `waitLimited` poll for this test: the assertion gets
  stricter (one probe, no "eventually"), not looser. `waitLimited` stays for the other
  tests, whose upstream ends every call.
- **D6** (2026-11-23) Watchdog lives in `startGateway`, so every run test that starts a
  gateway gets it without opting in. It is per test (armed when the gateway starts,
  stopped by the test's last cleanup), so `-count=N` and slow runners cannot add up to
  a trip. It also covers a hang inside a later cleanup, such as waiting for run to
  return. On expiry it sets traceback to `all` and panics, which prints every
  goroutine's stack and fails the binary at once. `t.Fatal` cannot be called from a
  timer goroutine and could not unblock the stuck test goroutine anyway.
- **D7** (2026-11-23) The watchdog's self-test no longer uses a skipped test as its
  re-exec target. `TestMain` checks `GATEWAY_TEST_WATCHDOG_CHILD` first and runs
  `hangUnderWatchdog`, which arms a 200 ms watchdog and blocks (the stdlib `os/exec`
  test pattern). So no run reports a skipped test, and no check reads as skipped.
  `watchdog` now returns its timer. The stop test arms one for an hour in a subtest
  and asserts that `Stop()` is false afterwards. That proves the cleanup stopped it,
  with no 150 ms sleep. Probes: with the cleanup's `Stop` removed, the stop test fails.
  With a child that exits without arming, the firing test fails (no stacks, no frame).
- **D8** (2026-11-23) Fresh review of `main...HEAD` against the spec: no P1 or P2.
  It traced `accessLog`'s deferred func (`internal/server/middleware.go`): `Settle` runs
  `OnUsage`, which runs `Limiter.Record`, then the `request` line is written, all on one
  goroutine. So the single probe after the line is not racy. The P3s are settled here:
  - The limiter's wall-clock 60 s window can reset between `Record` and the probe if
    both straddle a minute boundary. That was already there before this change, and
    every `rl*` test shares it. It now fails fast instead of hanging. A clock injected
    into run's options is left for a later fix. It is out of scope here.
  - A test held on a debugger breakpoint for over 60 s is killed by the watchdog. No
    opt-out env var is added: an off switch for a guard is what AGENTS.md forbids
    reaching for, and it would be an untested path. Raise `defaultWatchdog` locally
    when debugging.
  - The watchdog times the whole test, not each gateway. That is accepted, because the
    slowest test takes a few seconds and the package runs in 35 s with `-count=3`.
  - The repro script was a scratch file. The command and the table above describe it
    in full: the four conditions were run in sequence, and hogs are `nproc` shell busy
    loops started before the condition and killed after it.
