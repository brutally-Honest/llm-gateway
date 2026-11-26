---
feature: 046-ui-polish
kind: fix
created: 2026-11-26
branch: fix/046-ui-polish
status: done
---

This spec covers what was planned as 039 (UI polish). It is built here as 046.

## Problem
Small things make the UI harder to read or trust:
- **U1** (audit UX-3) Turn titles show raw wrappers: pasted-content tags, "Tool loaded.",
  skill bodies, messages from other sessions. In the largest session of the store copy 7 of
  28 turns are titled "Tool loaded.".
- **U2** (audit UX-7) The sessions-list token cell leaves out cache-write tokens, often the
  biggest cost line.
- **U3** (042 follow-up) When the gateway stops mid-request the UI shows the browser's raw
  "Failed to execute 'json' on 'Response'…" or "Failed to fetch".
- **U4** (043 D22) Rows change height when their details fill in; some shrink by 7-26 px.
- **U5** The installer says the UI "opens once its start-up warm-up ends, up to about 30 s on
  a large store". Since 030 the UI serves as soon as it binds; 043 made rows come first.
- **U6** (audit UX-1) The list is sorted by last activity but its first column shows Start.
- **U7** (audit UX-5) Opening a session that does not exist leaves the rail skeleton spinning.
- **U8** (audit UX-6) A filter with no results says only "no sessions match".
- **U9** (audit UX-11) A lost live connection shows only as a small dot.
- **U10** (audit UX-12) Tool input shows escaped `\n` instead of line breaks.

## Goal
Each item fixed as planned below, in both styles (Lean, Stark) and both themes.

Planned change per item (checked against the code at c2eab80):
- **U1** The client profile (`internal/clients/claudecode`) gets one title rule,
  `PromptTitle(prompt) (title, synthetic)`, reached by the timeline through an optional
  interface (as `SideCallNamer`). Each turn gets a `title` (rail and session-page turn list
  use it); `prompt` stays the full text, so the story still shows every word. The session
  title (overview and list digest) takes the first turn or prompt whose title is not
  synthetic, else the first synthetic one's title, else today's fallback. The rule per
  pattern is research D1. Turn boundaries do not change (D3).
- **U2** `sessions-page.js` `rowCells`/`fillRow`: a fourth figure, cache write, in the token
  cell and its bar, beside input, output and cache read.
- **U3** `api.js` `getJSON` and `getBody`: a network failure, or an answer whose body is not
  JSON, becomes an Error "The gateway is not answering." with `code: "unreachable"`; the
  sessions list keeps its Retry button for it.
- **U4** `style.css`: a pending row reserves one line per heavy cell at every width (the
  shortest filled row), so a row only grows when its details arrive.
- **U5** `internal/install/install.go` prints the UI address without the warm-up claim;
  doctor's fix texts and `docs/install.md` troubleshooting drop the "about 30 s" claim.
- **U6** The first column is "Last active" and shows `last_at`; its tooltip gives the start.
- **U7** A 404 on `sessions/<id>/view` shows "Session not found" with a link back to the list,
  and the rail's skeleton goes.
- **U8** An empty filtered list says which filter (the kind's label and/or the search text)
  matched nothing, with a Clear button that resets both.
- **U9** While the live stream is down the header shows a short visible "reconnecting…" next
  to the dot; it goes on reconnect.
- **U10** The tool-input block in the dialog shows string values that hold line breaks as real
  lines; Copy still copies the JSON.

## Non-goals
- Anything else from the audit's UX list.
- Layout redesign.
- Changing where turns start or end (a "Tool loaded." turn is still a turn).

## Acceptance criteria
- **AC1** Every pattern of research D1 gets its title in the list, the session page heading
  and the rail; a real prompt's title is its text with only the wrappers removed. Go tests
  use fictional examples of each pattern.
- **AC2** The token cell shows input, output, cache write and cache read (webtest).
- **AC3** A network failure or non-JSON answer shows "The gateway is not answering." and the
  list's Retry works (webtests).
- **AC4** A pending row's reserved height is never more than a filled row's (layout test).
- **AC5** No doc or message claims a start-up warm-up of about 30 s (installer test).
- **AC6** The first column's label matches the sort (webtest).
- **AC7** A missing session shows "Session not found" with a link back, no spinner (webtest).
- **AC8** An empty filtered list names the filter and Clear resets it (webtest).
- **AC9** "reconnecting…" shows while the stream is down and goes on reconnect (webtest).
- **AC10** Multi-line tool input shows real line breaks (webtest).
- **AC11** UI check on a store copy in Lean and Stark, light and dark, at 1,440 and 2,560 px:
  `docs/manual-checks/046-ui-polish.md`, fictional examples only.

## Edge cases
- A wrapper nested in another (pasted content inside a mid-turn message) is unwrapped in turn.
- A prompt that only starts like a synthetic one is still matched on its fixed opening; "Tool
  loaded." matches only as the whole text.
- A slash command's title keeps its arguments but drops the skill body that follows.
- A session whose every prompt is synthetic is titled by the first one, not "No prompt".
- An unclosed pasted-content tag is removed to the end of its line, keeping the text.
- A 404 that is not a missing session (an unknown call) keeps today's message.
