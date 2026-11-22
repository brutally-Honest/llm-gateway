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
  Non-goals); it is a different test from the two the brief names. *(Superseded
  2026-11-22 by D11: the follow-up brief brought it into this PR.)*
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
  Also rejected (added after review): `http.NewResponseController(w).EnableFullDuplex()`,
  which stops the server from closing the body on first write. It removes the close
  itself, but it changes HTTP/1.1 connection handling for every request, which is a
  broader and less tested change. That would flip if an upstream really answered before
  reading the request body, since the watcher fix does not cover that case.
  Known limit: the fix relies on Go's server body returning `io.EOF` with its last bytes
  (`transfer.go` `body.readLocked`, Go 1.25.4). If a release returned `(n, nil)` and then
  EOF, the guard would not fire. `TestWatcher_RequestReadAfterEOF` models today's
  behaviour, and the golden replay under load would catch a regression.
- D5 (2026-11-22) — The shutdown test's failure (`upstream_aborted`, the stream dead
  before shutdown, a fast exit 0) is the mechanism D4 fixes. In that test the fake
  upstream answers, and the transport's extra body read then drops the upstream
  connection. So no second product change is made. Instead of A's retry, the main
  session re-runs A's loaded reproduction three times on 4164f43 as the before/after
  proof, and there is no separate shutdown commit unless that proof fails.
- D6 (2026-11-22) — This fix was built the way the human's brief for 028 sets out (a main
  session plus subagents, with spec.md and research.md only). It has no plan.md or
  tasks.md, and it did not go through the agent-loop kit that AGENTS.md names, which the
  brief calls retired. spec.md keeps `status: draft` because nobody has approved it;
  approval is the human's to give. The review raised this as P4.
- D7 (2026-11-22) — Review P3s and P4s were handled in the main session's own files
  (research.md), apart from the missing negative test, which went to a haiku subagent.
- D8 (2026-11-22) — Production impact: the bug was latent in product code, and only
  HTTP/1.1 upstreams could trigger it: the test servers, or a plain `http://` upstream
  in config, which `internal/config/upstream.go:61` allows only for a loopback host.
  Real Anthropic traffic was not hit, because `newTransport` (`internal/core/proxy.go:288`)
  sets `ForceAttemptHTTP2` and the default `base_url` is `https://api.anthropic.com`, so
  TLS ALPN negotiates HTTP/2, and the HTTP/2 transport makes no extra read after EOF.
  With `HTTPS_PROXY` set (`environmentProxy`, `proxy.go:271`), an `https` target goes
  through a CONNECT tunnel. The stdlib (`transport.go:1828` `case cm.targetScheme == "https"`,
  then `addTLS`) runs TLS with the same ALPN inside the tunnel, so it is still HTTP/2.
  The remaining exception would be a TLS upstream that offers only HTTP/1.1 in ALPN.
- D9 (2026-11-22) — The follow-up brief asks to "replace" D2 and the "indicative only"
  line. AGENTS.md says research entries are never deleted, so each is kept, marked
  superseded, with a dated line under it giving the new finding.
- D10 (2026-11-22) — Commits d338afd and 07189f9 were reworded from `docs(docs)` to
  `docs(spec)` (now 5ee327a and f6c71ce), at the human's request, to match earlier spec
  commits such as d4841d6. `spec` is not in the PLAN §11 scope list; the human's choice
  and the repo's history take precedence here. The test commit was rewritten too
  (a2fcb98 → cd6f50f), and its note was added after the rewrite. 4164f43 keeps its hash
  and note (checked by hash and by the note's sha256).
- D11 (2026-11-22, supersedes D2) — The `internal/ui` hub flake is fixed in this PR
  (3871f65), and the cause is in the test.
  - **The race:** the test's own `hub.refresh` could read a stale build and arm a
    catch-up. The test waited only for `catching` to be empty, but `catchUp` deletes
    `catching[who]` before it takes `sendMu`. The catch-up could therefore read the
    store after `e.save("d")` and send `exchange d` ahead of the commit's `entries d`.
  - **Why it is the test:** that order is within the hub's contract (009 AC54: a refresh
    sends every exchange that differs from what was last sent; 026 D11 accepts a refresh
    between save and publish). The hub does not promise the order the test assumed.
  - **Fix:** `Hub.catchRuns` counts running catch-up goroutines. The test helpers
    `quietNow`/`quiet` wait for `catching` to be empty and `catchRuns` to be 0. The flaky
    test calls `e.quiet()` after its refresh, and its assertions are unchanged.
  - **Rejected:** product ordering changes that hold a refresh back until publish. They
    would change the live UI's behaviour to suit a test, against AC54.
  - **Trade-off:** `catchRuns` is product state read only by tests.
  - Side note: 026 D11's phrase "the commit then sends nothing new" is slightly off,
    because `writeLive` always sends the committed exchange's `entries`. 026 is history
    and is not edited.
  There was no P1 or P2, so no second opus review was run.

## Verification (on 4164f43 + d338afd, 2026-11-22)
- AC2, unloaded: `go test -race -count=100 -run '^TestRun_ShutdownCutsInFlightCallAfterGrace$' ./cmd/gateway`
  ran three times: ok 47.6 s, ok 47.6 s, ok 47.8 s.
- AC6: `make verify` ran three times in a row: green 105 s, green 77 s, green 76 s.

## Review (fresh opus subagent, `git diff main...HEAD`, 2026-11-22)
No P1 or P2. The reviewer checked the cause against Go 1.25.4 (`transfer.go` writeBody's
extra read and body.readLocked's EOF with the last bytes; `server.go` chunkWriter closing
the body; `transport.go` writeLoop closing the connection). It found that a real client
disconnect (`io.ErrUnexpectedEOF`) is still kept, that no new race was introduced, and
that both flaky tests are unchanged.
- P3 `internal/core/watcher_test.go`: no test for a closed-body read before EOF, so
  dropping the `eof` guard would pass. → `TestWatcher_RequestReadAfterCloseBeforeEOF`
  added (haiku subagent), with a mutation check.
- P3 research D4: the full-duplex alternative was not recorded. → Added to D4.
- P4 research root cause: "whenever upstream answers early" was too broad. → Narrowed to
  HTTP/1.1 upstreams with a fixed-length body (dated correction line).
- P4 `meta.go`: the fix relies on the stdlib returning EOF with the last bytes. → Recorded
  as a known limit in D4; code unchanged.
- P4 spec `status: draft` with no plan/tasks. → D6.
- The reviewer had marked AC6 "not verified"; the three green `make verify` runs are now
  recorded above.

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
  connection, cutting a stream that was still flowing. A real client can hit this only
  when the upstream leg is HTTP/1.1 with a fixed-length request body. Over HTTP/2 (TLS
  providers such as api.anthropic.com negotiate it) the transport does not read again
  after EOF (`h2_bundle.go`), and a chunked request body has no extra read.
  (Correction 2026-11-22, after review: an earlier line here said "whenever upstream
  answers early", which was too broad.)
- **Fix (4164f43):** after a Read has returned `io.EOF`, a Read that returns no bytes and
  `http.ErrBodyReadAfterClose` returns `io.EOF` and is not kept as an error. Every other
  error is passed on and kept as before. Nothing is buffered or read ahead. The
  regression test `TestWatcher_RequestReadAfterEOF` (`internal/core/watcher_test.go`)
  fails on the old code with `Read after EOF = 0, http: invalid Read on closed Body; want 0, io.EOF`.
- After the fix: the brief's command ran three times and passed each time (1.4 s each).
  `-count=100 -cpu=1,2,4 -run 'TestCapture_ForwardingUnchanged|^TestProxy_GoldenAnthropicStreamReplay$'`
  ran three times and passed each time (77–79 s each). The one-core `GOGC=1` setup ran
  twice for 270 s each and showed no failure before its timeout. Before the fix, the
  same setup failed within 1–2 minutes. *(Superseded 2026-11-22 by the completed run
  below: those two runs were killed at their timeout, so they count as indicative only.)*
- Completed proof (2026-11-22, HEAD f6c71ce, subagent): built with
  `go test -race -c -o $S/anth.test ./internal/protocols/anthropic`, then run from
  `internal/protocols/anthropic` with 4 × `taskset -c 0 sh -c 'while :; do :; done'` hogs:
  `GOGC=1 taskset -c 0 $S/anth.test -test.timeout 0 -test.v -test.cpu=2 -test.count=1600 -test.run '^TestCapture_ForwardingUnchanged$'`.
  It completed with no `timeout` wrapper: **1600 golden-replay runs, 0 failures**, exit 0,
  final line `PASS`, in 649 s (10 m 49 s). At the before-fix rate of about 1 in 300–600,
  roughly 3–5 failures would have been expected.

### TestHub_HookRefreshAndCommitInSameWindowSendEachChangeOnce — first attempt (subagent, 2026-11-22)
CI: run 44863192822, job 136720956516, macos-latest (3 cores), `hub_hook_test.go:222: extra event: {event:exchange … "d:0" … "d:1" …}`.
Every local run below used a `-race` build of `./internal/ui` under `taskset -c 1-11`
(the last one under `-c 1-2`). All of them completed, and **0 failed out of 3,400**:

| Setup | Runs | Failures |
|---|---|---|
| no load, `-test.count=500 -test.timeout 0` | 500 | 0 |
| 16 hogs, `GOGC=1`, `-cpu=1,2,4`, count 300 | 900 | 0 |
| 12 hogs, `-cpu=1,4`, count 300, instrumented | 600 | 0 |
| 20 hogs, `GOGC=1`, `-cpu=1,2`, count 400, instrumented | 800 | 0 |
| 6 hogs on 2 cores, `GOGC=1`, `-cpu=1,2`, count 300, instrumented | 600 | 0 |

- What the instrumentation showed: in every run, the read of commit `c` comes from a
  stale cached build (`hub.go:~511`) and arms a catch-up (`hub.go:~556`).
- Hypothesis, not proven: a second asynchronous read (a late catch-up, or a stale
  refresh) lands between `e.save("d")` and `e.publish("d")`, and sends `exchange d`
  before the `entries:d` batch.
- No change was made. The retry forces that interleaving deterministically through the
  hub's `read`/`fresh` hooks, to decide whether the cause is in the product (the same
  change sent twice) or in the test (an ordering the hub doesn't promise).

### TestHub_HookRefreshAndCommitInSameWindowSendEachChangeOnce — retry and fix (subagent, 2026-11-22)
- **Deterministic before:** the subagent wrote `TestHub_ForcedLateCatchUpSendsRefreshBeforeCommit`
  (`internal/ui/hub_hook_test.go`). It wraps the hub's `read`/`fresh` hooks to make the
  test's own refresh stale and hold its catch-up on a gate, then saves `d`, releases the
  gate, waits until the catch-up has read, and only then publishes `d`. Asserting the
  flaky test's "next event is `entries:d`", it failed **20/20** on the old code with the
  CI symptom:
  `hub_hook_test.go:674: extra event: {event:exchange … "d:0" … "d:1" … "text":"last" …}`.
  As committed, the test asserts what the contract allows instead (`exchange d`, then
  `entries d`).
- **Statistical:** `GOGC=1 taskset -c 1 $S/ui2.test -test.timeout 0 -test.cpu=1 -test.count=1000 -test.run '^TestHub_HookRefreshAndCommitInSameWindowSendEachChangeOnce$'`
  with 3 hogs on core 1: before 1000/1000 pass, after 1000/1000 pass. With the first
  attempt's 3,400 runs, about 4,400 loaded runs on 12-core Linux never hit the window.
  The deterministic test is the evidence.
- **The fix's own test:** `TestHub_QuietCoversTheGapBeforeACatchUpSends` holds `sendMu` and
  gates `fresh`, so it is in the gap. It fails ("quiet in the gap between leaving catching
  and sending") when `&& catchRuns == 0` is removed from `quietNow`.
- **After:** `go test -race -count=200 -run TestHub_ ./internal/ui` passed 3/3. That run
  takes about 16 minutes, so the subagent used a prebuilt binary with `-test.timeout 0`.
  The main session ran `-count=20` of the three hub tests (ok) and `go test -race ./internal/ui` (ok, 30.7 s).

### Review of 3871f65 (fresh opus subagent, 2026-11-22)
No P1 or P2. Verdict: the commit fixes the cause. The reviewer checked every
asynchronous sender (re-armed catch-ups, the dispatcher's catch-up, the hook and effects
timers, heartbeats); all are covered by `quiet()` or never fire in this test. Every
`catchRuns++` has one deferred decrement, which runs after `sendMu` is released, on
every path including a panic. There is no deadlock and no new race. A mutation probe
(`go test -overlay`, without `&& catchRuns == 0`) fails the gap test.
- P3 `internal/ui/hub_hook_test.go:736-745`: the first deadline loop in
  `TestHub_QuietCoversTheGapBeforeACatchUpSends` fell through silently on timeout, so the
  gap assertion could pass vacuously. → It now fails with "the catch-up never left
  catching" (haiku subagent, separate commit).
- P4 D11 and the 3871f65 note: "the test waited only for `catching` to be empty" is wrong.
  → Correction (2026-11-22): before 3871f65 the test did not wait for the hub at all
  after its refresh. The race window was the catch-up's whole life, its `fresh` wait
  included, not only the gap after the `catching` delete. `quiet()` covers both. A
  correction was appended to the note (`git notes append`).
- P4 the test name and the "Nothing else follows" comment: under the allowed
  interleaving, `d` is sent as `exchange` and then as `entries` (each event type once, per
  AC54). → Recorded only; the test's own order is pinned by `quiet()`.
