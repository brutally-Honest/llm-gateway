# 021: self-observability

Covers what the automated tests cannot see for `specs/021-self-observability` (AC17): the
stack against the real gateway running the build, real Claude Code calls, one restart,
the dashboard, a trace and the Prometheus target in a real browser. The automated parts
are `cmd/gateway/run_metrics_test.go`, `run_tracing_test.go`, `run_dashboard_test.go`,
`internal/observability/*_test.go` and `internal/server/metrics_test.go`; the overhead
numbers come from `BenchmarkProxyHotPath` (research.md). Record step / expected / seen /
match in the spec's research.md.

## Setup

1. Real gateway: build this branch and run it as the service, keeping the binary it
   replaces:

   ```sh
   make build
   cp ~/.local/bin/gateway ~/.local/bin/gateway.bak-021
   install -m 0755 bin/gateway ~/.local/bin/gateway
   ```

2. Tracing on in the service's config (`tracing.enabled: true`; the endpoint defaults to
   the stack's Jaeger), keeping a copy of the file. The config is the user's: change it
   only with their go-ahead.

3. `make observability-up`, then `systemctl --user restart llm-gateway`.

## Steps

| # | Step | Expected |
|---|---|---|
| 1 | `curl -s 127.0.0.1:7197/metrics \| grep gateway_build_info` and the same on 7199 | One line each, `version` = this build |
| 2 | `curl -s -x http://127.0.0.1:7198 http://127.0.0.1:7198/metrics` and a plain GET on 7198 | No metrics text |
| 3 | <http://127.0.0.1:9090/targets> | Job `llm-gateway`, target `127.0.0.1:7197`, state UP |
| 4 | Use Claude Code through the gateway for a few calls (one with a tool call), with the UI open on a session | Calls answer as before |
| 5 | `systemctl --user restart llm-gateway`; journal | Stops in a few seconds, no SIGKILL; `last-shutdown.json` in the capture directory |
| 6 | Two or three more Claude Code calls; open a session page in the UI (cold, then again for cached) | |
| 7 | Grafana <http://127.0.0.1:3030>, home dashboard "LLM Gateway — self-observability", last 1 hour | Every panel shows data: request rate, model-call latency, overhead, TTFB, upstream total, errors (0 is data), queue depth/capacity, drops (0), store write time, open tunnels, open UI streams, view build cold and cached, warm-up, last shutdown, cut at last shutdown, build, uptime |
| 8 | Jaeger <http://127.0.0.1:16686>, service `llm-gateway`, a trace of a model call | Spans `gateway.receive`, `gateway.upstream_request`, `gateway.response_stream`, `gateway.capture`, two `gateway.store_write`; attributes have the exchange id, sizes, status, model; no prompt, response or header |
| 9 | `gateway dump <exchange id from the trace>` | The same call |

## Undo

`make observability-down`; restore the config copy and the binary
(`install -m 0755 ~/.local/bin/gateway.bak-021 ~/.local/bin/gateway`), then restart the
service.
