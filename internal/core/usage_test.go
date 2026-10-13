package core_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/contentcoding"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/server"
)

// usageAdapter is the test adapter with the rate-limit extension: its reader counts
// the bytes it is shown as tokens, and ok is false when it saw none.
type usageAdapter struct {
	testAdapter
	readers atomic.Int64
	status  atomic.Int64
	stream  atomic.Bool
}

func (a *usageAdapter) IsModelCall(method, path string) bool { return true }
func (a *usageAdapter) LimitErrorBody(core.LimitErrorKind, string) (string, []byte) {
	return "text/plain", nil
}
func (a *usageAdapter) UsageReader(status int, h http.Header, stream bool) core.UsageReader {
	a.readers.Add(1)
	a.status.Store(int64(status))
	a.stream.Store(stream)
	return &byteCounter{}
}

type byteCounter struct{ n int64 }

func (b *byteCounter) Write(p []byte)        { b.n += int64(len(p)) }
func (b *byteCounter) Tokens() (int64, bool) { return b.n, b.n > 0 }

type usageCalls struct {
	mu    sync.Mutex
	got   []core.UsageResult
	ready chan struct{}
}

func newUsageCalls() *usageCalls { return &usageCalls{ready: make(chan struct{}, 8)} }

func (c *usageCalls) record(r core.UsageResult) {
	c.mu.Lock()
	c.got = append(c.got, r)
	c.mu.Unlock()
	c.ready <- struct{}{}
}

func (c *usageCalls) wait(t *testing.T) core.UsageResult {
	t.Helper()
	select {
	case <-c.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("OnUsage was not called")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.got[len(c.got)-1]
}

func (c *usageCalls) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

// startUsageGateway mounts a with a guard that sets OnUsage to cb when cb is not nil.
func startUsageGateway(t *testing.T, a core.Adapter, base *url.URL, cb func(core.UsageResult), opts ...core.RegistryOption) *gateway {
	t.Helper()
	logs := &syncBuffer{}
	log := logging.New(logs, "debug")
	guard := core.WithGuard(func(core.Adapter) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if m := core.MetaFrom(r.Context()); m != nil && cb != nil {
					m.OnUsage = cb
				}
				next.ServeHTTP(w, r)
			})
		}
	})
	reg := core.NewRegistry(log, append(opts, guard)...)
	reg.AddAdapter(a, upstreamConfig(base))
	srv := server.New(log, reg.Mount)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &gateway{addr: ln.Addr().String(), url: "http://" + ln.Addr().String(), logs: logs}
}

func TestUsageWriter_ForwardsBytesUnchangedAndFlushes(t *testing.T) {
	release := make(chan struct{})
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "second\n")
	})
	calls := newUsageCalls()
	gw := startUsageGateway(t, &usageAdapter{}, up.URL, calls.record)

	res, err := http.Get(gw.url + "/t/v1/x")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	br := bufio.NewReader(res.Body)
	line, err := br.ReadString('\n')
	if err != nil || line != "first\n" {
		t.Fatalf("first chunk = %q, %v; want it before upstream finished", line, err)
	}
	close(release)
	rest, _ := io.ReadAll(br)
	if string(rest) != "second\n" {
		t.Errorf("rest = %q, want second", rest)
	}
	if r := calls.wait(t); r.Tokens != int64(len("first\nsecond\n")) {
		t.Errorf("tokens = %d, want every forwarded byte seen", r.Tokens)
	}
}

// AC9: one OnUsage per call, at settle, with the status and the reader's tokens.
func TestUsageWriter_CallsOnUsageOnceAtSettle(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"a":1}`)
	})
	a := &usageAdapter{}
	calls := newUsageCalls()
	gw := startUsageGateway(t, a, up.URL, calls.record)

	res, body := gw.send(t, http.MethodPost, "/t/v1/x", nil)
	if res.StatusCode != 200 || string(body) != `{"a":1}` {
		t.Fatalf("response = %d %q", res.StatusCode, body)
	}
	got := calls.wait(t)
	if got != (core.UsageResult{Tokens: 7, OK: true, Status: 200}) {
		t.Errorf("result = %+v", got)
	}
	time.Sleep(100 * time.Millisecond)
	if n := calls.count(); n != 1 {
		t.Errorf("OnUsage called %d times, want 1", n)
	}
	if a.readers.Load() != 1 || a.stream.Load() {
		t.Errorf("readers = %d stream = %v, want 1 non-stream", a.readers.Load(), a.stream.Load())
	}
}

// E1: the client leaves mid-stream; what was seen still counts.
func TestUsageWriter_ClientDisconnectMidStreamStillReports(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "chunk")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	calls := newUsageCalls()
	gw := startUsageGateway(t, &usageAdapter{}, up.URL, calls.record)

	res, err := http.Get(gw.url + "/t/v1/x")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(res.Body, buf); err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	http.DefaultClient.CloseIdleConnections()

	got := calls.wait(t)
	if !got.OK || got.Tokens != 5 {
		t.Errorf("result = %+v, want 5 tokens from the part seen", got)
	}
	if n := calls.count(); n != 1 {
		t.Errorf("OnUsage called %d times, want 1", n)
	}
}

// AC1: without OnUsage nothing observes the response.
func TestUsageWriter_NoWrapperWhenOnUsageNil(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "body") })
	a := &usageAdapter{}
	gw := startUsageGateway(t, a, up.URL, nil)

	res, body := gw.send(t, http.MethodPost, "/t/v1/x", nil)
	if res.StatusCode != 200 || string(body) != "body" {
		t.Fatalf("response = %d %q", res.StatusCode, body)
	}
	if n := a.readers.Load(); n != 0 {
		t.Errorf("UsageReader asked for %d times, want 0", n)
	}
}

// AC10: counted with capture off, and when the exchange is dropped from capture.
func TestUsageWriter_IndependentOfCapture(t *testing.T) {
	cases := map[string][]core.RegistryOption{
		"capture off": nil,
		"capture dropped for memory": {core.WithCapture(core.Capture{
			Sink: &recordingSink{}, Budget: core.NewBudget(1), MaxBodyBytes: 1 << 20,
		})},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, strings.Repeat("x", 100))
			})
			calls := newUsageCalls()
			gw := startUsageGateway(t, &usageAdapter{}, up.URL, calls.record, opts...)
			_, body := gw.send(t, http.MethodPost, "/t/v1/x", nil)
			if len(body) != 100 {
				t.Fatalf("body = %d bytes", len(body))
			}
			if got := calls.wait(t); got.Tokens != 100 || !got.OK {
				t.Errorf("result = %+v, want 100 tokens", got)
			}
		})
	}
}

func TestRegistry_WithGuardWrapsProxy(t *testing.T) {
	up := newUpstream(t, nil)
	logs := &syncBuffer{}
	log := logging.New(logs, "debug")
	var guarded []string
	reg := core.NewRegistry(log, core.WithGuard(func(a core.Adapter) func(http.Handler) http.Handler {
		guarded = append(guarded, a.Name())
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Deny") != "" {
					w.WriteHeader(http.StatusTeapot)
					return
				}
				w.Header().Set("X-Guarded", "yes")
				next.ServeHTTP(w, r)
			})
		}
	}))
	reg.AddAdapter(testAdapter{}, upstreamConfig(up.URL))
	srv := server.New(log, func(r chi.Router) { reg.Mount(r) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	gw := &gateway{url: "http://" + ln.Addr().String()}

	if len(guarded) != 1 || guarded[0] != "test" {
		t.Fatalf("guard asked for %v, want [test]", guarded)
	}
	res, _ := gw.send(t, http.MethodGet, "/t/v1/x", nil)
	if res.StatusCode != 200 || res.Header.Get("X-Guarded") != "yes" {
		t.Errorf("allowed: status %d, X-Guarded %q", res.StatusCode, res.Header.Get("X-Guarded"))
	}
	res, _ = gw.send(t, http.MethodGet, "/t/v1/x", http.Header{"X-Deny": {"1"}})
	if res.StatusCode != http.StatusTeapot || len(up.reqs) != 1 {
		t.Errorf("denied: status %d, upstream saw %d requests; want 418 and 1", res.StatusCode, len(up.reqs))
	}
}

// panicReader is a reader whose decoder panicked: it reports no tokens and names the
// panic, the way an adapter's reader does over a contentcoding.Feeder.
type panicReader struct{ f *contentcoding.Feeder }

func (p *panicReader) Write(b []byte) { _, _ = p.f.Write(b) }
func (p *panicReader) Tokens() (int64, bool) {
	p.f.Close()
	return 0, false
}
func (p *panicReader) Panicked() (string, bool) {
	if p.f.Close() != contentcoding.Panicked {
		return "", false
	}
	return p.f.PanicType(), true
}

type panicAdapter struct{ usageAdapter }

func (a *panicAdapter) UsageReader(int, http.Header, bool) core.UsageReader {
	return &panicReader{f: contentcoding.NewFeeder("identity", func(io.Reader) { panic("decoder bug") }, contentcoding.DefaultMaxPending)}
}

// A panicking decoder logs one warn line with the request ID, records no tokens and
// never changes the bytes the client receives.
func TestUsageWriter_PanickedDecoderLogsOnceAndForwardsBytes(t *testing.T) {
	const body = "first\nsecond\n"
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	})
	calls := newUsageCalls()
	gw := startUsageGateway(t, &panicAdapter{}, up.URL, calls.record)

	res, err := http.Get(gw.url + "/t/v1/x")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if string(got) != body {
		t.Errorf("client got %q, want %q", got, body)
	}
	r := calls.wait(t)
	if r.OK || r.Tokens != 0 {
		t.Errorf("result = %+v, want no tokens", r)
	}
	var warns []map[string]any
	for _, raw := range strings.Split(gw.logs.String(), "\n") {
		var line map[string]any
		if json.Unmarshal([]byte(raw), &line) == nil && line["msg"] == "usage decoder panicked" {
			warns = append(warns, line)
		}
	}
	if len(warns) != 1 {
		t.Fatalf("got %d panic lines, want exactly 1:\n%s", len(warns), gw.logs.String())
	}
	if warns[0]["level"] != "warn" || warns[0]["request_id"] == nil || warns[0]["request_id"] == "" {
		t.Errorf("line = %v, want warn with a request_id", warns[0])
	}
	if warns[0]["panic_type"] != "string" {
		t.Errorf("panic_type = %v, want string", warns[0]["panic_type"])
	}
}
