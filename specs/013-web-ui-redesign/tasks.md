---
feature: 013-web-ui-redesign
status: done
version: 4
approved: 2026-11-03T15:33:37Z
approved-by: human
sha256: 34fbc5b1e5552e34d7cfa90682bf27d983174141dfd5f31bb7fc81890e82799f
fingerprint: 29e02d7bc1bde2238d8cfb1d2ef590d574b2e906d3aa3b3ac8fe3efb87a41af0
plan-fingerprint: 863ba78ddc3d516f28d2f1e104b907ac2b22340edc47f5975d7bb7a267988ce4
previous: aa82289
---
# Tasks — 013-web-ui-redesign

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

### T001 — Add client rules to the Claude Code profile
- Do: declare `ui.ClientRules` (`IsSideCall`, `StripReminders`, `TitleOf`, `IsAgentTool`) and `Deps.Rules` (client name to rules; nil means none) in `internal/ui`; implement it in `internal/clients/claudecode/rules.go`; wire `claudecode.Profile{}` into both `ui.Deps` sites in `cmd/gateway/run.go` (listener and hub). No behaviour change to any endpoint yet.
- Tests: TestRules_QuotaProbeIsSideCall (E18), TestRules_TitleCallIsSideCall, TestRules_PlainPromptNotSideCall, TestRules_StripsReminders, TestRules_TitleOfResponse, TestRules_UnparseableTitleIgnored (E21), TestRules_AgentToolName, TestRules_QuotaWithOtherMessagesIsNotSideCall
- AC: AC37, AC40
- Commit: feat(claude-code): add side-call and reminder rules for the ui
- Depends: —
- Size: M
- Risk: low

### T002 — Add thread metadata to timeline call entries
- Do: in `internal/timeline/build.go`, give the call entry of each exchange `Thread`, `PrevID` (request id of the matched earlier call, empty for a new thread), `Messages` and `Carried`, from the existing `bestMatch`; keep every existing entry and test unchanged.
- Tests: TestBuild_CallCarriesThreadAndPrevious, TestBuild_NewThreadHasNoPrevious (E6), TestBuild_CarriedAndNewCounts, TestBuild_SubAgentCallBetweenMainCallsKeepsMainPrevious (E7)
- AC: AC22, AC23
- Commit: feat(ui): add thread and carried counts to timeline calls
- Depends: —
- Size: M
- Risk: low

### T003 — Add the session digest to every session summary
- Do: `timeline.SessionDigest` (title, tool-state counts, errors without side calls, side-call errors, has_calls/has_hooks); route `buildSummary` through it in `api_sessions.go`, `hub.go readLive` and `readSnapshot` so list, get, snapshot and live `session` events carry `title`, `tool_states`, `main_errors`, `side_errors`, `has_calls`, `has_hooks`; the existing `errors` field keeps meaning all errors (= main + side). `timeline.SessionStats` and CLI output stay untouched.
- Tests: TestSummary_TitleIsLatestGenerated, TestSummary_TitleFallsBackToFirstPrompt, TestSummary_FailedTitleIgnored (E21), TestSummary_TitleSkipsReminderOnlyPrompt (E3), TestSummary_NoPromptTitle (E2), TestSummary_OnlySideCalls (E19), TestSummary_NoRulesShowsPromptsWhole (E20), TestSummary_MainErrorsExcludeSideCalls (E23), TestSummary_ErrorsStaysAllErrors, TestSummary_SideCallsStillCountInTokens, TestSummary_ToolStateCounts, TestSummary_Sources, TestHub_SessionEventCarriesNewTitle
- AC: AC1, AC3, AC10, AC37, AC38, AC39
- Commit: feat(ui): add title, tool states and sources to summaries
- Depends: T001
- Size: L
- Risk: high

### T004 — Add call facts to timeline model calls
- Do: `mapEntries`/`getTimeline` add `call` to `model_call` entries (`n`, `thread`, `prev_id`, `side_call`, `main`, `carried`, `new`); main = thread has a user prompt, not a side call, not a sub-agent (agent id/type on its tool calls, or first prompt equal to a parent Agent call's prompt); same data in live `entries`/`exchange` stream payloads.
- Tests: TestTimeline_ModelCallCarriesCallFacts, TestTimeline_SideCallFlag, TestTimeline_CallNumbersCountEveryThread, TestTimeline_SubAgentThreadNotMain (E7), TestTimeline_CompactionThreadStaysMain, TestTimeline_NoRulesMeansNoSideCalls (E20), TestTimeline_ErroredSideCallFlaggedAndNotMain, TestStream_ExchangeEventCarriesCallFacts
- AC: AC7, AC11, AC12, AC40
- Commit: feat(ui): mark side calls and threads in timeline entries
- Depends: T001, T002
- Size: L
- Risk: high

### T005 — Extend the exchange API for the drawer
- Do: `GET /api/exchanges/{id}` adds `side_call`, `prev_id`, `thread_new`, `messages` (role, cut text, new), `carried`, `new_count`, `response` (text, stop reason, or an `unavailable` reason) and `response_tool_call_ids`; missing bodies or failed parses answer 200 with reasons, existing fields and routes unchanged.
- Tests: TestExchange_SideCallField, TestExchange_MessagesCarriedAndNew, TestExchange_NewThreadMarked (E6), TestExchange_ResponseAndStopReason, TestExchange_ResponseToolCallIDs, TestExchange_NoUsageNoNumbers (E4), TestExchange_BodyNotStoredStillAnswers
- AC: AC22, AC23, AC24, AC29, AC40
- Commit: feat(ui): add drawer facts to the exchange api
- Depends: T001, T002
- Size: M
- Risk: low

### T006 — Add today's totals endpoint
- Do: `store.Reader` method listing exchanges started since a time (read-only); `GET /api/today?since=` returns sessions with activity, calls and the four counters (`counterJSON` shape, side calls counted); bad `since` is 400.
- Tests: TestToday_CountsOnlyCallsSince, TestToday_CountsSideCalls, TestToday_EmptyStoreZeros (E1), TestToday_BadSinceRejected, TestToday_SessionSpanningMidnightCountsTodayCallsOnly
- AC: AC4, AC38
- Commit: feat(ui): add a today totals endpoint
- Depends: —
- Size: M
- Risk: low

### T007 — Rebuild the page shell with theme, breadcrumb, routes and key bar
- Do: split `index.html` to `style.css` + `app.js` only; new `theme.js`, `route.js`, `keys.js` (key-bar content and shortcut reducer, input guard), `shell.js` (breadcrumb, Light/Dark picker, live indicator, bottom key bar); style tokens for dark (default) and light; wrap `#/ungrouped` and `#/exchanges/{id}` in the shell with level SESSIONS. Update `web_test.go` guards (assets embedded, no cost or percentage text, actions are buttons or links).
- Tests: theme.test.js: pickStoresAndApplies, usesStoredThemeOverOs, storageUnavailableDefaultsDark (E15), unknownStoredValueDefaultsDark; route.test.js: drawerUrlRoundTrips, oldRoutesStillParse; shell.test.js: breadcrumbStartsAtSessions, breadcrumbShowsSessionTitle; keybar.test.js: levelsShowOnlyWorkingShortcuts, shortcutsIgnoredInInputAndEscLeaves, ungroupedAndExchangeShowSessionsLevel (E17); contrast.test.js: allTextPairsMeet4_5InBothThemes; TestWeb_NoCostOrContextPercent, TestWeb_ActionsAreButtonsOrLinks
- AC: AC2, AC30, AC31, AC32, AC33, AC34, AC35
- Commit: feat(ui): add the shell, theme picker and key bar
- Depends: —
- Size: L
- Risk: low

### T008 — Add the turn, chip and stats model
- Do: new `model.js` pure functions over timeline entries with call facts: `chipOf`, `buildTurns` (main-prompt turns, other-thread calls inside the open turn, unnumbered "before first prompt" and "side calls" cards, hooks-only sessions), tool rows with agent labels, per-turn counts and duration, session stats row (errors read from the summary's `main_errors`, never `errors`).
- Tests: chips.test.js: sixStatesMapToGlyphAndText, ranNotReportedStaysPendingInEndedSession (E14); turns.test.js: turnStartsAtMainPrompt, otherThreadCallJoinsOpenTurn, sideCallsNeverStartTurn, beforeFirstPromptCard (E8), onlySideCallsOneCardNoTurns (E19), hooksOnlySessionStartsTurnsAtPrompt, erroredSideCallStillRed, agentLabelAndErrorMarkKept; stats.test.js: statsReadMainErrorsNotErrors, sideCallsCountAndKeepLink, contextIsLastMainCall
- AC: AC7, AC8, AC10, AC13, AC17, AC18, AC37, AC38, AC39
- Commit: feat(ui): add the turn and chip model
- Depends: T004, T007
- Size: L
- Risk: low

### T009 — Add the context, strip and filter model
- Do: `model.js` `contextOf` (partial and dash rules), `contextSeries` (main-thread only), `deltaFor` (from the previous call that has usage), `stripBars`; new `filters.js` for list filters (All, Has errors on `main_errors > 0`, Has gaps, Hooks only, text) and timeline text/tool filters.
- Tests: context.test.js: seriesLeavesOutSideAndSubAgent, deltaMeasuredFromPreviousCallWithUsage (E4), callWithUnreportedCounterIsDashAndPartial (E5), firstCallOfThreadHasNoDelta (E6), noMainCallsContextNote (E22); strip.test.js: oneBarPerCallMarkedByTurn, sideCallBarsMuted, barsKeepMinWidth (E13); filters.test.js: hasErrorsIgnoresSideCalls, hasGapsMatchesUnmatchedAndPartial, filterTextMatchesLoadedRows, noMatchNoteWithLoadMore (E12), toolPickerFiltersLoadedTurns; perf.test.js: twoThousandCallsModelUnderBudget
- AC: AC6, AC11, AC12, AC14, AC22, AC10
- Commit: feat(ui): add the context, strip and filter model
- Depends: T008
- Size: M
- Risk: low

### T010 — Build the Sessions landing page
- Do: new `sessions-page.js` replacing `session-list.js`: table with start, title, sources, calls, tool chips, errors (`main_errors`), status, four counters ("—" when none), today's totals from `/api/today` (local midnight, refetched on `session` events), filter bar, "filtering N loaded" with Load more, ungrouped link, live row and title updates, row keyboard selection, key bar level SESSIONS. Port the old list tests.
- Tests: sessions-page.test.js: rowCellsShowDashForNoUsage, errorsColumnReadsMainErrors, titleUpdatesWhenSessionEventArrives, ungroupedLinkShownWhenExchangesExist, emptyStoreShowsNoteAndZeroTotals (E1), filterAndLoadMoreCopy, arrowsSelectEnterOpens; live.test.js: loadsEveryTimelinePage
- AC: AC1, AC3, AC4, AC5, AC6, AC19, AC31, AC36
- Commit: feat(ui): add the sessions landing page
- Depends: T003, T006, T007, T009
- Size: L
- Risk: low

### T011 — Build the turn cards and tool rows
- Do: new `timeline-page.js` replacing `session-view.js`, `model-chip.js` and reshaping `tool-card.js` into tool rows: session header with live indicator, stats row, one card per turn with prompt, tool rows (chip, hover detail, expandable pretty input and result, truncated link, copy buttons), assistant text, `#n · duration` links, "N hooks" toggle, reminder folding, text filter and tool picker, side-call cards; replace the old harness-badge guards in `web_test.go`.
- Tests: turns.test.js: turnHeadingCountsAndDuration, callLinksLabelled, hooksHiddenBehindToggle, truncatedLinkAndCopyOffered, remindersFoldedByDefault; timeline-page.test.js: textRenderedAsTextNotHtml, filterNarrowsLoadedTurns
- AC: AC8, AC13, AC14, AC15, AC17, AC18, AC37, AC39
- Commit: feat(ui): add turn cards and tool rows
- Depends: T008, T009, T007
- Size: L
- Risk: low

### T012 — Add the rail, strip, context line and Show options
- Do: turn rail with prompt, chips, calls and duration (select scrolls and marks current); duration strip with muted side calls, hover detail and horizontal scroll; context line from `contextSeries`; Show options (hooks inline, reminders, context per call, only turns with errors); TURNS-level keys (j/k, e, t, Enter, h, /, Backspace) and key-bar position.
- Tests: turns.test.js: railListsEveryTurn, railSelectionMarksCurrent, showOptionsApplyAtOnce; keybar.test.js: turnKeysMoveAndFindErrors; strip.test.js: activatingBarOpensDrawerRoute
- AC: AC9, AC11, AC12, AC16, AC31
- Commit: feat(ui): add the turn rail, strip and context line
- Depends: T011
- Size: M
- Risk: low

### T013 — Add the exchange drawer and its URL
- Do: `drawer.js` right-side drawer opened from `#n` links, strip bars and Enter on a turn; URL `#/sessions/{id}/call/{exchangeId}` (push on open, replace on step), reload restore, Back closes, Esc/close returns focus (turn heading fallback), ↑/↓ step with disabled ends, "no such call" note, CALL-level keys, live stability (holds call, focus, scroll; ↓ and position see new calls; pending chip updates), no auto-scroll while open, stream-drop keeps drawer. Content sections are stubbed with headings only.
- Tests: drawer.test.js: opensFromLinkBarAndEnter, arrowsStepAndEscReturnsFocus, firstAndLastStepDisabled (E9), unknownCallShowsNoSuchCallNote (E10), focusFallsToTurnHeading (E11), backClosesDrawer, streamDropKeepsDrawer (E16); live.test.js: drawerKeepsCallFocusAndScrollOnLiveData, followOnlyWhenDrawerClosedAndAtBottom, liveUpdatesAllViews
- AC: AC19, AC20, AC26, AC27, AC28, AC31
- Commit: feat(ui): add the exchange drawer shell and url
- Depends: T012, T005
- Size: L
- Risk: high

### T014 — Fill the drawer sections
- Do: tool chain (asked, ran, reported with glyph, text and agent label, from `response_tool_call_ids` joined to loaded entries), context size with input/cache-write/cache-read parts, change since previous call, trend with this call marked, "What the model saw" carried/new lists and counts, response with stop reason and output tokens, health facts and session id, raw request/response/headers links and full-page link; each section states its reason and shows "—" when data is missing.
- Tests: drawer.test.js: toolChainShowsThreeSteps, chainShowsRanWithReportedMissing (E14), contextPartsAndDelta, messagesMarkedCarriedOrNew, newThreadMarked, healthShowsDisconnectAbortError, rawLinksAndFullPageLink, missingSectionStatesReason, noUsageCallShowsDashesAndNoDelta (E4)
- AC: AC21, AC22, AC23, AC24, AC25, AC29
- Commit: feat(ui): fill the drawer sections
- Depends: T013
- Size: M
- Risk: low

### T015 — Update docs and add the manual check
- Do: add `docs/manual-checks/013-web-ui.md` (keyboard-only walk-through, both themes, contrast spot check, 2,000-call render under 2 s, drawer focus); update `docs/manual-checks/010-hooks-ui.md` and `docs/clients/claude-code-hooks.md` for the short chip text; delete the old stylesheet rules and files nothing imports any more.
- Tests: none new — docs and dead-code removal only; TestWeb_ReferencedAssetsAreEmbedded and make verify stay green
- AC: AC33, AC34
- Commit: docs(ui): add the 013 manual check and update chip text
- Depends: T014
- Size: S
- Risk: low
- Manual: docs/manual-checks/013-web-ui.md

### T016 — Memoise the session digest
- Do: read-only `store.CtxReader.SessionVersions(ids)` returning a version per requested (principal, client, session_key): one transaction, two grouped queries for the whole list page (exchanges: `COUNT(*)`, `MAX(rowid)`, `COUNT(parse)`; hook events: `COUNT(*)`, `MAX(id)`), written as `WITH l(p,c,k) AS (VALUES …)` joined to `exchanges e` / `hook_events h` on all three columns and grouped on the full identity triple (never `session_key` alone); keep both query strings as shared constants that the test also uses. If this driver's SQLite plans the CTE join differently so the plan test cannot pass, use whichever equivalent form does (for example `session_key IN (…)` plus a principal/client check), and if none passes stop with BLOCKED in research.md rather than loosening the test; absent halves read as zero so hooks-only and hook-less sessions still get a key; single-session reads (`getSession`, live `session` events, snapshot) call it with one id; `COUNT(parse)` is what moves when `SaveParse` updates the row in place; in `internal/ui` a mutex-guarded LRU `digestCache` (512 entries) keyed by (session id, version) holding only the digest; `buildSummary` merges the cached digest with the fresh row and `now` so `active` and other clock values are never cached; compute outside the lock; used by list, get, snapshot and live `session` events. No storage change.
- Tests: TestDigestCache_UnchangedSessionNotRecomputed, TestDigestCache_ChangedSessionRecomputed (new exchange, backfilled exchange, new hook line), TestDigestCache_ParseLandingRecomputes (SaveExchange, compute digest, SaveParse, digest recomputed with title and tool states), TestDigestCache_ActiveFlagNotMemoised, TestDigestCache_BoundedAndRaceFree (run under -race with concurrent list and live reads), TestSessionVersions_ChangesOnEveryWritePath, TestSessionVersions_BatchEqualsSingle, TestSessionVersions_SameKeyOtherPrincipalOrClientIsSeparate, TestSessionVersions_HooksOnlyAndNoHooksSessionsHaveKeys, TestSessionVersions_QueryPlansUseSessionIndex (EXPLAIN QUERY PLAN on both shared query strings via the project's driver; fails unless the target-table line is SEARCH using `exchanges_session` / `hook_events_session` with principal_id, client and session_key all equality-matched; rejects `exchanges_principal` and any SCAN of the target table, alias or `SCAN … USING INDEX` included; ignores `SCAN l` and constant-rows lines), BenchmarkSessionList_WarmCache (50-row page, every digest cached). After the code works, run the benchmark (`go test -run '^$' -bench SessionList_WarmCache ./internal/ui`) and add one line under `## Notes` in `specs/013-web-ui-redesign/research.md` with the measured ns/op, the date and the machine or conditions; Notes lines are append-only, never edit an existing one.
- AC: AC1, AC3
- Commit: feat(ui): memoise the session digest
- Depends: T003
- Size: M
- Risk: high

### T017 — Load events once per build
- Do: in `internal/ui` add one `buildSession` that loads a session's events once and feeds both `callFacts` (now taking the loaded events instead of reading its own) and `timeline.BuildWithUsage`; `getTimeline`, `getExchange` and `sessionExchanges` call it. No caching yet, behaviour and JSON unchanged.
- Tests: TestBuildSession_LoadsEventsOnce (counting source), TestCallFacts_UnchangedWithSharedEvents; the existing call-facts, timeline, exchange and stream tests stay green unchanged
- AC: AC19 (rework), AC28 (rework), AC36 (rework)
- Commit: refactor(ui): load session events once per timeline build
- Depends: T016
- Size: M
- Risk: high

### T018 — Cache timeline builds with singleflight and detached builds
- Do: `TimelineCache` (mutex-guarded LRU keyed by session id and `SessionVersions` key; at most 4 sessions or about 512 MB estimated retained size; immutable entries) in `Deps.Timelines`, created when nil by `newRouter` and `newHub`; hand-written per-session singleflight (mutex and channels, no new dependency) for timeline and digest builds, run on a server-lifetime context so a build finishes and is cached after its request times out; `getTimeline` pages slice the cached build with the existing Index/Seq/Row token unchanged; every timeline and summary response gains additive `version` and `event_position`. `storeTimeout` and storage unchanged. Measure the retained size of a 2,000-call build and append it to `specs/013-web-ui-redesign/research.md` Notes (over about 128 MB: log it and continue); also append the follow-up note that incremental append-only builds are out of scope for 013.
- Tests: TestTimelineCache_IdleSessionFivePagesOneBuild, TestTimelineCache_TimedOutBuildIsCachedAndRetryHits, TestTimelineCache_ConcurrentRequestsOneBuild, TestTimelineCache_LRUEvictsOldestSession, TestTimelineCache_SizeCapEvicts, TestDigestCache_TimedOutComputeIsCachedAndRetryHits, TestDigestCache_ConcurrentRequestsOneCompute, TestTimeline_ResponseCarriesVersionAndEventPosition; all under -race, counters not wall-clock
- AC: AC1 (rework), AC3 (rework), AC36
- Commit: feat(ui): cache session timeline builds
- Depends: T017
- Size: L
- Risk: high

### T019 — Serve live sessions from the cache and resume the stream
- Do: per session at most one build running and one queued; a live event or version change during a build sets a dirty flag and triggers exactly one follow-up build; readers get the latest cached build at once while a rebuild runs (wait only when none is cached); `sessionExchanges` and stream snapshots use the cache and carry `version` and `event_position`; the stream resumes from exactly that position (rowid and hook id).
- Tests: TestLiveBuild_BurstDuringBuildOneFollowUp, TestLiveBuild_RequestDuringRebuildGetsOlderBuildWithoutWaiting, TestLiveBuild_PagedPlusStreamEqualsFinalIdleBuild, TestStream_ResumesFromBuildPosition; existing stream resend-bound tests stay green; all under -race
- AC: AC19 (rework), AC28 (rework), AC36
- Commit: feat(ui): serve live sessions from the build cache
- Depends: T018
- Size: L
- Risk: high

### T020 — Pin cursor stability and retry a 504 once in the browser
- Do: no change to the paging token; add the cursor test. In `internal/ui/web`, `api.js getJSON` throws an error carrying the HTTP status; `showSession` retries the session summary or first timeline page once on a 504 after about 1 s (injectable delay), showing "building session…" meanwhile, then shows the error; later pages and other errors are not retried.
- Tests: TestTimelinePaging_CursorResolvesToSamePositionOnRebuiltTimeline (entries present in both builds); app.test.js: retriesSessionLoadOnceAfter504 (stubbed 504 then 200), secondFailureShowsTheError (504, 504), laterPage504IsNotRetried; the existing loadsEveryTimelinePage and error-display tests unchanged; TestWeb_NoCostOrContextPercent stays green
- AC: AC36 (rework), AC19 (rework)
- Commit: feat(ui): retry a session load once after a 504
- Depends: T018
- Size: M
- Risk: low

### T021 — Benchmark the build, log pprof and fix the 504 docs
- Do: `BenchmarkTimeline_2000Calls` (cold build and cached page); re-run the pprof of research.md "Where one page's time goes" and append before/after build time, allocation and `store.Events` share to research.md Notes (append-only); fix the 504 message in `docs/manual-checks/seed-calls.mjs` and the timing line in `docs/manual-checks/013-web-ui.md` to state the real cause.
- Tests: BenchmarkTimeline_2000Calls (measurement, not a behaviour test); make verify stays green
- AC: none — measurement and doc fixes; the behaviour is proven in T017 to T020
- Commit: docs(ui): log the timeline build numbers and fix the 504 notes
- Depends: T020
- Size: S
- Risk: low

## Changelog
- v1 (2026-11-02) — generated from the approved plan
- v2 (2026-11-02) — reopened: CR-001: Plan: tool call chips are computed on the server
- v3 (2026-11-03) — CR-002: T015 Tests line reworded (starts with "none new"); no other task changed
- v4 (2026-11-03) — reopened: CR-003: Build each session timeline once, cache it, and serve pages and live updates from the cache
- v4 (2026-11-03) — CR-003: added T017 to T021; T001 to T016 unchanged; the branch review over 80c6332..HEAD is the loop's own step, not a task
