---
feature: 009-web-ui
status: draft
version: 1
approved:
approved-by:
sha256:
fingerprint:
spec-fingerprint:
---
# Plan — 009-web-ui

## Summary
A new `internal/ui` package serves a read-only JSON API, SSE streams and an embedded vanilla-JS page
on its own loopback listener. It reads through a second `mode=ro` store connection and builds
timelines with the existing `timeline` package (extended with stable entry positions, a cut length
and thinking text). Capture gets one non-blocking "exchange committed" callback that feeds a hub; a
hub goroutine (not the capture worker) reads the store, builds the session once per commit and fans
out to bounded subscribers. Config, wiring, docs and ADRs follow.

## Context
- `cmd/gateway/run.go`: startup order (config, `startCapture`, bind, serve, graceful shutdown with
  `cfg.ShutdownTimeout`); `deps.listen`/`openStore` seams tests already use. `captureRun` must keep the
  store dir for the reader.
- `internal/config/ratelimit.go` + `load.go`: pattern for a block (`xxxSettings()`, `xxxFile` tags with
  unknown-key walk, `validateXxx` after file+env). New `ui.go` follows it.
- `internal/store/reader.go`, `reader_sessions.go`: `Reader` is `mode=ro` already but has no
  `context.Context`; `exchanges` is a rowid table (research D11).
- `internal/timeline/build.go`, `sessions.go`, `usage.go`: `BuildWithUsage`, `Summarize`, `callUsage`.
  The UI must reuse these, not reimplement them (AC11).
- `internal/capture/sink.go`, `pipeline.go`: `process` is where an exchange is stored and parsed.
- `internal/core/purity_test.go` line ~53: the directory list that gets the forbidden-identifier scan.
- `internal/server/server.go`: reference only; the UI builds its own `http.Server` (chi is already a dependency).

## Approach
`ui.Handler(deps)` returns one chi router: `Host`/`Origin`/headers/recover middleware, then `/api/*`
(GET only) and the embedded `web/` files. Store access goes through `store.Reader.WithContext(ctx)`, a
view that satisfies `timeline.Source` with `QueryContext`, so the existing timeline code is reused
unchanged in shape. Live updates: `capture.Config.OnCommit` is called after `SaveParse` (or after a
parse failure) with the exchange's identity; `ui.Hub.Publish` drops it into a bounded queue without
blocking; one dispatcher goroutine resolves the row id, builds the session timeline once, and hands
the same immutable snapshot to each subscriber of that session. Each stream turns snapshots into
`entries` and `exchange` events using its own record of what it has sent.

## Alternatives considered
### A1 — Dispatcher goroutine reads the store; per-stream sent-record (chosen)
- Pros: capture worker never touches the UI or the store reader (AC28); no cross-subscriber state, so a late joiner cannot be starved by what an earlier one received (AC54, E21)
- Cons: one session Build per commit per session with a stream; per-stream record is more memory than one per session
### A2 — Worker builds the event and pushes entries through the hub
- Pros: no second store read; lower latency
- Cons: store read plus timeline build on the capture path breaks "capture never waits on the UI" (research D15); needs a UI-owned reader inside capture
### A3 — UI polls the store (SQLite `data_version` / max rowid) with no capture hook
- Pros: zero change to capture; also sees `backfill-sessions`
- Cons: latency and idle wakeups; AC27 asks for publish after commit and a hook is cheap; polling cannot name which session changed without a diff

## Design

**Config (AC3-AC6).** `internal/config/ui.go`: `UI{Enabled bool; ListenAddr string}`, defaults `true` /
`127.0.0.1:7199`, settings `ui.enabled`, `ui.listen_addr` (env names fall out of `envName`), a `ui`
walk in `load.go` rejecting unknown keys, `validateUI` after file+env: host must parse as an IP in
127.0.0.0/8, `::1`, or the literal `localhost`, and `ListenAddr != cfg.ListenAddr` (compare as
strings and by resolved host:port); errors `Key: "ui.listen_addr"`. When `ui.enabled=false` validation of the address is skipped.

**Store reader (AC9, AC10, AC15-AC17, AC20).** Existing `Reader` methods keep their signatures and
delegate to ctx-taking internals with `context.Background()`. New file `internal/store/reader_ctx.go`:
`(*Reader).WithContext(ctx) *CtxReader` (implements `timeline.Source`: `SessionExchanges`, `Events`,
`Content`, using `QueryContext`) plus UI-only reads: `SessionPage(after SessionCursor, limit)`,
`UngroupedPage(after, limit)`, `SessionByKey(principal, client, key)`, `RowID(requestID)`,
`SessionExchangeRowsAfter(session, rowid)` and a one-snapshot read returning exchanges with their
row ids and `MAX(rowid)` for `stream_since`. The UI package opens its own `OpenReader`; the DSN is
`mode=ro`, so a write fails at SQLite (AC9). Every call is bound to the request context plus a 5 s
`context.WithTimeout` (AC10): `DeadlineExceeded` maps to 504 `timeout`, `Canceled` to no body, other errors to
500 `store_error` with a fixed message (research D5).
Paging is keyset with a unique tie-breaker, already in the sort the CLI uses: sessions order by
`MAX(started_at) DESC` then `principal_id, client, session_key`, which together are the group key
(`internal/store/reader_sessions.go:29-30`); ungrouped exchanges by `started_at DESC, request_id`
(`reader_sessions.go:98`); the timeline by (start, request id, index). Every cursor holds the
full tuple, so rows sharing a sort key are neither repeated nor skipped. Sessions sort by last
activity, so a live session can move up past the cursor while the user pages: Load more never
repeats it and never skips a session it has not shown, and the moved session appears at the top
through the `/api/stream` session event, which the list applies in place.

**Timeline extensions (AC11, AC18, AC35).** In `internal/timeline`: `Options.Cut int` (0 keeps
`DefaultEntryLimit`; `Full` unchanged; the UI passes 16 KiB), so CLI output is byte-identical.
`Entry` gains `Index` (position within its own exchange's entries, set by `BuildWithUsage`), plus
`Thinking string` filled for reasoning blocks that carry thinking text in the stored block (the CLI render
ignores it). `timeline.SessionStats(src, row)` returns model-call, tool-call and error counts plus the
usage `Summary` from exchanges+events only (no content reads), sharing `callUsage`, so list pages stay
cheap. The UI's entry mapping (research D2) lives in `internal/ui/entries.go`: id `<request_id>:<index>`,
a message with thinking emits a `thinking` entry, `Omitted > 0` becomes `truncated: true`. Ordering key
is (exchange start, request id, index) which is exactly the order `BuildWithUsage` emits, so
cursors are opaque base64url of that key.
Entries are **not** append-only, so the live path must handle changes. Within one exchange nothing
changes after it is committed: tool calls and results are separate entries (pairing is the page's
job), thinking text and the model call's usage come from that exchange's own events, all stored
before `OnCommit` runs. But each exchange's entries are its diff against the best-matching exchange
that *started* earlier (`internal/timeline/build.go:124`, `bestMatch` at `build.go:195`). A
late-stored exchange that started earlier can change that match, so an already-sent later exchange
can gain or lose entries, gain a "history diverged" marker, and see its indexes shift. Because the
index moves, `<request_id>:<index>` is stable only within one version of an exchange, so the unit
of update is the whole exchange (`exchange` event, replace in place by exchange id), not a single
entry. `TestBuild_EarlierStartedExchangeChangesLaterEntries` pins this: it fails if the timeline
ever becomes append-only.

**API (AC13-AC21).** Routes under `/api`: health, sessions, ungrouped, sessions/{id}, timeline, exchanges/{id},
body, stream, sessions/{id}/stream. A method guard answers 405+`Allow: GET` for anything but GET
(HEAD included) before routing. `writeErr(w, status, code, msg)` is the only error path, JSON
envelope. Session `{id}` and cursors are opaque base64url (research D3); a cursor carries a list kind
tag so one from another list is 400 (E4). Limits: sessions/ungrouped default 50 cap 200, timeline
default 500 cap 2000; `limit<=0` or non-numeric is 400, above cap clamps (E3). Session pages order
by `MAX(started_at) DESC, principal, client, key`, the cursor holds the last row's sort tuple, so a
walk never repeats a session (E5). Timeline: build the whole session via `BuildWithUsage` (the CLI's
code), map, slice after the cursor; `stream_since` is read in the same read transaction as the
exchanges. Body: `Content(hash)`, decompressed by the reader, first 5 MiB, `X-Body-Truncated`, stored
`content-type` from the exchange's headers (`body_not_stored` 404 if hash is empty or missing).
Active = `now - last_at < ActiveWindow` (const 5 min, research D9).

**Live (AC22-AC30, AC54, AC55).** Two tasks: the hub and live-only streams (T008), then resume
and entry updates (T009).

*Hub (T008).* `Publish(Committed)` is `select { case <-done: return; case q <- c: default:
overflow.Store(true) }`: it never waits, and after `Close` it is a silent no-op. The queue is never
closed (publishers may still be sending from capture workers that drain during shutdown), so no
send can panic, and nothing relies on panic recovery for this. On overflow the dispatcher closes all
subscribers, which reconnect and refetch (AC50). The dispatcher takes one commit, then drains
everything else already queued, and groups the lot by session, so N commits queued for one session
cost one store read. Per session in the group: if at least one session stream watches it, build
the session once (`snapshot{row ids, session summary, entries by exchange, order}`) and hand the
same immutable snapshot to those subscribers; if none does, compute `SessionStats` only (no content
reads, no timeline build). Either way `/api/stream` subscribers get the session summary. Each
subscriber has a `chan` of 64; a full channel drops that subscriber (close and remove) and no other
(AC29). 32 concurrent streams max, 33rd gets 503 `too_many_streams` (AC30). Heartbeat comment every
15 s via a ticker (AC26, injectable interval for the test). `Hub.Close()` closes `done` and cancels
every stream context so `http.Server.Shutdown` can finish (AC2).

*Streams and convergence (T009).* Each stream keeps a sent-record: per exchange, a hash of the
mapped entry list it last sent for it. For a snapshot, in row-id order of the committed exchanges
it carries: emit `entries` for each committed exchange, then, for every exchange that started after
the earliest of them, an `exchange` event carrying its whole list when its hash differs from the
record or there is none; update the record. Only the last event of the batch has `id:` (the batch's
highest row id, research D14), so a drop mid-batch resends the batch, which replace-by-exchange and
known-id skipping make harmless (E20). This is how an entry the client already has gets updated
(see "Entries are not append-only" above). Open with `Last-Event-ID` (wins) or `?since`: subscribe
first, then catch up from a fresh snapshot with an **empty** record, applying the same batch rule to
the exchanges with row id above the cursor. With an empty record, every exchange that started after
the earliest caught-up one is resent whole, so entries the client received before its cursor that a
later commit changed are corrected on resume. Then drain live, skipping row ids already sent (AC24);
neither header means live only. The record is dropped when the stream closes or when the snapshot
shows the session inactive (E21). The page model for AC55 is mirrored in a small Go test helper
(`applyEvents`) that applies `entries`/`exchange` events with the same rules as `web/` (insert
sorted, ignore known ids, replace by exchange id).

**Capture hook (AC27, AC28).** `capture.Config.OnCommit func(Committed)` where `Committed{RequestID,
PrincipalID, Client, SessionKey}`. Timing: `process` calls it once, as its last step, after
`SaveParse` has returned. `SaveParse` is one transaction that sets the parse status and inserts every
event (`internal/store/exchange.go:60-93`), so when the callback runs, the row and all its events
are committed and readable through the read-only connection, or `SaveParse` has failed for good
(no retry exists) and the row stays as `SaveExchange` left it, shown folded (research D7). It is never
called between `SaveExchange` and `SaveParse`, and never when `SaveExchange` failed. Blobs need no
change: `Store.write` writes, fsyncs and renames every blob, and syncs its directory, before it opens
the transaction that inserts the rows pointing at it (`internal/store/store.go:152-180`,
`internal/store/content.go:76-112`, tested by `TestStore_BlobBeforeRow`). nil is a no-op; the
callback is wrapped so a panic is recovered and logged. `Store` interface unchanged.

**Security (AC8, AC31-AC35).** Middleware order: security headers (every response, including 421/403/500), recover
(logs path, 500 envelope if nothing written), Host check (421), Origin check (403 when present and not
allowed; no CORS headers are ever set). Allowed set computed from the bound listener's port and
`ui.listen_addr` host (research D8). Static files are served by an embedded `fs.FS` file server,
GET only. `web/` contains no inline script/style and no absolute URLs; a test scans `index.html`
and JS/CSS for `http://`, `https://`, `<style`, inline `on*=` and `<script>` without `src`.
Frontend only ever sets `textContent` (never `innerHTML`) for captured data; a test greps `web/*.js`
for `innerHTML`, `outerHTML`, `insertAdjacentHTML`, `document.write` (AC35).

**Process wiring (AC1, AC2, AC5-AC7).** `run.go`: after `startCapture` and proxy bind, if
`cfg.UI.Enabled && capt != nil` open `store.OpenReader(dir)`, bind `cfg.UI.ListenAddr` through `d.listen`;
failure closes the proxy listener and capture and exits `exitRuntime` with a log line naming
`ui.listen_addr` and a fixed reason (E17). With `ui.enabled` and capture off: one warn line, no UI. With
`ui.enabled` false: nothing and no log. The hub is created before the sink (so `OnCommit` can be given
to `capture.Config`) and started when the reader is open. Shutdown: `hub.Close()`, `uiSrv.Shutdown(shutdownCtx)`
with the proxy's, then the reader closes after capture stops; UI runs in `logging.Go`.

**Frontend (AC34-AC50).** `internal/ui/web/`: `index.html`, `style.css` (prefers-color-scheme), `app.js`
(router, `#/`, `#/ungrouped`, `#/sessions/{id}`, `#/exchanges/{id}`), `store.js` (one state object, subscribe/notify),
`api.js` (fetch + EventSource wrapper with since/Last-Event-ID handling), `entries.js` (pure functions:
insertSorted by key, replaceExchange, pairTools, groupTurns, filter, formatTime), and components as
custom elements with a single `render(data)` each (`session-list`, `session-view`, `tool-card`,
`model-chip`, `exchange-detail`). Subscribes only if the session is active; on SSE error shows "live:
disconnected, retrying", keeps history, refetches timeline and reopens with `?since=stream_since` (AC50).
Autoscroll only when within a few px of the bottom (AC45). No new Go or JS dependency.

**Docs (AC51-AC53).** ADR 0009 (supersedes 0007, which gets `status: superseded`), ADR 0010; PLAN.md phase table,
§6, §8 row, §11 `ui` scope; `config.example.yaml` `ui` block; README section.

## AC coverage
| AC | Where | Verified by |
|---|---|---|
| AC1, AC2 | cmd/gateway/run.go, ui/hub.go | TestRun_UIServesOnOwnListener, TestRun_ShutdownClosesUIStreams, TestRun_CommitsDuringShutdownAfterHubClose, TestHub_PublishAfterCloseIsNoOp |
| AC3, AC4 | config/ui.go, load.go | TestLoad_UIPrecedenceAndUnknownKey, TestLoad_UIListenAddrMustBeLoopbackAndDistinct |
| AC5, AC6 | run.go | TestRun_UIDisabledOpensNothingLogsNothing, TestRun_UIWithCaptureOffWarnsOnce |
| AC7 | run.go | TestRun_UIBindFailureExitsRuntime |
| AC8 | ui/middleware.go | TestUI_PanicRecoveredKeepsServing |
| AC9 | store/reader_ctx.go | TestReader_WriteThroughReaderFails |
| AC10 | store/reader_ctx.go, ui/errors.go | TestUI_StoreTimeoutIs504, TestUI_CancelledRequestAbandonsQuery |
| AC11 | timeline/, ui/entries.go | TestUI_TimelineMatchesCLIViewer, TestUI_SessionStatsMatchCLIUsage |
| AC12 | core/purity_test.go | TestCore_NoProviderOrClientIdentifiers (adds ../ui) |
| AC13, AC14 | ui/api.go | TestAPI_NonGETIs405WithAllow, TestAPI_Health |
| AC15, AC16, AC17 | ui/api_sessions.go, store/reader_ctx.go | TestAPI_SessionsPagedNewestFirst, TestAPI_UngroupedSummaryShape, TestAPI_SessionSummary, TestReader_SessionPageTiesBrokenByKey, TestReader_UngroupedPageTiesBrokenByRequestID |
| AC18 | ui/api_timeline.go | TestAPI_TimelineStableIdsAndOrder, TestAPI_TimelineCutsAt16KiB |
| AC19, AC20 | ui/api_exchange.go | TestAPI_ExchangeDetail, TestAPI_BodyTruncatesAt5MiB, TestAPI_BodyNotStored404 |
| AC21 | ui/api.go | TestAPI_NotFoundAndBadRequestCodes |
| AC22, AC23 | ui/hub.go, ui/stream.go | TestStream_GlobalSendsSessionEvent, TestStream_SessionSendsEntriesWithRowID, TestHub_CoalescesQueuedCommitsPerSession, TestHub_UnwatchedSessionGetsStatsOnly |
| AC24 | ui/stream.go | TestStream_ResumeLastEventIDAndSince, TestStream_ResumeResendsExchangesChangedBeforeCursor |
| AC25 | web/entries.js, ui/stream_test | TestLive_OutOfOrderInsertsAtSortedPosition |
| AC26 | ui/stream.go | TestStream_HeartbeatEveryInterval |
| AC27 | capture/pipeline.go | TestSink_OnCommitAfterStore, TestSink_OnCommitSeesEventsThroughReadOnlyReader, TestSink_OnCommitAfterSaveParseFails |
| AC28 | ui/hub.go, capture | TestHub_PublishNeverBlocks, TestHub_PublishAfterCloseIsNoOp, TestSink_SlowOnCommitDoesNotStallWorker, TestSink_PanickingOnCommitIsRecovered |
| AC29, AC30 | ui/hub.go, ui/stream.go | TestHub_FullBufferDropsOnlyThatSubscriber, TestStream_33rdIs503 |
| AC31, AC32, AC33 | ui/middleware.go | TestUI_BadHostIs421, TestUI_BadOriginIs403NoCORS, TestUI_SecurityHeadersOnEveryResponse |
| AC34, AC35, AC36 | ui/web, ui/web.go | TestWeb_NoInlineOrExternalReferences, TestWeb_NoHTMLInjectionSinks, TestWeb_ReferencedAssetsAreEmbedded |
| AC37, AC38 | web/session-list.js, app.js | manual M1 (T011, T016) |
| AC39, AC40, AC41 | web/session-view.js, tool-card.js, model-chip.js | manual M2 |
| AC42, AC43, AC44 | web/session-view.js | manual M2 |
| AC45, AC46 | web/session-view.js, api.js | manual M3 |
| AC47 | web/exchange-detail.js | manual M4 |
| AC48, AC49 | web/entries.js, style.css | manual M1 |
| AC50 | web/api.js | TestStream_ReconnectWithStreamSinceMissesNothing; manual M3 |
| AC51, AC52 | docs/decisions, PLAN.md | manual review |
| AC53 | config.example.yaml, README | TestDocs_ConfigExampleParses |
| AC54 | timeline/build.go, ui/stream.go | TestBuild_EarlierStartedExchangeChangesLaterEntries, TestStream_LaterStartedExchangeResentWhole, TestStream_ReceivedEntryUpdatedAfterLaterCommit, TestStream_ResumeResendsExchangesChangedBeforeCursor |
| AC55 | ui/stream_test | TestLive_ConvergesWithReloadAndCLI |

## Test strategy
Behaviour tests over a real temp store with fixture exchanges and `httptest` servers, under `-race`. Named
tests are in the AC table; each task in tasks.md maps the spec's E-ids to a test (E1-E21). The four
required tests: out-of-order = `TestLive_OutOfOrderInsertsAtSortedPosition`; resume =
`TestStream_ResumeLastEventIDAndSince`; stable ids = `TestAPI_TimelineStableIdsAndOrder`; convergence =
`TestLive_ConvergesWithReloadAndCLI`. No JS tests (spec): Go scan tests plus manual checks M1-M4.

## Risks
- Rebuilding a whole session is O(session). Bounded: list pages and unwatched sessions use `SessionStats` (no content reads); queued commits are coalesced per session, so a burst costs one Build per watched session; 5 s timeout. T016 times a rebuild of the largest real session and records it in research.md.
- The hook sits on the capture path. Limit: callback is non-blocking and panic-wrapped; tested with a stalled consumer.
- A reader opened before the store migrates would refuse (`checkSchema`). Limit: opened only after `startCapture` returned.
- Convergence (AC54/55) is subtle. Limit: reload and CLI are the source of truth; any drift is healed by reconnect-refetch.
- Frontend correctness cannot be machine-checked beyond scans. Limit: pure logic isolated in `entries.js`, manual checks listed, and the Go model mirrors it.

## Decisions
- **Per-stream sent-record (accepted by the human).** The record of what was last published per exchange (AC54) is kept per open stream, not per session. It is dropped when the stream closes, the session goes inactive, or the gateway restarts. This narrows the "per active session" wording of AC54 and the Constraints without changing observable behaviour: a late joiner is never starved by what an earlier stream received, and "nothing recorded" still means "send it". Tests (AC54, AC55, E21) are written against this. The spec's wording is to be brought in line through `/al-change spec`, not left to this note.

## Open questions
- **Q1** — Thinking text: the stored canonical message keeps only block hashes and the CLI prints `[reasoning]`. If a reasoning block's content has no readable text, should the UI emit a `thinking` entry with the placeholder text or omit it? (recommended: omit — the spec says nothing is invented when no thinking block text is stored) → decided: omit (a `thinking` entry only when readable thinking text is stored; never the `[reasoning]` placeholder)

## Changelog
- v1 (2026-10-31) — first draft
- v1 (2026-10-31) — review fixes, still draft: entries stated as not append-only and the update unit fixed as the whole exchange; per-stream record holds an entry-list hash per exchange; resume catches up with an empty record so pre-cursor changes are resent; OnCommit timing after `SaveParse` spelled out (blob order already correct); hub coalesces commits per session, builds only for watched sessions, publish after `Close` is a no-op; paging tie-breakers and moving sessions documented; Live split across T008/T009
