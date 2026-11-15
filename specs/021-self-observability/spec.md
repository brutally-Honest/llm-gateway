---
feature: 021-self-observability
kind: feat
status: draft
version: 1
created: 2026-11-15
supersedes:
branch: feat/021-self-observability
---
# Self-observability

## Goal
Phase 8 (PLAN §7): the gateway watches itself. It exposes Prometheus metrics about its
own health — requests, upstream latency and errors, capture, tunnels and UI streams, the
UI's build times, startup and shutdown — and can export OpenTelemetry spans for a
proxied model call to Jaeger. A separate compose stack runs Prometheus, Grafana (with a
provisioned dashboard) and Jaeger on loopback, so the gateway's own p50/p99 overhead is
visible. Metrics describe the gateway, never the traffic's content: no prompt, response,
header, session id, exchange id, raw path or host is ever a label.

## Acceptance criteria
### Metrics
- **AC1** — `GET /metrics` answers in the Prometheus text format on the API listener
  (`listen_addr`) and on the UI listener (`ui.listen_addr`) whenever the gateway runs,
  with no config key; it is never answered on the proxy listeners (`effects.network.*`).
- **AC2** — Requests: `gateway_http_requests_total` and the histogram
  `gateway_http_request_duration_seconds`, labelled `listener` (`api`, `ui`),
  `status_class` (`1xx`…`5xx`, `none` when nothing was written) and `proxied`
  (`true` for a call forwarded to a provider).
- **AC3** — Upstream: histograms `gateway_upstream_ttfb_seconds` and
  `gateway_upstream_duration_seconds` labelled `protocol` and `stream`;
  `gateway_upstream_errors_total` labelled `protocol` and `kind` (`timeout`,
  `unreachable`, `aborted`, `rate_limited`, `status_5xx`); and the histogram
  `gateway_proxy_overhead_seconds` (`protocol`): the time a call spends in the gateway
  before it is sent upstream.
- **AC4** — Capture: gauges `gateway_capture_queue_depth` and
  `gateway_capture_queue_capacity`; `gateway_capture_dropped_total` labelled `reason`
  (`queue_full`, `memory`); the histogram `gateway_store_write_seconds` labelled `op`
  (`exchange`, `parse`) and `outcome` (`ok`, `error`).
- **AC5** — Live connections: gauges `gateway_proxy_tunnels_open` and
  `gateway_ui_streams_open`.
- **AC6** — UI: the histogram `gateway_ui_view_build_seconds` labelled `cache` (`cold`,
  `cached`); the gauge `gateway_ui_warmup_seconds`, the start's warm-up time.
- **AC7** — Shutdown: each stop writes its duration and the model calls and tunnels it
  cut to the capture directory; the next start exposes them as
  `gateway_last_shutdown_duration_seconds`, `gateway_last_shutdown_calls_cut`,
  `gateway_last_shutdown_tunnels_cut` and `gateway_last_shutdown_timestamp_seconds`
  (absent before the first recorded stop, or with capture off).
- **AC8** — `gateway_build_info{version,go_version}` is 1; Go runtime and process metrics
  (`go_*`, `process_*`) are exposed too.
- **AC9** — Cardinality: every label's values come from a fixed set or from the
  registered protocol names; no label is a session id, exchange id, request id, path,
  host or model.

### Tracing
- **AC10** — Off by default. `tracing.enabled: true` with `tracing.otlp_endpoint` (an
  OTLP/HTTP base URL, default `http://127.0.0.1:4318`) exports spans in batches; an
  unreachable endpoint never fails or slows a request.
- **AC11** — One trace per proxied call, spans `gateway.receive` (root) →
  `gateway.upstream_request`, `gateway.response_stream`, `gateway.capture` →
  `gateway.store_write` (one per store call). Attributes: the exchange id
  (`gateway.exchange_id`), protocol, client, method, status, stream, request and response
  sizes, TTFB, gateway error, the capture outcome, the parse status and the model (on
  `gateway.capture`, once parsed). Never prompt or response content, never a header.
- **AC12** — With tracing off no span is created and the hot path pays no more than a
  nil check.

### Stack and docs
- **AC13** — `deploy/observability/` holds a compose file that starts Prometheus,
  Grafana and Jaeger, every published port on 127.0.0.1; Prometheus scrapes the gateway
  running on the host, on Linux and on macOS. Grafana loads a provisioned Prometheus
  datasource and one dashboard from the repo, with panels for every metric group above.
- **AC14** — Docs say how to start and stop the stack, which keys turn tracing on, and
  where the dashboard, Prometheus and Jaeger are. The config example lists the tracing
  keys. The roadmap's "Not now: OpenTelemetry" says it means OTel ingestion from
  harnesses; PLAN §7's Phase 8 row matches what is built.
- **AC15** — Go tests (-race): metric names and labels present after a request; a test
  that fails if any label value looks like an id, path or host; `/metrics` absent on the
  proxy listener; no span with tracing off; span attributes never carry content or
  headers.
- **AC16** — A benchmark of the proxy hot path with metrics on and tracing off vs on;
  the numbers are in research.md, and tracing off costs about nothing.
- **AC17** — On the real gateway, with the stack up and a few Claude Code calls and one
  restart: every dashboard panel shows live data, Jaeger shows a model call's trace with
  all its spans, and Prometheus shows the gateway target up.

## Edge cases
- **E1** — Capture off: no capture, store or UI metrics move; queue gauges are absent;
  the last-shutdown gauges are absent (no directory to keep them in). Request and
  upstream metrics still count.
- **E2** — UI off: `/metrics` is on the API listener only.
- **E3** — A dropped capture (queue full, memory): the `gateway.capture` span ends with
  the drop as its outcome; no `gateway.store_write` span.
- **E4** — A call cut at shutdown is not an upstream error (the gateway cut it, not
  upstream); it counts in the last-shutdown gauges.
- **E5** — A client that leaves early (499) is a `4xx` request and not an upstream error.
- **E6** — The last-shutdown file is unreadable or malformed: the gauges are absent and a
  warning is logged; the start goes on.
- **E7** — A scrape during shutdown is answered or refused like any request; metrics
  never hold a stop open.
- **E8** — A bad `tracing.otlp_endpoint` (not an http/https URL) is a config error at
  start, like any invalid key.

## Out of scope
- OTel ingestion from harnesses (roadmap "Not now").
- Metrics labelled by session, exchange, model or path; per-tenant metrics.
- Traces of anything but a proxied call (UI requests, hooks, tunnels).
- Alerting rules, Grafana auth beyond local anonymous viewing, remote storage.
- Running the gateway itself in the observability compose stack.
