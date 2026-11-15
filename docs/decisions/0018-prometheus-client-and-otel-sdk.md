---
status: proposed
date: 2026-11-15
---

# 0018 — Prometheus client_golang and the OpenTelemetry Go SDK for self-observability

## Context
PLAN §8 decided the backends for Phase 8: Prometheus + Grafana for metrics and
OpenTelemetry → Jaeger for tracing. It did not name the Go libraries, and AGENTS.md
requires an ADR for every new dependency. Spec 021 needs a `/metrics` endpoint with
histograms, Go runtime and process metrics, and OTLP span export that is off by default
and costs nothing then. The module stays on go 1.25 (the toolchain in use), which bounds
the versions: client_golang v1.25 and OTel v1.47 require go 1.26.

## Decision
Use `github.com/prometheus/client_golang` v1.24.0 for metrics, on a registry of the
gateway's own (never the global default), and the OpenTelemetry Go API and SDK v1.46.0
with the OTLP/HTTP trace exporter (`otlptracehttp`) for tracing. Tracing is wired through
a `trace.Tracer` that is nil when off, so the hot path checks a pointer and starts no
span.

## Alternatives
- **Hand-written Prometheus text exposition**: no dependency, but histograms, the runtime
  and process collectors and the exposition format's edge cases would be ours to keep
  right. Rejected: the client is the reference implementation, and its cost is binary
  size only.
- **VictoriaMetrics/metrics** (smaller, no protobuf): rejected; no process/runtime
  collectors of the same shape and less familiar to the dashboard tooling the plan names.
- **OTel metrics instead of Prometheus**: one SDK for both, but needs an exporter or a
  bridge for a pull-based `/metrics`, and the plan decided Prometheus.
- **OTLP/gRPC exporter**: links gRPC into the binary (several MB more) for no gain on a
  local Jaeger, which takes OTLP over HTTP on 4318.
- **Jaeger's own client libraries**: deprecated in favour of OTel; vendor lock-in (PLAN §8).

## Consequences
The binary grows (measured in specs/021-self-observability/research.md). Metrics are
always on; a metric's labels are a public contract the dashboard reads, so renaming one
means changing the dashboard JSON with it. Swapping the tracing backend is a config
change (any OTLP/HTTP collector). Moving to go 1.26 later lets both libraries upgrade.
