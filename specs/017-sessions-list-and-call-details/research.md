---
feature: 017-sessions-list-and-call-details
---
# Research & decisions — 017-sessions-list-and-call-details

## Open questions
<!-- Answer by changing "(open)" to "(answered)" and writing the answer underneath. -->
- **Q1** (answered) spec — a turn with model calls but no tool calls has no tool-calls chip, so
  its steps (016 AC7) cannot be opened — options: A) show "0 tool calls" as a chip anyway
  B) a "N model calls" chip that opens the same steps C) such a turn shows its steps open
  - Answer (Karthik, 2026-11-13, recorded 2026-11-14): B — see D7; D10 later replaced "the
    same steps" with an All chip and per-kind narrowed timelines.
- **Q2** (answered) spec — what do the failed, files-read and searches chips open — options:
  A) failed opens the steps with failed calls unfolded; read and searches open their lists
  like files changed B) only tool calls, files changed and sites open; the rest stay labels
  - Answer (Karthik, 2026-11-13, recorded 2026-11-14): A — see D8; D10 later made every
    chip a narrowed timeline in order.

## Decisions
- D1 (2026-11-10) — Split from 016 (016 research D3): the sessions list and the call details
  dialog that replaces the 013 drawer. Built outside the agent-loop kit like 014–016.
- D2 (2026-11-10) — Plan and tasks are drafted after 016 is built, since 017 reuses 016's
  view endpoint, dialog shape and theme (016 research, Decisions for Karthik 13).
- D3 (2026-11-13) — Markdown preview uses the UI's own small renderer, no dependency
  (Karthik, over vendoring marked + DOMPurify with an ADR). It escapes all HTML; Raw is
  one click away for anything it renders badly.
- D4 (2026-11-13) — The 016 review's turn card changes go into 017, not a new feature
  (Karthik). The tool-calls chip opens the whole stepper, model calls included.
- D5 (2026-11-13) — One chip panel open per turn at a time (Karthik).
- D6 (2026-11-13) — Effects not caused by a tool call move into the files and sites chip
  panels, marked as such (Karthik, option B over keeping them in the steps or a chip of
  their own).
- D7 (2026-11-13) — Q1 answered by Karthik in chat: B, a "N model calls" chip that opens
  the same steps as the tool-calls chip.
- D8 (2026-11-13) — Q2 answered by Karthik in chat: A, the failures chip opens the steps with
  the failed calls unfolded; the files-read and searches chips open their own lists.
- D9 (2026-11-13) — Karthik: build the turn card changes (AC13–AC19) directly, outside the
  agent-loop kit, like 014–016. The sessions list and call details (AC1–AC12) are not built
  yet.
- D10 (2026-11-13) — Karthik, after trying the build: the model-calls and tool-calls chips
  opened the same stepper, and separate file and site lists lost the order. Now an "All" chip
  opens the whole stepper, and every other chip shows the timeline narrowed to its kind, in
  order, with each row's #n. Supersedes D6 and D7's "same steps" (effects no tool call caused
  sit in All after the steps, and in the narrowed lists marked as such).
- D11 (2026-11-13) — Karthik: prompts and answers are shown whole; no "Show all" fold. Also
  widened the right card (23rem to 30rem) and dimmed the page behind the 013 call drawer.
- D12 (2026-11-13) — Seen while testing, not fixed (Karthik: no change for it): Claude Code's
  away-summary ("The user stepped away…") and prompt-suggestion ("[SUGGESTION MODE…")
  requests show as turns; they should be side calls. AskUserQuestion answers are that tool's
  result, inside the turn that asked, not a turn of their own.
- D13 (2026-11-13) — Karthik, second round of testing: (1) the turn list no longer expands
  a prompt on hover or shows it as a tooltip; (2) the Context chart's per-call bars made
  turn labels collide ("T5T6T7T8"); of four options (one bar per turn, a per-call line with
  thinned labels, a table, turn bars that expand) Karthik chose one bar per turn; (3) the
  call drawer keeps its top fixed and is modal: the page behind is inert and a click on it
  closes the drawer; (4) the 55vh space after the last turn is gone.
- D14 (2026-11-13) — Karthik, third round: (1) opening a call from the Context list left a
  white screen: two `.children.find` calls (an HTMLCollection has no find; the test stub's
  arrays hid it), fixed, with a test that bans Array methods on `.children`; (2) the turn
  chips' `.chips` class also styled the sessions table's Tools column, stacking its chips,
  renamed `.turn-chips`; (3) "where are my answers?": they are stored as the
  AskUserQuestion tool call's input and result; Karthik chose to show them as questions and
  answers in its dialog (core.QuestionReader, read by the Claude Code profile; a new tool
  kind "ask" gives the line "N questions: headers"); (4) the sessions list: Karthik chose a
  visual cleanup now (same data) over the full AC1–AC4, plus a loader.
- D15 (2026-11-14) — Karthik: the remaining work (AC1–AC12) is built by hand, outside the
  agent-loop kit like the turn card (D9), in three slices recorded in tasks.md: sessions list
  (AC1–AC4), call details dialog (AC5–AC11), other pages (AC12). Q1 and Q2 were marked
  answered by hand on Karthik's instruction (no /al-answer run).
- D16 (2026-11-14) — D12 (away-summary and prompt-suggestion requests shown as turns) stays
  out of 017: it changes side-call detection in the Claude Code profile, not this UI. It
  gets its own fix branch later.
- D17 (2026-11-14) — Decided while writing tasks.md: AC3 says the "Today" line totals "all
  stored sessions", but the label, the existing /api/today?since= endpoint and the page's
  local-midnight refresh all mean since local midnight. Slice 1 keeps since-midnight and
  extends that endpoint; AC3's wording is listed as a contradiction for Karthik.
- D18 (2026-11-14) — Decided while writing tasks.md: AC5 opens a call from "a Context row or
  bar", but AC20 (later, D13) made the bar one per turn that scrolls the list to its calls.
  Slice 2 opens a call from a Context row only; a bar keeps AC20's behaviour.
- D19 (2026-11-14) — Decided while writing tasks.md: AC21's modal drawer is replaced by
  AC5's dialog in slice 2, which keeps AC21's promises (page dimmed and inert, a click
  outside closes, the top fixed while the rest scrolls), and AC15's rows open the dialog in
  place of the 013 drawer. The dialog is a native <dialog> opened with showModal(), which
  gives the inert page and backdrop without the drawer's scrim layer.
- D20 (2026-11-14) — The spec's own "Open questions" line ("plan and tasks are drafted once
  016 is built") is resolved: 016 is merged (PR #44, #45) and tasks.md is drafted; the line
  now points to D2 and D15. There is no plan.md: like the turn card work (D9), the build is
  by hand and tasks.md carries the approach per task.
- D21 (2026-11-14) — The spec's status was left at draft although Karthik asked for
  approved: approval here is the approval script's validated stamp and commit, which is
  human-only (AGENTS.md), and a hook blocks it for agents; writing `status: approved` by
  hand would forge it. Karthik approves with /al-approve spec, or overrides this.
- D21a (2026-11-14) — Correction to D21: Karthik approved spec v7 with /al-approve spec
  (67c86da).
- D22 (2026-11-14, slice 1) — A row's counts come from the session's own view, built inside
  the digest memo (one compute per session version, from the events it already loads) and
  kept as compact row facts: prompts = numbered turns, model calls = the view's calls minus
  its side calls, commands, wall clock (first to last activity), folder, needs-a-look kinds.
  So the list counts in the session page's terms. Files changed and sites stay on the
  store's fresh effect counts (distinct paths, distinct hosts — the view's terms too). The
  memo is versioned by exchanges and hooks, not effects, so needs-a-look items that depend
  on effects (changes with no tool running, unpinned groups) refresh with the session's
  next exchange or hook. Rejected: a lighter separate counter (would duplicate needsLook
  and drift), cachedView per row (thrashes the 4-entry view cache the session page uses).
- D23 (2026-11-14, slice 1) — A row's title is now 016's title rule (AC1): generated, else
  the first prompt cut at 120 runes, else "No prompt · <folder>" / "Hooks only · <folder>"
  (or client and start). Four list tests that asserted the old rule (80 runes, "" for no
  prompt) now assert the new one. The view called a session with a stored but unparsed
  exchange "Hooks only"; it is "No prompt" now (fixed in the view, so the session page
  agrees).
- D24 (2026-11-14, slice 1) — "Tool calls with failures" (AC1) is the sum of the server's
  per-state tool counts (captured and hook-seen calls) with the failed state apart; the old
  Errors column and tool-state chips are gone. Failed model calls show through "needs a
  look" (AC2), which already leaves side calls out.
- D25 (2026-11-14, slice 1) — Today's files changed counts each file once per session that
  changed it since midnight; sites count distinct hosts any session connected to;
  unassigned connections count in neither. effects has no index on `at`, so the query scans
  the table; no migration for it unless T010's measurement says so.
- D26 (2026-11-14, slice 1) — AC4's filters (All, Needs a look, Active, Hooks only) replace
  013's Has errors and Has gaps; Needs a look reads `list.needs_look`, Active the server's
  active flag (last activity under 5 minutes). Their tests were rewritten to the new kinds,
  keeping each old concern (side calls never match, hooks-only rows match, Load more kept).
- D27 (2026-11-14, slice 1) — The unassigned section lists the first 10 connections the API
  returns (oldest first, as the unassigned page does) and links to that page for the rest.
  The effects API now names each effect's stored client; a harness label names the real
  client, shown in its place.
- D28 (2026-11-14, slice 1) — TestHub_HookEventPushesSessionsList was already flaky before
  slice 1 (3 of 150 runs at HEAD: "TempDir RemoveAll cleanup: directory not empty"), and the
  heavier memo compute made it about 5%. Hub.Close cancels computes without waiting; the
  stream test env now waits for in-flight digest and timeline computes in its cleanup
  (0 of 150 after).
- D29 (2026-11-14, slice 1) — Row cells carry their labels in words and group as Work
  (prompts · calls · side), Tools (tool calls · failed), Changes (files changed · commands ·
  sites), Time and Tokens (in · out · cache read), with folder · model and the needs-a-look
  line under the title, so the table stays readable at 1,400 px. Status glyphs follow the
  session page (■ ended, ● active, ◇ hooks only); sources are ⇄ API and ⚑ hooks.
- D30 (2026-11-14, slice 1, Karthik at the UI check) — The Today line's "sessions" counts
  every session with any activity since local midnight: a model call or a hook. Before, it
  counted only sessions with a model call since then (013), so hooks-only sessions were
  left out although AC3 says sessions; the label stays "sessions". A hook with no session
  counts nowhere. /api/today reads the sessions that got a hook since the time from
  hook_events (no new index: hook_events_session leads with the session, so this scans).
- D31 (2026-11-14, slice 1, Karthik at the UI check) — Narrows AC2: in a session that is
  hooks only (no exchanges) every turn is a hooks-only turn, so none is listed under needs a
  look and the session does not match the Needs a look filter; a session that also has
  model calls still lists its hooks-only turns. This applies in the view, so the session
  page's Needs a look agrees with the list. spec.md is not edited now: the AC2 wording
  change goes in at close-out with one re-approval (Karthik).
- D32 (2026-11-14, slice 1, UI check) — The empty and error notes inside the sessions card
  get their own padding (the shared `.empty` rule has none sideways), and the filter note
  takes no room while it is empty, which left a gap between the filters and the list.
- D33 (2026-11-14, slice 1, Karthik at the UI check) — A hooks-only session still going
  shows `● active` as its status; a finished one keeps `◇ hooks only`. Status answers
  whether it is still going, and Sources (`⚑ hooks`) already says it is hooks only.
- D34 (2026-11-14, slice 1, Karthik at the UI check) — "no sessions match" is said once, in
  the list card; the filter note only says "filtering N loaded" (while more rows exist),
  also when nothing matches, so it still tells you only the loaded rows were searched.
- D35 (2026-11-14, slice 2) — The call details are built on the server by a pure
  `timeline.CallDetailsOf(view, id, input)`: the session's cached View places the call (its
  step or side call, turn, sub-agent, Context row, the calls before and after it) and the
  call's own exchange row, usage, thread fact and parsed events fill in the rest. Opening a
  call costs one exchange read, as the drawer's detail did, and no second view build.
  Thread kinds come from the view, so from the profile (SideCallNamer, the agent tool's
  label); no client `if`. Rejected: a per-call section inside every View (sends every
  call's details with each view and live event).
- D36 (2026-11-14, slice 2) — The dialog is its own module, `call-dialog.js`, in 016's shape
  (dialog.details, its head, facts, sections and footer; `toolNode` and `textBlock` reused)
  rather than one more key in `dialog.js`'s item stack: a call is driven by the URL and
  steps through calls, which the item dialog does not. Its tests are in their own file,
  `call-dialog.test.js`, not `view.test.js` as tasks.md T015–T017 name; T018's
  `everyCallLinkOpensTheDialog` is in `view.test.js` as named.
- D37 (2026-11-14, slice 2) — "Its thinking behind a toggle" (AC8): the view never keeps a
  call's thinking text (016 AC7), and the server sends a flag only (T013), so the toggle
  says the call thought and links the raw response stream, where the thinking is.
- D38 (2026-11-14, slice 2) — AC21's fixed top (title, context, message counts) also holds
  the facts (AC6 "start with labelled facts"), one compact row; the Context and What the
  model saw sections scroll with the rest.
- D39 (2026-11-14, slice 2) — One item dialog at a time: a call opening (its URL) closes an
  open tool, file or site dialog; a tool opened from the call dialog opens over it and takes
  ↑/↓ and Esc until it closes. A click outside closes through a transparent scrim button
  under the dialog's content (the UI's rule allows click handlers on buttons and links
  only; the ::backdrop dims), and Esc closes through the URL, so history stays right.
- D40 (2026-11-14, slice 2, T018) — What the 013 drawer showed that the call details dialog
  does not, listed before the drawer was deleted:
  1. The context plot: the main thread's per-call line with this call's dot marked. The
     dialog gives the size, change and parts in words; the session's Context tab keeps its
     one-bar-per-turn chart (AC20) and its per-call rows, each opening the dialog.
  2. A tool call's asked → ran → reported chain with a glyph per step ("reported missing",
     "not run (reason)"). The dialog lists each tool line with its state word; the tool's
     own 016 dialog shows its state, reason, outcome and hook lines.
  3. Health as yes/no rows (Client disconnected: no, Upstream aborted: no, Gateway error:
     none). The dialog says what went wrong, or "No disconnect, abort or error.".
  4. The session id in Health: the dialog does not show it (the page behind is that session).
  5. The tool calls' state chips in the drawer's header: in the dialog they are the Tool
     calls section's lines.
  6. When the detail failed to load, the drawer still showed the context, computed in the
     browser from the timeline entries. The dialog's facts and context come from the one
     server answer, so a failed load shows the reason and the raw links only.
  Tests removed with the code they tested, no new home: `laterPage504IsNotRetried` (the
  drawer's paged `/timeline` load, gone: the dialog loads one call, whose single 504 retry
  `callDetailRetriesOnceAfter504` still covers); `followOnlyWhenDrawerClosedAndAtBottom`
  (`shouldFollow`, exported by drawer.js and used nowhere); the chain tests
  `toolChainShowsThreeSteps` and `chainShowsRanWithReportedMissing` (item 2); the plot
  assertions of `contextPartsAndDelta` (item 1); "Session: s1" and the yes/no rows of
  `healthShowsDisconnectAbortError` (items 3, 4); the context-from-entries part of
  `missingSectionStatesReason` (item 6). Every other drawer assertion moved to
  `call-dialog.test.js`, `live.test.js`, `app.test.js` or `layout.test.js`. Left in place,
  now unused by the UI: `model.js` and `plot.js` (`web_test.go` asserts on model.js, so
  removing them changes a Go test) and `api.js`'s `loadAllPages` (its own tests) — for
  Karthik to decide.
- D41 (2026-11-15, slice 2, Karthik at the UI check) — The call dialog's reply is capped like
  its message boxes (12rem, scrolling inside), with a "Show all" button that expands it in
  place and "Show less" that caps it again. The "cut short" link and the raw links stay. The
  expanded state holds through live redraws of the same call and resets on the next call; a
  reply that fits its box has no button. This is the dialog only: the turn card still shows
  prompts and answers whole (D11).
- D42 (2026-11-15, slice 2, Karthik at the UI check) — A live update of a complete call
  refreshes only its place in the dialog's head: its number (an earlier call arriving late
  renumbers it), "N of M" and Previous / Next. The body is left alone, so scroll inside a
  message box or the reply, an expanded reply and focus all hold. Complete means the call
  had ended (the server gives a duration only then) and none of its tool calls is pending,
  both before and after the update; otherwise the body is drawn again, so a running call's
  facts and a pending tool's state still follow. Seen in the check: a message box scrolled
  inside went back to its top when calls were added to the session.
- D43 (2026-11-15, slice 2, Karthik at the UI check) — The call dialog's Headers link opens
  the full exchange page at its top, the same as "Full exchange page". It is not changed in
  slice 2: T023 adds a headers anchor to the full exchange page and points the Headers link
  at it.

## Notes
- Sessions list budget, before slice 1 (2026-11-14, T001): `BenchmarkSessionsList500`
  (500 sessions × 20 calls, `-benchtime 5x`, two runs, AMD Ryzen 5 5600G, 12 threads):
  first page of 50 cold (fresh digest cache) 130 ms/op, 26.4 MB, 370k allocs; cached
  6.1–6.4 ms/op; `/api/today` over all 10,000 calls 920 ms/op, 102 MB.
- Sessions list budget, after slice 1 (2026-11-14, T010), same benchmark and machine, two
  runs: first page cold 207–208 ms/op (+60%), 46.4 MB, 556k allocs — each row's memo now
  also builds the session's view (D22); cached 6.3–6.9 ms/op (unchanged: rows come from
  the memo); `/api/today` 930–939 ms/op (unchanged; the benchmark store has no effects, so
  the new effects query costs nothing here). Judgement: within budget — the cost is paid
  once per session version, off the request once a row is cached, and the cached page,
  which live updates and every reload after the first use, did not move. What would flip
  it: a cold first page past about 1 s on a real store, or the hub's recomputes of active
  sessions showing in the live list; then reuse the timeline cache's build for the memo.
  `/api/today` at ~0.9 s for 10,000 calls in a day predates slice 1 and is not changed by it.
- Sessions list budget, re-measured after the UI check fixes (2026-11-14, T010, D30–D34),
  same benchmark and machine, two runs: first page cold 206–210 ms/op, 46.4 MB, 556k
  allocs; cached 6.1–6.6 ms/op; `/api/today` 932–938 ms/op. Unchanged from the first after
  run: the fixes add no per-row work, and the benchmark store has no hooks, so the new
  hook-sessions query in `/api/today` (D30) costs nothing here; it scans hook_events from
  midnight on a real store. Judgement unchanged: within budget.
- Slice 1 manual check (2026-11-14, T010, `/ui-check 017-sessions-and-calls` §1 in Chrome,
  light and dark, 1,440 px): Karthik accepted 1.1–1.8. Rejected on the way and fixed:
  the empty note had no inner padding and an empty filter note left a gap (D32); 1.2's
  expected title was wrong in the check doc (the prompt is the title); the needs-a-look line
  was cut with no hover text; Today counted only sessions with model calls (D30); hooks-only
  sessions needed a look for their own turns (D31); an active hooks-only session did not say
  active (D33); "no sessions match" showed twice (D34). Not exercised: 1.6's "Loading
  sessions…" (the empty store answered too fast to see it; the loader has its web test).
- Slice 2 manual check (2026-11-14 to 2026-11-15, T019, `/ui-check 017-sessions-and-calls`
  §2 in Chrome, light and dark): Karthik accepted 2.1–2.11. Fixed on the way, each in its
  own commit: a cut or failed reply now says so and links the raw response (c4bf325); a
  side call's error status is a warning, not red (c263eba); focus starts on Close, and
  empty replies and messages say why (b29b845); thinking placeholders stay out of a call's
  reply (fa77ec3); the "No such call" note clears when another call opens (5e89a7c); the
  reply is capped like a message box, with Show all, also in Raw (fabcab3, D41); a live
  update of a complete call keeps its body, so scroll inside a message box holds (da0ceea,
  D42). Three gaps Karthik asked to close, checked with real clicks: a tool dialog's
  "Asked by" link opened call #8, the call whose stored response asked for that Edit; all
  four raw links of the very long call served content (request body 159,452 bytes and
  response 239,200 bytes as JSON; Headers and Full exchange page both open the exchange
  page with the header tables, the API key `[REDACTED]`, D43); with that call open, its body
  scrolled about halfway and a message box scrolled inside, calls added to the session left
  both scrolls, the open call and focus where they were ("3 of 8" became "3 of 11").
