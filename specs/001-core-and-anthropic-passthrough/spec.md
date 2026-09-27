---
status: done
branch: feat/001-core-and-anthropic-passthrough
---

# 001 — Core and Anthropic passthrough

## Intent
Put the gateway on the wire. Claude Code, in both API-key and subscription mode, is
pointed at the gateway. Every request it makes reaches Anthropic unchanged, and every
response comes back unchanged, streams included, with no behaviour the user can tell
apart from a direct connection. Each request is labelled with the protocol and client
that sent it.

This serves priority 1, observability (PLAN.md §4). Nothing can be captured until
traffic flows through the gateway, and a capture of traffic the gateway has altered is
worthless. It also builds the first two extension points from PLAN.md §2, the protocol
adapter and the client profile, so 002 (capture) and 003 (OpenAI + Cursor) plug in
without changing the core.

Still local only. There is still no gateway auth, so the default bind stays
`127.0.0.1:7197` (000).

## Scope
- **Core.** A protocol-agnostic proxy that forwards a request to an upstream URL and
  streams the response back. It also holds an adapter interface, a client-profile
  interface, and a registry that falls back to the client `unknown`.
- **Anthropic Messages adapter.**
  - **Mount:** the prefix `/anthropic`. Every path under it is forwarded, with the
    prefix stripped, to the configured upstream.
  - **No route list.** The paths PLAN.md §7 names (`POST /v1/messages`,
    `POST /v1/messages/count_tokens`, `GET /v1/models`, `GET /v1/models/{id}`,
    `HEAD /api/hello`) are test cases, not a route list. An unknown path under the
    prefix is forwarded too.
  - **Query string** always forwarded (Claude Code sends `?beta=true`).
  - **Upstream path prefix:** a path in `base_url` is joined in front. With
    `https://host/api`, `/anthropic/v1/messages` goes to `https://host/api/v1/messages`.
  - **Outside the prefix:** a path under neither `/anthropic` nor `/healthz` gets the
    gateway's `404` and is never forwarded.
- **`HEAD /api/hello`** is forwarded like any other path. The client gets upstream's
  status and headers with an empty body. The gateway never answers it itself.
- **Claude Code client profile: detection only.**
  - Match: `User-Agent` starts with `claude-cli/`, or `x-app: cli` is present.
  - Label: a matching request is `claude-code`; anything else is `unknown` and is proxied
    the same way.
- **Upstream config** (ADR 0002 rules: defaults < file < env, errors name key and
  source, never value). Nested under the adapter's name:

  | Key | Env var | Default |
  |---|---|---|
  | `upstreams.anthropic.base_url` | `GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL` | `https://api.anthropic.com` |
  | `upstreams.anthropic.connect_timeout` | `GATEWAY_UPSTREAMS_ANTHROPIC_CONNECT_TIMEOUT` | `10s` |
  | `upstreams.anthropic.tls_handshake_timeout` | `GATEWAY_UPSTREAMS_ANTHROPIC_TLS_HANDSHAKE_TIMEOUT` | `10s` |
  | `upstreams.anthropic.response_header_timeout` | `GATEWAY_UPSTREAMS_ANTHROPIC_RESPONSE_HEADER_TIMEOUT` | `10m` |

  - `base_url` must be an absolute URL:
    - scheme `https`, or `http` only when the host is loopback (`localhost`,
      `127.0.0.0/8`, `::1`);
    - a non-empty host;
    - no userinfo, query or fragment;
    - a path is allowed.

    Anything else fails fast with the reason `invalid url`.
  - The timeouts use 000's duration rules (Go syntax, `> 0`, reason `invalid duration`).
  - An unknown key inside `upstreams`, or an upstream name with no adapter, fails fast
    with `unknown key`, naming the dotted key path.
  - There is no timeout on the request as a whole: streams run as long as upstream
    sends.
  - `config.example.yaml` gains these keys at their defaults.
- **Auth passthrough.**
  - **What is forwarded:** `x-api-key` (API-key mode), and `Authorization: Bearer …` plus
    `anthropic-beta` (subscription mode), byte-for-byte.
  - **What the gateway does with them:** it never reads their values, never injects a
    credential of its own, and never logs them.
- **Headers.**
  - **Request.** Everything is forwarded except the RFC 7230 hop-by-hop headers:
    `Connection` and every header it lists, `Keep-Alive`, `TE`, `Trailer`,
    `Transfer-Encoding`, `Upgrade`, `Proxy-*`.
    - `Host` becomes the upstream host.
    - No `X-Forwarded-*` header is added.
    - The gateway's request ID is not sent upstream.
    - A client-sent `X-Request-Id` is forwarded unchanged.
  - **Response.** Everything is forwarded except hop-by-hop.
    - The gateway's `X-Request-Id` overwrites any upstream `X-Request-Id`, keeping 000
      AC18.
    - Anthropic's `request-id` passes through untouched.
- **Compression.**
  - `Accept-Encoding` goes upstream exactly as the client sent it. If the client sent
    none, none is added.
  - `Content-Encoding` and the encoded body come back unchanged.
  - Go's transparent gzip is disabled (`DisableCompression`), so the gateway never
    decompresses.
- **Streaming.**
  - Each SSE event, keep-alive `ping`s included, reaches the client as upstream sends
    it, with no buffering.
  - The upstream `content-type` is kept.
- **Errors.**
  - **Upstream errors** pass through verbatim: status, body, `retry-after`,
    `x-should-retry`, `anthropic-ratelimit-*` and every other header.
  - **Errors the gateway creates** use the adapter's native error envelope, with an
    `x-gateway-error: <reason>` header:
    - `502` / `upstream_unreachable`: DNS, connection or TLS failure.
    - `504` / `upstream_timeout`: connect, TLS-handshake or response-header timeout.
    - `400` / `client_body`: the client's own request body is malformed on a live
      connection, such as broken chunked framing (research Q11, Q19). It is never a
      `502`. A client that has already gone is not this: see "Client disconnects"
      below. A body that ends short of its `Content-Length` counts as a client that
      has gone (`client_disconnected`), because Go cancels the request context then.

    The Anthropic envelope is
    `{"type":"error","error":{"type":"api_error","message":"gateway: <reason>"}}`.
  - **No retries.** The gateway never retries: one client request is at most one
    upstream request, except Go's transport replaying a request upstream never saw,
    or a bodyless idempotent one (research Q21).
  - **Client disconnects mid-request:** the upstream request is cancelled.
  - **Upstream fails after response headers were sent:** the client connection is
    aborted. No event is invented.
- **Request body.** No size limit in the gateway. The body is streamed, not held in
  memory, and upstream's own `413` passes through.
- **Access log.** 000's `request` line gains these fields on proxied requests:
  - `protocol` (`anthropic`);
  - `client` (`claude-code` / `unknown`);
  - `auth` (`api_key` / `bearer` / `both` / `none`: which header is present, never its value);
  - `stream` (the response is `text/event-stream`);
  - `ttfb_ms` (time to upstream response headers);
  - `gateway_error` (the reason, when the gateway created the error);
  - `client_disconnected` (true when the client went away before the response ended);
  - `upstream_aborted` (true when upstream failed after response headers were sent and
    the gateway aborted the client connection; research Q12).

  No headers, body or query string, as in 000. No `model`: that needs body parsing (002).
- **Carry-overs from 000.**
  - **Shutdown vs long streams.** `shutdown_timeout`'s default rises from `30s` to
    `10m`, so in-flight streams can finish. A second SIGINT/SIGTERM still exits at once
    (000). `docker-compose.yml` sets `stop_grace_period` to match, because Docker's
    default kills after 10s.
  - **Panic values.** The panic-recovery log line carries the panic value's type and
    the stack, never the value, because a value can hold request data.
  - **Goroutine panics.** Every goroutine the gateway starts goes through one helper
    that recovers a panic and logs it as one JSON line (type and stack, never the
    value).
- **Golden fixture.**
  - One real Anthropic streaming response is recorded and committed as a test fixture,
    then replayed through the gateway in tests.
  - Before commit it is scrubbed of every auth, account and organisation identifier,
    including API keys, bearer tokens, org and account IDs and emails. A test enforces
    it.
- **Docs.** `docs/clients/claude-code.md` covers:
  - how to point Claude Code at the gateway
    (`ANTHROPIC_BASE_URL=http://127.0.0.1:7197/anthropic`), in API-key and subscription
    mode;
  - the known limits from PLAN.md §3;
  - the manual smoke checklist;
  - that stopping the gateway, including `docker compose stop`, can take up to
    10 minutes while streams finish, and that a second Ctrl-C stops `make run` at once.

## Out of scope
- **Capture and parsing:** tee, capture sink, redaction of captured bodies, the store,
  parsing any body or stream, canonical events (002).
- **Cost:** usage extraction, pricing tables, cost of any kind, token estimation or
  auditing (Phase 5).
- **Rate limiting,** Redis, budgets (Phase 6).
- **Routing:** model aliases, weighted selection, fallback, circuit breakers, gateway
  retries (Phase 8).
- **Auth:** gateway token auth, virtual keys, principals beyond the existing seam, any
  route reachable off loopback, tunnels (003 and later).
- **OpenAI:** the OpenAI Chat Completions adapter, OpenAI Responses, Gemini, Bedrock,
  Vertex, and every other protocol adapter (003 and later).
- **Other clients:** the Cursor profile and every other client profile (003 and later).
- **Sessions:** session-ID or agent-ID extraction (`x-claude-code-session-id`), session
  grouping, timeline, viewer (Phase 4).
- **Credentials:** injecting upstream credentials from config or env; every request
  uses the client's own auth.
- **More than one upstream per adapter,** and choosing an upstream per request.
- **Cross-protocol translation.**
- **Non-standard HTTP methods** (405 from the router; research Q20).
- **Changing the traffic:** decompressing, reshaping or rewriting any request or
  response body, and inventing SSE events.
- **A gateway request-body size limit** (revisit in 003, when the tunnel exposes the
  gateway).
- **`model`** or any other body-derived field in logs.
- **Self-observability:** Prometheus metrics, OpenTelemetry tracing (Phase 9).
- **Hot config reload.**
- **Secrets inside prompt or tool-result content** (OQ-5, 002).
- **Tests against the live API in `make verify`.**

## Do
- Forward bytes faithfully. The request and response bodies stream through untouched.
- Treat headers as an open list: strip only hop-by-hop, never allowlist.
- Keep every identifier that names a provider, protocol or client (`anthropic`,
  `claude`, `x-api-key`, SSE event names) inside `internal/protocols/<name>/` and
  `internal/clients/<name>/`.
- Pass the client's auth through as-is, and keep the default bind on loopback.
- Fail fast on invalid upstream config before binding, naming the key and source,
  never the value (ADR 0002).
- Cancel the upstream request when the client goes away.
- Scrub the golden fixture before it is committed.

## Don't
- **Don't buffer a response or a stream,** or add a request timeout that can cut one.
  Streams are sacred (PLAN.md §5).
- **Don't retry upstream.** A retry duplicates cost and belongs to routing (Phase 8).
- **Don't answer a forwarded path locally,** `HEAD /api/hello` included. A local
  answer hides an unreachable upstream.
- **Don't add `X-Forwarded-*`** or any other header upstream. Anthropic should see what
  the client sent.
- **Don't decompress, re-encode or reshape bodies,** and don't invent SSE events. That
  breaks fidelity, prompt caching and thinking signatures (PLAN.md §5).
- **Don't log header values, bodies, query strings or panic values.** Secrets never
  land in storage, and logs count (PLAN.md §5).
- **Don't start a goroutine with a bare `go` statement** outside the recovery helper.
  A panic there would crash the process with a plain-text trace (000 plan, Risks).
- **Don't put provider- or client-specific code in `internal/core/`.**
- **Don't add a dependency without an ADR.**

## Agnosticism check
This touches all three axes for the first time:
- **Client:** the Claude Code profile.
- **Protocol:** the Anthropic Messages adapter.
- **Provider:** the upstream config under `upstreams.anthropic`.

`internal/core/` is created here. That's justified because 000 deferred it to the first
feature with code for it, and it holds only the protocol-agnostic proxy, the adapter
and profile interfaces, the registry, and the per-request metadata the access log
reads. Two tests keep it that way:
- one fails if `internal/core/` names a provider, protocol or client;
- one registers a test-only adapter and profile and proxies through them with no change
  to core.

## Open questions resolved here
- 000's open question "Shutdown for long SSE streams" → resolved in Scope
  (`shutdown_timeout` default `10m`, compose `stop_grace_period`). No PLAN.md §9 OQ is
  resolved here.

## Acceptance criteria
Config
- **AC1** `TestLoad_UpstreamDefaults` — with no file and no env, `base_url` is
  `https://api.anthropic.com`, `connect_timeout` and `tls_handshake_timeout` are `10s`,
  `response_header_timeout` is `10m`, and `shutdown_timeout` is `10m`.
- **AC2** `TestLoad_UpstreamEnvOverridesFile` — each `GATEWAY_UPSTREAMS_ANTHROPIC_*`
  var overrides the file value, and the startup line names the overridden key.
- **AC3** `TestLoad_InvalidUpstreamURL` — each of these fails with `invalid url`,
  naming the key and source, and the value appears nowhere in the output:
  - a relative URL;
  - an empty host;
  - userinfo, a query or a fragment;
  - a scheme other than `http`/`https`;
  - `http` to a non-loopback host.
- **AC4** `TestLoad_UpstreamURLAccepted` — these load:
  - `https://api.anthropic.com`;
  - `https://host/api` (a path prefix);
  - `http://127.0.0.1:<port>`, `http://localhost:<port>` and `http://[::1]:<port>`.
- **AC5** `TestLoad_InvalidUpstreamDuration` — `abc`, `0s` and `-5s` for each upstream
  timeout, from the file and from env, give `invalid duration`.
- **AC6** `TestLoad_UnknownUpstreamKey` — an unknown key under `upstreams.anthropic`, and
  an unknown upstream name under `upstreams`, each fail with `unknown key` naming the
  dotted path.
- **AC7** `TestLoad_ExampleFileIsDefaults` — still passes with the new keys in
  `config.example.yaml`.

Forwarding
- **AC8** `TestProxy_ForwardsRequestVerbatim` — method, path (prefix stripped), query
  and body arrive upstream byte-identical, and so does every header except hop-by-hop.
- **AC9** `TestProxy_AnthropicRoutes` — a table over `POST /anthropic/v1/messages`,
  `POST /anthropic/v1/messages/count_tokens`, `GET /anthropic/v1/models`,
  `GET /anthropic/v1/models/{id}` and `HEAD /anthropic/api/hello`. Each reaches upstream
  at the path without the prefix, and `?beta=true` is kept.
- **AC10** `TestProxy_UnknownPathUnderPrefixForwarded`.
- **AC11** `TestRouter_PathOutsidePrefixIs404` — the gateway's `404`, no upstream call,
  and `/healthz` still `200`.
- **AC12** `TestProxy_BaseURLPathPrefixJoined`.
- **AC13** `TestProxy_HeadAPIHelloReturnsUpstreamStatus` — upstream's status and
  headers, with an empty body.
- **AC14** `TestProxy_StripsHopByHopHeaders` — in both directions, including headers
  that `Connection` lists.
- **AC15** `TestProxy_SetsUpstreamHost`.
- **AC16** `TestProxy_AddsNoHeadersUpstream` — no `X-Forwarded-*`, no gateway request
  ID, and a client `X-Request-Id` arrives unchanged.
- **AC17** `TestProxy_AuthHeadersForwardedUnchanged` — `x-api-key`, and
  `Authorization: Bearer` + `anthropic-beta`, arrive byte-identical.
- **AC18** `TestProxy_ResponseVerbatim` — status, headers and body reach the client
  byte-identical, except hop-by-hop headers and `X-Request-Id`.
- **AC19** `TestProxy_GatewayRequestIDWinsOnResponse` — an upstream `X-Request-Id` is
  replaced by the gateway's, and `request-id` passes through.
- **AC20** `TestProxy_LargeBodyNotLimited` — a 64 MiB body is forwarded whole, with no
  gateway `413`.

Compression
- **AC21** `TestProxy_CompressionPassThrough` — `Accept-Encoding: gzip` reaches upstream
  as sent, and a gzip body with `Content-Encoding: gzip` reaches the client
  byte-identical, still compressed.
- **AC22** `TestProxy_TransparentGzipDisabled` — a client request with no
  `Accept-Encoding` reaches upstream with none. A gzip response to it reaches the
  client still compressed, with `Content-Encoding` and `Content-Length` intact.

Streaming
- **AC23** `TestProxy_StreamsSSEWithoutBuffering` — each SSE event, `ping` included,
  reaches the client before upstream sends the next, and `content-type` is unchanged.
- **AC24** `TestProxy_GoldenAnthropicStreamReplay` — the recorded stream replays through
  the gateway byte-identical.
- **AC25** `TestFixtures_NoIdentifiers` — no fixture under `testdata/` contains an API
  key, a bearer token, an org or account ID, or an email address.

Errors
- **AC26** `TestProxy_UpstreamErrorsVerbatim` — `429`, `500` and `529` reach the client
  with body, `retry-after`, `x-should-retry` and `anthropic-ratelimit-*` byte-identical.
- **AC27** `TestProxy_UpstreamUnreachable502` — Anthropic error envelope,
  `x-gateway-error: upstream_unreachable`.
- **AC28** `TestProxy_UpstreamTimeout504` — no response headers within
  `response_header_timeout` gives `504`, `x-gateway-error: upstream_timeout`.
- **AC29** `TestProxy_NoRetry` — an unreachable upstream, and an upstream `500`, each
  cause exactly one upstream attempt.
- **AC30** `TestProxy_ClientDisconnectCancelsUpstream` — the upstream request's
  context is cancelled, and the log line has `client_disconnected: true`.
- **AC31** `TestProxy_UpstreamDiesMidStreamAbortsClient` — the client sees the
  connection end with no invented event.

Client profiles and core
- **AC32** `TestProfile_ClaudeCodeDetected` — `User-Agent: claude-cli/…` and
  `x-app: cli` each give `client: claude-code`.
- **AC33** `TestProfile_UnknownClientProxied` — a request matching no profile is
  proxied and logged with `client: unknown`.
- **AC34** `TestCore_NoProviderOrClientIdentifiers` — no file in `internal/core/`
  contains `anthropic`, `claude`, `openai`, `cursor` or `x-api-key` (case-insensitive).
- **AC35** `TestCore_TestAdapterAndProfileNeedNoCoreChange` — a test-only adapter and
  profile, registered from the test, proxy and label a request.

Logging and secrets
- **AC36** `TestAccessLog_ProxyFields` — the `request` line has `protocol`, `client`,
  `stream` and `ttfb_ms` for a streamed and a non-streamed request.
- **AC37** `TestAccessLog_AuthKind` — `api_key`, `bearer`, `both` and `none`.
- **AC38** `TestAccessLog_GatewayErrorField` — `gateway_error` on a `502` and a `504`,
  and absent on upstream errors.
- **AC39** `TestProxy_SecretsNotLogged` — a sentinel in `x-api-key`, `Authorization`,
  the query and the body appears in no output, on success, on a `502` and on a panic.

Carry-overs
- **AC40** `TestRun_ShutdownWaitsForStream` — a stream in flight when shutdown starts
  runs to its end. Exit `0`.
- **AC41** `TestRecoverer_LogsPanicTypeNotValue` — a panic with a sentinel value logs
  its type and stack, and the sentinel appears nowhere.
- **AC42** `TestGo_RecoversAndLogsJSON` — a panic in a goroutine started through the
  helper is recovered as one JSON error line, with no plain text on stderr, and the
  process keeps running.
- **AC43** `TestCompose_StopGracePeriodMatchesShutdownTimeout` — `docker-compose.yml`'s
  `stop_grace_period` is at least the default `shutdown_timeout`.

Manual (evidence recorded in the PR)
- **AC44** `ManualSmoke_ClaudeCodeAPIKey` — with `ANTHROPIC_BASE_URL` on the gateway and
  an API key, these behave as they do direct:
  - `claude -p` with a prompt that triggers a tool call;
  - an interactive streamed turn;
  - starting an interactive session: the log shows `HEAD /api/hello` forwarded with
    upstream's status and `client: unknown` (research.md Q1).

  `/v1/messages` log lines show `client: claude-code` and `auth: api_key`.
- **AC45** `ManualSmoke_ClaudeCodeSubscription` — the same checks, logged in with a
  claude.ai subscription. `/v1/messages` log lines show `auth: bearer`.
- **AC46** `ManualDocs_ClaudeCodePage` — `docs/clients/claude-code.md` has the setup for
  both modes, the known limits, the smoke checklist, and the up-to-10-minute shutdown
  note.

Client errors and aborts
- **AC47** `TestProxy_MalformedClientBody400` — a request body with malformed framing on
  a live connection (broken chunked encoding) gets `400`, the adapter's error envelope
  and `x-gateway-error: client_body`, and the log line has `gateway_error: client_body`.
  A client that disconnects instead, including one whose body ends short of its
  `Content-Length`, gets no body and `client_disconnected: true` (research Q11, Q19).
- **AC48** `TestAccessLog_UpstreamAbortedField` — when upstream dies after response
  headers, the `request` line has `upstream_aborted: true`; on a completed response,
  and on a `502`, it is absent or false (research Q12).

## Open questions
- [ANSWERED: research.md Q1] Does Claude Code send `HEAD /api/hello` through `ANTHROPIC_BASE_URL`, or
  straight to `api.anthropic.com`, and when?
- [ANSWERED: research.md Q2] Does Claude Code keep a path prefix in `ANTHROPIC_BASE_URL` (`/anthropic`) on
  every path it calls, including `/api/*`?
- [ANSWERED: research.md Q3] Which paths beyond `/v1/*` does subscription mode call through the base URL
  (for example `/api/oauth/*`), and do they all work through the gateway?
