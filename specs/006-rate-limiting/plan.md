---
feature: 006-rate-limiting
status: done
version: 1
approved: 2026-10-12T16:11:38Z
approved-by: human
sha256: 9824aada05f50824044ce410ca82830774aa189040106f05c40183ab09433e88
fingerprint: 4d911d754ca478c402ed6968fad6ece8a4429a47414281bcd0533d0b899d1e3c
spec-fingerprint: 5d1b5daa97e1a0276b794565cb198c916fa578fd482fc01aa20ef10903d70acd
---
# Plan — 006-rate-limiting

## Summary
A new `internal/ratelimit` package holds a `Limiter` strategy interface, an in-memory
fixed-window implementation, and an HTTP middleware that gates model calls by the
`x-gateway-key` header. Core gains two small protocol-neutral hooks: a response-usage
observer on the forwarded bytes (so tokens count even with capture off or dropped), and
stripping/redacting `x-gateway-key`. The Anthropic adapter supplies what is protocol-shaped:
which route is a model call, the 403/429 envelopes, and the usage reader.

## Context
- `internal/core/proxy.go` — `Proxy.ServeHTTP` wraps the writer (capture tee) and `rewrite`
  builds the upstream request; `internal/core/meta.go` `Meta.Settle` runs from the access
  log's defer after every handler, including client disconnects and aborted streams.
- `internal/core/capture.go` / `tee.go` — the pattern for a writer wrapper that copies
  forwarded bytes (`teeWriter`: inner write first, `Unwrap` for `ResponseController`).
- `internal/core/redact.go` — `secretHeaders` list; `internal/core/registry.go` —
  `RegistryOption`, `AddAdapter`, `Mount`.
- `internal/protocols/anthropic/{adapter,response,stream}.go` — error envelope,
  `usageCounters` (the 005 counter meanings), SSE usage merge (`mergeUsage`).
- `internal/contentcoding/decode.go` — today only `Decode(encoding, []byte, limit)` (whole body in memory;
  gzip/deflate/br/zstd via deps already in go.mod); no streaming API, so this feature adds one.
- `internal/config/{config,load,capture}.go` — dotted-key settings, `Error{Key,Source,Reason}`,
  env via `envName`, ADR 0002 precedence.
- `cmd/gateway/run.go` — the one place that wires config into the registry.
- `internal/server/middleware.go` — `accessLog` creates `Meta` and calls `Settle`.

## Approach
The limiter is keyed by the gateway key (principal `local` prefixed, per PLAN §2 seam) and
knows nothing of HTTP. The middleware runs per adapter, inside the access log, installed
through a new `core.WithGuard` registry option so core stays ignorant of rate limiting. On
admit it counts the request and hands `Meta` a callback; core's usage writer observes the
forwarded response bytes through an adapter-supplied `UsageReader`, and `Settle` calls the
callback once with the tokens. The callback calls `Limiter.Record`.

## Alternatives considered
### A1 — Observe usage on forwarded bytes in core, hand tokens back via Meta (chosen)
- Pros: works with capture off or dropped (AC10); no buffering of streams; core stays generic.
- Cons: a second response-writer wrapper next to the capture tee; new optional adapter interface.

### A2 — Read tokens from the capture pipeline's parsed usage event
- Pros: reuses the parser; no new response wrapper.
- Cons: capture is async, can be disabled or drop exchanges (breaks AC10); couples limiting to the store.

### A3 — Token bucket with pre-call estimate (tiktoken)
- Pros: rejects before the call burns tokens; smoother than fixed windows.
- Cons: spec non-goal (no estimation on hot path); adds a dependency; Redis-style complexity.

## Design
**Config (AC1, AC16).** `config.RateLimit{Enabled bool, Strategy string, Keys map[string]KeyLimit{RPM, TPM int64}}`;
defaults `Enabled=false`, `Strategy="fixed_window_memory"`. Settings `ratelimit.enabled`
and `ratelimit.strategy` join `settingsFor` so env works (`GATEWAY_RATELIMIT_ENABLED`);
the `keys` block is file-only: `parseFile` walks a `ratelimit` block, one level of scalars plus
`keys.<name>.{rpm,tpm}`, rejecting unknown keys/duplicates like the other blocks. After file
and env are applied, a validation pass returns `*Error` with key `ratelimit.strategy`,
`ratelimit.keys.<name>.rpm|tpm` (negative or non-integer), `ratelimit.keys` (empty name, or
enabled with no keys). Validation applies only when relevant: disabled limits with bad
keys still fail on syntax/negatives, but "no keys" only matters when enabled.

**Limiter (AC5-AC9, AC12-AC14, AC17).** `internal/ratelimit/limiter.go`:
`Limiter{ Admit(key string, now time.Time) Decision; Record(key string, tokens int64, now time.Time) }`.
`Decision{Outcome (Admitted|UnknownKey|Limited), Dimension ("requests"|"tokens"), Limit, Remaining int64, Reset time.Time}`.
Membership lives in the limiter so a Redis strategy owns it too. The memory strategy builds
its key map once from config (read-only afterwards: state is bounded by configured keys; an
unknown key creates nothing); each entry has its own mutex guarding `{window int64, reqs, toks}`.
The window is `now.Unix()/60`, reset lazily when an entry sees a newer window, in both
`Admit` and `Record` (so a stream ending in a new window lands in that window: E3).
`Admit` under the entry lock: rpm>0 and reqs>=rpm → Limited(requests); else tpm>0 and
toks>=tpm → Limited(tokens); else reqs++ → Admitted. Requests are checked first. 0 means
unlimited (AC13). `Remaining` is clamped at 0 (E4). A `NewFixedWindowMemory(cfg, clock)` takes
an injectable clock for tests. `New(cfg)` selects the strategy by name (only `fixed_window_memory`).

**Core hooks (AC9, AC10, AC18, E9).** In `internal/core`:
- `RateLimitAdapter` optional interface (like `ParsingAdapter`): `IsModelCall(method, path string) bool`
  (path with the prefix stripped), `LimitErrorBody(kind LimitErrorKind, message string) (contentType string, body []byte)`
  (kinds: permission, rate_limit), `UsageReader(status int, h http.Header, stream bool) UsageReader`.
  `UsageReader{ Write(p []byte); Tokens() (n int64, ok bool) }` — input + cache-write + output, cache-read excluded,
  `ok` false when no usage was seen. Readers never panic out (recovered in the writer).
- `Meta.OnUsage func(UsageResult)` (exported, set by the middleware) with `UsageResult{Tokens int64; OK bool; Status int}`.
  `Proxy.ServeHTTP` wraps the writer in a `usageWriter` only when `m.OnUsage != nil` and the adapter
  implements `RateLimitAdapter` (so disabled = no wrapper, AC1). The wrapper forwards inner first, then feeds
  accepted bytes to the reader, records status/headers on `WriteHeader`, and has `Unwrap`.
  `Meta.Settle` finishes it and calls `OnUsage` exactly once (also on client disconnect / aborted stream, E1).
- `core.WithGuard(func(Adapter) func(http.Handler) http.Handler)` registry option; `AddAdapter` wraps the
  proxy; `mounted` holds an `http.Handler`.
- `rewrite` order: tee snapshot first, then `Header.Del("X-Gateway-Key")` after `teeRequest`, so upstream never
  sees it while the captured request header shows `[REDACTED]` (AC18); `X-Gateway-Key` is added to core's
  `secretHeaders`. Both unconditional, on every route, enabled or not, including the default
  `ratelimit.enabled=false` (AC1, AC18, E9). The header is gateway-owned, not provider-shaped.

**Streaming decoder in contentcoding (AC9, AC10).** `contentcoding` gains, with no new dependency (gzip,
zlib/flate, `andybalholm/brotli`, `klauspost/compress/zstd` are already imported there):
- `NewReader(encoding string, src io.Reader) (io.ReadCloser, Status)`: parses the header with the existing
  `parse`, chains one streaming decoder per coding in reverse order; `Unsupported` (nil reader) for an unknown
  coding. Deflate sniffs zlib vs raw with `bufio.Reader.Peek(2)` (the streaming twin of `isZlib`). `Decode`
  and its tests stay unchanged.
- `Feeder`: `NewFeeder(encoding string, sink func(io.Reader), maxPending int)`; `Write(p)` copies p into a
  bounded queue and returns at once, never blocking the caller (a full queue marks it `Overflowed` and later
  bytes are dropped). One goroutine reads the queue through `NewReader` and hands the decoded stream to `sink`.
  `Close()` ends input, waits for the goroutine and reports `Complete | CutShort | Unsupported | Overflowed`.
  A decoded-output cap (64 MiB) stops a decompression bomb; after a decode error or the cap the goroutine
  drains and discards, so it never leaks. Forwarded bytes are never buffered, delayed or blocked by decoding:
  only a bounded copy of the compressed side stream (1 MiB) waits for the decoder.

**Anthropic adapter (AC2, AC7, AC9, AC11, AC19).** `IsModelCall` is `POST` + `/v1/messages`
exactly (so `count_tokens`, `/v1/models`, `HEAD /api/hello` are not). `LimitErrorBody` uses the existing
`errorEnvelope` with types `permission_error` / `rate_limit_error`. `UsageReader` chooses by `stream`:
- Stream (SSE): bytes go through a `contentcoding.Feeder` when `Content-Encoding` names any coding (gzip,
  deflate, br, zstd, stacked), or straight to the scanner when identity. Compressed SSE is the normal case:
  Claude Code sends `Accept-Encoding: gzip, deflate, br, zstd` and Anthropic answered with
  `Content-Encoding: gzip` (verified 2026-10-12). An incremental SSE scanner keeps only the current event
  (capped at 1 MiB, an oversized event is skipped) and merges `usage` of `message_start` and `message_delta`
  through a shared accumulator (last value wins, as `streamState.mergeUsage`). A stream cut mid-way yields the
  usage decoded so far (E1).
- Non-stream JSON: the decoded stream (Feeder, or direct if identity) goes into a 4 MiB capped buffer read by
  the existing `readResponse`/`usageEvent` at `Tokens()`; past the cap there are no tokens (warned like AC11).
- Non-2xx statuses do no reader work: `ok=false`. An `Unsupported` or `Overflowed` result also gives `ok=false`
  (fails open; the middleware warns for 2xx).

**Middleware (AC2-AC8, AC11, AC15, AC17, AC19).** `ratelimit.Middleware(l Limiter, a core.Adapter, log *zap.Logger)`.
If `a` is not a `RateLimitAdapter`, or not `IsModelCall`, it passes the request through untouched (AC4).
Else: read `X-Gateway-Key` exactly (E8); empty → 403 permission; call `Admit`; `UnknownKey` → the same 403
(message names `x-gateway-key` and never echoes the supplied value, never says the upstream credential is bad);
`Limited` → 429 with the AC7 headers: `Retry-After` = ceil(reset-now) seconds, min 1;
`X-Gateway-Ratelimit`, `-Limit`, `-Remaining` (clamped), `-Reset` (RFC 3339, UTC); body from `LimitErrorBody`,
message naming key, dimension, limit, seconds. Rejections never reach the proxy, so nothing is forwarded or
counted beyond AC5's own count. Each rejection logs one line `ratelimit_rejected` with `request_id` (from `Meta`),
`key` (or `""` / `absent`), `reason` (`missing_key|unknown_key|requests|tokens`), `retry_after` when set.
On `Admitted` it sets `Meta.OnUsage`: `OK` → `Record(key, tokens, now)`; not OK and 2xx status → `Warn("usage_missing")`
(AC11). Upstream 429s are plain upstream responses: no gateway headers, `OK=false` (AC19).

**Wiring (AC1, AC10).** `run.go`: when `cfg.RateLimit.Enabled`, build the limiter with `ratelimit.New` and pass
`core.WithGuard(...)`; otherwise no option is passed and the proxy path is byte-for-byte as before.
The principal prefix comes from `core.LocalPrincipal` via `Meta.PrincipalID`, never hard-coded in the limiter.

**Concurrency.** Per-key mutex held for O(1) work only; no I/O under it. `OnUsage` runs on the handler goroutine
from `Settle`. **Tenancy/auth.** The gateway key is a label, not a secret, but it is stripped and redacted anyway.
No migrations. No new dependency.

**Docs (AC20, AC21).** `config.example.yaml`, `docs/clients/claude-code.md` (header through
`ANTHROPIC_CUSTOM_HEADERS`, verified live), ADR 0008, PLAN.md edits.

## AC coverage
| AC | Where | Verified by |
|----|-------|-------------|
| AC1 | `config` default; `run.go` wiring; no wrapper when `OnUsage` nil; strip/redact unconditional | `TestRun_RateLimitDisabled_ProxiesWithoutKey`, `TestRun_RateLimitDisabled_GatewayKeyStillStrippedAndRedacted`, `TestLoad_RateLimitDefaultsOff` |
| AC2 | `ratelimit/middleware.go`, anthropic `LimitErrorBody` | `TestMiddleware_MissingOrEmptyKey_403Permission` |
| AC3 | `ratelimit/limiter.go` (UnknownKey), middleware | `TestMiddleware_UnknownKey_403Permission` |
| AC4 | middleware passthrough, anthropic `IsModelCall` | `TestMiddleware_NonModelCall_NotRequiredNotCounted`, `TestIsModelCall_OnlyMessagesPost` |
| AC5 | limiter `Admit`, middleware | `TestFixedWindow_RejectsOverRPM`, `TestMiddleware_RPMExceeded_429NotForwarded` |
| AC6 | limiter `Admit`/`Record`, middleware | `TestFixedWindow_RejectsAtTPM`, `TestMiddleware_TPMExceeded_429NotForwarded` |
| AC7 | middleware headers/body | `TestMiddleware_429Response_HeadersAndEnvelope`, `TestRetryAfter_RoundsUpMinOne` |
| AC8 | limiter `Admit` count | `TestMiddleware_AdmittedCallCountedEvenIfUpstreamFails` |
| AC9 | core `usageWriter`, `Meta.Settle`, middleware callback | `TestUsageWriter_CallsOnUsageOnceAtSettle`, `TestRun_TokensRecordedFromStream`, `TestUsageReader_GzipSSEStream`, `TestRun_GzipStreamTokensRecorded` |
| AC10 | core observer independent of capture | `TestRun_TokensCountedWithCaptureOff`, `TestRun_TokensCountedWhenExchangeDropped` |
| AC11 | anthropic reader `ok=false`; middleware warn | `TestMiddleware_NoUsage2xx_WarnsNoTokens`, `TestMiddleware_Upstream5xx_NoTokensNoWarn` |
| AC12 | limiter lazy rollover | `TestFixedWindow_NewWindowResetsCounts` |
| AC13 | limiter 0 = unlimited | `TestFixedWindow_ZeroLimitUnlimited` |
| AC14 | limiter per-key mutex | `TestFixedWindow_ConcurrentBurstAdmitsExactlyRemaining` (-race) |
| AC15 | middleware rejection log | `TestMiddleware_RejectionLogsOneLine` |
| AC16 | `config` validation | `TestLoad_RateLimitInvalid_NamesField` |
| AC17 | `Limiter` interface, middleware | `TestMiddleware_WorksAgainstFakeLimiter` |
| AC18 | `core.rewrite`, `redact.go` | `TestRewrite_StripsGatewayKey`, `TestCapture_RedactsGatewayKey` |
| AC19 | middleware/upstream passthrough | `TestRun_Upstream429PassedThroughVerbatim` |
| AC20 | `config.example.yaml`, `docs/clients/claude-code.md` | manual: real Claude Code session (T009) |
| AC21 | `docs/decisions/0008-*.md`, `PLAN.md` | manual review of the diff (T009) |

## Test strategy
- Limiter: `TestFixedWindow_ConcurrentBurstAdmitsExactlyRemaining (AC14, E10)`,
  `TestFixedWindow_OvershootRejectsUntilWindowEnds (E4)`, `TestFixedWindow_StreamEndingInNextWindowCountsThere (E3)`,
  `TestFixedWindow_NewWindowResetsCounts (AC12)`, remaining clamped at 0.
- contentcoding: `TestNewReader_StreamsEachCoding` (gzip, deflate zlib and raw, br, zstd, stacked; fed in 1-byte
  chunks), `TestNewReader_UnknownCodingUnsupported`, `TestFeeder_WriteNeverBlocksOnSlowSink`,
  `TestFeeder_OverflowMarksOverflowedAndDrops`, `TestFeeder_BombStopsAtOutputCap`, `TestFeeder_CloseDrainsNoLeak`,
  `TestFeeder_TruncatedInputReportsCutShort`.
- Anthropic reader: stream golden (`testdata/stream.sse`) token total equals the parser's usage event total;
  `TestUsageReader_GzipSSEStream (AC9)` feeds `testdata/stream.sse.gz` in small chunks; its siblings feed
  the br, zstd and deflate variants the same way. Each fixture is generated by compressing the existing
  recorded `internal/protocols/anthropic/testdata/stream.sse` byte for byte, with no hand edits, and each
  test asserts the same usage total as the uncompressed test on `stream.sse`. The 2026-10-12 capture is not
  a fixture source (it holds org, workspace and session identifiers); every fixture passes
  `TestFixtures_NoIdentifiers`. `TestUsageReader_CutGzipStreamStillReportsSeenUsage (E1)`; gzip JSON body
  decoded; oversize JSON body gives no tokens.
- Core: `usageWriter` forwards bytes identically, `Flush` still works through `Unwrap`, `OnUsage` once even on abort.
- Middleware: every AC row above with a fake adapter/limiter plus the anthropic adapter.
- End to end through `run()` with a fake upstream: AC1 byte-identical proxying, 403/429 flows, token recording
  with capture off/on/dropped, `x-gateway-key` absent upstream and `[REDACTED]` in the store (E9),
  `TestRun_KeyWithWhitespaceOrCaseMatchedExactly (E8)`.
- E5, E6, E7 are documented limits (ADR), not tests.

## Risks
- Counting drift if `OnUsage` is skipped on a panic path: `Settle` runs in a defer, tested with an aborted stream.
- Decoding compressed SSE on a side goroutine: a slow decoder could grow the queue or leak a goroutine. Limit on
  damage: queue capped at 1 MiB (overflow means no tokens, fails open, warned), `Close` always drains, forwarded
  bytes never wait on it, a decompression bomb stops at the output cap.
- A new streaming API in the shared `contentcoding` package; `Decode` and its tests stay unchanged.
- The usage wrapper sits on the streaming path: it must never delay or reorder bytes; tests compare forwarded bytes.
- Fixed-window boundary bursts (2x rpm) and per-instance counts: documented in the ADR.
- Research Q1 (answered): Claude Code 2.1.296 sends `ANTHROPIC_CUSTOM_HEADERS` on every /v1/messages call,
  including sub-agent and side calls (verified 2026-10-12), so the header approach holds; the manual check in
  T009 only re-confirms it for the docs (AC20).

## Open questions
None.

## Changelog
- v1 (2026-10-12) — first draft, revised against spec v2: AC1 strip/redact when disabled; streaming content decoding replaces the 4 MiB side copy; Q1 answered
