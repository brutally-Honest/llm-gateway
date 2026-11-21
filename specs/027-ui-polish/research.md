# 027 UI polish: research

## Setup
- Throwaway gateway from this branch on ports 27195–27199, a scratch store and a copy of
  the config, forwarding to the seed scripts' stub; seeded with `seed-session.mjs --fast`,
  `seed-call-kinds.mjs --ungrouped`, `seed-cost.mjs` and `seed-calls.mjs --calls 200`
  (fictional data only). The real gateway on 7197/7199 was not touched.

## Decisions
- **D1** (2026-11-21) C1 — Sources column removed and Work + Tools merged into one Work cell
  ("8 prompts · 22 calls · 2 side · 17 tool calls · 1 failed"), each part a segment that
  wraps only at the dots (016 D49); failures keep the bad colour. At 1,440 px the Session
  column grows from about 320 px to 674 px; the table still fits with no sideways scroll.
- **D2** (2026-11-21) C1 — Status is a glyph alone (● active, ■ ended, ◇ hooks only) in a
  narrow first column; the word is its hover and its accessible name (`role="img"`,
  `aria-label`), and the header cell is named for screen readers. Karthik chose this over
  a glyph-and-word chip knowing it departs from spec 016 AC14 ("a status is always a
  glyph and a word"): the word is no longer visible. The three glyphs differ by shape, so
  colour is still never the only signal.
- **D3** (2026-11-21) C1 — With Sources gone, an active hooks-only session shows ● like any
  active one; that it has no model calls is said by the Work cell ("no model calls
  captured"), which replaces 016 D33's "Sources already says hooks only".
- **D4** (2026-11-21) C1 — The layout guard `sessionColumnKeepsItsMinimumAt1440` names
  `td.tools` in a selector it matches exactly, so that selector keeps `td.tools` (now
  unused) rather than editing the guard. Offered to Karthik as a one-word guard cleanup.

- **D5** (2026-11-21) C2 — Two styles only, Lean (Supabase x Linear: Inter, rounded, soft)
  and Stark (from the Clarity report style: Geist, square, mono caps labels). The old look
  is not kept as a third style (Karthik). Lean is the default; any other stored value,
  including an old "clarity", falls back to Lean.
- **D6** (2026-11-21) C2 — Colours are one base per theme shared by both styles, plus a few
  per-style overrides (Stark keeps to black, white, violet and light blue: success is plain
  ink, warnings and added lines are light blue). Light was the same for both styles at
  Karthik's request, and the dark base is darker (page #050505) so switching theme is not
  a jump in contrast; the switch eases over 200 ms, off for reduced motion.
- **D7** (2026-11-21) C2 — Accent is the violet Karthik picked (#a594f9 for text in dark,
  #6a5ae0 for fills under white text, #5b4fd6 in light). Red is kept for failures only.
- **D8** (2026-11-21) C2 — The contrast test now checks each style x theme as the base with
  that style's overrides on top, and `everyStyleBlockIsChecked` fails if the stylesheet has
  a style block the test does not name. Both were probed: a bad Stark light colour (1.93:1)
  and an unknown style block each failed. Lowest real pairs: 5.38 Lean dark, 4.56 Lean light,
  5.67 Stark dark, 4.69 Stark light.
- **D9** (2026-11-21) C2 — Settings is a modal `<dialog>` opened from a header button, with
  Theme and Style rows; Esc closes it (the dialog does that itself) and no backdrop click
  handler is added, because click handlers belong on buttons and links only. Page keys are
  ignored while it is open. The style variable is named `look` so no source line reads
  `style =`, which the web guard bans.
- **D10** (2026-11-21) C2 — Geist and Geist Mono are bundled (ADR 0020) because the CSP
  allows fonts only from the gateway; the OFL licence sits in `docs/licenses/` since served
  files may not contain URLs.
- **D11** (2026-11-21) — A "% cached" field was considered and skipped (Karthik): spec 013
  AC35 and `TestWeb_NoContextPercentAndMoneyOnlyFromCost` keep percentages out of the UI,
  and a ratio in tokens was not wanted for now.
- **D12** (2026-11-21) — Next changes, agreed from mockups v4–v6 (scratchpad, not in the
  repo): C3 the call details as a full-height panel on the right with a one-line header,
  section links and folded lists in one scroll (mockup C); C4 the session page as chapters,
  one page scroll, a sticky contents list, prompt and answer cards with Copy, a model chip per
  turn, boxes for short turns and a one-line-per-call list for long ones; C5 tabs under the
  summary (Story, Files changed, Commands, Sites, Context) with Files changed by turn or by
  file, and file changes shown only there. Karthik approved rewriting the guards
  `storyScrollsAloneBetweenTheTwoCards` (016 AC3) and `callReplyCapAndShowAllCoverRaw` to
  check the new layouts, and small server additions so the browser only formats: which
  calls fold, which turns use the long layout, and files grouped by turn.

- **D13** (2026-11-21) C3 — The call details stay a modal `<dialog>` (the page behind is
  inert and dimmed; `callScrimDimsOnlyThePageBehind` unchanged), docked with
  `margin: 8px 8px 8px auto` at full height, 52rem wide. Its top is the title, one line of
  label-value facts (model, duration, start, end, thread, status, cost) and buttons that
  scroll to each section; the body is the one scroll. This drops spec 017 AC5's repeated
  context line and message counts from the top: each is said once, in its section.
- **D14** (2026-11-21) C3 — No inner scrolls: the reply folds at 12rem with "Show all"
  (overflow hidden), messages carried over are folded behind one button and one line each,
  the new ones are shown whole. `callReplyCapAndShowAllCoverRaw` became
  `callPanelFoldsWithoutInnerScrolls` with Karthik's OK; probed with an inner message scroll.
- **D15** (2026-11-21) C3 — Empty sections go: no Health section for a healthy call (its
  status is in the top line), no Tools section when none were asked for, no counts row or bar
  when no usage was reported. A call in trouble shows Health first, in the failure colour,
  with its status. The context bar is sized by the server's three counts (flex-grow), with no
  percent; cache write uses the ink colour so it differs from input in both styles. The
  footer's Close button is gone; × and Esc close.

## Open questions
