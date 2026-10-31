---
feature: 009-web-ui
status: approved
version: 3
approved: 2026-10-31T16:07:44Z
approved-by: human
sha256: 0afa1df4e0e7a95ca4334c339b189fc99ce8893a51f4bb6688d4cdc6ee48f3d3
fingerprint: 8c499f774f6d1241c59ad2dc2b8557b062d84dc5fe0ba57595472c3c1243ab6c
plan-fingerprint: 060da350aa26bcd11e9bba9846962e4a9c772a0bd2227d5cff95f8fcffa5f4c1
previous: 45916f6
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

### T018 — Rename the store paging key from cursor to after
- Do: `internal/store/reader_ctx.go` and `reader_ctx_test.go`: rename the exported `SessionCursor` / `UngroupedCursor` types, parameters and comments to `SessionAfter` / `UngroupedAfter` ("after key"), so nothing in `store` contains the substring "cursor" (purity test, CR-001). No behaviour change.
- Tests: existing T002 tests unchanged apart from the renamed identifiers (TestReader_SessionPageTiesBrokenByKey, TestReader_UngroupedPageTiesBrokenByRequestID, TestReader_SessionPageWalkNoDuplicates); TestCore_NoProviderOrClientIdentifiers passes over `../store` (AC12); `make verify` green
- AC: AC15 (rework), AC16 (rework)
- Commit: refactor(store): rename paging key to after
- Depends: T002
- Size: S
- Risk: low

### T003 — Extend timeline for stable entries and cheap stats
- Do: `internal/timeline`: `Options.Cut`, `Entry.Index`, `Entry.Thinking`, `SessionStats` (counts + usage Summary without content reads); CLI output untouched. Also fix the `internal/timeline/stats_test.go:60` fixture (CR-001 finding): model the case in canonical form, a stored reasoning block whose content has no readable thinking text (whatever canonical block type and content the store actually holds for it), with no provider block-type name anywhere in the test. The test must not construct any banned word by concatenation, encoding or similar; the purity test is not weakened.
- Tests: existing timeline and viewer tests unchanged; TestBuild_EntryIndexIsPositionWithinExchange (AC18); TestBuild_CutOptionLeavesCLIDefault (AC11); TestSessionStats_MatchesBuildCountsAndUsage (AC11, E15); TestBuild_ThinkingTextFromStoredBlock (AC18); TestBuild_EarlierStartedExchangeChangesLaterEntries (AC54: pins that entries are not append-only: storing an exchange that started earlier changes a later-started exchange's entries; if the timeline ever becomes append-only this test fails, and the re-send rule in T010 can be simplified)
- AC: AC11, AC18, AC54
- Commit: feat(timeline): add entry index, cut length and stats
- Depends: T018
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
- Do: `ui/api_sessions.go`: `/api/sessions`, `/api/ungrouped`, `/api/sessions/{id}`; opaque id and `after` codecs with list-kind tag (`?after=`, `next_after`; no identifier or test name contains "cursor", CR-001); summary built from `SessionStats`; active window constant.
- Tests: TestAPI_SessionsPagedNewestFirst (AC15); TestAPI_UngroupedSummaryShape (AC16); TestAPI_SessionSummary (AC17); TestAPI_EmptyStoreEmptyLists (E1); TestAPI_LimitValidation (E3, AC21); TestAPI_AfterFromOtherListIs400 (E4); TestAPI_PagingWhileSessionMovesNoDuplicates (E5)
- AC: AC15, AC16, AC17, AC21
- Commit: feat(ui): add sessions and ungrouped api
- Depends: T003, T005, T018
- Size: M
- Risk: low

### T007 — Serve timeline, exchange detail and bodies
- Do: `ui/entries.go` (kind mapping, ids, 16 KiB truncation), `ui/api_timeline.go` (`after`, `next_after`, `stream_since`), `ui/api_exchange.go` (detail, body with 5 MiB cap).
- Tests: TestAPI_TimelineStableIdsAndOrder (AC18); TestAPI_TimelineCutsAt16KiB (AC18); TestUI_TimelineMatchesCLIViewer (AC11); TestAPI_TimelineFoldedExchangeFlagsMissingUsage (E15); TestAPI_ExchangeDetail (AC19); TestAPI_BodyTruncatesAt5MiB (AC20, E7); TestAPI_BodyNotStored404 (AC20, E8); TestAPI_NotFoundAndBadRequestCodes (AC21); TestAPI_TimelineOmitsUnreadableReasoning (Q1 in plan.md, AC18, AC11: for a session whose stored reasoning block has no readable thinking text, the timeline API returns no thinking entry for it, no entry carries [reasoning] as thinking text, and the assistant message entry still matches the CLI viewer (text [reasoning]); the fixture is a stored reasoning block with no provider block-type name in any form, as T003's fixture in internal/timeline/stats_test.go)
- AC: AC11, AC18, AC19, AC20, AC21
- Commit: feat(ui): add timeline and exchange api
- Depends: T003, T005
- Size: L
- Risk: high

### T008 — Add the hub and basic live streams
- Do: `ui/hub.go`: `Publish` is a non-blocking `select` on the queue and a `done` channel, and the queue is never closed, so a publish after `Close` is a silent no-op (no panic recovery involved); dispatcher drains every queued commit and coalesces by session, so N queued commits for one session give one read; a full timeline build only when at least one session stream watches that session, otherwise `SessionStats` only for `/api/stream`; bounded subscribers (64), 32 stream cap, `Close`. `ui/stream.go`: `/api/stream`, `/api/sessions/{id}/stream` live events only (the connect snapshot is T009, entry updates T010), heartbeat.
- Tests: TestHub_PublishNeverBlocks (AC28); TestHub_PublishAfterCloseIsNoOp (AC2, AC28: neither panics nor blocks, under `-race`); TestHub_CoalescesQueuedCommitsPerSession (AC23, AC28: N commits queued for one session before the dispatcher runs → one session read and one batch carrying all N exchanges); TestHub_UnwatchedSessionGetsStatsOnly (AC22, AC28: commits for a session with no session stream do no timeline build and still produce the `/api/stream` session event); TestHub_FullBufferDropsOnlyThatSubscriber (AC29, E11); TestStream_33rdIs503 (AC30, E12); TestStream_GlobalSendsLiveSessionEvent (AC22); TestStream_SessionSendsEntriesWithRowID (AC23); TestStream_HeartbeatEveryInterval (AC26)
- AC: AC2, AC22, AC23, AC26, AC28, AC29, AC30
- Commit: feat(ui): add live stream hub
- Depends: T004, T007
- Size: M
- Risk: high

### T009 — Add the connect snapshot and snapshot_error
- Do: `ui/stream.go`: on every open validate `Last-Event-ID`/`?since` (400 if malformed, otherwise ignored), subscribe to the hub first, then take the snapshot and write it directly to the response, not through the 64-message buffer (session stream: one `exchange` event per stored exchange in timeline order, no `id:`; `/api/stream`: first page of sessions as `session` events), then drain live events as T008 sends them; snapshot reads go through a small source interface with the 5 s timeout so tests can inject failures; on snapshot failure send `snapshot_error` (JSON envelope, code `timeout` or `store_error`, fixed message), `retry: 30000`, close the stream, and send no `exchange` event.
- Tests: TestStream_SnapshotEqualsFreshTimelineWithAndWithoutResumeInputs (AC24: no header, `Last-Event-ID` and `?since` each first send one `exchange` event per exchange, equal to a fresh timeline call); TestStream_LastEventIDAndSinceValidatedNotNarrowing (AC21, AC24: malformed is 400, a valid value does not narrow the snapshot); TestStream_GlobalSnapshotThenSessionEvent (AC22: first page of sessions, then a live `session` event on commit); TestStream_CommitOutOfRowIDOrderStillReachesPage (AC24, AC55: an exchange with a lower row id committed after a higher one is still delivered); TestStream_CommitDuringSnapshotNotMissed (AC24: a commit between subscribe and snapshot read arrives at least once, never lost); TestStream_BackfilledExchangesAppearOnReconnectSnapshot (E19: nothing is sent at backfill, present in the next snapshot); TestStream_SnapshotLargerThan64DoesNotDropSubscriber (AC29); TestStream_SnapshotFailureSendsErrorAndLongRetry (E22, AC10: store error, then store timeout; `snapshot_error` code `store_error` / `timeout`, `retry: 30000`, closed stream, no `exchange` event, message holds no captured data)
- AC: AC10, AC21, AC22, AC24, AC29, AC55
- Commit: feat(ui): add stream connect snapshot
- Depends: T008
- Size: M
- Risk: high

### T010 — Add per-stream sent-record and entry-update convergence
- Do: `ui/stream.go`: per-stream sent-record (per exchange a hash of its mapped entry list, seeded from the snapshot); after each committed exchange send `entries`, then `exchange` for every later-started exchange whose hash differs or is unrecorded; batch ids (only the last event of a batch carries its highest row id, D14); record dropped on close or when the session is inactive. Test helper `applyEvents` mirroring the page rules (replace by exchange id for `entries` and `exchange` alike, insert sorted, never keep an entry because its id is known).
- Tests: TestStream_LaterStartedExchangeResentWhole (AC54); TestStream_ReceivedEntryUpdatedAfterLaterCommit (AC54: an exchange already sent is re-sent with the changed entry after an earlier-started exchange is committed); TestStream_RecordPerStreamNotPerSession (AC54, E21: a stream opened later still gets what an earlier one already received); TestStream_RecordDroppedWhenInactive (E21: nothing recorded resends every later-started exchange); TestStream_ResendBatchAfterDropIsHarmless (E20: a reconnect snapshot after a half-sent batch leaves nothing stale); TestStream_ExchangeCommittedBeforeOpenSentOnce (E18: applyEvents shows it once); TestLive_OutOfOrderInsertsAtSortedPosition (AC25, E6); TestLive_ConvergesWithReloadAndCLI (AC55: live view built from events equals a fresh timeline call equals the CLI output); TestLive_ReconnectSnapshotReplacesEverything (AC50, E13: after a drop and a new stream the view equals a reload)
- AC: AC25, AC50, AC54, AC55
- Commit: feat(ui): add live entry updates
- Depends: T009
- Size: L
- Risk: high

### T011 — Wire the UI into gateway startup and shutdown
- Do: `cmd/gateway/run.go`: keep the store dir in `captureRun`, create the hub and pass `OnCommit`, open the reader, bind the UI listener via `d.listen`, serve with `logging.Go`, warn once when capture is off, graceful shutdown (hub.Close, then UI Shutdown, then capture stop).
- Tests: TestRun_UIServesOnOwnListener (AC1, E2); TestRun_ShutdownClosesUIStreams (AC2); TestRun_CommitsDuringShutdownAfterHubClose (AC2, AC28: capture workers still draining after `hub.Close` call `OnCommit` without panic or stall, and shutdown finishes in time); TestRun_UIDisabledOpensNothingLogsNothing (AC5); TestRun_UIWithCaptureOffWarnsOnce (AC6); TestRun_UIBindFailureExitsRuntime (AC7, E17: proxy not started); TestRun_ProxyUnaffectedByStuckUISubscriber (AC28)
- AC: AC1, AC2, AC5, AC6, AC7, AC28
- Commit: feat(gateway): serve the ui on its own listener
- Depends: T001, T010
- Size: M
- Risk: high

### T012 — Frontend shell, sessions list and ungrouped list
- Do: `internal/ui/web/`: `index.html`, `style.css` (light/dark), `app.js` hash router, `store.js`, `api.js`, `entries.js` helpers (time format, local time + relative hover), `session-list` and ungrouped components with Load more (sends `?after=`, reads `next_after`), live row updates from `/api/stream` (a session that moves to the top while the user pages is shown there by the live update, and Load more skips nothing it has not shown); Go scan tests for the embed.
- Tests: TestWeb_ReferencedAssetsAreEmbedded (AC36); TestWeb_NoInlineOrExternalReferences (AC34); TestWeb_NoHTMLInjectionSinks (AC35, E9)
- AC: AC34, AC35, AC36, AC37, AC38, AC48, AC49
- Commit: feat(ui): add web shell and session lists
- Depends: T006, T008
- Size: M
- Risk: low
- Manual: M1 — open the page against a real store: empty store shows an empty state; sessions table, active badge, Load more, ungrouped list, light/dark follow the OS, a prompt containing `<script>` shows literally.

### T013 — Frontend session timeline
- Do: `web/session-view.js` and `tool-card.js`, `model-chip.js`: header totals, turns, tool call and result cards, chips, truncated link, text filter and tool picker, copy buttons, sorted insertion and replace-by-exchange (for `entries` and `exchange` events alike) in `entries.js`.
- Tests: none — frontend has no JS test framework (spec); the sorted-insert and replace rules are proven by TestLive_OutOfOrderInsertsAtSortedPosition and TestLive_ConvergesWithReloadAndCLI in T010
- AC: AC39, AC40, AC41, AC42, AC43, AC44
- Commit: feat(ui): add session timeline view
- Depends: T007, T012
- Size: L
- Risk: low
- Manual: M2 — open a real Claude Code session: turns start at prompts, tool calls pair with results (pending shown while waiting, red on error), chips show unknown counters as unknown, filters and copy work, "truncated, open raw" links to the exchange.

### T014 — Frontend live updates and reconnect
- Do: `api.js` session stream (no `?since`, no timeline refetch: a reconnect is a new stream whose snapshot replaces every exchange held), subscribe only for active sessions, autoscroll rule, "live: disconnected, retrying" banner, snapshot-error state set by `snapshot_error`, kept through the native `error` event and the reconnect attempt, cleared on the first snapshot event of a later connection.
- Tests: none — no JS test framework (spec); snapshot and reconnect semantics are proven server-side by TestLive_ReconnectSnapshotReplacesEverything (T010) and TestStream_SnapshotFailureSendsErrorAndLongRetry (T009)
- AC: AC45, AC46, AC50
- Commit: feat(ui): add live timeline updates
- Depends: T010, T013
- Size: M
- Risk: low
- Manual: M3 — run a Claude Code turn while the page is open (entries appear, page follows only at bottom); restart the gateway (banner, then recovery; the timeline then equals a reload); open an old session (no stream).

### T015 — Frontend exchange detail
- Do: `web/exchange-detail.js`: method, path, status, timings, usage, capture flags, headers, lazy request and response body loaders with "not stored" and truncation notice.
- Tests: none — no JS test framework (spec); the API behind it is proven by T007 tests
- AC: AC47
- Commit: feat(ui): add exchange detail view
- Depends: T013
- Size: S
- Risk: low
- Manual: M4 — open an exchange: bodies load only when opened, a missing body says "not stored".

### T016 — Docs: ADRs, PLAN, config example, README
- Do: ADR 0009 and 0010, mark 0007 superseded; PLAN.md phase table, §6, §8 row, §11 `ui` scope; `config.example.yaml` `ui` block; README UI section and status line.
- Tests: TestDocs_ConfigExampleParses (AC53: the example loads through `config.Load`); the rest is prose reviewed by the human
- AC: AC51, AC52, AC53
- Commit: docs(repo): record web ui and v1 roadmap
- Depends: T011
- Size: S
- Risk: low

### T017 — Manual check of the web UI against real captures
- Do: no code change; run the gateway with defaults, open http://127.0.0.1:7199, walk the checklist in Manual, and note the result in `specs/009-web-ui/research.md` Notes.
- Tests: none — human check only; `make verify` stays green
- AC: AC37, AC39, AC45, AC46, AC50, AC55
- Commit: docs(repo): record manual web ui check
- Depends: T016
- Size: S
- Risk: low
- Manual: (1) all previously captured sessions are listed with correct totals; (2) an old session's timeline matches `gateway timeline` for the same session; (3) a new Claude Code session through the gateway appears in the list and updates live, including a sub-agent task; (4) reloading mid-session keeps history and live updates resume, and restarting the gateway shows the banner and afterwards the view equals a reload; (5) devtools show no CSP violations and no external requests; (6) time one full rebuild of the largest captured session (`BuildWithUsage` through the UI reader, e.g. the timeline API for it with `limit` at the cap) and record the session's exchange count and the time in research.md Notes.

## Changelog
- v1 (2026-10-31) — generated from the approved plan
- v1 (2026-10-31) — review fixes, still draft: T003 no longer depends on T002 and is high risk (the kit allows only low or high); T008 split into T008 (hub, coalescing, stats-only for unwatched sessions, publish after stop) and T009 (resume, entry updates); later tasks renumbered T010–T016; new tests for tie-broken paging, OnCommit timing, changed-entry re-send and rebuild timing
- v1 (2026-10-31) — revised to spec v2 and D16, still draft: T009 builds the connect snapshot (taken after subscribing, written outside the live buffer), `snapshot_error` with `retry: 30000`, the per-stream record and replace-by-exchange; row-id resume tests replaced by snapshot tests; the Q2 gate is gone; T013 reconnect is a new stream with no refetch and keeps the snapshot-error state
- v1 (2026-10-31) — T009 split, still draft: T009 is the connect snapshot and `snapshot_error`, T010 the per-stream record, entry-update convergence and `applyEvents` (depends on T009); later tasks renumbered T011–T017
- v2 (2026-10-31) — reopened: spec v3 changed its contract sections
- v2 (2026-10-31) — CR-001: new T018 reworks done T002 (cursor to after); T003 gains the fixture fix; T006, T007, T012 wording and test name use `after` / `next_after`; T006 depends on T018
- v3 (2026-10-31) — reopened: CR-002: T007 gains an API test for unreadable reasoning
- v3 (2026-10-31) — CR-002: T007 Tests line gains TestAPI_TimelineOmitsUnreadableReasoning
