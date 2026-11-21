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

- **D16** (2026-11-21) C4a — One page scroll: the turns list (20rem, was 15rem) and the tab
  bar stick under the header; the story has no card around it, each turn is a chapter. Under
  1,080 px the list narrows to 15rem; under 760 px it hides. `storyScrollsAloneBetweenTheTwoCards`
  became `sessionPageIsOneScrollWithContentsInView` with Karthik's OK.
- **D17** (2026-11-21) C4a — Tabs: Story first, then Files, Commands, Sites, Context; each
  non-story tab starts with its own summary line, so the overview keeps only the Time and
  Context cards. The token line moved into Context.
- **D18** (2026-11-21) C4a — Twelve repeated facts removed (Headers link, Ended/Status/Thread
  on a failed call, empty Cost and Response sections, per-card Preview/Raw switches, the
  hooks-only sentence, the between-turn list entry, the hero token line, …). The hooks-only
  turn says "none captured" once, in its model chip; the fold reads "The hooks saw: N".
  `TestWeb_ServesHarnessBadgeRendering` now pins "The hooks saw:" with Karthik's OK.
- **D19** (2026-11-21) C4a — A hooks-only turn's unmatched tools join that turn's needs-a-look
  item on the server (`needsLook`), not in the browser.
- **D20** (2026-11-21) C4a — Files: a changed file expands in place to its changes and diff
  (or content, or why there is none); a Details link still opens the attribution dialog.
  Open files stay open across live redraws.
- **D21** (2026-11-21) C4a — Copy is an icon drawn in CSS from `data-state` (copy, tick,
  failed), last in its row, labelled for screen readers; one `copyButton` in tool-card.js
  serves the story, dialogs and the exchange page. Loaders are one `skeleton()` helper: a
  status line over bars shaped like what is coming; no shimmer under reduced motion.
- **D22** (2026-11-21) C4a — Scrollbar shift: `scrollbar-gutter: stable both-edges` on the
  page (and stable on inner scrolls). The gutter shows only the root's flat background colour
  (a gradient there was tried and does not reach it), so `html` takes the header colour and
  the body the page colour: the strips read as tracks on both sides, with no notch beside the
  header. Guarded by `scrollbarsNeverShiftTheLayout`.
- **D23** (2026-11-21) Next — Lean restyle agreed from mockup v9 (scratchpad): neutral
  surfaces (pure black dark, near-white light), green as the only accent, frosted glass cards
  with a top highlight, small radii, layered shadows; Stark keeps violet.

- **D24** (2026-11-21) C6a — Contrast for glass: the contrast test reads `#rrggbbaa` tokens,
  blends each over the page and over a card, and checks text against both; it checks
  on-accent at every stop of a `*-grad` token, and every text colour on `glass` and
  `glass-strong` for a style that paints glass (Lean). Text colours and the page stay
  opaque. Option A, approved by Karthik; probed with planted failures.
- **D25** (2026-11-21) C6a — Lean's colours sit in `:root[data-style="lean"][data-theme=…]`
  over the shared bases, which stay Stark's, so Stark renders as before. Lean: dark page
  #0a0a0b (from mockup v9's #000, lightened a step on request), light #fafafa, accent green
  (#34d399 dark, #055f46 light), gradient on the primary button only. Stark's dark page
  moved to the same #0a0a0b.
- **D26** (2026-11-21) C6a — The day's totals are tiles (both styles; layout, not colour);
  each row has a token bar whose segments take the server's raw counts as flex-grow. No
  status column (it would undo C1's glyph); sparklines are C6b (they need per-hour counts
  from the server).
- **D27** (2026-11-21) C6a — A whole row opens its session: the title link's `::after`
  stretches over the row, so it stays one real link (keyboard, screen readers, new tab) and
  `TestWeb_ActionsAreButtonsOrLinks` holds; a row click handler was rejected.
- **D28** (2026-11-21) C6a — Dialogs get no backdrop-filter, filter or transform: any of
  them makes the dialog the containing block of its fixed click-outside scrim, which then
  covers only the dialog (seen after the first Lean pass). Dialogs use near-solid
  `glass-strong`. `html:has(dialog:modal)` hides the page's overflow so the page behind does
  not scroll; the reserved gutters keep it from shifting.
- **D29** (2026-11-21) C6a — Built by a Sonnet agent from mockup v9 and reviewed: its token
  bar read the formatted counts and lived inside the cost text (fixed: raw counts, own
  element); tiles rendered ~300 px tall because the head was a wrapping flex column (fixed:
  no wrap).

- **D30** (2026-11-21) C6b — /api/today gains `hourly`: model calls (side calls included) and
  distinct active sessions per whole hour from `since`, with each series' peak; at most 48
  buckets. Hook activity is bucketed in SQL (`HookSessionsByHour`) so hourly sessions follow
  the total's 017 D30 rule. Bars are sized by `calc(var(--v) / var(--peak) * 100%)`; the
  browser computes nothing.
- **D31** (2026-11-21) C7a — From an audit of what each page shows (27 items), Karthik kept 25.
  Two of mine were wrong and withdrawn on review: the "All" chip opens a turn's whole stepper
  (not a default view), and the side calls do open a list (they keep a disclosure mark).
- **D32** (2026-11-21) C7a — The logo is the funnel (option E of five mockups): `logo.svg` used
  as a CSS mask so it takes each style's colours, and `favicon.svg` for the tab. The external
  reference guard now exempts `xmlns="http://www.w3.org/2000/svg"` in .svg files, exactly
  (option A, approved). The tab keeps the name Gateway.
- **D33** (2026-11-21) C7a — The call panel is no longer a browser modal: `showModal` made the
  header inert with the page. It opens with `show()`, the session page under it is made inert,
  its own scrim dims from below the header, and a `::after` repaints the panel's background
  over that scrim. This changes 017 AC21 (the call dialog was modal); guards updated with
  Karthik's OK. While it is open the edge strips take `--gutter-dim`.
- **D34** (2026-11-21) C7a — The stream dot has its own `--live-on`/`--live-off` pair, the same in
  both styles (Stark's `--ok` is ink), and is always round. The stop reason is labelled "Stop
  reason": a bare "Stop" is a hook event name the core purity test keeps inside
  clients/claudecode (found by make verify after the change; my miss).
- **D35** (2026-11-21) C7a — The turn list stays pinned (unpinning it was tried and reverted:
  no turn could be jumped to) but keeps no scrollbar strip it does not use.
- **D36** (2026-11-21) C8 — Newest first: the server keeps sending turns oldest first; the
  browser builds the story oldest first, so between-turn entries sit between the right pair,
  then shows it reversed. A live session follows the top.

- **D37** (2026-11-21) C7b — The failed status mark maps counts the server already sends
  (failed tool calls, errored main calls) to the row's glyph; no new arithmetic. startText is
  shared, so the ungrouped and unassigned tables also show today's starts as times. The
  hooks-only title is built by the server, so item 14 changed view_overview.go.
- **D38** (2026-11-21) C7b — Splitting commits with zero-context patches placed one CSS line a
  few rows off; it was moved back in its own refactor commit, as the branch has no interactive
  rebase.
- **D39** (2026-11-21) C7b — The turn list's sticky offset equals the page's top padding (24px,
  20px under 1,080px), so it pins where it starts instead of sliding 4px.
- **D40** (2026-11-21) C9 — One card per turn (option A of three mockups: one card, a thread
  rail, a tinted band). CSS makes .turn.chapter the card and the prompt and answer parts of it;
  the chips sit in a "How it got there" strip between them.
- **D41** (2026-11-21) C5 — FilesList gains by_turn: Changed split by each change's place
  (turn N, between two turns, outside any turn), oldest first; the page lists it newest first.
  A file changed in two turns is in both groups with only that turn's changes. The switch shows
  only when there is more than one group.
- **D42** (2026-11-21) C4b — Turn.figure is "boxes" for at most 5 main calls and 12 boxes
  (a sub-agent counts as one), "list" beyond. A list turn draws nothing new: the All panel is
  already one line per call, so D12's long layout is that panel. Tool states are glyphs, their
  word in the box's label. The CSS cursor property goes on its own line (009 D17 exemption).

## Wrap-up (2026-11-21)
- Accepted: C1–C4a, C4b, C5, C6a, C6b, C7a, C7b, C8, C9 and F1, each shown in light and dark,
  accepted by Karthik, then committed with its spec line. Nothing was rejected; one try (an
  unpinned turn list) was put back when it stopped turns being reachable.
- Guards: none weakened. The contrast guard was extended to alpha tokens, gradient stops and
  glass surfaces (Lean); the SVG namespace exemption was added for the logo and favicon; the
  layout guards were rewritten only where Karthik approved it in D12. Final run: contrast and
  layout 15/15, `make verify` green.
- Server additions so the browser only formats: hourly counts for the sparklines, the
  needs-a-look merge, the hooks-only title, files grouped by turn and each turn's figure.
- Lessons: run the full `make verify` after every change, not only the UI tests (the core
  purity guard caught "Stop" and a mid-line CSS cursor property); verify split commits alone
  in a worktree; let theme transitions settle before a screenshot.
- Left for later: the turn-figure thresholds are constants; sub-agent steps are not drawn as
  boxes; the Files by-turn choice lasts for the page only.

## Open questions
