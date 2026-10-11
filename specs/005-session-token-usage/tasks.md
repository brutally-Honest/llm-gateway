---
feature: 005-session-token-usage
status: approved
version: 2
approved: 2026-10-11T09:56:17Z
approved-by: human
sha256: 68505683fa77eba0e3ebcf3772212d26ef226466bbe62fefb48d48232fd185a6
fingerprint: bf88de0c1cc3a52a29ba8de50a622156e395b48e9f8b4cb7e8ac3b6c7ec9fb41
plan-fingerprint: bc1315ed6e80260cd7a973e0e4bd13dd202102ea873f349f32b59d76a2f913a1
---
# Tasks — 005-session-token-usage

## Tasks

### T001 — Add per-call usage decoding and the totals aggregator
- Do: FIRST step, before any code: read `internal/protocols/anthropic/stream.go` and confirm the decoder sets the usage event's `Partial` flag on a partial stream. If it does not, do NOT edit `internal/protocols/anthropic/`: add a `- **Qn** (open) T001 — ...` line to research.md `## Open questions` and stop BLOCKED (the decoder fix becomes its own spec). If it does, a test-only file in that package pinning the behaviour is allowed (no behaviour change). Then: new `internal/timeline/usage.go`: `Counters`, `CallUsage`, `callUsage(ex, evs)` (model call iff a request event exists; model or `unknown`; nil counters; partial from the event flag; errored only when `ex.TTFBNS != nil` and status is 400-599, else missing, because `capture.go:329-332` stores the gateway's fallback status when no response was recorded), `Group`, `Summary`, `Summarize` (per-model groups sorted ascending with `unknown` last, total built from the same calls). Add `fake.request`/`fake.usage` helpers in `helpers_test.go`. Pure logic, no rendering yet.
- Tests: TestCallUsage_PartialFromEventFlag (AC4), TestCallUsage_NoResponseFallbackStatusIsMissing (AC3, AC7), TestCallUsage_NilCounterIsUnknownNotZero (AC2), TestCallUsage_NoUsageErroredVsMissing (AC3), TestCallUsage_EmptyModelIsUnknown (AC10, E4), TestCallUsage_FailedParse2xxIsMissing (E9), TestSummarize_TotalsAndModelOrder (AC5), TestSummarize_MissingMakesIncompleteErroredDoesNot (AC7, E1), TestSummarize_NilCounterIncomplete (AC8, E2), TestSummarize_NoReporterIsUnknown (AC8), TestSummarize_BothIncompleteCounts (AC8), TestSummarize_PartialAddedAndCounted (AC9, E3), TestSummarize_UnknownModelLast (AC10), TestSummarize_NonModelExchangeIgnored (E6), TestSummarize_TotalEqualsSumOfModels (E7), TestSummarize_AllMissingUnknownNofN (E8), TestSummarize_AllErroredNoUsage (AC16, E11)
- AC: AC2, AC3, AC4, AC5, AC7, AC8, AC9, AC10, AC12, AC16
- Commit: feat(observability): sum canonical usage per model call
- Depends: —
- Size: L
- Risk: high

### T002 — Show each model call's counters in the timeline
- Do: add `Entry.Call *CallUsage`; `Build` fills it on exchange and folded entries that are model calls (loading events for folded-parse exchanges only to find the request event; folding behaviour unchanged). Add the per-call text suffix and the JSON `usage` / `no_usage` keys to `render.go` per plan Design. Non-calls render exactly as before.
- Tests: TestRenderText_CallCountersOnExchangeAndFolded (AC1), TestRenderJSON_NilCounterIsNull (AC2), TestRenderText_NoUsageErroredAndMissing (AC3), TestRenderJSON_NoUsageKey (AC3), TestRender_PartialUsageMarked (AC4, E3), TestRender_NonModelExchangeUnchanged (E6), existing build/render tests unchanged
- AC: AC1, AC2, AC3, AC4
- Commit: feat(observability): show token counters on timeline exchanges
- Depends: T001
- Size: M
- Risk: low

### T003 — Print session usage totals at the end of the timeline
- Do: `BuildWithUsage(src, session, opts) ([]Entry, Summary, error)` sharing one events pass; `Build` wraps it. Add group text/JSON renderers (`RenderUsageText`, `RenderUsageJSON`, the `usage_total` line) covering incomplete, partial, unknown, all-errored and no-model-call forms. `viewer.go` `runTimeline` renders totals after each session's entries, also for several sessions sharing a key.
- Tests: TestTimelineCmd_PrintsUsageTotals (AC5), TestRender_UsageTotalsQualifiers (AC7, AC8, AC9), TestRender_AllErroredText (AC16), TestRenderJSON_AllErroredUsageNull (AC16), TestRender_NoModelCalls (AC12, E5), TestRenderJSON_NoModelCallsUsageNull (AC12), TestTimelineCmd_TwoSessionsSameKeyEachHasTotals (AC5)
- AC: AC5, AC7, AC8, AC9, AC12, AC16
- Commit: feat(server): print usage totals after a session timeline
- Depends: T002
- Size: M
- Risk: low

### T004 — Add usage totals to `gateway sessions`
- Do: `SessionListing.Usage Summary`, filled by `ListSessions` in a pass beside `preview`. Text row: existing columns unchanged, usage group appended as a final tab column, indented per-model lines only when more than one model. JSON: `usage` and `models` keys on every session row (`"usage": null` when no calls or all errored, with `calls`/`errored`). Update `viewer.go` only as needed.
- Tests: TestSessionsCmd_RowTotalsAndModelLines (AC6, E7), TestRenderSessionsJSON_UsageAndModels (AC6), TestRenderSessions_NoModelCallsSession (AC12), TestRenderSessions_ExistingColumnsKeepPosition (constraint), TestListSessions_UsageDoesNotWrite (read-only)
- AC: AC6, AC12, AC16
- Commit: feat(server): show token totals per session in sessions
- Depends: T001
- Size: M
- Risk: low

### T005 — Add usage to `gateway sessions -ungrouped` and the ungrouped summary line
- Do: `timeline.ListUngrouped(src, limit)` returning rows with `Call` and a `Summary` over all ungrouped exchanges regardless of `-n` (plan Q1 decided), with a text line before the total saying it covers all N and `exchanges: N` in the JSON line; `UngroupedExchange.Call`; per-row counters text/JSON as in AC1-AC4; total with per-model breakdown after the list and a final `ungrouped_usage` JSON line; `UngroupedSummary` carries the Summary and the plain-sessions summary line prints the group. `viewer.go` uses `ListUngrouped`.
- Tests: TestSessionsCmd_UngroupedRowsAndTotals (AC11), TestSessionsCmd_UngroupedSummaryLineCarriesTotals (AC11), TestSessionsCmd_UngroupedNoModelCalls (AC12, E5), TestSessionsCmd_UngroupedNonModelExchangeIgnored (E6), TestSessionsCmd_UngroupedTotalScopeWithLimit (AC11, Q1)
- AC: AC11, AC12, AC10
- Commit: feat(server): show token usage for ungrouped exchanges
- Depends: T001, T003, T004
- Size: M
- Risk: low

### T006 — Prove the claude-code-task fixture totals end to end
- Do: viewer tests that run `gateway timeline fixture-id-1` and `gateway sessions` against `testdata/sessions/claude-code-task/` in text and `-json`; confirm the 003 purity test still passes. Fix any discrepancy in the earlier code, no new features.
- Tests: TestFixture_TimelineUsageTotals (AC13), TestFixture_SessionsUsageTotals (AC13), TestFixture_HeadHelloNotAModelCall (AC13, E6), TestFixture_TimelineJSONCarriesSameTotals (AC13), existing TestPurity (constraint)
- AC: AC13
- Commit: test(server): pin fixture token totals in text and json
- Depends: T003, T004
- Size: S
- Risk: low

### T008 — fix: json header usage keys
- Do: reproduce and fix: json header usage keys
- Tests: a regression test that fails before the fix and passes after it (json header usage keys)
- AC: none — bug fix: json header usage keys
- Commit: fix(session-token-usage): json header usage keys
- Depends: —
- Size: S
- Risk: low

### T007 — Update PLAN.md for usage-only Phase 4 and the new Phase 9
- Do: edit `PLAN.md` only: §7 Phase 4 renamed "Usage (tokens)" with token-only scope and done-when; new Phase 9 "Cost: pricing table keyed by provider + model, needs API billing to verify"; no other phase renumbered; §8 "Token estimation" row and OQ-4 retargeted to Phase 9, OQ-4 still open; §7 "Later, each as a new adapter or profile" paragraph requires each new protocol's decoder to map usage to the counter meaning in 005's Terms.
- Tests: none — documentation only; nothing executable changes, `make verify` stays green
- AC: AC14
- Commit: docs(docs): split phase 4 usage from phase 9 cost
- Depends: —
- Size: S
- Risk: low
- Manual: read the PLAN.md diff against AC14 (phase names, no renumbering, OQ-4 still open, decoder rule present)

## Changelog
- v1 (2026-10-11) — generated from the approved plan
- v2 (2026-10-11) — T001: partial-flag check first and BLOCKED if the decoder lacks it; errored needs a recorded response (TTFBNS); new tests TestCallUsage_PartialFromEventFlag and TestCallUsage_NoResponseFallbackStatusIsMissing
