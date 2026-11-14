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
