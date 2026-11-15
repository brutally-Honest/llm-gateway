---
feature: 021-self-observability
---
# Research & decisions — 021-self-observability

## Open questions
<!-- Answer by changing "(open)" to "(answered)" and writing the answer underneath. -->

## Decisions
- D1 (2026-11-15) — Karthik: built by hand, no agent-loop kit (no /al-* commands), so
  spec.md stays a draft with no approval stamps and there is no tasks.md. Decided by
  Karthik before the build: /metrics always on, served on the UI and API listeners only;
  a separate compose file for Prometheus, Grafana and Jaeger, the gateway itself staying
  under systemd; every port on 127.0.0.1; low-cardinality metrics in the seven groups of
  the spec; OTel spans for one path (a proxied model call), off by default, OTLP endpoint
  in config; one provisioned Grafana dashboard and datasource.
- D2 (2026-11-15) — "/metrics on the UI/API listener only": read as both the API listener
  (`listen_addr`, always bound, so "always exposes /metrics" holds with the UI off) and
  the UI listener (where it sits behind the UI's Host and Origin checks). Prometheus
  scrapes the API listener: the UI's Host check would refuse `host.docker.internal`.
  Never on the proxy listeners.
- D3 (2026-11-15) — Requests are counted on the API and UI listeners. The proxy listeners
  serve tunnels and forwards, not requests the gateway answers; they are covered by
  `gateway_proxy_tunnels_open`. A third label, `proxied`, keeps model calls apart from
  the gateway's own routes (healthz, /metrics, hooks), so a scrape every 15 s does not
  drown the p50/p99 of model calls.
- D4 (2026-11-15) — No `model` label on metrics, though allowed: the model is known only
  after the capture worker parses the body (off the request path), and its value comes
  from the client, so it is unbounded. Upstream metrics are labelled by `protocol` (the
  registered adapter names) and `stream`. The model is on the trace's capture span.
- D5 (2026-11-15) — "Overhead" (PLAN §7: the gateway's own p50/p99 overhead):
  `gateway_proxy_overhead_seconds`, from the proxy taking the call to handing it to the
  transport (session key, capture tee set-up, rewrite). The response path adds only the
  tee's copy per chunk, which the benchmark measures.
- D6 (2026-11-15) — Shutdown metrics: a stop's numbers cannot be scraped from the process
  that is exiting, so each stop writes `last-shutdown.json` (duration, calls cut, tunnels
  cut, time) to the capture directory and the next start exposes it as
  `gateway_last_shutdown_*` gauges. The process stays stateless; the record lives beside
  the store. Calls cut is the in-flight count when the grace period ran out (0 on a
  clean stop).
- D7 (2026-11-15) — Upstream error kinds: timeout and unreachable (before headers),
  aborted (cut mid-body), rate_limited (upstream 429), status_5xx. A client that left, a
  bad client body, a shutdown cut and the gateway's own rate-limit 429 are not upstream
  errors.
- D8 (2026-11-15) — Library versions: client_golang v1.25 and OTel v1.47 need go 1.26,
  and `go get @latest` raised the module's go line to 1.26.0. Kept go 1.25.4 (the
  installed toolchain) with client_golang v1.24.0 and OTel v1.46.0 (ADR 0018).
  `go mod tidy` picks the newest OTel for a new import and raises the go line again, so
  it is run with `GOTOOLCHAIN=local` and the versions pinned with `go get …@v1.46.0`.
- D9 (2026-11-15) — Spans: `gateway.receive` (root, from the proxy taking the call to
  Settle) → `gateway.upstream_request` (handed to the transport → response headers, or
  the error) and `gateway.response_stream` (headers → body done) and `gateway.capture`
  (handed to the sink → stored, dropped or undrained) → `gateway.store_write` per store
  call. No trace context is read from or written to any header (no propagator): a
  client's `traceparent` is ignored and upstream never gets one, so headers stay as sent.
  The model comes from the canonical request event, on `gateway.capture` (D4), cut at
  128 characters. `client` is the profile's name (`claude-code`, `unknown`), not the
  User-Agent. Request and response sizes are counted in the body watchers that already
  exist (one add per read). The capture span ends with `stored`, `store_failed`,
  `dropped_queue_full`, `dropped_memory` or `undrained` (E3).
- D10 (2026-11-15) — Correction to ADR 0018's first draft: the OTLP/HTTP exporter links
  gRPC too. Its proto package (`go.opentelemetry.io/proto/otlp/collector/trace/v1`)
  imports `google.golang.org/grpc` and grpc-gateway, so 66 grpc packages end up in the
  binary either way. Kept the official exporter: the alternative, a hand-written OTLP/JSON
  exporter (~150 lines, no proto), would save most of the size but leave the gateway
  owning the OTLP mapping and retries; on a local single-user gateway disk size matters
  less than that. ADR 0018 is corrected.
- D11 (2026-11-15) — OTel's global error handler and logger default to the `log` package
  on stderr, which the lint (spec 000, Logging) forbids; `StartTracing` routes errors to a
  zap warning ("trace export failed") and discards OTel's internal logger. With tracing
  off neither global is touched.
- D12 (2026-11-15) — Decision 1 said Prometheus reaches the gateway through host-gateway
  on Linux. Tested on this machine (Docker Engine 29.4.3, Ubuntu 26.04): a container with
  `host.docker.internal:host-gateway` gets "connection refused" from 172.17.0.1 for a
  server bound to the host's 127.0.0.1, which is where the gateway listens (Notes,
  host-gateway test). Making it work through host-gateway would mean binding the gateway
  to the bridge address, which is reachable from every container and breaks "no route
  reachable from outside localhost without a gateway token". So: `compose.yml` keeps
  `extra_hosts: host.docker.internal:host-gateway` and that target (macOS, Docker
  Desktop), and on Linux `make observability-up` adds `compose.linux.yml`, which puts
  Prometheus and Grafana on the host network, each bound to 127.0.0.1. Jaeger stays on
  the bridge with ports published on 127.0.0.1 (the gateway reaches it from the host).
  The goal of decision 1 holds (Prometheus reaches the gateway on both, everything on
  loopback); its mechanism on Linux differs.
- D13 (2026-11-15) — Grafana on 127.0.0.1:3030, not 3000: on this machine 3000 was taken
  by another local dev server (a `node server.js`), and Grafana failed to bind. 3000 is a
  common dev port, so the default moved; `GRAFANA_PORT` picks another.
- D14 (2026-11-15) — Every series of the fixed label sets is created at 0 at start: a
  labelled metric with no observation is missing from the scrape, which left the error
  and drop panels at "no data" and let the dashboard test miss a renamed metric. The run
  tests now require recorded values, not presence.
- D15 (2026-11-15) — The dashboard JSON is generated once and then kept by hand in the
  repo (one row per metric group, 17 panels); error and drop panels count over the last
  hour, quantiles over 5 minutes, refresh 10 s. `TestDashboard_QueriesOnlyExposedMetrics`
  ties it to the gateway's real scrape.

### Dependencies
- `github.com/prometheus/client_golang` v1.24.0 — the reference Prometheus client
  (registry, histograms, Go/process collectors, text exposition); chosen over hand-rolled
  exposition or VictoriaMetrics/metrics for correctness and familiarity (ADR 0018).
  Size: the gateway binary grew from 21,530,874 to 24,942,155 bytes (+3.4 MB, mostly
  protobuf and procfs), `CGO_ENABLED=0 -trimpath`.
- `go.opentelemetry.io/otel`, `/otel/trace`, `/otel/sdk`,
  `/otel/exporters/otlp/otlptrace/otlptracehttp` v1.46.0 — the OpenTelemetry API, SDK
  (batch span processor, sampler) and OTLP/HTTP exporter; chosen over Jaeger's deprecated
  clients (lock-in) and a hand-written exporter (D10). Size: 24,942,155 → 33,439,040
  bytes (+8.5 MB: the SDK, the OTLP protos and the gRPC packages they import).
- `github.com/go-logr/logr` v1.4.4 — already a transitive dependency of OTel; imported
  directly only for `logr.Discard()` (D11). No size change.
- Test only: `github.com/prometheus/common/expfmt` and `client_model` (parse a scrape),
  `go.opentelemetry.io/proto/otlp` and `google.golang.org/protobuf` (the fake OTLP
  collector) — all already in the module graph through the libraries above.

### Evidence: host-gateway and a loopback-bound server (2026-11-15)
`python3 -m http.server 17411 --bind 127.0.0.1` on the host, then
`docker run --rm --add-host host.docker.internal:host-gateway busybox:1.36 wget
http://host.docker.internal:17411/` → `can't connect to remote host (172.17.0.1):
Connection refused`. docker0 is 172.17.0.1/16. (D12)

### Real-gateway check (2026-11-15, docs/manual-checks/021-self-observability.md)
Build 6608bd1 installed first (binary kept as `~/.local/bin/gateway.bak-021`; config
kept as `config.yaml.bak-021`, `tracing.enabled: true` appended with Karthik's
go-ahead), then 06e6211 after the check showed `ui p50/p99` lines in the model-call
latency panel (pre-created `listener=ui, proxied=true` series the UI can never fill;
fixed in 06e6211). Stack via `make observability-up` (Linux override). Claude Code calls
were this build session's own, which run through the gateway and its proxy listeners.

| # | Step | Expected | Seen | Match |
|---|---|---|---|---|
| 1 | `/metrics` on 7197 and 7199 | build info, this version | `gateway_build_info{go_version="go1.25.4",version="6608bd1"} 1` on both | yes |
| 2 | `/metrics` via and on the proxy listener 7198 | no metrics | 400 both ways, no `gateway_` line | yes |
| 3 | Prometheus targets | `llm-gateway` 127.0.0.1:7197 UP | UP, last scrape 2 ms | yes |
| 4 | Claude Code calls with the UI open on a session | answer as before | calls 200; `ui_streams_open 1`, `proxy_tunnels_open 2`, cold view builds 4 | yes |
| 5 | Restart | seconds, no SIGKILL, record written | 0.32 s (`gateway stopped`), no SIGKILL; `last-shutdown.json`: 0.323 s, calls_cut 0, tunnels_cut 2. The 06e6211 install restart: 0.004 s, tunnels_cut 2 | yes |
| 6 | More calls; session page cold then cached | both builds recorded | `cache="cold"` 5, `cache="cached"` 1 on 06e6211 | yes |
| 7 | Grafana dashboard, last 15 min | data on every panel | all 17 panels show data: requests, latency p50 ~5 s / p99 ~27 s, overhead p50 ~50 µs / p99 ~230 µs, TTFB, upstream total, errors 0, queue 0 of 256, drops 0, store write (parse p50 ~180 ms), tunnels, UI streams, view build cold/cached, warm-up 23.43 s, last shutdown 3.86 ms, cut 0 calls / 2 tunnels, build 06e6211, uptime | yes |
| 8 | Jaeger trace of a model call | six spans, ids/sizes/status/model, no content or headers | trace 2c5d97c…: receive (6.3 s, POST 200) → upstream_request (1.1 s, 200), response_stream (5.2 s), capture (model claude-opus-5-5) → 2 store_write (17.6 ms, 223 ms); root attributes: exchange id, client, protocol, stream, ttfb 1117.6 ms, request 1,539,815 B, response 5,666 B. Across 26 traces the only keys are the spec's plus Jaeger's own `@jaeger@warnings` (clock-skew notes: the capture span ends after its parent, as it runs after the response) | yes |
| 9 | `gateway dump` of the trace's exchange id | the same call | `IEYWQ6SGA2FG4CWOA5SJLEDLTG`, POST /v1/messages, claude-code | yes |

Seen beside the steps: a trace queried while its call still streamed had only
`gateway.upstream_request` (ended spans export first); fetched again it had all six.

### Flake seen (2026-11-15)
`TestCapture_ForwardingUnchanged/TestProxy_GoldenAnthropicStreamReplay`
(`internal/protocols/anthropic`) failed once in a full `make verify` while building
tracing, the flake the roadmap's Later already watches (015 research). It then passed 8
times alone, 25 times under parallel load from three other packages, and in the next full
`make verify`. Left as the roadmap has it.

### Benchmark: the proxy hot path (2026-11-15)
`go test -run '^$' -bench ProxyHotPath -benchtime 3s -count 6 ./internal/observability`
(AMD Ryzen 5 5600G, 12 threads, go1.25.4). One streamed model call per op, run in
parallel over keep-alive loopback connections: client → gateway (capture on into a
store that does nothing) → local SSE upstream (6 events) → client reads it whole. So an
op includes the client, the upstream and loopback, not just the gateway. Medians of 6:

| Case | ns/op | B/op | allocs/op | vs metrics off |
|---|---|---|---|---|
| metrics off, tracing off | 62,319 | 87,263 | 580 | — |
| metrics on, tracing off | 62,649 | 87,038 | 584 | +0.5 % (in the noise: runs 60,991–64,306) |
| metrics on, tracing on | 65,836 | 96,104 | 641 | +5.6 %, +3.5 µs, +61 allocs, +8.8 KB |

Raw runs (ns/op): off/off 62158 62480 63104 66761 61498 61417; on/off 62613 60991 64306
62684 62764 61698; on/on 68188 65836 65503 65835 65767 71417. Tracing off costs a nil
check per hook (start, sent, answered, failed, capture, finish) and nothing measurable;
tracing on costs about 3.5 µs per call for six spans, next to a real model call's
seconds. Tracing on exports to a processor that drops the spans, so the network cost of
export (async, batched) is not in the numbers.

## Notes
### Evidence: what the code measures today (main, f216cb3)
| Signal (spec group) | Measured or logged today | Where |
|---|---|---|
| Requests by listener, status, latency | Logged per request, API listener only: status, bytes, `duration_ms` (access line) | `internal/server/middleware.go` accessLog |
| UI listener requests | Not logged, not measured | `ui.Handler`, mounted bare in `cmd/gateway/run.go` |
| Upstream time to first byte | Logged: `ttfb_ms` on the access line, `Meta.TTFB` from proxy start to response headers | `internal/core/proxy.go` modifyResponse |
| Upstream total time | Only as the access line's `duration_ms` (whole request) | accessLog |
| Upstream errors by kind | Logged: `gateway_error` (upstream_timeout, upstream_unreachable, client_body, gateway_shutdown), `upstream_aborted`, `client_disconnected`; never counted | `core.classify`, `Meta.Settle` |
| Capture queue depth | Not measured; the queue is `Sink.queue` (a buffered chan) | `internal/capture/sink.go` |
| Capture drops | Counted, logged only at shutdown (`capture stopped`: dropped_queue_full, dropped_memory, store_failed, parse_failed, memory_peak_bytes) | `Sink.Counts`, `Budget.Dropped` |
| Store write time | Not measured | `Sink.process`: SaveExchange, SaveParse |
| Open proxy tunnels | Tracked (map, for shutdown), never exposed | `netproxy.Proxy.tunnels` |
| Open UI streams | Tracked (`Hub.subs`, capped at 32), never exposed | `internal/ui/hub.go` subscribeView |
| UI view build time, cold vs cached | Not measured; cached is a `ViewCache` hit | `ui.Deps.cachedView` (`api_view.go`) |
| Startup warm-up time | Logged once: `ui ready` `warm_ms` | `cmd/gateway/run.go` |
| Shutdown duration | Not measured; `shutdown timed out` logs `in_flight` (calls cut) | `run.go` shutdown block |
| Calls cut at shutdown | Logged as `in_flight` on timeout; each stored with `gateway_error gateway_shutdown` | `run.go`, `core.Meta.Settle` |
| Build info | Logged: `gateway started` `version` | `run.go`, `-ldflags -X main.version` |

Nothing is exported today: no metrics endpoint, no tracing, no metrics library in go.mod.

### Evidence: the hot paths
- **Proxied model call** (API listener): `server` middleware (countInFlight → requestID →
  accessLog → recoverer) → `core.Proxy.ServeHTTP` (labels Meta, starts the capture tee,
  reads the session key) → `httputil.ReverseProxy` (Rewrite: rewrite, teeRequest,
  watchRequestBody) → upstream → modifyResponse (TTFB) → streamed copy through the tee
  (FlushInterval -1) → accessLog's defer: `Meta.Settle` → `finishCapture` →
  `Sink.Submit` (non-blocking). Every byte of a stream passes the tee writer, so nothing
  per-chunk may be added there.
- **Capture worker** (off the request path): `Sink.process` → SaveExchange → parse →
  Canonicalize → SaveParse → OnCommit (UI hub publish).
- **UI**: `cachedView` on every session page and live view update; `Warm` once at start.
- **Proxy listeners**: per CONNECT tunnel, two copy goroutines; nothing per-byte beyond
  counters.

### Evidence: what PLAN.md says about Phase 8
- §7 row 8: "Self-observability | Prometheus, OTel → Jaeger, Grafana | The gateway's own
  p50/p99 overhead is visible". §7 v1 order: Phase 8 is last, after 9.
- §8 Metrics: Prometheus + Grafana, decided, Phase 8 (pull-based; histograms give p50/p99
  overhead). §8 Tracing: OpenTelemetry → Jaeger, decided, Phase 8 (vendor-neutral; Jaeger
  is a local UI in one container). §8 Packaging: docker-compose for the stack, the gateway
  also a plain binary.
- §11 has the scope `observability` for this work and `deploy` for compose files.
- The roadmap's "Not now: OpenTelemetry" means OTel ingestion from harnesses (019 D9),
  not this phase.
