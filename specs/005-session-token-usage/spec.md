---
feature: 005-session-token-usage
kind: feat
status: approved
version: 3
created: 2026-10-11
supersedes:
branch: feat/005-session-token-usage
approved: 2026-10-11T09:26:54Z
approved-by: human
sha256: 6d5421708a8ef6099f371fbae51971098688d665610ffe0ce0bd4a80b08c9e87
fingerprint: 90e6e708aab15d33bdb698dd9aa73a692901f8978bb46aa44a4b4c2588aa630e
---
# Session token usage

## Problem
Every exchange already stores a canonical `usage` event (input, output, cache-write and
cache-read tokens, as the provider reported them), but nothing adds them up. I can't see
how many tokens a session used, which exchanges were heavy, or how much came from cache.
I'm on a Claude subscription, so there's no per-token bill to compare against, and money
is out of scope until I have an API key.

This is Phase 4 of PLAN.md §7, scoped to tokens only.

## Goal
- `gateway timeline` shows each model call's token counts, and ends with the session's
  totals broken down by model.
- `gateway sessions` (and `-ungrouped`) shows totals per session, broken down by model.
  Sub-agents and side calls may use a different model, and pricing will later be per model.
- Text and `-json` both carry the numbers. Missing data is never shown as 0.
- Everything reads canonical `usage` and `request` events and stored exchange columns,
  never a wire format; the 003 purity test covers it.

## Non-goals
- Money of any kind: pricing table, currency, cost estimates.
- The tiktoken estimate audit, and OQ-4 (stays open, deferred to the pricing phase).
- Budgets and limits (rate limiting is Phase 5).
- Splitting usage by sub-agent thread (003's thread detection isn't exposed as data).
- Changing the canonical `usage` event, or any other canonical event.
- A web UI.
- A new stored column or migration, unless the plan shows a real need and justifies it.

## Acceptance criteria
Terms used below. A **model call** is an exchange that has a canonical `request` event;
any other exchange (e.g. `HEAD /api/hello`, parse skipped) is neither summed nor counted.
A call's **model** is the `model` of its `request` event, or `unknown` when that is empty.
The four **counters** are input, output, cache-write and cache-read tokens, with one
meaning for every provider: input is uncached input only, so input + cache-write +
cache-read is the full prompt. A model call with no usage event is **errored** when its
response status is 4xx/5xx, and **missing** otherwise (2xx, or no response recorded).

- **AC1** — When `gateway timeline` prints a session, the system shall show, on every
  model call's exchange line (full and folded alike), that call's four counters.
- **AC2** — When a call's usage event leaves a counter nil, the system shall show it
  as `unknown` in text and `null` in `-json`, never as 0.
- **AC3** — When a model call has no usage event, the system shall show it as
  `no usage (errored)` or `no usage (missing)` in text, and `"usage": null` with
  `"no_usage": "errored" | "missing"` in `-json`, never as zeros.
- **AC4** — When a model call's usage event is partial, the system shall show its counters
  marked partial in text and `-json`.
- **AC5** — When `gateway timeline` finishes printing a session, the system shall print the
  session's usage totals: the sum of each counter over its model calls, and one breakdown
  per model with the same fields. Per-model breakdowns are ordered by model name
  ascending, with `unknown` last.
- **AC6** — When `gateway sessions` lists a session, the system shall show the session's
  totals on its row and, when the session used more than one model, one indented line per
  model below it; `-json` carries the totals and the per-model breakdown for every session.
- **AC7** — When some model calls in a total are missing usage, the system shall mark
  that total (and the affected per-model total) incomplete and state "K of N model calls
  missing usage", where N excludes errored calls. Errored calls shall be reported as a
  separate count ("E errored") and shall never make a total incomplete.
- **AC8** — When some model calls in a total left a counter nil, the system shall show that
  counter's total (and the affected per-model total) as the sum of the calls that reported
  it, marked incomplete with how many calls did not report it. If no call reported it, the
  counter shall show `unknown`, never 0. When a total is incomplete for both reasons (AC7
  and this one), both counts shall be shown.
- **AC9** — When a total includes partial usage, the system shall add those counts in and
  mark the total (and the affected per-model total) partial with how many calls' usage was
  partial.
- **AC10** — When a model call's model is `unknown`, the system shall group its usage under
  `unknown` and report it like any other model, never as an error.
- **AC11** — When `gateway sessions -ungrouped` lists exchanges, the system shall show each
  model call's counters on its row as in AC1–AC4, and after the list a total with per-model
  breakdown as in AC5 and AC7–AC10; the "ungrouped" summary line of plain
  `gateway sessions` carries the same totals.
- **AC12** — When a session, or the ungrouped set, has no model calls, the system shall show
  "no model calls" instead of numbers in text, and `"usage": null` (not zeros, not an
  omitted key) in `-json`.
- **AC13** — When run on `testdata/sessions/claude-code-task/`, the system shall report,
  in both text and `-json` for `gateway timeline` and `gateway sessions`, that session
  `fixture-id-1` totals input 9123, output 1084, cache-write 129405, cache-read 422293, all
  under one model `claude-opus-5-5`, complete with 9 of 9 model calls reporting and 1
  errored (the 429 side call); the fixture's `HEAD /api/hello` is not a model call.
- **AC14** — When the change is merged, PLAN.md shall have: §7 Phase 4 renamed "Usage
  (tokens)" with token-only scope and done-when; a new Phase 9 "Cost: pricing table keyed
  by provider + model, needs API billing to verify"; no other phase renumbered; the §8
  "Token estimation" row and OQ-4 retargeted to Phase 9, with OQ-4 still open; and the
  §7 "Later, each as a new adapter or profile" paragraph requiring that each new
  protocol's decoder maps usage to the counter meaning in 005's Terms.
- **AC16** — When a total (session, ungrouped set, or per-model breakdown) has no model calls that reported usage and none missing — every call errored — the system shall
  show `no usage (E errored)` in text, never 0 or unknown, and `"usage": null` with the
  errored count in `-json`; the total is not incomplete, and "0 of 0" is not printed.

## Edge cases
- **E1** — A model call with an error response and no usage (the fixture's 429) →
  `no usage (errored)`; the total stays complete and shows "1 errored" (AC3, AC7, AC13)
- **E2** — A usage event with one nil counter → that counter `unknown` on the exchange; its
  total is the sum of the others, marked incomplete (AC2, AC8)
- **E3** — A usage event from a partial stream → counts shown and summed, marked partial
  (AC4, AC9)
- **E4** — A request event with an empty model → grouped under `unknown` (AC10)
- **E5** — An empty session / ungrouped set with no model calls → "no model calls", no
  zeros (AC12)
- **E6** — A non-model exchange (`HEAD /api/hello`, parse skipped) in a session or the
  ungrouped set → not summed, not counted missing (AC7, AC13)
- **E7** — A session mixing models (a sub-agent or side call on another model) → one
  breakdown line per model, totals equal the sum of the breakdowns (AC5, AC6)
- **E8** — Every call in a total missing usage → each counter shows `unknown`, marked
  incomplete N of N (AC7)
- **E9** — A 2xx model call with no usage event (failed parse, or a stream that ended before
  usage arrived) → `no usage (missing)`; the total is incomplete (AC3, AC7)
- **E11** — A session or per-model group whose every model call errored (e.g. only 429s) →
  `no usage (1 errored)`, usage null, not incomplete (AC7, AC16)

## Constraints
- Reads only canonical events and stored exchange columns; the code lives where the 003
  purity test checks it (`internal/timeline`, `cmd/gateway/viewer.go`), and the test passes.
- No schema migration by default: usage is read from events at query time. A new column
  needs a justification in plan.md.
- Read-only: viewing usage never writes to the store.
- Existing `gateway sessions` / `gateway timeline` output keeps everything it shows today;
  usage is added, not substituted.

## Agnosticism check
Consumption side only (viewer and timeline package). The counter meaning is
provider-neutral and defined in Terms; the Anthropic decoder already produces it
(`internal/protocols/anthropic/response.go:25-28`). Every future decoder must map to it,
and each protocol's mapping (OpenAI/Codex included) is pinned in that protocol's own spec,
not here. The canonical `usage` event's shape is unchanged. Resolves no OQ; OQ-4 is
retargeted to Phase 9 and stays open.

## Open questions
None.

## Changelog
- v1 (2026-10-11) — first draft
- v2 (2026-10-11) — no-usage calls split into errored (4xx/5xx, never incomplete) and
  missing; counters get one provider-neutral meaning (input excludes cache); breakdown
  order fixed; AC8 covers all-nil and double incompleteness; AC12 JSON is `"usage": null`;
  AC13 now complete with 1 errored, in text and JSON; AC14 adds the decoder rule to the
  §7 "Later" paragraph; E1 rewritten, E9 added. The proposed OpenAI AC15/E10 were dropped:
  no OpenAI decoder exists, so the mapping moves to that protocol's spec.
- v3 (2026-10-11) — AC16 and E11: an all-errored total shows `no usage (E errored)`, never
  0, unknown or "0 of 0".
