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
