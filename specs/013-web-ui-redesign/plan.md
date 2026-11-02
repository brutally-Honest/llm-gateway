---
feature: 013-web-ui-redesign
status: approved
version: 2
approved: 2026-11-02T15:06:41Z
approved-by: human
sha256: 2ebf5646a2c743edcb72008b2982d6fad6a59a4909aae50143b3ee29321e2f04
fingerprint: a156ca1161755466280cc470fd9e1b5ab3bc62d0c4414131f90eddb81fb638ec
spec-fingerprint: 5c8ccf9abea20e2cea6fb75e71dc6bb5cb3fa777528b3ea6b27288ae8b3b9aeb
previous: db06cef
---
# Plan — 013-web-ui-redesign

## Summary
The server learns four new facts and exposes them additively on the existing read-only API: which calls are side calls, each call's thread, previous call and carried/new message counts, a session digest (title, tool-state counts, errors without side calls, sources), and today's totals. The web client is then rebuilt in vanilla ES modules around a pure, Node-tested model layer (turns, chips, context series, deltas, filters) with thin DOM components on top: Sessions list, turn timeline with rail/stats/strip/context line, and a right-side exchange drawer. No dependency, no storage change, `internal/core/` untouched.

## Context
- API: `internal/ui/api_sessions.go` (`buildSummary` is the single choke point for list, get, snapshot and live `session` events), `api_timeline.go`, `api_exchange.go`, `entries.go` (`mapEntries`), `hub.go`/`stream.go` (live), `ui.go` (`Deps`, router, embedded `web/`).
- Thread logic already exists: `internal/timeline/build.go` (`bestMatch`, `lcp`, `thread`), `sessions.go` (`SessionStats`, `preview`, `firstUserText`), `usage.go` (`CallUsage`), `harness.go` (tool states). The client profile is `internal/clients/claudecode/profile.go`, wired in `cmd/gateway/run.go` (`ui.Deps` at the UI listener and `ui.NewHub`).
- Web: `internal/ui/web/*.js|css|html`, tests `internal/ui/webtest/*.test.js` (ADR 0013), Go guards in `internal/ui/web_test.go` (no inline script/style, no HTML sinks, referenced assets embedded). Pure logic lives in `entries.js`-style modules and is imported by Node tests; components stay thin (pattern from 010).
- Manual checks: `docs/manual-checks/010-hooks-ui.md`, `docs/clients/claude-code-hooks.md`.

## Approach
Client-specific knowledge (side calls, reminders, generated title, the "Agent" tool name) is one optional interface, `ui.ClientRules`, declared in `internal/ui` and implemented by `claudecode.Profile`; `Deps.Rules` maps client name to rules (nil means prompts whole, no side calls). `timeline` stays client-agnostic: it only adds thread metadata (thread id, previous call, carried/new counts) to call entries. The `ui` package combines both into API fields; the browser never reads message text to classify. The browser derives everything else (turns, chips, stats, strip, context series, deltas, filters, key bar) in pure modules from the API fields.

## Alternatives considered
### A1 — Server annotates calls, browser builds views from pure modules (chosen)
- Pros: classification once, on the server, reusable by the CLI later (D13); view logic is Node-testable; API changes are additive.
- Cons: touches timeline, ui, claudecode and web in one feature; digest adds per-session work to list pages.

### A2 — Browser derives threads, deltas and titles from the raw entries
- Pros: no server change beyond side-call flag; one language.
- Cons: re-implements `bestMatch` and breaks the "browser never classifies" rule (AC40); a title needs response bodies the entries do not carry.

### A3 — New dedicated `/api/sessions/{id}/view` endpoint returning a prebuilt view model
- Pros: browser becomes dumb; one round trip.
- Cons: breaks the existing paged timeline and live entry events; large payload for 2,000 calls; two models to keep in sync.

## Design
**Client rules (AC3, AC37, AC40, E18, E20, E21).** `ui.ClientRules`: `IsSideCall(userTexts []string) bool` (quota probe: exactly one user message equal to `quota`; title generation: a user message starting `<session>` containing `Write the title`), `StripReminders(text) string` (removes `<system-reminder>…</system-reminder>` blocks), `TitleOf(responseText) (string, bool)` (the `title` of the `{"title":…}` reply; false when empty, unparseable or errored), `IsAgentTool(name) bool`. Implemented in `internal/clients/claudecode/rules.go`; wired in `run.go`. A client without rules gets no side calls and unstripped prompts.

**Thread metadata (AC22, AC23, E6, E7).** `timeline.Entry` for an exchange/call gains `Thread` (index of the matched or new thread), `PrevID` (request id of the earlier call in the matched thread, empty on a new thread), `Carried` and `Messages` (request message count; `New = Messages - Carried`). Computed from the existing `bestMatch` result; no behaviour change to entries already produced.

**Call facts in the timeline API (AC7, AC11, AC12, AC40, E7, E8).** `model_call` entries gain `call`: `n` (1-based start order over every thread), `thread`, `prev_id`, `side_call`, `main`, `carried`, `new`. `main` = thread has a user prompt and is not a side call and is not a sub-agent. A thread is a sub-agent when any tool call entry in it carries `agent_id`/`agent_type`, or its first user prompt equals the `prompt` input of a parent tool call that `IsAgentTool` names (D7). Compaction and `/clear` start threads that keep a prompt and stay main. Errored side calls stay red per call (`side_call` plus the exchange's error entry); session-level counts come from `main_errors`/`side_errors` in the digest, never recounted in the browser. Context, context partial flag and deltas are computed in the browser from the entry's `usage`, following `prev_id` back to a call with complete usage (E4, E5).

**Session digest (AC1, AC3, AC10, AC37-AC39, E2, E3, E19, E21, E23).** `timeline.SessionDigest` (one pass, content reads only for the title) feeds `buildSummary`: `title`, `has_calls`, `has_hooks`, `tool_states` (counts by the six chips, from the same harness derivation as the timeline), `errors` (unchanged: all errors, so the API change stays additive), `main_errors` (side calls excluded) and `side_errors`, with `errors = main_errors + side_errors`; the UI reads only `main_errors` for the Errors column, the Has errors filter and the stats row (AC10, AC39). `usage` unchanged (side calls count). Title = latest stored generated title that `TitleOf` accepts, else first non-side user prompt with reminders stripped and not empty, cut to `PreviewRunes`, else `""` (browser shows "no prompt captured"). Because the list, get, snapshot and live `session` event all go through `buildSummary`, titles update live with no new event. CLI output (`timeline.SessionStats`) is untouched.

**Digest memo (AC1, AC3; perf).** `ui` keeps an in-memory `digestCache` mapping session id plus a version to the digest (title, tool states, error split, sources only). The version is a composite from a new read-only `store.CtxReader.SessionVersions(ids []SessionID) (map[SessionID]Version, error)`, read in one transaction from two grouped queries for a whole list page: `WITH l(p,c,k) AS (VALUES …) SELECT e.principal_id, e.client, e.session_key, COUNT(*), MAX(e.rowid), COUNT(e.parse) FROM l JOIN exchanges e ON e.principal_id=l.p AND e.client=l.c AND e.session_key=l.k GROUP BY` the three columns, and the same CTE joined to `hook_events h` with `COUNT(*), MAX(h.id)`. Query-plan evidence (sqlite3 3.46.1 CLI, read-only on the local store; the driver, modernc.org/sqlite, may differ so the test below decides): the row-value `IN (VALUES …)` form on `exchanges` plans as `SEARCH exchanges USING INDEX exchanges_principal (principal_id=?)`, which has no SCAN but walks every exchange of the principal (the whole table in a single-principal store); the CTE join plans as `SEARCH e USING INDEX exchanges_session (principal_id=? AND client=? AND session_key=?)` and `SEARCH h USING COVERING INDEX hook_events_session (…)` with only `SCAN l` for the list; a plain `session_key IN (…) AND principal_id IN (…) AND client IN (…)` uses the index too but can match identity combinations that were not requested and would need filtering back. The CTE join was chosen on that evidence and matches exact identities. Grouping and filtering use the full identity, never `session_key` alone: a session is (principal, client, key) (`sessionID`), the key is a client-supplied header value, and the same value from another principal or client is a different session, so a key-only group would merge them and share one version. A session with no exchanges (hooks only) or no hook events is absent from that query's result and gets the zero value for that half, so every requested session has a defined `Version{ExCount, ExMaxRow, Parsed, HookCount, HookMaxID}`; rows with a NULL `session_key` never match the `IN` list. A single-session read (`getSession`, live `session` events, snapshot) calls the same function with a one-element slice, so both paths run the same SQL; `TestSessionVersions_BatchEqualsSingle` proves it. Findings from the store's write paths: no single "last event seq" exists (`events.seq` is per request, `hook_events.seq` its own counter); `exchanges` is only INSERTed (no REPLACE, no DELETE anywhere), so there is no re-commit case. The normal capture path is two writes: `SaveExchange` inserts the row with `parse` NULL, then `SaveParse` (`internal/store/exchange.go`) updates `parse` and inserts all of that exchange's events in one transaction, with no change to rowid or count. `COUNT(parse)` (non-NULL parses) moves in exactly that transaction, and events are only ever inserted by it, so it covers every event write. Other writes feeding the digest: `backfill.go` sets `session_key` on old exchanges, which moves them into a session and changes its exchange count and, if they are newest, max rowid; `SaveHook` inserts into `hook_events`, which moves the hook count and max id. The composite therefore changes on all four writes (insert, parse landing, backfill, hook). A future write that edits events or exchange rows in place without changing these aggregates would be missed; `TestSessionVersions_ChangesOnEveryWritePath` (run through the batch query) is the guard and must be extended with any new write path. `TestSessionVersions_QueryPlansUseSessionIndex` runs `EXPLAIN QUERY PLAN` on the exact query strings the code runs (shared constants) through the real driver and fails unless the target table's line is a SEARCH using `exchanges_session` / `hook_events_session` with all three equality columns; any SCAN of the target table (alias or `SCAN … USING INDEX` included) or use of `exchanges_principal` fails, while `SCAN l` and constant-rows lines for the list are allowed. Cost: a list page costs two queries in total, not about two per session. Each uses the `exchanges_session` and `hook_events_session` indexes, with one row lookup per exchange for `rowid`/`parse`, so O(exchanges in the page's sessions) with no event or content reads, far below the digest's own cost (events pass plus content reads), which a hit skips entirely. `BenchmarkSessionList_WarmCache` (50-row page, every digest cached) measures the whole warm path; the implementer records the number in `research.md` Notes. Clock-dependent values (`active`, anything from `now`) and `last_at`/counts from the session row are never memoised: `buildSummary` merges the cached digest with the fresh row and `d.now()` every time. It is a pure cache: lost on restart and rebuilt on demand, so the process stays stateless in the AGENTS.md sense. Size bound: at most 512 entries, least recently used evicted; an entry holds a few hundred bytes. Concurrency: one `sync.Mutex` guards lookup and insert; compute runs outside the lock; two requests may compute the same session at once and the last write wins, which is harmless because values are immutable and equal for equal versions. Digests are stored by value and never mutated after insert, so list pages and live `session` events share it safely under `-race`.

**Today (AC4, E1).** `GET /api/today?since=<RFC3339>` (the browser sends its local midnight): sessions with a call started at or after `since`, calls, and the four counters, using the existing `counterJSON` shape ("—" for unreported). New read-only `store.Reader` method for exchanges started since a time. Refetched by the client on `session` live events (debounced).

**Exchange fields for the drawer (AC21-AC25, AC29, AC40, E4, E6, E14).** `GET /exchanges/{id}` gains `side_call`, `prev_id`, `thread_new`, `messages` (`role`, cut `text`, `new`), `carried`, `new_count`, `response` (`text`, `stop_reason`, assembled from stored events; empty with a reason string when not stored/parse failed), and `response_tool_call_ids` so the browser joins tool states from the loaded timeline. A section that cannot be built sets `unavailable` with a reason, never an error status.

**Web structure (AC2, AC30-AC35, AC19, AC20, AC26-AC28).** `index.html` references `style.css`, `app.js` only. New modules: `theme.js` (get/set with try/catch around localStorage, dark default, E15), `route.js` (parse/build hashes incl. `#/sessions/{id}/call/{exchangeId}`), `model.js` (chips, turns, stats, strip, context series, deltas, side-call cards), `filters.js` (list and timeline filters), `keys.js` (key-bar content and the shortcut reducer, input guard), components `shell.js`, `sessions-page.js`, `timeline-page.js`, `drawer.js`. `session-view.js`, `model-chip.js`, `session-list.js` are replaced; `tool-card.js` is reshaped into tool rows; `exchange-detail.js` and `ungrouped-list.js` stay, under the new shell. Opening a drawer pushes a hash; ↑/↓ replace it; Esc/close goes back when the drawer was opened in-app, else replaces with the session URL; focus returns to the opener or the current turn heading (E11). Live data goes through the existing `liveEffect` path; the drawer keys on request id so it holds its call, focus and scroll while content behind it updates (AC28). Auto-scroll runs only with the drawer closed and the view at the bottom. Rendering uses `DocumentFragment` and one build per live batch to meet the 2,000-call budget. Captured text is only set via `textContent`; the existing `web_test.go` guards stay and are extended for new files. Contrast is enforced by a Node test that reads the colour tokens from `style.css` for both themes (AC34); status never relies on colour (glyph plus word).

**Errors, tenancy, migrations.** Loopback-only and read-only as before; no storage or schema change; no new dependency. Store failures keep the existing `writeStoreErr` path.

## AC coverage
| AC | Where | Verified by |
|---|---|---|
| AC1 | `api_sessions.go` digest; `sessions-page.js` | `TestSummary_ToolStateCounts`, `TestSummary_Sources`; `sessions-page.test.js: rowCellsShowDashForNoUsage` |
| AC2 | `shell.js`, `route.js` | `shell.test.js: breadcrumbStartsAtSessions`, `breadcrumbShowsSessionTitle` |
| AC3 | `timeline.SessionDigest`, `claudecode/rules.go` | `TestSummary_TitleIsLatestGenerated`, `TestSummary_TitleFallsBackToFirstPrompt`, `TestHub_SessionEventCarriesNewTitle` |
| AC4 | `/api/today`, `sessions-page.js` | `TestToday_CountsOnlyCallsSince`, `TestToday_CountsSideCalls` |
| AC5 | `sessions-page.js` | `sessions-page.test.js: ungroupedLinkShownWhenExchangesExist` |
| AC6 | `filters.js` | `filters.test.js: hasErrorsIgnoresSideCalls`, `hasGapsMatchesUnmatchedAndPartial`, `filterTextMatchesLoadedRows` |
| AC7 | `model.js buildTurns`; call facts in `api_timeline.go` | `turns.test.js: turnStartsAtMainPrompt`, `otherThreadCallJoinsOpenTurn` |
| AC8 | `timeline-page.js` | `turns.test.js: turnHeadingCountsAndDuration`, `callLinksLabelled` |
| AC9 | `timeline-page.js` rail | `turns.test.js: railListsEveryTurn`, `railSelectionMarksCurrent` |
| AC10 | `model.js stats` (reads `main_errors`) | `stats.test.js: statsExcludeSideCallErrors`, `contextIsLastMainCall` |
| AC11 | `model.js strip`, `timeline-page.js` | `strip.test.js: oneBarPerCallMarkedByTurn`, `sideCallBarsMuted` |
| AC12 | `model.js contextSeries` | `context.test.js: seriesLeavesOutSideAndSubAgent`, `rowShowsContext` |
| AC13 | `model.js chipOf` | `chips.test.js: sixStatesMapToGlyphAndText` |
| AC14 | `timeline-page.js` tool rows | `filters.test.js: toolPickerFiltersLoadedTurns`; `turns.test.js: truncatedLinkAndCopyOffered` |
| AC15 | `timeline-page.js` | `turns.test.js: hooksHiddenBehindToggle` |
| AC16 | `timeline-page.js` Show options | `turns.test.js: showOptionsApplyAtOnce` |
| AC17 | `timeline-page.js`, `shell.js` | `turns.test.js: agentLabelAndErrorMarkKept` |
| AC18 | `model.js buildTurns` (hooks-only) | `turns.test.js: hooksOnlySessionStartsTurnsAtPrompt` |
| AC19 | `app.js`, `timeline-page.js` | `live.test.js: followOnlyWhenDrawerClosedAndAtBottom`, `liveUpdatesAllViews` |
| AC20 | `drawer.js`, `route.js` | `drawer.test.js: opensFromLinkBarAndEnter` |
| AC21 | `drawer.js` | `drawer.test.js: toolChainShowsThreeSteps` |
| AC22 | `/exchanges/{id}`, `model.js deltas`, `drawer.js` | `TestBuild_CallCarriesThreadAndPrevious`; `context.test.js: deltaMeasuredFromPreviousCallWithUsage` |
| AC23 | `/exchanges/{id}`, `drawer.js` | `TestExchange_MessagesCarriedAndNew`, `TestExchange_NewThreadMarked` |
| AC24 | `/exchanges/{id}`, `drawer.js` | `TestExchange_ResponseAndStopReason`; `drawer.test.js: healthShowsDisconnectAbortError` |
| AC25 | `drawer.js` | `drawer.test.js: rawLinksAndFullPageLink` |
| AC26 | `drawer.js`, `keys.js` | `drawer.test.js: arrowsStepAndEscReturnsFocus` |
| AC27 | `route.js`, `app.js` | `route.test.js: drawerUrlRoundTrips`; `drawer.test.js: backClosesDrawer` |
| AC28 | `drawer.js`, `app.js` | `live.test.js: drawerKeepsCallFocusAndScrollOnLiveData` |
| AC29 | `/exchanges/{id}`, `drawer.js` | `TestExchange_BodyNotStoredStillAnswers`; `drawer.test.js: missingSectionStatesReason` |
| AC30 | `theme.js`, `shell.js` | `theme.test.js: pickStoresAndApplies`, `usesStoredThemeOverOs` |
| AC31 | `keys.js keyBar` | `keybar.test.js: levelsShowOnlyWorkingShortcuts` |
| AC32 | `keys.js` | `keybar.test.js: shortcutsIgnoredInInputAndEscLeaves` |
| AC33 | all components | `web_test.go: TestWeb_ActionsAreButtonsOrLinks`; manual check |
| AC34 | `style.css` | `contrast.test.js: allTextPairsMeet4_5InBothThemes` |
| AC35 | all views | `web_test.go: TestWeb_NoCostOrContextPercent` |
| AC36 | `app.js` paging | `live.test.js: loadsEveryTimelinePage` |
| AC37 | `model.js buildTurns`, `rules.go` | `turns.test.js: sideCallsNeverStartTurn`, `TestSummary_OnlySideCalls` |
| AC38 | digest, `/api/today`, stats | `TestSummary_SideCallsStillCountInTokens`; `stats.test.js: sideCallsCountAndKeepLink` |
| AC39 | digest, `filters.js`, cards | `TestSummary_MainErrorsExcludeSideCalls`; `turns.test.js: erroredSideCallStillRed` |
| AC40 | `api_timeline.go`, `api_exchange.go` | `TestTimeline_SideCallFlag`, `TestExchange_SideCallField`; `web_test.go: TestWeb_NoSideCallTextMatching` |

## Test strategy
- Go: `TestRules_QuotaProbeIsSideCall` (E18), `TestRules_TitleCallIsSideCall`, `TestRules_PlainPromptNotSideCall`, `TestRules_StripsReminders`, `TestRules_UnparseableTitleIgnored` (E21), `TestSummary_TitleSkipsReminderOnlyPrompt` (E3), `TestSummary_NoPromptTitle` (E2), `TestSummary_NoRulesShowsPromptsWhole` (E20), `TestBuild_NewThreadHasNoPrevious` (E6), `TestTimeline_SubAgentThreadNotMain` (E7), `TestTimeline_CompactionThreadStaysMain`, `TestTimeline_CallNumbersCountEveryThread`, `TestToday_EmptyStoreZeros` (E1), `TestToday_BadSinceRejected`, `TestExchange_NoUsageNoNumbers` (E4), `TestSummary_MainErrorsExcludeSideCalls` (E23), `TestSummary_ErrorsStaysAllErrors`, `TestDigestCache_UnchangedSessionNotRecomputed`, `TestDigestCache_ChangedSessionRecomputed` (new exchange, backfilled exchange, new hook line), `TestDigestCache_ParseLandingRecomputes` (save exchange, compute digest, save parse, digest recomputed with title and tool states), `TestDigestCache_ActiveFlagNotMemoised`, `TestDigestCache_BoundedAndRaceFree`, `TestSessionVersions_BatchEqualsSingle`, `TestSessionVersions_SameKeyOtherPrincipalOrClientIsSeparate`, `TestSessionVersions_HooksOnlyAndNoHooksSessionsHaveKeys`, `TestSessionVersions_QueryPlansUseSessionIndex`; `BenchmarkSessionList_WarmCache` (50-row page, all digests cached; number recorded in research.md Notes by the implementer).
- Node: `context.test.js: callWithUnreportedCounterIsDashAndPartial` (E5), `turns.test.js: beforeFirstPromptCard` (E8), `onlySideCallsOneCardNoTurns` (E19), `noMainCallsContextNote` (E22), `keybar.test.js: ungroupedAndExchangeShowSessionsLevel` (E17), `drawer.test.js: firstAndLastStepDisabled` (E9), `unknownCallShowsNoSuchCallNote` (E10), `focusFallsToTurnHeading` (E11), `streamDropKeepsDrawer` (E16), `filters.test.js: noMatchNoteWithLoadMore` (E12), `strip.test.js: barsKeepMinWidth` (E13), `chips.test.js: ranNotReportedStaysPendingInEndedSession` (E14), `theme.test.js: storageUnavailableDefaultsDark` (E15), `perf.test.js: twoThousandCallsModelUnderBudget`.
- Go guards in `web_test.go` extended per task. The 2 s render budget and keyboard/contrast feel get one manual check file.

## Risks
- Side-call and sub-agent rules are heuristics on client text: mislabel limits to a missing/extra "side calls" card; raw data and `#n` links stay. Rule lives in one profile file with table tests.
- Digest adds per-session work to every list page and live `session` event (title content reads, harness pass): one events pass, bound by the 50-row page, memoised per (session, composite version) with a 512-entry LRU bound. A stale hit would show an old title or counts, and the likeliest cause is the two-step capture (`SaveExchange` then `SaveParse` updates the row in place): the version includes the count of parsed exchanges so the parse landing changes it, backfill and hook writes are covered too, and `TestDigestCache_ParseLandingRecomputes` plus `TestSessionVersions_ChangesOnEveryWritePath` guard it. Keying on the full (principal, client, key) identity in the grouped queries keeps unrelated sessions from sharing a version. The grouped queries could degrade to a table walk if the planner picks another index (`exchanges_principal` walks a whole principal): the plan test fails on that instead of letting it slow every list page. A new write path that edits rows in place would need the version extended; a miss only costs a recompute.
- Big front-end rewrite replaces working 009/010 views: old tests are ported, not deleted, and `#/ungrouped`, `#/exchanges/{id}` keep their tests.
- Sub-agent detection (D7) can show a false turn when no hooks and no captured Agent call exist; accepted in the spec.

## Open questions
None.

## Changelog
- v1 (2026-11-02) — first draft; errors stay all errors with added `main_errors`/`side_errors`; digest memo added
- v2 (2026-11-02) — reopened: CR-001: Plan: tool call chips are computed on the server
