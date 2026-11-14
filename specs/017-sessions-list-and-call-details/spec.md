---
feature: 017-sessions-list-and-call-details
kind: feat
status: draft
version: 7
created: 2026-11-10
supersedes:
branch: feat/017-sessions-list-and-call-details
approved:
approved-by:
sha256:
fingerprint:
---
# Sessions list and call details

## Problem
016 rebuilds the session page around a server-built view and one details dialog, but two
parts of the UI stay on 013's design: the sessions list, whose rows show unlabelled counts,
raw keys for sessions with no prompt and no effect counts in the same terms as the session
page; and the exchange drawer, which slides over the timeline in a different shape from
every other details view, mixes context strata, message lists and raw links without a
summary of what the call did, and is the only place a model call's thinking, tokens and
context growth can be read.

Reference mockup: `specs/016-web-ui-v2/mockups/sessions.html` and the model-call dialog in
`mockups/v4-story.html`. Where they and this spec differ, this spec wins. Depends on 016
(the view endpoint, the dialog shape, the theme).

This changes earlier features:
- **013 AC1, AC4, AC6** (sessions columns, today's totals, filters): replaced by AC1–AC4.
- **013 AC2, AC3, AC5** (breadcrumb, title rule, ungrouped link): kept; the title rule is
  016's (research D10).
- **013 AC20–AC29** (exchange drawer): replaced by AC5–AC11 (the call details dialog).
- **013 AC38, AC40** (side calls counted, server classifies side calls): kept.
- **014 AC15** (sessions-list effect counts, unassigned page link, coverage line): replaced
  by AC2 and AC12.
- **016 AC7, AC10** (`#n` and Context rows open the 013 drawer until 017): now open the call
  details dialog (AC5).
- **016 AC4** (the prompt as the turn's heading; the counts): the prompt is a labelled block
  like the answer, and the counts are chips that open panels (AC13–AC16).
- **016 AC7** (step lines always shown): the steps move behind the tool-calls chip (AC14).
- **016 AC3** (room under the last turn) and **016 AC10** (a bar per call): changed by AC22
  and AC20.
- **016 AC9** (Sites lists connections, bytes, turns and tools per host): the Sites tab
  lists hosts only; the rest stays in the site's dialog (AC17).

## Goal
- The sessions list says, per session and in labelled words, what it did (prompts, calls,
  tool calls, files changed, commands, sites), whether it needs a look, and how long it ran.
- A turn reads as prompt → chips → final answer: prompt and answer rendered as Markdown by
  default with the raw text one click away, and the steps, files and sites behind chips.
- A model call opens in the same dialog shape as files, commands and sites, and answers what
  the call saw, what it said, what it cost in tokens and how the context grew.

## Non-goals
- The session page itself (016).
- Cost or money; percentages of a context window.
- Search across sessions' content; deleting or exporting sessions.
- New capture data.

## Acceptance criteria
### Sessions list
- **AC1** — When the sessions list is shown, each row shall show the start time, the title
  (016's rule, so never a session key; the key on hover), the folder and model, the capture
  sources as glyph and word, a status (active, ended, hooks only), and labelled counts:
  prompts, model calls with side calls apart, tool calls with failures, files changed,
  commands, sites, wall-clock time, and input, output and cache-read tokens ("—", never 0,
  when no usage was captured).
- **AC2** — When the sessions list is shown, a row with anything in its "Needs a look" set
  (016) shall say how many items and of what kinds as words, and the list shall show the
  unassigned connections in their own section with each one's label, client, host, bytes
  and time.
- **AC3** — When the sessions list is shown, a "Today" line shall show labelled totals since
  local midnight (sessions, model calls with side calls included, files changed, sites,
  input and output tokens), computed on the server (amended 2026-11-14, research D17).
- **AC4** — When the user types in the filter or picks All, Needs a look, Active or Hooks
  only, the list shall show the matching loaded rows, say "filtering N loaded" while more
  rows exist, and keep Load more.

### Call details dialog
- **AC5** — When a model call is opened (from a step's `#n`, a Context row, a tool's
  "asked by", or a URL naming the call), the system shall show it in 016's dialog shape
  without leaving the session, and the URL shall name the session and call so a reload
  restores it and Back closes it. A Context bar keeps AC20's behaviour and opens no call
  (amended 2026-11-14, research D18).
- **AC6** — When the call details are shown, they shall start with labelled facts: model,
  duration, start, stop reason or error status as a word, thread (main, sub-agent name, side
  call kind) and context size.
- **AC7** — When the call details are shown, a Context section shall show the call's
  context, its change since the previous call of its thread, and what was added in words
  (016 AC10's rows), with the input, cache-write and cache-read parts.
- **AC8** — When the call details are shown, they shall show what the call saw (its
  messages, marked carried over or new since the previous call, with counts; a thread's
  first call marked as such), its thinking behind a toggle, its response, and the tool calls
  it asked for, each opening its 016 tool dialog.
- **AC9** — When the call details are shown, a Health section shall show disconnect, abort,
  error and partial-capture facts in words, and a Raw section shall link the raw request,
  response stream and headers and the full exchange page.
- **AC10** — While the call details are open, ↑ and ↓ (and Previous and Next buttons) shall
  step through the session's calls in order, disabled at the ends, and live updates shall
  keep the dialog on its call.
- **AC11** — When a section of the call details lacks data (body not stored, capture cut,
  parse failed, no usage), it shall say why and show "—" for numbers, while the other
  sections and raw links still work.

### Turn card (changes 016)
- **AC13** — When a turn's prompt or final answer is shown, it shall be a labelled block
  ("Prompt", "Final answer") with a Preview / Raw switch, Preview by default: Preview renders
  the text as Markdown (headings, emphasis, inline code, code blocks, lists, links, quotes,
  tables) and Raw shows the stored text as is. The last choice is remembered in the browser.
- **AC14** — When a turn is shown, it shall show the prompt, then its chips, then the final
  answer, with no steps in between. The chips are "All", then the turn's counts (model calls,
  tool calls, files changed, files read, searches, sites, failures); clicking "All" shall mark
  it selected and open the turn's whole stepper in order as 016 AC5–AC8 show it (each step's
  tool calls folded, the effects no tool call caused after the steps), or the hooks-only tool
  list for a hooks-only turn (amended 2026-11-13).
- **AC15** — When any other chip is clicked, it shall be marked selected and open the same
  timeline narrowed to its kind, in the order things happened, each row carrying the `#n` of
  its call: model calls (one line each: what it did, how long, how many tool calls); tool
  calls (state, name, summary, lines, run time, first error); files changed, files read or
  sites (each change, read or connection with the tool that caused it, or "several running",
  or "not caused by a tool call"); searches; failures (the failed tool calls). Each row opens
  its 016 details dialog, or the call details dialog (AC5) for a call (amended 2026-11-13,
  2026-11-14 research D19).
- **AC16** — When a chip is clicked, at most one chip's panel per turn shall be open: another
  chip closes it, clicking the selected chip closes it, and every chip is a button reachable
  by Tab with its open state announced (016 AC13). "Show in turn" opens "All".
- **AC17** — When the Sites tab is shown, each host shall be one row with its name alone
  (`host:port` when the port is not 443), like the Files tab's rows; connections, bytes,
  turns and tools stay in the site's dialog (016 AC11).
- **AC18** — When the prompt and the final answer are shown, they shall use the same body
  text size and weight, neither as a heading, and each shall be shown whole, never folded
  (amended 2026-11-13).
- **AC19** — When a side-card tab is hovered, focused or selected, its highlight shall keep
  space on both sides of the label and count, so the label never touches its edges.
- **AC20** — When the Context tab is shown, its chart shall be one bar per turn: the context
  at the turn's last main-thread call over the peak, with a darker cap for what the turn
  added since its first call, labelled by turn (every k-th past 24 turns, the first and
  last always); clicking a bar shall bring that turn's calls into view in the list
  (changes 016 AC10's bar per call; added 2026-11-13).
- **AC21** — While the call details dialog (AC5) is open, the page behind it shall be dimmed
  and inert (no click or Tab reaches it), a click on the dimmed page shall close the dialog,
  and the dialog's top (title, context, message counts) shall stay while the messages and
  later sections scroll (added 2026-11-13 for the 013 drawer; amended 2026-11-14 for the
  dialog that replaces it, research D19).
- **AC22** — When the turn list is hovered or focused, an entry shall keep its two-line
  prompt, with no tooltip; the story shall end at its last turn, with no empty space after
  it (changes 016 AC3; added 2026-11-13).
- **AC23** — When a tool call that asked the user questions is opened, and the client's
  profile can read them, its dialog shall show each question with its header, its options
  with the picked one marked, a typed answer or "No option picked", and the user's notes,
  before the raw result and input; its line shall say how many questions and their headers
  (added 2026-11-13).
- **AC24** — Until the sessions list's first page arrives, the list shall show a loader, not
  "No sessions captured yet" (added 2026-11-13).

### Other pages
- **AC12** — When the ungrouped exchanges page, the unassigned connections page or the full
  exchange page is shown, it shall use 016's theme, labelled counts and glyph-and-word
  statuses, with the breadcrumb back to Sessions.

## Edge cases
- **E1** — Empty store → the list's empty note, today's totals 0, no unassigned section (AC1,
  AC2, AC3).
- **E2** — A side call opened → its kind (quota probe, title generation) as the thread, its
  status not red (AC6).
- **E3** — A URL naming a call not in the session → the session opens with a "no such call"
  note and no dialog (AC5).
- **E4** — The first or last call → Previous or Next disabled (AC10).
- **E5** — A hooks-only session → no call to open; the list row shows "no model calls
  captured" in place of call counts (AC1).
- **E7** — A prompt or answer holding HTML or `<script>` → shown as text in both modes,
  never run or rendered as HTML; a link only works for `http`/`https` (AC13).
- **E8** — Markdown the renderer does not know → shown as plain text, nothing dropped (AC13).
- **E9** — A turn with no files changed or no sites → no such chip (AC14, AC15).
- **E10** — Live updates while a chip's panel is open → the panel stays open and updates
  (AC14, 016 AC12).
- **E6** — A filter matching nothing → "no sessions match", Load more kept while more rows
  exist (AC4).

## Constraints
- Every count, title and "needs a look" kind comes from the server; the browser formats only.
- The sessions list answers within today's budget for a store of 500 sessions (measured in
  017's research.md before and after).
- No new dependency: the Markdown renderer is the UI's own code (research D3).

## Agnosticism check
- Client axis: side-call kinds and titles come from the profile (013, 016); nothing new.
- `internal/core/` unchanged. Protocol and provider axes untouched.

## Open questions
- None. Plan and tasks waited for 016 (research D2); 016 is merged and tasks.md holds the
  remaining work (research D15, D20).

## Changelog
- v1 (2026-11-10) — first draft, split from 016 (016 research D3)
- v2 (2026-11-13) — turn card and side card changes from the 016 review: Markdown
  preview, chips that open panels, Sites tab hosts only, prompt and answer type, tab
  padding (AC13–AC19, E7–E10; research D3–D6)
- v3 (2026-11-13) — a model-calls chip opens the steps; the failures, files-read and
  searches chips open panels (AC14, AC15; research D7, D8)
- v4 (2026-11-13) — an "All" chip opens the whole stepper; the other chips narrow it to their
  kind in order; prompt and answer shown whole (AC14–AC16, AC18; research D10, D11)
- v5 (2026-11-13) — Context chart one bar per turn, modal drawer with a fixed top, turn list
  without hover expansion or tooltip, no space after the last turn (AC20–AC22; research D13)
- v6 (2026-11-13) — questions and answers in a tool's dialog, a loader on the sessions list,
  a visual cleanup of the list ahead of AC1–AC4 (AC23, AC24; research D14)
- v7 (2026-11-14) — the spec text follows the decisions taken while writing tasks.md: AC3's
  "Today" totals are since local midnight (research D17); AC5 opens a call from a Context
  row only, a bar keeps AC20 (D18); AC15 and AC21 name the call details dialog in place of
  the 013 drawer, keeping AC21's behaviour (D19); E1 also cites AC2 (no unassigned section)
