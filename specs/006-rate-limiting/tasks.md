---
feature: 006-rate-limiting
status: approved
version: 1
approved: 2026-10-12T16:11:39Z
approved-by: human
sha256: 46dc57a699596da44bdc3dc4eff0094e296fe641d51a327d7c708c238853cf4f
fingerprint: 30707d6011a0e1f6560def1fa536bb44205e0313d50c5221f168f277e02609c7
plan-fingerprint: 4d911d754ca478c402ed6968fad6ece8a4429a47414281bcd0533d0b899d1e3c
---
# Tasks — 006-rate-limiting

<!-- One block per task, in build order. Each task = one commit that leaves verify green.
     Tasks not started yet stay editable after approval (reorder, split, merge, retitle);
     done tasks are frozen. Progress lives in .agent-loop/ and git (see: loop.sh status).
     Size: S|M|L picks the model, Risk: high forces a review; Model:, Review: skip|always,
     Verify: targeted|full are optional overrides.

### T001 — <imperative title>
- Do: <what changes, which packages/files>
- Tests: <named behaviour tests: the happy path + the spec's edge cases it touches>   (or: none — <reason>)
- AC: AC1, AC2   (or: none — <reason>; in amendments: AC2 (rework), AC4 (remove))
- Commit: feat(scope): <subject>
- Depends: —   (or: T00n, earlier tasks only)
- Size: S   (S | M | L)
- Risk: low   (low | high)
- Manual: <only for a check a human must run by hand; makes the task NEEDS-HUMAN>
-->

## Tasks

### T001 — Load and validate the ratelimit config block
- Do: internal/config: add `RateLimit{Enabled, Strategy, Keys map[string]KeyLimit{RPM,TPM}}` to `Config` (defaults: disabled, `fixed_window_memory`); `ratelimit.enabled` and `ratelimit.strategy` as settings (env-settable per ADR 0002); parse the file-only `ratelimit.keys.<name>.{rpm,tpm}` block in `parseFile`; validate after file+env (unknown strategy, negative or non-integer limit, empty key name, enabled with no keys) with an `*Error` naming the field.
- Tests: TestLoad_RateLimitDefaultsOff (AC1); TestLoad_RateLimitFileAndEnv (enabled via env, keys from file); TestLoad_RateLimitInvalid_NamesField (AC16: unknown strategy, negative rpm, empty key name, enabled with no keys, unknown sub-key)
- AC: AC1, AC16
- Commit: feat(config): load and validate the ratelimit block
- Depends: —
- Size: M
- Risk: low

### T002 — Add the Limiter interface and fixed-window memory strategy
- Do: new internal/ratelimit (limiter.go, fixedwindow.go): `Limiter{Admit,Record}`, `Decision`, `New(cfg)` selecting by strategy name, per-key-mutex memory implementation with an injectable clock, lazy window rollover, 0 = unlimited, remaining clamped at 0, unknown key reported without creating state.
- Tests: TestFixedWindow_RejectsOverRPM (AC5); TestFixedWindow_RejectsAtTPM (AC6); TestFixedWindow_NewWindowResetsCounts (AC12); TestFixedWindow_ZeroLimitUnlimited (AC13); TestFixedWindow_ConcurrentBurstAdmitsExactlyRemaining under -race (AC14, E10); TestFixedWindow_OvershootRejectsUntilWindowEnds (E4); TestFixedWindow_StreamEndingInNextWindowCountsThere (E3); TestFixedWindow_UnknownKeyCreatesNoState (AC3)
- AC: AC3, AC5, AC6, AC12, AC13, AC14
- Commit: feat(ratelimit): add limiter interface and fixed-window strategy
- Depends: —
- Size: M
- Risk: high

### T003 — Strip and redact x-gateway-key in the proxy
- Do: internal/core: delete `X-Gateway-Key` from the outbound headers in `Proxy` rewrite after `teeRequest` has snapshotted them, and add it to core's `secretHeaders`; unconditional, on every route.
- Tests: TestRewrite_StripsGatewayKey (AC18); TestCapture_RedactsGatewayKey (AC18: stored request header shows [REDACTED], value absent); TestProxy_GatewayKeyOnNonModelRoute_StrippedAndRedacted (E9)
- AC: AC1, AC18
- Commit: feat(proxy): strip and redact the gateway key header
- Depends: —
- Size: S
- Risk: high

### T004 — Add a streaming decoder and non-blocking feeder to contentcoding
- Do: internal/contentcoding: `NewReader(encoding, io.Reader)` chaining streaming gzip/deflate (zlib or raw via Peek)/br/zstd decoders, `Unsupported` for unknown codings; `Feeder` (non-blocking `Write` into a 1 MiB bounded queue, one goroutine decoding to a sink, `Close` waits and reports Complete/CutShort/Unsupported/Overflowed, 64 MiB decoded-output cap, always drains). Existing `Decode` and its tests unchanged. No new dependency.
- Tests: TestNewReader_StreamsEachCoding (AC9: gzip, deflate zlib and raw, br, zstd, stacked, 1-byte chunks); TestNewReader_UnknownCodingUnsupported; TestFeeder_WriteNeverBlocksOnSlowSink (AC9); TestFeeder_OverflowMarksOverflowedAndDrops; TestFeeder_BombStopsAtOutputCap; TestFeeder_CloseDrainsNoLeak; TestFeeder_TruncatedInputReportsCutShort (E1)
- AC: AC9
- Commit: feat(contentcoding): add streaming decoder and non-blocking feeder
- Depends: —
- Size: L
- Risk: high

### T005 — Add the RateLimitAdapter contract and the Anthropic implementation
- Do: internal/core: `RateLimitAdapter` (IsModelCall, LimitErrorBody, UsageReader), `LimitErrorKind`, `UsageReader`. internal/protocols/anthropic: implement them: `POST /v1/messages` only; permission_error and rate_limit_error envelopes via the existing `errorEnvelope`; usage reader with an incremental SSE scanner (single event buffered, 1 MiB cap) fed directly for identity streams and through `contentcoding.Feeder` for any Content-Encoding (gzip, deflate, br, zstd); non-stream JSON via a 4 MiB capped decoded buffer; shared usage-merge code with `streamState`; tokens = input + cache-write + output; Unsupported/Overflowed gives no tokens.
- Tests: TestIsModelCall_OnlyMessagesPost (AC4: count_tokens, /v1/models, HEAD /api/hello are not); TestLimitErrorBody_PermissionAndRateLimitTypes (AC2, AC7); TestUsageReader_StreamTokensMatchParser (AC9); TestUsageReader_GzipSSEStream (AC9: testdata/stream.sse.gz, generated by gzip-compressing the existing recorded testdata/stream.sse byte for byte with no hand edits, fed in small chunks, asserts the same usage total as TestUsageReader_StreamTokensMatchParser on stream.sse); TestUsageReader_EachCodingSSEStream (AC9: br, zstd and deflate variants generated the same way from stream.sse, same usage total); fixtures never come from the 2026-10-12 capture (it holds org, workspace and session identifiers) and all pass TestFixtures_NoIdentifiers; TestUsageReader_JSONBody (AC9); TestUsageReader_GzipJSONBody (AC9); TestUsageReader_CutGzipStreamStillReportsSeenUsage (E1); TestUsageReader_NoUsageReportsNotOK (AC11); TestUsageReader_ErrorStatusNotOK (E2); TestUsageReader_OversizeJSONBodyNoTokens; TestUsageReader_UnsupportedCodingNotOK (AC11)
- AC: AC2, AC4, AC7, AC9, AC11
- Commit: feat(anthropic): report model calls, usage and limit errors
- Depends: T004
- Size: L
- Risk: high

### T006 — Observe response usage in core and add the registry guard
- Do: internal/core: `Meta.OnUsage` and `UsageResult`; a `usageWriter` (inner write first, status/headers recorded, `Unwrap`) installed in `Proxy.ServeHTTP` only when `OnUsage` is set and the adapter implements `RateLimitAdapter`; `Meta.Settle` finishes it and calls `OnUsage` once; `core.WithGuard` registry option wrapping each adapter's proxy (`mounted` holds an `http.Handler`).
- Tests: TestUsageWriter_ForwardsBytesUnchangedAndFlushes; TestUsageWriter_CallsOnUsageOnceAtSettle (AC9); TestUsageWriter_ClientDisconnectMidStreamStillReports (E1); TestUsageWriter_NoWrapperWhenOnUsageNil (AC1); TestUsageWriter_IndependentOfCapture (AC10: capture off); TestRegistry_WithGuardWrapsProxy
- AC: AC1, AC9, AC10
- Commit: feat(proxy): observe response usage and add a registry guard
- Depends: T005
- Size: L
- Risk: high

### T007 — Add the rate-limit middleware
- Do: internal/ratelimit/middleware.go: model-call gate per adapter; exact `X-Gateway-Key` read; 403 for missing/empty/unknown key; 429 with `retry-after` (ceil, min 1) and `x-gateway-ratelimit*` headers; adapter-formatted bodies; one `ratelimit_rejected` log line; on admit set `Meta.OnUsage` to record tokens, warning `usage_missing` for a 2xx with no usage.
- Tests: TestMiddleware_MissingOrEmptyKey_403Permission (AC2, E8); TestMiddleware_UnknownKey_403Permission (AC3, E8: whitespace and case matched exactly); TestMiddleware_NonModelCall_NotRequiredNotCounted (AC4, E9); TestMiddleware_RPMExceeded_429NotForwarded (AC5); TestMiddleware_TPMExceeded_429NotForwarded (AC6); TestMiddleware_429Response_HeadersAndEnvelope (AC7); TestRetryAfter_RoundsUpMinOne (AC7); TestMiddleware_AdmittedCallCountedEvenIfUpstreamFails (AC8, E2); TestMiddleware_NoUsage2xx_WarnsNoTokens (AC11); TestMiddleware_Upstream5xx_NoTokensNoWarn (AC11, E2); TestMiddleware_RejectionLogsOneLine (AC15); TestMiddleware_WorksAgainstFakeLimiter (AC17); TestMiddleware_Upstream429NoGatewayHeaders (AC19)
- AC: AC2, AC3, AC4, AC5, AC6, AC7, AC8, AC11, AC15, AC17, AC19
- Commit: feat(ratelimit): add model-call middleware with 403 and 429
- Depends: T002, T005, T006
- Size: L
- Risk: high

### T008 — Wire the limiter into the gateway and prove it end to end
- Do: cmd/gateway/run.go: when `cfg.RateLimit.Enabled`, build the limiter and pass `core.WithGuard(ratelimit middleware)`; otherwise no change. Integration tests through `run()` with a fake upstream.
- Tests: TestRun_RateLimitDisabled_ProxiesWithoutKey (AC1); TestRun_InvalidRateLimitConfig_RefusesToStart (AC16); TestRun_RateLimitDisabled_GatewayKeyStillStrippedAndRedacted (AC1, AC18); TestRun_TokensRecordedFromStream (AC9, then next call 429 at tpm); TestRun_GzipStreamTokensRecorded (AC9: upstream serves internal/protocols/anthropic/testdata/stream.sse.gz, the same fixture, with Content-Encoding gzip; bytes forwarded identical; asserts the same usage total as the stream.sse test); TestRun_TokensCountedWithCaptureOff (AC10); TestRun_TokensCountedWhenExchangeDropped (AC10); TestRun_GatewayKeyNotForwardedAndRedacted (AC18, E9); TestRun_Upstream429PassedThroughVerbatim (AC19); TestRun_ClientDisconnectMidStreamRecordsSeenTokens (E1)
- AC: AC1, AC9, AC10, AC16, AC18, AC19
- Commit: feat(server): enable rate limiting from config
- Depends: T001, T003, T007
- Size: M
- Risk: high

### T009 — Document rate limiting and record the decision
- Do: `config.example.yaml` ratelimit block; `docs/clients/claude-code.md` (send `x-gateway-key` via `ANTHROPIC_CUSTOM_HEADERS`); ADR `docs/decisions/0008-in-memory-fixed-window-limiter.md` (including E5-E7 limits); PLAN.md: §8 "Rate-limit state" row, §7 Phase 5 goal, §5 stateless rule exception.
- Tests: none — docs only; `make verify` stays green
- AC: AC20, AC21
- Commit: docs(ratelimit): document the limiter and record the decision
- Depends: T008
- Size: S
- Risk: low
- Manual: run a real Claude Code session with `ratelimit.enabled` and `ANTHROPIC_CUSTOM_HEADERS="x-gateway-key: <name>"`; confirm every /v1/messages call (including sub-agent and side calls) is admitted and counted, and that an over-limit 429 makes Claude Code back off and retry (research Q1).

## Changelog
- v1 (2026-10-12) — generated from the plan; revised against spec v2 (new T004 streaming decoder, tasks renumbered)
