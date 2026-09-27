package anthropic_test

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

	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/protocols/anthropic"
	"github.com/brutally-honest/llm-gateway/internal/server"
)

// fixedDate keeps upstream's Date header identical between a direct answer and one
// through the gateway.
const fixedDate = "Sat, 26 Sep 2026 10:00:00 GMT"

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

// upstreamConfig is an upstream at base with timeouts no test waits for.
func upstreamConfig(base *url.URL) config.Upstream {
	return config.Upstream{
		BaseURL:               base,
		ConnectTimeout:        5 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

// gateway is server.New with a core.Registry holding the Anthropic adapter, mounted
// as production mounts it, listening on loopback, with its logs in a buffer.
type gateway struct {
	addr string
	logs *syncBuffer
}

func startGateway(t *testing.T, up config.Upstream) *gateway {
	t.Helper()
	logs := &syncBuffer{}
	log := logging.New(logs, "debug")
	reg := core.NewRegistry(log)
	reg.AddAdapter(anthropic.Adapter{}, up)
	srv := server.New(log, reg.Mount)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &gateway{addr: ln.Addr().String(), logs: logs}
}

// raw sends req byte for byte on a new connection to the gateway and returns the
// response with its body read.
func (g *gateway) raw(t *testing.T, req string) (*http.Response, []byte) {
	t.Helper()
	return rawAt(t, g.addr, req, false)
}

// rawAt sends req byte for byte to addr. head tells the reader the response has no
// body, as it has for a HEAD request.
func rawAt(t *testing.T, addr, req string, head bool) (*http.Response, []byte) {
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
	var forReq *http.Request
	if head {
		forReq = &http.Request{Method: http.MethodHead}
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), forReq)
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

// rawRequest builds an HTTP/1.1 request: the request line, Host, then each header
// line as given (name and value exactly as the client writes them), then the body.
func rawRequest(method, target string, headers []string, body string) string {
	var b strings.Builder
	b.WriteString(method + " " + target + " HTTP/1.1\r\n")
	b.WriteString("Host: gateway.local\r\n")
	for _, h := range headers {
		b.WriteString(h + "\r\n")
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
// proxy still at work) and no connection handler of this test package's servers.
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
	var leaked []string
	for _, s := range strings.Split(buf.String(), "\n\n") {
		if strings.Contains(s, "anthropic_test.leakedStacks") {
			continue // the goroutine writing the profile: this one
		}
		inProxy := strings.Contains(s, "/internal/core.")
		inTestHandler := strings.Contains(s, "net/http.(*conn).serve") &&
			strings.Contains(s, "/internal/protocols/anthropic_test.")
		if inProxy || inTestHandler {
			leaked = append(leaked, s)
		}
	}
	return leaked
}

// hangingUpstream is an http URL on loopback that accepts connections, reads what
// the gateway sends and never answers. Its connections close when the test ends.
func hangingUpstream(t *testing.T) *url.URL {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		conns []net.Conn
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return &url.URL{Scheme: "http", Host: ln.Addr().String()}
}

// refusedURL is an http URL on loopback where nothing listens.
func refusedURL(t *testing.T) *url.URL {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return &url.URL{Scheme: "http", Host: addr}
}
