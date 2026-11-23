---
feature: 036-macos-stream-test-hang
kind: fix
created: 2026-11-23
branch: fix/036-macos-stream-test-hang
status: done
---

## Problem
On macOS CI, `go test` in `cmd/gateway` hangs until the 10-minute timeout in
`TestRun_ClientDisconnectMidStreamRecordsSeenTokens`, with a goroutine stuck in
`core.(*responseWatcher).Read`. Ubuntu passes. Seen on main in runs 45001477758
(b4b2d6c) and 45013800880 (5120e65), and on PR #65 run 45100294664. Main requires both
checks green, so this blocks every merge.

## Goal
- Find the root cause and fix it where it lives (product or test code).
- A future hang in `cmd/gateway`'s run tests fails within about a minute with every
  goroutine's stack, not after 10 minutes.

## Non-goals
- No skip, retry, longer sleep or looser assertion.
- Other flakes (e.g. `internal/ui` `TestStream_ReplayOf20CallsResendsAtMostThePreviousCall`
  on run 45104601139) are not fixed here.
- No change to how the gateway notices a client disconnect unless the evidence shows
  it is wrong.

## Acceptance criteria
- AC1: research.md holds the goroutine dump summary, the first hanging run, the
  expected unblock chain and which link fails.
- AC2: the test still proves E1: tokens seen before a client left mid-stream are
  counted, so the next call with that key is limited.
- AC3: the test cannot block forever on a call it makes after the disconnect.
- AC4: the reproduction that hung before passes three times after the fix, under load
  on Linux (and on the macOS bench if one was used); before and after rates recorded.
- AC5: a watchdog helper fails a `cmd/gateway` run test with all goroutine stacks
  after about 60 s; it costs nothing to a test that ends in time, and never fires on
  a normal run.
- AC6: `make verify` passes.

## Edge cases
- E1: the tokens land after the next call is admitted (the race behind this hang).
- E2: a regression where a disconnect records no tokens must fail fast with a clear
  message, not hang.
- E3: a test that legitimately runs long (`-count=N`, `-race` on a slow runner) must
  not trip the watchdog; the watchdog is per test, not per binary.
- E4: the watchdog must stop when the test ends, so it leaks no goroutine and never
  fires into a later test.
