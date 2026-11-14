---
feature: 017-sessions-list-and-call-details
status: draft
version: 1
approved:
approved-by:
sha256:
fingerprint:
plan-fingerprint:
previous:
---
# Tasks — 017-sessions-list-and-call-details

Built by hand, outside the agent-loop kit (research D9, D15), like 014–016. AC13–AC24 are
built and committed (`bf4a55f`…`52bc8f0`); these tasks cover what is left, in three slices.
One commit per task, `make verify` green after every commit (Go tests run with `-race`, the
Node web-client tests with `node --test "internal/ui/webtest/*.test.js"`), a git note per
commit, and each doubt logged in research.md as a dated D-line. Server first, then the web
client, in each slice. D12 (side calls shown as turns) is out of scope (research D16).

Tests name the file and the test to add (new ones are marked "new"). UI check names the step
in `docs/manual-checks/017-sessions-and-calls.md` that shows the task by eye; the slice's
last task runs that section with `/ui-check 017-sessions-and-calls` while Karthik watches.

## Slice 1 — Sessions list (AC1–AC4)

Edge cases: E1 (AC1, AC3), E5 (AC1), E6 (AC4).

### T001 — Measure the sessions list against the budget before the change
- Do: seed 500 sessions (`docs/manual-checks/seed-calls.mjs` in a loop, or a Go seed helper
  in the bench); time `/api/sessions` first page and `/api/today` (median of 5);
  record the figures in research.md (spec Constraints: "measured before and after").
- Tests: Go `-race` — `internal/ui/timeline_bench_test.go`: new `BenchmarkSessionsList500`
- AC: Constraints (budget)
- UI check: none — a measurement
- Commit: test(ui): benchmark the sessions list at 500 sessions
- Depends: —
- Size: S
- Risk: low

### T002 — Write the slice 1 manual check
- Do: `docs/manual-checks/017-sessions-and-calls.md` §1: throwaway gateway, `seed-session.mjs`
  plus one hooks-only session, one session with a failed tool call, one unassigned
  connection; steps 1.1 row counts, 1.2 hooks-only row, 1.3 needs a look, 1.4 unassigned
  section, 1.5 today line, 1.6 empty store, 1.7 filters and Load more, 1.8 filter matching
  nothing; light and dark.
- Tests: none — a manual check document
- AC: AC1–AC4, E1, E5, E6 (manual)
- UI check: the document itself
- Manual: docs/manual-checks/017-sessions-and-calls.md
- Commit: docs(docs): add the 017 sessions list manual check
- Depends: —
- Size: S
- Risk: low

### T003 — Serve each session row's labelled counts
- Do: `internal/ui/api_sessions.go` `sessionSummary`: prompts, model calls with side calls
  apart, tool calls with failures, files changed, commands, sites, wall-clock ms, folder,
  status word (active, ended, hooks only), input/output/cache-read with "no usage" kept
  distinct from 0; counts from the timeline (`SessionStats` / the view builder's counters),
  never from the browser.
  - AC1 → **E5**: a hooks-only session has no call counts and says "no model calls captured".
- Tests: Go `-race` — `internal/ui/api_sessions_test.go`: new `TestSessions_RowCounts`,
  `TestSessions_HooksOnlyRowHasNoCallCounts`, `TestSessions_NoUsageIsNotZero`
- AC: AC1, E5
- UI check: 017-sessions-and-calls §1.1, §1.2
- Commit: feat(ui): serve labelled counts for each session row
- Depends: T001
- Size: M
- Risk: medium (the per-row cost must stay inside the T001 budget)

### T004 — Serve each row's needs-a-look summary and the unassigned connections
- Do: `sessionSummary` gains the "Needs a look" kinds and count from
  `timeline` `needsLook` (016's fixed set); `/api/effects/unassigned` rows carry label,
  client, host, bytes and time for the list's own section.
- Tests: Go `-race` — `internal/ui/api_sessions_test.go`: new `TestSessions_NeedsLookKinds`;
  `internal/ui/api_effects_test.go`: new `TestUnassigned_RowFields`
- AC: AC2
- UI check: 017-sessions-and-calls §1.3, §1.4
- Commit: feat(ui): serve needs-a-look kinds for each session row
- Depends: T003
- Size: M
- Risk: medium (needsLook builds a view; it may need a cheaper path for the list)

### T005 — Serve today's labelled totals
- Do: `internal/ui/api_today.go` `todayJSON`: files changed, sites, model calls with side
  calls included, input and output tokens, since the given local midnight (research D17).
  - AC3 → **E1**: an empty store gives all totals 0.
- Tests: Go `-race` — `internal/ui/api_today_test.go`: new `TestToday_FilesAndSites`,
  existing `TestToday_EmptyStoreZeros` extended to the new fields
- AC: AC3, E1
- UI check: 017-sessions-and-calls §1.5, §1.6
- Commit: feat(ui): serve files changed and sites in today's totals
- Depends: —
- Size: S
- Risk: low

### T006 — Show labelled counts in each sessions row
- Do: `internal/ui/web/sessions-page.js` `rowCells`/`fillRow`: start time, title (key on
  hover only), folder and model, capture sources as glyph and word, status, the T003 counts
  as words ("—" for no usage); formatting only.
  - AC1 → **E5**: hooks-only row shows "no model calls captured".
  - AC1 → **E1**: empty store shows the list's empty note (after AC24's loader).
- Tests: Node — `internal/ui/webtest/sessions-page.test.js`: new `rowShowsLabelledCounts`,
  `hooksOnlyRowSaysNoModelCalls`, `emptyStoreShowsEmptyNote`
- AC: AC1, E1, E5
- UI check: 017-sessions-and-calls §1.1, §1.2, §1.6
- Commit: feat(ui): show labelled counts in the sessions list
- Depends: T003
- Size: M
- Risk: low

### T007 — Show needs a look and the unassigned connections section
- Do: `sessions-page.js`: a row's needs-a-look as "N items: kinds" in words; an
  "Unassigned connections" section listing label, client, host, bytes, time, hidden when
  there are none.
  - AC2 → **E1**: no unassigned section on an empty store.
- Tests: Node — `sessions-page.test.js`: new `rowSaysWhatNeedsALook`,
  `unassignedSectionListsConnections`, `noUnassignedSectionWhenNone`
- AC: AC2, E1
- UI check: 017-sessions-and-calls §1.3, §1.4, §1.6
- Commit: feat(ui): show needs a look and unassigned connections in the list
- Depends: T004, T006
- Size: M
- Risk: low

### T008 — Show today's line from the server totals
- Do: `sessions-page.js` `todayCells`: sessions, model calls (side calls included), files
  changed, sites, input and output tokens, labelled; values from T005 only.
  - AC3 → **E1**: zeros on an empty store.
- Tests: Node — `sessions-page.test.js`: new `todayLineShowsServerTotals`,
  `todayLineZerosOnEmptyStore`
- AC: AC3, E1
- UI check: 017-sessions-and-calls §1.5, §1.6
- Commit: feat(ui): show today's totals in labelled words
- Depends: T005, T006
- Size: S
- Risk: low

### T009 — Filter the loaded rows
- Do: `sessions-page.js` (+ `filters.js` if shared): text filter and All / Needs a look /
  Active / Hooks only over loaded rows, using the server's flags; "filtering N loaded"
  while `next_after` is set; Load more kept.
  - AC4 → **E6**: nothing matches → "no sessions match", Load more still shown while more
    rows exist.
- Tests: Node — `sessions-page.test.js`: new `filterShowsMatchingLoadedRows`,
  `filterSaysFilteringNLoaded`, `filterMatchingNothingKeepsLoadMore`
- AC: AC4, E6
- UI check: 017-sessions-and-calls §1.7, §1.8
- Commit: feat(ui): filter the loaded sessions by kind and text
- Depends: T006, T007
- Size: M
- Risk: low

### T010 — Measure after and run the slice 1 manual check
- Do: rerun T001's benchmark and timings, record before/after in research.md; fix any
  regression past the budget in its own commit first. Then `/ui-check 017-sessions-and-calls`
  §1 while Karthik watches; record expected vs seen in research.md.
- Tests: Go `-race` — `BenchmarkSessionsList500`; full `make verify`
- AC: AC1–AC4, E1, E5, E6, Constraints
- UI check: 017-sessions-and-calls §1 (all steps)
- Manual: docs/manual-checks/017-sessions-and-calls.md
- Commit: docs(docs): record the 017 sessions list check and timings
- Depends: T002–T009
- Size: S
- Risk: medium

## Slice 2 — Call details dialog (AC5–AC11), replacing the 013 drawer

Edge cases: E2 (AC6), E3 (AC5), E4 (AC10). The dialog keeps AC21's promises and AC15's rows
open it in place of the drawer (research D19); a Context bar keeps AC20 (research D18).

### T011 — Write the slice 2 manual check
- Do: `docs/manual-checks/017-sessions-and-calls.md` §2: open a call from a step's `#n`, a
  Context row, a tool's "asked by", a pasted URL; 2.1 URL, reload, Back; 2.2 facts; 2.3
  side call; 2.4 context; 2.5 what it saw, thinking, response, tool calls; 2.6 health and
  raw; 2.7 ↑/↓ and the ends; 2.8 live update; 2.9 missing data; 2.10 URL naming no call;
  2.11 modal (dimmed, inert, click outside closes, fixed top).
- Tests: none — a manual check document
- AC: AC5–AC11, E2–E4 (manual)
- UI check: the document itself
- Manual: docs/manual-checks/017-sessions-and-calls.md
- Commit: docs(docs): add the 017 call details manual check
- Depends: T010
- Size: S
- Risk: low

### T012 — Build a call's facts, context and health on the server
- Do: `internal/timeline/view_call.go` (new): `CallDetails` with facts (model, duration,
  start, stop reason or error status as a word, thread: main, sub-agent name or side-call
  kind from the profile, context size), context (size, change since the previous call of
  its thread, what was added in words as 016 AC10's rows, input / cache-write / cache-read),
  health (disconnect, abort, error, partial capture in words); each section says why when
  its data is missing and numbers are "—" (nil), never 0.
  - AC6 → **E2**: a side call's thread is its kind (quota probe, title generation), status
    not marked bad.
- Tests: Go `-race` — `internal/timeline/view_test.go`: new `TestView_CallFacts`,
  `TestView_CallContextDelta`, `TestView_SideCallThread`, `TestView_CallMissingUsage`
- AC: AC6, AC7, AC9, AC11, E2
- UI check: 017-sessions-and-calls §2.2, §2.3, §2.4, §2.6, §2.9
- Commit: feat(timeline): build a call's facts context and health
- Depends: —
- Size: L
- Risk: medium (thread and side-call kind must come from the profile, not a client `if`)

### T013 — Build what a call saw and said on the server
- Do: `view_call.go`: the call's messages marked carried over or new since the previous
  call of its thread, with counts; a thread's first call marked as such; thinking flagged
  (sent, not inlined); response text; the tool calls it asked for with their tool ids;
  body not stored, capture cut or parse failed → a reason, the other sections still built.
- Tests: Go `-race` — `internal/timeline/view_test.go`: new `TestView_CallSawCarriedAndNew`,
  `TestView_FirstCallOfThread`, `TestView_CallBodyNotStored`
- AC: AC8, AC11
- UI check: 017-sessions-and-calls §2.5, §2.9
- Commit: feat(timeline): mark what each call saw as carried or new
- Depends: T012
- Size: L
- Risk: medium (large message lists: cost per call against the drawer's current load)

### T014 — Serve call details
- Do: `internal/ui/api_view.go`: `GET /api/sessions/{id}/calls/{eid}`; 404 `not_found` for a
  call not in the session; raw links (request, response stream, headers, full exchange
  page) as URLs; no credentials in any field.
- Tests: Go `-race` — `internal/ui/api_view_test.go`: new `TestCallDetails_Served`,
  `TestCallDetails_NotInSession404`, `TestCallDetails_NoAuthHeaders`
- AC: AC5, AC9, AC11, E3
- UI check: 017-sessions-and-calls §2.6, §2.10
- Commit: feat(ui): serve a call's details by session and call
- Depends: T013
- Size: M
- Risk: low

### T015 — Open a call in the dialog shape from its URL
- Do: `internal/ui/web/dialog.js` (016 shape, native `<dialog>` with `showModal()`):
  `#/sessions/{id}/call/{eid}` opens it without leaving the session; reload restores it;
  Back closes it; focus returns to the opener; the top (facts) stays while the rest scrolls;
  a click on the backdrop closes it (AC21 carried over, D19).
  - AC5 → **E3**: a URL naming a call not in the session opens the session with a
    "no such call" note and no dialog.
- Tests: Node — `internal/ui/webtest/route.test.js`: new `callRouteNamesSessionAndCall`;
  `view.test.js`: new `callDialogOpensFromUrlAndBackCloses`, `missingCallShowsNote`,
  `callDialogIsModalWithAFixedTop` (carries `drawerIsModalWithAFixedTop`'s assertions)
- AC: AC5, AC21, E3
- UI check: 017-sessions-and-calls §2.1, §2.10, §2.11
- Commit: feat(ui): open a call in the details dialog from its url
- Depends: T014
- Size: M
- Risk: medium (Back/reload against the hash router)

### T016 — Render the call details sections
- Do: `dialog.js`: facts, Context (with the parts), What the model saw (counts, carried/new,
  first-of-thread), thinking behind a toggle, response (Markdown preview as AC13), tool
  calls each opening its 016 tool dialog, Health, Raw links; "—" and the server's reasons
  where data is missing.
  - AC6 → **E2**: side call shows its kind as thread, not red.
- Tests: Node — `view.test.js`: new `callDialogShowsFactsAndContext`,
  `callDialogShowsSawThinkingResponse`, `callDialogHealthAndRaw`,
  `callDialogMissingDataSaysWhy`, `sideCallIsNotRed`
- AC: AC6, AC7, AC8, AC9, AC11, E2
- UI check: 017-sessions-and-calls §2.2–§2.6, §2.9
- Commit: feat(ui): show a call's sections in the details dialog
- Depends: T015
- Size: L
- Risk: low

### T017 — Step through calls and stay on the call during live updates
- Do: `dialog.js` + `keys.js`: ↑/↓ and Previous/Next over the session's calls in order,
  disabled at the ends; the URL replaced, not pushed, on a step; a live event redraws the
  open call, never moves it.
  - AC10 → **E4**: Previous disabled on the first call, Next on the last.
- Tests: Node — `view.test.js`: new `arrowsStepThroughCalls`, `stepsDisabledAtTheEnds`;
  `live.test.js`: new `callDialogKeepsItsCallOnLiveData` (carries
  `drawerKeepsCallFocusAndScrollOnLiveData`'s assertions)
- AC: AC10, E4
- UI check: 017-sessions-and-calls §2.7, §2.8
- Commit: feat(ui): step through calls in the details dialog
- Depends: T016
- Size: M
- Risk: low

### T018 — Open calls from every place and retire the 013 drawer
- Do: a step's `#n`, AC15's chip rows, a Context row and a tool's "asked by" open the call
  dialog; remove `drawer.js`, `drawer-sections.js`, the drawer CSS and `drawer-host.js` once
  each drawer test's assertions live on in the dialog tests (T015, T017) — no assertion is
  dropped; a Context bar still scrolls its turn's calls into view (AC20, D18).
- Tests: Node — `view.test.js`: new `everyCallLinkOpensTheDialog`,
  existing `contextChartIsOneBarPerTurn` unchanged; `drawer.test.js` moved into
  `view.test.js` and `live.test.js` assertion by assertion; `dom-api.test.js` unchanged
- AC: AC5, AC15, AC20, AC21
- UI check: 017-sessions-and-calls §2.1, §2.11
- Commit: refactor(ui): retire the call drawer for the details dialog
- Depends: T017
- Size: M
- Risk: medium (test migration must keep every assertion)

### T019 — Run the slice 2 manual check
- Do: `/ui-check 017-sessions-and-calls` §2 while Karthik watches; record expected vs seen in
  research.md; fixes in their own commits first.
- Tests: full `make verify`
- AC: AC5–AC11, E2–E4
- UI check: 017-sessions-and-calls §2 (all steps)
- Manual: docs/manual-checks/017-sessions-and-calls.md
- Commit: docs(docs): record the 017 call details check
- Depends: T011–T018
- Size: S
- Risk: medium

## Slice 3 — Other pages on 016's theme (AC12)

No edge cases belong to AC12.

### T020 — Write the slice 3 manual check
- Do: `docs/manual-checks/017-sessions-and-calls.md` §3: 3.1 ungrouped exchanges, 3.2
  unassigned connections, 3.3 full exchange page; each: theme in light and dark, labelled
  counts, glyph-and-word statuses, breadcrumb back to Sessions.
- Tests: none — a manual check document
- AC: AC12 (manual)
- UI check: the document itself
- Manual: docs/manual-checks/017-sessions-and-calls.md
- Commit: docs(docs): add the 017 other pages manual check
- Depends: T019
- Size: S
- Risk: low

### T021 — Restyle the ungrouped exchanges page
- Do: `internal/ui/web/ungrouped-list.js` + `style.css`: 016 theme, labelled counts,
  glyph-and-word statuses, breadcrumb "Sessions / Ungrouped".
- Tests: Node — `internal/ui/webtest/app.test.js`: new `ungroupedPageUsesLabelsAndBreadcrumb`
- AC: AC12
- UI check: 017-sessions-and-calls §3.1
- Commit: feat(ui): restyle the ungrouped exchanges page
- Depends: T020
- Size: S
- Risk: low

### T022 — Restyle the unassigned connections page
- Do: `internal/ui/web/effects.js` (the unassigned list) + `style.css`: same rules,
  breadcrumb "Sessions / Unassigned connections".
- Tests: Node — `internal/ui/webtest/effects.test.js`: new
  `unassignedPageUsesLabelsAndBreadcrumb`
- AC: AC12
- UI check: 017-sessions-and-calls §3.2
- Commit: feat(ui): restyle the unassigned connections page
- Depends: T020
- Size: S
- Risk: low

### T023 — Restyle the full exchange page
- Do: `internal/ui/web/exchange-detail.js` + `style.css`: same rules, breadcrumb
  "Sessions / <session title> / Call #n" (or "Sessions / Ungrouped" for an ungrouped call).
- Tests: Node — `app.test.js`: new `exchangePageUsesLabelsAndBreadcrumb`;
  `contrast.test.js` extended to the three pages' new colour pairs
- AC: AC12
- UI check: 017-sessions-and-calls §3.3
- Commit: feat(ui): restyle the full exchange page
- Depends: T020
- Size: M
- Risk: low

### T024 — Run the slice 3 manual check and close 017
- Do: `/ui-check 017-sessions-and-calls` §3 while Karthik watches; record it in research.md;
  set spec and tasks to done in their frontmatter once Karthik confirms.
- Tests: full `make verify`
- AC: AC12
- UI check: 017-sessions-and-calls §3 (all steps)
- Manual: docs/manual-checks/017-sessions-and-calls.md
- Commit: docs(docs): record the 017 other pages check
- Depends: T021–T023
- Size: S
- Risk: low

## AC coverage
| AC | Tasks |
|---|---|
| AC1 | T003, T006, T010 |
| AC2 | T004, T007, T010 |
| AC3 | T005, T008, T010 |
| AC4 | T009, T010 |
| AC5 | T014, T015, T018, T019 |
| AC6 | T012, T016, T019 |
| AC7 | T012, T016, T019 |
| AC8 | T013, T016, T019 |
| AC9 | T012, T014, T016, T019 |
| AC10 | T017, T019 |
| AC11 | T012, T013, T014, T016, T019 |
| AC12 | T021, T022, T023, T024 |
| AC13–AC20, AC22–AC24 | built: `ee30057`, `4020243`, `f9cc0df`, `c90f7ac` (no task) |
| AC21 | built `7172d69`; carried into the dialog by T015, T018 |

| Edge case | AC | Tasks |
|---|---|---|
| E1 | AC1, AC2, AC3 | T005, T006, T007, T008 |
| E2 | AC6 | T012, T016 |
| E3 | AC5 | T014, T015 |
| E4 | AC10 | T017 |
| E5 | AC1 | T003, T006 |
| E6 | AC4 | T009 |
| E7–E10 | AC13–AC15 | built (`ee30057`, `4020243`) |
