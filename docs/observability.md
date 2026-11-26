# Self-observability

The gateway watches itself (spec 021, PLAN §7 Phase 8): Prometheus metrics about its own
health, and OpenTelemetry traces of proxied model calls. A separate compose stack runs
Prometheus, Grafana and Jaeger on 127.0.0.1. The gateway itself keeps running where it
runs (systemd, launchd, `make run`); it is never part of the stack.

Metrics and traces describe the gateway, never the traffic: no metric label is a session,
exchange or request id, a path, a host or a model, and no span carries prompt or
response content or a header.

## Metrics: always on
`GET /metrics` answers on the API listener (`listen_addr`, default `127.0.0.1:7197`) and
on the UI listener (`ui.listen_addr`, default `127.0.0.1:7199`), never on the proxy
listeners. There is no key to turn it on or off.

| Group | Metrics |
|---|---|
| Requests | `gateway_http_requests_total`, `gateway_http_request_duration_seconds` (`listener` api/ui, `status_class`, `proxied`) |
| Upstream | `gateway_upstream_ttfb_seconds`, `gateway_upstream_duration_seconds` (`protocol`, `stream`), `gateway_upstream_errors_total` (`protocol`, `kind`: timeout, unreachable, aborted, rate_limited, status_5xx), `gateway_proxy_overhead_seconds` (time in the gateway before a call goes upstream) |
| Capture | `gateway_capture_queue_depth`, `gateway_capture_queue_capacity`, `gateway_capture_dropped_total` (`reason`: queue_full, memory, and effects when the effects proxy is on), `gateway_hook_events_dropped_total` (`reason`: queue_full, memory, oversize, unreadable), `gateway_store_write_failures_total`, `gateway_blob_write_failures_total`, `gateway_store_write_seconds` (`op`: exchange, parse; `outcome`) |
| Live connections | `gateway_proxy_tunnels_open`, `gateway_ui_streams_open` |
| UI | `gateway_ui_view_build_seconds` (`cache`: cold, cached), `gateway_ui_warmup_seconds` (time from start until the UI serves), `gateway_ui_cache_rebuild_seconds` (duration of the last background pass), `gateway_ui_view_cache_reads_total` (`kind`: memo, view; `result`: hit, miss, damaged) |
| Lifecycle | `gateway_last_shutdown_duration_seconds`, `gateway_last_shutdown_calls_cut`, `gateway_last_shutdown_tunnels_cut`, `gateway_last_shutdown_timestamp_seconds`, `gateway_build_info{version,go_version}`, `go_*`, `process_*` |

A stop cannot be scraped from the process that is exiting, so each stop writes
`last-shutdown.json` in the capture directory and the next start exposes it as the
`gateway_last_shutdown_*` gauges (absent before the first stop, or with capture off).

## Capture health
The gateway is **degraded** when it has lost capture data since it started: a hook event,
a capture or a store or blob write was dropped or failed. It still proxies every request
normally.

The counts are since process start. They never go down. A restart clears them, so restart
only after the cause is fixed. Each place shows the same state:

| Where | What it shows |
|---|---|
| `GET /healthz` (API listener, `127.0.0.1:7197`) | the JSON below; always 200 while the gateway serves |
| `GET /api/health` (UI listener, `127.0.0.1:7199`) | the same JSON, plus `"ok": true`; always 200 |
| `GET /metrics` | the four counters below, by reason |
| `gateway doctor` | a "Capture health" line: pass when nothing is lost, warn with the counts |
| UI header | a red banner, "capture degraded: N lost since start", with a link to `/api/health`; it cannot be dismissed |

`/healthz` stays 200 when degraded, so the `claude` shell function does not bypass a
gateway that is only degraded. Check the status field, not the HTTP code.

```json
{"ok":true,"status":"degraded","since":"2026-11-26T09:00:00Z","lost":3,
 "counts":{"hook_events_dropped":0,"captures_dropped":3,"store_write_failures":0,"blob_write_failures":0},
 "reason":"captures dropped"}
```

`status` is `ok` or `degraded`. `since` is the process start. `lost` is the sum of the
counts. `reason` appears only when degraded and names each non-zero kind.

| Counter | What it means | What to do |
|---|---|---|
| `hook_events_dropped` | a burst of hook events overflowed `hooks.queue_size` or `hooks.memory_limit`, or a body was over `capture.max_body_bytes` or unreadable | raise `hooks.queue_size` or `hooks.memory_limit` if bursts are normal; check the log for `hook event dropped` |
| `captures_dropped` | the capture queue was full (`capture.queue_size`, `capture.workers`), the capture memory budget was exceeded, or the effects network recorder queue was full | raise the queue or the workers; check disk speed; check the log for `capture_failed` or the shutdown line |
| `store_write_failures` | the SQLite store refused a write: disk full, permissions, or the store file was deleted or replaced | check free space and permissions of `capture.dir`; check the log for `capture_failed` (stage `store`), `hook event not stored` or `store file deleted or replaced` |
| `blob_write_failures` | a body over 4 KiB could not be written to `<capture.dir>/blobs`: disk full, permissions or a read-only filesystem. The call is kept without that body | fix the space or permissions; check the log for `blob not stored` (with the hash) |

Fix the cause first, then restart the gateway to clear the counts. A later write of the same
content repairs a blob that was not written.

A body whose blob is missing or unreadable shows as `[content unavailable]` in the session.
The body endpoint answers 404 `body_not_stored` and logs `body unavailable` with the hash.
The rest of the session is unaffected. A call whose body blob could not be written is still
listed with its metadata.

## Traces: off by default
Turn tracing on in the config file or the environment:

```yaml
tracing:
  enabled: true                          # GATEWAY_TRACING_ENABLED=true
  otlp_endpoint: http://127.0.0.1:4318   # GATEWAY_TRACING_OTLP_ENDPOINT; the default
```

`otlp_endpoint` is an OTLP/HTTP base URL (spans go to `<it>/v1/traces`): https anywhere,
http on loopback only. The default is the stack's Jaeger. Restart the gateway after the
change. Spans are exported in batches; a collector that is down never fails or slows a
request. It costs a `trace export failed` warning at the first failure, then at most one
a minute (with `failed_batches`), and one `trace export recovered` line when it is back;
the spans of the failed batches are dropped.

One trace per proxied call: `gateway.receive` → `gateway.upstream_request`,
`gateway.response_stream`, `gateway.capture` → `gateway.store_write` (one per store call).
Attributes: `gateway.exchange_id` (the id `gateway dump` and the UI use), protocol,
client, method, status, stream, request and response sizes, TTFB, the gateway error, the
capture outcome, the parse status and the model (`gen_ai.request.model`, on
`gateway.capture`).

## The stack
Needs Docker with compose. From the repo root:

```sh
make observability-up     # start Prometheus, Grafana and Jaeger
make observability-down   # stop them; their data stays in named volumes
```

To drop the stored metrics and Grafana state as well:
`docker compose -f deploy/observability/compose.yml down -v`.

| What | Where |
|---|---|
| Grafana, with the dashboard "LLM Gateway — self-observability" as its home page | <http://127.0.0.1:3030> (`GRAFANA_PORT=… make observability-up` for another port) |
| Prometheus; the gateway target is under Status → Targets | <http://127.0.0.1:9090/targets> |
| Jaeger; pick the service `llm-gateway` | <http://127.0.0.1:16686> |
| Jaeger's OTLP/HTTP intake (the gateway's `tracing.otlp_endpoint`) | `http://127.0.0.1:4318` |

Grafana lets anyone on this machine view; editing needs its admin login (admin / admin
on first start). The dashboard is read from
`deploy/observability/grafana/dashboards/llm-gateway.json` and the Prometheus datasource
from `deploy/observability/grafana/provisioning/`; edit the JSON in the repo, not in
Grafana (`TestDashboard_QueriesOnlyExposedMetrics` checks it against the gateway's
metrics).

Prometheus scrapes the API listener every 5 s, at `127.0.0.1:7197` (Linux) or
`host.docker.internal:7197` (macOS). If `listen_addr` is not the default, change the
target in `deploy/observability/prometheus/`.

### Linux and macOS
The gateway listens on the host's 127.0.0.1, which a container on Docker's bridge network
cannot reach: on Linux, `host-gateway` resolves to the bridge address (172.17.0.1), where
nothing listens. So on Linux `make observability-up` adds `compose.linux.yml`, which
puts Prometheus and Grafana on the host's network, each bound to 127.0.0.1. On macOS
(Docker Desktop) `host.docker.internal` reaches the host's loopback, so `compose.yml`
alone is used. Jaeger stays on the bridge either way: the gateway reaches it through
its published loopback ports.
