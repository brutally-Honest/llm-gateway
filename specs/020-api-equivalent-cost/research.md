---
feature: 020-api-equivalent-cost
---
# Research & decisions — 020-api-equivalent-cost

## Open questions
<!-- Answer by changing "(open)" to "(answered)" and writing the answer underneath. -->

## Decisions
- D1 (2026-11-15) — Karthik: built by hand, no agent-loop kit (no /al-* commands), so
  spec.md stays a draft with no approval stamps and there is no tasks.md. Decided by
  Karthik before the build: a checked-in price table with effective dates; the 5m/1h
  split from usage, else all at 5m and marked estimated; the four places; "— no price
  for <model>" with partial totals ("≥"); server computes, browser formats; computed on
  read, no stored cost; side calls count; no-usage calls cost "—".
- D2 (2026-11-15) — Prices: read from <https://platform.claude.com/docs/en/about-claude/pricing>
  (the Markdown form, `pricing.md`) on 2026-11-15, with model IDs from
  <https://platform.claude.com/docs/en/about-claude/models/overview> the same day. Claude
  Opus 5.5 (`claude-opus-5-5`): input $4, 5m cache write $5, 1h cache write $8, cache
  hits $0.20, output $20 per MTok.
- D3 (2026-11-15) — Which models get rows: only those whose Claude API ID the overview
  page lists: `claude-fable-5-2`, `claude-opus-5-5`, `claude-sonnet-5-5`,
  `claude-haiku-5-5`. The pricing page prices older models by name only, and their IDs
  (dated snapshots and aliases) are not on these pages; a row is never written from a
  guessed ID. Adding a model later is one row. The real store has only
  `claude-opus-5-5` (Notes).
- D4 (2026-11-15) — Tiers: the page says Claude 4.6 and later models, except Haiku 5.5,
  have the full 1M context at standard prices (no long-context rate). Haiku 5.5 is priced
  by prompt length: above 100,000 prompt tokens (input + cache reads + cache writes, per
  request) it pays the higher row ($0.50 / $0.625 / $1 / $0.05 / $2.50). Modelled as a
  `prompt_over` column: a row applies to prompts above its threshold; the highest
  threshold under the call's prompt wins.
- D5 (2026-11-15) — Effective dates: the page shows one current price per model and no
  history or start date, so each model's first row has no lower bound ("since the
  start"). A later price change is a new row with its date; calls before it keep the old
  row. Dates are UTC days.
- D6 (2026-11-15) — Variants not priced: fast mode (`speed: fast`; Opus 5.5 $8 / $40, but
  its cache prices are not on the page) and US-only inference (`inference_geo`: 1.1x).
  Rather than guess, a call whose usage reports either is "no price for <model> (fast)"
  or "(us)", left out of totals like an unknown model. The real store has neither (every
  call `speed: standard`, `inference_geo: global`). Batch pricing does not apply: the
  gateway proxies the Messages API, not batches.
- D7 (2026-11-15) — Money unit: a cost is an int64 count of pico-dollars (10⁻¹² USD),
  the exact product of tokens and a price in micro-dollars per million tokens (every
  published price has at most six decimals in $/MTok). int64 holds $9.2 million, far
  past one user's history. The API sends amounts as exact decimal strings in USD
  (`"0.045210800000"` trimmed to `"0.0452108"`); JSON numbers would lose digits in the
  browser past 2⁵³ pico-dollars ($9,007). The browser rounds for display only.
- D8 (2026-11-15) — Where the code lives (AGENTS agnosticism): `internal/cost` holds the
  table format, the parser and the arithmetic and names no provider (added to the purity
  test). The Anthropic table (`prices.csv`, embedded) and the reading of the split from
  the usage event's detail (`cache_creation.ephemeral_5m_input_tokens` / `_1h_`, plus
  `speed` and `inference_geo` for D6) live in `internal/protocols/anthropic`. The
  gateway registers one book per protocol at start (`cost.Register`), like the profiles.
  The split is read from the stored detail instead of new canonical counters, so the
  calls captured before this build are priced the same way (compute on read, spec AC9),
  with no backfill.
- D9 (2026-11-15) — Registry vs threading a pricer: the books are a package-level
  registry in `internal/cost`, written once at start and read after. Antithesis: passing
  a pricer through every builder (as the client rules are) is explicit and keeps tests
  free of shared state; it touches every SessionStats/Build/BuildView call site and the
  CLI viewer for one read-only value. Synthesis: the registry, since it is set before any
  read and tests register a fixed fake book for protocol `test`. Flips if pricing ever
  varies per request (a per-principal price list), which would need the parameter.
- D10 (2026-11-15) — Errors and cut calls (spec E3): an error response with no usage
  counts (429, an upstream 5xx) is billed nothing on the API, so it costs $0 and leaves
  the total whole; a call with no usage counts for another reason (client cut, 499) may
  have used tokens, so it is "left out" and makes the total partial. A call whose usage
  event is partial (a reply cut short) or misses a counter is priced from what it has and
  marks the total "≥".
- D11 (2026-11-15) — Display: totals and a call's cost to cents ("$1.23"); below a cent
  but above zero "< $0.01"; the dialog's per-kind breakdown to 4 decimals, so its
  arithmetic can be checked by eye. Label "API-eq." in tight places, "API-equivalent"
  elsewhere; the hover: "What these tokens would cost at Anthropic's published API
  prices — not what was charged" names no provider in the page code: the provider's
  name comes from the book (`source` in the API).
- D12 (2026-11-15) — 013 AC35 said no view shows cost or money, and
  `TestWeb_NoCostOrContextPercent` held every web file to it. This spec reverses the money
  half on purpose (Phase 9), so the test is replaced, not loosened past its intent:
  `TestWeb_NoContextPercentAndMoneyOnlyFromCost` keeps the context-percentage ban for
  every file, allows money only in `cost.js` (no other file writes a currency sign, a
  `$`-amount or reads an amount field), and requires `cost.js` to label amounts
  "API-equivalent … not what was charged" and every file that reads a cost to go through
  it. Flagged to Karthik in the report, since it edits an older feature's check.
- D13 (2026-11-15) — Sessions list: the cost is a column of its own after Tokens, "API-eq.
  cost", one line, its header hover saying what the amount is. The page overview puts
  "API-equivalent cost: $X" in its foot after the tokens line; the Today line ends with
  "$X API-eq. cost"; the dialog has the cost in its top facts and an "API-equivalent cost"
  section after Context, with one row per token kind (5m and 1h apart) and the total.
- D14 (2026-11-15) — Correction to D13, from the browser check: a cost column of its own
  made the sessions table 1,626 px wide inside the 1,600 px page at 2,560 px, cutting the
  column off (the Session column stayed at its 20rem, 280 px at the 14 px root). The cost
  is now the Tokens cell's second line, "$X API-eq.", with its hover; the Tokens header's
  hover explains the line. No width is added.
- D15 (2026-11-15) — Correction to D12, on Karthik's question: the money regex existed on
  main (013, since 409ed1d). D12's first replacement dropped two of its patterns, so a
  "$5" in the middle of a string in another file would have passed. They are restored
  (`[$€£]\s*\d`, `\$\{[^}]*\}\s*usd`), so the money check is a superset of main's for
  every file but cost.js; a planted "Total: $5" in session-page.js fails it. What it no
  longer bans is the words "cost" and "price", which the labels need.
- D16 (2026-11-15) — Karthik: price fast mode from the published fast rates if any capture
  marks it, else keep D6. Checked on a fresh read-only copy of the real store (2,494
  exchanges, to 16:10): none of the 2,474 request bodies has a `speed` field, no request
  sends a fast-mode beta header, and all 2,457 usage events report `speed: standard`
  (`inference_geo: global`, `service_tier: standard`). Nothing marks fast mode, so D6
  stands: a call whose usage reports a non-standard speed stays "no price for <model>
  (fast)" rather than priced from rates the table does not hold. Adding it later is a
  `speed` column in the table and a row per fast rate.
- D17 (2026-11-15) — Karthik: fix the 1,440 px Tokens overflow (Notes, check) on this
  branch. Below 1,600 px the Tokens cell wraps at its " · " separators like Work and
  Changes (017 D49): each count is an unbreakable `.seg` part with the dots as text
  between them, in the same media query; the cost stays a line of its own that never
  breaks. This changes 017 D49's "Tokens stays on one line" below 1,600 px only; at
  1,600 px and wider every count cell is still one line.

## Notes
### Evidence: models in the real store (2026-11-15)
Read-only copy of `~/.local/share/llm-gateway/gateway.db` (`.backup`), 2,356 exchanges,
all protocol `anthropic`:

| Model (request) | Calls | With usage | With 5m/1h split | First call | Last call |
|---|---|---|---|---|---|
| `claude-opus-5-5` | 2,336 | 2,319 | 2,319 | 2026-11-10 14:09:39 | 2026-11-15 15:14:33 |

- Every response message names the same model. 20 exchanges have no request event (not
  model calls).
- The 17 calls without usage: status 429 or 499; 2 have no response at all.
- Every usage event's detail has `cache_creation` with both
  `ephemeral_5m_input_tokens` and `ephemeral_1h_input_tokens`; 2,290 write to the 1h
  cache, 6 to the 5m cache, 2,296 write at all. Detail keys seen: `cache_creation`,
  `inference_geo` (all `global`), `iterations` (always one, type `message`),
  `output_tokens_details`, `service_tier` (all `standard`), `speed` (all `standard`).
- 1,421 calls have a prompt above 200k tokens (largest 639,839): no tier applies to
  Opus 5.5 (D4).
- No model without a price: the manual check seeds one in a throwaway gateway.

### Check on the real gateway (2026-11-15)
`docs/manual-checks/020-cost.md`, Chrome, the real service on this branch's build
(`~/.local/bin/gateway`, the old binary kept as `gateway.bak-020`) and a throwaway gateway
for the seeded session. Karthik accepted the 2,560 px pass (with D15) and asked for the
header alignment, the fast-mode check (D16) and the 1,440 pass.

- 2,560 px (`innerWidth` 2560), light and dark, build bbffe91: every step of §1–§3 as
  expected after D14; the first run of §1 found the cost column cut off (D14).
- 1,440 px (`innerWidth` 1440; a 1,464 px window, 24 px of frame), light and dark, build
  83675a6: the Session column at its 20rem (280 px); every row's Tokens cell has its
  "$X API-eq." line; the Today line ends "$75.90 API-eq. cost"; the overview foot reads
  "API-equivalent cost: ≥ $47.98"; the dialog's cost table fits the dialog with Count,
  Price and Amount right-aligned over their numbers; the seeded session reads
  "≥ $0.22 API-eq." with no horizontal scroll.
- At 1,440 px the real list's table is 1,430 px in a 1,377 px frame, so its Tokens column
  is cut at the right edge (the list scrolls sideways). Not caused by this branch: hiding
  the cost lines leaves the width at 1,430 px, and main's build (b. 9777fb1) on a copy of
  the same store measures the same 1,430 in 1,377. The real store's token counts grew past
  what 017 D49's "Tokens stays on one line" fits ("209.4M cache read"). Left on the
  roadmap.
- Arithmetic, "Pending and completed tasks" (536 calls), summed from the store with exact
  decimals: 12,385 input × $4 + 0 × $5 + 1,299,387 1h writes × $8 + 148,184,385 reads ×
  $0.20 + 395,088 output × $20 per MTok = $0.04954 + $0 + $10.395096 + $29.636877 +
  $7.90176 = $47.983273; the API's `usd` is "47.983273", shown "≥ $47.98" (2 cut calls
  left out). One main call: 2 × $4 + 45,677 × $8 + 221 × $20 per MTok = $0.369844, shown
  $0.37; one side call (title generation): 1,172 × $4 + 18 × $20 = $0.005048, shown $0.01.
- GIFs: `~/Downloads/020-cost-2026-11-15.gif` (2,560 px),
  `~/Downloads/020-cost-1440-2026-11-15.gif` (1,440 px).

### Re-check after the Tokens wrap (D17, build 3aa2eb1, 2026-11-15)
Sessions list, real store, 15 rows, scanned in the page (widths from the DOM):

| Width | Theme | Table in frame | Sideways scroll | Session column | Tokens | Cut or outside the frame |
|---|---|---|---|---|---|---|
| 1,440 | dark, light | 1,375 in 1,377 | none | 283 px (min 280 = 20rem) | 2 lines of counts per row, counts never split, cost one line | none |
| 2,560 | dark, light | 1,598 in 1,600 | none | 350 px | one line per row, cost one line | none |

Work and Changes stay on one line at 2,560 too. The Session column's own lines shorten
with "…" by 017's design (1,440: 3 titles, 13 folder lines, 7 needs-a-look; 2,560: 1
title, 7 needs-a-look). Karthik accepted the 1,440 pass and the ellipsis; the 2,560 pass
showed nothing different from before, so the re-check is accepted on his instruction.
