---
feature: 020-api-equivalent-cost
kind: feat
status: draft
version: 1
created: 2026-11-15
supersedes:
branch: feat/020-api-equivalent-cost
---
# API-equivalent cost

## Goal
PLAN §7 Phase 9. Every model call and session shows what its captured tokens would
cost at the provider's published API prices: "API-equivalent", not money charged (the
user is on a subscription plan). The gateway prices each call from its stored usage
and a price table checked into the repo, when the page is read, so a fix to the table
re-prices history. The server computes every amount; the browser only formats it.

## Acceptance criteria
- **AC1** — A price table is checked into the repo, one row per model per price period,
  with an effective date and five prices per million tokens: input, output, cache read,
  cache write 5m, cache write 1h. Every price comes from the provider's published
  pricing page, whose URL and read date are in research.md. A row may also hold a
  prompt-size threshold, for a model whose published price rises above a prompt size.
- **AC2** — Each call is priced at the row in force when it started: the row of its
  model with the latest effective date at or before the call's start (and, for a tiered
  model, the tier its prompt size falls in).
- **AC3** — A call's cost is uncached input × input + output × output + cache read ×
  cache read + 5m cache writes × 5m + 1h cache writes × 1h. The 5m/1h split comes from
  the call's usage when it reports one; without one, every cache write is priced at the
  5m rate and the call's cost is marked estimated.
- **AC4** — Money is exact integer arithmetic (no float64) in the server; amounts reach
  the browser as exact decimal USD strings and are rounded only when shown.
- **AC5** — A call whose model has no row, or whose usage names a price variant the
  table does not hold (fast speed, US-only inference), shows "— no price for <model>"
  and is left out of every total; a call with no usage (failed, cut) shows "—" with the
  reason, as its tokens already do.
- **AC6** — A total that left out calls it could not price (no price, or no usage
  counts on a call that was not an error response) is partial: shown as "≥ $1.23", with
  a hover saying how many calls were left out and why. A call whose usage was cut short
  or left a counter unset makes the total a lower bound too.
- **AC7** — Side calls count toward the session's cost; the call details dialog shows a
  side call's cost like any call's.
- **AC8** — Cost shows in four places, each labelled "API-equivalent" (a short label and
  a hover explaining it is what the tokens would cost at published API prices, not what
  was charged): the call details dialog (the call's cost with its breakdown by token
  kind: tokens × price = amount), the sessions-list row, the session page overview, and
  the Today line.
- **AC9** — Cost is computed on read from the stored usage and the table; nothing about
  cost is stored. The existing view caches hold it like the rest of a view.
- **AC10** — `internal/cost` names no provider or client; the provider's price table
  and its reading of the 5m/1h split live in its protocol package. An unknown model is
  never an error.
- **AC11** — Go tests (-race): one per token kind, the split and no-split cases, a price
  period that changes mid-session, an unknown model making a total partial, a call with
  no usage, the tier threshold, and the embedded table parsing with every model the
  evidence found. Node tests cover the formatting of amounts and partial totals.
- **AC12** — On the real gateway running this build, in Chrome, light and dark, at
  1,440 px and 2,560 px: a normal session, a session with side calls, a session with a
  model that has no price, the dialog breakdown, the sessions row and the Today line;
  the sessions list's Session column keeps its 20rem; for one real session the
  arithmetic, tokens by kind × price, equals the cost shown.

## Edge cases
- **E1** — A usage event with a counter unset: that kind is priced as 0 and the call's
  cost is a lower bound (≥).
- **E2** — A split whose 5m + 1h does not equal the cache-write counter is not used: the
  call is priced as having no split (estimated).
- **E3** — An error response with no usage costs nothing and does not make a total
  partial; a cut call with no usage counts does, since tokens may have been used.
- **E4** — A call that started before a model's first row has no price (AC5).
- **E5** — A session or a day with no priced call shows "$0.00", or "—" when every call
  was left out.
- **E6** — A total that is both estimated and partial shows "≥" and both reasons.

## Out of scope
- Money actually charged, invoices, the provider's usage or cost APIs, API keys.
- Batch discounts, fast-mode and US-only inference prices, server-tool charges (web
  search per call), and partner platforms' prices (research D-entries).
- Budgets, alerts or limits on cost.
- Token audit against a local tokenizer (OQ-4) — left for later.
- Fetching prices at runtime: the table changes by a commit.
