---
status: approved
date: 2026-11-11
---

# 0016 — the session page is a view built on the server

## Context
specs/016 redesigns the session page around a story: turns with their steps, tool lines
that say what each tool did (`npm test`, `src/app.js +3 −1`), effects under the tool call
that caused them, between-turn blocks, and Files, Commands, Sites and Context lists. Until
016 the server sent flat timeline entries and the browser built turns, durations, the rail,
effect placement and the context line itself (`model.js`, `effects.js`), while the
`gateway timeline` CLI printed its own reading of the same entries. The two could disagree,
and the new rules (turn clock time, tool spans, between-turn placement, tool summaries) would
have to be written twice. A tool summary needs to know which input field is a command or a
path, which is client knowledge.

## Decision
- One pure builder, `timeline.BuildView`, turns one session's timeline build, call facts,
  hook rows and labelled effects into a `View`. The UI serves it at
  `GET /api/sessions/{id}/view` and streams changed turns; `gateway timeline` prints it and
  `-json` prints the same object. The browser renders what the view holds and computes no
  turn, duration, label, placement, path or title.
- The view is lean: lines, counts, times and ids. Tool inputs, results, diffs and thinking
  are fetched when a dialog opens (`/sessions/{id}/tools/{tid}`, `/effects/{id}/diff`,
  `/exchanges/{id}`).
- Tool summaries come from a new optional profile interface, `core.ToolSummarizer`, which
  returns a neutral `core.ToolSummary` (kind, argument, path, lines added and removed). The
  view makes paths relative to the session's folder; a client without the interface gets
  tool names alone.

## Alternatives
- **Keep flat entries; the browser builds the view.** Rejected: every rule is written in JS
  and again in Go for the CLI, and the profile's tool knowledge would leak into the browser.
- **Server renders HTML.** Rejected: live updates and dialogs become HTML diffing, and the
  CLI still needs a data form.
- **A config table of tool names and fields.** Rejected: it duplicates the tool knowledge
  015 already keeps in the profile.

## Consequences
- The page and the CLI cannot disagree; the rules are Go functions with table tests.
- The view must be memoised per (timeline version, effect version) and diffed by turn to
  stay cheap on a 2,000-call session; the cost is measured against `/timeline` in
  specs/016 research.md.
- The 013 drawer still reads flat entries until 017 replaces it; `/timeline` stays.
- **Revisit** (page turns, spec Q3 B) if the 2,000-call view breaks the 1.2× budget.
