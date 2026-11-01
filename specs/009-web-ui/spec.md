---
feature: 009-web-ui
kind: feat
status: done
version: 4
created: 2026-10-31
supersedes:
branch: feat/009-web-ui
approved: 2026-10-31T16:47:35Z
approved-by: human
sha256: e1dc7bd447407ec526c591e75be6d21f2c9766aa3cd3e0a9d02daea71f539f55
fingerprint: 665ac9bd04ff253338b0fef5b401ac657fe99866a32f1cbae2509bb8e9b03759
previous: 1587b97
---
# Web UI

## Problem
Everything Claude Code does through the gateway is captured, but the only way to see it is
the CLI viewer (`gateway sessions`, `gateway timeline`, `gateway dump`). It is greppable but
slow to read: long timelines scroll past, tool calls and their results are far apart, raw
bodies need a second command, and nothing updates while a session runs. I want instant visual
feedback on what Claude Code did: browse every session ever captured, and watch an active
session update live. Read-only.

This is roadmap item A of the new v1 order: A web UI (this feature) → B Phase 6 harness hooks
(asked / did / reported) → C file-system and network visibility → Phase 9 cost. Routing (old
Phase 7) and real auth are removed from v1. This feature updates PLAN.md and the ADRs to match.

## Goal
- A local web page, served by the gateway on its own loopback listener, lists every captured
  session and ungrouped exchange, shows each session as a timeline built from canonical events
  (the same timeline the CLI viewer prints), and shows each exchange's raw detail.
- An active session updates live in the page within moments of each exchange being stored,
  and a reload or a dropped connection resumes without losing history.
- Nothing in the UI can block, slow down or fail proxying or capture.

## Non-goals
- Auth of any kind; access from anything but loopback.
- Writing, deleting or editing captured data.
- Full-text search across sessions (the text filter works only over entries already loaded).
- Cost or pricing (Phase 9); harness hooks (B); file-system or network events (C).
- Charts beyond the per-model-call token chip.
- Mobile layout (usable from 1024 px wide upward only).
- Exporting data.
- A JS test framework, npm, node_modules, a bundler, a build step, or any JS dependency
  (vendored Preact+htm is the recorded fallback, not used now).

## Acceptance criteria
Terms used below. A **session** is what the CLI viewer calls a session: the stored exchanges
that share a principal, client and session key (`x-claude-code-session-id`). An **ungrouped
exchange** has no session key. **Usage totals** are the four counters (input, output,
cache write, cache read) summed as the CLI viewer sums them, with unknown, partial and errored
calls flagged, never shown as 0. A session is **active** when its last stored exchange started
less than 5 minutes ago (a named constant, not config). An exchange is **committed** when the
capture pipeline has finished storing it and its parse. The **UI origins** are
`http://127.0.0.1:<port>`, `http://localhost:<port>` and `http://[::1]:<port>`, where `<port>`
is the UI listener's port, plus `http://<listen host>:<port>` when `ui.listen_addr` names another
loopback address (research D8).

### Process and config
- **AC1** — When the gateway starts with `ui.enabled` true (the default) and capture enabled,
  the system shall serve the UI on its own HTTP server and listener at `ui.listen_addr`
  (default `127.0.0.1:7199`), separate from the proxy listener.
- **AC2** — When the gateway shuts down, the system shall shut the UI server down in the same
  graceful shutdown as the proxy, within `shutdown_timeout`, closing open SSE streams.
- **AC3** — When `ui.enabled` or `ui.listen_addr` is set in the YAML file or in
  `GATEWAY_UI_ENABLED` / `GATEWAY_UI_LISTEN_ADDR`, the system shall apply it with the existing
  precedence (defaults < file < env), and an unknown key under `ui` shall be a config error.
- **AC4** — When `ui.listen_addr` is not a loopback address (a host in 127.0.0.0/8, `::1`, or
  `localhost`), or equals the proxy `listen_addr`, the system shall refuse to start with a
  config error naming `ui.listen_addr`.
- **AC5** — When `ui.enabled` is false, the system shall open no UI listener and log nothing
  about the UI.
- **AC6** — When `ui.enabled` is true and `capture.enabled` is false, the system shall not start
  the UI and shall log exactly one warn line saying the UI is off because capture is disabled.
- **AC7** — When the UI listener cannot be bound at startup, the system shall exit with a
  startup error, as it does for the proxy listener.
- **AC8** — When a UI handler panics, the system shall recover it, log it with the request
  path, answer 500 with the JSON error envelope if nothing was written yet, and keep serving
  both the UI and the proxy.

### Data access
- **AC9** — When the UI reads captured data, the system shall do so through its own read-only
  connection to the store (SQLite `mode=ro`), so that any attempt to write through it fails.
- **AC10** — When a UI store query runs longer than 5 s, or the HTTP request is cancelled, the
  system shall abandon the query and answer the request with a JSON error (`504` with code
  `timeout` when it timed out).
- **AC11** — When the UI builds a session timeline, sessions list, ungrouped list or usage
  totals, it shall produce the same sessions, entries and usage totals as the CLI viewer
  computes for the same store: the same entry order (exchange start time, then exchange id,
  then the entry's index within its exchange), kinds, text and tool pairing, apart from the cut
  length (16 KiB, AC18).
- **AC12** — When the purity test runs, it shall fail if the UI package (Go and embedded web
  files) names any provider or client identifier, as it does for the timeline package.

### JSON API
- **AC13** — When any `/api/...` route receives a method other than GET (HEAD included), the
  system shall answer 405 with an `Allow: GET` header and the JSON error envelope; every API
  error shall be `{"error":{"code":"...","message":"..."}}` with `content-type: application/json`.
- **AC14** — When `GET /api/health` is called, the system shall answer 200 `{"ok":true}`.
- **AC15** — When `GET /api/sessions?limit=&after=` is called, the system shall return sessions
  newest last activity first, each with: id, session key, client, models seen, first_at,
  last_at, exchange count, model-call count, tool-call count, error count, usage totals with
  their flags, and active; plus `next_after` (null on the last page). `limit` defaults to 50
  and is capped at 200; following `next_after` lists every session exactly once.
- **AC16** — When `GET /api/ungrouped?limit=&after=` is called, the system shall return the
  ungrouped exchanges, newest first, in the same summary shape per exchange (counts of 0 or 1
  where a count applies) with the same limit, cap and `after` rules as AC15.
- **AC17** — When `GET /api/sessions/{id}` is called, the system shall return that session's
  summary as in AC15.
- **AC18** — When `GET /api/sessions/{id}/timeline?after=&limit=` is called, the system shall
  return the session's entries ordered by (exchange start time, exchange id, index within the
  exchange), at most `limit` (default 500, capped at 2000), starting after the opaque `after`
  value (none = from the start), plus `next_after` (null when no more) and `stream_since`, the
  highest store row id among the session's exchanges included in this answer (research D12); the
  page may pass it as `?since`, but no result depends on it (AC24, research D16).
  Each entry has the id `<exchange_id>:<index>`, where index is its position within its own
  exchange's entries; for the same stored exchanges every call returns the same ids. Entry
  kinds are: user prompt, assistant text, thinking (when the stored message content holds a
  thinking block), tool call (name, tool_call_id, input JSON), tool result (tool_call_id,
  is_error, content text), model call (exchange id, model, status, duration, usage), error,
  and the CLI viewer's other kinds (research D2). Each entry carries its id, exchange id, index
  and exchange start time. A text field longer than 16 KiB is cut at 16 KiB with `truncated: true`.
- **AC19** — When `GET /api/exchanges/{id}` is called, the system shall return the exchange's
  method, path, status, timings (start, time to first byte, end, duration), model, usage,
  session id (if any), capture flags (request/response truncated, request incomplete, client
  disconnected, upstream aborted, gateway error) and its request and response headers as
  stored (already redacted).
- **AC20** — When `GET /api/exchanges/{id}/body?part=request|response` is called, the system
  shall return that body decompressed, with its stored `content-type`; if it is larger than
  5 MiB it shall return the first 5 MiB with `X-Body-Truncated: true`. A body not stored
  answers 404 with code `body_not_stored`; a missing or other `part` answers 400.
- **AC21** — When an `{id}` names no session or exchange, the system shall answer 404 with code
  `not_found`; when `limit`, `after` or `since` is malformed, it shall answer 400 with code
  `bad_request`.
- **AC22** — When `GET /api/stream` is opened or reopened, the system shall first send the first
  page of the sessions list (AC15 order and default limit) as one `session` event per session
  (AC15 shape), then, while it is open, a `session` event carrying the updated summary each time
  a session gains a committed exchange. The page applies a `session` event by replacing that
  session's row by id.
- **AC23** — When `GET /api/sessions/{id}/stream` is open and the session gains a committed
  exchange, the system shall send one SSE event `entries` carrying that one exchange's full
  timeline entry list (AC18 shape and ids), with the event id set to the exchange's store row id.
  The page applies it like an `exchange` event: it replaces every entry it holds for that
  exchange id.
- **AC24** — When a session stream is opened or reopened, with or without `Last-Event-ID` or
  `?since=<row id>`, the system shall first send a snapshot of the session: one `exchange` event
  per stored exchange of the session, in timeline order, each carrying that exchange's full
  current entry list (AC18 shape and ids); then continue live. The snapshot is taken after the
  stream is subscribed, so no exchange committed around the open is missed; one sent both in the
  snapshot and live is replaced, never duplicated. `Last-Event-ID` and `?since` are accepted
  (malformed values are 400, AC21) but do not narrow the snapshot: correctness never depends on
  row-id order, which can differ from commit order when capture runs several workers
  (research D16).
- **AC25** — When an exchange that started before exchanges already shown is committed after
  them (for example a concurrent sub-agent call that finished later), the page shall insert its
  entries at their sorted position by (exchange start time, exchange id, index) without
  reloading, replacing any entries it already holds for that exchange id (never keeping an
  entry just because its id is known); and a reload shall show the same entries in the same
  positions.
- **AC26** — When an SSE stream is open, the system shall send a heartbeat comment at least
  every 15 s.
- **AC54** — When an exchange is committed to a session that has an open stream, the system
  shall recompute that session's exchanges that started after the committed exchange's start
  time, and for each one whose entries differ from what that stream last sent for it (or for
  which that stream has sent nothing), send on that stream an SSE event `exchange` carrying that
  exchange's full, current entry list (AC18 shape and ids). The record of what was sent is kept
  per open stream, not per session. The page shall replace all entries for that exchange id in
  place. The `entries` event and these `exchange` events form one batch for the committed
  exchange's row id (research D14).
- **AC55** — When any sequence of exchanges is committed to a session out of start order or out
  of row-id order, including across stream drops and reconnects, the page's live view of that
  session shall equal a fresh reload of it, which shall equal the CLI viewer's timeline for the
  same store (entry ids, order, kinds and text, apart from the 16 KiB cut).

### Live updates
- **AC27** — When the capture pipeline commits an exchange, the system shall publish it to the
  UI's broadcaster only after it is stored, so the UI never shows anything that isn't stored.
- **AC28** — When publishing to subscribers, the system shall never block the capture worker:
  a publish to any number of subscribers, slow or not, returns without waiting on them.
- **AC29** — When a subscriber's buffer of 64 live messages is full, the system shall drop that
  subscriber and close its stream; other subscribers are unaffected. The connect snapshot
  (AC22, AC24) is written by the stream itself and does not pass through that buffer.
- **AC30** — When 32 SSE streams are already open and another is requested, the system shall
  answer 503 with the JSON error envelope (code `too_many_streams`).

### Security
- **AC31** — When a request's Host header is not one of the UI origins' host:port, the system
  shall answer 421 and serve nothing else.
- **AC32** — When a request carries an Origin header that is not one of the UI origins, the
  system shall answer 403; no response from the UI carries any CORS header.
- **AC33** — When the UI answers any request (pages, assets, API, SSE, errors), the response
  shall carry `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` and a
  `Content-Security-Policy`: `default-src 'self'; script-src 'self'; style-src 'self';
  img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'`, except that
  a response from the exchange body endpoint (AC20) carries
  `Content-Security-Policy: default-src 'none'; sandbox` instead, so a stored HTML body can
  never run.
- **AC34** — When the page loads, it shall load no inline script or style and request nothing
  outside the UI origin (no CDN, no web font).
- **AC35** — When the page shows captured content (prompts, text, thinking, tool input, tool
  output, headers, bodies), it shall render it as text, never as HTML.

### Frontend
- **AC36** — When the page is served, it shall be plain HTML, CSS and ES-module JS embedded in
  the gateway binary, with every script and stylesheet `index.html` references present in the
  embedded files.
- **AC37** — When `#/` is opened, the page shall show the sessions as a table (client, models,
  first and last activity, counts, usage totals, an "active" badge), update it live from
  `/api/stream`, and page further with a "Load more" button.
- **AC38** — When `#/ungrouped` is opened, the page shall list the ungrouped exchanges, each
  linking to its exchange detail, with "Load more".
- **AC39** — When `#/sessions/{id}` is opened, the page shall show a header with the session's
  totals and the entries in order, grouped into turns that each start at a user prompt.
- **AC40** — When the timeline shows a tool call, it shall show it and its result (paired by
  tool_call_id) as one card: tool name, collapsible pretty-printed input, collapsible result, a
  red marker when is_error is true, and "pending" while no result has arrived.
- **AC41** — When the timeline shows a model call, it shall show a chip with model, status,
  duration and the four token counters, with unknown counters shown as unknown, not 0.
- **AC42** — When the user types in the text filter or picks a tool name, the timeline shall
  show only the loaded entries that match, and clearing the filters shall show them all again.
- **AC43** — When an entry is truncated, the timeline shall show a "truncated — open raw" link
  to that exchange's detail.
- **AC44** — When the user clicks the copy button on a prompt, tool input or tool result, the
  page shall copy that entry's full shown text to the clipboard.
- **AC45** — When new live entries arrive and the view is scrolled to the bottom, the page shall
  scroll to show them; when it is not at the bottom, it shall not move.
- **AC46** — When an active session's timeline is opened, the page shall subscribe to its
  stream; for an inactive session it shall not.
- **AC47** — When `#/exchanges/{id}` is opened, the page shall show the exchange detail, and load
  each raw body only when the user opens it.
- **AC48** — When a timestamp is shown, it shall be in the browser's local time, with the
  relative time ("3 min ago") on hover.
- **AC49** — When the OS prefers a dark or light colour scheme, the page shall use the matching
  theme.
- **AC50** — When an SSE connection fails or is dropped, the page shall keep showing the history
  it has, show "live: disconnected, retrying", and on reconnecting open a new stream whose
  snapshot replaces every exchange it holds, so it misses nothing and depends on no state the
  gateway kept for the old connection (AC24, AC54).

### Docs
- **AC51** — When the change is merged, ADR 0009 (web UI: vanilla JS, read-only, loopback-only,
  Preact+htm as the fallback) shall exist and supersede ADR 0007, which is marked superseded;
  and ADR 0010 (routing and real auth removed from v1; single provider, local single user; the
  routing seams stay) shall exist.
- **AC52** — When the change is merged, PLAN.md shall have: the phase table in the new order
  (web UI, Phase 6, file system and network, cost) with routing marked removed from v1
  (ADR 0010); routing and real auth in §6 out of scope; a UI row in the §8 stack table
  (vanilla JS + go:embed; React/Preact+Vite rejected for needing Node in the build, htmx for
  being a dependency that limits a rich timeline); and a `ui` scope in §11.
- **AC53** — When the change is merged, `config.example.yaml` shall document the `ui` block in
  the style of the other blocks, and the README shall have a UI section (how to open it, what it
  shows) and a status line that includes the web UI.

## Edge cases
- **E1** — Empty store (no sessions, no ungrouped) → empty lists with `next_after: null`; the
  page shows an empty state, not an error (AC15, AC16, AC37)
- **E2** — Store file missing when the gateway starts with capture on → capture creates it; the
  UI opens its reader after the store exists (AC1, AC9)
- **E3** — `limit=0`, negative, non-numeric → 400; `limit` above the cap → the cap (AC15, AC18,
  AC21)
- **E4** — An `after` value from a different list, or tampered → 400 (AC21)
- **E5** — A session gains exchanges while the user pages with `after` → no session appears
  twice in one walk; a session that moved to the top is picked up by the live stream (AC15,
  AC22)
- **E6** — Concurrent sub-agent calls in one session finish out of start order → the late
  exchange's entries slot in at their sorted position, and any later-started exchange whose
  entries the late one changed (its best match moved) is re-sent whole as an `exchange` event
  and replaced in place; live, reload and CLI agree (AC23, AC25, AC54, AC55)
- **E20** — A connection drops in the middle of one row id's batch → the reconnect's snapshot
  replaces every exchange, so the half-sent batch leaves nothing stale (AC24, AC54, research
  D14, D16)
- **E21** — The stream closes, the session goes inactive, or the gateway restarts → that
  stream's record of what it sent is dropped; a later commit on a stream with nothing recorded
  re-sends every later-started exchange whole, and a reconnecting page gets a full snapshot
  (AC24, AC50, AC54)
- **E7** — A stored body larger than 5 MiB → first 5 MiB and `X-Body-Truncated: true`; a body
  truncated by capture keeps the capture flag in the detail (AC19, AC20)
- **E8** — A body not stored (capture dropped or no body) → 404 `body_not_stored`; the page says
  "not stored" (AC20, AC47)
- **E9** — Prompt or tool output containing `<script>`, HTML or ANSI escapes → shown literally
  as text (AC35)
- **E10** — Request with `Host: evil.example:7199` (DNS rebinding) → 421; with
  `Origin: http://evil.example` → 403 (AC31, AC32)
- **E11** — Many slow tabs / a stuck subscriber → that subscriber is dropped; capture and proxy
  latency are unaffected (AC28, AC29)
- **E12** — 33rd SSE subscriber → 503; the page shows "live: disconnected, retrying" and keeps
  the history (AC30, AC50)
- **E13** — Gateway restarted while the page is open → the stream drops, the page retries,
  opens a new stream and its snapshot replaces every exchange (AC24, AC50)
- **E14** — A session that went inactive while open → its stream stays open until closed; no
  new subscription is made on the next visit (AC46)
- **E15** — Exchange still unparsed or parse failed → the timeline shows it as the CLI viewer
  does (folded), and usage is flagged missing, never 0 (AC11, AC41)
- **E16** — `ui.listen_addr: 0.0.0.0:7199`, `[::]:7199`, `192.168.1.5:7199` or the proxy
  address → config error (AC4)
- **E17** — UI port already in use → startup error; the proxy does not start either (AC7)
- **E18** — Page loads the timeline, and an exchange is committed before its stream opens → the
  stream's snapshot carries it; an exchange already in the timeline answer arrives again in the
  snapshot and replaces itself, so nothing is lost or shown twice (AC18, AC24, AC25)
- **E19** — `gateway backfill-sessions` assigns old exchanges to a session while its page is
  open → nothing is sent when they are assigned (backfill does not publish); they appear on the
  next reload or reconnect snapshot, or when a later commit re-sends them as later-started
  exchanges (AC24, AC54)
- **E22** — The connect snapshot fails (store timeout or other store error) → the server sends
  an SSE `snapshot_error` event whose data is the JSON error envelope with code `timeout` or
  `store_error` and a message holding no captured data, sets `retry: 30000`, and closes the
  stream; the page shows an error state for that session (keeping any history it has) and does
  not reconnect sooner than that retry. When the server closes the stream, the browser also
  fires its native `error` event; the page keeps the snapshot-error state shown through that
  event and through the reconnect attempt (not replacing it with "live: disconnected,
  retrying"), and clears it only when a later snapshot succeeds (AC10, AC24, AC50, research
  D16)

## Constraints
- Proxying and capture never wait on the UI: the UI has its own listener, its own read-only
  store connection, and the broadcaster's publish never blocks (AC28).
- Every store query from the UI takes the request's context with a 5 s timeout.
- No new Go dependency; no JS dependency; no build step. Static files live in the UI package's
  `web/` directory and are embedded with `go:embed`.
- Frontend structure: each component has one `render(data)` that rebuilds its own subtree from
  data; state lives in one small store module; no two-way binding.
- Captured content is untrusted: rendered as text only (AC35), strict CSP (AC33).
- Bounds: 64-message buffer of live messages per subscriber (the connect snapshot is not
  counted), 32 SSE subscribers, 16 KiB per text field in the timeline API and the streams, 5 MiB
  per body response, 200 sessions or 2000 entries per page.
- `make verify` stays green under `-race`.
- Required tests, among the others the plan lists: out-of-order completion (an exchange that
  started earlier but is stored later appears at its sorted position, both in the live
  `entries` event the page receives and in the timeline API after it; AC18, AC23, AC25); resume
  (a stream opened or reopened with no header, with `Last-Event-ID` or with `?since=` first sends
  a snapshot equal to a fresh timeline call, and an exchange committed out of row-id order, with
  a lower row id than one already sent, still reaches the page; AC24, AC55); stable ids (two
  timeline calls over the same store return the same ids; AC18); convergence (a fixture where a
  late-stored, earlier-started exchange changes the best match of an exchange the page already
  has: the `exchange` event re-sends that exchange, and the live view built from the events
  equals a fresh timeline call equals the CLI viewer's output; AC54, AC55); and snapshot failure
  (`TestStream_SnapshotFailureSendsErrorAndLongRetry`: a store that fails or times out during
  the connect snapshot yields a `snapshot_error` event with code `timeout` or `store_error`, a
  `retry: 30000` field, and a closed stream, with no `exchange` event sent; E22).
- The record of what was last sent per exchange is in memory, kept per open stream (not per
  session), only while that stream is open and its session active, and is dropped when the
  stream closes, the session goes inactive or the gateway restarts; correctness never depends on
  it surviving, nor on row-id order (AC24, AC50, AC54).
- No JS test framework: the frontend is checked by the embedded-assets test and the human's
  manual check.

## Decisions
Made by the user in the requirement (2026-10-31), recorded here; not open for change in this
feature:
- The UI runs in the gateway process on its own `http.Server` and listener, loopback-only, with
  no auth in v1; config block `ui.enabled` / `ui.listen_addr`.
- If capture is disabled the UI does not start (one warn line).
- Read-only store access through a separate `mode=ro` pool; timelines only from canonical events
  through the existing timeline package; reuse the CLI viewer's grouping and usage totals.
- "Active" = last exchange less than 5 minutes old, a named constant.
- GET-only JSON API under `/api` with opaque `after` values and the listed limits; SSE for live updates
  fed by the capture workers after commit; 64-message buffers, drop slow subscribers, 32
  subscribers max.
- Host and Origin checks, the CSP and the listed headers; render captured content as text.
- Vanilla JS (ES modules, custom elements, `<template>`, fetch, EventSource, hash routing), no
  dependencies or build; Preact+htm is the recorded fallback only.
- New v1 roadmap: web UI → Phase 6 harness hooks → file system and network visibility → Phase 9
  cost; routing and real auth removed from v1 (ADR 0010).

Smaller choices made while writing this spec, where the requirement left room, are logged with
their reasons in `research.md` (D1–D16; D11 supersedes D1, D14 supersedes D11's known limit,
D16 supersedes D11(c), D12 and D14's resume rule).

## Agnosticism check
Touches no axis: the UI reads only canonical events and stored exchanges through the timeline
and store packages, and names no provider and no client (AC12, extending the purity test to the
UI package and its web files). Client names are shown only as the stored `client` label.

Touches **capture** (not core): the capture pipeline gains an optional "exchange committed"
notification so the broadcaster is fed after commit (AC27); it never blocks the worker.
`internal/core/` is unchanged. Touches the **timeline** package only to expose what the UI needs
(a configurable cut length, entry positions); its output for the CLI stays the same.

Resolves no OQ. Supersedes ADR 0007 (viewer CLI-first) with ADR 0009; ADR 0010 records removing
routing and real auth from v1.

## Open questions
None. The requirement's decisions are recorded above; smaller choices are in research.md.

## Changelog
- v1 (2026-10-31) — first draft
  - review: entries get stable ids `<exchange_id>:<index>` and a cursor over (start, exchange id,
    index); the stream resumes by store row id, one exchange per `entries` event; the `reset`
    event is dropped (AC11, AC18, AC23–AC25, AC50; research D11–D13). Renumbered 008 → 009.
  - review: changed exchanges are re-sent whole as `exchange` events and replaced in place, so
    live = reload = CLI (AC54, AC55; AC50, E6, E13, E20, E21; research D14).
- v2 (2026-10-31) — reopened: AC54, AC24, AC55 and Constraints: the sent-record is kept per open stream, not per active session. On every connect or reconnect, a stream first sends a full snapshot of what it watches (the open session's exchanges as replace events, or the first page of the sessions/ungrouped list), then live events. Last-Event-ID and ?since are accepted, but correctness never depends on row-id order. The page applies exchange events as replace-by-exchange-id and never ignores an id it already holds.
  - AC22, AC23, AC24, AC25, AC29, AC50, AC54, AC55; E13, E18, E19, E20, E21; Constraints:
    connect snapshot replaces row-id resume; replace-by-exchange for every entry event; record
    per open stream (research D16, Q2).
  - E22 and a required test: a failed connect snapshot sends an SSE `snapshot_error` event, `retry: 30000`
    and closes; the page shows an error state and does not hot-loop (research D16 addendum).
- v3 (2026-10-31) — reopened: CR-001: Rename the paging parameter cursor to after
  - AC15, AC16, AC18, AC21 (CR-001): the paging parameter `?cursor=` is now `?after=` and the
    field `next_cursor` is now `next_after`, same opaque keyset semantics and 400 on a malformed
    value; E1, E4, E5 and the Decisions line reworded to match. Reason: "cursor" is a client name
    the purity test bans (AC12, D10). Earlier changelog lines keep their wording as history.
- v4 (2026-10-31) — reopened: CR-003: Rename tool_use_id to tool_call_id, two-level CSP, and T005/T008 gaps
  - AC18, AC40 (CR-003): the tool-call field is `tool_call_id` (was `tool_use_id`), the canonical
    name the store and timeline already use; `tool_use_id` is a provider wire name the purity test
    bans (AC12, D10). AC33 (CR-003): the exchange body endpoint carries the stricter
    `default-src 'none'; sandbox` CSP instead of the standard one; every other response is unchanged.
