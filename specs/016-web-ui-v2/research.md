---
feature: 016-web-ui-v2
---
# Research & decisions — 016-web-ui-v2

## Open questions
<!-- Answer by changing "(open)" to "(answered)" and writing the answer underneath. -->
- **Q1** (answered) T003 — Where does a turn's clock time end when there is no final answer
  (still running, interrupted with Esc, ended by an errored call)? — options: A) at the last
  call or tool end seen in the turn, and "running" while the session is active (recommended:
  honest, never counts idle time) B) at the next prompt (simple, but counts the time the user
  spent reading)
  - Answer (Karthik, 2026-11-11): A. Plus: a turn with no final answer shows its end state as
    glyph + word: running (session active), interrupted, or ended by error.
- **Q2** (answered) T007 — What does `gateway timeline` print by default once it follows the
  view? — options: A) the turn view in text, `-json` the API's view object, today's flat
  entry list behind a new `-entries` flag (recommended: one source of truth, old output still
  reachable for scripts) B) keep the flat list as default and add `-turns`
  - Answer (Karthik, 2026-11-11): A.
- **Q3** (answered) T006 — How much does one view response carry for a 2,000-call session? —
  options: A) every turn at once with tool lines and effects, but no tool inputs, results,
  diffs or thinking (fetched when a dialog opens); measure against the 1.2× budget before
  and after (recommended) B) page turns (first 50, then "Load more") from the start
  - Answer (Karthik, 2026-11-11): A. Measure both cold (fresh gateway, no cache) and warm
    against the budget, and keep 013's constraint: a 2,000-call session renders within 2 s
    of its data arriving. Record the numbers here.
- **Q4** (answered) T008 — The chosen mockup drops 013's key bar and most shortcuts (`e` next
  error, `t` next tool, `h` hooks, `/` filter, Backspace). Keep any? — options: A) no key bar;
  keep `j`/`k` for turns, arrow keys in tabs, Esc in dialogs (recommended; written into AC13)
  B) keep the key bar and all 013 shortcuts that still have a target
  - Answer (Karthik, 2026-11-11): A. Every action stays reachable by Tab/Enter on real buttons
    and links.
- **Q5** (answered) T009 — 013's text filter and tool-name picker over turns have no place in
  the mockup. Drop them? — options: A) drop in 016; the right card's lists cover "where did
  this file / command / site come from" (recommended) B) keep a filter box above the story
  - Answer (Karthik, 2026-11-11): A.

## Decisions
- D1 (2026-11-10) — Built outside the agent-loop kit, like 014 and 015: one branch, feature
  tasks first, the seed early, functional tests near the end, `make verify` green after every
  commit, a git note per commit, leaks patched as found and logged here as dated lines; no
  `/al-*` commands, no `loop.sh`, no approval fingerprints — Karthik's choice.
- D2 (2026-11-10) — Reference: `mockups/v4-story.html`, chosen by Karthik after two rounds of
  feedback; variants 1–3 removed (in git history at 36233f6). The spec wins where they differ.
- D3 (2026-11-10) — Split: 016 is the session page (15 ACs), 017 the sessions list and the
  call details that replace the 013 drawer — the session page alone fills the 15-AC budget.
- D4 (2026-11-10) — The server computes every fact; the browser only displays. One builder
  in `internal/timeline/` makes the view for the API and the CLI (plan.md A1).
- D5 (2026-11-10) — Tool time leaves out an Agent call's span (its sub-agent's model calls
  already count as model time); a turn's clock time = model ∪ tool ∪ waiting, where waiting
  is the clock time covered by neither (mockup choice).
- D6 (2026-11-10) — `several running` effects are listed once, after the step whose tool
  calls they name; when the candidates came from different steps, after the latest (mockup
  choice, spec E6).
- D7 (2026-11-10) — `harness` and `outside` effects inside a turn sit in one "not caused by a
  tool call" group, each with a "during #n Tool" hint naming whatever tool was running
  (mockup choice).
- D8 (2026-11-10) — Candidates are named by tool and the first words of their input, flags
  dropped from the end (`#16 Bash npm install`, `#16 Bash npm test`), so two parallel calls
  of one tool read apart (mockup choice).
- D9 (2026-11-10) — An edit between turns gets its own block only when it is an `outside`
  file change whose checkpoint window starts before the next turn's prompt; it is worded
  "seen between turns N and N+1", since the gateway sees when a change was found, not when it
  was made (mockup choice, refined by Karthik).
- D10 (2026-11-10) — A session with no captured prompt is titled from what is known, never
  its key: generated title → first prompt → "Hooks only · <folder>" or "No prompt ·
  <folder>" → "Hooks only · <client> · <start time>" (mockup choice).
- D11 (2026-11-10) — Searches (Grep, Glob, and any tool the profile marks as a search) are
  counted and listed apart from file reads; folders never count as files read. Found as a
  mockup accuracy bug.
- D12 (2026-11-10) — Context is per thread: each row says what was added since the previous
  call of the same thread; side calls are separate requests and left out (mockup choice).
- D14 (2026-11-11) — 016 spec, plan and tasks approved by Karthik with Q1–Q5 and the 15
  recommendations folded in; 017 stays spec-only until 016 is built. Build rules: no
  agent-loop kit, feature tasks first, one or two functional tests per part near the end,
  `make verify` green after every commit, a git note per commit, no push without his OK, the
  gateway service is never restarted by the agent (commands are printed for him).
- D13 (2026-11-10) — `gateway timeline` follows the server view; its pinned tests
  (`internal/timeline/render_test.go`, `cmd/gateway/viewer*_test.go`) are updated in the open
  in T007, each change noted under Notes with why (Karthik's rule).

## Decisions for Karthik
Choices made while drafting that you were not asked about. Each has a recommendation; say if
you want another.

1. **Tool summaries live in the profile behind a new optional `core.ToolSummarizer`**
   (`SummarizeTool(name, input, workDir) ToolSummary` with arg, kind, added, removed). Kind
   (`command`, `file`, `search`, `fetch`, `skill`, `agent`, `other`) is how the view knows
   which calls go in the Commands tab without naming Bash. *Recommend: yes.* Alternative: a
   table in config — rejected, it would duplicate 015's tool knowledge.
2. **Tool run time comes from hook spans** (pre → post receive time). With no hooks, the gap
   between the asking call's end and the next call of its thread, shown with `≈` (spec E3).
   *Recommend: yes.* Alternative: show "—" — honest but leaves most API-only sessions blank.
3. **Model time is the union of model-call spans in the turn, sub-agent calls included;**
   side calls are not counted in any turn. *Recommend: yes.*
4. **Paths are relative to the session's starting folder** (the first hook's working
   folder); outside it, absolute with the home folder as `~` (spec E7). *Recommend: yes.*
5. **"Needs a look" holds a fixed set**: failed tool calls, errored model calls outside side
   calls, `outside` changes, `several running` effects, hooks-only turns, `? unmatched` tool
   calls — at most 8 items, then "and N more". *Recommend: yes.*
6. **Hook lines move into the tool details dialog** (asked → ran → reported with state,
   reason and outcome), replacing 013's "N hooks" toggle. *Recommend: yes.* Alternative: a
   per-turn "N hooks" toggle at the end of the card.
7. **Reminders**: the prompt heading shows what the user wrote; a "+N reminders" toggle under
   it shows the injected blocks (013 AC16's option, always available). *Recommend: yes.*
8. **Copy buttons** move into the dialogs (prompt, input, output, diff). *Recommend: yes.*
9. **Theme lands in `style.css` for every page in 016** (tokens, type, cards, badges), so the
   sessions list changes colour now and layout in 017. *Recommend: yes.* Alternative: keep
   two themes until 017 — the UI would look half-done between the two features.
10. **Contrast check**: Chrome DevTools' Lighthouse accessibility audit plus a page snippet
    (in the manual check) that walks every visible text node, computes its WCAG ratio against
    its painted background and lists any pair under 4.5:1, in both themes. No install.
    *Recommend: yes.*
11. **Live updates**: the session stream gains a `view` event carrying the changed turns, the
    overview and the right-card lists whole (not entry deltas); the browser replaces them by
    turn number. *Recommend: yes.* Alternative: a version ping and a full refetch — simpler,
    heavier at 2,000 calls.
12. **`#n` keeps opening the 013 drawer until 017** replaces it with the call details
    dialog; the Context tab rows and bars open the same drawer. *Recommend: yes.*
13. **017 gets only a spec now**; its plan and tasks are drafted once 016 is built, since 017
    reuses the view endpoint and the dialog shape. *Recommend: yes.*
14. **New seed `docs/manual-checks/seed-session.mjs`** combines `seed-calls.mjs` (stub
    upstream, real capture path) and `seed-effects.mjs` (hooks, file changes, proxy
    connections) to reproduce every case of the mockup dataset in one session. *Recommend:
    yes.* Alternative: extend `seed-effects.mjs` — it has no model calls.
15. **Web tests for removed browser code are retired with that code, in the open**:
    `turns.test.js`, `strip.test.js`, `stats.test.js`, `context.test.js`, `keybar.test.js`,
    `filters.test.js`, `tool-card.test.js` and the placement tests in `effects.test.js` test
    builders that move to Go (where `view_test.go` covers the same rules) or features the
    spec drops. Each removal is listed under Notes with the AC that replaced it; no other
    test is weakened. *Recommend: yes.* Alternative: keep the old modules alive for their
    tests — dead code kept to keep tests green.

Answer (Karthik, 2026-11-11): all 15 accepted. #11 adds: the browser also renders turns by
key and replaces only the changed turn's node. #15 is accepted on these conditions: each
retired web test is removed in the same commit as the code it covered; this file lists each
one with either the Go test that now covers the same fact or the AC that drops the feature;
no test is retired for any other reason.

## Notes
- 2026-11-10 — Mockup accuracy bugs found during review and fixed in the dataset before
  drafting: context shrank between calls of one thread (cache read now equals the previous
  call's context); Grep and Glob counted as file reads; the sub-agent's last call said "Wrote
  the answer" (it reports back to the main agent); "3 changes" counted a connection as a
  change. Each is now an AC or a decision (AC10, D11, AC7, AC2).
- 2026-11-10 — The turn-list bug in the mockup (the last turn never marked) came from an
  observer band the page could not scroll the last turn into; AC3 states the behaviour, and
  the mockup's fix (click pins the mark until the user scrolls; room under the last turn) is
  the reference.
- 2026-11-10 — Today `GET /api/sessions/{id}/timeline` sends flat entries and
  `internal/ui/web/model.js` builds turns, durations, rail items and effect placement
  (`buildTurns`, `railItems`, `placeEffects` in `effects.js`); 016 moves each of these to
  `internal/timeline/` (plan.md).
- 2026-11-11 — View cost at 2,000 calls (T006, constraint and Q3): `seed-calls.mjs --calls 2000`
  on a fresh throwaway gateway (9,600 entries, no hooks or effects), curl from localhost.
  Cold = first request after a gateway restart, no cache; warm = five repeats.
  | request | cold | warm | body |
  |---|---|---|---|
  | `/timeline?limit=2000` (one page, before) | 2.35–2.57 s | 10–12 ms | 0.54 MB |
  | `/timeline` every page (what the 013 page loads) | 2.41–2.48 s | 70–79 ms | 2.59 MB |
  | `/view` (after) | 2.34–2.39 s | 6–11 ms | 1.34 MB |
  The view is ≈1.0× cold and faster warm: both wait on the same timeline build (2.7 s of
  CPU, mostly event reads and call facts); BuildView adds ~80 ms and marshalling ~130 ms.
  A first run before empty fields were omitted measured 3.16 s cold at 1.54 MB (1.22×), so
  the view's JSON omits empty lists and zero fields. Within the 1.2× budget; paging (Q3 B)
  not needed.
- 2026-11-11 — `gateway timeline` now prints the session view (T007, Q2 A, D13). Pinned
  tests changed in the open; each keeps every assertion and only gains `-entries`, because
  the facts it checks (flat entry order, entry text, `-json` entry and `usage_total` lines)
  now live behind that flag:
  `TestTimeline_PrintsSession`, `TestTimeline_LastPrintsTopSession`,
  `TestTimeline_SameKeyTwoClientsPrintsBoth`, `TestTimeline_JSONFlag` (viewer_test.go);
  `TestTimelineCmd_PrintsUsageTotals` (its `-json` half), `TestTimelineCmd_JSONSessionHeadersHaveNoUsage`
  (viewer_usage_test.go); `TestFixture_HeadHelloNotAModelCall` (its `-json` half),
  `TestFixture_TimelineJSONCarriesSameTotals` (its `-json` half) (viewer_fixture_test.go).
  The default text keeps the usage totals block after the view, so the text halves of the
  usage tests pass unchanged. `internal/timeline/render_test.go` needed no change: the flat
  renderers it pins are unchanged and still used by `-entries`.

### Retired and changed tests (Decisions for Karthik 15, as answered)
Each retired test is removed in the commit that removes the code it covered, with the Go or
web test that now covers the same fact, or the AC that drops the feature.

- 2026-11-11 — T008 (`feat(ui): lay out the session page in the new theme`). The session
  route renders the server's view; the entry-merging live path has no caller and is removed
  (`liveEffect`, `liveBatch`, `replaceExchange`, `replaceHooks` in `web/entries.js`).
  Retired:
  - `app.test.js` `sixPageLoadRendersABoundedNumberOfTimes`, `pagesBeforeAFailingPageStillShow`
    — the page no longer pages the timeline: it loads one view (AC1, Q3 A); render cost is
    covered by `view.test.js` `twoThousandCallViewRendersUnderBudget`.
  - `app.test.js` `aStreamBurstIsDrawnOnceNotPerEvent` — the server folds a burst into one
    view event: Go `TestViewStream_SendsChangedTurns`; the page swaps only changed turns:
    `view.test.js` `liveEventReplacesOnlyTheChangedTurn` (AC12).
  - `entries.test.js` `empty hooks snapshot removes the hook group (E6)`, `a hooks event
    replaces the hook group and keeps the exchanges (never replaceExchange)`, `an empty hooks
    event removes the hook group and keeps the exchanges`, `an exchange or entries event
    replaces its exchange, not the hook group`, `a session event sets the summary and leaves
    the entries alone`, `a snapshot_error event reports its message`, `an unknown event name is
    dropped`, `liveBatchMatchesApplyingEachEventInOrder`, and the `replaceHooks` line of
    `exchange sorts before hook lines on an exact time tie` — AC12 replaces 013 AC19: Go
    `TestViewStream_SendsChangedTurns`, `view.test.js` `liveEventReplacesOnlyTheChangedTurn`.
  - `perf.test.js` `snapshotOfTwoThousandExchangesMergesUnderBudget` — no entry merge (AC12):
    `view.test.js` `twoThousandCallViewRendersUnderBudget`.
  - `turns.test.js` `lateCommitRenumbersFromTheOrderKey` — the server numbers calls on every
    build: Go `TestView_Turns`.
  - `keybar.test.js`: the TURNS and CALL bar contents and the `e`, `t`, `Enter`, `h`, `/`,
    `Backspace` session keys — AC13 and Q4 A drop the session key bar and those keys; `j`/`k`
    and the drawer's arrows and Esc stay asserted.
  - `layout.test.js` `railIsFixedBetweenHeaderAndKeyBar`, `narrowWindowPutsTheRailBackInFlow`
    — the rail is gone (AC3 replaces 013 AC9): `storyScrollsAloneBetweenTheTwoCards`,
    `narrowWindowFallsBackToOnePage`.
  - Go `TestStream_LiveHookUpdateReplacesFirstSnapshotHookLines`: only its pin of the removed
    `web/entries.js` replace functions; its server assertions stay.
  Changed in the open, same facts: `api.test.js` (the stream also listens for `view`);
  `app.test.js` `droppedSessionStream…`, `retriesSessionLoadOnceAfter504`,
  `showsBuildingSessionWhileWaitingToRetry`, `secondFailureShowsTheError`,
  `callDetailRetriesOnceAfter504`, `callDetailSecond504ShowsTheReason` (the page loads
  `/view`; the drawer loads entries when a call opens), `laterPage504IsNotRetried` (now the
  drawer's paged load); `contrast.test.js` (pairs on the new tokens, every text colour on
  every background, digit names read); `web_test.go` `TestWeb_ComponentsCallTheTestedHelpers`
  (wiring of `mergeView` and keyed turns instead of `liveBatch`); `dom-stub.js` (`append`
  turns a string into a text node, as the DOM does).
- 2026-11-11 — T009 (`feat(ui): render the session story from the server view`). The story
  renders the view's turns (`web/story.js`); the 013 page and its builders are removed:
  `web/timeline-page.js`; in `web/model.js` `buildTurns` and its helpers, `railItems`,
  `hooksLabel`, and — a deviation from plan.md, which put them in T010 — `stripBars`,
  `buildStats` and `borrowedContext`, because their only caller was the 013 page;
  `placeEffects`, `countsText`, `toolNumbers` in `web/effects.js`; `filterTurns`, `toolNames`
  in `web/filters.js`; `toolRowNode` and `section` in `web/tool-card.js`; `turnTarget` in
  `web/keys.js`; `countText` in `web/entries.js` (no caller left). Retired:
  - `timeline-page.test.js` (whole file) — the 013 page is gone: the story is AC4–AC8, Go
    `TestView_Turns`, `TestView_Placement`, `view.test.js` (T014).
  - `turns.test.js`, `stats.test.js`, `strip.test.js` (whole files) — turns, durations and
    counts are built on the server: Go `TestView_Turns`, `TestView_Times`; the stats row and
    strip are replaced by the overview (AC2) and the Context tab's bars (AC10): Go
    `TestView_Lists`.
  - `perf.test.js` `twoThousandCallsModelUnderBudget` (whole file) — the browser builds no
    model: `view.test.js` `twoThousandCallViewRendersUnderBudget`.
  - `effects.test.js` `placesEffectsInTurns` — placement is on the server (AC6, AC8): Go
    `TestView_Placement`.
  - `filters.test.js` `toolPickerFiltersLoadedTurns` — filters over turns are dropped (Q5 A).
  - `tool-card.test.js` (whole file: hook input, omitted-bytes note, raw link) — the tool row
    is replaced by the tool line (AC5); the hook input moves to the details dialog (AC11),
    and these three facts return as dialog tests in T011.
  - `context.test.js` `contextLineIsAnSvgPolylineWithADotPerCall`,
    `contextLineKeepsTheNoMainCallsNote` — the page's context line is replaced by the
    Context tab (AC10): Go `TestView_Lists`; the drawer's plot keeps `drawer.test.js`
    `contextPartsAndDelta`.
  - `live.test.js` `liveUpdatesAllViews`, `liveEffectsRedrawOnlyTheChangedRows`,
    `newHookEventKeepsEarlierTurnsElements` — AC12 replaces 013 AC19 and 014 AC14:
    `view.test.js` `liveEventReplacesOnlyTheChangedTurn`, Go `TestViewStream_SendsChangedTurns`.
  - `keybar.test.js`: the `turnTarget` asserts — the `e`/`t` keys were dropped in T008 (Q4 A).
  - `web_test.go`: the `tool-card.js` and `timeline-page.js` lines of
    `TestWeb_ServesHarnessBadgeRendering` and `TestWeb_ComponentsCallTheTestedHelpers` — the
    tool line's state, agent and hook tools are now pinned in `story.js`; the hook-input wiring
    returns against `dialog.js` in T011.
  Changed in the open, same facts: `drawer.test.js` and the two drawer tests of `live.test.js`
  host the drawer in `webtest/drawer-host.js` (call links, a turn heading, `sync`) instead of
  the 013 page; `strip-bar` clicks became `call-link` clicks; `unknownCallShowsNoSuchCallNote`
  asserts the drawer's `missing` result (app.js turns it into the note);
  `streamDropKeepsDrawer` lost only its page-note line (covered by `app.test.js`
  `droppedSessionStream…`); `backClosesDrawer` checks the closed position instead of the old
  page's turn count. `app.test.js` `droppedSessionStream…` now also counts the steps' call
  links. `effects.test.js` `attributionText` builds its tool-number map by hand.
- 2026-11-11 — T011 (`feat(ui): open session details in one dialog shape`). The dialog
  needs the hook input's own cut (the old tool row read `hook_omitted_bytes` from the
  timeline entry), so `GET /api/sessions/{id}/tools/{tid}` gains `hook_omitted_bytes`
  (omitted when 0); `TestViewAPI_ToolDetails` (T013) pins it. The three retired
  `tool-card.test.js` facts return: the hook-input section and its omitted-bytes note are
  wired in `dialog.js` (`web_test.go` pins `hookInputEntry(det)`,
  `omittedNote(det.hook_omitted_bytes)`), and `view.test.js` (T014) renders them; the hook
  input still has no raw link, the input and result keep theirs.
- 2026-11-11 — T016: `docs/manual-checks/016-session-page.md` run with the `ui-check` skill in
  Chrome (1,440 × 900, then 1,000 × 800), Karthik watching, on a throwaway gateway (ports
  17195–17199, stub upstream) seeded with `seed-session.mjs --fast`. GIF:
  `~/Downloads/016-session-page-2026-11-11.gif`. Expected against seen:
  | step | expected | seen | match |
  |---|---|---|---|
  | 3 layout | three columns, only the story scrolls; one page ~1,000 px | three columns, story scrolls alone; at 1,000 px two columns, turn list sticky | yes |
  | 4–5 overview | generated title, meta, five cards opening tabs | "Fix flaky checkout tests", folder, model, client; Time 25s, Files 9 (+18 −4, 2 read), Commands 6, Sites 2, Context 7,308 | yes |
  | 6 needs a look | failed Edit, errored #16, between edit, several, hooks-only turn 8, unmatched command | all six, each with Open / Show in turn | yes |
  | 7 tokens, side calls | quiet "N side calls", not red | "2 side calls": #1 quota probe 429, #4 title generation ok, grey | yes |
  | 8 turn list | 8 turns + 1 between entry; last turn selectable | 9 entries; clicking turn 8 marks it although it cannot reach the top | yes |
  | 9–12 turns 1–8 | each turn as the seed printed it | every printed line matched (steps, tool lines, labels, several group, 500 in red as a word, sub-agent block, harness "during #22 Bash", between block, hooks-only turn 8). Turn 1's `npm test` read 0.6 s, not the seed's "~2.4s": `--fast` caps tool time; the seed's line is corrected to follow `--fast` | yes |
  | 13 `#n` opens the drawer | drawer for the call | not exercised in this run (covered by `app.test.js` `droppedSessionStream…` and `drawer.test.js`) | NOT EXERCISED |
  | 14 tabs | files grouped, reads and searches apart; commands in order; sites with unassigned link; context facts, bars, rows | as expected; unassigned link → `#/effects/unassigned` | yes |
  | 15 tablist keys | arrows move, roving tabindex | ArrowRight moved Files → Commands → Sites → Context; only the selected tab has tabindex 0 | yes |
  | 16 dialog | one shape for file, command, tool, site; Back; Show in turn; Esc returns focus | all four; related tool link then Back; "Show in turn 3" closed, scrolled, highlighted, marked turn 3; Esc returned focus to the Diff button | yes |
  | 16 (found) | — | `dialog.close()` then an immediate open (same task) stacked on the closed item: fixed in `fix(ui): start the details dialog afresh…` with a test | fixed |
  | 17 live | new turns without reload; follows at bottom; holds scroll when up; dialog stays | `--live`: 7 → 10 turns, followed the bottom; scrolled up 11 → 13 turns, scrollTop stayed 400; an open tool dialog stayed on `$ npm test` | yes |
  | 18 earlier turns kept | earlier `<article>` not re-created | turn 1 the same element after new turns; its opened "+1 reminder" stayed open | yes |
  | 19 keys | Tab/Enter; `j`/`k` | `j` moved the mark 2 → 3, `k k` 3 → 1 | yes |
  | 20 theme | dark default; light kept on reload | dark first; Light, then a real reload: light | yes |
  | 21 contrast | every pair ≥ 4.5:1 both themes | in-page snippet: dark 474 text nodes, min 6.24:1 (519 with a file dialog open, min 6.24); light 474, min 5.90:1 (519 with a file dialog, min 5.90; tool dialog 30 nodes, min 6.29); none below 4.5. Lighthouse not run (not reachable from the extension) | yes (snippet); Lighthouse NOT EXERCISED |
  | 22 CLI | text and `-json` match the page and `/view` | text matched the seed's lines; `-json` equals `/view` in every field except `version` (CLI `""`, API the cache build key) | yes, `version` differs |
  | 23 2,000 calls ≤ 2 s | | warm reload: 200 turns, 2,000 `#n` links drawn by 586 ms after navigation start (`/view` answered at 67 ms). Cold first open: `/view` 2.31 s (1.3 MB, the server build measured in T006) then drawn ≤ 0.38 s after it, ≈ 2.7 s in all | warm yes; cold over by the server build |
  Open for Karthik: (a) a command's tool line sums the line counts of the changes labelled to it
  (turn 4 `Bash … +7 −1`); the mockup shows counts on Edit/Write only — keep or drop? (b) the
  CLI's `-json` sends `version: ""` where the API sends its build key — AC15 says "the same
  view object"; fill it in the CLI or accept? (c) the cold 2,000-call open is ≈ 2.7 s, of which
  2.3 s is the server build (within the 1.2× budget of T006); the browser render alone is
  ≤ 0.6 s.
- 2026-11-11 — Attribution wording, Files tab against the story (asked by Karthik before the
  final report). They differed: the story's effect lines show spec 015's label as glyph and
  word (`● named`, `◐ only one running`, `◌ several running`, `○ outside any tool call`,
  `⚙ harness`), while the Files tab's rows said it in other words (`by #10 Edit …`,
  `likely by #12 Bash …`, `by one of 2 tool calls running together`, `with no tool running,
  likely you`, `by claude-code itself`). Made them match in
  `fix(ui): word attributions in the files tab as the story does`: a Files row now shows
  the same glyph and word, then the tool call or calls the label points at
  (`● named · #10 Edit src/payment/retry.js`, `◌ several running · #14 Bash npm install,
  #14 Bash npm test`, `○ outside any tool call`). The view's `by` field is now only that
  tool part (empty for outside and harness), which the dialog also uses as its link text;
  the why sentence (`why`) is unchanged. AC9's "who changed it in words" is the label word
  plus the tool. Pinned by `TestView_Placement` (`by` per label) and `view.test.js` (a Files
  row and the story's effect line carry the same label words).
- 2026-11-11 — Light theme: the mockup's `--ok`/`--add` (#15803d) and `--warn` (#a16207)
  read 4.29–4.49:1 on the panel shades; darkened to #166534 and #854d0e. Probed: restoring
  #a16207 fails `contrast.test.js` (`light: warn on panel is 4.48`).
- 2026-11-12 — Time card split (asked by Karthik before the browser look). `6e680d9` counted
  every second outside a turn's model and tool time as waiting, gaps between turns included
  (21.8 s = 5.5 + 1.48 + 14.82 in `TestView_Times`), so idle time between prompts read as
  waiting. Waiting and between turns are now two parts: **waiting** is the sum of the turn
  headers' waiting (inside turns, the same rule: turn clock − model ∪ tool), **between turns**
  is the session clock that no turn covers (before the first prompt, from one turn's end to
  the next prompt, after the last; side calls and hooks-only turns fall here, as they have no
  turn clock). The Time card and `gateway timeline`'s overview read `model · tools · waiting
  · between turns`; the view gains `time.between_ms`. The fixture now reads 21.8 s = 5.5 +
  1.48 + 2.22 + 12.6. Turn headers are unchanged. The four sum to the clock as a turn's three
  do: exactly when model and tool time do not overlap inside a turn (a model call running
  while a non-Agent tool runs would be counted in both). Pinned by `TestView_Times`,
  `TestView_TimeLineText` (CLI line) and `view.test.js` (card text); each fails without the
  change.
- 2026-11-13 — Karthik's answers to the three open points of the T016 run and the one in
  the T003 notes:
  1. **Line counts on a command's tool line** (point a): kept, summed only from file changes
     labelled `named` or `only one running`; `several running` changes are not counted. No
     code change needed: `place()` (`internal/timeline/view_effects.go`) adds an effect to a
     tool's own list only when its label names one tool (`Tool` set), and spec 015's
     attribution sets `Tool` only for `named` and `only one running`
     (`internal/effects/attribute.go`); a `several running` change goes to its step's
     several group (D6), never into a tool line's sum. Turn 4's `Bash … +7 −1` is a `named`
     change. No test pins this exclusion alone today: `TestView_Placement` checks where the
     several change sits, not that it is left out of `t2`'s line counts.
  2. **The CLI's empty `version`** (point b): accepted. AC15 amended: `-json` is the API's
     view object in every field except `version`. Why: `version` is the API's cache key (the
     build key the `ViewCache` memo and the stream compare to decide whether a view changed);
     the CLI builds the view once and caches nothing, so it has no key to send, and making
     one up would claim a cache state that does not exist.
  3. **Cold first open of a 2,000-call session ≈ 2.7 s** (point c): accepted for 016. 013's
     2 s budget counts from the data arriving (the browser draws in ≤ 0.6 s after `/view`
     answers); the 2.3 s before it is the server's timeline build, measured in T006 and
     within its 1.2× budget. Added to the speed follow-ups in `docs/roadmap.md`: cold view
     build for large sessions.
  4. **The 3 ms model/tool overlap** (Time card note of 2026-11-12: a model call running
     while a non-Agent tool runs is counted in both, so the four parts can sum past the
     clock): accepted as is; it rounds away on screen. The note above stays.
- 2026-11-13 — Browser look after the three fixes since T016 (`6e680d9`, `e1cd236`,
  `320966f`, `8b406f8`), fresh throwaway gateway (ports 17195–17199, stub upstream), seed
  `--fast`, Chrome at 1,520 × 784, both themes. Time card: `25s · model 5.5s · tools 9.4s ·
  waiting 2.1s · between turns 7.8s`. Commands card: `longest 5.4s · pandoc d…`, one line
  (box 18.6 px = its line height; `white-space: nowrap`, `text-overflow: ellipsis`), full
  command `pandoc docs/checkout-fix.md -o docs/checkout-fix.pdf` in `title` (the browser's
  tooltip is drawn outside the page and does not show in a screenshot). Files tab: all nine
  changed rows read glyph + word + tool as the story's effect lines do (`● named · #10 Edit
  src/payment/retry.js`, `◐ only one running · #12 Bash node scripts/gen-fixtures.js`,
  `◌ several running · #14 Bash npm install, #14 Bash npm test`, `○ outside any tool
  call`); turn 5's two Bash lines carry no line counts (answer 1). Contrast snippet: dark
  474 text nodes, min 6.24:1; light 474, min 5.90:1; none below 4.5.
- 2026-11-13 — Manual check `docs/manual-checks/016-session-page.md` (T016): passed,
  Karthik's verdict after reviewing the GIF (`~/Downloads/016-session-page-2026-11-11.gif`).

