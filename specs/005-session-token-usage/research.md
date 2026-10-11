---
feature: 005-session-token-usage
---
# Research & decisions — 005-session-token-usage

## Open questions
<!-- Agents add a question here when they are blocked; the loop stops until you answer.
     Answer by changing "(open)" to "(answered)" and writing the answer underneath:
- **Q1** (open) T003 — <question> — options: A) … B) …
-->

## Decisions
<!-- - D1 (date) — <decision> — why -->
- D1 (2026-10-11) — Group usage by the request event's model — always present when the request parsed, so failed calls (the 429) land in the same group as successes.
- D2 (2026-10-11) — Only exchanges with a request event are model calls; others (HEAD /api/hello) are neither summed nor counted missing — a probe must not make every total incomplete.
- D3 (2026-10-11) — A nil counter in a total: sum the calls that reported it, mark incomplete with the count — same rule as a missing usage event, per counter.
- D4 (2026-10-11) — Partial usage is summed and the total marked partial with a count, not excluded.
- D5 (2026-10-11) — No model calls → "no model calls", never zeros.
- D6 (2026-10-11) — Pricing becomes Phase 9 in PLAN §7; the §8 token-estimation row and OQ-4 retarget to it.
- D7 (2026-10-11) — A model call with no usage splits into errored (4xx/5xx) and missing (otherwise); only missing makes a total incomplete — an error response consumed no tokens, so flagging it would make every rate-limited session look short.
- D8 (2026-10-11) — Counters have one provider-neutral meaning, fixed in spec Terms: input is uncached input only, so input + cache-write + cache-read is the full prompt. The Anthropic decoder already complies (internal/protocols/anthropic/response.go:25-28). Every future decoder must map to it; the OpenAI/Codex mapping is deferred to that protocol's spec, since no OpenAI decoder exists today (checked 2026-10-11) — keeps totals agnostic without pinning behaviour no code backs.
- D9 (2026-10-11) — An all-errored total shows no usage rather than 0 — 0 would assert a measurement nobody reported; same reasoning as AC12.

## Notes
