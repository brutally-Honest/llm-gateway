package capture_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/server"
)

// testAdapter is a protocol that exists only in tests. Its name and prefix are
// neutral, so capture's tests name no provider.
type testAdapter struct{}

func (testAdapter) Name() string                       { return "test" }
func (testAdapter) Prefix() string                     { return "/t" }
func (testAdapter) DefaultBaseURL() string             { return "http://127.0.0.1" }
func (testAdapter) AuthKind(http.Header) core.AuthKind { return core.AuthNone }
func (testAdapter) ErrorBody(reason string) (string, []byte) {
	return "text/plain", []byte("test error: " + reason)
}

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newLogger() (*zap.Logger, *syncBuffer) {
	logs := &syncBuffer{}
	return logging.New(logs, "debug"), logs
}

// blockingStore blocks every SaveExchange until its context is cancelled: a store
// that never returns on its own. Each call announces itself on entered first.
type blockingStore struct {
	entered chan string // request IDs, in the order the calls began

	mu      sync.Mutex
	ctxErrs []error // what each call's context said when it gave up
}

func newBlockingStore() *blockingStore {
	return &blockingStore{entered: make(chan string, 1024)}
}

func (s *blockingStore) SaveExchange(ctx context.Context, ex *core.Exchange) error {
	s.entered <- ex.RequestID
	<-ctx.Done()
	s.mu.Lock()
	s.ctxErrs = append(s.ctxErrs, ctx.Err())
	s.mu.Unlock()
	return ctx.Err()
}

func (s *blockingStore) SaveParse(context.Context, string, string, core.ParseStatus, []core.StoredEvent, []core.Content) error {
	return nil
}

func (s *blockingStore) Close() error { return nil }

// waitEntered waits for n calls to have begun.
func (s *blockingStore) waitEntered(t *testing.T, n int) {
	t.Helper()
	for range n {
		select {
		case <-s.entered:
		case <-time.After(2 * time.Second):
			t.Fatal("the store was never called")
		}
	}
}

func (s *blockingStore) cancelled() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]error(nil), s.ctxErrs...)
}

// failingStore fails every write at once, and counts the calls.
type failingStore struct {
	calls atomic.Int64
}

var errStoreFailed = errors.New("store failed")

func (s *failingStore) SaveExchange(context.Context, *core.Exchange) error {
	s.calls.Add(1)
	return errStoreFailed
}

func (s *failingStore) SaveParse(context.Context, string, string, core.ParseStatus, []core.StoredEvent, []core.Content) error {
	return errStoreFailed
}

func (s *failingStore) Close() error { return nil }

// recordingStore keeps the request ID of every stored exchange.
type recordingStore struct {
	mu  sync.Mutex
	ids []string
}

func (s *recordingStore) SaveExchange(_ context.Context, ex *core.Exchange) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ids = append(s.ids, ex.RequestID)
	return nil
}

func (s *recordingStore) SaveParse(context.Context, string, string, core.ParseStatus, []core.StoredEvent, []core.Content) error {
	return nil
}

func (s *recordingStore) Close() error { return nil }

func (s *recordingStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ids)
}

// holdingSink keeps every exchange the proxy submits, still holding its memory, so a
// sink test can hand real exchanges to a capture.Sink.
type holdingSink struct {
	mu  sync.Mutex
	exs []*core.Exchange
}

func (s *holdingSink) Submit(ex *core.Exchange) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exs = append(s.exs, ex)
	return true
}

func (s *holdingSink) wait(t *testing.T, n int) []*core.Exchange {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		got := append([]*core.Exchange(nil), s.exs...)
		s.mu.Unlock()
		if len(got) >= n {
			return got[:n]
		}
		if time.Now().After(deadline) {
			t.Fatalf("sink holds %d exchanges, want %d", len(got), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// gateway is server.New with the core proxy for the test adapter in front of an
// upstream, capturing into sink, listening on loopback, with its logs in a buffer.
type gateway struct {
	url  string
	logs *syncBuffer
}

// startGateway serves the proxy until the test ends. sink is closed, if it is a
// capture.Sink, only after the server: the shutdown order of the spec.
func startGateway(t *testing.T, base *url.URL, log *zap.Logger, logs *syncBuffer, sink core.CaptureSink, budget *core.Budget) *gateway {
	t.Helper()
	return startGatewayFor(t, testAdapter{}, base, log, logs, sink, budget)
}

// startGatewayFor is startGateway with adapter a in place of the test adapter.
func startGatewayFor(t *testing.T, a core.Adapter, base *url.URL, log *zap.Logger, logs *syncBuffer, sink core.CaptureSink, budget *core.Budget) *gateway {
	t.Helper()
	reg := core.NewRegistry(log, core.WithCapture(core.Capture{Sink: sink, Budget: budget, MaxBodyBytes: 1 << 20}))
	reg.AddAdapter(a, config.Upstream{
		BaseURL:               base,
		ConnectTimeout:        5 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	})
	srv := server.New(log, reg.Mount)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		if s, ok := sink.(*capture.Sink); ok {
			closeSink(s)
		}
	})
	return &gateway{url: "http://" + ln.Addr().String(), logs: logs}
}

// closeSink closes s with a short drain, so a store that blocks is cancelled.
func closeSink(s *capture.Sink) int {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	return s.Close(ctx)
}

// drainSink closes s with time to store everything queued, and fails the test if
// anything is left undrained.
func drainSink(t *testing.T, s *capture.Sink) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if n := s.Close(ctx); n != 0 {
		t.Fatalf("Close left %d exchanges undrained", n)
	}
}

// logLines decodes every log line with message msg.
func logLines(t *testing.T, logs *syncBuffer, msg string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(logs.String(), "\n") {
		if raw == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line is not JSON: %q", raw)
		}
		if line["msg"] == msg {
			lines = append(lines, line)
		}
	}
	return lines
}

// captureFailed returns the capture_failed lines for requestID.
func captureFailed(t *testing.T, logs *syncBuffer, requestID string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, line := range logLines(t, logs, "capture_failed") {
		if line["request_id"] == requestID {
			lines = append(lines, line)
		}
	}
	return lines
}

// newUpstream is a loopback upstream answering with respond.
func newUpstream(t *testing.T, respond http.HandlerFunc) *url.URL {
	t.Helper()
	srv := httptest.NewServer(respond)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// accessLine waits for the gateway's request line for path and returns it decoded.
func (g *gateway) accessLine(t *testing.T, path string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, raw := range strings.Split(g.logs.String(), "\n") {
			if raw == "" {
				continue
			}
			var line map[string]any
			if err := json.Unmarshal([]byte(raw), &line); err != nil {
				t.Fatalf("log line is not JSON: %q", raw)
			}
			if line["msg"] == "request" && line["path"] == path {
				return line
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no request line for %s in:\n%s", path, g.logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// post sends body to path and returns the response body, failing the test if the
// request takes longer than within.
func (g *gateway) post(t *testing.T, path, body string, within time.Duration) string {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	start := time.Now()
	res, err := client.Post(g.url+path, "text/plain", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > within {
		t.Errorf("%s took %v, want under %v: the request waited on capture", path, took, within)
	}
	if res.StatusCode != http.StatusOK {
		t.Errorf("%s: status %d, want 200", path, res.StatusCode)
	}
	return string(got)
}

// capturedExchanges runs n requests with bodies through the proxy and returns the n
// exchanges it submitted, each still holding its share of the returned budget.
func capturedExchanges(t *testing.T, n int) ([]*core.Exchange, *core.Budget) {
	t.Helper()
	base := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "response body")
	})
	log, logs := newLogger()
	hold := &holdingSink{}
	budget := core.NewBudget(1 << 20)
	g := startGateway(t, base, log, logs, hold, budget)
	for range n {
		g.post(t, "/t/v1/x", "request body", 5*time.Second)
	}
	exs := hold.wait(t, n)
	if budget.InUse() == 0 {
		t.Fatal("the captured exchanges hold no memory")
	}
	return exs, budget
}

// checkNoLeaksAtEnd registers 001's leak check, extended to internal/capture. Call
// it first in a test, so it runs after every other cleanup has closed the gateway,
// the sink and the upstreams. It polls for up to 2s for the goroutine profile to
// hold no stack through internal/core or internal/capture (a proxy or a worker still
// at work) and no connection handler of the test's own servers.
func checkNoLeaksAtEnd(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			leaked := leakedStacks()
			if len(leaked) == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("%d goroutine(s) left behind:\n\n%s", len(leaked), strings.Join(leaked, "\n\n"))
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
}

func leakedStacks() []string {
	var buf bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&buf, 2)
	var leaked []string
	for _, s := range strings.Split(buf.String(), "\n\n") {
		if strings.Contains(s, "capture_test.leakedStacks") {
			continue // the goroutine writing the profile: this one
		}
		inGateway := strings.Contains(s, "/internal/core.") || strings.Contains(s, "/internal/capture.")
		inTestHandler := strings.Contains(s, "net/http.(*conn).serve") && strings.Contains(s, "/internal/capture_test.")
		if inGateway || inTestHandler {
			leaked = append(leaked, s)
		}
	}
	return leaked
}
