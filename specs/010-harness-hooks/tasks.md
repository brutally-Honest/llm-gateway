---
feature: 010-harness-hooks
status: approved
version: 2
approved: 2026-11-02T04:23:59Z
approved-by: human
sha256: 212627c6dcf90ef71c59591e301c9d380f45e547de72033c08679082a2bbdc9d
fingerprint: 75d9899bd96c4980953c1a84fd32711306f409b13a0b149b24f5e47350efd712
plan-fingerprint: 8772085cd107373575362de576fd92b14cee5a29c8132c9fddf6fc3a60646e4b
previous: 92aae62
---
# Tasks — 010-harness-hooks

<!-- One block per task, in build order. Each task = one commit that leaves verify green.
     Tasks not started yet stay editable after approval (reorder, split, merge, retitle);
     done tasks are frozen. Progress lives in .agent-loop/ and git (see: loop.sh status).
     Size: S|M|L picks the model, Risk: high forces a review; Model:, Review: skip|always,
     Verify: targeted|full are optional overrides.

### T001 — <imperative title>
- Do: <what changes, which packages/files>
- Tests: <named behaviour tests: the happy path + the spec's edge cases it touches>   (or: none — <reason>)
- AC: AC1, AC2   (or: none — <reason>; in amendments: AC2 (rework), AC4 (remove))
- Commit: feat(scope): <subject>
- Depends: —   (or: T00n, earlier tasks only)
- Size: S   (S | M | L)
- Risk: low   (low | high)
- Manual: <only for a check a human must run by hand; makes the task NEEDS-HUMAN>
-->

## Tasks

### T001 — Spike the installed Claude Code hooks and record the facts
- Do: run against the installed Claude Code with a throwaway local listener (not committed): record in research.md the version; `tool_use_id` on PreToolUse/PostToolUse; `session_id` vs `x-claude-code-session-id` byte equality; HTTP vs command hook latency, behaviour when the endpoint is down, async support, and the hook type chosen; whether PermissionDenied carries `tool_use_id`; how often `input differs` would fire with no other hook rewriting input; the field names the mapper needs (tool_input, agent_id, agent_type, hook_event_name). If either join key fails, add an open question and stop BLOCKED.
- Tests: none — research spike; output is research.md Decisions/Notes
- AC: AC1
- Commit: docs(claude-code): record hook spike findings
- Depends: —
- Size: M
- Risk: low
- Manual: run a real Claude Code session with the throwaway listener and confirm the recorded facts

### T002 — Add the harness_event canonical kind and ADR 0012
- Do: `internal/core/harness.go` with `HarnessEvent`, `HarnessPhase` consts and the optional `HarnessMapper` interface; extend `core/purity_test.go` so no package outside `internal/clients/claudecode/` names a Claude Code hook event; write `docs/decisions/0012-harness-event-kind.md`.
- Tests: TestPurity_NoHarnessEventNamesOutsideProfile (constraint); TestHarnessPhase_ValuesNameNoHarness
- AC: none — foundation types and ADR; AC11 is proven in T003
- Commit: feat(observability): add harness_event canonical kind
- Depends: —
- Size: S
- Risk: low

### T003 — Map Claude Code hook events in the client profile
- Do: `internal/clients/claudecode/harness.go` implementing `core.HarnessMapper` for the 16 in-scope events, lenient parse, session_id verbatim, tool input, agent id/type, original name kept for unknown.
- Tests: TestMapHarness_EachInScopeEvent (AC11); TestMapHarness_UnknownEventKeepsName (AC12); TestMapHarness_NotJSONIsUnknown (AC12); TestMapHarness_EmptyAndNoEventName (E2); TestMapHarness_ConcurrentSubagentsKeepOwnIDs (E12); TestMapHarness_SessionIDVerbatim (AC14)
- AC: AC11, AC12, AC14
- Commit: feat(claude-code): map hook events to harness_event
- Depends: T001, T002
- Size: M
- Risk: low

### T004 — Store hook events (migration 3 and SaveHook)
- Do: append migration 3 (`hook_events`) in `internal/store/migrate.go`; `internal/store/hooks.go` with `SaveHook` over the existing `write()` (payload and tool input as content rows); define the record type the hooks sink will pass.
- Tests: TestSaveHook_PayloadByteForByte (AC10); TestSaveHook_StoresNoRequestHeaders (AC10); TestSaveHook_SessionIDVerbatim (AC14); TestSaveHook_IdenticalPayloadsBothStored (AC20); TestSaveHook_NoSessionStoredUngrouped (E4); TestMigrate_V2StoreUpgradesKeepsData
- AC: AC10, AC14, AC20
- Commit: feat(store): store harness hook events
- Depends: T001, T002
- Size: M
- Risk: high

### T005 — Read hook events and hooks-only sessions from the store
- Do: `HookEvents(principal, client, key)` ordered by `received_at, seq` on Reader and CtxReader; `SessionRows`/`SessionsByKey` include sessions known only from hook rows with `SessionRow.HooksOnly`; ungrouped hook count; principal-scoped.
- Tests: TestHookEvents_OrderedByReceiveThenSeq; TestHookEvents_PrincipalScoped; TestSessionRows_HooksOnlySessionListed (AC25); TestSessionRows_ExchangeSessionNotHooksOnly; TestUngroupedHookCount (E4)
- AC: AC25
- Commit: feat(store): read hook events and hooks-only sessions
- Depends: T004
- Size: M
- Risk: low

### T006 — Add the hooks config section
- Do: `internal/config/hooks.go`: `hooks.queue_size`, `hooks.workers`, `hooks.memory_limit` with defaults and env/file loading; docs the config docs test requires.
- Tests: TestLoad_HooksDefaults; TestLoad_HooksFromFileAndEnv; TestLoad_HooksRejectsNonPositive (AC6, E13)
- AC: AC6
- Commit: feat(config): add hooks queue and memory settings
- Depends: —
- Size: S
- Risk: low

### T007 — Build the hook sink
- Do: `internal/hooks/sink.go`: bounded queue, workers via `logging.Go`, `core.Budget`, `Submit` never blocks, mapper lookup by client name (none => client/phase `unknown`), `Store` interface, panic recovery, `OnStored` callback, `Counts`, `Close(ctx)`.
- Tests: TestSink_QueueFullDropsCountsAndLogsOnce (AC6); TestSink_MemoryLimitDrops (AC6); TestSink_UnknownClientStoredAsUnknown (AC13, E9); TestSink_NotJSONStoredAndCounted (AC12); TestSink_StoreFailureCounted; TestSink_MapperPanicDoesNotKillWorker; TestSink_CloseDrainsQueue; TestSink_OnStoredCalledAfterSave; TestSink_ConcurrentSubmitAndClose (race); TestPurity_HooksPackageImportsNoClientAndNamesNoHarnessEvent (internal/hooks imports no internal/clients/* and names no Claude Code event; constraint)
- AC: AC6, AC12, AC13
- Commit: feat(capture): add the hook ingest sink
- Depends: T002, T003, T004, T006
- Size: L
- Risk: high

### T008 — Add the ingest route and reserve /_gateway/
- Do: `internal/hooks/handler.go` (method, loopback Host, Origin/Sec-Fetch-Site guards, size cap, capture-off, never logs bodies/headers) and `internal/server/server.go` reserved-prefix 404 route.
- Tests: TestHandler_LoopbackPostAnswers200Empty (AC2); TestHandler_AnswersBeforeStorageFinishes (AC2); TestServer_ReservedPrefixUnknownIs404NotForwarded (AC2); TestHandler_NonLoopbackHost403 (AC4, E10); TestHandler_OriginOrSecFetchSite403 (AC4); TestHandler_NonPostIs405 (AC5); TestHandler_FullQueueStillAnswers200 (AC6, E13); TestHandler_OversizeBody200NotStoredCounted (AC7, E3); TestHandler_CaptureOffCounts (AC8)
- AC: AC2, AC4, AC5, AC6, AC7, AC8
- Commit: feat(server): add the loopback hook ingest route
- Depends: T007
- Size: M
- Risk: high

### T009 — Wire hook ingest into the gateway run and shutdown
- Do: `cmd/gateway/run.go`: build the hooks sink in `startCapture` (store asserted to `hooks.Store`), mount the handler always, close the sink with capture, log the hook counters beside `capture stopped`; add `!race` latency test file.
- Tests: TestRun_HooksIngestStoresEvent (AC2, AC10); TestRun_CaptureOffHooksAnswers200 (AC8); TestRun_ShutdownLogsHookCounters (AC9); TestRun_HookLatencyP99HealthyAndStalled (AC3, build tag !race); TestRun_HookBurstDoesNotDropLLMCapture (E13)
- AC: AC3, AC9
- Commit: feat(server): run hook ingest with the gateway
- Depends: T008
- Size: M
- Risk: high

### T016 — fix: Hook events get their sequence number when the store writes them (MAX+1), not when the ingest route receives them, so with hooks.workers > 1 two events received in the same millisecond can be ordered by write order instead of arrival order. The spec's Constraints call for a per-process monotonic sequence number assigned with the receive time. Assign it in the ingest handler from an atomic counter at receive time, carry it through the queue with the event, and store it as given. Regression test: with hooks.workers=4 and a store fake that finishes writes out of order, events posted in order keep their posting order in the timeline.
- Do: reproduce and fix: Hook events get their sequence number when the store writes them (MAX+1), not when the ingest route receives them, so with hooks.workers > 1 two events received in the same millisecond can be ordered by write order instead of arrival order. The spec's Constraints call for a per-process monotonic sequence number assigned with the receive time. Assign it in the ingest handler from an atomic counter at receive time, carry it through the queue with the event, and store it as given. Regression test: with hooks.workers=4 and a store fake that finishes writes out of order, events posted in order keep their posting order in the timeline.
- Tests: a regression test that fails before the fix and passes after it (Hook events get their sequence number when the store writes them (MAX+1), not when the ingest route receives them, so with hooks.workers > 1 two events received in the same millisecond can be ordered by write order instead of arrival order. The spec's Constraints call for a per-process monotonic sequence number assigned with the receive time. Assign it in the ingest handler from an atomic counter at receive time, carry it through the queue with the event, and store it as given. Regression test: with hooks.workers=4 and a store fake that finishes writes out of order, events posted in order keep their posting order in the timeline.)
- AC: none — bug fix: Hook events get their sequence number when the store writes them (MAX+1), not when the ingest route receives them, so with hooks.workers > 1 two events received in the same millisecond can be ordered by write order instead of arrival order. The spec's Constraints call for a per-process monotonic sequence number assigned with the receive time. Assign it in the ingest handler from an atomic counter at receive time, carry it through the queue with the event, and store it as given. Regression test: with hooks.workers=4 and a store fake that finishes writes out of order, events posted in order keep their posting order in the timeline.
- Commit: fix(harness-hooks): hook events get their sequence number when the store
- Depends: —
- Size: S
- Risk: low

### T010 — Compute tool-call match states in the timeline package
- Do: `internal/timeline/harness.go`: extend the source with `HookEvents`; dedup (AC20), exact joins, states and outcome, `no harness data`, `input differs` via canonical-JSON form, unmatched hook entries, agent labels, phase-rank ordering; add `Entry` fields and `KindHarness`.
- Tests: TestStates_AskedRanReported; TestStates_RanNotReported; TestStates_FailureOutcome; TestStates_DeniedWithID; TestStates_DeniedWithoutIDIsNotRun (E8); TestStates_NotRunByLaterResult; TestStates_Pending (E11); TestStates_NoHookEventsMeansNoHarnessData (AC17); TestStates_PartialHookDataKeepsRemainingStates (E1); TestStates_SessionJoinIsExactString (AC14); TestStates_ToolJoinIsExactID (AC15); TestStates_InputDiffersFlagged (AC18, E7); TestStates_InputKeyOrderIsNotADifference; TestStates_UnmatchedHookShown (AC19); TestStates_DuplicateShownOnce (AC20, E5); TestStates_DuplicatesFarApartWithoutIDShownTwice (AC20); TestStates_ArrivalOrderDoesNotChangeStates (AC21, E6); TestStates_SeededPermutationsGiveIdenticalStates (AC21: 100 shuffles with a fixed seed of the same hook rows and exchanges, identical states every time); TestStates_AgentLabelsOnEventAndCall (AC22)
- AC: AC14, AC15, AC16, AC17, AC18, AC19, AC20, AC21, AC22
- Commit: feat(observability): derive tool call match states from hook events
- Depends: T001, T005
- Size: L
- Risk: high

### T011 — Show states and hook events in the CLI
- Do: `internal/timeline/render.go`, `sessions.go`, `cmd/gateway/viewer.go`: state and `input differs` per tool call, agent labels, unmatched hook lines in time order, `--json` with states and hook events, `hooks only` in `gateway sessions`, ungrouped hook count.
- Tests: TestRender_ShowsStateAndInputDiffers (AC23); TestRender_UnmatchedHookLinesInTimeOrder (AC23); TestRender_ShowsAgentLabels (AC22); TestTimelineJSON_IncludesStatesAndHookEvents (AC23); TestSessions_HooksOnlyLabelled (AC25); TestSessions_UngroupedShowsHookCount (E4)
- AC: AC22, AC23, AC25
- Commit: feat(observability): show hook states in the timeline viewer
- Depends: T010
- Size: M
- Risk: low

### T017 — fix: Hook line ids in the web API are positional (hook:<n>), so a late-arriving earlier hook event shifts the ids of later lines and the UI's open/filter state lands on the wrong line. Use the hook event's stored row id instead (hook:<row id>); not the sequence number, which restarts with the gateway. Regression test: build a session's hook lines, insert an earlier hook event, rebuild, and assert every existing line keeps its id.
- Do: reproduce and fix: Hook line ids in the web API are positional (hook:<n>), so a late-arriving earlier hook event shifts the ids of later lines and the UI's open/filter state lands on the wrong line. Use the hook event's stored row id instead (hook:<row id>); not the sequence number, which restarts with the gateway. Regression test: build a session's hook lines, insert an earlier hook event, rebuild, and assert every existing line keeps its id.
- Tests: a regression test that fails before the fix and passes after it (Hook line ids in the web API are positional (hook:<n>), so a late-arriving earlier hook event shifts the ids of later lines and the UI's open/filter state lands on the wrong line. Use the hook event's stored row id instead (hook:<row id>); not the sequence number, which restarts with the gateway. Regression test: build a session's hook lines, insert an earlier hook event, rebuild, and assert every existing line keeps its id.)
- AC: none — bug fix: Hook line ids in the web API are positional (hook:<n>), so a late-arriving earlier hook event shifts the ids of later lines and the UI's open/filter state lands on the wrong line. Use the hook event's stored row id instead (hook:<row id>); not the sequence number, which restarts with the gateway. Regression test: build a session's hook lines, insert an earlier hook event, rebuild, and assert every existing line keeps its id.
- Commit: fix(harness-hooks): hook line ids in the web api are positional hook n
- Depends: —
- Size: S
- Risk: low

### T018 — fix: Timeline paging for hook lines builds its after-token from the line's Index (position), so a late-arriving earlier hook event shifts where the next page starts and can repeat or skip a hook line. Make it keyset paging on the timeline's own sort key: the token carries the last line's (receive time, seq, row id), row id as the final tie-break for a total order, and the next page starts strictly after that key in the same order the timeline sorts by. Do not page by row id alone: row ids follow insert order, not display order. Regression test: load page 1, insert a hook event received earlier than the page boundary, load page 2 with page 1's token, and assert no line that existed when page 1 was loaded is repeated or skipped.
- Do: reproduce and fix: Timeline paging for hook lines builds its after-token from the line's Index (position), so a late-arriving earlier hook event shifts where the next page starts and can repeat or skip a hook line. Make it keyset paging on the timeline's own sort key: the token carries the last line's (receive time, seq, row id), row id as the final tie-break for a total order, and the next page starts strictly after that key in the same order the timeline sorts by. Do not page by row id alone: row ids follow insert order, not display order. Regression test: load page 1, insert a hook event received earlier than the page boundary, load page 2 with page 1's token, and assert no line that existed when page 1 was loaded is repeated or skipped.
- Tests: a regression test that fails before the fix and passes after it (Timeline paging for hook lines builds its after-token from the line's Index (position), so a late-arriving earlier hook event shifts where the next page starts and can repeat or skip a hook line. Make it keyset paging on the timeline's own sort key: the token carries the last line's (receive time, seq, row id), row id as the final tie-break for a total order, and the next page starts strictly after that key in the same order the timeline sorts by. Do not page by row id alone: row ids follow insert order, not display order. Regression test: load page 1, insert a hook event received earlier than the page boundary, load page 2 with page 1's token, and assert no line that existed when page 1 was loaded is repeated or skipped.)
- AC: none — bug fix: Timeline paging for hook lines builds its after-token from the line's Index (position), so a late-arriving earlier hook event shifts where the next page starts and can repeat or skip a hook line. Make it keyset paging on the timeline's own sort key: the token carries the last line's (receive time, seq, row id), row id as the final tie-break for a total order, and the next page starts strictly after that key in the same order the timeline sorts by. Do not page by row id alone: row ids follow insert order, not display order. Regression test: load page 1, insert a hook event received earlier than the page boundary, load page 2 with page 1's token, and assert no line that existed when page 1 was loaded is repeated or skipped.
- Commit: fix(harness-hooks): timeline paging for hook lines builds its
- Depends: —
- Size: S
- Risk: low

### T019 — fix: sort hook lines by receive time, seq and row id everywhere. sortHooks (internal/timeline/harness.go:56) breaks ties on equal receive time and seq by phase rank and payload hash, while the paging token (T018) and the store read break them by row id, so after a gateway restart (seq restarts at 1) the CLI order and page order can differ. Define the hook-line order once in Go as a key (receive time, seq, row id) used by both sortHooks and the paging token, and keep the store's ORDER BY identical. Regression test: seeded random hook rows including ties on receive time and seq; assert the store read order, the sortHooks order and the concatenation of all pages are identical.
- Do: reproduce and fix: sort hook lines by receive time, seq and row id everywhere. sortHooks (internal/timeline/harness.go:56) breaks ties on equal receive time and seq by phase rank and payload hash, while the paging token (T018) and the store read break them by row id, so after a gateway restart (seq restarts at 1) the CLI order and page order can differ. Define the hook-line order once in Go as a key (receive time, seq, row id) used by both sortHooks and the paging token, and keep the store's ORDER BY identical. Regression test: seeded random hook rows including ties on receive time and seq; assert the store read order, the sortHooks order and the concatenation of all pages are identical.
- Tests: a regression test that fails before the fix and passes after it (sort hook lines by receive time, seq and row id everywhere. sortHooks (internal/timeline/harness.go:56) breaks ties on equal receive time and seq by phase rank and payload hash, while the paging token (T018) and the store read break them by row id, so after a gateway restart (seq restarts at 1) the CLI order and page order can differ. Define the hook-line order once in Go as a key (receive time, seq, row id) used by both sortHooks and the paging token, and keep the store's ORDER BY identical. Regression test: seeded random hook rows including ties on receive time and seq; assert the store read order, the sortHooks order and the concatenation of all pages are identical.)
- AC: none — bug fix: sort hook lines by receive time, seq and row id everywhere. sortHooks (internal/timeline/harness.go:56) breaks ties on equal receive time and seq by phase rank and payload hash, while the paging token (T018) and the store read break them by row id, so after a gateway restart (seq restarts at 1) the CLI order and page order can differ. Define the hook-line order once in Go as a key (receive time, seq, row id) used by both sortHooks and the paging token, and keep the store's ORDER BY identical. Regression test: seeded random hook rows including ties on receive time and seq; assert the store read order, the sortHooks order and the concatenation of all pages are identical.
- Commit: fix(harness-hooks): sort hook lines by receive time, seq and row id
- Depends: —
- Size: S
- Risk: low

### T026 — fix: add end-to-end live tests for two untested behaviours. (1) On a real hub and store with an open session stream: commit an exchange whose tool call has PreToolUse and PostToolUse hook events, then commit a later exchange carrying its tool_result; assert the stream's last state for that call is asked+ran+reported and equals a fresh snapshot (reload). It must fail when writeLive goes back to re-sending only exchanges after the committed one. (2) A hooks-only session that received a hook event within the last second is not marked inactive. Test-only change.
- Do: reproduce and fix: add end-to-end live tests for two untested behaviours. (1) On a real hub and store with an open session stream: commit an exchange whose tool call has PreToolUse and PostToolUse hook events, then commit a later exchange carrying its tool_result; assert the stream's last state for that call is asked+ran+reported and equals a fresh snapshot (reload). It must fail when writeLive goes back to re-sending only exchanges after the committed one. (2) A hooks-only session that received a hook event within the last second is not marked inactive. Test-only change.
- Tests: a regression test that fails before the fix and passes after it (add end-to-end live tests for two untested behaviours. (1) On a real hub and store with an open session stream: commit an exchange whose tool call has PreToolUse and PostToolUse hook events, then commit a later exchange carrying its tool_result; assert the stream's last state for that call is asked+ran+reported and equals a fresh snapshot (reload). It must fail when writeLive goes back to re-sending only exchanges after the committed one. (2) A hooks-only session that received a hook event within the last second is not marked inactive. Test-only change.)
- AC: none — bug fix: add end-to-end live tests for two untested behaviours. (1) On a real hub and store with an open session stream: commit an exchange whose tool call has PreToolUse and PostToolUse hook events, then commit a later exchange carrying its tool_result; assert the stream's last state for that call is asked+ran+reported and equals a fresh snapshot (reload). It must fail when writeLive goes back to re-sending only exchanges after the committed one. (2) A hooks-only session that received a hook event within the last second is not marked inactive. Test-only change.
- Commit: fix(harness-hooks): add end-to-end live tests for two untested
- Depends: —
- Size: S
- Risk: low

### T012 — Show states and hook events in the web UI
- Do: `internal/ui/entries.go`, `api_timeline.go`, `api_sessions.go`, `web/`: same fields, flags, agent labels and hook lines from the timeline package; `hooks only` label in the sessions list.
- Tests: TestAPITimeline_StatesFlagsAndHookLines (AC24); TestAPITimeline_AgentLabels (AC22); TestAPISessions_HooksOnlyLabel (AC25); TestWeb_ServesHarnessBadgeRendering (AC24)
- AC: AC22, AC24, AC25
- Commit: feat(ui): show hook states in the web timeline
- Depends: T010
- Size: M
- Risk: low
- Manual: open the web UI on a session with hook events and check badges, agent labels and the hooks-only label render

### T013 — Push hook events to open web UI streams
- Do: `internal/ui/hub.go` and `stream.go` per the plan's "Hub and streams" paragraph: `PublishHook`; hook commits coalesced per session to at most one re-read per 250 ms (trailing edge); a `Refresh` liveMsg that `writeLive` compares against the whole `sent` map (no after-first cut-off, no `id:`), including the `hooks` pseudo-exchange; a session's pending refresh timer is stopped when its last open stream closes; hook refreshes and exchange commits go through the same per-stream lock; refreshed summary for the global stream; `run.go` sets the hooks sink `OnStored` to it.
- Tests: TestHub_HookEventPushesSessionTimeline (AC26); TestHub_HookEventPushesSessionsList (AC26); TestHub_HookOnlySessionAppears (AC25, AC26); TestHub_HookPublishNeverBlocks; TestHub_HookRefreshesCoalescePerSession (AC26: a burst of events for one session inside 250 ms causes one re-read and the last event's state is sent); TestHub_HookRefreshSendsOnlyChangedExchanges (AC26); TestHub_HookRefreshCancelledWhenLastStreamCloses (AC26: a pending refresh timer for a session is stopped when its last open stream closes; nothing fires afterwards); TestHub_HookRefreshAndCommitInSameWindowSendEachChangeOnce (AC26: a hook refresh and an exchange commit landing together go through the same per-stream lock, and each changed exchange is sent once)
- AC: AC26
- Commit: feat(ui): push stored hook events to open streams
- Depends: T009, T012
- Size: M
- Risk: high

### T014 — Add gateway hooks print
- Do: `internal/clients/claudecode/snippet.go` builds the settings JSON from the profile's event list and the T001-chosen hook type; `cmd/gateway/hooks.go` writes it to stdout using `listen_addr`; dispatched from `run.go`.
- Tests: TestHooksPrint_SnippetCoversEveryEvent (AC27); TestHooksPrint_UsesListenAddr (AC27); TestHooksPrint_SnippetIsValidJSONNoDecision (AC27); TestHooksPrint_WritesNoFileExitsZero (AC27)
- AC: AC27
- Commit: feat(claude-code): add gateway hooks print
- Depends: T001, T003
- Size: M
- Risk: low

### T015 — Document installing the hooks
- Do: `docs/clients/claude-code-hooks.md` (and a pointer from the existing client doc): user-scope install, coexistence with project hooks such as the agent-loop kit's, removal, match-state meanings and limits.
- Tests: none — documentation only; make verify proves nothing else changed
- AC: AC28
- Commit: docs(claude-code): document installing the harness hooks
- Depends: T014
- Size: S
- Risk: low

### T020 — fix: take hook seq and receive time together under one lock
- Do: `internal/hooks/handler.go`: replace the `seq atomic.Int64` with one mutex over seq and the last time; the time is `max(clock(), last)`; injectable clock (default `time.Now`); pass seq and time through the queue. `internal/hooks/sink.go`: `Submit` stamps nothing and takes the receive time from the handler; update the T007/T008 tests that relied on sink-stamped time. `internal/store/hooks.go`: store `received_at` and `seq` as given (adjust only if it still derives either).
- Tests: TestHandler_BackwardsClockKeepsReceiveTimeNonDecreasing (clock steps back; stored times never decrease as seq increases); TestHandler_BlockedClockKeepsSeqAndTimeOrderAligned (clock blocks request 1 inside the call until request 2 has started and returns it a later time than request 2; seq order and time order agree); TestHandler_ConcurrentPostsOutOfOrderWritesKeepSeqAndTimeAligned (smoke test only, workers=4, writes finish out of order). Mutation checks: taking seq before and outside the lock must fail the blocked-clock test; removing max(now, last) must fail the backwards-clock test
- AC: none — constraint fix (seq and receive time, spec Constraints); no AC text changes
- Commit: fix(harness-hooks): take hook seq and receive time under one lock
- Depends: —
- Size: M
- Risk: high

### T021 — Add the Node test runner for the web client (ADR 0013)
- Do: `docs/decisions/0013-node-test-runner.md` (test-only runtime, like shellcheck in ADR 0011; no npm, no node_modules); `Makefile` `verify` runs `node --test "internal/ui/webtest/*.test.js"` (quoted glob; the directory form exits 1 on node v25); node absent fails verify, as shellcheck does; ADR 0013 states a minimum Node version (module detection of a plain `export` file without package.json depends on it); `make setup`/`scripts` report whether node is installed. Test files live in `internal/ui/webtest/`, not `internal/ui/web/` (that directory is `go:embed`ed and walked by `web_test.go`); one minimal passing `internal/ui/webtest/entries.test.js` imports the real `../web/entries.js` so the runner is proven end to end.
- Tests: `webtest/entries.test.js` `module loads and compareEntries orders by time` (runs the real file); make verify runs it
- AC: none — tooling and ADR; the AC behaviours are proven in T023
- Commit: chore(verify): run node tests for the web client
- Depends: —
- Size: M
- Risk: high

### T022 — fix: commit path re-sends every exchange whose hash changed
- Do: `internal/ui/stream.go` `writeLive`: on a commit compare every exchange in `All` with `sent` and re-send each whose hash differs, as the hook refresh does; remove the after-first-committed cut-off.
- Tests: TestStream_CommitResendsEarlierExchangeWhoseStateChanged (AC16, AC26: the reviewer's probe, a later exchange's tool_result turns an earlier call from ran-not-reported to asked+ran+reported on an open stream; the stream's final entries equal a reload); existing hub/stream tests unchanged
- AC: AC16 (rework), AC26 (rework)
- Commit: fix(ui): resend earlier exchanges changed by a commit
- Depends: —
- Size: M
- Risk: high

### T023 — fix: send the hook snapshot as a hooks event; exchange sorts before hook lines on ties
- Do: apply `specs/010-harness-hooks/reviewer-live-hook-test.patch` (committed in c013d51); adapt `TestStream_LiveHookUpdateReplacesFirstSnapshotHookLines` to the `hooks` event and the new replace function, or move its client half to the Node test; commit it with fix 2; delete the patch file in the same commit. Also: `ui/hub.go` `sessionExchanges` always includes the hooks item (`Entries` as `[]`, never `null`) and the "has anything" check counts real exchanges only; `ui/stream.go` writes it as `event: hooks` on the connect snapshot, `writeLive` and `writeRefresh` (not on the global stream); update the committed tests that expect `exchange` for hook lines (`hub_hook_test.go`, `stream_test.go`); `web/api.js` listens for `hooks`; `web/app.js` dispatches it to a new hook-group replace function in `web/entries.js` (drop every `hook_event` entry, insert the list, empty removes the group); `compareEntries` sorts the exchange before hook lines on equal time.
- Tests: TestStream_LiveHookUpdateReplacesFirstSnapshotHookLines (AC24, AC26, from the patch); TestStream_HooksSnapshotEmptyIsSentAndRemovesGroup (AC21, AC26: the connect snapshot of a session with no hook lines carries an empty `hooks` event; the only unmatched hook line becoming matched sends `[]`); `internal/ui/webtest/entries.test.js` `empty hooks snapshot removes the hook group (E6)` (AC21) and `exchange sorts before hook lines on an exact time tie` (AC24), both on the real `web/entries.js`; TestHub_HookEventPushesSessionTimeline and the other T013 tests updated for `hooks`
- AC: AC21 (rework), AC24 (rework), AC26 (rework)
- Commit: fix(ui): send the hook snapshot as its own stream event
- Depends: T021, T022
- Size: L
- Risk: high

### T024 — fix: viewer text for omitted bytes, unknown events and the pending badge
- Do: `internal/timeline/harness.go` (carry the omitted-byte count), `render.go` (show `… N bytes omitted`; `hook unknown: <name>` from the stored name), CLI `--json` carries the count; `internal/ui/entries.go` `entryJSON` carries it; `web/session-view.js` shows omitted bytes and the stored name; the pending-badge decision becomes a pure exported function in `web/entries.js` (hidden when a harness state exists) that `web/tool-card.js` calls (tool-card.js extends HTMLElement and cannot load in Node); update `TestWeb_ServesHarnessBadgeRendering` pins.
- Tests: TestRender_TruncatedHookInputShowsBytesOmitted (AC23); TestRender_UnknownHookShowsStoredName (AC23); TestTimelineJSON_CarriesOmittedCount (AC23); TestAPITimeline_TruncatedHookInputCarriesOmittedCount (AC24); `webtest/entries.test.js` `pending badge hidden when a harness state exists` (AC24, the pure function in entries.js); TestWeb_ServesHarnessBadgeRendering updated with a string pin that tool-card.js calls that function
- AC: AC23 (rework), AC24 (rework)
- Commit: fix(ui): show omitted bytes and unknown hook names
- Depends: T021, T023
- Size: M
- Risk: low

### T025 — Record the E4 gap in the hooks docs
- Do: `docs/clients/claude-code-hooks.md`: hook events with no session id appear only as a count in `gateway sessions`, not in the web ungrouped list (known v1 gap); describe the new viewer text (`N bytes omitted`, `hook unknown: <name>`). Note in the docs task record that T007's frozen block keeps the stale name TestPurity_HooksPackageImportsNoClientAndNamesNoHarnessEvent; the real test is `TestPurity_HooksPackageImportsNoClient` (`internal/hooks/purity_test.go`) and the no-Claude-Code-event-name check is TestPurity_NoHarnessEventNamesOutsideProfile (`internal/core/purity_test.go`, T002). No code changes.
- Tests: none — documentation only; make verify proves nothing else changed
- AC: AC25 (rework)
- Commit: docs(claude-code): note the hook events without session gap
- Depends: T024
- Size: S
- Risk: low

## Changelog
- v1 (2026-11-01) — generated from the approved plan
- v2 (2026-11-02) — reopened: spec v2 changed its contract sections
- v2 (2026-11-02) — CR-001: add T020 to T025 (seq and time under one lock, Node runner, commit-path resend, hooks snapshot event with the reviewer's test patch, viewer text, E4 docs)
