---
feature: 017-sessions-list-and-call-details
---
# Research & decisions — 017-sessions-list-and-call-details

## Open questions
<!-- Answer by changing "(open)" to "(answered)" and writing the answer underneath. -->
- **Q1** (open) spec — a turn with model calls but no tool calls has no tool-calls chip, so
  its steps (016 AC7) cannot be opened — options: A) show "0 tool calls" as a chip anyway
  B) a "N model calls" chip that opens the same steps C) such a turn shows its steps open
- **Q2** (open) spec — what do the failed, files-read and searches chips open — options:
  A) failed opens the steps with failed calls unfolded; read and searches open their lists
  like files changed B) only tool calls, files changed and sites open; the rest stay labels

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

## Notes
