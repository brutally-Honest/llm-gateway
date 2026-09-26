---
status: approved
spec: ./spec.md
plan: ./plan.md
research: ./research.md
---

# 001 — Tasks

Small, ordered, checkable steps. The agent ticks them as it goes. A task held up by a
query in `research.md` ends with `(blocked: Q2)`; once that query is answered it becomes
`(shaped: Q2)`. The link stays on the task line either way — the status lives in
`research.md`.

> Note: spec.md holds AC1–AC48. AC47 (`client_body`) and AC48 (`upstream_aborted`) came
> from research Q11 and Q12 after the first draft of the ACs, so this file covers all
> 48, not 46.

Each task is one commit. When it is done, `make verify` passes. Tests land in the same
task as the code they cover, and every task starts by writing the failing tests it
names. A test that passes on arrival (a guard over behaviour an earlier task built) is
proved by breaking the code for a moment and watching it fail, then reverting; never
committed broken. "Done" names the proof, and the ACs a task contributes to are listed
at the end of its line. An AC whose evidence spans tasks is listed on each of them.

Layout the tasks assume: `internal/core` tests are in package `core_test`, and use one
test adapter (name `test`, prefix `/t`) and one test profile, defined in the test files
of the task that first needs them. Core never names a provider, so no core test does
either. Every proxy test that ends in a cancel, a timeout or an abort runs the plan's
leak check (a goroutine-profile stack scan, no new dependency) before it returns.

## Config

- [x] T1 — `internal/config`: nested `upstreams.<name>.<field>` settings. `Options`
  gains `Upstreams []UpstreamSpec{Name, DefaultBaseURL}`, `Config` gains
  `Upstreams map[string]Upstream` (`BaseURL *url.URL`, `ConnectTimeout`,
  `TLSHandshakeTimeout`, `ResponseHeaderTimeout`; `base_url` is only `url.Parse`d
  here, T2 validates it). `envName` maps `.` and `-` to `_`
  (`GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL`); `parseFile` walks one level under
  `upstreams`, so an unknown name or field is `unknown key` with the dotted path and
  line. The three timeouts use the `> 0`, `invalid duration` rule. `shutdown_timeout`'s
  default becomes `10m`, `config.example.yaml` gains the `upstreams.anthropic` block at
  the defaults, each key commented, and `shutdown_timeout: 10m`. `Config` is no longer
  comparable, so the three `cfg != Defaults()` checks in `load_test.go` become
  `reflect.DeepEqual`, and 000's `30s` shutdown assertions become `10m`. Nothing else in
  000's tests is loosened. New tests are in `load_upstream_test.go` (package
  `config_test`), using a spec list of one upstream named `anthropic`. Tests first:
  `TestLoad_UpstreamDefaults`, `TestLoad_UpstreamEnvOverridesFile` (each of the four
  env vars beats the file; `env_overrides` names the key),
  `TestLoad_InvalidUpstreamDuration` (three keys × `abc`, `0s`, `-5s` × file, env),
  `TestLoad_UnknownUpstreamKey` (a field under `anthropic`, and a name under
  `upstreams`). Done: those four, plus `TestLoad_ExampleFileIsDefaults` (with the new
  block) and `TestLoad_ErrorsNeverContainValue`, pass. Commit:
  `feat(config): add nested upstream settings` (AC1, AC2, AC5, AC6, AC7)
- [x] T2 — `internal/config`: `base_url` validation in `upstream.go`, one function whose
  only failure reason is the new fixed `invalid url` (the message never holds the
  value). Reject: a string containing `?` or `#` (an empty query or fragment that
  `url.Parse` swallows), a parse failure, a non-absolute URL, an empty host, userinfo.
  The scheme must be `https`, or `http` when the hostname is `localhost` or
  `net.ParseIP(host).IsLoopback()`. `TestLoad_ErrorsNeverContainValue` gains a row for
  the new reason. Tests first: `TestLoad_InvalidUpstreamURL` (five rows: relative URL,
  empty host, userinfo / query / fragment, scheme other than `http`/`https`, `http` to
  a non-loopback host; each names key and source, and a sentinel value is absent from
  `Error()` and its fields) and `TestLoad_UpstreamURLAccepted`
  (`https://api.anthropic.com`, `https://host/api`, and `http://127.0.0.1:<port>`,
  `http://localhost:<port>`, `http://[::1]:<port>`). Done: both pass, and the
  reason-table test still passes. Commit: `feat(config): validate the upstream base_url`
  (AC3, AC4)

## Logging and goroutines

- [x] T3 — `internal/logging/panic.go`: `LogPanic(log, msg, v, fields...)` writes one
  error line with `panic_type` (`%T`) and `stack` (`debug.Stack()`), never `v`.
  `internal/server/middleware.go`'s `recoverer` calls it instead of
  `zap.Any("panic", v)`. The two 000 assertions on the `panic` value
  (`internal/server/server_test.go`, `cmd/gateway/run_logging_test.go` around line 157)
  become `panic_type` present plus "the sentinel appears nowhere in the output", which is
  stronger. Test first: `TestRecoverer_LogsPanicTypeNotValue` (a panic with a sentinel
  value logs its type and a stack, and the sentinel is in no output). Done: it passes,
  and 000's `TestRun_PanicRecovered` and `TestRun_AllLinesJSON` pass with the changed
  assertions. Commit: `feat(logging): log panic types, never values` (AC41)
- [x] T4 — `internal/logging/go.go`: `ErrPanicked` and
  `Go(log, name, fn func() error) <-chan error`, which starts the goroutine, recovers a
  panic through `LogPanic`, and delivers `fn`'s error, or `ErrPanicked` after a panic,
  on a buffered channel. `cmd/gateway/run.go`'s bare `go func() { serveErr <- ... }()`
  becomes `logging.Go`. Test first: `TestGo_RecoversAndLogsJSON` (a panic in a helper
  goroutine gives one JSON error line with `panic_type` and `stack` and no value, the
  channel delivers `ErrPanicked`, and the test process keeps running). Done: it passes
  under `-race`, and every existing `TestRun_*` test still passes. Commit:
  `feat(logging): add a recovering goroutine helper` (AC42)
- [x] T5 — `test/conventions/nobarego_test.go` (a new repo-shape package, like
  `test/githooks`): an AST walk over every non-`_test.go` file in the module that fails
  on a `go` statement outside `internal/logging/go.go`. It uses only `go/parser` from
  the standard library. Test first: `TestNoBareGoStatements`, which must first fail
  against a throwaway file holding a bare `go` statement (delete it afterwards), and
  which reports file and line. Done: it passes on the tree, and the failing run above is
  described in the git note. Commit: `test(repo): fail on a bare go statement` (spec
  Don't "no bare `go`"; no AC)

## Core, bottom-up

- [x] T6 — `internal/core` is created: `adapter.go` (`AuthKind` with `AuthNone`,
  `AuthAPIKey`, `AuthBearer`; `ClientUnknown = "unknown"`; the `Adapter` interface
  `Name`, `Prefix`, `DefaultBaseURL`, `AuthKind(http.Header)`, `ErrorBody(reason)`; the
  `Profile` interface `Name`, `Match`), and `meta.go` (`Meta` with `Protocol`, `Client`,
  `Auth`, `Stream`, `TTFB`, `HasTTFB`, `GatewayError`, `ClientDisconnected`,
  `UpstreamAborted`; `WithMeta`, `MetaFrom`, `Settle`). `Settle(ctx)` decides the two
  abort flags once the handler is done: the response watcher holds a read error and
  `ctx.Err() == nil` sets `UpstreamAborted`; otherwise `ctx.Err() != nil` sets
  `ClientDisconnected`. The two body watchers are unexported types here
  (read-through, first non-`io.EOF` error kept, `atomic.Pointer` on the request side,
  which the transport reads on its own goroutine; no buffering, `Read`/`Close` passed
  straight through). Nothing in `core` imports `server`. Tests first:
  `TestCore_NoProviderOrClientIdentifiers` (`internal/core/purity_test.go`: no file in
  `internal/core/` except this one contains `anthropic`, `claude`, `openai`, `cursor` or
  `x-api-key`, case-insensitive; it must first fail against a file that does), and
  `TestMeta_Settle` (table: upstream read error with a live context, client gone, both
  clean, watcher error with a cancelled context), and `TestWatcher_PassesBytesThrough`
  (same bytes, same read sizes, first error kept). Done: the three pass under `-race`.
  Commit: `feat(proxy): add the adapter, profile and metadata types` (AC34)
  (shaped: Q12)
- [x] T7 — `internal/server/middleware.go`: `accessLog` creates the `Meta`
  (`core.WithMeta`) before the handler chain and logs from a `defer`, so a handler that
  ends in `http.ErrAbortHandler` still writes its line; in the `defer` it calls
  `Meta.Settle(r.Context())`. When `Meta.Protocol != ""` the `request` line gains
  `protocol`, `client`, `auth`, `stream`; `ttfb_ms` only once headers arrived;
  `gateway_error`, `client_disconnected`, `upstream_aborted` only when set or true. A
  line with no `Protocol` (`/healthz`, a 404) is byte-for-byte the 000 line. No
  headers, body, query or `model` are ever added. Tests first, in `internal/server`
  with a test handler that fills `Meta` through `core.MetaFrom`:
  `TestAccessLog_ProxyFieldsOnlyWhenProtocolSet`, `TestAccessLog_OptionalFieldsOnlyWhenSet`,
  `TestAccessLog_LogsWhenHandlerAborts` (a handler that panics `http.ErrAbortHandler`
  through `recoverer` still produces the line, and the client sees the dropped
  connection). Done: those pass and every 000 access-log test passes unchanged. Commit:
  `feat(server): log proxy fields from a deferred access line` (AC30, AC36, AC38, AC48)
  (shaped: Q8)
- [x] T8 — `internal/core/proxy.go`, the request half: `NewProxy(a, up, identify, log)`
  builds one `httputil.ReverseProxy` per adapter with `Rewrite` (in this order: strip the
  adapter prefix from `Path` and `RawPath`; `SetURL(base)` with `Out.Host` cleared and
  never `SetXForwarded`; put `pr.In.URL.RawQuery` back; put back the client's
  `Forwarded`, `X-Forwarded-For`, `X-Forwarded-Host`, `X-Forwarded-Proto` unless the
  client's `Connection` header names them; `Del("Te")`), and `ServeHTTP` fills
  `Meta.Protocol`, `Client` (from `identify`) and `Auth` (from `Adapter.AuthKind`)
  before delegating. The transport is built explicitly (not cloned from the default):
  `DisableCompression`, a `net.Dialer{Timeout: connect_timeout, KeepAlive: 30s}`,
  `TLSHandshakeTimeout`, `ResponseHeaderTimeout`, no `Timeout` anywhere,
  `ForceAttemptHTTP2`, `MaxIdleConns 100`, `MaxIdleConnsPerHost 32`,
  `IdleConnTimeout 90s`, `ExpectContinueTimeout 1s`, `Proxy: ProxyFromEnvironment`. The
  test adapter, test profile and a fake-upstream helper are defined here, in
  `internal/core/helpers_test.go`, with the proxy mounted on `server.New`. Tests first,
  in `internal/core/proxy_test.go`: `TestProxy_ForwardsRequestVerbatim` (method, path
  with prefix stripped, a query containing `;` and `%zz`, body, every header, and a
  client `X-Forwarded-For` all byte-identical), `TestProxy_BaseURLPathPrefixJoined`,
  `TestProxy_StripsHopByHopHeaders` (both directions, a header named by `Connection`,
  and `Te: trailers`), `TestProxy_SetsUpstreamHost`, `TestProxy_AddsNoHeadersUpstream`,
  `TestProxy_LargeBodyNotLimited` (64 MiB from a reader, hashed upstream, never held
  whole), `TestProxy_CompressionPassThrough`, `TestProxy_TransparentGzipDisabled`.
  Done: those eight pass under `-race`. Commit:
  `feat(proxy): forward requests through a reverse proxy` (AC8, AC12, AC14, AC15, AC16,
  AC20, AC21, AC22) (shaped: Q5, Q9, Q16)
- [x] T9 — `internal/core/proxy.go`, the response half: `FlushInterval: -1`, and
  `ModifyResponse` that deletes upstream's `X-Request-Id` (the gateway's, set by 000's
  `requestID`, stands; `request-id` is untouched), sets `Meta.Stream` (`Content-Type`
  is `text/event-stream`) and `Meta.TTFB`, and wraps `res.Body` in the response watcher
  from T6. Nothing else about the response changes. Tests first:
  `TestProxy_ResponseVerbatim` (status, headers, body byte-identical except hop-by-hop
  and `X-Request-Id`, including a response with no `Content-Type`),
  `TestProxy_GatewayRequestIDWinsOnResponse` (exactly one `X-Request-Id`, and
  `request-id` passes),
  `TestProxy_StreamsSSEWithoutBuffering` (an upstream that writes one event, a `ping`,
  then waits on a channel the test controls: the client has the event before the next
  is released, through `server.New` so 000's middleware is in the path, and
  `content-type` is unchanged), `TestProxy_UpstreamErrorsVerbatim` (`429`, `500`, `529`
  with body, `retry-after`, `x-should-retry`, `anthropic-ratelimit-*` byte-identical),
  `TestProxy_FlushIntervalIsImmediate` (asserts the built proxy's `FlushInterval`), and
  `TestAccessLog_ProxyFields` (`protocol`, `client`, `stream`, `ttfb_ms` for a streamed
  and a non-streamed request). Done: those pass under `-race`. Commit:
  `feat(proxy): stream responses back unchanged` (AC18, AC19, AC23, AC26, AC36)
  (shaped: Q6, Q7, Q17, Q18)
- [x] T10 — `internal/core/proxy.go`, gateway-made errors: an `ErrorHandler`
  (first match wins, as plan.md orders it: the request-body watcher holds a read error
  is `400` `client_body`; a `net.Error` timeout is `504` `upstream_timeout`; anything
  else is `502` `upstream_unreachable`), where the reason gives `Adapter.ErrorBody(reason)`, its
  content type, an `x-gateway-error: <reason>` header, and `Meta.GatewayError`. The
  handler never logs and never formats `err`. The request body is wrapped in the
  request watcher (T6) after `Rewrite`'s five steps, keeping `ContentLength`.
  `ErrorLog` is `logging.StdLog(log)`. The test adapter's `ErrorBody` returns a neutral
  envelope. Tests first, in `proxy_test.go`: `TestProxy_GatewayErrorStatus` (connection
  refused, TLS failure, and each of the connect, TLS-handshake and response-header
  timeouts, using a raw `net.Listener` where the test must hang), `TestProxy_NoRetry`
  (an unreachable upstream and an upstream `500` each cause one upstream attempt,
  counted at the upstream), `TestProxy_MalformedClientBody400` (a body short of its
  `Content-Length` after a half-close, and broken chunked framing, each give `400`,
  the adapter's envelope and `x-gateway-error: client_body`, never `502`; the vanished
  client half is T11), `TestProxy_ClientBodyBeatsTimeout` (a body read error beats a
  `net.Error` timeout from the transport), and `TestAccessLog_GatewayErrorField` (`gateway_error` on a `502`
  and a `504`, absent on upstream errors). Done: those pass under `-race` with the leak
  check. Commit: `feat(proxy): return gateway errors in the adapter's envelope` (AC27,
  AC28, AC29, AC38, AC47) (shaped: Q11) (short-body case superseded by Q19; tests
  moved in T11)
- [x] T11 — `internal/core/proxy.go`, disconnects and aborts. The `ErrorHandler`'s first
  branch is now `r.Context().Err() != nil`: it sets `Meta.ClientDisconnected` and writes
  status `499` with no body, no `x-gateway-error` and no `gateway_error`. After headers
  `ReverseProxy` aborts with `http.ErrAbortHandler` and nothing is invented; the T7
  `defer` plus `Meta.Settle` classify it. Tests first, in `proxy_test.go`:
  `TestProxy_ClientDisconnectCancelsUpstream` (cancel while waiting for headers, and
  again mid-stream: upstream's request context is cancelled, and the awaited log line
  has `client_disconnected: true` and status `499` in the first case),
  `TestProxy_UpstreamDiesMidStreamAbortsClient` (the client sees the connection end,
  with no invented event or terminator), `TestAccessLog_UpstreamAbortedField` (upstream
  dying after headers gives `upstream_aborted: true` and no `client_disconnected`; a
  client leaving mid-stream gives the reverse; a completed response and a `502` have
  neither), and `TestProxy_MalformedClientBody400`'s second half (a client that
  vanishes instead gets no body and `client_disconnected: true`). Before the context
  branch, a test first proves that `net/http` keeps the request context live on a
  chunked framing error; if it does not, stop BLOCKED with a new query. T11 also moves
  T10's short-body tests to the malformed-chunk case: `TestProxy_MalformedClientBody400`
  and `TestProxy_ClientBodyBeatsTimeout` prove `400` `client_body` with broken chunked
  framing, and a body short of its `Content-Length` now asserts `499`, no body and
  `client_disconnected: true` (Q19). Done: those pass under `-race` with the leak
  check, and T10's tests still pass. Commit:
  `feat(proxy): handle client disconnects and upstream aborts` (AC30, AC31, AC47, AC48)
  (shaped: Q8, Q12, Q13) (shaped: Q19)
- [x] T12 — `internal/core/registry.go`: `NewRegistry(log)`, `AddProfile`,
  `AddAdapter(a, up)` (builds a `Proxy` per adapter), `Identify(r)` (first matching
  profile in registration order, else `ClientUnknown`) and `Mount(chi.Router)` (the
  signature `server.New` takes), which registers `prefix + "/*"` for every method with
  chi's `Handle`, so `HEAD` and every other standard method are forwarded; a
  non-standard method gets chi's `405` (Q20). A prefix without a trailing slash (`/t`)
  is not under the prefix and gets chi's 404. Tests first, in
  `internal/core/registry_test.go` with the test adapter: `TestRouter_PathOutsidePrefixIs404`
  (the gateway's `404`, no upstream call, `/healthz` still `200`, the bare prefix `404`),
  `TestProfile_UnknownClientProxied` (a request matching no profile is proxied and the
  line has `client: unknown`), `TestCore_TestAdapterAndProfileNeedNoCoreChange` (a test
  adapter and profile, registered from the test, proxy and label a request over
  standard methods only; no edit in `internal/core/`),
  `TestRegistry_IdentifyFirstMatchWins`. Done: those pass. Commit:
  `feat(proxy): add the adapter and profile registry` (AC11, AC33, AC35) (shaped: Q20)

## Client and protocol

- [x] T13 — `internal/clients/claudecode/profile.go`: `Profile` (`Name` is
  `claude-code`; `Match` is true when `User-Agent` starts with `claude-cli/` or
  `x-app: cli` is present). Detection only: no session or agent IDs. It must not import
  `core`, only satisfy its interface. Test first: `TestProfile_ClaudeCodeDetected`
  (`User-Agent: claude-cli/2.1.283 (external, cli)` and `x-app: cli` each match; a
  `Bun/…` user agent with neither header, the `HEAD /api/hello` shape from Q1, does
  not). Done: it passes. Commit: `feat(claude-code): detect claude code requests`
  (AC32) (shaped: Q1)
- [x] T14 — `internal/protocols/anthropic/adapter.go`: `Name = "anthropic"`, `Adapter`
  (`Prefix` `/anthropic`, `DefaultBaseURL` `https://api.anthropic.com`, `AuthKind` from
  header presence only, `ErrorBody` returning the envelope
  `{"type":"error","error":{"type":"api_error","message":"gateway: <reason>"}}` as
  `application/json`). Tests first, in `adapter_test.go`, through
  `core.Registry` and `server.New` against a fake upstream:
  `TestProxy_AnthropicRoutes` (`POST /anthropic/v1/messages`, `.../count_tokens`,
  `GET .../v1/models`, `.../v1/models/{id}`, `HEAD .../api/hello`: each arrives at the
  path without the prefix, `?beta=true` kept), `TestProxy_UnknownPathUnderPrefixForwarded`,
  `TestProxy_HeadAPIHelloReturnsUpstreamStatus` (upstream's status and headers, empty
  body, never answered locally), `TestProxy_AuthHeadersForwardedUnchanged` (`x-api-key`,
  and `Authorization: Bearer` + `anthropic-beta`, byte-identical),
  `TestProxy_UpstreamUnreachable502` and `TestProxy_UpstreamTimeout504` (this envelope
  and `x-gateway-error`), `TestProxy_ClientBodyEnvelope` (`400`, this envelope,
  `client_body`), `TestProxy_UpstreamErrorsVerbatim` (`429`, `500` and `529` with
  `anthropic-ratelimit-*` headers, all byte-identical), and `TestAccessLog_AuthKind`
  (`api_key`, `bearer`, `both` when `x-api-key` and `Authorization` are both present,
  `none`). T14 also adds `AuthBoth AuthKind = "both"` to `internal/core/adapter.go`,
  next to `AuthNone`, `AuthAPIKey` and `AuthBearer`, so `AuthKind` can report it.
  Done:
  those pass under `-race`. Commit: `feat(anthropic): add the messages adapter` (AC9,
  AC10, AC13, AC17, AC26, AC27, AC28, AC37, AC47) (shaped: Q1, Q2, Q3, Q15, Q18)

## Wiring

- [x] T15 — `cmd/gateway/run.go`: build the adapter list and profile list once; pass
  `config.Options.Upstreams` from the same list, so a key and its adapter cannot drift;
  build the `core.Registry` (`AddProfile(claudecode.Profile{})`, `AddAdapter` with
  `cfg.Upstreams["anthropic"]`) and add `registry.Mount` to the `server.New` mounts
  ahead of the test-only `deps.mount` routes. `cmd/gateway` is the only package that
  imports an adapter or a profile. Tests first, in `cmd/gateway/run_proxy_test.go`, all
  through `run` against a fake upstream on loopback: `TestRun_ProxiesAnthropicEndToEnd`
  (`protocol: anthropic`, `client: claude-code`, `auth: api_key`),
  `TestRun_UpstreamEnvOverrideLogged` (an `GATEWAY_UPSTREAMS_ANTHROPIC_*` override
  appears in the startup line's `env_overrides`), and `TestProxy_SecretsNotLogged` (a
  sentinel in `x-api-key`, `Authorization`, the query and the body is in no stdout or
  stderr, on a success, on a `502`, on a mid-stream abort and on a panicking test
  route). Done: those three pass under `-race`, and the log line for an unknown user
  agent has `client: unknown`. Commit: `feat(server): mount the anthropic adapter in run`
  (AC2, AC36, AC37, AC38, AC39)
- [x] T16 — `cmd/gateway/run_shutdown_test.go`: a guard over behaviour the earlier
  tasks built, with no production change. Test: `TestRun_ShutdownWaitsForStream` (a real
  proxied stream, through `run` with the default `shutdown_timeout`; shutdown starts
  mid-stream; the stream reaches its end and the process exits `0`). It passes on
  arrival, so it is proved by running it once with `GATEWAY_SHUTDOWN_TIMEOUT=100ms` and
  seeing the stream cut and a non-zero exit, then reverting. Done: it passes under
  `-race`. Commit: `test(server): prove shutdown waits for a proxied stream` (AC40)
- [x] T17 — `docker-compose.yml`: `stop_grace_period: 10m` on the gateway service, and
  `test/conventions/compose_test.go`, which parses the file with the `yaml` package
  already in `go.mod` and compares it to `config.Defaults().ShutdownTimeout`, so the two
  cannot drift. Test first: `TestCompose_StopGracePeriodMatchesShutdownTimeout`, which
  fails before the compose edit. Done: it passes, and `docker compose config` still
  parses the file if Docker is present (if not, say so in the note). Commit:
  `build(deploy): set the compose stop grace period` (AC43)

## Fixture guard and docs

- [x] T18 — `test/conventions/fixtures_test.go`: walks every `testdata` directory in the
  module and fails on `sk-ant-` keys, `Bearer` (any case) followed by a token, any UUID, any
  email address, any `wrkspc_` workspace ID, and the words `organization`, `account_uuid`
  and `org_id`. The scanner is a function the test calls, so it can be
  proved on seeded input. Tests first:
  `TestFixtureScanner_FlagsEachIdentifier` (a temp directory seeded with one of each
  identifier, each flagged, and a clean file passes) and `TestFixtures_NoIdentifiers`
  (the real tree; passes vacuously until T21 adds a fixture). Done: both pass. Commit:
  `test(repo): fail on identifiers in test fixtures` (AC25)
- [x] T19 — `docs/decisions/0003-reverseproxy-with-rewrite-as-proxy-engine.md`: `status:
  approved`. `PLAN.md` §8, the "Proxy engine" row: `Proposed` becomes `Decided
  ([ADR 0003](docs/decisions/0003-reverseproxy-with-rewrite-as-proxy-engine.md))`, in the
  same commit. No other line of `PLAN.md` changes. Done: `git diff` shows exactly those
  two edits, and `make verify` passes. Commit: `docs(repo): mark adr 0003 approved` (no
  AC; plan "Files and packages touched")
- [x] T20 — `docs/clients/claude-code.md` (replacing the `.gitkeep`). It has four
  sections. (1) Setup: `ANTHROPIC_BASE_URL=http://127.0.0.1:7197/anthropic`, in API-key
  mode (`ANTHROPIC_API_KEY`) and in subscription mode (`/login`, no
  `ANTHROPIC_API_KEY`). (2) The known limits from `PLAN.md` §3, quoted by link, not
  copied. (3) The smoke checklist: the numbered steps of T22, written for a reader and
  kept identical in substance. (4) Shutdown: stopping the gateway, `docker compose stop`
  included, can take up to 10 minutes while streams finish, and a second Ctrl-C stops
  `make run` at once. Done: a reader of the page finds all four items
  (`ManualDocs_ClaudeCodePage` is this read, checked by the reviewer against the AC's
  four items), and `make verify` passes. Commit:
  `docs(claude-code): add the claude code setup page` (AC46)
- [ ] T21 — `internal/protocols/anthropic/golden_test.go`: a fake upstream serves
  `testdata/stream.sse` with `testdata/stream.headers`, flushing at each blank-line event
  boundary, through the real adapter and `server.New`; the client's bytes must equal the
  file. The fixture is recorded and committed by the owner, not by the implementer
  (plan "Golden fixture" steps 1–4: the throwaway recording proxy with a Claude Code
  session; `stream.sse`, `stream.headers` and `README.md` under
  `internal/protocols/anthropic/testdata/`, scrubbed of every UUID, key, token, email and
  `organization` / `account_uuid` / `org_id`). If those files are absent the implementer
  reports BLOCKED on Q14 and adds no new query; it never fakes a fixture and never
  skips the test. Test first: `TestProxy_GoldenAnthropicStreamReplay`. Done: it passes
  under `-race`, and `TestFixtures_NoIdentifiers` passes over the real fixture. Commit:
  `test(anthropic): replay the golden stream through the gateway` (AC24, AC25)
  (shaped: Q10) (shaped: Q14)

## Manual evidence (no commit; output goes into the PR as evidence)

- [ ] T22 — Smoke Claude Code through the gateway, in both auth modes. The implementer
  reports NEEDS-HUMAN: it has no Claude Code login, no API key, and no way to run an
  interactive session. Nothing is built here; nothing is committed. Run this, in this
  order, after T1–T21 are ticked. AC44's API-key half needs an API key that research
  Q4 says is not yet available.

  Setup, once. Terminal A: `make run 2>&1 | tee /tmp/llmgw-smoke.log` (the log is JSON
  lines). Terminal B, in a scratch directory that holds a few files:
  `export ANTHROPIC_BASE_URL=http://127.0.0.1:7197/anthropic`.

  Run the same seven steps for each mode.
  1. Set the mode. API key (AC44): `export ANTHROPIC_API_KEY=<your key>`. Subscription
     (AC45): `unset ANTHROPIC_API_KEY`, start `claude`, run `/login` with the claude.ai
     account, then `/exit`.
  2. Tool-call turn: `claude -p "Use the Bash tool to run ls and tell me how many
     entries there are." --allowedTools Bash`. It exits `0`, runs the tool and answers
     correctly.
  3. Streamed turn: `claude`, then ask `Write 200 words about lighthouses.` The text
     appears as it is generated, not in one lump at the end. Then `/exit`.
  4. Direct baseline: repeat steps 2 and 3 once with `env -u ANTHROPIC_BASE_URL claude
     …` and note that they behave the same (same success, same streaming).
  5. Session start: `jq -c 'select(.msg=="request" and .method=="HEAD")'
     /tmp/llmgw-smoke.log`. In an interactive session there is one line for
     `/anthropic/api/hello` (research Q1; `-p` mode may not send it), with upstream's
     status and `client: unknown`.
  6. Messages lines: `jq -c 'select(.msg=="request" and (.path|endswith("/v1/messages")))
     | {path,status,protocol,client,auth,stream,ttfb_ms}' /tmp/llmgw-smoke.log`. Every
     line shows `protocol: anthropic` and `client: claude-code`; `auth` is `api_key`
     in AC44's run and `bearer` in AC45's; `stream: true` for the streamed turn.
  7. Secrets: `grep -c "$ANTHROPIC_API_KEY" /tmp/llmgw-smoke.log` (API-key mode) and
     `grep -ci 'sk-ant\|bearer ' /tmp/llmgw-smoke.log` (both modes) print `0`.

  Evidence to paste into the PR, per mode: the gateway sha (`git rev-parse --short
  HEAD`); one line for step 2 and one for step 3 each saying what happened and that it
  matches the direct run; the step 5 and step 6 output; the step 7 counts. Redact
  nothing else and never paste a key or token. Then append a dated answer line under
  Q4 in `research.md` with the API-key mode result and set its Status to answered, so
  the task's `(blocked: Q4)` becomes `(shaped: Q4)`. If any step fails, do not
  work around it: add an open query and a fix task. (AC44, AC45) (shaped: Q3)
  (blocked: Q4) (needs-human)

## Coverage check

Every AC in `spec.md` (AC1–AC48) maps to at least one task:

| AC | Tasks | AC | Tasks | AC | Tasks |
|---|---|---|---|---|---|
| AC1 | T1 | AC17 | T14 | AC33 | T12 |
| AC2 | T1, T15 | AC18 | T9 | AC34 | T6 |
| AC3 | T2 | AC19 | T9 | AC35 | T12 |
| AC4 | T2 | AC20 | T8 | AC36 | T7, T9, T15 |
| AC5 | T1 | AC21 | T8 | AC37 | T14, T15 |
| AC6 | T1 | AC22 | T8 | AC38 | T7, T10, T15 |
| AC7 | T1 | AC23 | T9 | AC39 | T15 |
| AC8 | T8 | AC24 | T21 | AC40 | T16 |
| AC9 | T14 | AC25 | T18, T21 | AC41 | T3 |
| AC10 | T14 | AC26 | T9, T14 | AC42 | T4 |
| AC11 | T12 | AC27 | T10, T14 | AC43 | T17 |
| AC12 | T8 | AC28 | T10, T14 | AC44 | T22 |
| AC13 | T14 | AC29 | T10 | AC45 | T22 |
| AC14 | T8 | AC30 | T7, T11 | AC46 | T20 |
| AC15 | T8 | AC31 | T11 | AC47 | T10, T11, T14 |
| AC16 | T8 | AC32 | T13 | AC48 | T7, T11 |

No AC is missing: 48 of 48 appear. T5 (bare-`go` test) and T19 (ADR status) name no AC:
they carry a spec Don't and a plan file-table entry.
