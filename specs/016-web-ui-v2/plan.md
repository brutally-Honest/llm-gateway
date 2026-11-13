---
feature: 016-web-ui-v2
status: draft
version: 1
approved:
approved-by:
sha256:
fingerprint:
spec-fingerprint:
---
# Plan — 016-web-ui-v2

## Summary
Move the session page's facts from the browser to the server. A new builder in
`internal/timeline/` turns one session's timeline build (013), hook rows (010) and labelled
effects (014, 015) into a `View`: overview, turns with steps, tool lines and placed effects,
between-turn blocks, and the Files, Commands, Sites and Context lists. The API serves it at
`GET /api/sessions/{id}/view` and streams changed parts; `gateway timeline` prints it. The
browser is rewritten to render the view in the chosen layout and theme. Built outside the
agent-loop kit (research.md D1). Updated on 2026-11-13 to match what was built; the
differences from the approved plan are listed under "As built".

## Context
- Timeline build: `internal/timeline/build.go` (`Build`, `BuildWithUsage`, `Entry`,
  `Options`), `harness.go` (hook pairing, chip states), `usage.go`.
- UI build and cache: `internal/ui/build_session.go` (`buildSession`, one snapshot),
  `call_facts.go` (thread, previous call, side call, main), `timeline_cache.go`,
  `api_timeline.go`, `entries.go` (`entryJSON`).
- Effects and labels: `internal/ui/api_effects.go` (`listEffects`, `attribute`,
  `harnessLookup`), `internal/effects/attribute.go`.
- Live: `internal/ui/hub.go`, `hub_effects.go`, `stream.go` (`event: session`, exchange
  events, `event: effects`).
- Profile: `internal/clients/claudecode/rules.go` (`IsSideCall`, `SplitReminders`,
  `TitleOf`, `IsAgentTool`), `effects.go` (`ReadTarget`, `WritePaths`), `attribution.go`.
- CLI: `cmd/gateway/viewer.go` (`runTimeline`), `internal/timeline/render.go`.
- Browser today: `internal/ui/web/model.js` (`buildTurns`, `railItems`, `contextPlot`,
  `stripBars`, `buildStats`), `effects.js` (`placeEffects`), `timeline-page.js`,
  `tool-card.js`, `drawer*.js`, `keys.js`, `style.css`; tests in `internal/ui/webtest/*.test.js`
  run by `make web-test`, among them `contrast.test.js` (WCAG ratios of the `style.css` tokens).
- Seeds: `docs/manual-checks/seed-calls.mjs` (stub upstream, real capture),
  `seed-effects.mjs` (hooks, file changes, proxy).

## Approach
- **One builder, two outputs.** `timeline.BuildView(in ViewInput) View` is pure: it takes the
  entries and call facts of one build, the session's hook rows, its effects with their
  attributions, the profile's optional `core.ToolSummarizer`, the session's side-call and
  reminder rules, and `now`. The UI marshals `View` to JSON; the CLI renders it as text or
  marshals the same JSON.
- **Lean view, details on demand.** The view carries lines, counts, times and ids, never
  tool inputs, results, diffs or thinking text; dialogs fetch those from existing endpoints
  (`/exchanges/{id}`, `/effects/{id}/diff|content`) plus one new `/sessions/{id}/tools/{tid}`
  for a tool call's input, result and hook lines (research Q3).
- **Live by parts.** The hub rebuilds the view when the timeline version or effect version
  moves and sends a `view` event with the overview, the lists and only the turns whose
  content hash changed; the browser renders turns by key and replaces only the changed
  turn's node (Decisions for Karthik 11, as answered).

## Alternatives considered
### A1 — Server builds a view; the browser renders it (chosen)
- Pros: one source for the page and the CLI (they cannot disagree); rules stay in Go next to
  the profile; testable as plain functions; the browser loses ~600 lines of model logic.
- Cons: a new endpoint and live event to design; the view must be cached and diffed by turn
  to stay cheap; a bigger first change than restyling.
### A2 — Keep flat entries; the browser builds the new view
- Pros: no API change; live updates already work per entry.
- Cons: every new rule (durations, between-turn placement, summaries) is written in JS and
  again in Go for the CLI; the profile's tool knowledge would leak into the browser; this is
  what 016 is asked to stop.
### A3 — Server renders HTML
- Pros: the browser shrinks to almost nothing.
- Cons: live updates and dialogs become HTML diffing; the CLI still needs a data form; a
  rewrite of the UI's whole approach for no reader gain.
### Verdict
A1. Flip to paging turns (Q3 B) if the 2,000-call view breaks the 1.2× budget, cold or warm.

## Design
### Core (`internal/core/tools.go`)
```go
// ToolKind says what a tool call does, without naming a tool.
type ToolKind string
const (
    ToolCommand ToolKind = "command" // runs a shell command
    ToolFile    ToolKind = "file"    // reads or writes one file
    ToolSearch  ToolKind = "search"  // searches or lists files
    ToolFetch   ToolKind = "fetch"   // fetches a URL
    ToolSkill   ToolKind = "skill"
    ToolAgent   ToolKind = "agent"   // spawns a sub-agent
    ToolOther   ToolKind = "other"
)

// ToolSummary is one tool call as a line: what it acted on, and for edits the lines
// added and removed (-1 when not known). The view shows Arg alone, the path alone, or
// "Arg in path".
type ToolSummary struct {
    Kind           ToolKind
    Arg            string // "npm test", "“makeKey”"; "" when the line is only a path
    Path           string // absolute path the call names; made relative by the view
    Added, Removed int
}

// ToolSummarizer is an optional profile capability.
type ToolSummarizer interface {
    SummarizeTool(name string, input []byte, workDir string) ToolSummary
}
```
`internal/core/harness.go` gains an optional `PromptReader` (`PromptOf(payload)`), so a
hooks-only turn has its prompt without the core reading Claude Code's payload fields.
ADR 0016 records the server-built view, `ToolSummarizer` and the lean-view rule.

### Claude Code (`internal/clients/claudecode/summary.go`)
Bash → command (whole `command`); Read, Write, Edit, MultiEdit, NotebookEdit → file
(`file_path`, line counts from `old_string`/`new_string` or `content`); Grep → search
(`“pattern” in path`); Glob → search (pattern); WebFetch → fetch (URL without scheme);
WebSearch → fetch (query); Skill → skill (`skill`); Agent → agent (`subagent_type ·
description`); anything else → other (name only). Candidate short names (D8) are made in
the view from `Arg`: first three words, trailing flags dropped.

### View (`internal/timeline/`)
Files: `view.go` (types, `ViewInput`, `ViewRules`, optional `SideCallNamer`),
`view_build.go` (`BuildView`, calls, tools, hook spans), `view_turns.go` (turns, steps, tool
lines, times), `view_effects.go` (effect lines and placement), `view_lists.go` (Files,
Commands, Sites, Context), `view_overview.go` (title, cards, needs-a-look, tokens),
`render_view.go` (CLI text). The call facts (thread, previous call, side call, main) move
from `internal/ui/call_facts.go` to `internal/timeline/facts.go` (`CallFacts`), so the view
and the UI build share one rule.
```go
type View struct {
    Version   string       // the API's cache key; "" from the CLI (AC15 as amended)
    Overview  Overview     // title + source, status, range, folder, models, client, the
                           // Time/Files/Commands/Sites/Context cards, needs_look, tokens
    SideCalls []SideCall   // n, name, time, duration, status word; never errors here
    Turns     []Turn
    Between   []Between    // after turn N: title, text, effects
    Files     FilesList    // changed (per path, with every change), read, searches, coverage
    Commands  CommandsList
    Sites     SitesList    // per host, with the unassigned connections seen meanwhile
    Context   ContextView  // per thread: rows with ctx, delta, why
}
type TimeCard struct { ClockMS, ModelMS, ToolMS, WaitMS, BetweenMS int64 }
type Turn struct {
    N; Hash                // Hash: content hash for live diffing
    Prompt; Reminders; At; Models; End *Mark // End: running, interrupted, ended by error (Q1)
    ClockMS; ModelMS; ToolMS; WaitMS; HooksOnly; Counts
    Steps []Step; HookTools []ToolLine // hooks-only tool calls
    Loose []EffectLine; Answer; AnswerCall
}
type Step struct { N; ExchangeID; At; DurMS; Did; Errored; Status; Thinking; Agent
                   Tools []ToolLine; Several []EffectLine; SeveralNames []string }
type ToolLine struct { ID; Name; State Mark; Chip; Kind; Summary; FullPath; Added; Removed
                       DurMS *int64; Approx; FirstError; CallN; Agent; Short; Turn
                       Effects []EffectLine; Sub *SubAgent }
type EffectLine struct { ID; Kind; At; What Mark; Path; FullPath; Change; Size; Added; Removed
                         NoCopy; HasDiff; HasCopy; Detail; Host; Port; Up; Down; DurMS
                         Outcome; Label; Mark *Mark; Tool; By; Why; During; Candidates
                         Turn; Where }
```
`Mark` is a glyph and a word (`● named`, `✗ failed`): the page and the CLI show the same
pair. The JSON omits empty lists and zero fields (cost, research 2026-11-11).
Rules, each one function with a table test:
1. **Turns**: the 013 rule (a main-thread prompt starts a turn; other threads join the open
   turn; side calls go to `SideCalls`); hooks-only sessions from prompt hooks (013 AC18).
2. **Steps**: main-thread calls; sub-agent calls nest under the Agent tool line whose
   tool-call id their thread's first prompt answers (013 thread facts); `Did` text from tool
   count and stop reason; errored = error entry or status ≥ 400.
3. **Times**: tool span from hook pair (pre → post/failed/denied receive time) else the gap
   rule (E3); clock = prompt time → end of the last main-thread call (Q1 A: last activity
   when none and `Running` while active); model = union of call spans; tool = union of
   non-agent tool spans; wait = clock − union(model ∪ tool). The Time card adds a fourth
   part, **between turns** = session clock no turn covers (research 2026-11-12).
4. **Placement**: an effect with `tool` (`named`, `only one running`) → that tool line,
   whose line counts then sum those changes only; `several` → the step of its latest
   candidate; `harness`/`outside` → `Loose` with `During` = tools whose span holds the
   effect's time; `outside` file change with `window_from` < next prompt → `Between` (D9);
   unlabelled → by time into the turn holding it (014 rule), `Loose`.
5. **Paths**: relative to the folder of the session's first hook with a working folder;
   outside it, absolute with home as `~` (home from the gateway's own `os.UserHomeDir`).
6. **Lists**: Files groups changes by path (reads and searches apart, D11), each row worded
   as the story's effect line: label glyph and word, then the tool call(s) it points at
   (`by`); Commands = tool lines of kind `command`, hooks-only ones included; Sites groups
   connections by host; Context per thread (D12).
7. **Title** (D10) and **needs-a-look** (Decisions for Karthik 5).

### API (`internal/ui/api_view.go`)
- `GET /api/sessions/{id}/view` → `View` JSON; built by `internal/ui/view.go` (the loader,
  also exported as `ui.BuildView` for the CLI) from the cached timeline build plus a fresh
  effects read (`store.SessionEffects`, `store.UnassignedConnsBetween`), memoised in a
  `ViewCache` per (timeline version, effect version); that key is the view's `version`.
  Effect line counts come from the stored copies through `effects.LineCounts`, the same
  edit script the diff dialog renders.
- `GET /api/sessions/{id}/tools/{tid}` → a tool call's input, result (cut like today, with
  the raw link), hook lines with state, reason and outcome, and `hook_omitted_bytes` for
  the hook input's own cut.
- The existing `/timeline`, `/effects`, `/exchanges` endpoints stay (drawer, exchange page,
  017).

### Live (`internal/ui/hub.go`, `stream.go`)
The session stream gains a view mode, `GET /api/sessions/{id}/stream?view=1`. After the hub's
existing rebuild trigger (exchange, hook or effects event), it waits 150 ms for the burst to
end, builds the view once and sends `event: view` with `{version, full, order, turns:
[changed turns only], overview, side_calls, between, files, commands, sites, context}`;
`order` is every turn number in order, so the page drops turns that are gone. A new
subscriber gets the whole view first (`full: true`). The stream without `view=1` is
unchanged and still feeds the drawer until 017.

### CLI (`cmd/gateway/viewer.go`, `internal/timeline/render_view.go`)
Text: header line; per turn `Turn N · 10:08:40 · 32s (model 30s · tools 0.4s · waiting
1.3s)`, the prompt, step lines (`#10 thought and wrote 9.2s, ran 1:`), indented tool lines
(`✗ failed Edit src/payment/retry.js · 0.1s`), effect lines (`● named ~ modified
src/payment/retry.js +6 −2`), between blocks, one `side calls: #1 quota probe 429, #3 title
generation` line. `-json` = the API's view JSON. Old flat output behind `-entries` (Q2 A).

### Web (`internal/ui/web/`)
- `style.css`: the mockup's tokens, type, cards, badges, app shell; every page takes the
  tokens (Decisions for Karthik 9).
- New `session-page.js` (three-column layout, overview, turn list with the current mark,
  keyed live updates via `mergeView`), `story.js` (turn cards, steps, tool and effect lines,
  between blocks), `side-card.js` (tabs), `dialog.js` (one dialog shape, Back stack, Show in
  turn), `fmt.js` (shared formatting and the `button()` helper). Rendering only: every
  string, count and time comes from the view. The Commands card clamps the longest command
  to one line and carries the whole command in `title`.
- The page loads `/view` once; the drawer loads the timeline entries only when a call opens.
- Removed: `timeline-page.js`; `model.js` turn, rail, strip, context and stats builders;
  `effects.js` placement; `tool-card.js`'s tool row (its copy button, cut link and chip stay
  for the drawer); the turn filters in `filters.js` (the sessions filter stays); the key
  bar from the session level. Kept: `drawer*.js` (013 drawer, until 017), `api.js`,
  `route.js`, `theme.js`, `sessions-page.js`.

### Seed (`docs/manual-checks/seed-session.mjs`)
One session reproducing the mockup dataset end to end: a stub upstream answering the
scripted model calls (thinking on two, tool_use, a 429 quota probe, a title call, a sub-agent
thread, a 500 for the errored-call case), hooks posted as Claude Code would (pre/post, a
failed Edit, an Agent with its sub-agent's tools, a hooks-only turn), real file changes in a
scratch git repo (an Edit, a Bash writing 3 files, two parallel Bash, your own edit between
turns), and connections through the proxy (a WebFetch host, a harness host during a long
Bash, one unassigned). It prints the session link and the expected line for every tool,
effect and between block. `--fast` shrinks the 2-minute Bash to 5 s.

### Tests (functional, few)
- `internal/timeline/view_test.go`: one fixture session from the seed's shapes; asserts turn
  count, every turn's clock/model/tool/wait, each tool line, each effect's placement (under
  tool, several after step, loose with during, between), titles for the three no-prompt
  cases, unknown client lines, searches apart from reads, context deltas.
- `internal/ui/api_view_test.go`: view JSON for a stored session; live `view` event sends
  only changed turns.
- `internal/timeline/view_test.go` also pins the CLI text (`TestView_TimeLineText`);
  `cmd/gateway/viewer*_test.go` keep every assertion and gain `-entries` where they check
  the flat output (research 2026-11-11).
- `internal/ui/webtest/view.test.js`: the page renders a view fixture without computing (no
  call to removed builders), the turn list marks the clicked last turn, a live event
  replaces only the changed turn, 2,000 calls render under budget, the dialog's hook input;
  the drawer's tests are hosted by `webtest/drawer-host.js`.
- `internal/ui/webtest/contrast.test.js`: its pairs move to the new tokens (text, muted,
  accent, ok, bad, warn, add, del on bg, card and panel, both themes), all ≥ 4.5:1.
- Retired with the code they test (research Decisions for Karthik 15, as answered): `turns`,
  `strip`, `stats`, `context`, `keybar`, `filters`, `tool-card` and the placement half of
  `effects`. Each one is removed in the same commit as the code it covered and listed in
  research.md with the Go test that now covers the same fact or the AC that drops the
  feature; no test is retired for any other reason.

## Risks
- **View cost at 2,000 calls** (constraint 1.2×): measured in T006 before the web work.
- **Live diffing by turn hash**: a late hook can change an old turn; hashes catch it.
- **Profile line counts** for MultiEdit and NotebookEdit: approximate; counts come from the
  effect's diff when a change exists (the tool line shows the effect's counts then).
- **CLI output change**: scripts reading the text output break; `-entries` keeps it (Q2).

## As built (2026-11-13)
Differences from the plan as approved (D14), each folded into the sections above:
- T003–T005 landed as one commit (`0d5024d`): the builder's parts share one fixture.
- `core.ToolSummary` gained `Path`; `core.PromptReader` and `timeline.SideCallNamer` are new
  optional interfaces; the call facts moved to `timeline.CallFacts`.
- Live updates use a view mode of the session stream (`?view=1`) with `order` and `full`
  instead of `turn_count`, debounced 150 ms.
- The view's JSON omits empty fields (it measured 1.22× cold before, ≈1.0× after).
- `Between.Title`; the Time card's fourth part, between turns (`time.between_ms`).
- The page's drawer loads entries lazily; the 013 stats and strip builders went in T009,
  not T010 (their only caller was the 013 page); `.btn` styles were added in T009.
- The Files tab words each attribution as the story does (label glyph and word, then the
  tool); the Commands card clamps to one line.
- The tools endpoint gained `hook_omitted_bytes`.
- AC15 amended: the CLI's `-json` matches `/view` in every field except `version`.
- After Karthik's review (2026-11-13, research.md): the story scrolls inside its own card
  (`.storycol`); a turn's steps sit on a stepper, folded behind one line each, with the
  open folds kept in the page across live redraws and opened by `place()` through
  `story.js` `reveal()`; `Turn.Models` and the header's model tag; Files rows are the name
  and line counts only.
