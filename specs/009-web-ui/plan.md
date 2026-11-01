---
feature: 009-web-ui
status: done
version: 3
approved: 2026-10-31T16:51:28Z
approved-by: human
sha256: 616e8cc51501f5da1e0ce5baa9d1d5ca3775279a76b1eb533741998cadb5be52
fingerprint: e2962ea3ecddb732149bde3bdebbea7a69e3cdf1405e3596e96c3a58b845a6d0
spec-fingerprint: 665ac9bd04ff253338b0fef5b401ac657fe99866a32f1cbae2509bb8e9b03759
previous: 45916f6
---
# Plan — 009-web-ui

## Summary
A new `internal/ui` package serves a read-only JSON API, SSE streams and an embedded vanilla-JS page
on its own loopback listener. It reads through a second `mode=ro` store connection and builds
timelines with the existing `timeline` package (extended with stable entry positions, a cut length
and thinking text). Capture gets one non-blocking "exchange committed" callback that feeds a hub; a
hub goroutine (not the capture worker) reads the store and fans out to bounded subscribers. Every
stream connect starts with a full snapshot; nothing depends on row-id order (spec v2, D16).

## Context
- `cmd/gateway/run.go`: startup order (config, `startCapture`, bind, serve, graceful shutdown with
  `cfg.ShutdownTimeout`); `deps.listen`/`openStore` seams tests already use. `captureRun` must keep the store dir.
- `internal/config/ratelimit.go` + `load.go`: pattern for a block (`xxxSettings()`, `xxxFile` tags with
  unknown-key walk, `validateXxx`). New `ui.go` follows it.
- `internal/store/reader.go`, `reader_sessions.go`: `Reader` is `mode=ro` already but has no `context.Context`.
- `internal/timeline/build.go`, `sessions.go`, `usage.go`: `BuildWithUsage`, `Summarize`, `callUsage`; the UI reuses them (AC11).
- `internal/capture/sink.go`, `pipeline.go`: `process` stores and parses an exchange; workers default to 2 (`internal/config/capture.go:34`).
- `internal/core/purity_test.go` ~line 53: directories scanned for forbidden identifiers.
- `internal/server/server.go`: reference; chi is already a dependency.

## Approach
`ui.Handler(deps)` returns one chi router (middleware, `/api/*` GET only, embedded `web/`). Store access goes
through `store.Reader.WithContext(ctx)`, a `timeline.Source` using `QueryContext`. `capture.Config.OnCommit`
fires after `SaveParse`; `ui.Hub.Publish` queues it without blocking; one dispatcher goroutine reads the store
and fans out. Each stream writes its snapshot itself (after subscribing), then drains live messages into
`entries`/`exchange` events using its own sent-record. The page replaces by exchange id.

## Alternatives considered
### A1 — Dispatcher goroutine reads the store; per-stream sent-record; snapshot on connect (chosen)
- Pros: capture worker never touches the UI or store reader (AC28); no cross-subscriber state, no row-id dependence (AC24, AC54)
- Cons: one session Build per commit per watched session; a whole-session snapshot per connect (local only)
### A2 — Worker builds the event and pushes entries through the hub
- Pros: no second store read; lower latency
- Cons: store read plus timeline build on the capture path breaks "capture never waits on the UI" (D15)
### A3 — Row-id resume (`Last-Event-ID`/`?since`) with per-session record
- Pros: small reconnects; no snapshot cost
- Cons: commit order differs from row-id order with 2 workers, so a resume can skip an exchange for good (D16)

## Design

**Config (AC3-AC6).** `internal/config/ui.go`: `UI{Enabled bool; ListenAddr string}`, defaults `true` /
`127.0.0.1:7199`, settings `ui.enabled`, `ui.listen_addr`, a `ui` walk in `load.go` rejecting unknown
keys, `validateUI` after file+env: host an IP in 127.0.0.0/8, `::1` or `localhost`, and not the proxy
`listen_addr` (strings and resolved host:port); errors `Key: "ui.listen_addr"`. Skipped when `ui.enabled=false`.

**Store reader (AC9, AC10, AC15-AC17, AC20).** Existing `Reader` methods keep signatures and delegate to
ctx-taking internals with `context.Background()`. New `internal/store/reader_ctx.go`:
`(*Reader).WithContext(ctx) *CtxReader` (`timeline.Source`: `SessionExchanges`, `Events`, `Content`, via
`QueryContext`) plus `SessionPage(after, limit)`, `UngroupedPage(after, limit)`, `SessionByKey`, `RowID(requestID)`
and a one-snapshot session read returning exchanges with row ids and `MAX(rowid)` (for `stream_since`).
The UI opens its own `OpenReader` (`mode=ro`: a write fails at SQLite, AC9). Every call gets the request
context plus a 5 s timeout (AC10): `DeadlineExceeded` is 504 `timeout`, `Canceled` no body, anything else 500
`store_error` with a fixed message (D5).
Paging is keyset with a unique tie-breaker already in the CLI sort: sessions by `MAX(started_at) DESC` then
`principal_id, client, session_key` (`reader_sessions.go:29-30`); ungrouped by `started_at DESC, request_id`
(`:98`); timeline by (start, request id, index). The `after` values hold the full tuple, so tied rows are neither
repeated nor skipped. Paging parameter is `?after=`, response field `next_after` (CR-001). A live session can move up past the `after` position while the user pages: Load more never
repeats it, and `/api/stream` session events move it to the top in place.

**Timeline extensions (AC11, AC18, AC35).** `internal/timeline`: `Options.Cut int` (0 keeps
`DefaultEntryLimit`; the UI passes 16 KiB, so CLI output is byte-identical); `Entry.Index` (position within
its exchange, set by `BuildWithUsage`); `Entry.Thinking` filled only when the stored block has readable
thinking text (Q1: otherwise no `thinking` entry). `timeline.SessionStats(src, row)` returns model-call,
tool-call, error counts and the usage `Summary` from exchanges+events only (no content reads), sharing `callUsage`.
The UI entry mapping (D2) lives in `internal/ui/entries.go`: id `<request_id>:<index>`, `Omitted > 0` becomes
`truncated: true`. `after` values are opaque base64url of (start, request id, index), the order `BuildWithUsage` emits.
Entries are **not** append-only: each exchange's entries are its diff against the best-matching exchange that
*started* earlier (`build.go:124`, `bestMatch` at `:195`), so a late-stored, earlier-started exchange can
change a later exchange's entries and shift its indexes. `<request_id>:<index>` is stable only within one
version of an exchange, so the unit of update is the whole exchange, replaced by exchange id.
`TestBuild_EarlierStartedExchangeChangesLaterEntries` pins this. Within one exchange nothing changes after commit.

**API (AC13-AC21).** Routes under `/api`: health, sessions, ungrouped, sessions/{id}, timeline, exchanges/{id},
body, stream, sessions/{id}/stream. A method guard answers 405+`Allow: GET` (HEAD included) before routing;
`writeErr(w, status, code, msg)` is the only error path. Session `{id}` and `after` values are opaque base64url
(D3); an `after` value carries a list-kind tag, so one from another list is 400 (E4). Limits: sessions/ungrouped
default 50 cap 200, timeline default 500 cap 2000; `limit<=0` or non-numeric is 400, above cap clamps (E3).
Timeline: build the whole session via `BuildWithUsage`, map, slice after the `after` value; `stream_since` comes
from the same read as the exchanges and is informational only (AC18). Body: `Content(hash)`, first 5 MiB,
`X-Body-Truncated`, stored `content-type` (404 `body_not_stored` if hash empty/missing). Active =
`now - last_at < ActiveWindow` (const 5 min, D9). `since`/`Last-Event-ID` malformed is 400 (AC21).

**Live (AC22-AC30, AC54, AC55).** Three tasks: hub and live-only streams (T008), connect snapshot and `snapshot_error` (T009), then sent-record and convergence (T010).

*Hub (T008).* `Publish(Committed)` is `select { case <-done: return; case q <- c: default: overflow.Store(true) }`:
it never waits, and after `Close` it is a silent no-op. The queue is never closed, so no send can panic.
On overflow the dispatcher closes all subscribers, which reconnect and get a snapshot (AC50). The dispatcher
takes one commit, drains the rest already queued, and groups by session, so N commits for one session cost one
read. Per session: if a session stream watches it, build once (`snapshot{row ids, summary, entries by exchange,
order}`) and share it immutably; otherwise `SessionStats` only. `/api/stream` subscribers always get the summary.
Each subscriber has a `chan` of 64; a full channel drops that subscriber alone (AC29). 32 streams max, the
33rd gets 503 `too_many_streams` (AC30). Heartbeat comment every 15 s (injectable, AC26). `Hub.Close()`
closes `done` and cancels every stream context so `Shutdown` can finish (AC2).

*Snapshot (T009, AC22, AC24, AC29).* Order on open: validate `Last-Event-ID`/`?since` (400 if
malformed, otherwise ignored for narrowing), subscribe to the hub, then take the snapshot, write it directly
to the response (not through the 64-message channel, so a session of any size connects without dropping
itself), then drain the channel. Subscribing first means an exchange committed around the open is missed by
neither; one in both is replaced, never duplicated. Session stream snapshot: one `exchange` event per
stored exchange in timeline order (events carry no `id:`); `/api/stream` snapshot: first page of sessions
(AC15 order, default limit) as `session` events. Snapshot reads use the request context plus the 5 s timeout.
Snapshot failure (E22): write SSE `snapshot_error` with the JSON envelope (`timeout` or `store_error`, fixed
message), `retry: 30000`, and return; no `exchange` event is sent after a failure. A live message may be older
than the snapshot (built before it, delivered after); the commit that made the snapshot newer has its own
later message, so the stream converges, and replace-by-exchange makes the overlap harmless.
*Convergence (T010, AC25, AC50, AC54, AC55).* Each stream keeps a sent-record, per exchange a hash of the mapped entry list it last sent, seeded from its
snapshot. For a live message carrying committed exchanges (row-id order inside the batch): send `entries`
for each, then for every exchange that started after the earliest of them send an `exchange` event with its
whole list when its hash differs or there is no record; update the record. Only the last event of the batch
has `id:` (its highest row id, D14). The record is dropped when the stream closes or the session is
inactive (E21); an empty record resends all later-started exchanges. The page applies `entries` and
`exchange` identically (replace by exchange id, insert sorted); the Go helper `applyEvents` mirrors this.

**Capture hook (AC27, AC28).** `capture.Config.OnCommit func(Committed)`, `Committed{RequestID, PrincipalID,
Client, SessionKey}`. `process` calls it once, as its last step, after `SaveParse` has returned. `SaveParse`
is one transaction setting the parse status and inserting all events (`internal/store/exchange.go:60-93`), so
the row and its events are readable through the read-only connection, or `SaveParse` failed for good (no
retry) and the row is shown folded (D7). Never called between `SaveExchange` and `SaveParse`, nor when
`SaveExchange` failed. Blob-before-row already holds (`store.go:152-180`, `TestStore_BlobBeforeRow`); no change.
nil is a no-op; a panic is recovered and logged. `Store` interface unchanged.

**Security (AC8, AC31-AC35).** Middleware order: security headers (every response, including 421/403/500),
recover (logs path, 500 envelope if nothing written), Host check (421), Origin check (403; no CORS header
ever). Allowed set from the bound port and `ui.listen_addr` host (D8). Static files via an embedded `fs.FS`
server, GET only (other methods 405 with `Allow: GET`). The exchange body endpoint overrides the CSP with `default-src 'none'; sandbox` (AC33); every other response carries the standard CSP. The entry JSON field is `tool_call_id` (AC18). Tests scan `web/` for absolute URLs, inline script/style and `on*=` (AC34), and `web/*.js`
for `innerHTML`, `outerHTML`, `insertAdjacentHTML`, `document.write` (AC35; captured data uses `textContent`).

**Process wiring (AC1, AC2, AC5-AC7).** `run.go`: after `startCapture` and proxy bind, if `cfg.UI.Enabled && capt != nil`
open `store.OpenReader(dir)` and bind `cfg.UI.ListenAddr` via `d.listen`; failure closes the proxy listener and
capture and exits `exitRuntime` with a log line naming `ui.listen_addr` (E17). `ui.enabled` with capture off: one
warn line. `ui.enabled` false: nothing, no log. The hub is created before the sink so `OnCommit` can be given
to `capture.Config`. Shutdown: `hub.Close()`, `uiSrv.Shutdown(shutdownCtx)` with the proxy's, reader closes after
capture stops; the UI runs in `logging.Go`.

**Frontend (AC34-AC50).** `internal/ui/web/`: `index.html`, `style.css` (prefers-color-scheme), `app.js` (hash
router), `store.js` (one state object, subscribe/notify), `api.js` (fetch + EventSource wrapper), `entries.js`
(pure: insertSorted, replaceExchange, pairTools, groupTurns, filter, formatTime), custom elements with one
`render(data)` each (`session-list`, `session-view`, `tool-card`, `model-chip`, `exchange-detail`). Subscribes
only if the session is active. Reconnect (AC50) is just opening a new stream: its snapshot replaces every
exchange held, with no timeline refetch; while down, the page shows "live: disconnected, retrying" and keeps
history. On `snapshot_error` the page sets a snapshot-error state for the session, keeps history, and keeps
showing it through the native `error` event and the reconnect attempt; it clears the state on the first
`exchange`/`session` event of a later connection. Autoscroll only within a few px of the bottom (AC45).

**Docs (AC51-AC53).** ADR 0009 (supersedes 0007), ADR 0010; PLAN.md table, §6, §8, §11; `config.example.yaml`; README.

## AC coverage
| AC | Where | Verified by |
|---|---|---|
| AC1, AC2 | cmd/gateway/run.go, ui/hub.go | TestRun_UIServesOnOwnListener, TestRun_ShutdownClosesUIStreams, TestRun_CommitsDuringShutdownAfterHubClose, TestHub_PublishAfterCloseIsNoOp |
| AC3, AC4 | config/ui.go, load.go | TestLoad_UIPrecedenceAndUnknownKey, TestLoad_UIListenAddrMustBeLoopbackAndDistinct |
| AC5, AC6, AC7 | run.go | TestRun_UIDisabledOpensNothingLogsNothing, TestRun_UIWithCaptureOffWarnsOnce, TestRun_UIBindFailureExitsRuntime |
| AC8 | ui/middleware.go | TestUI_PanicRecoveredKeepsServing |
| AC9, AC10 | store/reader_ctx.go, ui/errors.go | TestReader_WriteThroughReaderFails, TestUI_StoreTimeoutIs504, TestReader_CancelledContextAbandonsQuery |
| AC11 | timeline/, ui/entries.go | TestUI_TimelineMatchesCLIViewer, TestSessionStats_MatchesBuildCountsAndUsage |
| AC12, AC13, AC14 | core/purity_test.go, ui/api.go | TestCore_NoProviderOrClientIdentifiers (adds ../ui), TestAPI_NonGETIs405WithAllow, TestUI_StaticNonGETIs405, TestAPI_Health |
| AC15, AC16, AC17 | ui/api_sessions.go, store/reader_ctx.go | TestAPI_SessionsPagedNewestFirst, TestAPI_UngroupedSummaryShape, TestAPI_SessionSummary, TestReader_SessionPageTiesBrokenByKey, TestReader_UngroupedPageTiesBrokenByRequestID |
| AC18 | ui/api_timeline.go | TestAPI_TimelineStableIdsAndOrder, TestAPI_TimelineCutsAt16KiB, TestAPI_ToolEntriesCarryToolCallID |
| AC19, AC20 | ui/api_exchange.go | TestAPI_ExchangeDetail, TestAPI_BodyTruncatesAt5MiB, TestAPI_BodyNotStored404 |
| AC21 | ui/api.go, stream.go | TestAPI_NotFoundAndBadRequestCodes, TestStream_LastEventIDAndSinceValidatedNotNarrowing |
| AC22 | ui/stream.go | TestStream_GlobalSnapshotThenSessionEvent, TestHub_UnwatchedSessionGetsStatsOnly |
| AC23 | ui/hub.go, stream.go | TestStream_SessionSendsEntriesWithRowID, TestHub_CoalescesQueuedCommitsPerSession |
| AC24 | ui/stream.go | TestStream_SnapshotEqualsFreshTimelineWithAndWithoutResumeInputs, TestStream_CommitOutOfRowIDOrderStillReachesPage, TestStream_CommitDuringSnapshotNotMissed |
| AC25 | web/entries.js, ui/stream_test | TestLive_OutOfOrderInsertsAtSortedPosition |
| AC26, AC27 | ui/stream.go, capture/pipeline.go | TestStream_HeartbeatEveryInterval, TestSink_OnCommitAfterStore, TestSink_OnCommitSeesEventsThroughReadOnlyReader, TestSink_OnCommitAfterSaveParseFails |
| AC28 | ui/hub.go, capture | TestHub_PublishNeverBlocks, TestHub_PublishReturnsWhenQueueFull, TestHub_PublishAfterCloseIsNoOp, TestSink_SlowOnCommitDoesNotStallWorker, TestSink_PanickingOnCommitIsRecovered |
| AC29, AC30 | ui/hub.go, stream.go | TestHub_FullBufferDropsOnlyThatSubscriber, TestStream_SnapshotLargerThan64DoesNotDropSubscriber, TestStream_33rdIs503 |
| AC31, AC32, AC33 | ui/middleware.go | TestUI_BadHostIs421, TestUI_AllLoopbackHostFormsAccepted, TestUI_LoopbackHostWrongPortIs421, TestUI_BadOriginIs403NoCORS, TestUI_SecurityHeadersOnEveryResponse, TestUI_BodyEndpointSendsStricterCSP, TestUI_NonBodyResponsesSendStandardCSP |
| AC34, AC35, AC36 | ui/web, ui/web.go | TestWeb_NoInlineOrExternalReferences, TestWeb_NoHTMLInjectionSinks, TestWeb_ReferencedAssetsAreEmbedded |
| AC37, AC38 | web/session-list.js, app.js | manual M1 (T012, T017) |
| AC39, AC40, AC41, AC42, AC43, AC44 | web/session-view.js, tool-card.js, model-chip.js | manual M2 |
| AC45, AC46, AC47, AC48, AC49 | web/session-view.js, api.js, exchange-detail.js, entries.js, style.css | manual M3 (AC45, AC46), M4 (AC47), M1 (AC48, AC49) |
| AC50 | web/api.js, ui/stream.go | TestLive_ReconnectSnapshotReplacesEverything; manual M3 |
| AC51, AC52, AC53 | docs/decisions, PLAN.md, config.example.yaml, README | manual review; TestDocs_ConfigExampleParses |
| AC54 | timeline/build.go, ui/stream.go | TestBuild_EarlierStartedExchangeChangesLaterEntries, TestStream_LaterStartedExchangeResentWhole, TestStream_ReceivedEntryUpdatedAfterLaterCommit, TestStream_RecordPerStreamNotPerSession |
| AC55 | ui/stream_test | TestLive_ConvergesWithReloadAndCLI |

## Test strategy
Vocabulary rule (CR-001): no identifier, file, test name or comment in `store`, `timeline`, `ui` or `web/` may
contain the substring "cursor" (the purity test bans it, AC12); the paging key is `after` / `next_after`.
Behaviour tests over a real temp store and `httptest` servers, under `-race`; the stream's snapshot source is a
small interface so tests inject a failing or slow store (E22). Named tests are in the AC table; tasks.md maps
E1-E22 to tests. Required: out-of-order `TestLive_OutOfOrderInsertsAtSortedPosition`; resume
`TestStream_SnapshotEqualsFreshTimelineWithAndWithoutResumeInputs` + `TestStream_CommitOutOfRowIDOrderStillReachesPage`;
stable ids `TestAPI_TimelineStableIdsAndOrder`; convergence `TestLive_ConvergesWithReloadAndCLI`; snapshot failure
`TestStream_SnapshotFailureSendsErrorAndLongRetry`. No JS tests (spec): Go scan tests plus manual checks M1-M4.

## Risks
- Rebuilding a session is O(session), and each connect snapshots a whole session. Bounded: list pages and unwatched sessions use `SessionStats`; commits are coalesced; 5 s timeout; `snapshot_error` retry of 30 s stops hot loops. T017 times the largest real session and records it in research.md.
- The hook sits on the capture path. Limit: non-blocking, panic-wrapped, tested with a stalled consumer.
- Convergence (AC54/55) is subtle. Limit: reload and CLI are the source of truth; any drift is healed by reconnect (full snapshot).
- An empty-store global stream sends no snapshot event, so its error state clears at the first live event. Limit: cosmetic.
- Frontend is not machine-checked beyond scans. Limit: pure logic in `entries.js`, mirrored by the Go model; manual checks.

## Open questions
- **Q1** — Thinking text: the stored canonical message keeps only block hashes and the CLI prints `[reasoning]`. If a reasoning block has no readable text, emit a `thinking` entry with the placeholder or omit it? (recommended: omit — nothing is invented when no thinking text is stored) → decided: omit (a `thinking` entry only when readable thinking text is stored; never the `[reasoning]` placeholder)

## Changelog
- v1 (2026-10-31) — first draft
- v1 (2026-10-31) — review fixes, still draft: entries not append-only, whole-exchange update unit; per-stream hash record; OnCommit after `SaveParse`; hub coalescing, stats-only for unwatched sessions, publish after `Close` a no-op; paging tie-breakers; Live split across T008/T009
- v1 (2026-10-31) — revised to spec v2 and D16, still draft: resume by row id replaced by a connect snapshot taken after subscribing and written outside the 64-message buffer; replace-by-exchange for `entries` and `exchange`; `snapshot_error` with `retry: 30000` (E22); reconnect is a new stream (AC50); row-id resume tests replaced; plan tightened
- v1 (2026-10-31) — T009 split, still draft: snapshot and `snapshot_error` (T009), sent-record and convergence (T010); later tasks renumbered T011–T017
- v2 (2026-10-31) — reopened: spec v3 changed its contract sections
- v2 (2026-10-31) — CR-001: paging parameter `cursor` renamed `after` (`next_after`) in Design; vocabulary rule added to Test strategy; T002 reworked as T018
- v3 (2026-10-31) — reopened: spec v4 changed its contract sections
- v4 (2026-10-31) — CR-003: tool_call_id field name, two-level CSP, Host forms and static GET-only tests added to AC coverage; no design change
