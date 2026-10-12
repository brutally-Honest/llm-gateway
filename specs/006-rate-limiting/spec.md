---
feature: 006-rate-limiting
kind: feat
status: draft
version: 2
created: 2026-10-12
supersedes:
branch: feat/006-rate-limiting
approved:
approved-by:
sha256:
fingerprint:
previous: 7025eed
---
# Rate limiting

## Problem
Nothing stops one caller from sending as many model calls or burning as many tokens as it
likes through the gateway. I want a per-caller ceiling on requests and tokens per minute,
enforced by the gateway itself, with well-formed 429s so clients back off and retry instead
of breaking.

The gateway has no identity of its own today: Claude Code sends its own Anthropic
credential, which is passed through. Keying limits on that credential doesn't work, because
subscription OAuth tokens refresh, and every new token would start a fresh, empty counter.
Config also couldn't name a key in advance. So the caller names itself with a gateway key
header, separate from the upstream credential.

This is Phase 5 of PLAN.md §7. It deliberately departs from the §8 decision (Redis token
bucket) for v1: in-memory, fixed window, behind a seam so the Redis strategy can replace it
without touching the middleware.

## Goal
- Every model call carries a gateway key; the gateway enforces that key's requests-per-minute
  (RPM) and tokens-per-minute (TPM) limits from config.
- Over a limit, the client gets a 429 in its protocol's error format with `retry-after`, and
  Claude Code backs off and retries cleanly.
- The limiter is one swappable strategy: a Redis implementation later needs no change to the
  middleware, config shape, or 429 behaviour.

## Non-goals
- Redis, or any limit state that survives a restart or is shared between instances.
- Token bucket or sliding window algorithms (fixed window only in v1).
- Per-tenant, per-model or combined-dimension limits; separate input/output token limits.
- Authentication: the gateway key is an identity label, not a secret. No hashing,
  validation against a store, or virtual-key issuance.
- Budgets or money of any kind.
- Concurrency (max in-flight requests) limits.
- Adding rate-limit headers to responses the gateway did not create.
- Reserving or estimating tokens before a call (no tiktoken on the hot path).
- Changing limits at runtime without a restart.
- Capturing rejected requests as exchanges; rejections are logged only.

## Acceptance criteria
Terms used below. A **model call** is a request on a route the protocol adapter marks as a
model call (for Anthropic: `POST /v1/messages`; not `count_tokens`, `/v1/models` or
`HEAD /api/hello`). The **gateway key** is the value of the `x-gateway-key` request header.
A **window** is a fixed 60-second interval aligned to the wall-clock
minute. A call's **tokens** are input + cache-write + output, with the counter meanings of
005's Terms; cache-read is not counted.

- **AC1** — When `ratelimit.enabled` is false (the default), the
  system shall proxy every request exactly as before this feature: no header required,
  nothing counted, nothing rejected.
- **AC2** — When limiting is enabled and a model call has no gateway key, or an empty one,
  the system shall answer 403 in the protocol's error envelope with its permission error
  type (for Anthropic: `permission_error`), with a message saying the gateway key header
  `x-gateway-key` is missing or not configured and not suggesting the upstream credential
  is bad; it shall not forward the call upstream, and shall count nothing.
- **AC3** — When limiting is enabled and a model call's gateway key is not listed in config,
  the system shall answer as in AC2.
- **AC4** — When limiting is enabled and a request is not a model call, the system shall
  proxy it without requiring a gateway key, counting it, or limiting it.
- **AC5** — When a key's admitted requests in the current window have reached its `rpm`, the
  system shall reject the next model call with a 429 and shall not forward it upstream.
- **AC6** — When a key's recorded tokens in the current window have reached its `tpm`, the
  system shall reject the next model call with a 429 and shall not forward it upstream.
- **AC7** — When the system rejects a model call for a limit, the response shall have
  status 429; the protocol's error envelope with its rate-limit error type and a message
  naming the key, the limit hit (`requests` or `tokens`), its value and the seconds to wait;
  a `retry-after` header of whole seconds until the window ends, rounded up, at least 1;
  `x-gateway-ratelimit: requests|tokens`; `x-gateway-ratelimit-limit`;
  `x-gateway-ratelimit-remaining`, clamped at 0 so it is never negative even when the
  window has overshot its limit (E4); and `x-gateway-ratelimit-reset` as RFC 3339.
- **AC8** — When a model call is admitted, the system shall count one request in the key's
  current window at admission, whether upstream later succeeds, fails or never answers.
- **AC9** — When an admitted model call's response finishes (a stream ending, a non-stream
  body complete, or the client disconnecting), the system shall add that call's tokens, from
  the usage the provider reported, to the key's window current at that moment.
- **AC10** — When capture is disabled, or the exchange is dropped from the capture queue,
  the system shall still count the call's tokens as in AC9.
- **AC11** — When a finished model call has no provider usage (an upstream error, or a stream
  that ended before usage arrived), the system shall add no tokens, and shall log a warning
  when the response status was 2xx.
- **AC12** — When a new window begins, the system shall start every key's request and token
  counts at 0; nothing carries over from the previous window.
- **AC13** — When a key's `rpm` or `tpm` is 0, the system shall not limit that dimension for
  that key.
- **AC14** — When N model calls for one key arrive concurrently with R < N requests remaining
  in the window, the system shall admit exactly R of them and reject the rest (verified
  under `-race`).
- **AC15** — When the system rejects a request (AC2, AC3, AC5, AC6), it shall log one line
  with the request ID, the key (or its absence), the reason, and `retry-after` when set.
- **AC16** — When config sets an unknown `ratelimit.strategy`, a negative limit, an empty key
  name, or `enabled: true` with no keys, the system shall refuse to start and name the
  offending field.
- **AC17** — When the middleware is run against a test-only `Limiter` implementation, it
  shall admit, reject (with AC7's response) and record usage purely through that interface,
  proving a strategy can be swapped without changing the middleware.
- **AC18** — When a request carries `x-gateway-key`, the system shall strip it before
  forwarding upstream and redact it in capture, on every route.
- **AC19** — When upstream itself answers 429, the system shall pass it through verbatim,
  with no `x-gateway-ratelimit*` headers added and no tokens recorded.
- **AC20** — When the change is merged, `config.example.yaml` shall document the `ratelimit`
  block, and `docs/clients/claude-code.md` shall show how Claude Code sends the gateway key
  (verified on a real session).
- **AC21** — When the change is merged, a new ADR shall record "in-memory fixed-window
  limiter for v1, behind a `Limiter` strategy; Redis token bucket deferred", and PLAN.md
  shall have: the §8 "Rate-limit state" row pointing to it with status changed; §7 Phase 5's
  goal matching this spec; and §5's stateless rule naming this as a documented, temporary
  exception.

## Edge cases
- **E1** — Client disconnects mid-stream → tokens from whatever usage was seen are recorded;
  none if no usage arrived (AC9, AC11)
- **E2** — Upstream 4xx/5xx for an admitted call → the request stays counted, no tokens
  (AC8, AC11)
- **E3** — A long stream starts in one window and ends in the next → its tokens go to the
  window in which it ended (AC9)
- **E4** — A call is admitted with 1 token left under `tpm` and uses 50k → admitted; the
  window overshoots and the next call is rejected until the window ends (AC6, AC9)
- **E5** — Bursts at a window edge: up to 2× `rpm` within a few seconds across the boundary →
  accepted fixed-window behaviour, documented in the ADR (AC12)
- **E6** — Gateway restart → all counts reset to 0; documented limit (AC12)
- **E7** — Two gateway instances → each counts separately, so the effective limit is
  doubled; documented limit, fixed by the Redis strategy (Non-goals)
- **E8** — Gateway key with surrounding whitespace or different case → matched exactly as
  sent; an empty value is treated as absent (AC2, AC3)
- **E9** — `x-gateway-key` on a non-model route → ignored for limiting, still stripped and
  redacted (AC4, AC18)
- **E10** — Concurrent burst at exactly the limit → exactly the remaining count admitted
  (AC14)

## Constraints
- Admission does no I/O and holds a lock only for an O(1) check-and-increment; it adds no
  measurable latency to a model call.
- Streams are never buffered or delayed to read usage; usage is observed on the bytes
  already being forwarded.
- Limit state is bounded by the number of configured keys: unknown keys are rejected before
  any state is created.
- The limit key is built from the principal and the gateway key (PLAN.md §2 seam), never
  hard-coded; the principal stays `local` in v1.
- No new dependency. No Redis in compose for this feature.
- Config: the `ratelimit` block lives in the YAML file; `enabled` and `strategy` are also
  settable from env, following ADR 0002 precedence. The only valid strategy in v1 is
  `fixed_window_memory`.
- Gateway-created 429s never reuse provider header names (`anthropic-ratelimit-*`), so the
  two are never confused.

## Agnosticism check
Touches the **protocol** axis: which routes are model calls, how provider usage is read from
a live response, and the error envelope's permission and rate-limit types all come from
the adapter, so core names no route, header or wire field. The Anthropic adapter supplies
them for `POST /v1/messages` and its SSE `message_start` / `message_delta` usage.

Touches **core**: a hook to observe a response's usage on the forwarded bytes, needed so
tokens are counted without depending on async capture (AC10). This is justified because
capture can be off or drop exchanges by design ("capture never blocks"). The limiter and its
middleware live outside core, in `internal/ratelimit`.

The **client** axis is touched only in docs (how Claude Code sends the header). Resolves no
OQ; departs from a §8 decision, recorded by a new ADR (AC21).

## Open questions
None. The header name, default-off, 403 `permission_error`, strip+redact and log-only rejections were confirmed by the user on 2026-10-12.

## Changelog
- v1 (2026-10-12) — first draft
  - 403 permission_error instead of 401 for a missing or unknown gateway key; remaining clamped at 0
- v2 (2026-10-12) — reopened: AC1: when ratelimit.enabled is false, nothing is counted, limited or required, and every request is proxied as before this feature, except that x-gateway-key is still stripped and redacted on every route (AC18). Reason: the header is addressed to the gateway, and redacting a future secret must not depend on a toggle.
