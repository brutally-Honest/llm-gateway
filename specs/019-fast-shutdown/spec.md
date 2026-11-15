---
feature: 019-fast-shutdown
kind: fix
status: draft
version: 1
created: 2026-11-15
supersedes:
branch: fix/019-fast-shutdown
---
# Fast shutdown

## Goal
`systemctl --user restart llm-gateway` takes about 90 s and ends in SIGKILL: on a stop
signal the gateway waits for connections that never end by themselves (proxy tunnels),
and for in-flight model calls, up to `shutdown_timeout` (10 minutes), longer than
systemd's 90 s stop timeout. The gateway must stop in a few seconds, keep what it
captured, and come back answering: the first sessions-list request after a restart also
failed while the gateway was still starting.

## Acceptance criteria
- **AC1** — On SIGTERM (or SIGINT) the gateway stops accepting at once on every listener:
  the API, the UI and the proxy listeners.
- **AC2** — Open proxy tunnels are cut at once and stored as closed early; the UI's live
  streams close at once. Neither waits for the grace period.
- **AC3** — In-flight model calls get a grace period, `shutdown_timeout`, now 10 s by
  default. A call still running when it ends is cut and stored as cut by the gateway
  (`gateway_error: gateway_shutdown`), not as a client that left; the gateway logs
  `shutdown timed out` and exits 1, as before (spec 001 AC16).
- **AC4** — Captures and the store flush cleanly: capture drains on a budget of its own
  after the API server has stopped, so a cut call is stored, and the store closes.
- **AC5** — After a start, the UI accepts once the sessions list's first page is built,
  so the first list request answers instead of timing out on a cold build; the API serves
  model calls from the start.
- **AC6** — Go tests (-race): a gateway with an open CONNECT tunnel and an open UI stream
  stops in under 2 s with the default grace period; an in-flight streamed model call is
  cut after the grace period and stored as cut by the gateway; the warm-up fills the list
  before the first request.
- **AC7** — On the real gateway, a restart with a Claude Code session and the UI open
  stops in under 3 s with no SIGKILL in the journal, and the session shows correctly
  afterwards.
- **AC8** — The config example, the compose file and the service units in
  `docs/clients/claude-code.md` match: 10 s default, room for the grace and the drain.

## Out of scope
- Zero-downtime restarts (socket activation, a second process taking over).
- The cold build of one large session's view (roadmap, 016 research).
- Changing what a client does with a cut call (Claude Code retries it).
