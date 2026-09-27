package core_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/server"
)

// testAdapter is a protocol that exists only in tests. Its name and prefix are
// neutral, so core's tests name no provider either.
type testAdapter struct{}

func (testAdapter) Name() string           { return "test" }
func (testAdapter) Prefix() string         { return "/t" }
func (testAdapter) DefaultBaseURL() string { return "http://127.0.0.1" }

// AuthKind looks at header presence only, never a value.
func (testAdapter) AuthKind(h http.Header) core.AuthKind {
	switch {
	case h.Get("X-Test-Key") != "":
		return core.AuthAPIKey
	case h.Get("Authorization") != "":
		return core.AuthBearer
	}
	return core.AuthNone
}

func (testAdapter) ErrorBody(reason string) (string, []byte) {
	return "text/plain", []byte("test error: " + reason)
}

// testProfile is a client that exists only in tests: it sends X-Test-Client.
type testProfile struct{}

func (testProfile) Name() string               { return "test-client" }
func (testProfile) Match(r *http.Request) bool { return r.Header.Get("X-Test-Client") != "" }

// identifyWith is what the registry will do: the first matching profile, else unknown.
func identifyWith(profiles ...core.Profile) func(*http.Request) string {
	return func(r *http.Request) string {
		for _, p := range profiles {
			if p.Match(r) {
				return p.Name()
			}
		}
		return core.ClientUnknown
	}
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

// seen is one request as the fake upstream received it.
type seen struct {
	Method     string
	RequestURI string
	Host       string
	Header     http.Header
	Body       []byte
}

// upstream is a fake upstream on loopback that records every request, body included,
// before it answers with respond (or a plain 200 when respond is nil).
type upstream struct {
	URL *url.URL

	mu   sync.Mutex
	reqs []seen
}

func newUpstream(t *testing.T, respond http.HandlerFunc) *upstream {
	t.Helper()
	u := &upstream{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("upstream: reading body: %v", err)
		}
		u.mu.Lock()
		u.reqs = append(u.reqs, seen{
			Method:     r.Method,
			RequestURI: r.RequestURI,
			Host:       r.Host,
			Header:     r.Header.Clone(),
			Body:       body,
		})
		u.mu.Unlock()
		if respond == nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		respond(w, r)
	}))
	t.Cleanup(srv.Close)
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.URL = base
	return u
}

// only returns the one request upstream saw, failing if it saw any other number.
func (u *upstream) only(t *testing.T) seen {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.reqs) != 1 {
		t.Fatalf("upstream saw %d requests, want 1", len(u.reqs))
	}
	return u.reqs[0]
}

// gateway is server.New with a core.Proxy for the test adapter mounted at its prefix,
// listening on loopback, with its logs in a buffer.
type gateway struct {
	addr string
	url  string
	logs *syncBuffer
}

func startGateway(t *testing.T, base *url.URL, identify func(*http.Request) string) *gateway {
	t.Helper()
	return startGatewayWith(t, upstreamConfig(base), identify)
}

// upstreamConfig is an upstream at base with timeouts no test waits for.
func upstreamConfig(base *url.URL) config.Upstream {
	return config.Upstream{
		BaseURL:               base,
		ConnectTimeout:        5 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

// startGatewayWith is startGateway with the whole upstream config, for the tests
// that need one timeout short.
func startGatewayWith(t *testing.T, up config.Upstream, identify func(*http.Request) string) *gateway {
	t.Helper()
	logs := &syncBuffer{}
	log := logging.New(logs, "debug")
	a := testAdapter{}
	p := core.NewProxy(a, up, identify, log)
	srv := server.New(log, func(r chi.Router) { r.Handle(a.Prefix()+"/*", p) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	addr := ln.Addr().String()
	return &gateway{addr: addr, url: "http://" + addr, logs: logs}
}

// raw sends req byte for byte on a new connection and returns the response with its
// body read. It is how a test sends what a Go client would rewrite (a raw query, TE,
// Connection, no User-Agent).
func (g *gateway) raw(t *testing.T, req string) (*http.Response, []byte) {
	t.Helper()
	return rawAt(t, g.addr, req)
}

// rawAt is raw against any address, so a test can ask upstream directly and compare.
func rawAt(t *testing.T, addr, req string) (*http.Response, []byte) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// A gateway that never answers fails the test instead of hanging it.
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, body
}

// rawRequest builds an HTTP/1.1 request: the request line, Host, then every header in
// h in the order given by names, then the body. A caller with a body puts its
// Content-Length in h.
func rawRequest(method, target, host string, h http.Header, names []string, body string) string {
	var b strings.Builder
	b.WriteString(method + " " + target + " HTTP/1.1\r\n")
	b.WriteString("Host: " + host + "\r\n")
	for _, name := range names {
		for _, v := range h[name] {
			b.WriteString(name + ": " + v + "\r\n")
		}
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}

// accessLine waits for the gateway's request line for path and returns it decoded.
// The line is written after the response, so it is polled for, not assumed.
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

// checkNoLeaksAtEnd registers the plan's leak check. Call it first in a test, so it
// runs after every other cleanup has closed the gateway and the upstreams. It polls
// for up to 2s for the goroutine profile to hold no stack through internal/core (a
// proxy still at work) and no connection handler of the test's own servers.
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

// leakedStacks are the goroutine stacks, other than the caller's, that run proxy
// code or serve a connection with a handler from this test package.
func leakedStacks() []string {
	var buf bytes.Buffer
	_ = pprof.Lookup("goroutine").WriteTo(&buf, 2)
	stacks := strings.Split(buf.String(), "\n\n")
	var leaked []string
	for _, s := range stacks {
		if strings.Contains(s, "core_test.leakedStacks") {
			continue // the goroutine writing the profile: this one
		}
		inProxy := strings.Contains(s, "/internal/core.")
		inTestHandler := strings.Contains(s, "net/http.(*conn).serve") && strings.Contains(s, "/internal/core_test.")
		if inProxy || inTestHandler {
			leaked = append(leaked, s)
		}
	}
	return leaked
}
