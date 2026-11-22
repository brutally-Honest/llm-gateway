---
feature: 028-ci-flakes
kind: fix
status: done
version: 1
created: 2026-11-22
supersedes:
branch: fix/028-ci-flakes
---
# Two flaky tests

## Problem
Two tests fail now and then and pass when run again:
- `TestRun_ShutdownCutsInFlightCallAfterGrace` (`cmd/gateway/run_fast_shutdown_test.go`,
  spec 019 AC3). A streamed call that is still running when shutdown starts gets the
  grace period and is then cut. It must be stored as cut by the gateway
  (`gateway_error gateway_shutdown`), never as a client that left.
- `TestProxy_GoldenAnthropicStreamReplay` (`internal/protocols/anthropic/golden_test.go`,
  spec 001 AC24). It has failed with "reading the replayed stream: unexpected EOF"
  (015 research, 2026-11-10; 021 research, 2026-11-15; roadmap "Watch").

A test that fails for no reason in the code under test hides real failures and teaches
everyone to rerun.

## Goal
Both tests stop failing because their cause is fixed, in the product or in the test,
wherever the cause is.

## Non-goals
- No `t.Skip`, retries, longer sleeps or looser assertions.
- No change to what either test asserts.
- No change to CI or `make verify`.
- No new dependency.
- Other flaky tests (for example `TestHub_HookRefreshAndCommitInSameWindowSendEachChangeOnce`,
  seen in CI run 44863192822) are out of scope.

## Acceptance criteria
- **AC1** The root cause of each flake is named in `research.md`, with whether it lies in
  the test or the product, and a reproduction that fails before the fix.
- **AC2** `go test -race -count=100 -run '^TestRun_ShutdownCutsInFlightCallAfterGrace$' ./cmd/gateway`
  passes three times in a row after the fix.
- **AC3** `go test -race -count=200 -run '^TestProxy_GoldenAnthropicStreamReplay$' ./internal/protocols/anthropic`
  passes three times in a row after the fix.
- **AC4** A call cut at shutdown is still stored as `gateway_error gateway_shutdown`, never
  as `client_aborted`, whatever order the stream's goroutines observe the cut in.
- **AC5** The tests' assertions are unchanged or stricter; no timeout grows unless the
  timeout itself is the bug, and then `research.md` says why.
- **AC6** `make verify` passes three times in a row.

## Edge cases
- Shutdown with no call in flight, and a call that ends within the grace period: behaviour
  unchanged (spec 019 AC1, AC2).
- A client that really leaves while the gateway is not shutting down is still stored as
  `client_aborted`.
- A client that leaves during the grace period, before the cut: stays `client_aborted`.
- The golden stream is still forwarded byte for byte (spec 001 AC24); streams are not
  buffered.
