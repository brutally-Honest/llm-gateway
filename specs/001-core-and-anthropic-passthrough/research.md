---
status: draft
spec: ./spec.md
plan: ./plan.md
tasks: ./tasks.md
---

# 001 — Research

The feature's working memory: doubts, roadblocks and limits, with their answers.

What belongs here, when to escalate an answer to `spec.md`, `plan.md` or an ADR, and
the never-delete rule: `PLAN.md` §10 and `AGENTS.md`. Don't restate them here.

---

## Q1 — Does Claude Code send `HEAD /api/hello` through the base URL, and when?
- Status: answered     Level: flow
- Blocks / shapes: nothing yet (tasks.md not written); shapes AC9, AC13, AC33
- Context: 2026-09-26. A throwaway logging proxy (not in the repo) ran at
  `ANTHROPIC_BASE_URL=http://127.0.0.1:7198/anthropic`, forwarding to
  `api.anthropic.com`. An interactive Claude Code session (`claude-cli/2.1.283`,
  subscription/OAuth login) ran a tool-call prompt and `/model`, then quit. Only header
  names were logged, plus the values of safe headers. Earlier, a `claude -p` run in a
  different environment sent no `HEAD /api/hello`.
- Question: does Claude Code send `HEAD /api/hello` to `ANTHROPIC_BASE_URL` or straight
  to `api.anthropic.com`, and when?
- Answer: through the base URL, prefix kept: `HEAD /anthropic/api/hello`, once, at the
  start of the interactive session. It carries no auth (`auth=none`), and its only
  headers are `Accept`, `Accept-Encoding`, `Connection` and `User-Agent: Bun/1.4.3`.
  Not seen in `-p` mode.
- Outcome: the spec is unchanged: it already forwards `/api/hello` like any other path.
  The request has no `claude-cli/` User-Agent and no `X-App`, so the Claude Code
  profile labels it `unknown`. That is the correct fallback, not a bug.

## Q2 — Does Claude Code keep the base-URL path prefix on every path?
- Status: answered     Level: flow
- Blocks / shapes: nothing yet (tasks.md not written); shapes the `/anthropic` mount
- Context: the same probe run as Q1, 2026-09-26.
- Question: with `ANTHROPIC_BASE_URL=http://127.0.0.1:7198/anthropic`, does every
  request keep the `/anthropic` prefix, including `/api/*`?
- Answer: yes. Every request kept it: `HEAD /anthropic/api/hello` and every
  `POST /anthropic/v1/messages?beta=true`.
- Outcome: none; this confirms the `/anthropic` prefix mount in spec.md's Scope.

## Q3 — Which paths does subscription mode call through the base URL?
- Status: answered     Level: flow
- Blocks / shapes: nothing yet (tasks.md not written); shapes AC9, AC45
- Context: the same probe run as Q1, 2026-09-26. Subscription (OAuth) login, one
  tool-call prompt, `/model`.
- Question: does subscription mode call paths beyond `/v1/*` through the base URL (for
  example `/api/oauth/*`), and do they all work through a proxy?
- Answer: in this session, only `HEAD /api/hello` (Q1) and five
  `POST /v1/messages?beta=true`. No `/api/oauth/*` or other path went through the base
  URL, and `/model` made no API call (no `GET /v1/models`).
  - The messages requests carried `auth=bearer`,
    `User-Agent: claude-cli/2.1.283 (external, cli)` and `X-App: cli`.
  - Headers included `Anthropic-Beta`, `Anthropic-Version`, `Authorization`,
    `X-Claude-Code-Session-Id` and `X-Stainless-*`.
  - `Anthropic-Beta` always contained `oauth-2025-04-20`, but its flag list changed
    between requests: 8 flags on the first call, up to 15 later.
- Outcome: none to the spec. The beta list that changes per request is live evidence
  for the open-list header rule (PLAN.md §5): a header allowlist, or a value cached from
  the first request, would break Claude Code.

## Q4 — Does API-key mode behave the same through the gateway?
- Status: open     Level: flow
- Blocks / shapes: AC44
- Context: 2026-09-26. The probe in Q1–Q3 covered subscription mode only; no API key
  was available.
- Question: in API-key mode, which paths and headers does Claude Code send through the
  base URL, and does it behave as it does direct?
- Answer: untested.
- Outcome: to be run as part of the manual checks (AC44–AC46).
- 2026-09-27: AC45 (subscription) passed on `c2846ad`. The tool-call turn and the
  streamed turn match the direct run. `HEAD /api/hello` is logged with
  `client: unknown`. The `/v1/messages` lines show `protocol: anthropic`,
  `client: claude-code`, `auth: bearer`, `stream: true`. The secret grep prints `0`.
  One upstream `429` was passed through verbatim and Claude Code recovered on its own.
  AC44 (API key) is deferred until a key is available, so Q4 stays open.

## Q5 — Does `ReverseProxy` with `Rewrite` forward the request exactly as the client sent it?
- Status: answered     Level: technical
- Blocks / shapes: nothing yet (tasks.md not written); shapes plan.md "Approach: request rewrite", AC8, AC14, AC16
- Context: 2026-09-26. Read `net/http/httputil/reverseproxy.go` (go1.25.4) before
  agreeing to `Rewrite` without `SetXForwarded`.
- Question: what does `ReverseProxy` change on the outbound request before, or after,
  our `Rewrite` runs?
- Answer: three things, all before `Rewrite` sees the request, so a bare `SetURL` loses
  them.
  - It deletes the client's `Forwarded`, `X-Forwarded-For`, `X-Forwarded-Host` and
    `X-Forwarded-Proto`. The spec says everything except hop-by-hop is forwarded, so a
    client-sent one must reach upstream.
  - It runs `cleanQueryParams` on `RawQuery`, which drops parameters it cannot parse
    (a raw `;`, a bad `%zz`). AC8 wants the query byte-identical.
  - It re-adds `Te: trailers` when the client sent it, although `TE` is hop-by-hop.
  Confirmed with a throwaway program (`X-Forwarded-For: 9.9.9.9` and
  `?a=1;b=2&c=%zz` in, empty `X-Forwarded-For` and empty query out).
- Outcome: escalated → plan. `Rewrite` copies the four forwarding headers and
  `RawQuery` back from `pr.In` and deletes `Te`. No spec change: the spec already asks
  for exactly this behaviour.

## Q6 — Does `ReverseProxy` overwrite the gateway's `X-Request-Id` with upstream's?
- Status: answered     Level: technical
- Blocks / shapes: nothing yet (tasks.md not written); shapes plan.md "Approach: response hook", AC19
- Context: 2026-09-26. The plan agreed to "overwrite `X-Request-Id` in `ModifyResponse`".
  000's `requestID` middleware already sets the gateway's ID on the response writer's
  header map before the handler runs.
- Question: what does `ReverseProxy` do when upstream's response also has `X-Request-Id`?
- Answer: it appends. `copyHeader` uses `Header.Add`, so the client got
  `[gateway upstream]`, two values (throwaway program). Setting the header on the
  upstream response inside `ModifyResponse` would give two copies of the gateway's ID.
- Outcome: escalated → plan. `ModifyResponse` deletes upstream's `X-Request-Id`
  (`res.Header.Del`) and the gateway's, already on the writer, stands. Same outcome as
  the spec, and `core` needs no request-ID accessor.

## Q7 — Does 000's response wrapper keep `http.Flusher` under `ReverseProxy`?
- Status: answered     Level: technical
- Blocks / shapes: nothing yet (tasks.md not written); shapes plan.md "Approach: flushing", AC23
- Context: 2026-09-26. `accessLog` wraps the writer with chi's
  `middleware.NewWrapResponseWriter`. `ReverseProxy` flushes through
  `http.NewResponseController`.
- Question: does the wrapped writer still flush, so `FlushInterval: -1` works?
- Answer: yes. For HTTP/1 chi returns a writer with `Flush`, and every wrapper has
  `Unwrap`. In a throwaway run behind the wrapper, the second event arrived ~100 ms
  after the first, as upstream sent it.
- Outcome: none; 000's wrapper stays, and AC23 keeps it honest.

## Q8 — What happens to the access log when the stream aborts mid-response?
- Status: answered     Level: technical
- Blocks / shapes: nothing yet (tasks.md not written); shapes plan.md "Approach: access log", AC30, AC31
- Context: 2026-09-26. Read `reverseproxy.go` and ran a throwaway proxy; the client
  closed its connection mid-stream.
- Question: how does `ReverseProxy` end a response that fails after headers, and does
  000's `accessLog` still write its line?
- Answer: it panics with `http.ErrAbortHandler` (under a real `http.Server`), for an
  upstream read error and for a client that left. In the run, the request context was
  already cancelled when the panic reached the handler's `defer`. 000's `accessLog`
  logs after `next.ServeHTTP` returns, not in a `defer`, and `recoverer` re-panics
  `ErrAbortHandler`, so the line for exactly these requests is never written.
- Outcome: escalated → plan. `accessLog` logs from a `defer`, and sets
  `client_disconnected` from `r.Context().Err()` there.

## Q9 — Does the upstream transport honour `HTTP_PROXY` / `HTTPS_PROXY`?
- Status: answered     Level: flow
- Blocks / shapes: plan.md "Approach: transport"
- Context: 2026-09-26. The spec fixes dial, TLS-handshake and header timeouts and
  system roots, and says nothing about an outbound proxy. Go's default transport sends
  upstream traffic through `HTTPS_PROXY` when it is set (loopback upstreams are exempt).
- Question: should the gateway's upstream calls follow the standard proxy env vars, or
  always connect directly?
- Answer: open. Working default in the plan: `http.ProxyFromEnvironment`, because a
  user behind a corporate proxy cannot reach Anthropic otherwise, and it is what a
  direct Claude Code connection would do. Cost: an env var the gateway never logs can
  change where prompts go. Needs a decision before the transport task.
- 2026-09-26 (owner): Answered: yes, honour the standard proxy env vars (`http.ProxyFromEnvironment`), as the plan's working default has it.
- Outcome: plan: no change (already the Transport's `Proxy`).

## Q10 — How is the golden stream recorded without an API key?
- Status: answered     Level: limit
- Blocks / shapes: AC24, AC25; the fixture task
- Context: 2026-09-26. Q4: no API key is available. The spec's fixture has to be a
  real Anthropic streaming response, scrubbed by hand.
- Question: which credential and which client record the stream: a raw request with an
  API key, or a Claude Code subscription session through a throwaway recording proxy?
- Answer: open. Working default in the plan: the throwaway proxy from the Q1–Q3 probe,
  extended to save the upstream response's status, headers and body. It forces
  `Accept-Encoding: identity` so the saved body is readable text, and it works in
  either auth mode. It is not the gateway, so the fixture cannot vouch for itself.
- 2026-09-26 (owner): Answered: agreed. Record the stream with the throwaway recording proxy driven by Claude Code, as the plan's working default has it.
- Outcome: plan: no change (Golden fixture section already says so). Q4 still governs AC44.

## Q11 — What does the gateway answer when the client's own request body fails?
- Status: answered     Level: technical
- Blocks / shapes: plan.md "Approach: error handler", AC27
- Context: 2026-09-26. `ReverseProxy` calls `ErrorHandler` with one error type for
  every `RoundTrip` failure, and a failed read of the client's request body (the client
  sent fewer bytes than its `Content-Length`, or reset the connection mid-upload) comes
  back the same way. The spec's 502 says "DNS, connection or TLS failure".
- Question: is a body-read failure a 502 `upstream_unreachable`, which would blame
  upstream, or something else?
- Answer: open. Working default in the plan: the spec's literal rule, so anything that
  is not a timeout and not a cancelled context is a 502. When the client has already
  gone, the cancelled-context branch usually catches it first.
- 2026-09-26 (owner): Answered: a client that is gone gives `client_disconnected` and no body. A malformed or short request body gives `400` in the adapter's error envelope with `x-gateway-error: client_body`. It is never a 502.
- Outcome: escalated → spec and plan, not yet applied: a new gateway-made error reason (`client_body`, 400) changes the spec's Errors list and needs an AC; plan.md's error handler must tell a request-body read failure from an upstream failure. spec.md is approved and was left untouched in this pass.
- 2026-09-26: applied. spec.md now has the `400` / `client_body` reason and AC47; plan.md's error handler and AC table follow it.

## Q12 — Should the access line say that the upstream aborted mid-stream?
- Status: answered     Level: limit
- Blocks / shapes: plan.md "Approach: access log", AC31
- Context: 2026-09-26. After headers, `ReverseProxy` can only abort (Q8). The line then
  has the upstream's `status` (for example 200) and a short `bytes`, with no field
  saying the response was cut. The spec's field list has no such field, and
  `gateway_error` is defined as errors the gateway created.
- Question: is `status` plus `bytes` enough, or should the line gain a field?
- Answer: open. Working default in the plan: add nothing, as the spec's list is
  closed.
- 2026-09-26 (owner): Answered: yes, the access line says so, with the field `upstream_aborted`.
- Outcome: escalated → spec and plan, not yet applied: `upstream_aborted` is a new access-log field (spec's Access log list) and needs an AC; plan.md's access log and response hook sections must set it when `ReverseProxy` aborts after headers. spec.md left untouched in this pass.
- 2026-09-26: applied. spec.md now has `upstream_aborted` and AC48; plan.md's access log section and AC table follow it.

## Q13 — Which status does the access line show when the client left before any response?
- Status: answered     Level: limit
- Blocks / shapes: plan.md "Approach: error handler", AC30
- Context: 2026-09-26. On a cancelled context the error handler writes no body (agreed,
  and the spec's `client_disconnected`). If it writes no status either, the chi wrapper
  reports `0` and `net/http` would send an implicit `200` to nobody.
- Question: what `status` should the line carry?
- Answer: open. Working default in the plan: `WriteHeader(499)` with no body. 499 is the
  usual "client closed request" convention and is never seen by anyone.
- 2026-09-26 (owner): Answered: `499`, header only, no body, as the plan's working default has it.
- Outcome: plan: no change (already the working default).

## Q14 — Who records and commits the golden fixture, given an implementer cannot?
- Status: answered     Level: limit
- Blocks / shapes: AC24, AC25; the golden-replay task (T21) in tasks.md
- Context: 2026-09-26. Plan "Golden fixture" steps 1–4 record one real stream through
  the throwaway recording proxy, driven by a logged-in Claude Code session (Q10). An
  implementer agent has no Claude Code login, and the recording proxy is deliberately
  not in the repo. `TestProxy_GoldenAnthropicStreamReplay` cannot pass, and `make
  verify` cannot stay green, without `stream.sse` and `stream.headers`. Skipping the
  test when the files are absent is forbidden (AGENTS.md, Do not).
- Question: does the owner record and commit the fixture files themselves, so the task
  only adds the replay test, or is there another way to produce a real recording?
- Answer: open. Working default in tasks.md: the owner follows plan steps 1–4 and
  commits `internal/protocols/anthropic/testdata/{stream.sse,stream.headers,README.md}`
  as `test(anthropic): record the golden stream fixture`. T21 is ordered after the docs
  page and stops as BLOCKED until those files exist. Nothing is faked.
- 2026-09-26 (owner): Answered: the owner recorded and committed the fixture, as the
  working default has it. `internal/protocols/anthropic/testdata/{stream.sse,
  stream.headers,README.md}` landed in `8662b4d` as `test(anthropic): add the golden
  stream fixture`. T21 now adds only the replay test.
- Outcome: tasks: T21 changed from `(blocked: Q14)` to `(shaped: Q14)`.

## Q15 — Which auth kind does the Anthropic adapter report for unusual header combinations?
- Status: answered     Level: flow
- Blocks / shapes: `AuthKind` in the Anthropic adapter (T14), AC37
- Context: 2026-09-26. The spec says `auth` is `api_key` / `bearer` / `none`, "which
  header is present, never its value", and that the gateway never reads the values of
  `x-api-key` or `Authorization`. It does not say what happens when both headers are
  present, or when `Authorization` carries a scheme other than `Bearer`, which can only
  be told apart by reading the value's prefix.
- Question: with both `x-api-key` and `Authorization` present, which wins? Is any
  `Authorization` header `bearer` regardless of scheme, or only `Bearer …`?
- Answer: open. Working default: presence only, never the value. `x-api-key` present
  gives `api_key`; otherwise `Authorization` present gives `bearer`; otherwise `none`.
  Claude Code sends one of them per mode (Q3), so the edge cases are not observed.
- Answer (2026-09-26, owner): with both `x-api-key` and `Authorization` present, log
  `auth: both`. Still presence only, never the value, so any `Authorization` scheme
  counts as `bearer`.
- Outcome: spec: `both` added to the `auth` values. tasks: T14's
  `TestAccessLog_AuthKind` gains the both-headers case; T14 changed from
  `(blocked: Q15)` to `(shaped: Q15)`.

## Q16 — Does `ReverseProxy` strip every header the spec calls hop-by-hop?
- Status: answered     Level: technical
- Blocks / shapes: plan.md "Approach: request rewrite" step 5 and "Response hook"; AC14 (T8)
- Context: 2026-09-26. Building T8 against `net/http/httputil/reverseproxy.go`
  (go1.25.4). The spec's hop-by-hop list is `Connection` and what it names,
  `Keep-Alive`, `TE`, `Trailer`, `Transfer-Encoding`, `Upgrade`, `Proxy-*`. The plan
  said `ReverseProxy` already strips them, apart from `Te` (Q5).
- Question: are there others it misses or re-adds?
- Answer: two. Its `hopHeaders` names only `Proxy-Authorization` and
  `Proxy-Connection` (plus `Proxy-Authenticate` on responses), so any other `Proxy-*`
  passes in both directions. And when the client asks for a protocol switch
  (`Connection: Upgrade`, `Upgrade: websocket`), it re-sets `Connection: Upgrade` and
  `Upgrade` after stripping, before `Rewrite` runs. Confirmed by
  `TestProxy_StripsHopByHopHeaders`, which failed with each fix removed.
- Outcome: escalated → plan. `Rewrite` step 5 also deletes `Connection`, `Upgrade`
  and every `Proxy-*`; `ModifyResponse` deletes every `Proxy-*` on the response. No
  spec change: the spec already lists these as hop-by-hop. A consequence: the gateway
  never switches protocols (no WebSocket through it), which no adapter needs.

## Q17 — Does `net/http` invent a `Content-Type` for a response upstream sent without one?
- Status: answered     Level: technical
- Blocks / shapes: plan.md "Response hook"; AC18 (T9)
- Context: 2026-09-26. Building T9 (go1.25.4). `TestProxy_ResponseVerbatim` has an
  upstream that sends no `Content-Type`; AC18 wants the client to see none either.
- Question: does the response reach the client without a `Content-Type`?
- Answer: not reliably. When the first body write carries the headers out and the
  header map has no `Content-Type` key, `net/http`'s response writer sniffs one
  (`http.DetectContentType`, e.g. `text/html; charset=utf-8`). With `FlushInterval: -1`
  `ReverseProxy`'s early header flush usually wins, but that is a race. An entry that
  is present but empty (`Header()["Content-Type"] = nil`) stops the sniff outright,
  and `ReverseProxy` adds upstream's value to that entry when there is one. Confirmed
  by `TestProxy_ResponseVerbatim`, which failed (sniffed `text/html`) with the entry
  removed and `FlushInterval: 0`.
- Outcome: escalated → plan. "Response hook": `ServeHTTP` puts an empty
  `Content-Type` entry on the writer when it has none. T10's error handler must use
  `Set`, not `Add`, for its own `Content-Type`. No spec change.

## Q18 — Where does AC26's literal `anthropic-ratelimit-*` proof live?
- Status: answered     Level: flow
- Blocks / shapes: AC26 (T9); may add a test to T14 and a row to the traceability table
- Context: 2026-09-26. AC26 names `TestProxy_UpstreamErrorsVerbatim` and says `429`,
  `500` and `529` reach the client with body, `retry-after`, `x-should-retry` and
  `anthropic-ratelimit-*` byte-identical. T9 wrote that test in `internal/core/`, where
  AC34's `TestCore_NoProviderOrClientIdentifiers` walks every file, tests included, and
  forbids `anthropic`. So the core test sends a neutral `X-Ratelimit-*` family instead.
  That proves the mechanism (the proxy treats every header name alike) but no test in
  the repo sends a header named `anthropic-ratelimit-*` on an error. T21's golden
  replay does carry `Anthropic-Ratelimit-Unified-*` headers, but on a `200`, not on
  `429` / `500` / `529`. The spec does not say which package AC26's test lives in.
  Raised by review of T9 (`ca3f86a`).
- Question: does AC26 need a test that sends the literal `anthropic-ratelimit-*` family
  on `429` / `500` / `529`, and if so, which task owns it? Or does the neutral stand-in
  in core count as full proof?
- Answer: open. Working default (not applied): T14 adds a second
  `TestProxy_UpstreamErrorsVerbatim` in `internal/protocols/anthropic/adapter_test.go`,
  through the real adapter and `server.New`, sending `anthropic-ratelimit-*` headers on
  each of the three statuses; the traceability row for AC26 becomes `T9, T14`. The
  alternative is that the owner accepts the core stand-in as full proof of AC26.
- Answer (2026-09-26, owner): the working default. AC26 needs the literal family on
  an error. T14 adds a second `TestProxy_UpstreamErrorsVerbatim` in
  `internal/protocols/anthropic/adapter_test.go` that sends `anthropic-ratelimit-*` on
  `429`, `500` and `529` and checks them byte-identical. The core test stays as the
  mechanism proof.
- Outcome: tasks: T9 changed from `(blocked: Q18)` to `(shaped: Q18)`; T14 gains the
  test and AC26; AC26's coverage row becomes `T9, T14`. plan: AC26's test row names
  both files. No spec change.

## Q19 — Is the inbound context still live when a client half-closes after a short body?
- Status: answered     Level: technical
- Blocks / shapes: plan.md "Error handler" steps 1–2; AC47; T11 (the `ErrorHandler`'s
  context-first branch)
- Context: 2026-09-26. Building T10 (go1.25.4). Plan step 2 says that for a client that
  half-closes after too few bytes "the context is still live", so the body-watcher
  branch catches it. It is not: `net/http`'s `connReader` cancels the request context
  on any read error on the connection, including the `EOF` a half-close delivers. In
  `TestProxy_MalformedClientBody400`'s short-body case, the `ErrorHandler` sees
  `r.Context().Err() != nil` and `err` is `context canceled`, every run; the watcher
  holds `io.ErrUnexpectedEOF`. The broken-chunked case keeps a live context. The
  gateway can still answer on the half-closed connection, and the test reads the `400`.
  T10 has no context branch (it is T11's), so T10's `400` holds.
- Question: T11 makes `r.Context().Err() != nil` the first branch (`499`, no body).
  With the ordering in plan.md and tasks.md, the half-close short-body case of AC47
  becomes a `499` and T10's test fails. Should the body-watcher branch come before the
  context branch (a vanished client then gives `400` if its body read had already
  failed, though nobody reads it), or should the context branch tell a vanished client
  from a half-closed one some other way, or does AC47's short-body case need another
  shape?
- Answer: open. No working default applied; T10 does not depend on it.
- Answer (2026-09-26, owner): option (c), AC47's short-body case changes shape. A body
  shorter than its `Content-Length` cannot be told from a client leaving: Go cancels
  the request context in both. So it is classified as `client_disconnected` (`499`, no
  body). `400` `client_body` is a malformed body on a live connection, such as invalid
  chunked framing. The context-first order in plan.md stays.
- Outcome: spec: the `400` / `client_body` scope bullet and AC47 name malformed
  framing, and a short body counts as `client_disconnected`. plan: error-handler steps
  1–2, the request-body watcher note and AC47's test row match. tasks: T11 first proves
  the context stays live on a chunked framing error (else BLOCKED with a new query),
  moves T10's short-body tests (`TestProxy_MalformedClientBody400`,
  `TestProxy_ClientBodyBeatsTimeout`) to the malformed-chunk case, and changes from
  `(blocked: Q19)` to `(shaped: Q19)`.

## Q20 — How does `Registry.Mount` forward a method chi does not know?
- Status: answered     Level: technical
- Blocks / shapes: plan.md "Registry and routes"; T12 (`Mount`, and the unknown-method
  half of `TestCore_TestAdapterAndProfileNeedNoCoreChange`)
- Context: 2026-09-26. Building T12 (chi v5.3.2). Plan and T12 say `Mount` registers
  `prefix + "/*"` with chi's `Handle` "so `HEAD` and an unknown method are forwarded".
  `HEAD` is. An unknown method is not: `Mux.routeHTTP` looks `r.Method` up in chi's
  package-level `methodMap` before it searches the tree, and a method missing from it
  (anything outside the nine standard ones unless `chi.RegisterMethod` added it) goes
  straight to the router's `MethodNotAllowedHandler`. A `BREW /t/v1/x` request with the
  test adapter mounted gets chi's `405` and never reaches the proxy; `POST` and `HEAD`
  on the same route reach upstream. The same lookup means an unknown method on a path
  outside every prefix gets `405`, not AC11's `404` (AC11's test only uses standard
  methods). The spec itself names no unknown-method behaviour; PLAN §5's open-list rule
  is about headers and body fields.
- Question: which should it be?
  (a) Unknown methods are not forwarded in 001: chi's `405` stands, and plan.md and
  T12 drop "and an unknown method" (T12 tests `HEAD` and the standard methods only).
  (b) `Mount` also installs the router's `MethodNotAllowed` handler, which sends a
  request whose path is under a registered prefix to that adapter's proxy and gives
  every other request chi's `405` (or `404`). This touches a router-wide handler from
  one mount, so a later mount that sets its own would conflict.
  (c) `server.New` (or `run`) wraps the chi router so a method chi does not know is
  dispatched by prefix before chi sees it. Changes `server`, outside T12's file.
  (d) `chi.RegisterMethod` for a fixed list of extra methods: a global allowlist, which
  the open-list spirit argues against.
- Answer: open. No working default applied; T12's code and tests are stashed as
  "T12 blocked on Q20" (registry.go passes every other T12 test).
- Answer (2026-09-26, owner): option (a). Every standard method chi knows, `HEAD`
  included, is forwarded under an adapter prefix. A non-standard method gets chi's
  `405`, inside or outside a prefix. Known limit: revisit with option (c), dispatching
  by prefix before chi sees the request, if a real client ever needs one.
- Outcome: spec: one Out of scope line for non-standard HTTP methods, since the
  outside-the-prefix `404` rule would otherwise cover them. plan: "Registry and routes"
  drops the unknown-method promise and states the known limit. tasks: T12 drops the
  unknown-method row of `TestCore_TestAdapterAndProfileNeedNoCoreChange` and changes
  from `(blocked: Q20)` to `(shaped: Q20)`.

## Q21 — Can Go's transport send one client request upstream more than once?
- Status: answered     Level: technical
- Blocks / shapes: spec.md "No retries"; AC29 (`TestProxy_NoRetry`)
- Context: 2026-09-26. The final branch review of 001 found that `internal/core/proxy.go`
  uses a stock `http.Transport`, which replays some requests on its own, while the spec
  says one client request is at most one upstream request. `TestProxy_NoRetry` only
  sends a POST with a body on a fresh connection, so it cannot see this. From the Go
  1.25 source (`net/http` `Request.isReplayable`, `persistConn.shouldRetryRequest`,
  `Transport.roundTrip`):
  - A request is replayed only when it failed on a reused connection; a fresh
    connection is never retried.
  - If nothing was written before the failure, a request with no body (any method) is
    replayed; upstream never saw the first try.
  - If the request was written, it is replayed only when it has no body and is GET,
    HEAD, OPTIONS or TRACE, or carries `Idempotency-Key` or `X-Idempotency-Key`.
  - The loop is not capped at one replay: it goes on while each failure is on a reused
    connection.
  - With HTTP/2 (`ForceAttemptHTTP2`), a request whose body has not been sent yet can be
    retried after a refused stream or GOAWAY; upstream never processed it.
  - The gateway's outgoing request has no `GetBody`, so a request with a body that was
    written is never replayed.
- Question: is this replay acceptable under the no-retry rule?
- Answer (2026-09-26, owner): yes. A POST with a body that reached upstream is never
  resent, so no billed request is duplicated; the replays are bodyless idempotent
  requests or requests upstream never saw. The spec states the exception.
- Outcome: spec: one clause on the "No retries" rule naming the transport's replay of
  a bodyless idempotent request on a failed reused connection.
- Correction (2026-09-26, owner): the first clause listed GET, HEAD, OPTIONS, TRACE
  and `Idempotency-Key`, which read as complete but left out `X-Idempotency-Key`, a
  request that failed before anything was written, and an HTTP/2 retry after a refused
  stream or GOAWAY. The clause now reads "except Go's transport replaying a request
  upstream never saw, or a bodyless idempotent one (research Q21)"; the cases are
  listed in the Context above.

## Q22 — Should a `Trailer` header reach the client when the spec calls it hop-by-hop?
- Status: answered     Level: technical
- Blocks / shapes: spec.md hop-by-hop header rule; AC14
- Context: 2026-09-26. The final branch review of 001 found that when upstream
  announces trailers, `httputil.ReverseProxy` strips the hop-by-hop `Trailer` header
  and then adds a new one to the client response naming the same trailer keys (Go 1.25
  `net/http/httputil/reverseproxy.go`, `rw.Header().Add("Trailer", …)`). The spec lists
  `Trailer` as hop-by-hop, and AC14's test covers only the request side.
- Question: is the re-announced `Trailer` a breach of the hop-by-hop rule?
- Answer (2026-09-26, owner): no. Re-announcing `Trailer` is how the reverse proxy
  forwards upstream's trailers to the client, which the passthrough rule wants, so the
  behaviour stays.
- Outcome: no spec, plan or task change.
