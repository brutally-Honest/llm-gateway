package observability_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/clients/claudecode"
	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/observability"
	"github.com/brutally-honest/llm-gateway/internal/protocols/anthropic"
	"github.com/brutally-honest/llm-gateway/internal/server"
)

// nopStore stores nothing, so the benchmark measures the proxy, not SQLite.
type nopStore struct{}

func (nopStore) SaveExchange(context.Context, *core.Exchange) error { return nil }
func (nopStore) SaveParse(context.Context, string, string, core.ParseStatus, []core.StoredEvent, []core.Content) error {
	return nil
}
func (nopStore) Close() error { return nil }

// nopExporter drops every span: tracing on costs the span work, not the network.
type nopExporter struct{}

func (nopExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }
func (nopExporter) Shutdown(context.Context) error                             { return nil }

// sse is a short streamed answer, the shape of a Claude Code turn.
const sse = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-opus-5-5\",\"content\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n" +
	"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
	"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
	"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
	"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

const reqBody = `{"model":"claude-opus-5-5","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"say hello"}]}`

// benchGateway serves the proxy with capture on (into nopStore), metrics m (nil: off)
// and tracer provider tp (nil: tracing off), in front of a local streaming upstream.
func benchGateway(b *testing.B, m *observability.Metrics, tp *sdktrace.TracerProvider) string {
	b.Helper()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range strings.SplitAfter(sse, "\n\n") {
			_, _ = io.WriteString(w, ev)
			w.(http.Flusher).Flush()
		}
	}))
	b.Cleanup(up.Close)
	base, _ := url.Parse(up.URL)
	log := zap.NewNop()
	sink := capture.NewSink(capture.Config{QueueSize: 1024, Workers: 2, DecodeLimit: 32 << 20, Metrics: m}, nopStore{}, log)
	b.Cleanup(func() { sink.Close(context.Background()) })
	opts := []core.RegistryOption{core.WithCapture(core.Capture{Sink: sink, Budget: core.NewBudget(256 << 20), MaxBodyBytes: 32 << 20})}
	if tp != nil {
		opts = append(opts, core.WithTracer(observability.TracerOf(tp)))
		b.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	}
	reg := core.NewRegistry(log, opts...)
	reg.AddProfile(claudecode.Profile{})
	reg.AddAdapter(anthropic.Adapter{}, config.Upstream{BaseURL: base, ConnectTimeout: 5 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: time.Minute})
	srv := server.NewWithMetrics(log, m, reg.Mount)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	b.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String() + "/anthropic/v1/messages"
}

// benchCalls makes b.N streamed model calls through the gateway, reading each answer
// whole, over keep-alive connections.
func benchCalls(b *testing.B, target string) {
	b.Helper()
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 64}}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest(http.MethodPost, target, strings.NewReader(reqBody))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "claude-cli/2.1.0 (external, cli)")
			resp, err := client.Do(req)
			if err != nil {
				b.Error(err)
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	})
}

// Spec 021 AC16: the proxy hot path, metrics off and on, tracing off and on. Run with
// go test -run '^$' -bench ProxyHotPath -count 6 ./internal/observability.
func BenchmarkProxyHotPath(b *testing.B) {
	b.Run("metrics_off_tracing_off", func(b *testing.B) {
		benchCalls(b, benchGateway(b, nil, nil))
	})
	b.Run("metrics_on_tracing_off", func(b *testing.B) {
		benchCalls(b, benchGateway(b, observability.NewMetrics("bench"), nil))
	})
	b.Run("metrics_on_tracing_on", func(b *testing.B) {
		tp := observability.NewTracerProvider(sdktrace.NewBatchSpanProcessor(nopExporter{}), nil)
		benchCalls(b, benchGateway(b, observability.NewMetrics("bench"), tp))
	})
}
