package observability

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// ServiceName is the service the gateway's spans are filed under in Jaeger.
const ServiceName = "llm-gateway"

// tracerScope is the instrumentation scope of the proxied call's spans.
const tracerScope = "github.com/brutally-honest/llm-gateway"

// Tracing is tracing while the gateway runs. A nil *Tracing is tracing off.
type Tracing struct {
	provider *sdktrace.TracerProvider
}

// StartTracing exports spans in batches to endpoint, an OTLP/HTTP base URL, over a
// provider of the gateway's own. The exporter connects lazily: an unreachable collector
// never costs a request, and its failures are logged at most once a minute (spec 021
// AC10, research D16). OTel's own errors and logs go through log, never to stderr.
func StartTracing(endpoint *url.URL, version string, log *zap.Logger) (*Tracing, error) {
	otel.SetLogger(logr.Discard())
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warn("tracing error", zap.Error(err))
	}))
	exp, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpointURL(strings.TrimSuffix(endpoint.String(), "/")+"/v1/traces"))
	if err != nil {
		return nil, err
	}
	res := resource.NewSchemaless(
		attribute.String("service.name", ServiceName),
		attribute.String("service.version", version),
	)
	quiet := newQuietExporter(exp, log, exportWarnEvery, time.Now)
	return &Tracing{provider: NewTracerProvider(sdktrace.NewBatchSpanProcessor(quiet), res)}, nil
}

// exportWarnEvery is the most often a collector that stays down is logged: the batch
// processor exports every 5 s, and a warning per batch floods the journal.
const exportWarnEvery = time.Minute

// quietExporter logs failed exports itself, rate-limited: the first failure at once,
// then at most one warning per interval with the count of failed batches, and one
// "trace export recovered" line when an export succeeds again. It reports success to
// the batch processor, which drops a failed batch either way, so the global handler does
// not log every batch too.
type quietExporter struct {
	sdktrace.SpanExporter
	log   *zap.Logger
	every time.Duration
	now   func() time.Time

	mu       sync.Mutex
	failed   int // failed batches since the last success
	lastWarn time.Time
}

func newQuietExporter(exp sdktrace.SpanExporter, log *zap.Logger, every time.Duration, now func() time.Time) *quietExporter {
	return &quietExporter{SpanExporter: exp, log: log, every: every, now: now}
}

func (e *quietExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.SpanExporter.ExportSpans(ctx, spans)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err == nil {
		if e.failed > 0 {
			e.log.Info("trace export recovered", zap.Int("failed_batches", e.failed))
			e.failed = 0
		}
		return nil
	}
	e.failed++
	if now := e.now(); e.failed == 1 || now.Sub(e.lastWarn) >= e.every {
		e.log.Warn("trace export failed", zap.Error(err), zap.Int("failed_batches", e.failed),
			zap.Duration("next_warning_in", e.every))
		e.lastWarn = now
	}
	return nil
}

// NewTracerProvider is the provider the gateway traces with: every call sampled, its
// spans handed to sp. Tests pass an in-memory processor.
func NewTracerProvider(sp sdktrace.SpanProcessor, res *resource.Resource) *sdktrace.TracerProvider {
	opts := []sdktrace.TracerProviderOption{sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(sp)}
	if res != nil {
		opts = append(opts, sdktrace.WithResource(res))
	}
	return sdktrace.NewTracerProvider(opts...)
}

// Tracer is the tracer for proxied calls; nil when tracing is off, which core reads as
// "start no span".
func (t *Tracing) Tracer() trace.Tracer {
	if t == nil {
		return nil
	}
	return TracerOf(t.provider)
}

// TracerOf is the gateway's tracer from p.
func TracerOf(p trace.TracerProvider) trace.Tracer {
	return p.Tracer(tracerScope)
}

// Shutdown flushes the spans still queued, up to ctx, and stops the exporter.
func (t *Tracing) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	return t.provider.Shutdown(ctx)
}
