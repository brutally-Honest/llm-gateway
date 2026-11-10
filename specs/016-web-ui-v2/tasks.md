---
feature: 016-web-ui-v2
status: draft
version: 1
approved:
approved-by:
sha256:
fingerprint:
plan-fingerprint:
previous:
---
# Tasks — 016-web-ui-v2

Built outside the agent-loop kit (research.md D1): one branch, feature tasks first, the seed
early, functional tests together near the end, `make verify` green after every commit, a
git note per commit, leaks patched as found and logged as dated lines in research.md.

### T001 — Add the tool summary interface and Claude Code summaries
- Do: `internal/core/tools.go` (`ToolKind`, `ToolSummary`, `ToolSummarizer`);
  `internal/clients/claudecode/summary.go` (the table in plan.md); compile-time check;
  `docs/decisions/0016-server-built-session-view.md` (A1 vs A2/A3, lean view, interface).
- Tests: none new — covered by T012
- AC: AC5 (summaries)
- Commit: feat(claude-code): summarize tool calls in the profile
- Depends: —
- Size: S
- Risk: low

### T002 — Seed one session covering every mockup case
- Do: `docs/manual-checks/seed-session.mjs` (plan.md Seed): stub upstream, scripted calls,
  hooks, file changes in a scratch repo, proxy connections, one unassigned connection,
  `--fast`; prints the session link and every expected tool line, effect placement, between
  block, title and context row.
- Tests: none — a seed script; run once against the current build (the old page shows it)
- AC: AC1–AC15 (manual)
- Commit: docs(docs): seed one session with every 016 case
- Depends: —
- Size: M
- Risk: medium (timing of hooks against checkpoints and proxy spans must give the labels
  the mockup shows)

### T003 — Build turns, steps and tool lines on the server
- Do: `internal/timeline/view.go`: `View`, `Turn`, `Step`, `ToolLine`; turns by the 013 rule,
  side calls folded, hooks-only turns, steps with sub-agent nesting, tool lines with state
  word, summary, line counts, run time (hook span or `≈` gap), first error line; clock, model,
  tool and waiting times (D5, Q1); reminders split; thinking flagged, never inlined.
- Tests: none new — covered by T012
- AC: AC4, AC5, AC7
- Commit: feat(ui): build session turns and tool lines on the server
- Depends: T001
- Size: L
- Risk: medium (time rules on real hook timing)

### T004 — Place effects and build the session lists
- Do: `internal/timeline/view_place.go`: effects under their tool line, several-running after
  the latest candidate's step with short names (D6, D8), loose group with during hints (D7),
  between blocks (D9), relative paths (Decisions for Karthik 4), turn counts;
  `view_lists.go`: Files (changes by path, reads, searches apart — D11), Commands, Sites,
  Context per thread with deltas and reasons (D12).
- Tests: none new — covered by T012
- AC: AC6, AC8, AC9, AC10
- Commit: feat(ui): place effects and list files commands and sites
- Depends: T003
- Size: L
- Risk: medium

### T005 — Build the overview and titles
- Do: `internal/timeline/view.go`: title rule (D10), status, range, folder, cards, needs-a-look
  set (Decisions for Karthik 5), tokens, side-call list with status words.
- Tests: none new — covered by T012
- AC: AC2, AC3 (counts)
- Commit: feat(ui): build the session overview and readable titles
- Depends: T004
- Size: M
- Risk: low

### T006 — Serve the view and stream its changes
- Do: `internal/ui/api_view.go` (`GET /api/sessions/{id}/view`, `GET
  /api/sessions/{id}/tools/{tid}`), memo by (timeline version, effect version); hub `view`
  event with changed turns by hash; measure the view against `/timeline` on the 2,000-call
  seed before and after, figures in research.md (Q3).
- Tests: none new — covered by T013
- AC: AC1, AC12
- Commit: feat(ui): serve the session view and stream its changes
- Depends: T005
- Size: M
- Risk: high (cost budget 1.2×, live correctness)

### T007 — Print the view from gateway timeline
- Do: `internal/timeline/render_view.go` (text), `-json` = view JSON, `-entries` keeps
  today's flat output (Q2); update the pinned outputs in `render_test.go` and
  `cmd/gateway/viewer*_test.go` in the open, each change listed in research.md Notes (D13).
- Tests: the pinned outputs above, updated
- AC: AC15
- Commit: feat(server): print the session view in gateway timeline
- Depends: T005
- Size: M
- Risk: medium (scripts reading the old text)

### T008 — Restyle the UI and lay out the session page
- Do: `style.css` tokens, type, cards, badges and app shell for every page (Decisions for
  Karthik 9); `session-page.js` (three columns, only the story scrolls, narrow fallback),
  `turn-list.js` (clamped prompts, counts, between entries, current mark, last turns
  selectable), `overview.js`; `j`/`k`, Esc and tab arrow keys (Q4); key bar removed from the
  session level; `contrast.test.js` moved to the new tokens.
- Tests: `contrast.test.js` (updated pairs)
- AC: AC2, AC3, AC13, AC14
- Commit: feat(ui): lay out the session page in the new theme
- Depends: T006
- Size: L
- Risk: medium

### T009 — Render the story from the view
- Do: `story.js`: turn cards (prompt heading, reminders toggle, times, counts), step lines
  (`#n` opens the 013 drawer), tool lines, sub-agent blocks, effect lines with label badges,
  several and loose groups, between blocks, hooks-only turns; remove `buildTurns`,
  `placeEffects`, `tool-card.js` and the filter (Q5) with their tests (Decisions for
  Karthik 15).
- Tests: none new — covered by T014
- AC: AC4–AC8
- Commit: feat(ui): render the session story from the server view
- Depends: T008
- Size: L
- Risk: medium

### T010 — Add the files commands sites and context tabs
- Do: `side-card.js`: four tabs with counts and coverage lines, rows per file, command,
  site, context call; the unassigned link; bars by turn; rows open the dialog (or the 013
  drawer for calls); remove strip, context line and stats builders with their tests.
- Tests: none new — covered by T014
- AC: AC9, AC10
- Commit: feat(ui): add the files commands sites and context tabs
- Depends: T009
- Size: M
- Risk: low

### T011 — Open details in one dialog shape
- Do: `dialog.js`: file, command or tool, and site dialogs (facts, why in words, diff,
  content, output or input, hook lines, related links with Back, copy buttons, Show in turn
  N with highlight); focus return; live updates keep an open dialog on its item.
- Tests: none new — covered by T014
- AC: AC11, AC12, AC13
- Commit: feat(ui): open session details in one dialog shape
- Depends: T010
- Size: M
- Risk: low

### T012 — Test the view builder
- Do: `internal/timeline/view_test.go` on a fixture shaped like the seed (plan.md Tests).
- Tests: `TestView_Turns`, `TestView_Times`, `TestView_Placement`, `TestView_Lists`,
  `TestView_Titles`, `TestView_UnknownClient`
- AC: AC1–AC10
- Commit: test(ui): cover the server-built session view
- Depends: T005
- Size: M
- Risk: low

### T013 — Test the view API and live event
- Do: `internal/ui/api_view_test.go`.
- Tests: `TestViewAPI_ServesView`, `TestViewAPI_ToolDetails`, `TestViewStream_SendsChangedTurns`
- AC: AC1, AC12
- Commit: test(ui): cover the view endpoint and live view event
- Depends: T006
- Size: S
- Risk: low

### T014 — Test the page renders without computing
- Do: `internal/ui/webtest/view.test.js` on a view fixture.
- Tests: renders turns, tabs and dialogs from the fixture; the turn list marks a clicked last
  turn; no removed builder is imported
- AC: AC1, AC3, AC11
- Commit: test(ui): cover rendering the session view
- Depends: T011
- Size: S
- Risk: low

### T015 — Write the 016 manual check
- Do: `docs/manual-checks/016-session-page.md`: throwaway gateway, `seed-session.mjs`, steps
  per AC (layout and scroll, overview and needs-a-look, turn list, each turn against the
  seed's printed lines, tabs, dialogs, live with `--live`, keys, light and dark), the
  contrast check (Lighthouse accessibility audit and the page snippet in both themes, every
  pair ≥ 4.5:1), `gateway timeline` text and `-json` against the view.
- Tests: none — a manual check document
- AC: AC1–AC15 (manual)
- Manual: docs/manual-checks/016-session-page.md
- Commit: docs(docs): add the 016 session page manual check
- Depends: T014
- Size: S
- Risk: low

### T016 — Run the 016 manual check
- Do: `/ui-check 016-session-page` while Karthik watches; record expected vs seen and the
  contrast figures in research.md; fix what fails in its own commits before the check is
  recorded.
- Tests: none — manual
- AC: AC1–AC15
- Manual: docs/manual-checks/016-session-page.md
- Commit: docs(docs): record the 016 manual check
- Depends: T015
- Size: S
- Risk: medium

## AC coverage
| AC | Tasks |
|---|---|
| AC1 | T006, T012, T013, T014, T016 |
| AC2 | T005, T008, T012, T016 |
| AC3 | T005, T008, T014, T016 |
| AC4 | T003, T009, T012, T016 |
| AC5 | T001, T003, T009, T012, T016 |
| AC6 | T004, T009, T012, T016 |
| AC7 | T003, T009, T012, T016 |
| AC8 | T004, T009, T012, T016 |
| AC9 | T004, T010, T012, T016 |
| AC10 | T004, T010, T012, T016 |
| AC11 | T011, T014, T016 |
| AC12 | T006, T011, T013, T016 |
| AC13 | T008, T011, T016 |
| AC14 | T008, T016 |
| AC15 | T007, T016 |
