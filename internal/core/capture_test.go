package core_test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/server"
)

// recordingSink keeps every exchange it is handed, for the test to inspect. With
// refuse set it plays a full queue: it releases the exchange and says no.
type recordingSink struct {
	mu     sync.Mutex
	exs    []*core.Exchange
	refuse bool
}

func (s *recordingSink) Submit(ex *core.Exchange) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refuse {
		ex.Release()
		return false
	}
	s.exs = append(s.exs, ex)
	return true
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.exs)
}

// wait returns the first n exchanges once the sink holds them. Exchanges are
// submitted when the handler ends, which can be after the client has its response.
func (s *recordingSink) wait(t *testing.T, n int) []*core.Exchange {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		got := slices.Clone(s.exs)
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

// startCaptureGateway is startRegistryGateway with capture on: adapter a in front of
// base, the test profile, and c handed to the registry through WithCapture.
func startCaptureGateway(t *testing.T, a core.Adapter, base *url.URL, c core.Capture) *gateway {
	t.Helper()
	logs := &syncBuffer{}
	log := logging.New(logs, "debug")
	reg := core.NewRegistry(log, core.WithCapture(c))
	reg.AddProfile(testProfile{})
	reg.AddAdapter(a, upstreamConfig(base))
	return serveOnLoopback(t, logs, server.New(log, reg.Mount))
}

func serveOnLoopback(t *testing.T, logs *syncBuffer, srv *server.Server) *gateway {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	addr := ln.Addr().String()
	return &gateway{addr: addr, url: "http://" + addr, logs: logs}
}

// streamEvents answers with n server-sent events of size bytes each, flushing after
// every one, and returns the bytes it sent through sent.
func streamEvents(n, size int, sent *[]byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i := range n {
			ev := fmt.Sprintf("data: %d%s\n\n", i, strings.Repeat("x", size-9))
			*sent = append(*sent, ev...)
			_, _ = io.WriteString(w, ev)
			w.(http.Flusher).Flush()
		}
	}
}

// AC24: a capture that runs out of memory drops the exchange, never the request.
func TestCapture_MemoryLimitDrops(t *testing.T) {
	t.Run("over_limit", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "response body")
		})
		budget := core.NewBudget(10)
		if !budget.TryReserve(10) {
			t.Fatal("could not spend the budget")
		}
		sink := &recordingSink{}
		g := startCaptureGateway(t, testAdapter{}, up.URL, core.Capture{Sink: sink, Budget: budget, MaxBodyBytes: 1 << 20})

		res, err := http.Post(g.url+"/t/v1/x", "text/plain", strings.NewReader("request body"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if string(body) != "response body" {
			t.Errorf("client got %q, want the whole response", body)
		}
		if got := up.only(t).Body; string(got) != "request body" {
			t.Errorf("upstream got %q, want the whole request", got)
		}
		if line := g.accessLine(t, "/t/v1/x"); line["capture"] != "dropped_memory" {
			t.Errorf("capture = %v, want dropped_memory", line["capture"])
		}
		if n := sink.count(); n != 0 {
			t.Errorf("sink got %d exchanges, want none", n)
		}
		if got := budget.InUse(); got != 10 {
			t.Errorf("InUse = %d, want the 10 spent before the request", got)
		}
		if got := budget.Dropped(); got != 1 {
			t.Errorf("Dropped = %d, want the one exchange counted", got)
		}
	})

	t.Run("crosses_limit_mid_stream", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		var sent []byte
		up := newUpstream(t, streamEvents(5, 20, &sent))
		budget := core.NewBudget(50)
		sink := &recordingSink{}
		g := startCaptureGateway(t, testAdapter{}, up.URL, core.Capture{Sink: sink, Budget: budget, MaxBodyBytes: 1 << 20})
		before := budget.InUse()

		res, err := http.Get(g.url + "/t/v1/stream")
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if !bytes.Equal(body, sent) || len(body) != 100 {
			t.Errorf("client got %q, want the whole stream %q", body, sent)
		}
		if line := g.accessLine(t, "/t/v1/stream"); line["capture"] != "dropped_memory" {
			t.Errorf("capture = %v, want dropped_memory", line["capture"])
		}
		if n := sink.count(); n != 0 {
			t.Errorf("sink got %d exchanges, want none", n)
		}
		if got := budget.InUse(); got != before {
			t.Errorf("InUse = %d, want %d as before the request", got, before)
		}
		if budget.Peak() == 0 {
			t.Error("Peak = 0: the stream reserved nothing before it crossed the limit")
		}
		if got := budget.Dropped(); got != 1 {
			t.Errorf("Dropped = %d, want the one exchange counted", got)
		}
	})
}

// AC29: an adapter's declared secret query parameter is redacted in the submitted
// exchange, and forwarded unchanged.
func TestRedact_AdapterQueryParams(t *testing.T) {
	checkNoLeaksAtEnd(t)
	up := newUpstream(t, nil)
	sink := &recordingSink{}
	g := startCaptureGateway(t, parsingAdapter{}, up.URL, core.Capture{Sink: sink, Budget: core.NewBudget(1 << 20), MaxBodyBytes: 1 << 20})

	res, _ := g.raw(t, "GET /t/v1/x?a=1&key=sekrit-q&b=2 HTTP/1.1\r\nHost: x\r\nX-Test-Key: sekrit-h\r\n\r\n")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	if got := up.only(t).RequestURI; got != "/v1/x?a=1&key=sekrit-q&b=2" {
		t.Errorf("upstream got %q, want the query unchanged", got)
	}
	ex := sink.wait(t, 1)[0]
	if ex.Query != "a=1&key=[REDACTED]&b=2" {
		t.Errorf("stored query %q, want key redacted and the rest as sent", ex.Query)
	}
	if got := ex.RequestHeader.Values("X-Test-Key"); !slices.Equal(got, []string{core.Redacted}) {
		t.Errorf("stored X-Test-Key = %q, want [REDACTED]", got)
	}
	ex.Release()
}

// AC22, AC12 groundwork: every Exchange field, from a streamed and a non-streamed
// request, and request_incomplete when upstream answers before the body is read.
func TestCapture_ExchangeFields(t *testing.T) {
	check := func(t *testing.T, ex *core.Exchange, wantStream bool) {
		t.Helper()
		if ex.PrincipalID != core.PrincipalLocal {
			t.Errorf("PrincipalID = %q, want local", ex.PrincipalID)
		}
		if ex.Protocol != "test" || ex.Client != "test-client" || ex.Auth != core.AuthAPIKey {
			t.Errorf("protocol, client, auth = %q, %q, %q", ex.Protocol, ex.Client, ex.Auth)
		}
		if ex.Stream != wantStream {
			t.Errorf("Stream = %v, want %v", ex.Stream, wantStream)
		}
		if !ex.HasTTFB || ex.TTFB <= 0 || ex.Start.IsZero() || ex.End.Before(ex.Start) || ex.End.Sub(ex.Start) < ex.TTFB {
			t.Errorf("times: start %v, ttfb %v (%v), end %v", ex.Start, ex.TTFB, ex.HasTTFB, ex.End)
		}
		if ex.ClientDisconnected || ex.UpstreamAborted || ex.GatewayError != "" || ex.RequestIncomplete {
			t.Errorf("flags: disconnected %v, aborted %v, gateway error %q, incomplete %v",
				ex.ClientDisconnected, ex.UpstreamAborted, ex.GatewayError, ex.RequestIncomplete)
		}
		for _, name := range []string{"Authorization", "X-Test-Key"} {
			if got := ex.RequestHeader.Values(name); !slices.Equal(got, []string{core.Redacted}) {
				t.Errorf("request %s = %q, want [REDACTED]", name, got)
			}
		}
		if got := ex.RequestHeader.Get("X-Test-Client"); got != "yes" {
			t.Errorf("request X-Test-Client = %q, want it as sent", got)
		}
		if got := ex.ResponseHeader.Get("X-Upstream"); got != "here" {
			t.Errorf("response X-Upstream = %q, want upstream's header", got)
		}
		if got := ex.ResponseHeader.Values("Set-Cookie"); !slices.Equal(got, []string{core.Redacted}) {
			t.Errorf("response Set-Cookie = %q, want [REDACTED]", got)
		}
		if ex.Parser == nil || !slices.Equal(ex.Excluded, []string{excludedKey}) {
			t.Errorf("parser %v, excluded %v, want the adapter's", ex.Parser, ex.Excluded)
		}
		if ex.Request.Truncated || ex.Response.Truncated {
			t.Error("a body under the cap is flagged truncated")
		}
	}
	send := func(t *testing.T, g *gateway, path, body string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, g.url+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer sekrit-a")
		req.Header.Set("X-Test-Key", "sekrit-k")
		req.Header.Set("X-Test-Client", "yes")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		return res, got
	}

	t.Run("non_streamed", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Upstream", "here")
			w.Header().Set("Set-Cookie", "session=sekrit-c")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"ok":true}`)
		})
		budget := core.NewBudget(1 << 20)
		sink := &recordingSink{}
		g := startCaptureGateway(t, parsingAdapter{}, up.URL, core.Capture{Sink: sink, Budget: budget, MaxBodyBytes: 1 << 20})

		res, body := send(t, g, "/t/v1/x?q=1", `{"in":1}`)
		ex := sink.wait(t, 1)[0]
		check(t, ex, false)
		if ex.RequestID == "" || ex.RequestID != res.Header.Get("X-Request-Id") {
			t.Errorf("RequestID = %q, want the gateway's %q", ex.RequestID, res.Header.Get("X-Request-Id"))
		}
		if ex.Method != http.MethodPost || ex.Path != "/v1/x" || ex.Query != "q=1" || ex.Status != http.StatusCreated {
			t.Errorf("method, path, query, status = %q, %q, %q, %d", ex.Method, ex.Path, ex.Query, ex.Status)
		}
		if got := string(ex.Request.Bytes()); got != `{"in":1}` {
			t.Errorf("request body %q", got)
		}
		if got := ex.Response.Bytes(); !bytes.Equal(got, body) {
			t.Errorf("response body %q, client got %q", got, body)
		}
		if line := g.accessLine(t, "/t/v1/x"); line["capture"] != "queued" {
			t.Errorf("capture = %v, want queued", line["capture"])
		}
		if budget.InUse() != int64(len(`{"in":1}`)+len(body)) {
			t.Errorf("InUse = %d, want both bodies held until Release", budget.InUse())
		}
		ex.Release()
		ex.Release()
		if budget.InUse() != 0 {
			t.Errorf("InUse = %d after Release, want 0", budget.InUse())
		}
	})

	t.Run("streamed", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		var sent []byte
		events := streamEvents(3, 16, &sent)
		up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Upstream", "here")
			w.Header().Set("Set-Cookie", "session=sekrit-c")
			events(w, r)
		})
		sink := &recordingSink{}
		g := startCaptureGateway(t, parsingAdapter{}, up.URL, core.Capture{Sink: sink, Budget: core.NewBudget(1 << 20), MaxBodyBytes: 1 << 20})

		_, body := send(t, g, "/t/v1/stream", `{"stream":true}`)
		ex := sink.wait(t, 1)[0]
		defer ex.Release()
		check(t, ex, true)
		if ex.Path != "/v1/stream" || ex.Status != http.StatusOK {
			t.Errorf("path, status = %q, %d", ex.Path, ex.Status)
		}
		if got := ex.Response.Bytes(); !bytes.Equal(got, body) || !bytes.Equal(body, sent) {
			t.Errorf("response copy %q, client got %q, upstream sent %q", got, body, sent)
		}
	})

	t.Run("request_incomplete", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		const size = 32 << 20 // far more than loopback's socket buffers hold
		up := newEarlyUpstream(t)
		sink := &recordingSink{}
		g := startCaptureGateway(t, parsingAdapter{}, up, core.Capture{Sink: sink, Budget: core.NewBudget(2 * size), MaxBodyBytes: 2 * size})

		conn, err := net.Dial("tcp", g.addr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		// Upstream never reads the body, so the transport stalls writing it once the
		// socket buffers fill, and upstream's answer comes back long before the end.
		// The body is sent from its own goroutine until the connection gives up.
		if _, err := fmt.Fprintf(conn, "POST /t/v1/x HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\n\r\n", size); err != nil {
			t.Fatal(err)
		}
		written := make(chan struct{})
		go func() {
			defer close(written)
			chunk := bytes.Repeat([]byte("b"), 64<<10)
			for sent := 0; sent < size; sent += len(chunk) {
				if _, err := conn.Write(chunk); err != nil {
					return
				}
			}
		}()
		res, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		early, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if string(early) != "early" {
			t.Fatalf("client got %q, want upstream's early answer", early)
		}
		ex := sink.wait(t, 1)[0]
		defer ex.Release()
		_ = conn.Close()
		<-written
		if !ex.RequestIncomplete {
			t.Error("RequestIncomplete = false for a body sealed before its end")
		}
		if ex.Request.Size >= size {
			t.Errorf("request copy holds %d bytes, want fewer than the %d declared", ex.Request.Size, size)
		}
		if ex.Request.Truncated {
			t.Error("an incomplete body is flagged truncated; truncated means the cap only")
		}
	})
}

// newEarlyUpstream answers "early" at once, without reading the request body.
func newEarlyUpstream(t *testing.T) *url.URL {
	t.Helper()
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "early")
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &url.URL{Scheme: "http", Host: ln.Addr().String()}
}

// AC8, through the proxy (a subtest of TestCapture_OutgoingRequestHasNoGetBody): with
// capture on, the request the proxy hands its transport has no GetBody, so the
// transport can never replay a body the tee has already copied.
func outgoingRequestHasNoGetBodyThroughProxy(t *testing.T) {
	checkNoLeaksAtEnd(t)
	up := newUpstream(t, nil)
	sink := &recordingSink{}
	logs := &syncBuffer{}
	log := logging.New(logs, "debug")
	p := core.NewCapturingProxy(testAdapter{}, upstreamConfig(up.URL), identifyWith(), log,
		core.Capture{Sink: sink, Budget: core.NewBudget(1 << 20), MaxBodyBytes: 1 << 20})
	var mu sync.Mutex
	var sawGetBody, sawBody bool
	core.WrapTransport(p, func(next http.RoundTripper) http.RoundTripper {
		return roundTripFunc(func(req *http.Request) (*http.Response, error) {
			mu.Lock()
			sawGetBody = req.GetBody != nil
			sawBody = req.Body != nil
			mu.Unlock()
			return next.RoundTrip(req)
		})
	})
	g := serveOnLoopback(t, logs, server.New(log, func(r chi.Router) { r.Handle("/t/*", p) }))

	res, err := http.Post(g.url+"/t/v1/x", "text/plain", strings.NewReader("request body"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	ex := sink.wait(t, 1)[0]
	defer ex.Release()
	mu.Lock()
	defer mu.Unlock()
	if !sawBody {
		t.Fatal("the outgoing request had no body; the check proves nothing")
	}
	if sawGetBody {
		t.Fatal("the outgoing request has a GetBody: the transport could replay the body")
	}
	if got := string(ex.Request.Bytes()); got != "request body" {
		t.Errorf("request copy %q, want the body once", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// AC7 groundwork, AC23 groundwork: a proxied line always says what became of its
// capture, off with no capture; a line off the proxy has no capture field.
func TestAccessLog_CaptureField(t *testing.T) {
	t.Run("off_without_capture", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		up := newUpstream(t, nil)
		g := startRegistryGateway(t, up.URL)
		g.send(t, http.MethodGet, "/t/v1/x", nil)
		g.send(t, http.MethodGet, "/healthz", nil)
		if line := g.accessLine(t, "/t/v1/x"); line["capture"] != "off" {
			t.Errorf("capture = %v, want off", line["capture"])
		}
		if line := g.accessLine(t, "/healthz"); line["capture"] != nil {
			t.Errorf("/healthz line has capture = %v, want no field", line["capture"])
		}
	})

	t.Run("queue_full", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "response body")
		})
		budget := core.NewBudget(1 << 20)
		sink := &recordingSink{refuse: true}
		g := startCaptureGateway(t, testAdapter{}, up.URL, core.Capture{Sink: sink, Budget: budget, MaxBodyBytes: 1 << 20})
		g.send(t, http.MethodGet, "/t/v1/x", nil)
		if line := g.accessLine(t, "/t/v1/x"); line["capture"] != "dropped_queue_full" {
			t.Errorf("capture = %v, want dropped_queue_full", line["capture"])
		}
		if budget.InUse() != 0 {
			t.Errorf("InUse = %d, want 0 after a refused submit", budget.InUse())
		}
	})
}
