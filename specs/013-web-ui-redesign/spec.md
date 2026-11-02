---
feature: 013-web-ui-redesign
kind: feat
status: approved
version: 1
created: 2026-11-02
supersedes:
branch: feat/013-web-ui-redesign
approved: 2026-11-02T12:31:37Z
approved-by: human
sha256: 44305f289184d700a767c03a1036555033abaf829c0b4bae300d45ce192c4533
fingerprint: 5c8ccf9abea20e2cea6fb75e71dc6bb5cb3fa777528b3ea6b27288ae8b3b9aeb
---
# Web UI redesign: sessions-first, turn timeline, exchange drawer

## Problem
The web UI (009) shows a session as one long flat page: prompts, full system text, hook lines
and tool cards are all at the same level, so it takes scrolling to see what happened in a turn.
Exchange detail is a separate page, so opening a call loses your place in the timeline. Token
totals sit in one string, nothing shows how the context grows from call to call, and nothing
shows which messages a call added. Theme follows the OS with no manual choice.

Reference mockup: `gateway-ui-v4.html`, approved 2026-11-02 (held outside this repo, at
`~/Downloads/gateway-ui-v4.html`). Where the mockup and this spec differ, this spec wins.

This changes earlier features:
- **009 AC37** (sessions table): new columns; live updates and "Load more" stay.
- **009 AC39** (turns start at a user prompt): replaced by AC7 here.
- **009 AC40, AC41** (tool cards, model-call chip): replaced by the turn rows; full input and
  result stay reachable (AC14), and the chip's facts move to the row, stats and drawer.
- **009 AC49** (theme follows the OS): replaced by AC30.
- **009 non-goal "charts beyond the token chip"**: replaced by the strip and context line.
- **010 AC24** (same state words as the CLI): the short chip is shown and the full state, reason
  and outcome stay on hover and in the drawer. CLI output does not change.
- Kept as they are: 009 AC38 (`#/ungrouped`), AC42 to AC45, AC47 (`#/exchanges/{id}`), AC48,
  AC50 and 010 AC25.

## Goal
- Sessions is the one landing page, with each session's capture sources, tool states, errors and
  four token counters visible at a glance, plus totals for today.
- A session reads as turns, each one call away from a drawer that shows what the model saw, what
  it answered, what ran, and how big the context was, without leaving the timeline.
- Everything can be done by mouse or by keyboard, in a light or dark theme you choose.

## Non-goals
- Cost or money of any kind; Phase 9.
- "% of context window used" or any context-window limit meter.
- OpenTelemetry ingestion.
- Redesigning the Ungrouped page.
- File-system or network visibility; Phase C.
- Cross-session search: filters cover loaded rows only (AC6).
- Following the OS theme.
- A working directory or path in the session header or list.
- Rate-limit utilisation in the drawer's health section.
- A "Copy as curl" action.
- Nesting sub-agent calls in their own card inside a turn.
- Changing capture or hook storage formats for the UI.
- Updating `gateway sessions` or other CLI output to use the side-call rule; a follow-up.
- Mobile layout (still usable from 1024 px wide upward only, as in 009).
- A JS framework, npm, a bundler or a build step.

## Acceptance criteria
Terms used below. A **call** is a model call; `#n` is its position among the session's calls in
start order, counting every thread. A call's **thread** comes from the timeline's existing match:
the earlier call sharing its longest leading run of message hashes is its **previous call**, and a
call with no such match starts a new thread. The **main thread** is
every thread with a user prompt, unless it is a side call or hooks or a parent Agent call identify it as a sub-agent, so a thread started by compaction or `/clear` stays main. A call the profile does not recognise follows this rule. **Context** of a call is its input + cache write + cache read tokens.
A **reminder** is a `<system-reminder>` block, recognised by the Claude Code client profile.
A **side call** is a call the Claude Code client profile recognises as the client's own: the quota probe (a single user message that is exactly `quota`) or title generation (a user message starting with `<session>` and containing the instruction `Write the title`).
The **chip** of a tool call is: `asked+ran+reported: succeeded` → `✓ ok`; `asked+ran+reported:
failed` → `✗ failed`; `asked-not-run` → `⊘ not run`; `ran-not-asked-or-not-captured` → `? unmatched`;
`pending` and `ran-not-reported` → `… pending`; `no harness data` → `– no hooks`.

### Sessions list
- **AC1** — When the user opens the UI, the system shall show the Sessions list as the landing page with, per session: start time, title, capture sources (API / hooks), calls, tool states as chips with counts, errors excluding side calls, a status (active, ended or hooks only), and input, output, cache-write and cache-read tokens from `usage_total`, showing "—" for a counter when no usage was captured, never 0.
- **AC2** — When any view is shown, the header shall show a breadcrumb starting at `Sessions`, followed by `/ <session title>` inside a session, in place of the Sessions and Ungrouped links, and the `Sessions` link shall return to the list.
- **AC3** — When a session's title is shown, it shall be the latest stored generated title from the session's title-generation side calls; when there is none or the call failed, it shall be the session's first user prompt that is not in a side call, with reminders removed; either is cut to the existing preview length, and an open list row and session header shall update live when a title arrives.
- **AC4** — When the Sessions list is shown, the system shall show totals for today over all stored sessions, loaded or not: sessions with activity today, plus calls and the four token counters counted only from calls started today in browser-local time.
- **AC5** — When ungrouped exchanges exist, the Sessions list shall show a quiet "N ungrouped exchanges" link under the table that opens `#/ungrouped`, which stays reachable and unchanged.
- **AC6** — When the user types in the filter or picks All, Has errors, Has gaps or Hooks only, the list shall show only the loaded rows that match; Has errors shall match a session with an error outside its side calls; Has gaps shall match a session with a tool call in the `? unmatched` state or a call with missing or partial usage; and while more rows exist, the list shall show "filtering N loaded" with Load more.

### Session timeline
- **AC7** — When the user opens a session, the system shall show its calls in one card per turn, where a turn starts at a user prompt in the main thread, and a call from another thread shall appear in the turn open when it started, without starting a turn.
- **AC8** — When a turn card is shown, it shall show the prompt, each tool call with its chip, the assistant text, and for each call a link labelled `#n · duration`, with the turn's counts and total duration in its heading.
- **AC9** — When a session is open, a turn rail shall list every turn with its prompt, tool chips, call count and duration, and selecting one shall scroll to that turn and mark it current.
- **AC10** — When a session is open, a stats row shall show model calls, model time, tool-state chips, errors, the last call's context, and input and output tokens; its errors exclude side calls and its context is the last main-thread call's, matching the Errors column and the context line.
- **AC11** — When a session is open, a strip shall show one bar per call, sized by duration and marked by turn, with status, duration and tool on hover, side calls drawn muted and named as side calls on hover, and activating a bar shall open that call's drawer.
- **AC12** — When a session is open, a context line shall show each main-thread call's context as a trend across the session, leaving out side calls and sub-agent calls, and each call's row shall still show its context size.
- **AC13** — When a tool call is shown, it shall show its chip as glyph plus text, with the full state, reason and outcome on hover.
- **AC14** — When a tool row is shown, it shall offer its full pretty-printed input and result on expansion, a "truncated — open raw" link when cut, and copy buttons on prompts, tool input and tool results; the text filter and tool-name picker shall filter the loaded turns.
- **AC15** — When a turn has hook events, they shall stay hidden behind an "N hooks" toggle until it is used, and the toggle shall show them with their agent labels.
- **AC16** — When the user changes a rail Show option, the timeline shall apply it at once: Hooks inline shows all turns' hooks, System reminders shows reminders in prompts (otherwise folded behind a "+N reminders" control), Context per call shows or hides the context sizes on rows, and Only turns with errors hides the other turns.
- **AC17** — When a call or tool has a sub-agent id or type, or an exchange errored, the timeline shall keep showing the agent label and the red errored marking with an error word or glyph, and the header shall keep the live indicator.
- **AC18** — When a session has hook events but no calls, each `prompt` hook shall start a turn card and hook-seen tools shall show as rows with chips, with no `#n` links or drawer and a note that no model calls were captured.
- **AC19** — When an active session receives new data, the timeline, rail, stats, strip, context line and chips shall update without a reload, and the page shall scroll to new entries only when the drawer is closed and the view is already at the bottom.

### Exchange drawer
- **AC20** — When the user opens a call by `#n`, a strip bar, or Enter on a turn (its first call), the system shall open a right-side drawer over the timeline without leaving the session.
- **AC21** — When the drawer shows a call with tool use, it shall show a tool chain of asked → ran → reported with each step's state in glyph and text, including an agent label where known.
- **AC22** — When the drawer is open, it shall show the call's context size, its input, cache-write and cache-read parts, the change since its previous call, and the session's context trend, which plots main-thread calls as in AC12, with this call marked when it is one of them.
- **AC23** — When the drawer is open, "What the model saw" shall list the request's messages, marked as carried over or new since the previous call, with the count of each, and a call starting a new thread shall be marked as such.
- **AC24** — When the drawer is open, it shall show the response assembled from the stored stream, with output tokens and stop reason, and a health section with gateway disconnect, abort and error facts and the session id.
- **AC25** — When the drawer is open, it shall link to the raw request, the raw SSE or response body, and all headers, and to the call's full page at `#/exchanges/{id}`.
- **AC26** — While the drawer is open, ↑ and ↓ shall step to the previous and next call in session order, and Esc or the close control shall close it and return focus to where it was opened from.
- **AC27** — When a drawer opens, the URL shall change to name the session and call, so reloading restores that drawer and Back closes it, and `#/exchanges/{id}` shall keep working as a full page.
- **AC28** — While the drawer is open and live data arrives, the drawer shall stay on its call and keep focus and scroll position, while the content behind it, the position and ↓ pick up new calls, and a pending chip updates in place.
- **AC29** — When a drawer section lacks data (body not stored, capture truncated, parse failed, or no usage), that section shall state the reason and show "—" for numbers, and the other sections and raw links that exist shall still work.

### Theme, keys and access
- **AC30** — When the user picks Light or Dark in the header, the system shall apply that theme immediately, store it in localStorage, and use it on the next load whatever the OS theme is, with dark as the default.
- **AC31** — While a view is shown, a bottom key bar shall show the level (SESSIONS, TURNS or CALL), the position (such as `session 1/3`, `turn 2/3`, `call #3/4`), and only the shortcuts that work there: SESSIONS ↑/↓ select, Enter open, / filter; TURNS j/k turn, e next error, t next tool, Enter open first call, h hooks, / filter, Backspace to Sessions; CALL ↑/↓ call, Esc close, t back to turn.
- **AC32** — While focus is in a text input, the system shall ignore these shortcuts, and Esc shall leave the input.
- **AC33** — When any action is offered, it shall have both a mouse and a keyboard path through real `<button>` or `<a>` elements, with aria labels on icon-only controls.
- **AC34** — When text is shown in either theme, it shall have at least 4.5:1 contrast with its background, and no status shall rely on colour alone.
- **AC35** — When any view is shown, it shall show no cost or money and no percentage of a context window.
- **AC36** — When a session or list is opened, it shall show all stored history available through its existing paging, not only live data.

### Side calls
- **AC37** — When a session holds side calls, they shall never start a turn and their prompts shall never become the session title: those before the first main-thread prompt shall appear in the unnumbered "before first prompt" card, and later ones in an unnumbered "side calls" card at their place in the timeline, with neither card counted as a turn.
- **AC38** — When calls or tokens are counted in the Sessions list, today's totals or the stats row, side calls shall count like any other call, and each shall keep its `#n` link and drawer.
- **AC39** — When a side call errored, it shall be left out of the Errors column and the Has errors filter, and shall still show red with an error word or glyph in its card and drawer.
- **AC40** — When the API returns an exchange, it shall state whether it is a side call (for example a `side_call` field), classified on the server by the Claude Code profile, and the browser shall use that value and never classify a call from its message text.

## Edge cases
- **E1** — Empty store → the Sessions list shows its empty note, today's totals are 0, the key bar still shows (AC1, AC4, AC31).
- **E2** — A session with no generated title and no user prompt outside side calls → its title reads "no prompt captured" (AC3).
- **E3** — A prompt made only of reminders → the title falls to the next prompt with text, or E2 (AC3).
- **E4** — A call with no usage, such as a 429 → red with its status, context "—", no change shown; it is never counted as 0, and the next call's change is measured from its previous call that has usage (AC12, AC22, AC29).
- **E5** — A call with any of the three context counters unreported → context "—", flagged partial when some were reported (AC12, AC22).
- **E6** — The first call of a thread → "first call" in place of a change, every message marked new (AC22, AC23).
- **E7** — A sub-agent call between two main calls → it stays in the open turn with its label, and the next main call compares with the prior main call, not the sub-agent's (AC7, AC23).
- **E8** — Calls before the first main-thread prompt that are not side calls → shown in the same unnumbered "before first prompt" card, not counted as a turn (AC7, AC37).
- **E9** — First or last call → the matching step does nothing and its button is disabled (AC26).
- **E10** — A drawer URL naming a call not in the session → the session opens with a "no such call" note and no drawer (AC27).
- **E11** — The element that opened the drawer is gone when it closes → focus goes to the current turn's heading (AC26).
- **E12** — A filter that matches nothing → "no sessions match", plus Load more while more rows exist (AC6).
- **E13** — A strip with more bars than fit → bars keep a minimum width and the strip scrolls sideways; every call stays reachable (AC11).
- **E14** — A `ran-not-reported` call in an ended session stays `… pending`, and the drawer's chain shows ran with reported missing (AC13, AC21).
- **E15** — localStorage unavailable or holding an unknown value → the theme still applies for this page load, defaulting to dark, with no error (AC30).
- **E16** — A session stream drops while the drawer is open → the drawer and history stay, and the header shows the existing disconnected text (AC17, AC28).
- **E18** — A user's own first prompt that is exactly `quota`, alone in its request → recognised as a side call by the rule above; it does not start a turn or become the title, and the call is still shown (AC37).
- **E19** — A session holding only side calls → title per AC3 or E2, one "before first prompt" card, no turns (AC3, AC37).
- **E20** — A client with no side-call or reminder recognition → every thread follows the main-thread rule and prompts show whole (AC3, AC7).
- **E21** — A title call whose response is empty, unparseable or errored → it is ignored, and an earlier stored title or the first prompt is used (AC3).
- **E22** — A session with no main-thread calls → the context line is replaced by a note that there are none to plot (AC12).
- **E23** — `gateway sessions` and other CLI output stay as they are, so the CLI's error count can be higher than the Errors column by the session's side-call errors (AC39).
- **E17** — `#/ungrouped` and `#/exchanges/{id}` → the key bar shows level SESSIONS with no position and only Backspace to Sessions, and the header's breadcrumb and theme picker still apply (AC2, AC30, AC31).

## Constraints
- Vanilla JS, ES modules, no dependency; pure logic is tested with the Node runner from ADR 0013.
- A session of 2,000 calls shall render within 2 s of its data arriving, with no cap on calls shown.
- Uses stored data only: timeline, usage totals, stored bodies and SSE. New read-only API fields or routes are allowed; capture and hook storage formats do not change.
- Captured text is rendered as text, never HTML (009 AC35). The page's Content-Security-Policy and `TestWeb_NoInlineOrExternalReferences` stay as they are, so the mockup's inline `<style>`, `style=` attributes and `<script>` must move to embedded files.
- Read-only and loopback-only, as in 009; the UI cannot block or slow proxying or capture.
- `docs/manual-checks/010-hooks-ui.md` and `docs/clients/claude-code-hooks.md` describe the old badge text and need updating with this change.

## Agnosticism check
- **Client:** reminder and side-call recognition live in the Claude Code profile (`internal/clients/claudecode`) as an optional capability, so a client without them shows prompts whole and has no side calls. The classification runs on the server and reaches the browser through the API, so the CLI can adopt the same rule later without duplicating it. An unknown client's sessions still list and open, labelled `unknown`.
- **Protocol / provider:** carried-over and new messages, the assembled response and usage come from canonical events and their content hashes. The browser never parses a provider's request or SSE format; raw bodies are only linked.
- **Core:** `internal/core/` is unchanged. Resolves no OQ-n.

## Open questions
None.

## Changelog
- v1 (2026-11-02) — first draft
