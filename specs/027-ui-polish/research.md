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

## Open questions
