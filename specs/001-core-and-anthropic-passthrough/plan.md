---
status: approved
spec: ./spec.md
---

# 001 — Implementation plan

> Agent-drafted against the spec, human-approved. Written in a separate session, after
> the spec is approved. Do not write this file at the same time as the spec.

## Approach

`httputil.ReverseProxy` with a `Rewrite` hook is the engine (ADR 0003). One
`core.Proxy` is built per registered adapter and mounted under the adapter's prefix.
Everything provider- or client-shaped is a value the adapter or profile hands to core:
a prefix, an error body, an auth kind, a client name. Core holds no `if` on any of them.

Each hook below was checked against the standard library source (go1.25.4) and a
throwaway program, not assumed. Where the check changed the agreed answer, the entry
says so and cites the research query.

### Request rewrite (AC8, AC12, AC14–AC16)
`Rewrite(pr *httputil.ProxyRequest)` does exactly this, in order:
1. Strip the adapter prefix from `pr.Out.URL.Path` **and** `RawPath` (when set), so an
   escaped path keeps its escapes byte for byte.
2. `pr.SetURL(base)`: scheme and host from `base_url`, the base path joined in front,
   and `Out.Host` cleared so the upstream host is used (AC15, AC12). Never
   `SetXForwarded`.
3. Put `pr.In.URL.RawQuery` back. `ReverseProxy` runs `cleanQueryParams` before
   `Rewrite`, which drops parameters it cannot parse, so without this the query is not
   byte-identical (research Q5).
4. Put back the client's `Forwarded`, `X-Forwarded-For`, `X-Forwarded-Host` and
   `X-Forwarded-Proto` from `pr.In`, skipping any name the client's `Connection` header
   lists. `ReverseProxy` deletes them before `Rewrite`, but the spec forwards everything
   except hop-by-hop (Q5). Nothing is ever *added*.
5. `pr.Out.Header.Del("Te")`. `ReverseProxy` re-adds `Te: trailers` after stripping,
   but `TE` is on the spec's hop-by-hop list (Q5). Likewise `Del("Connection")` and
   `Del("Upgrade")`, which it re-adds for a protocol switch, and every `Proxy-*`
   header, of which its own list names only `Proxy-Authorization` and
   `Proxy-Connection` (Q16).

Everything else is left as the client sent it: `ReverseProxy` already strips the rest
of the hop-by-hop headers (including those named in `Connection`) before `Rewrite`, and the
gateway's request ID lives only on the response writer, so it never reaches upstream
(AC16). The gateway does not clean `..` segments: a path is forwarded as sent.

One more step, after the five above, exists only to classify errors (AC47): when
`pr.Out.Body` is non-nil it is wrapped in a read-through watcher. The watcher passes
every `Read` and `Close` straight to the client's body, changes no byte, adds no
buffering and keeps `ContentLength`; it only remembers the first non-`io.EOF` read
error, in an `atomic.Pointer` because the transport reads it on its own goroutine. The
error value is kept for classification and never logged. It decides only while the
connection is live, such as broken chunked framing: a body short of its
`Content-Length` also leaves an error here, but `net/http` has already cancelled the
request context, so the error handler's context branch classifies it first (Q19).

### Transport (AC21, AC22, AC27, AC28)
One `*http.Transport` per upstream, built explicitly rather than cloned from
`http.DefaultTransport` (which a test in the same process could have replaced):
- `DisableCompression: true`, so `Accept-Encoding` goes upstream as sent and the
  gateway never inflates a body (AC21, AC22).
- `DialContext` from a `net.Dialer{Timeout: connect_timeout, KeepAlive: 30s}`. The
  timeout covers DNS and connect together.
- `TLSHandshakeTimeout` and `ResponseHeaderTimeout` from config. No `Timeout` anywhere:
  there is no whole-request deadline (spec, Upstream config).
- `TLSClientConfig` left nil: system roots, Go's default minimum TLS version.
- `ForceAttemptHTTP2: true`, because a custom `DialContext` otherwise turns HTTP/2 off.
- `MaxIdleConns: 100`, `MaxIdleConnsPerHost: 32`, `IdleConnTimeout: 90s`,
  `ExpectContinueTimeout: 1s`. The stdlib default of 2 idle connections per host would
  make parallel sub-agents re-handshake TLS constantly.
- `Proxy`: `http.ProxyFromEnvironment` (research Q9: the standard proxy env vars are honoured).

### Flushing (AC23)
`FlushInterval: -1` flushes after every write. It goes through
`http.NewResponseController`, which follows `Unwrap`, and 000's chi wrapper implements
both `Flush` and `Unwrap` (Q7), so the wrapper stays. AC23 is the guard: if a later
middleware wraps the writer without `Unwrap`, the test fails.

### Response hook (AC18, AC19, AC23, AC36)
`ModifyResponse(res)` runs after `ReverseProxy` has removed hop-by-hop headers:
0. Delete every `Proxy-*` header, which `ReverseProxy` does not (Q16). This step lands
   with the request half (T8), because AC14 checks both directions.
1. `res.Header.Del("X-Request-Id")`. `ReverseProxy` copies response headers with
   `Header.Add`, so setting the gateway's ID here would appear twice. The gateway's ID
   is already on the writer, set by 000's `requestID` middleware, so deleting
   upstream's leaves exactly one (Q6). The agreed "overwrite" is met without core ever
   reading the request ID. Anthropic's `request-id` is a different name and is not
   touched (AC19).
2. Record `Meta.Stream` (`Content-Type` is `text/event-stream`, a generic media type)
   and `Meta.TTFB` (time since the proxy handler started, read from the `Meta`).
3. Wrap `res.Body` in the same kind of read-through watcher as the request side, to
   remember the first non-`io.EOF` read error, so an upstream failure after headers can
   be told from a client that left (AC48, Q12). It runs on the handler's goroutine,
   because `ReverseProxy` reads the body there, so it needs no atomic.

Nothing else is changed: no `Content-Length`, no `Content-Encoding`, and the body
bytes and their timing pass through the watcher untouched.

One guard outside the hook keeps that true for a response with no `Content-Type`
(AC18). `net/http` sniffs one when the first body write carries the headers out;
`FlushInterval: -1` usually flushes the headers first, but that is a timer race. So
`ServeHTTP` puts an empty `Content-Type` entry on the writer when it has none, which
stops the sniff outright; `ReverseProxy` adds upstream's value to that entry when
upstream sent one.

### Error handler (AC27–AC31)
`ErrorHandler(w, r, err)`, first match wins:
1. `r.Context().Err() != nil`: the client left. Set `Meta.ClientDisconnected`, write
   the header only with status `499`, no body (Q13). No `x-gateway-error`, no
   `gateway_error`: the gateway did not create an error (AC30).
   This includes a body that ends short of its `Content-Length`: `net/http` cancels
   the request context on the connection's `EOF`, so a short body cannot be told from
   a client that left, and it is a disconnect, not a `400` (Q19).
2. The request-body watcher holds a read error on a live connection: the client's own
   body is malformed, such as broken chunked framing, which keeps the context live.
   `400`, reason `client_body`, never a 502 (Q11, Q19, AC47).
   `TestNetHTTP_ChunkedFramingErrorKeepsContextLive` pins that `net/http` keeps the
   context live on a chunked framing error, which this order relies on.
3. `errors.As(err, &net.Error)` with `Timeout()`: `504`, reason `upstream_timeout`. The
   dial, TLS-handshake and response-header timeouts are all `net.Error` timeouts (AC28).
4. Anything else: `502`, reason `upstream_unreachable` (AC27).

The 400, 502 and 504 get `Content-Type` and body from `adapter.ErrorBody(reason)` and
an `x-gateway-error: <reason>` header, and set `Meta.GatewayError`. The handler logs
nothing and never formats `err`: it can carry the upstream address. The gateway's
`X-Request-Id` is already on the writer.

`ReverseProxy` calls the handler only before headers are written. After them it can
only abort: it panics `http.ErrAbortHandler`, which `recoverer` re-panics and
`net/http` turns into a dropped connection (AC31; nothing invented, Q8). Whether that
abort was upstream's fault or the client's is decided afterwards, from the response
watcher (see the next section). `ErrorLog` is
`logging.StdLog(log)` so the one line `ReverseProxy` writes on that path is JSON, not
plain text. Nothing retries: the handler never re-issues, and the one retry left in the
stack is `http.Transport`'s replay of a request it could not write on a stale idle
connection, which upstream never sees (see Risks).

### Request metadata (AC30, AC36–AC38)
`core.Meta` is a plain struct in a context value. 000's `accessLog` creates it
(`core.WithMeta`) before the handler chain, and after the handler reads it.
`core.Proxy.ServeHTTP` fills `Protocol`, `Client` and `Auth` before handing to
`ReverseProxy`. The hooks run on the handler's goroutine, so `Meta` needs no lock: it
is written before `next.ServeHTTP` returns and read after (`-race` checks this). The one
field written from another goroutine, the request-body error, lives in the watcher's
atomic and is read by the error handler only.

`accessLog` changes in two ways:
- It adds the proxy fields only when `Meta.Protocol != ""`, so `/healthz` and 404 lines
  are unchanged. `ttfb_ms` is present only once headers arrived, `gateway_error` only
  when set.
- It logs from a `defer`. Today it logs after `next.ServeHTTP`, so a handler that ends
  by `http.ErrAbortHandler` (a mid-stream failure, a client that left) writes no line
  at all (Q8). In the `defer` it calls `Meta.Settle(ctx)`, which decides the two abort
  flags once the handler is done:
  - the response watcher holds a read error and `r.Context().Err() == nil`: upstream
    failed after headers, so `UpstreamAborted` (Q12, AC48);
  - otherwise, `r.Context().Err() != nil`: `ClientDisconnected` (AC30).

  `upstream_aborted` and `client_disconnected` are logged only when true, like
  `gateway_error`. `Settle` is used instead of a `recover` in the proxy so a real panic
  keeps its original stack for `recoverer`. When the client leaves, the outbound read
  error is `context.Canceled` and the inbound context is cancelled too, which is why
  the check reads the inbound one.

`internal/server` imports `internal/core` for `Meta`. `internal/core` never imports
`internal/server`.

### Registry and routes (AC9–AC11, AC32–AC35)
`core.Registry` holds adapters (with their upstreams) and profiles in registration
order. `Registry.Mount(chi.Router)` has the signature `server.New` already takes:
for each adapter it registers `prefix + "/*"` for every method with chi's
`Handle`, so every standard method chi knows, `HEAD` included, is forwarded.
`/anthropic` with no trailing slash is not under the prefix and gets chi's 404.
Anything else is chi's 404 and never reaches a proxy (AC11). Known limit (Q20): a
non-standard method gets chi's `405`, inside or outside a prefix, because chi checks
the method before the path. If a real client ever needs one, dispatch by prefix before
chi sees the request.

`Registry.Identify(r)` returns the first profile whose `Match` is true, else
`ClientUnknown` (`"unknown"`). The order profiles are registered in is the tie-break.

### Config: nested keys and adapter-supplied upstreams (AC1–AC7)
Core and config must not name a provider, so the *names* and the default `base_url`
come from the adapters:
- `Adapter.DefaultBaseURL()` gives the default, `Adapter.Name()` the key.
- `config.Options` gains `Upstreams []UpstreamSpec{Name, DefaultBaseURL}`; `run.go`
  builds it from the same adapter list it registers, so a key and its adapter cannot
  drift. An `upstreams` name with no spec is `unknown key`.
- `Config.Upstreams` is `map[string]Upstream`. This makes `Config` non-comparable, so
  the three `cfg != Defaults()` checks in `internal/config/load_test.go` become
  `reflect.DeepEqual`. The assertion is the same, not weaker.

The loader treats a setting's key as a dotted path. Three edits, no rewrite:
1. `settings` becomes a function of the specs: the three flat settings, then one
   `upstreams.<name>.<field>` setting per spec and field (`base_url`,
   `connect_timeout`, `tls_handshake_timeout`, `response_header_timeout`).
2. `envName` upper-cases the key and maps `.` and `-` to `_`:
   `upstreams.anthropic.base_url` → `GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL`.
3. `parseFile` walks one level down under `upstreams`: each name must be a spec and
   each field a known one, else `unknown key` with the dotted path and the line.
   `fileConfig` gains `Upstreams map[string]map[string]*string` for the final strict
   decode, and `fileValues` flattens it to dotted keys.

`base_url` validation is one function returning `invalid url` (a new fixed reason, so
the message never holds the value): reject if the string contains `?` or `#` (this
catches an empty query or fragment that `url.Parse` swallows), or `url.Parse` fails,
or the URL is not absolute, or the host is empty, or userinfo is set. Scheme must be
`https`, or `http` when the hostname is `localhost` or `net.ParseIP(...).IsLoopback()`.
The three timeouts reuse `setShutdownTimeout`'s rule (`> 0`, `invalid duration`).

`ShutdownTimeout`'s default becomes `10m` and `config.example.yaml` gains the nested
block at the defaults (AC7, AC1).

### Recovery and goroutines (AC41, AC42)
Both live in `internal/logging`, the one place that knows zap:
- `LogPanic(log, msg string, v any, fields ...zap.Field)` writes one error line with
  `panic_type` (`%T`) and `stack` (`debug.Stack()`), never `v`. `recoverer` calls it,
  replacing `zap.Any("panic", v)` (agreed answer 9).
- `Go(log, name string, fn func() error) <-chan error` starts the goroutine, recovers a
  panic through `LogPanic`, and delivers `fn`'s error, or `ErrPanicked` after a panic,
  on a buffered channel. The channel is what stops `run` waiting forever if `Serve`
  panics. `run.go`'s one existing bare `go func() { serveErr <- srv.Serve(ln) }()`
  becomes `logging.Go`.

A `%T` is a type name and cannot hold request data. `debug.Stack()` prints arguments
as words and pointers, so a string argument shows its address and length, not its
bytes.

"No bare `go` elsewhere" is reviewer-enforced, as agreed, and also made a test: an
AST walk over every non-`_test.go` file that fails on a `go` statement outside
`internal/logging/go.go`. It needs no dependency and no lint change, and it turns the
spec's Don't into a check that fails on the diff that breaks it.

### Golden fixture (AC24, AC25)
Recorded once, outside `make verify`, with no committed script (Q10):
1. Run the throwaway recording proxy from the Q1–Q3 probe, extended to save the
   upstream status, headers and body, and to send `Accept-Encoding: identity` so the
   body is readable text. Point Claude Code at it and run one short prompt that
   streams. It is not the gateway, so the fixture does not verify itself.
2. Copy the saved response into
   `internal/protocols/anthropic/testdata/`: `stream.sse` (the body, byte-exact) and
   `stream.headers` (status line and the headers to replay).
3. Scrub by hand: `anthropic-organization-id`, `request-id` values, `cf-*` and any
   `set-cookie`, and every UUID, key, token and email. The response body carries no
   request content, but check it.
4. `testdata/README.md` records the date, the model, the recording steps and what was
   scrubbed, so the fixture's provenance survives without the script.

The replay serves `stream.sse` as a fake upstream, flushing at each blank-line event
boundary, and compares the client's bytes to the file (AC24). `TestFixtures_NoIdentifiers`
walks every `testdata` directory in the module and fails on: `sk-ant-` keys, `Bearer`
(any case) followed by a token, any UUID, any email address, any `wrkspc_` workspace
ID, and the names `organization`, `account_uuid` and `org_id`. UUIDs are banned
outright, which makes scrubbing `anthropic-organization-id` a hard failure rather than a
review point.

### Shutdown and compose (AC40, AC43)
No new mechanism: `srv.Shutdown` already waits for in-flight handlers, and a proxied
stream is one. The change is the default, `shutdown_timeout: 10m`, in `Defaults()` and
`config.example.yaml`, and `stop_grace_period: 10m` on the compose service. AC43 parses
`docker-compose.yml` with the yaml package already in `go.mod` and compares it to
`config.Defaults().ShutdownTimeout`, so the two cannot drift.

### Build order (input to tasks.md)
1. Config: nested keys, `invalid url`, `10m`, example file (AC1–AC7).
2. `internal/logging`: `LogPanic`, `Go`; `run.go` and `recoverer` switch to them; 000
   tests updated (AC41, AC42).
3. `core.Meta` and the `accessLog` change (`defer`, proxy fields).
4. `core`: interfaces, registry, proxy, error handler, with a test adapter (AC8–AC23,
   AC26–AC31, AC33–AC35, AC47, AC48).
5. `anthropic` adapter and `claudecode` profile; wire in `run.go` (AC9, AC10, AC13,
   AC17, AC27, AC28, AC32, AC36–AC39).
6. Compose and shutdown (AC40, AC43); record the fixture (AC24, AC25).
7. `docs/clients/claude-code.md` and the manual smoke (AC44–AC46).

## Files and packages touched

| Path | Why |
|---|---|
| `internal/core/adapter.go` | `Adapter`, `Profile`, `AuthKind`, `ClientUnknown` |
| `internal/core/registry.go` | `Registry`: `AddAdapter`, `AddProfile`, `Identify`, `Mount` |
| `internal/core/proxy.go` | `Proxy`: `ReverseProxy` setup, `Rewrite`, `ModifyResponse`, `ErrorHandler`, transport |
| `internal/core/meta.go` | `Meta`, `WithMeta`, `MetaFrom` |
| `internal/core/*_test.go` | Proxy, registry, purity and test-adapter tests (package `core_test`) |
| `internal/protocols/anthropic/adapter.go` | `Name`, `Prefix`, `DefaultBaseURL`, `AuthKind`, `ErrorBody` |
| `internal/protocols/anthropic/*_test.go`, `testdata/` | Route table, envelope, auth, golden replay, fixture README |
| `internal/clients/claudecode/profile.go`, `profile_test.go` | Detection only |
| `internal/config/config.go`, `load.go`, `upstream.go` | `Upstream`, `UpstreamSpec`, nested keys, `invalid url`, `10m` default |
| `internal/config/load_test.go`, `load_upstream_test.go` | 000 comparisons move to `reflect.DeepEqual`; new upstream tests (package `config_test`) |
| `internal/logging/panic.go`, `go.go` and tests | `LogPanic`, `Go`, `ErrPanicked` |
| `internal/server/middleware.go` | `accessLog` (`defer`, `Meta`, proxy fields), `recoverer` uses `LogPanic` |
| `internal/server/server_test.go` | 000's `panic` value assertion becomes `panic_type` plus "value absent" (AC41) |
| `cmd/gateway/run.go` | Build the adapter list, `config.Options.Upstreams`, registry, mount; `logging.Go` for `Serve` |
| `cmd/gateway/run_proxy_test.go`, `run_logging_test.go` | End-to-end through `run`; the `panic` assertion at line 157 changes as above |
| `test/conventions/` | `TestFixtures_NoIdentifiers`, `TestCompose_StopGracePeriodMatchesShutdownTimeout`, `TestNoBareGoStatements` (repo-shape tests, like `test/githooks`) |
| `config.example.yaml` | Nested `upstreams.anthropic` block; `shutdown_timeout: 10m` |
| `docker-compose.yml` | `stop_grace_period: 10m` |
| `docs/clients/claude-code.md` | AC46 |
| `docs/decisions/0003-reverseproxy-with-rewrite-as-proxy-engine.md` | ADR for the engine |

`internal/providers/` is not created: there is no pricing or provider table here.
`PLAN.md` §8's "Proxy engine" row flips from Proposed to Decided (ADR 0003) in the PR
that implements this, in the same change as the ADR's status.

## Interfaces introduced or changed

```go
// internal/core
type AuthKind string

const (
	AuthNone   AuthKind = "none"
	AuthAPIKey AuthKind = "api_key"
	AuthBearer AuthKind = "bearer"
	AuthBoth   AuthKind = "both" // an API key and a bearer token are both present (Q15)
)

const ClientUnknown = "unknown"

// Adapter is one wire protocol. Everything provider-shaped is a value it returns.
type Adapter interface {
	Name() string // logged as `protocol`
	Prefix() string // "/anthropic": leading slash, no trailing one
	DefaultBaseURL() string // the upstream's default; config validates it like any other
	AuthKind(h http.Header) AuthKind
	ErrorBody(reason string) (contentType string, body []byte)
}

// Profile is one client. Detection only in 001.
type Profile interface {
	Name() string
	Match(r *http.Request) bool
}

type Registry struct{ /* adapters, profiles, log */ }
func NewRegistry(log *zap.Logger) *Registry
func (r *Registry) AddProfile(p Profile)
func (r *Registry) AddAdapter(a Adapter, up config.Upstream)
func (r *Registry) Identify(req *http.Request) string
func (r *Registry) Mount(rt chi.Router) // has server.New's mount signature

type Proxy struct{ /* ReverseProxy, adapter, identify */ }
func NewProxy(a Adapter, up config.Upstream, identify func(*http.Request) string, log *zap.Logger) *Proxy
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request)

type Meta struct {
	Protocol           string
	Client             string
	Auth               AuthKind
	Stream             bool
	TTFB               time.Duration
	HasTTFB            bool
	GatewayError       string
	ClientDisconnected bool
	UpstreamAborted    bool
	// unexported: the time the proxy handler started, and the two body watchers
}
func WithMeta(ctx context.Context) (context.Context, *Meta)
func MetaFrom(ctx context.Context) *Meta // nil outside a request
func (m *Meta) Settle(ctx context.Context) // sets UpstreamAborted / ClientDisconnected; called by accessLog's defer

// internal/protocols/anthropic
const Name = "anthropic"
type Adapter struct{}                    // satisfies core.Adapter
// internal/clients/claudecode
type Profile struct{}                    // satisfies core.Profile

// internal/config (changed)
type Upstream struct {
	BaseURL               *url.URL
	ConnectTimeout        time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
}
type UpstreamSpec struct{ Name, DefaultBaseURL string }
type Options struct {
	Path, DefaultPath string
	LookupEnv         func(string) (string, bool)
	Upstreams         []UpstreamSpec // new: what adapters are registered
}
type Config struct {
	ListenAddr      string
	LogLevel        string
	ShutdownTimeout time.Duration       // default 10m (was 30s)
	Upstreams       map[string]Upstream // new; makes Config non-comparable
}
// Load's signature is unchanged. New fixed reason: "invalid url".

// internal/logging (new)
var ErrPanicked = errors.New("goroutine panicked")
func LogPanic(log *zap.Logger, msg string, v any, fields ...zap.Field)
func Go(log *zap.Logger, name string, fn func() error) <-chan error

// internal/server (unchanged signatures; behaviour changed)
// accessLog: logs from a defer, and adds the proxy fields when Meta.Protocol != "".
// recoverer: logs panic_type and stack, not the value.
```

`internal/config` imports nothing from `core`; `core` imports `config` for `Upstream`
only. `internal/server` imports `core` for `Meta`. `cmd/gateway` is the only package
that imports an adapter or a profile.

## New dependencies

None. Everything is the standard library (`net/http/httputil`, `httptest`,
`go/parser` for the AST test) plus what `go.mod` already holds: chi, zap, yaml. The
compose test reads `docker-compose.yml` with the existing yaml package. No goroutine
leak library: the leak check below is a stack scan. Any dependency added while building
this needs its own ADR first.

## Testing strategy

Everything in `make verify`, under `-race`, with no live API. Upstreams are
`httptest.Server`s, or a raw `net.Listener` where the test must drop a connection,
hang, or stall. Log lines are read as JSON from a goroutine-safe buffer, as in 000.

**Shared helpers** (`internal/core`, test files only):
- A test adapter and profile, registered from the test (AC35). The test adapter's name
  and prefix are neutral (`test`, `/t`), which keeps `core`'s tests free of provider
  names.
- A stream helper: an upstream that writes one event, waits on a channel the test
  controls, then the next. AC23 fails if the client sees the first event only after
  the second is released.
- A leak check: at test end, poll up to 2s for `runtime/pprof`'s goroutine profile to
  hold no stack through `internal/core` or the test's own server. Every proxy test that
  ends with a cancel, a timeout or an abort calls it. `-race` then also covers the
  `Meta` handoff.

### AC evidence

| AC | Test | File |
|---|---|---|
| 1 | `TestLoad_UpstreamDefaults` | `internal/config/load_upstream_test.go` |
| 2 | `TestLoad_UpstreamEnvOverridesFile`; the startup line half: `TestRun_UpstreamEnvOverrideLogged` | `internal/config/load_upstream_test.go`, `cmd/gateway/run_proxy_test.go` |
| 3 | `TestLoad_InvalidUpstreamURL` (five rows; sentinel value absent from `Error()` and its fields) | `internal/config/load_upstream_test.go` |
| 4 | `TestLoad_UpstreamURLAccepted` | same |
| 5 | `TestLoad_InvalidUpstreamDuration` (3 keys × `abc`, `0s`, `-5s` × file, env) | same |
| 6 | `TestLoad_UnknownUpstreamKey` | same |
| 7 | `TestLoad_ExampleFileIsDefaults` (existing; the example file gains the block) | `internal/config/load_test.go` |
| 8 | `TestProxy_ForwardsRequestVerbatim`, including a client `X-Forwarded-For` and a `;` and `%zz` in the query (Q5) | `internal/core/proxy_test.go` |
| 9 | `TestProxy_AnthropicRoutes` | `internal/protocols/anthropic/adapter_test.go` |
| 10 | `TestProxy_UnknownPathUnderPrefixForwarded` | same |
| 11 | `TestRouter_PathOutsidePrefixIs404` | `internal/core/registry_test.go` |
| 12 | `TestProxy_BaseURLPathPrefixJoined` | `internal/core/proxy_test.go` |
| 13 | `TestProxy_HeadAPIHelloReturnsUpstreamStatus` | `internal/protocols/anthropic/adapter_test.go` |
| 14 | `TestProxy_StripsHopByHopHeaders`, including a header named by `Connection` and `Te: trailers` | `internal/core/proxy_test.go` |
| 15 | `TestProxy_SetsUpstreamHost` | same |
| 16 | `TestProxy_AddsNoHeadersUpstream` | same |
| 17 | `TestProxy_AuthHeadersForwardedUnchanged` | `internal/protocols/anthropic/adapter_test.go` |
| 18 | `TestProxy_ResponseVerbatim`, including a response with no `Content-Type` | `internal/core/proxy_test.go` |
| 19 | `TestProxy_GatewayRequestIDWinsOnResponse` (exactly one `X-Request-Id`, Q6) | same |
| 20 | `TestProxy_LargeBodyNotLimited` (64 MiB generated by a reader, hashed upstream; never held whole) | same |
| 21 | `TestProxy_CompressionPassThrough` | same |
| 22 | `TestProxy_TransparentGzipDisabled` | same |
| 23 | `TestProxy_StreamsSSEWithoutBuffering` | same |
| 24 | `TestProxy_GoldenAnthropicStreamReplay` | `internal/protocols/anthropic/golden_test.go` |
| 25 | `TestFixtures_NoIdentifiers` | `test/conventions/fixtures_test.go` |
| 26 | `TestProxy_UpstreamErrorsVerbatim` | `internal/core/proxy_test.go` (T9), `internal/protocols/anthropic/adapter_test.go` (T14) |
| 27 | `TestProxy_UpstreamUnreachable502` (envelope). Core half: `TestProxy_GatewayErrorStatus` (dial refused, TLS failure, each timeout kind) | `internal/protocols/anthropic/adapter_test.go`, `internal/core/proxy_test.go` |
| 28 | `TestProxy_UpstreamTimeout504` | `internal/protocols/anthropic/adapter_test.go` |
| 29 | `TestProxy_NoRetry`, counted at the upstream | `internal/core/proxy_test.go` |
| 30 | `TestProxy_ClientDisconnectCancelsUpstream`: cancel while waiting for headers, and again mid-stream; the log line is awaited, not assumed | same |
| 31 | `TestProxy_UpstreamDiesMidStreamAbortsClient` | same |
| 32 | `TestProfile_ClaudeCodeDetected` (both signals, plus a non-match) | `internal/clients/claudecode/profile_test.go` |
| 33 | `TestProfile_UnknownClientProxied` | `internal/core/registry_test.go` |
| 34 | `TestCore_NoProviderOrClientIdentifiers`. It scans every file in `internal/core/` except its own, which has to hold the words | `internal/core/purity_test.go` |
| 35 | `TestCore_TestAdapterAndProfileNeedNoCoreChange` | `internal/core/registry_test.go` |
| 36 | `TestAccessLog_ProxyFields` | `internal/core/proxy_test.go` |
| 37 | `TestAccessLog_AuthKind` (`api_key`, `bearer`, `both`, `none`; `both` from Q15) | `internal/protocols/anthropic/adapter_test.go` |
| 38 | `TestAccessLog_GatewayErrorField` | `internal/core/proxy_test.go` |
| 39 | `TestProxy_SecretsNotLogged`: through `run`, real adapter, a sentinel in `x-api-key`, `Authorization`, the query and the body; success, 502, and a panicking test route | `cmd/gateway/run_proxy_test.go` |
| 40 | `TestRun_ShutdownWaitsForStream`: a real proxied stream, shutdown started mid-stream, exit `0` | `cmd/gateway/run_shutdown_test.go` |
| 41 | `TestRecoverer_LogsPanicTypeNotValue` | `internal/server/server_test.go` |
| 42 | `TestGo_RecoversAndLogsJSON` | `internal/logging/go_test.go` |
| 43 | `TestCompose_StopGracePeriodMatchesShutdownTimeout` | `test/conventions/compose_test.go` |
| 44 | Manual: API-key session and `claude -p` through the gateway; log lines checked as the AC says. Evidence in the PR. Needs an API key (research Q4) | PR |
| 45 | Manual: the same on a claude.ai subscription | PR |
| 46 | Manual: read `docs/clients/claude-code.md` against the AC's four items | PR |
| 47 | `TestProxy_MalformedClientBody400`: broken chunked framing on a live connection gives 400 `client_body`, with the test adapter's body; a short body (half-close) and a vanished client each get no body and `client_disconnected` (Q19). The Anthropic envelope: `TestProxy_ClientBodyEnvelope` | `internal/core/proxy_test.go`, `internal/protocols/anthropic/adapter_test.go` |
| 48 | `TestAccessLog_UpstreamAbortedField`: upstream dies after headers gives `upstream_aborted: true` and no `client_disconnected`; a client that leaves mid-stream gives the reverse; a completed response and a 502 have neither | `internal/core/proxy_test.go` |

**Tests no single AC names**
- `TestRun_ProxiesAnthropicEndToEnd`: through `run` with the real adapter and profile
  against a fake upstream on loopback. Checks `protocol: anthropic`, `client:
  claude-code`, `auth: api_key`. Core's own tests use the test adapter, so this is the
  one test that proves the wiring.
- `TestNoBareGoStatements` (`test/conventions`): the AST check described above.
- `TestLoad_ErrorsNeverContainValue` (000's): gains a row for `invalid url`, so the
  reason table stays complete.
- The 000 shutdown-default assertions (`30s`) move to `10m`; nothing else in 000's
  tests is loosened. The two `panic`-value assertions become `panic_type` plus "the
  value appears nowhere", which is stronger.

**What `-race` is asked to catch:** the `Meta` handoff between the hooks and
`accessLog`; the recorder buffers the tests share; `Go`'s channel. It cannot catch a
goroutine that is merely stuck, which is what the stack-scan leak check is for.

## Risks and unknowns

- **Buffering.** Any layer between `ReverseProxy` and the socket that does not forward
  `Flush` turns a stream into one lump at the end, with all functional tests still
  green. Caught by AC23 (the client must see event 1 while upstream is blocked before
  event 2) and AC24 (the recorded stream, event by event). Both run through
  `server.New`, so 000's middleware is in the path. `FlushInterval: -1` must stay set;
  a test on the built `ReverseProxy` asserts it.
- **Header leakage, upward.** Something the gateway adds or keeps that Anthropic
  should not see: an `X-Forwarded-*`, the gateway's request ID, a `Host` of
  `127.0.0.1:7197`, an injected `Accept-Encoding: gzip` (Go adds one unless
  `DisableCompression` is set). Caught by AC15, AC16, AC22. The `X-Forwarded-*`
  restore in step 4 is the reverse risk: a client-sent one must still arrive (AC8).
- **Header leakage, downward.** Upstream's `X-Request-Id` reaching the client next to
  the gateway's (found in Q6); a hop-by-hop header surviving. Caught by AC14, AC18,
  AC19.
- **Secrets in logs.** The paths that could carry one, and what closes each:
  `ErrorHandler` never logs or formats `err` (it can hold the upstream address);
  `ReverseProxy.ErrorLog` writes one line on a mid-stream failure, with error text
  from `net`, so `TestProxy_SecretsNotLogged` includes a mid-stream abort; the panic
  line has no value (AC41); the stack holds no string bytes; the access line has
  `r.URL.Path` only and no query (000). AC39 runs a sentinel through all of them.
- **Goroutine leaks.** A cancelled or aborted request that leaves a copy loop or a
  transport reader behind. Caught by the leak check at the end of the AC28–AC31 tests
  and by `-race` on the shared buffers. Client-disconnect handling is the likeliest
  place: `ReverseProxy` cancels the outbound context, but only when the inbound one
  is cancelled, so the test proves the upstream saw the cancel (AC30).
- **`ReverseProxy` behaviour changes with Go releases.** The `Rewrite` fix-ups in
  steps 3–5 answer what go1.25.4 does today (Q5). A release that stops stripping, or
  starts stripping more, fails AC8, AC14 or AC16 rather than silently altering
  traffic. `go.mod` pins the toolchain.
- **"Never retries" and the stdlib.** `http.Transport` replays a request when a
  reused idle connection turns out to be dead before any byte was written; upstream
  never saw the first attempt. AC29 counts attempts at the upstream, which is where
  the spec's "one client request is at most one upstream request" is true. It is not
  disabled: turning off keep-alive would cost a TLS handshake per request.
- **Paths are forwarded as sent.** No `..` cleaning, so a client can walk out of the
  `base_url` path prefix on the same host. Not in the spec, no privilege gained, and
  cleaning would change what the client sent; noted, not changed.
- **A 10-minute shutdown.** `docker compose stop` and `make run` can now hang up to
  10 minutes on an open stream. The docs say so (AC46); a second signal still kills
  the process at once (000).
- **Recording the fixture** (Q10, answered): through the recording proxy with a Claude
  Code subscription login, so it needs no API key. AC44 still does (Q4). Nothing else
  depends on the fixture except AC24 and AC25.
- **A client-side write failure with a live context.** If the client's socket dies on
  a write before the server cancels its context, and upstream is healthy, `Settle` sets
  neither abort flag. In the throwaway run the context was already cancelled when the
  abort reached the handler, so this is expected to be rare; AC30's mid-stream case
  awaits the log line rather than assuming it, and would show the gap.
- **The two watchers.** They sit on the byte path. They are read-through and change
  nothing, but an accidental `io.ReadAll` or buffer in one would break "streams are
  sacred". AC23 and AC24 run with them in place, and AC20 (64 MiB) covers the
  request one.
- **Answers applied.** Q9 (`ProxyFromEnvironment`), Q10, Q13 (499) confirm the
  plan's defaults. Q11 and Q12 changed the spec (AC47, AC48) and are built as written
  there.
- **ADR status.** ADR 0003 is written `proposed` (the template's word). It flips when
  this plan is approved; the request said "Decided", and the repo's ADRs use
  `approved`, so it will read `approved`.

## Deviations from the spec

None.
