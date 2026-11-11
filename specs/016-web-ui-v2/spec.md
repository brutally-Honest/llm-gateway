---
feature: 016-web-ui-v2
kind: feat
status: draft
version: 2
created: 2026-11-10
supersedes:
branch: feat/016-web-ui-v2
approved:
approved-by:
sha256:
fingerprint:
---
# Web UI v2: the session page

## Problem
The session page (013, with effects from 014 and labels from 015) answers "what happened in
this session" only after work. Tool rows show a tool name and a chip, not what the tool did
(`npm test`, `src/app.js +3 −1`). Effects sit in a folded row at the bottom of each turn,
away from the tool call that caused them. A turn's duration is the sum of its model calls,
not the time from the prompt to the answer, and tool time is not shown at all. The stats row
mixes unlabelled numbers ("2 side"), side calls are red when the quota probe answers 429,
"[reasoning]" and JSON title lines appear inline, the rail shows whole prompts, and a session
with no prompt shows its raw key. There is no single place that lists every file changed,
every command run, every site contacted or how the context grew. And the browser builds all
of it from flat entries, so the CLI and the page can disagree.

Reference mockup: `specs/016-web-ui-v2/mockups/v4-story.html` (chosen 2026-11-10). Where the
mockup and this spec differ, this spec wins.

This changes earlier features (each AC listed is replaced or narrowed as said):
- **013 AC7, AC8** (turn cards, `#n · duration` links): replaced by AC4–AC7 here. The rule
  for what starts a turn stays; the server applies it.
- **013 AC9** (rail): replaced by AC3.
- **013 AC10** (stats row): replaced by AC2.
- **013 AC11** (call strip) and **AC12** (context line): replaced by the Context tab, AC10.
- **013 AC13** (chip as glyph + text): kept, now on the tool line (AC5); full state, reason
  and outcome move to the tool details (AC11).
- **013 AC14** (expansion, copy buttons, text filter, tool picker): input and result move to
  the tool details (AC11), copy buttons with them; filter and picker are dropped (research
  Q5).
- **013 AC15, AC16** ("N hooks" toggle, rail Show options): replaced; hook lines move to the
  tool details (AC11), reminders behind a toggle under the prompt (AC4), Show options removed.
- **013 AC17, AC18** (agent label and error marking; hooks-only sessions): kept, restated in
  AC5 and AC7.
- **013 AC19** (live updates): replaced by AC12.
- **013 AC30, AC34** (theme, contrast): replaced by AC14. **AC31, AC32** (key bar, shortcuts):
  replaced by AC13 (research Q4). **AC33, AC35, AC36**: kept.
- **013 AC37, AC39** (side-call cards, side errors red): replaced by AC2: side calls fold
  behind one quiet control and are never red by default.
- **014 AC14** (one folded effects row per turn): replaced by AC6, AC7, AC8, AC9. **014
  AC15**: its coverage line moves to AC9; the sessions-list part moves to 017.
- **015 AC12** (label and tool text appended to a row): replaced by AC6.
- **014 follow-up** (research.md 2026-11-09, effect counts in the turn header and rail):
  folded in as AC3 and AC4.
- Not changed here, moved to **017**: 013 AC1–AC6 (sessions list), AC20–AC29 (drawer), AC38,
  AC40. Until 017 ships, a model call's `#n` opens the 013 drawer as today.

## Goal
- Opening a session tells the reader, without hunting, what was asked, what ran, what changed
  on disk and on the network, what failed and what deserves a second look.
- Every fact on the page is computed once on the server and shown as given; the `gateway
  timeline` CLI prints the same facts.
- One layout: turns on the left, the story in the middle, the session's files, commands,
  sites and context on the right, one shape of details dialog, in a neutral theme that
  passes 4.5:1 contrast in both modes.

## Non-goals
- The sessions list, the call details that replace the drawer, the ungrouped and
  unassigned pages (017).
- New capture: no new hook, effect or proxy data; 016 reads what 013–015 store.
- Filters and search over turns (dropped from 013 AC14, research Q5 A).
- Cost or money, and any percentage of a context window (013 AC35 stands).
- Editing, replaying or exporting a session.
- Other clients' tool summaries: the seam is there, only Claude Code fills it.

## Acceptance criteria
### Server-built view
- **AC1** — When `GET /api/sessions/{id}/view` is requested, the system shall return the
  session's view computed on the server: its turns with their steps, tool lines, effects and
  durations, its between-turn blocks, its overview, its file, command, site and context
  lists, and its title; the browser shall render only what the view holds and compute no
  turn, duration, label, placement, path or title itself.

### Overview and turn list
- **AC2** — When a session is open, the overview shall show the title, status, time range,
  folder, model and client, then five labelled cards (Time with model and tool parts, Files
  changed with lines added and removed and files read, Commands, Sites, Context), a "Needs a
  look" list (failed tool calls, errored model calls outside side calls, changes outside any
  tool call, effects not pinned to one tool, hooks-only turns, tool calls in the `?
  unmatched` state), each item opening its details or showing its place, a token line, and a
  quiet "N side calls" control that lists them without counting them as errors or turns and
  without red; a session with no captured prompt shall get a readable title, never its key.
- **AC3** — When a session is open, the turn list shall show every turn with its prompt
  clamped to two lines (full text on hover and focus), its duration or "hooks only", its
  files-changed count and failure count, plus one entry per between-turn block; selecting an
  entry shall scroll the story to it and mark it current, scrolling shall move the mark to
  the turn in view, and the last turns shall be selectable even when they cannot reach the
  top of the story.

### The story
- **AC4** — When a turn is shown, it shall show the prompt as its heading (reminders behind a
  "+N reminders" toggle), its start time, its clock time from the prompt to the final answer
  split into model, tool and waiting time, and its counts (tool calls, files changed, files
  read, searches, sites, failures); a turn with no final answer shall show its end state as
  glyph and word: running (session active), interrupted, or ended by error.
- **AC5** — When a tool call is shown, it shall be one line holding its state as glyph and
  word (`✓` alone for ok), its name, the profile's summary of its input (`npm test`,
  `src/app.js +3 −1`, `pdf`) and its run time, with a failed call's first error line under
  it; a sub-agent's model calls and tool calls shall sit under the tool call that spawned it,
  and a client the profile does not know shall get the tool name alone.
- **AC6** — When an effect has a tool call it is labelled for (`named`, `only one running`),
  it shall sit under that tool line, showing its label as glyph and word before the change,
  the path relative to the session's folder (full path on hover) and its size or line
  counts; `several running` effects shall be listed once, after the tool calls they name,
  with each candidate named by its tool and the first words of its input; `harness` and
  `outside` effects in a turn shall sit in a "not caused by a tool call" group with a
  "during #n Tool" hint when a tool was running.
- **AC7** — When a turn's model calls are shown, each main-thread call shall be one step line
  (`#n`, what it did and for how long, how many tools it ran at once), an errored call shall
  show its status as a word in red, thinking shall never appear inline but behind a "Read the
  thinking" control, the turn shall end with the final answer, and a hooks-only turn shall
  say "no model calls captured" and show its tools without `#n`.
- **AC8** — When a file change is labelled `outside` and its checkpoint window starts before
  the next turn's prompt, it shall be shown in its own block between those two turns, worded
  "seen between turns N and N+1", and not inside either turn.

### Side card and details
- **AC9** — When a session is open, the right card shall offer Files, Commands, Sites and
  Context tabs with their counts: Files lists each changed file once (all its changes, lines
  added and removed, the turn, who changed it in words) then the files read and the
  searches; Commands lists every shell command in order with its state, turn, run time and
  what it changed; Sites lists each host once with its connections, bytes, turns and the
  tools that made them, and names an unassigned connection seen while the session ran with a
  link to the unassigned list; each tab states what it covers and what is not seen (014's
  coverage line, split by tab).
- **AC10** — When the Context tab is shown, it shall show the last main-thread call's
  context, the first call's, the growth, a bar per main-thread call grouped by turn, and one
  row per call with its context and what it added since the previous call of its thread
  (the reply, tool results by name, the next prompt), sub-agent threads apart and side calls
  left out; a call with unreported usage shows "—", never 0.
- **AC11** — When a file, command or other tool call, or site is opened from the story, the
  right card or the overview, the system shall show one details dialog with labelled facts,
  why the effect is attributed in words, the diff, content, output or input in full width,
  the hook lines of a tool call with its state, reason and outcome, links to related files,
  sites and calls (with Back), and "Show in turn N", which closes the dialog, scrolls to the
  row and highlights it.

### Live, keys, theme, CLI
- **AC12** — When an open session receives new calls, hooks or effects, the turn list, turns,
  overview, tabs and counts shall update without a reload, an open dialog shall stay on its
  item, and the story shall keep its scroll position unless it was at the bottom.
- **AC13** — When any action is offered, it shall be a real button or link reachable by
  Tab and run by Enter, tabs shall move with arrow keys, Esc shall close a dialog and return focus to
  what opened it, and `j`/`k` shall step through turns while focus is not in a text field.
- **AC14** — When any page of the UI is shown, it shall use the neutral theme in dark by
  default with a stored light choice, show every status as glyph and word, and keep every
  text and its background at 4.5:1 contrast or more in both themes, checked with a contrast
  tool during the browser check.
- **AC15** — When `gateway timeline` prints a session, it shall print the server's view: one
  block per turn with its prompt, durations, step lines, tool lines and effects with labels,
  the between-turn blocks, side calls folded into one line, and with `-json` the same view
  object the API returns.

## Edge cases
- **E1** — A turn still running (no final answer yet, session active) → clock time runs to
  the last activity seen and the turn shows "running" as glyph and word (AC4, research Q1).
- **E2** — A turn interrupted (Esc) or ended by an errored call → clock time ends at the last
  call or tool end; the turn shows "interrupted" or "ended by error" as glyph and word; the
  errored call is red with its status (AC4, AC7).
- **E3** — A tool call with no hook events (hooks off) → run time from the gap between the
  call that asked for it and the next call of its thread, shown with `≈`; no hook lines in
  its details (AC5, AC11).
- **E4** — A tool call in `⊘ not run`, `? unmatched`, `… pending` or `– no hooks` → that
  glyph and word on the line; `? unmatched` also in "Needs a look" (AC2, AC5).
- **E5** — An Agent tool call → its span is not tool time (its sub-agent's calls count as
  model time) and it is never an effect's candidate (015 AC4) (AC4, AC6).
- **E6** — Two parallel calls asked by different model calls are both candidates → the
  several-running group follows the later of the two calls (AC6).
- **E7** — A file outside the session's folder → shown with its absolute path, home folder as
  `~` (AC6, AC9).
- **E8** — An `outside` change whose window starts after the turn's prompt → stays in the
  turn's "not caused by a tool call" group (AC6, AC8).
- **E9** — A session with only side calls → title "No prompt · <folder>", no turns, the side
  calls control and an empty-state line (AC2).
- **E10** — A hooks-only session with no folder known → title "Hooks only · <client> ·
  <start time>" (AC2).
- **E11** — A session holding only 013-era data (no effects, no hooks) → turns, tools and
  context show; the Files, Commands and Sites tabs say none were seen (AC9, AC10).
- **E12** — An unknown client → tool lines show the name only, no `named`/`harness` labels
  (015 AC10), every other part shows (AC5).
- **E13** — A binary or over-limit file → "binary" or its size, no diff, the dialog says why
  (AC9, AC11).
- **E14** — Thinking that was redacted or empty → "Read the thinking" is not offered (AC7).
- **E15** — A session of 2,000 calls → the view loads within the store timeout and the page
  stays responsive; long lists scroll inside their card (AC1, AC12).

## Constraints
- The view is built from the same snapshot as the timeline (013) plus the session's effects
  and their labels (015); the effect version and `SessionVersions` keep their meanings.
- At the 2,000-call seed the view request answers within the 5 s store timeout and no slower
  than 1.2× today's `GET /api/sessions/{id}/timeline` for the same session, measured both
  cold (fresh gateway, no cache) and warm, before and after, figures in research.md
  (research Q3).
- A 2,000-call session renders in the browser within 2 s of its view data arriving (013's
  constraint kept), figure in research.md.
- No new Go or JS dependency; the contrast check uses a tool that needs no install (Chrome
  DevTools or a snippet run in the page).
- The browser keeps no copy of any rule: no tool names, no label logic, no time arithmetic
  beyond formatting.
- Capture, hook ingest and the effects runner are untouched.

## Agnosticism check
- Client axis: tool summaries (which input field is the command, path, pattern, URL or skill,
  and line counts for edit tools) live in `internal/clients/claudecode/` behind a new
  optional profile interface in `internal/core/`; a client without it gets tool names only.
  The no-prompt title wording and the side-call rules already live in the profile (013).
- `internal/core/` gains that interface and a neutral summary type naming no client or tool.
  The view builder (`internal/timeline/`) reads canonical entries, hook rows and effects only.
- Protocol and provider axes: untouched. ADR 0016 records the server-built view and the
  interface.

## Open questions
- None. research.md Q1–Q5 answered 2026-11-11 and folded in (AC4, AC13, E1, E2,
  Constraints).

## Changelog
- v1 (2026-11-10) — first draft from the chosen mockup
- v2 (2026-11-11) — Q1–Q5 answers folded in: turn end states (AC4, E1, E2), Tab/Enter
  (AC13), cold and warm measures and the 2 s render budget (Constraints); approved
