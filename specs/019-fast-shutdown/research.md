---
feature: 019-fast-shutdown
---
# Research & decisions — 019-fast-shutdown

## Open questions
<!-- Answer by changing "(open)" to "(answered)" and writing the answer underneath. -->

## Decisions
- D1 (2026-11-15) — Karthik: a fix branch built by hand, no agent-loop kit (no /al-*
  commands), so spec.md stays a draft with no approval stamps and there is no tasks.md.
- D2 (2026-11-15) — What holds shutdown open (Notes, timed stops): proxy tunnels and
  in-flight model calls, each until `shutdown_timeout` runs out; the UI's live streams do
  not (the hub closes before the UI server stops). On the real gateway that timeout was
  10 minutes, so systemd's 90 s stop timeout always won: the journal shows nothing between
  "Stopping" and the SIGKILL. A second finding: a model call still running when the
  timeout ran out was lost, its capture failing on the expired context ("store …: begin:
  context canceled").
- D3 (2026-11-15) — Grace period: 10 s, as the default of the existing
  `shutdown_timeout` (was 10m), not a new key: one knob still means "how long in-flight
  calls get". 10 s lets a stream that is about to end finish, and is short enough that a
  stop with a call in flight still ends well inside systemd's default. A long Claude Code
  turn is now cut at a stop (Claude Code retries it); this replaces the old choice of 10
  minutes so a turn is never cut (docs/clients/claude-code.md §4), on Karthik's request.
- D4 (2026-11-15) — A cut call is told apart by a cancel cause, not a guess: the API
  server's requests derive from a base context the gateway cancels with
  `core.ErrShutdown` when the grace ends; `Meta.Settle` and `classify` read the cause and
  record `gateway_error: gateway_shutdown` (503 when no header was sent yet). Rejected:
  letting `srv.Close` cut the connections, which records the call as client_disconnected,
  untrue. The `shutdown timed out` line and exit 1 of spec 001 AC16 stay as they are; the
  cut calls' handlers get up to 2 s to hand their capture over before the connections
  close.
- D5 (2026-11-15) — Capture drains on a fresh budget as long as `shutdown_timeout`, after
  the API server is done, never on what the grace left (D2's loss). Not a separate fixed
  value: a stalled store must still give up on the same knob, as the 002 drain tests
  expect (a fixed 10 s drain made three of them outlast the test harness).
- D6 (2026-11-15) — Order: the API server's shutdown starts first (its listener closes at
  once), in parallel the proxy listeners close and every tunnel is cut (stored closed
  early), then the hub closes the UI streams and the UI server gets 2 s; a UI read still
  running then is cut with a warning, not counted a failed stop (the UI only reads).
- D7 (2026-11-15) — Startup gap (Notes): on the real store a cold sessions list needs
  about 17 s of work, and each UI request gives the store 5 s, so the first requests after
  a restart got 504 "the store did not answer in time" until the digest cache was warm.
  Fix: the gateway builds the list's first page (`ui.Warm`) before the UI server accepts;
  a page that connects meanwhile waits in the listen queue and then gets its answer. The
  API listener serves model calls from the start. Rejected: answering 503 "starting"
  while warm (still fails the first request, and needs UI work); raising the 5 s timeout
  (every cold request would hold a connection that long).
- D8 (2026-11-15) — The service units in docs/clients/claude-code.md get a stop timeout
  of 30 s (`TimeoutStopSec`, launchd `ExitTimeOut`) and the compose file 30 s instead of
  10m: room for the grace, the cut and the drain. Karthik's own config file sets
  `shutdown_timeout: 10m` (copied from the old example); it is changed to 10s for the
  real-gateway check, with a backup beside it.

## Notes
### Evidence: timed stops (2026-11-15)
Throwaway gateway (main, 278d16a) on 127.0.0.1:17295–17299, a temp store, a fake slow
upstream and a TCP target that holds connections; `shutdown_timeout` 30s; SIGTERM 1 s
after the connection is open:

| Held open | Stop | Notes |
|---|---|---|
| nothing | 0.00 s | |
| an idle keep-alive UI connection | 0.00 s | |
| a UI live stream (`/api/stream`) | 0.00 s | the hub closes first, the stream ends |
| a CONNECT tunnel | 30.02 s | `netproxy.Proxy.Shutdown` waits for tunnels until the deadline |
| a streamed model call | 30.02 s, exit 1 | `shutdown timed out` in_flight 1; capture lost: `begin: context canceled` |

Same harness, this branch, `shutdown_timeout` 10s: nothing, idle, UI stream and tunnel
0.00–0.01 s, the tunnel stored `closed early`; the model call 10.03 s, exit 1, stored
with `gateway_error gateway_shutdown`, parse partial, nothing undrained.

### Evidence: the startup gap
Main, on a copy of the real store (`.backup`, blobs hard-linked), effects off:
`/api/sessions` at 5.01 s, 10.01 s and 15.01 s → 504 `the store did not answer in time`,
then 200 at 17.09 s (2.08 s), then 0.03 s. This branch: the first `/api/sessions` waits in
the listen queue and answers 200 at 16.35 s.
