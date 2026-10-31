---
feature: 009-web-ui
---
# Research & decisions — 009-web-ui

## Open questions
<!-- Agents add a question here when they are blocked; the loop stops until you answer.
     Answer by changing "(open)" to "(answered)" and writing the answer underneath:
- **Q1** (open) T003 — <question> — options: A) … B) …
-->

## Decisions
<!-- - D1 (date) — <decision> — why -->
The requirement (2026-10-31) said: where something is still ambiguous, pick the simplest option
consistent with it and log it here instead of blocking. These are those picks, made while
writing spec v1; approving the spec confirms them.

- D1 (2026-10-31) — A timeline entry's `seq` is its 1-based position in the session's timeline as
  `timeline.BuildWithUsage` orders it (exchange start time, then request id). When a newly
  committed exchange sorts after every exchange already in the timeline, its entries get the
  next seqs and go out as an `entries` event. When it sorts earlier (a concurrent sub-agent call
  that started earlier but finished later), positions shift, so the stream sends `reset` and the
  page reloads the timeline from the start (AC25). — why: the timeline package has no stable id
  per entry; entries are diffs against the best-matching earlier exchange, so only positions
  exist. Ordering by commit instead would make the UI disagree with the CLI viewer (AC11 and the
  manual check). A full reload on reset is cheap on one local machine.
- D2 (2026-10-31) — API entry kinds map from the timeline kinds: `message` with role user →
  `user_prompt`, role assistant → `assistant_text`, a thinking block → `thinking`; `tool_call`,
  `tool_result` and `error` keep their names; an exchange header or folded line that carries a
  model call → `model_call`; one that does not → `exchange`; the "history diverged" marker →
  `marker`. — why: the requirement's list leaves out `exchange` and `marker`, but the CLI viewer
  shows both, and AC11 wants the same entries. Thinking is emitted only when the stored message
  content holds a thinking block (the canonical message event keeps the raw blocks); nothing is
  invented when it doesn't.
- D3 (2026-10-31) — A session's `{id}` is opaque: base64url (no padding) of principal, client
  and session key joined by NUL. The summary also returns `session_key` in clear. — why: a store
  session is keyed by all three (`SessionRow`), and two clients can share a session key, which
  the CLI handles by printing several sessions. An opaque id names exactly one. Cursors are
  opaque the same way (base64url of the last row's sort key).
- D4 (2026-10-31) — HEAD on an `/api` route is answered 405 like any other non-GET method, with
  `Allow: GET`. Static assets also answer GET only. — why: the requirement says "GET only; any
  other method returns 405". Go's mux would otherwise route HEAD to GET handlers, so this is
  stated to stop that silently happening.
- D5 (2026-10-31) — A store query that hits the 5 s timeout answers 504 with code `timeout`; any
  other store error answers 500 with code `store_error` and a message that holds no captured
  data. A request the client cancelled gets no body. — why: the requirement fixes the envelope
  but not the status per failure; 504 says "the gateway gave up waiting".
- D6 (2026-10-31) — The ungrouped summary per exchange uses the session summary's fields: id is
  the request id, first_at = last_at = its start, exchange count 1, model-call count 0 or 1,
  tool-call and error counts from its events, and its usage. Ungrouped exchanges are never
  "active" (false). — why: "the same summary shape per exchange"; an exchange has no ongoing
  activity to watch, and it has no session stream.
- D7 (2026-10-31) — The broadcaster is notified once per exchange, at the end of the capture
  pipeline, when the raw exchange was stored, whether or not its parse or `SaveParse` succeeded.
  A failed `SaveExchange` publishes nothing. — why: "never shows anything that isn't stored": the
  row exists, and the timeline shows an unparsed exchange as folded, as the CLI does. Not
  publishing would hide a stored exchange until reload.
- D8 (2026-10-31) — The accepted Host values are `127.0.0.1:<port>`, `localhost:<port>`,
  `[::1]:<port>` and, when `ui.listen_addr` names another loopback address (e.g. `127.0.0.2`),
  that `host:<port>` too. Origins are the same set with `http://`. — why: with only the three
  named hosts, a valid config like `127.0.0.2:7199` would answer 421 to every request from the
  address it listens on. Adding the listener's own literal host keeps DNS-rebinding protection
  (no other name is accepted).
- D9 (2026-10-31) — "Active" uses the start time of the session's newest stored exchange
  (`MAX(started_at)`, the value the session list already sorts by), compared with the gateway's
  clock at the time of the request. — why: it is what the store indexes; a long stream that
  started 6 minutes ago and is still running is a rare edge, and its session becomes active again
  on its next exchange.
- D10 (2026-10-31) — The purity test's check covers the whole UI package directory, including
  `web/`. So no page text, script or stylesheet may spell a provider or client name (for example
  the page title is "LLM Gateway", not a client's name); client names appear only as data from
  the API. — why: the requirement asks for the purity test to cover the UI package, and the
  test already walks every file in a directory, not only `.go` files.
- D11 (2026-10-31) — Supersedes D1 (user's review of spec v1). No timeline position and no
  `reset` event. (a) Every entry's id is `<exchange_id>:<index>`, its index within its own
  exchange's entries; the timeline API orders by (exchange start time, exchange id, index), the
  CLI viewer's order, and pages with an opaque cursor over that key. (b) The page inserts live
  entries at their sorted position by the same key, so an exchange that started earlier and was
  stored later slots into the middle without a reload. (c) The SSE cursor is the exchanges
  table's rowid, the store's insert order: each `entries` event carries one stored exchange's
  entries with event id = its rowid, and `Last-Event-ID` resumes with the session's exchanges
  stored after it. — why: positions shift when a concurrent sub-agent call finishes out of start
  order, so they cannot be ids or resume points; insert order never shifts. The rowid is safe as
  a cursor because `exchanges` is a rowid table (only `content` and `events` are `WITHOUT
  ROWID`) and nothing ever deletes an exchange, so a new row always gets a higher rowid.
  Known limit, accepted: the timeline package diffs each exchange against the best-matching
  exchange that started earlier. A late-stored exchange can therefore change the shared prefix,
  and so the entries, of a later-started exchange already sent live. Those are not re-sent, so
  until a reload the live view can show a little more or less of that exchange than the CLI.
  This is rare: a sub-agent's thread shares little prefix with the main thread. The page and the
  CLI agree after a reload. Re-sending affected exchanges would need replace semantics per
  exchange, which the requirement did not ask for.
- D12 (2026-10-31) — EventSource sets `Last-Event-ID` only on its own reconnects, so the first
  connection needs a start point: the timeline answer carries `stream_since` (the highest rowid
  among the session's exchanges it includes, read in the same snapshot), and the stream also
  accepts `?since=<rowid>`; `Last-Event-ID` wins when both are present. The page drops any entry
  whose id it already has. — why: without it, an exchange committed between the timeline fetch
  and the stream opening is lost (or, with a looser start point, shown twice).
- D13 (2026-10-31) — Exchanges that `gateway backfill-sessions` assigns to a session later keep
  their old rowids, so a live stream does not send them; they appear on reload. — why: backfill
  is an offline, one-off repair command; streaming its result is not worth a second cursor.
- D14 (2026-10-31) — Supersedes the "known limit, accepted" part of D11 (user's review): the
  limit is fixed, bounded. When an exchange is committed, only the session's exchanges that
  started after it are recomputed, since only their best match can change. Each is compared with
  the entries last published for it; one that differs, or has nothing recorded, goes out whole
  as an SSE `exchange` event and the page replaces that exchange's entries in place. The record
  of published entries is in memory, per active session with an open stream, dropped when the
  session goes inactive or the gateway restarts; "nothing recorded" means "send it", so losing
  the record costs extra events, never wrong ones. On reconnect the page refetches the timeline
  and opens the stream from `stream_since`, so it relies on none of that state. — why: the
  user asked that live, reload and CLI always agree (AC55).
  Batch ids, a pick of mine: one committed exchange produces a batch (its `entries` event plus
  any `exchange` events). Only the last event of a batch carries `id: <row id>`; SSE keeps the
  previous last-event-id for events without one, so a drop mid-batch resumes from the previous
  row id and the whole batch is resent, which replace-in-place and known-id skipping make
  harmless. The requirement said the event id is the triggering exchange's row id; this keeps
  that for the batch and makes resume exact.
  Numbered D14, not D13 as the review said, because D13 (backfill) already exists and research
  ids are never reused.
- D15 (2026-10-31) — Note for the plan, not a contract: the recompute reads the store, so it
  must not run on the capture worker (AC28). The worker only records the committed exchange's
  session and row id and signals without blocking; a broadcaster goroutine reads what changed
  and fans out. — why: a store read on the capture path would slow capture whenever the UI is
  open.

## Notes
- 2026-10-31 — Grounding read while drafting: `store.OpenReader` already opens `mode=ro` and is
  safe for concurrent use, but its methods take no `context.Context`, so AC10 needs context-aware
  reads (a plan matter). `timeline.Options` has only `Full`; the 16 KiB cut needs a cut length
  (plan). `timeline.Entry` has no seq (D1). The purity test lives in
  `internal/core/purity_test.go` and lists `../timeline` with the full forbidden list.
- 2026-10-31 — Exchange timings: the store holds `started_at`, `ttfb_ns` and `ended_at`; duration
  is `ended_at - started_at`.
