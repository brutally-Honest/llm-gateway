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
