---
feature: 005-session-token-usage
status: draft
version: 3
approved:
approved-by:
sha256:
fingerprint:
spec-fingerprint:
previous: cfb7183
---
# Plan — 005-session-token-usage

## Summary
Add a read-side usage layer to `internal/timeline`: decode each model call's canonical
`usage` + `request` events into a per-call record, aggregate records into totals with a
per-model breakdown, and render both in `gateway timeline`, `gateway sessions` and
`gateway sessions -ungrouped` (text and `-json`). No schema change, no write path, no new
dependency. PLAN.md is edited to add Phase 9 (cost) per AC14.

## Context
- `internal/timeline/build.go` — `Build`, `Entry`, `loadEvents`; `render.go` — all text/JSON
  renderers; `sessions.go` — `ListSessions`, `SessionListing`. `cmd/gateway/viewer.go` wires
  flags to these. All of it is covered by `internal/core/purity_test.go` (lists
  `../timeline` and `viewer.go`); new code lives only there.
- `store.EventRow` for kind `usage` has payload `input_tokens`, `output_tokens`,
  `cache_write_tokens`, `cache_read_tokens` (JSON number or `null`, from `*int64`) and
  `EventRow.Partial`. The `request` event payload has `model` (`core/canonical.go:114`).
  `store.ExchangeRow.Status` plus `TTFBNS != nil` (a response was recorded) gives errored.
- `loadEvents` skips events for parse `failed|skipped|unsupported_encoding`, but a failed
  parse can still store a request event (E9 needs it to count as a model call), so usage
  loads events for every exchange itself rather than reuse `loadEvents`.
- Test helpers: `internal/timeline/helpers_test.go` (`fake`, `build`), fixture tests in
  `fixture_test.go`; viewer tests in `cmd/gateway/viewer_test.go`.
- Fixture `testdata/sessions/claude-code-task/` (11 exchanges, 1 is `HEAD /api/hello`).

## Approach
New file `internal/timeline/usage.go` holds the pure logic: `CallUsage` (one model call),
`Counters`, `Summary` (total + per-model). Everything else only calls it. `Build` stays
source-compatible; a new `BuildWithUsage` returns entries plus the session's `Summary`.
`Entry` gains a `Call *CallUsage` set on exchange and folded entries that are model calls.
Renderers print it. `ListSessions` and a new `ListUngrouped` attach a `Summary`.

## Alternatives considered
### A1 — Aggregate at query time in the timeline package (chosen)
- Pros: no migration, honours the spec constraint; one pure aggregator shared by three views; testable with the in-memory `fake`.
- Cons: `gateway sessions` reads every session's events (N+1 queries, already so for the preview).
- Cons: partial-stream detail must come from the event flag.
### A2 — SQL aggregate over event payloads in the store reader
- Pros: one query per listing; fast on large stores.
- Cons: JSON-in-SQL for null/partial/missing semantics is hard to get right and test; puts policy (errored vs missing) in `internal/store`.
- Cons: the 003 purity check does not cover store, so a leak would go unseen.
### A3 — Denormalised usage columns on `exchanges`
- Pros: trivial reads and sums.
- Cons: schema migration plus a backfill for existing rows; the spec forbids it without a real need, and there is none at local scale.
- Cons: a second copy of data that is already in events.

## Design
**Per-call record (AC1-4, AC10, AC12, AC16).** An exchange is a model call iff it has a
`request` event (D2). `CallUsage{Model, Counters, Partial, NoUsage}`: `Model` is the request
event's `model`, `unknown` when empty (AC10). `Counters` has four `*int64`, nil for a JSON
`null`/absent counter (AC2). With a usage event: `Partial` = the event's `Partial` flag
(AC4), `NoUsage` = "". Without one: `NoUsage` = `errored` only when a response was actually
recorded and its status is 400-599, else `missing` (AC3, AC7). When no response was
recorded, `finishCapture` (`internal/core/capture.go:329-332`) stores `m.Status` instead
(the resTee status is 0): that is the gateway's own answer from `handleError`
(`proxy.go:87-91`, e.g. 499 client closed, or a 5xx the gateway chose) and 0 if nothing was
set. Those values look like real 4xx/5xx, so status alone cannot tell them apart. The stored
column `ttfb_ns` can: `ExchangeRow.TTFBNS` is nil exactly when no upstream response headers
arrived (`HasTTFB` false). So a response was recorded iff `TTFBNS != nil`; errored =
`TTFBNS != nil && 400 <= Status <= 599`. No migration is needed. The fixture's 429 has a
non-null `ttfb_ns` (none of its rows is null), so it stays errored. A second usage event on one exchange is not expected; the last one wins.
Constructor `callUsage(ex store.ExchangeRow, evs []store.EventRow) (CallUsage, bool)`; the
bool is false for a non-call. Malformed usage payload is treated as a usage event with
all counters nil and is not an error (capture never fails the view).

**Aggregation (AC5, AC7-AC10, AC12, AC16, E-ids).** `Summarize([]CallUsage) Summary`.
`Summary{Calls int, Total Group, Models []NamedGroup}` where `Models` is sorted by name
ascending with `unknown` last. `Group` holds: `Reporting` (calls with a usage event),
`Missing`, `Errored`, `PartialCalls`, and per counter `Sum *int64` + `Unreported int`
(calls with a usage event that left it nil). Rules: `Sum` is nil when no call reported the
counter (AC8, shown `unknown`); `Missing` is the only thing that makes the group
incomplete by AC7, with N = Reporting + Missing (errored excluded); a counter's
`Unreported` makes it incomplete by AC8; both counts are shown when both apply. A group
with Reporting = 0 and Missing = 0 and Errored > 0 is all-errored (AC16): no counters, usage
null. A group with Reporting = 0 and Missing > 0 shows every counter `unknown`, incomplete
N of N (E8). A Summary with Calls = 0 renders "no model calls" / `"usage": null` (AC12).
Total counters equal the sum of the per-model counters (E7): both are built from the same
call list, and a test asserts the invariant.

**Text format (one definition, used by all views).**
- Per call: ` tokens in=<n|unknown> out=<n|unknown> cache_write=<n|unknown> cache_read=<n|unknown>`
  appended to the exchange/folded line, plus ` [usage partial]` when partial (AC1-4);
  no usage: ` no usage (errored)` / ` no usage (missing)`. Non-calls get nothing appended.
- Group: `input <n>, output <n>, cache-write <n>, cache-read <n>` then qualifiers in
  order: `; partial (P calls)`, `; incomplete: K of N model calls missing usage`,
  `; <counter> incomplete (M calls did not report)`, `; E errored`. A counter with no
  reporter prints `unknown`. All-errored: `no usage (E errored)`. No calls: `no model calls`.
- Timeline: after a session's entries, `== usage total: <group>` then one indented line
  `   <model>: <group>` per model (always listed, even for one model, so AC5 holds; the
  session row in `sessions` lists them only when more than one model, per AC6).

**JSON.** Per-call: model-call entries (kinds `exchange`, `folded`) carry
`"usage": {"input_tokens":..,"output_tokens":..,"cache_write_tokens":..,"cache_read_tokens":..,"partial":bool}`
(null counters stay `null`) or `"usage": null` with `"no_usage":"errored|missing"`;
non-call entries omit both keys. Totals: `{"usage": <group>|null, "models":[{"model":..,"usage":<group>|null,...}]}`
where a group object is the counters (number or `null`) plus `calls`, `missing`, `errored`,
`partial_calls`, `unreported` (per counter), and `complete` bool. `"usage": null` always
carries `errored` and `calls` siblings so AC16/AC12 stay distinguishable. Timeline emits a
final line `{"type":"usage_total",...}` per session; sessions rows get `usage` and `models`
keys; the ungrouped row and `-ungrouped` output likewise (a final `{"type":"ungrouped_usage"}`
line after the exchange lines).

**Timeline wiring (AC1-AC5).** `BuildWithUsage` computes `Summary` from the same events
pass; for exchanges with folded parse it fetches events only for the model-call check.
`Build` calls it and drops the Summary so existing callers and tests are untouched.
`viewer.go` calls `BuildWithUsage` and renders totals after each session's entries (also
when several sessions share a key).

**Sessions (AC6).** `SessionListing` gains `Usage Summary`; `ListSessions` fills it with a
usage pass beside `preview`. Text row: existing columns unchanged, usage group appended as a
final tab column; when more than one model, indented model lines follow. Existing output
is kept (constraint).

**Ungrouped (AC11).** New `ListUngrouped(src, limit)` returns the listed
`UngroupedExchange` rows (each with `Call *CallUsage`) and a `Summary`. `UngroupedSummary`
(the plain-sessions summary line) gains the Summary; the line carries the group text.
Source needs `UngroupedExchanges` and `Events`; `*store.Reader` already satisfies both.
Totals cover all ungrouped exchanges whatever `-n` is (Q1 decided); `ListUngrouped` reads
every ungrouped exchange for the Summary and slices the listed rows to `limit`. In text, a
line before the total states "total over all N ungrouped exchanges"; in JSON the
`ungrouped_usage` line carries `exchanges: N`.

**Cross-cutting.** Read-only, no concurrency, no auth or tenancy change (events are fetched
per `(principal, client, key)` exchange list as today). No new dependency. No migration.
Agnostic: reads only `request`/`usage` payload fields and exchange status; no provider name.

**PLAN.md (AC14).** Doc-only edit of §7 Phase 4 (rename "Usage (tokens)", token-only scope
and done-when), new Phase 9 entry, §8 "Token estimation" row and OQ-4 retargeted to Phase 9
(OQ-4 stays open), and the §7 "Later" paragraph gains the decoder-mapping rule.

## AC coverage
| AC | Where | Verified by |
|----|-------|-------------|
| AC1 | `usage.go`, `build.go` (Entry.Call), `render.go` | `TestRenderText_CallCountersOnExchangeAndFolded` |
| AC2 | `usage.go` `callUsage`, `render.go` | `TestCallUsage_NilCounterIsUnknownNotZero`, `TestRenderJSON_NilCounterIsNull` |
| AC3 | `usage.go`, `render.go` | `TestCallUsage_NoUsageErroredVsMissing`, `TestCallUsage_NoResponseFallbackStatusIsMissing`, `TestRenderJSON_NoUsageKey` |
| AC4 | `usage.go`, `render.go` | `TestCallUsage_PartialFromEventFlag`, `TestRender_PartialUsageMarked` |
| AC5 | `usage.go` `Summarize`, `render.go`, `viewer.go` | `TestSummarize_TotalsAndModelOrder`, `TestTimelineCmd_PrintsUsageTotals` |
| AC6 | `sessions.go`, `render.go`, `viewer.go` | `TestSessionsCmd_RowTotalsAndModelLines`, `TestRenderSessionsJSON_UsageAndModels` |
| AC7 | `usage.go` | `TestSummarize_MissingMakesIncompleteErroredDoesNot`, `TestCallUsage_NoResponseFallbackStatusIsMissing` |
| AC8 | `usage.go` | `TestSummarize_NilCounterIncomplete`, `TestSummarize_NoReporterIsUnknown`, `TestSummarize_BothIncompleteCounts` |
| AC9 | `usage.go` | `TestSummarize_PartialAddedAndCounted` |
| AC10 | `usage.go` | `TestCallUsage_EmptyModelIsUnknown`, `TestSummarize_UnknownModelLast` |
| AC11 | `sessions.go` `ListUngrouped`, `render.go`, `viewer.go` | `TestSessionsCmd_UngroupedRowsAndTotals`, `TestSessionsCmd_UngroupedSummaryLineCarriesTotals` |
| AC12 | `usage.go`, `render.go` | `TestRender_NoModelCalls`, `TestRenderJSON_NoModelCallsUsageNull` |
| AC13 | fixture end-to-end | `TestFixture_TimelineUsageTotals`, `TestFixture_SessionsUsageTotals` (text and JSON) |
| AC14 | `PLAN.md` | no Go test (doc-only): human read of the PLAN.md diff, task T007 `Manual:` |
| AC16 | `usage.go`, `render.go` | `TestSummarize_AllErroredNoUsage`, `TestRender_AllErroredText`, `TestRenderJSON_AllErroredUsageNull` |

## Test strategy
Unit tests of `Summarize` and `callUsage` against the in-memory `fake` (add `fake.request`,
`fake.usage` helpers): one test per E-id.
- E1 `TestSummarize_ErroredCallKeepsTotalComplete`; E2 `TestSummarize_NilCounterIncomplete`;
  E3 `TestSummarize_PartialAddedAndCounted`; E4 `TestCallUsage_EmptyModelIsUnknown`;
  E5 `TestRender_NoModelCalls`; E6 `TestSummarize_NonModelExchangeIgnored` (a HEAD with no
  request event, plus a failed-parse one with none); E7 `TestSummarize_TotalEqualsSumOfModels`;
  E8 `TestSummarize_AllMissingUnknownNofN`; E9 `TestCallUsage_FailedParse2xxIsMissing`;
  E11 `TestSummarize_AllErroredNoUsage`.
- Renderers: golden-string tests for text and decoded-JSON assertions (null vs 0 vs absent).
- Fixture acceptance (AC13) through the real viewer commands in `cmd/gateway/viewer_test.go`.
- `TestPurity` (existing) must still pass; no new imports of store internals beyond `store`.
- Mutation-relevant: a test that fails if "missing" and "errored" swap, and one that fails
  if a nil counter sums as 0.

## Risks
- Partial flag source: relies on the usage event's `Partial`. The first step of T001 is to
  check that `internal/protocols/anthropic/stream.go` sets it on a partial stream (it passes
  `partial` to `assistantEvents` at line 293, to be confirmed). If it does not, T001 does
  not touch `internal/protocols/anthropic/`: it adds a `Qn` to research.md and stops
  BLOCKED; the decoder fix becomes its own spec. A test-only file in
  `internal/protocols/anthropic/` that pins the existing behaviour is allowed and changes
  no behaviour. Damage is limited to a missing partial marker, never to the counts.
- Errored/missing split depends on `TTFBNS`; a future change to when `ttfb_ns` is written
  would shift it. `TestCallUsage_NoResponseFallbackStatusIsMissing` pins it.
- JSON shape is a public CLI contract; one definition above, one golden test per view.
- Sessions listing cost grows with event count; acceptable locally, `-n` bounds it.
- Text column added to sessions rows could break scripts splitting columns; it is appended
  last so earlier columns keep their positions.

## Open questions
- **Q1** — With `gateway sessions -ungrouped -n 20`, should the total after the list cover only the 20 listed exchanges or all ungrouped exchanges? (recommended: all ungrouped — it then equals the "ungrouped" summary line in plain `sessions` (AC11), and the line before the total says it covers all N) → decided: all ungrouped

## Changelog
- v1 (2026-10-11) — first draft
- v2 (2026-10-11) — errored requires a recorded response (`TTFBNS != nil`), fallback status explained; partial-flag check is T001's first step and blocks instead of editing the decoder; test-only file in the anthropic package allowed; new tests in AC coverage
- v3 (2026-10-11) — reopened: spec v4 changed its contract sections
