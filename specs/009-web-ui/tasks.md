---
feature: 009-web-ui
status: draft
version: 1
approved:
approved-by:
sha256:
fingerprint:
plan-fingerprint:
---
# Tasks — 009-web-ui

## Tasks

### T001 — Add the ui config block
- Do: `internal/config/ui.go` (UI struct, defaults true / 127.0.0.1:7199, settings, `ui` walk in load.go, `validateUI`: loopback host, differs from `listen_addr`); expose `Config.UI`.
- Tests: TestLoad_UIPrecedenceAndUnknownKey (AC3); TestLoad_UIListenAddrMustBeLoopbackAndDistinct (AC4, E16: 0.0.0.0, [::], 192.168.1.5, the proxy address all fail naming `ui.listen_addr`; 127.0.0.2 and localhost pass)
- AC: AC3, AC4
- Commit: feat(config): add ui block
- Depends: —
- Size: S
- Risk: low

### T002 — Add context-aware, paged store reads
- Do: `internal/store/reader_ctx.go`: `Reader.WithContext` view satisfying `timeline.Source`, page queries for sessions and ungrouped with sort-tuple cursors, row-id lookups, one-snapshot session read returning row ids; existing `Reader` methods delegate with `context.Background()`, signatures unchanged.
- Tests: TestReader_WriteThroughReaderFails (AC9); TestReader_CancelledContextAbandonsQuery (AC10); TestReader_SessionPageWalkNoDuplicates (E5); TestReader_SessionPageTiesBrokenByKey (AC15, E5: sessions sharing one `MAX(started_at)` split across pages, none repeated or skipped); TestReader_UngroupedPageTiesBrokenByRequestID (AC16, E5: exchanges sharing one `started_at`); TestReader_ExistingMethodsUnchanged (CLI reads same rows)
- AC: AC9, AC10, AC15, AC16
- Commit: feat(store): add context-aware paged reads
- Depends: —
- Size: M
- Risk: high

### T003 — Extend timeline for stable entries and cheap stats
- Do: `internal/timeline`: `Options.Cut`, `Entry.Index`, `Entry.Thinking`, `SessionStats` (counts + usage Summary without content reads); CLI output untouched.
- Tests: existing timeline and viewer tests unchanged; TestBuild_EntryIndexIsPositionWithinExchange (AC18); TestBuild_CutOptionLeavesCLIDefault (AC11); TestSessionStats_MatchesBuildCountsAndUsage (AC11, E15); TestBuild_ThinkingTextFromStoredBlock (AC18); TestBuild_EarlierStartedExchangeChangesLaterEntries (AC54: pins that entries are not append-only: storing an exchange that started earlier changes a later-started exchange's entries; if the timeline ever becomes append-only this test fails, and the re-send rule in T009 can be simplified)
- AC: AC11, AC18, AC54
- Commit: feat(timeline): add entry index, cut length and stats
- Depends: —
- Size: M
- Risk: high

### T004 — Notify after an exchange is committed
- Do: `capture.Config.OnCommit` and `Committed`; `process` calls it once, as its last step, after `SaveParse` has returned (committed, or failed for good), never between `SaveExchange` and `SaveParse`; only when `SaveExchange` succeeded; panic-wrapped; nil = no-op. Blob order needs no change: `Store.write` already puts blobs on disk before the row commits (`internal/store/store.go:152-180`, `internal/store/content.go:76-112`, tested by `TestStore_BlobBeforeRow` at `internal/store/content_test.go:206`).
- Tests: TestSink_OnCommitAfterStore (AC27: callback sees the row already readable); TestSink_OnCommitSeesEventsThroughReadOnlyReader (AC27: inside the callback, a `store.OpenReader` connection reads the exchange's parse status and all its events); TestSink_OnCommitAfterSaveParseFails (AC27, research D7: called once, after the failed `SaveParse`); TestSink_NoCallbackWhenSaveExchangeFails (research D7); TestSink_SlowOnCommitDoesNotStallWorker (AC28); TestSink_PanickingOnCommitIsRecovered (AC28)
- AC: AC27, AC28
- Commit: feat(capture): add exchange committed callback
- Depends: —
- Size: S
- Risk: high

### T005 — Build the UI server shell and security layer
- Do: `internal/ui`: `Handler`, chi router, security headers, recover, Host and Origin checks, GET-only guard for `/api`, JSON error envelope and store-error mapping (504/500), `/api/health`, embedded `web/` with a placeholder `index.html`; add `../ui` to `internal/core/purity_test.go`.
- Tests: TestUI_BadHostIs421 (AC31, E10); TestUI_BadOriginIs403NoCORS (AC32); TestUI_SecurityHeadersOnEveryResponse (AC33); TestUI_PanicRecoveredKeepsServing (AC8); TestAPI_NonGETIs405WithAllow (AC13); TestAPI_Health (AC14); TestUI_StoreTimeoutIs504 (AC10); TestCore_NoProviderOrClientIdentifiers covers ui (AC12)
- AC: AC8, AC10, AC12, AC13, AC14, AC31, AC32, AC33
- Commit: feat(ui): add server shell and security layer
- Depends: T002
- Size: M
- Risk: high

### T006 — Serve sessions and ungrouped lists
- Do: `ui/api_sessions.go`: `/api/sessions`, `/api/ungrouped`, `/api/sessions/{id}`; opaque id and cursor codecs with list-kind tag; summary built from `SessionStats`; active window constant.
- Tests: TestAPI_SessionsPagedNewestFirst (AC15); TestAPI_UngroupedSummaryShape (AC16); TestAPI_SessionSummary (AC17); TestAPI_EmptyStoreEmptyLists (E1); TestAPI_LimitValidation (E3, AC21); TestAPI_CursorFromOtherListIs400 (E4); TestAPI_PagingWhileSessionMovesNoDuplicates (E5)
- AC: AC15, AC16, AC17, AC21
- Commit: feat(ui): add sessions and ungrouped api
- Depends: T003, T005
- Size: M
- Risk: low

### T007 — Serve timeline, exchange detail and bodies
- Do: `ui/entries.go` (kind mapping, ids, 16 KiB truncation), `ui/api_timeline.go` (cursor, `stream_since`), `ui/api_exchange.go` (detail, body with 5 MiB cap).
- Tests: TestAPI_TimelineStableIdsAndOrder (AC18); TestAPI_TimelineCutsAt16KiB (AC18); TestUI_TimelineMatchesCLIViewer (AC11); TestAPI_TimelineFoldedExchangeFlagsMissingUsage (E15); TestAPI_ExchangeDetail (AC19); TestAPI_BodyTruncatesAt5MiB (AC20, E7); TestAPI_BodyNotStored404 (AC20, E8); TestAPI_NotFoundAndBadRequestCodes (AC21)
- AC: AC11, AC18, AC19, AC20, AC21
- Commit: feat(ui): add timeline and exchange api
- Depends: T003, T005
- Size: L
- Risk: high

### T008 — Add the hub and basic live streams
- Do: `ui/hub.go`: `Publish` is a non-blocking `select` on the queue and a `done` channel, and the queue is never closed, so a publish after `Close` is a silent no-op (no panic recovery involved); dispatcher drains every queued commit and coalesces by session, so N queued commits for one session give one read; a full timeline build only when at least one session stream watches that session, otherwise `SessionStats` only for `/api/stream`; bounded subscribers (64), 32 stream cap, `Close`. `ui/stream.go`: `/api/stream`, `/api/sessions/{id}/stream` live only (no resume), heartbeat.
- Tests: TestHub_PublishNeverBlocks (AC28); TestHub_PublishAfterCloseIsNoOp (AC2, AC28: neither panics nor blocks, under `-race`); TestHub_CoalescesQueuedCommitsPerSession (AC23, AC28: N commits queued for one session before the dispatcher runs → one session read and one batch carrying all N exchanges); TestHub_UnwatchedSessionGetsStatsOnly (AC22, AC28: commits for a session with no session stream do no timeline build and still produce the `/api/stream` session event); TestHub_FullBufferDropsOnlyThatSubscriber (AC29, E11); TestStream_33rdIs503 (AC30, E12); TestStream_GlobalSendsSessionEvent (AC22); TestStream_SessionSendsEntriesWithRowID (AC23); TestStream_HeartbeatEveryInterval (AC26)
- AC: AC2, AC22, AC23, AC26, AC28, AC29, AC30
- Commit: feat(ui): add live stream hub
- Depends: T004, T007
- Size: M
- Risk: high

### T009 — Add stream resume and entry-update convergence
- Do: `ui/stream.go`: per-stream sent-record (per exchange: a hash of its mapped entry list); after each committed exchange, re-send whole every later-started exchange whose hash differs or is unrecorded (`exchange` event); batch ids (only the last event carries the row id); resume from `Last-Event-ID` / `?since` (subscribe first, catch up from a fresh snapshot with an empty record, then drain live); record dropped on close or when the session goes inactive; test helper `applyEvents` mirroring the page rules. Open: research.md Q2 (the row-id resume cursor misses exchanges when capture runs more than one worker) — do not start this task until Q2 is answered.
- Tests: TestStream_ResumeLastEventIDAndSince (AC24); TestStream_ExchangeCommittedBeforeOpenSentOnce (E18); TestStream_BackfilledRowsBelowSinceNotStreamed (E19); TestStream_LaterStartedExchangeResentWhole (AC54); TestStream_ReceivedEntryUpdatedAfterLaterCommit (AC54: a stream that already received entry K of an exchange gets that exchange re-sent with the changed entry K after a commit of an earlier-started exchange changes it); TestStream_ResumeResendsExchangesChangedBeforeCursor (AC24, AC54: entries the client got before its cursor that a commit after the cursor changed are re-sent on resume); TestStream_ResendBatchAfterDropIsHarmless (E20); TestStream_RecordDroppedWhenInactive (E21); TestLive_OutOfOrderInsertsAtSortedPosition (AC25, E6); TestLive_ConvergesWithReloadAndCLI (AC55); TestStream_ReconnectWithStreamSinceMissesNothing (AC50, E13)
- AC: AC24, AC25, AC50, AC54, AC55
- Commit: feat(ui): add stream resume and entry updates
- Depends: T008
- Size: L
- Risk: high

### T010 — Wire the UI into gateway startup and shutdown
- Do: `cmd/gateway/run.go`: keep the store dir in `captureRun`, create the hub and pass `OnCommit`, open the reader, bind the UI listener via `d.listen`, serve with `logging.Go`, warn once when capture is off, graceful shutdown (hub.Close, then UI Shutdown, then capture stop).
- Tests: TestRun_UIServesOnOwnListener (AC1, E2); TestRun_ShutdownClosesUIStreams (AC2); TestRun_CommitsDuringShutdownAfterHubClose (AC2, AC28: capture workers still draining after `hub.Close` call `OnCommit` without panic or stall, and shutdown finishes in time); TestRun_UIDisabledOpensNothingLogsNothing (AC5); TestRun_UIWithCaptureOffWarnsOnce (AC6); TestRun_UIBindFailureExitsRuntime (AC7, E17: proxy not started); TestRun_ProxyUnaffectedByStuckUISubscriber (AC28)
- AC: AC1, AC2, AC5, AC6, AC7, AC28
- Commit: feat(gateway): serve the ui on its own listener
- Depends: T001, T009
- Size: M
- Risk: high

### T011 — Frontend shell, sessions list and ungrouped list
- Do: `internal/ui/web/`: `index.html`, `style.css` (light/dark), `app.js` hash router, `store.js`, `api.js`, `entries.js` helpers (time format, local time + relative hover), `session-list` and ungrouped components with Load more, live row updates from `/api/stream` (a session that moves to the top while the user pages is shown there by the live update, and Load more skips nothing it has not shown); Go scan tests for the embed.
- Tests: TestWeb_ReferencedAssetsAreEmbedded (AC36); TestWeb_NoInlineOrExternalReferences (AC34); TestWeb_NoHTMLInjectionSinks (AC35, E9)
- AC: AC34, AC35, AC36, AC37, AC38, AC48, AC49
- Commit: feat(ui): add web shell and session lists
- Depends: T006, T008
- Size: M
- Risk: low
- Manual: M1 — open the page against a real store: empty store shows an empty state; sessions table, active badge, Load more, ungrouped list, light/dark follow the OS, a prompt containing `<script>` shows literally.

### T012 — Frontend session timeline
- Do: `web/session-view.js` and `tool-card.js`, `model-chip.js`: header totals, turns, tool call and result cards, chips, truncated link, text filter and tool picker, copy buttons, sorted insertion and replace-by-exchange in `entries.js`.
- Tests: none — frontend has no JS test framework (spec); the sorted-insert and replace rules are proven by TestLive_OutOfOrderInsertsAtSortedPosition and TestLive_ConvergesWithReloadAndCLI in T009
- AC: AC39, AC40, AC41, AC42, AC43, AC44
- Commit: feat(ui): add session timeline view
- Depends: T007, T011
- Size: L
- Risk: low
- Manual: M2 — open a real Claude Code session: turns start at prompts, tool calls pair with results (pending shown while waiting, red on error), chips show unknown counters as unknown, filters and copy work, "truncated, open raw" links to the exchange.

### T013 — Frontend live updates and reconnect
- Do: `api.js` session stream with `?since=stream_since`, subscribe only for active sessions, autoscroll rule, "live: disconnected, retrying" with refetch and reopen.
- Tests: none — no JS test framework (spec); resume and reconnect semantics are proven server-side by TestStream_ReconnectWithStreamSinceMissesNothing (T009)
- AC: AC45, AC46, AC50
- Commit: feat(ui): add live timeline updates
- Depends: T009, T012
- Size: M
- Risk: low
- Manual: M3 — run a Claude Code turn while the page is open (entries appear, page follows only at bottom); restart the gateway (banner, then recovery without gaps); open an old session (no stream).

### T014 — Frontend exchange detail
- Do: `web/exchange-detail.js`: method, path, status, timings, usage, capture flags, headers, lazy request and response body loaders with "not stored" and truncation notice.
- Tests: none — no JS test framework (spec); the API behind it is proven by T007 tests
- AC: AC47
- Commit: feat(ui): add exchange detail view
- Depends: T012
- Size: S
- Risk: low
- Manual: M4 — open an exchange: bodies load only when opened, a missing body says "not stored".

### T015 — Docs: ADRs, PLAN, config example, README
- Do: ADR 0009 and 0010, mark 0007 superseded; PLAN.md phase table, §6, §8 row, §11 `ui` scope; `config.example.yaml` `ui` block; README UI section and status line.
- Tests: TestDocs_ConfigExampleParses (AC53: the example loads through `config.Load`); the rest is prose reviewed by the human
- AC: AC51, AC52, AC53
- Commit: docs(repo): record web ui and v1 roadmap
- Depends: T010
- Size: S
- Risk: low

### T016 — Manual check of the web UI against real captures
- Do: no code change; run the gateway with defaults, open http://127.0.0.1:7199, walk the checklist in Manual, and note the result in `specs/009-web-ui/research.md` Notes.
- Tests: none — human check only; `make verify` stays green
- AC: AC37, AC39, AC45, AC46, AC50, AC55
- Commit: docs(repo): record manual web ui check
- Depends: T015
- Size: S
- Risk: low
- Manual: (1) all previously captured sessions are listed with correct totals; (2) an old session's timeline matches `gateway timeline` for the same session; (3) a new Claude Code session through the gateway appears in the list and updates live, including a sub-agent task; (4) reloading mid-session keeps history and live updates resume; (5) devtools show no CSP violations and no external requests; (6) time one full rebuild of the largest captured session (`BuildWithUsage` through the UI reader, e.g. the timeline API for it with `limit` at the cap) and record the session's exchange count and the time in research.md Notes.

## Changelog
- v1 (2026-10-31) — generated from the approved plan
- v1 (2026-10-31) — review fixes, still draft: T003 no longer depends on T002 and is high risk (the kit allows only low or high); T008 split into T008 (hub, coalescing, stats-only for unwatched sessions, publish after stop) and T009 (resume, entry updates); later tasks renumbered T010–T016; new tests for tie-broken paging, OnCommit timing, changed-entry re-send and rebuild timing
