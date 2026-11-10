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
agent-loop kit (research.md D1).

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
  content hash changed; the browser swaps them by turn number (Decisions for Karthik 11).

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
A1. Flip to paging turns (Q3 B) if the 2,000-call view breaks the 1.2× budget.

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
// added and removed (-1 when not known).
type ToolSummary struct {
    Kind           ToolKind
    Arg            string // "npm test", "src/app.js" (absolute; made relative by the view)
    Added, Removed int
}

// ToolSummarizer is an optional profile capability.
type ToolSummarizer interface {
    SummarizeTool(name string, input []byte, workDir string) ToolSummary
}
```
ADR 0016 records the server-built view, `ToolSummarizer` and the lean-view rule.

### Claude Code (`internal/clients/claudecode/summary.go`)
Bash → command (whole `command`); Read, Write, Edit, MultiEdit, NotebookEdit → file
(`file_path`, line counts from `old_string`/`new_string` or `content`); Grep → search
(`“pattern” in path`); Glob → search (pattern); WebFetch → fetch (URL without scheme);
WebSearch → fetch (query); Skill → skill (`skill`); Agent → agent (`subagent_type ·
description`); anything else → other (name only). Candidate short names (D8) are made in
the view from `Arg`: first three words, trailing flags dropped.

### View (`internal/timeline/view.go`, `view_place.go`, `view_lists.go`)
```go
type View struct {
    Title       Title        // text and source: generated, prompt, hooks_only, no_prompt
    Overview    Overview     // status, range, folder, model, client, cards, needs_look, tokens
    SideCalls   []SideCall   // n, kind, time, duration, status word; never errors here
    Turns       []Turn
    Between     []Between    // after turn N: effects
    Files       []FileItem   // one per changed path; reads; searches
    Commands    []ToolRef
    Sites       []SiteItem
    Context     ContextView  // per thread: rows with ctx, delta, why
    Version     string       // timeline version + effect version
}
type Turn struct {
    N, Hash        // Hash: content hash for live diffing
    Prompt, Reminders, At, Clock, Model, Tool, Wait, Running, HooksOnly
    Counts         // tools, changed, read, searched, sites, failed
    Steps   []Step // main-thread calls in order
    Loose   []EffectLine // harness / outside in the turn, with During
    Answer  string
}
type Step struct { N, ExchangeID, At, Dur, Agent, Errored, Status, HasThinking, Did string;
                   Tools []ToolLine; Several []EffectLine; SeveralNames []string }
type ToolLine struct { ID, Name, State, StateWord, Summary, Kind, Added, Removed, Dur, DurApprox,
                       FirstError, Agent; Effects []EffectLine; Sub []Step /* Agent tools */ }
type EffectLine struct { ID, Kind, Change, Path, FullPath, Size, Added, Removed, Binary,
                         Label, LabelWord, Tool, During, Search, Host, Port, Up, Down }
```
Rules, each one function with a table test:
1. **Turns**: the 013 rule (a main-thread prompt starts a turn; other threads join the open
   turn; side calls go to `SideCalls`); hooks-only sessions from prompt hooks (013 AC18).
2. **Steps**: main-thread calls; sub-agent calls nest under the Agent tool line whose
   tool-call id their thread's first prompt answers (013 thread facts); `Did` text from tool
   count and stop reason; errored = error entry or status ≥ 400.
3. **Times**: tool span from hook pair (pre → post/failed/denied receive time) else the gap
   rule (E3); clock = prompt time → end of the last main-thread call (Q1 A: last activity
   when none and `Running` while active); model = union of call spans; tool = union of
   non-agent tool spans; wait = clock − union(model ∪ tool).
4. **Placement**: an effect with `tool` → that tool line; `several` → the step of its latest
   candidate; `harness`/`outside` → `Loose` with `During` = tools whose span holds the
   effect's time; `outside` file change with `window_from` < next prompt → `Between` (D9);
   unlabelled → by time into the turn holding it (014 rule), `Loose`.
5. **Paths**: relative to the folder of the session's first hook with a working folder;
   outside it, absolute with home as `~` (home from the gateway's own `os.UserHomeDir`).
6. **Lists**: Files groups changes by path (reads and searches apart, D11); Commands = tool
   lines of kind `command`; Sites groups connections by host; Context per thread (D12).
7. **Title** (D10) and **needs-a-look** (Decisions for Karthik 5).

### API (`internal/ui/api_view.go`)
- `GET /api/sessions/{id}/view` → `View` JSON; built from the cached timeline build plus a
  fresh effects read, memoised per (timeline version, effect version) next to the timeline
  cache (`timeline_cache.go` keyed the same way).
- `GET /api/sessions/{id}/tools/{tid}` → a tool call's input, result (cut like today, with
  the raw link), hook lines with state, reason and outcome.
- The existing `/timeline`, `/effects`, `/exchanges` endpoints stay (drawer, exchange page,
  017).

### Live (`internal/ui/hub.go`, `stream.go`)
After the hub's existing rebuild trigger (exchange, hook or effects event), it builds the
view once per session per tick and sends `event: view` with `{version, overview, side_calls,
between, files, commands, sites, context, turns: [changed turns only], turn_count}`. A new
subscriber gets the whole view first. The old exchange and effects events stay for the
drawer until 017.

### CLI (`cmd/gateway/viewer.go`, `internal/timeline/render_view.go`)
Text: header line; per turn `Turn N · 10:08:40 · 32s (model 30s · tools 0.4s · waiting
1.3s)`, the prompt, step lines (`#10 thought and wrote 9.2s, ran 1:`), indented tool lines
(`✗ failed Edit src/payment/retry.js · 0.1s`), effect lines (`● named ~ modified
src/payment/retry.js +6 −2`), between blocks, one `side calls: #1 quota probe 429, #3 title
generation` line. `-json` = the API's view JSON. Old flat output behind `-entries` (Q2 A).

### Web (`internal/ui/web/`)
- `style.css`: the mockup's tokens, type, cards, badges, app shell; every page takes the
  tokens (Decisions for Karthik 9).
- New `session-page.js` (layout, live), `turn-list.js`, `story.js` (turn cards, steps, tool
  and effect lines, between blocks), `side-card.js` (tabs), `dialog.js` (one dialog shape,
  Back stack, Show in turn), `overview.js`. Rendering only: every string, count and time
  comes from the view.
- Removed: `model.js` turn, rail, strip, context and stats builders; `effects.js`
  placement; `tool-card.js`; the key bar from the session level. Kept: `drawer*.js` (013
  drawer, until 017), `api.js`, `route.js`, `theme.js`, `sessions-page.js`.

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
- `cmd/gateway/viewer_test.go`: text and `-json` output pinned for the fixture; `-entries`
  keeps today's output.
- `internal/ui/webtest/view.test.js`: the page renders a view fixture without computing (no
  call to removed builders), the turn list marks the clicked last turn.
- `internal/ui/webtest/contrast.test.js`: its pairs move to the new tokens (text, muted,
  accent, ok, bad, warn, add, del on bg, card and panel, both themes), all ≥ 4.5:1.
- Retired with the code they test (research Decisions for Karthik 15): `turns`, `strip`,
  `stats`, `context`, `keybar`, `filters`, `tool-card` and the placement half of `effects`.

## Risks
- **View cost at 2,000 calls** (constraint 1.2×): measured in T006 before the web work.
- **Live diffing by turn hash**: a late hook can change an old turn; hashes catch it.
- **Profile line counts** for MultiEdit and NotebookEdit: approximate; counts come from the
  effect's diff when a change exists (the tool line shows the effect's counts then).
- **CLI output change**: scripts reading the text output break; `-entries` keeps it (Q2).
