---
feature: 017-sessions-list-and-call-details
kind: feat
status: draft
version: 1
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

## Goal
- The sessions list says, per session and in labelled words, what it did (prompts, calls,
  tool calls, files changed, commands, sites), whether it needs a look, and how long it ran.
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
- **AC3** — When the sessions list is shown, a "Today" line shall show labelled totals over
  all stored sessions (sessions, model calls with side calls included, files changed, sites,
  input and output tokens), computed on the server.
- **AC4** — When the user types in the filter or picks All, Needs a look, Active or Hooks
  only, the list shall show the matching loaded rows, say "filtering N loaded" while more
  rows exist, and keep Load more.

### Call details dialog
- **AC5** — When a model call is opened (from a step's `#n`, a Context row or bar, a tool's
  "asked by", or a URL naming the call), the system shall show it in 016's dialog shape
  without leaving the session, and the URL shall name the session and call so a reload
  restores it and Back closes it.
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

### Other pages
- **AC12** — When the ungrouped exchanges page, the unassigned connections page or the full
  exchange page is shown, it shall use 016's theme, labelled counts and glyph-and-word
  statuses, with the breadcrumb back to Sessions.

## Edge cases
- **E1** — Empty store → the list's empty note, today's totals 0, no unassigned section (AC1,
  AC3).
- **E2** — A side call opened → its kind (quota probe, title generation) as the thread, its
  status not red (AC6).
- **E3** — A URL naming a call not in the session → the session opens with a "no such call"
  note and no dialog (AC5).
- **E4** — The first or last call → Previous or Next disabled (AC10).
- **E5** — A hooks-only session → no call to open; the list row shows "no model calls
  captured" in place of call counts (AC1).
- **E6** — A filter matching nothing → "no sessions match", Load more kept while more rows
  exist (AC4).

## Constraints
- Every count, title and "needs a look" kind comes from the server; the browser formats only.
- The sessions list answers within today's budget for a store of 500 sessions (measured in
  017's research.md before and after).
- No new dependency.

## Agnosticism check
- Client axis: side-call kinds and titles come from the profile (013, 016); nothing new.
- `internal/core/` unchanged. Protocol and provider axes untouched.

## Open questions
- [NEEDS CLARIFICATION: plan and tasks are drafted once 016 is built (016 research,
  Decisions for Karthik 13)]

## Changelog
- v1 (2026-11-10) — first draft, split from 016 (016 research D3)
