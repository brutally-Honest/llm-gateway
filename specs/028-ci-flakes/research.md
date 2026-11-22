---
feature: 028-ci-flakes
---
# Research & decisions — 028-ci-flakes

## Open questions
<!-- Answer by changing "(open)" to "(answered)" and writing the answer underneath. -->

## Evidence: CI (2026-11-22, main at 5ebd7ce)

`gh run list --workflow verify --status failure --limit 20` lists two failed runs, both on
`ci/026-github-actions`, both failing only on `verify (macos-latest)`:

| Run | Job | OS | What failed |
|---|---|---|---|
| 44863192822 | 136720956516 | macos-latest | `TestHub_HookRefreshAndCommitInSameWindowSendEachChangeOnce` (`internal/ui`): `hub_hook_test.go:222: extra event: {event:exchange id: data:[…"id":"d:0"…]}` |
| 44862083462 | 136717483778 | macos-latest | `make kit-test`: `test_answer_colon.sh`, `test_commit_attribution.sh`, `test_harness.sh` (`/var/folders` vs `/private/var/folders`); fixed later in 026 |

`gh run view <id> --log-failed` printed nothing for either run; the logs were read with
`gh api repos/{owner}/{repo}/actions/jobs/<job>/logs`. All seven `verify` runs so far
(five green) were searched: **neither of the two target tests has failed in CI.**

Where they have failed:
- `TestProxy_GoldenAnthropicStreamReplay`: locally, "reading the replayed stream: unexpected
  EOF" (015 research, 2026-11-10, verify of b71e18b in a worktree), and once more in a full
  `make verify` (021 research, 2026-11-15). Roadmap "Later" says to watch it.
- `TestRun_ShutdownCutsInFlightCallAfterGrace`: no failure written down in the repo; the
  report comes from the human's brief. The local reproduction below is the evidence.

## Decisions
- D1 (2026-11-22) — CI holds no failure of either target test, so the CI run table above
  records what CI does show, and the local `-race -count=N` reproductions (below) are the
  evidence of the flakes. Rejected: stopping to ask, since the brief says build without
  stopping and both tests can be reproduced locally.
- D2 (2026-11-22) — The `internal/ui` hub flake that CI did catch is left out of scope (spec
  Non-goals); it is a different test from the two the brief names.
- D3 (2026-11-22) — The brief's direction for the shutdown test ("record the shutdown
  cause before closing anything") does not match the failure: the ordering is already
  right, and the stream dies `upstream_aborted` before shutdown starts. No change is made
  to `run.go` for it: a reorder there would fix nothing, so making one would treat the
  symptom. The failure looks like the golden test's "unexpected EOF" (an upstream stream
  ending early). The shutdown fix waits for subagent B's root cause in `internal/core`,
  and A's single retry is aimed at that shared cause.
- D4 (2026-11-22) — The golden flake is fixed in the product, at the request watcher, by
  answering net/http's closed-body read after EOF with EOF. Rejected: buffering or
  reading the request body ahead (breaks passthrough), rewriting Content-Length or
  `Body` (reshapes the request), and a longer test timeout (not the cause).
- D5 (2026-11-22) — The shutdown test's failure (`upstream_aborted`, the stream dead
  before shutdown, a fast exit 0) is the mechanism D4 fixes. In that test the fake
  upstream answers, and the transport's extra body read then drops the upstream
  connection. So no second product change is made. Instead of A's retry, the main
  session re-runs A's loaded reproduction three times on 4164f43 as the before/after
  proof, and there is no separate shutdown commit unless that proof fails.

## Reproductions and fixes

### TestRun_ShutdownCutsInFlightCallAfterGrace — first pass (subagent A, 2026-11-22)
- `go test -race -count=100 -run '^TestRun_ShutdownCutsInFlightCallAfterGrace$' ./cmd/gateway`:
  100/100 passed, 49 s.
- With 12 busy-loop CPU hogs, `go test -race -count=300 -cpu=1,2,4 -run '^TestRun_ShutdownCutsInFlightCallAfterGrace$' ./cmd/gateway`:
  **5 failures in 900 (0.55 %)**, 486 s, every one the same:
  ```
  run_fast_shutdown_test.go:179: shutdown took 10.48402ms, want at least the 400ms grace period
  run_fast_shutdown_test.go:185: exit code = 0, want 1 (a call was cut)
  run_fast_shutdown_test.go:188: shutdown timed out lines = [], want one with in_flight 1
  run_fast_shutdown_test.go:203: stored call: gateway_error , client_disconnected false, upstream_aborted true, stream true; want a stream cut by gateway_shutdown
  ```
- Four parallel copies (`-count=200 -cpu=1,2`): 17 failures, 15 of them "address in use"
  from `freeAddr` port contention between processes (noise), 2 with the signature above.
- Two later loaded runs (1200 and about 1800 iterations) passed: the rate is low and
  depends on load.
- Reading: the call is stored `upstream_aborted`, not `client_aborted`. Shutdown found
  nothing in flight, so the upstream stream ended **before** shutdown began.
  `run.go:446` already calls `srv.Cancel(core.ErrShutdown)` before `srv.Close()`.

### TestRun_ShutdownCutsInFlightCallAfterGrace — after 4164f43 (main session, 2026-11-22)
- Why the same fix covers it: the test's fake upstream writes `message_start` without
  reading the request body (which has a fixed length). The gateway's flush closes the
  client body, and the transport's extra read past the length then dropped the upstream
  connection. The call ended `upstream_aborted` before `g.stop()`, so shutdown had
  nothing in flight: about 10 ms, exit 0, no "shutdown timed out".
- Loaded reproduction (12 `yes` hogs, sequential,
  `scratchpad/shutdown-load.sh`): `go test -race -count=300 -cpu=1,2,4 -run '^TestRun_ShutdownCutsInFlightCallAfterGrace$' ./cmd/gateway`
  run 1 ok 0/900 (546 s), run 2 ok 0/900 (542 s), run 3 ok 0/900 (542 s).
  **0 of 2700 after, against 5 of 900 before**; about 15 failures would be expected if
  the fix did not cover it.

### TestProxy_GoldenAnthropicStreamReplay (subagent B, 2026-11-22)
- `go test -race -count=200 -run '^TestProxy_GoldenAnthropicStreamReplay$' ./internal/protocols/anthropic`:
  200/200 passed. It also passed four copies at `-count=300 -cpu=1,2,4` with 12 hogs, and
  1500-run loops at cpu 1, 2 and 4 under 24 hogs.
- It reproduced only in the capture-on variant
  `TestCapture_ForwardingUnchanged/TestProxy_GoldenAnthropicStreamReplay` (the one 021
  saw). It needed a prebuilt `-race` binary pinned to one core, `GOGC=1`, 4 hogs on that
  core, and `-test.cpu=2 -test.count=2500 -test.run TestCapture_ForwardingUnchanged`.
  That failed **about once per 300–600 iterations** (at least 9 in the first 5 minutes):
  ```
  golden_test.go:108: reading the replayed stream: unexpected EOF
  golden_test.go:83: upstream: writing event: write tcp …: broken pipe   (or connection reset by peer)
  gateway: httputil: ReverseProxy read error during body copy: read tcp …: use of closed network connection
  access line: upstream_aborted:true
  ```
- **Root cause (product):** `requestWatcher.Read` (`internal/core/meta.go`) passed every
  Read through, including the transport's extra read past the declared Content-Length.
  When upstream had already answered, the gateway's first response write made net/http
  close the fully read client body. That read then failed with
  `http: invalid Read on closed Body`, and `persistConn.writeLoop` closed the upstream
  connection, cutting a stream that was still flowing. A real client can hit this
  whenever upstream answers early.
- **Fix (4164f43):** after a Read has returned `io.EOF`, a Read that returns no bytes and
  `http.ErrBodyReadAfterClose` returns `io.EOF` and is not kept as an error. Every other
  error is passed on and kept as before. Nothing is buffered or read ahead. The
  regression test `TestWatcher_RequestReadAfterEOF` (`internal/core/watcher_test.go`)
  fails on the old code with `Read after EOF = 0, http: invalid Read on closed Body; want 0, io.EOF`.
- After the fix: the brief's command ran three times and passed each time (1.4 s each).
  `-count=100 -cpu=1,2,4 -run 'TestCapture_ForwardingUnchanged|^TestProxy_GoldenAnthropicStreamReplay$'`
  ran three times and passed each time (77–79 s each). The one-core `GOGC=1` setup ran
  twice for 270 s each and showed no failure before its timeout. Before the fix, the
  same setup failed within 1–2 minutes.
