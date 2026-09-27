package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// baseKeys is the 000 access line: what every request line has.
var baseKeys = []string{"bytes", "caller", "duration_ms", "level", "method", "msg", "path", "remote_addr", "request_id", "status", "ts"}

// proxyKeys are the fields a line gains whenever Meta.Protocol is set.
var proxyKeys = []string{"auth", "client", "protocol", "stream"}

func keysOf(line map[string]any) []string {
	var got []string
	for k := range line {
		got = append(got, k)
	}
	slices.Sort(got)
	return got
}

func sorted(parts ...[]string) []string {
	out := slices.Concat(parts...)
	slices.Sort(out)
	return out
}

// fillMeta is a test handler that sets Meta the way the proxy would, then writes 200.
func fillMeta(fill func(*core.Meta)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m := core.MetaFrom(r.Context())
		if m == nil {
			http.Error(w, "no Meta in the request context", http.StatusInternalServerError)
			return
		}
		fill(m)
		w.WriteHeader(http.StatusOK)
	}
}

func lineFor(t *testing.T, lines []map[string]any, path string) map[string]any {
	t.Helper()
	for _, l := range lines {
		if l["path"] == path {
			return l
		}
	}
	t.Fatalf("no access line for %s in %v", path, lines)
	return nil
}

// AC36: the proxy fields appear only on lines whose Meta has a Protocol. A line
// without one (/healthz, a 404, a handler that set other fields but no Protocol)
// keeps exactly the 000 keys.
func TestAccessLog_ProxyFieldsOnlyWhenProtocolSet(t *testing.T) {
	h := start(t, func(r chi.Router) {
		r.Get("/proxied", fillMeta(func(m *core.Meta) {
			m.Protocol = "test"
			m.Client = core.ClientUnknown
			m.Auth = core.AuthAPIKey
			m.Stream = true
		}))
		r.Get("/no-protocol", fillMeta(func(m *core.Meta) {
			m.Client = "someone"
			m.Auth = core.AuthBearer
			m.Stream = true
			m.TTFB, m.HasTTFB = time.Millisecond, true
			m.GatewayError = "timeout"
			m.ClientDisconnected = true
			m.UpstreamAborted = true
		}))
	})
	for _, p := range []string{"/proxied", "/no-protocol", "/healthz", "/nowhere"} {
		h.get(p)
	}
	lines := h.waitLines("request", 4)

	proxied := lineFor(t, lines, "/proxied")
	if got, want := keysOf(proxied), sorted(baseKeys, proxyKeys); !slices.Equal(got, want) {
		t.Errorf("/proxied keys = %v, want %v", got, want)
	}
	for k, v := range map[string]any{"protocol": "test", "client": "unknown", "auth": "api_key", "stream": true} {
		if proxied[k] != v {
			t.Errorf("/proxied %s = %v, want %v", k, proxied[k], v)
		}
	}
	for _, p := range []string{"/no-protocol", "/healthz", "/nowhere"} {
		if got := keysOf(lineFor(t, lines, p)); !slices.Equal(got, baseKeys) {
			t.Errorf("%s keys = %v, want the 000 keys %v", p, got, baseKeys)
		}
	}
}

// AC36, AC38: ttfb_ms only once headers arrived; gateway_error, client_disconnected
// and upstream_aborted only when set or true. stream is always there, false included.
func TestAccessLog_OptionalFieldsOnlyWhenSet(t *testing.T) {
	cases := []struct {
		name  string
		fill  func(*core.Meta)
		extra map[string]any
	}{
		{"none", func(*core.Meta) {}, map[string]any{}},
		{"ttfb", func(m *core.Meta) { m.TTFB, m.HasTTFB = 1500*time.Microsecond, true }, map[string]any{"ttfb_ms": 1.5}},
		{"ttfb-zero", func(m *core.Meta) { m.HasTTFB = true }, map[string]any{"ttfb_ms": float64(0)}},
		{"ttfb-not-arrived", func(m *core.Meta) { m.TTFB = time.Second }, map[string]any{}},
		{"gateway-error", func(m *core.Meta) { m.GatewayError = "timeout" }, map[string]any{"gateway_error": "timeout"}},
		{"client-disconnected", func(m *core.Meta) { m.ClientDisconnected = true }, map[string]any{"client_disconnected": true}},
		{"upstream-aborted", func(m *core.Meta) { m.UpstreamAborted = true }, map[string]any{"upstream_aborted": true}},
	}
	h := start(t, func(r chi.Router) {
		for _, c := range cases {
			r.Get("/"+c.name, fillMeta(func(m *core.Meta) {
				m.Protocol = "test"
				m.Client = core.ClientUnknown
				m.Auth = core.AuthNone
				c.fill(m)
			}))
		}
	})
	for _, c := range cases {
		h.get("/" + c.name)
	}
	lines := h.waitLines("request", len(cases))
	for _, c := range cases {
		line := lineFor(t, lines, "/"+c.name)
		var extraKeys []string
		for k := range c.extra {
			extraKeys = append(extraKeys, k)
		}
		if got, want := keysOf(line), sorted(baseKeys, proxyKeys, extraKeys); !slices.Equal(got, want) {
			t.Errorf("%s keys = %v, want %v", c.name, got, want)
		}
		for k, v := range c.extra {
			if line[k] != v {
				t.Errorf("%s: %s = %v, want %v", c.name, k, line[k], v)
			}
		}
		if line["stream"] != false || line["auth"] != "none" {
			t.Errorf("%s: stream = %v, auth = %v, want false, none", c.name, line["stream"], line["auth"])
		}
	}
}

// AC30, AC48 groundwork: a handler that ends in http.ErrAbortHandler, the proxy's
// only abort, still writes its line, and the client still sees a dropped connection.
// A client that left before the abort is marked client_disconnected by Settle.
func TestAccessLog_LogsWhenHandlerAborts(t *testing.T) {
	left := make(chan struct{})
	h := start(t, func(r chi.Router) {
		r.Get("/abort", func(w http.ResponseWriter, r *http.Request) {
			core.MetaFrom(r.Context()).Protocol = "test"
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("partial"))
			w.(http.Flusher).Flush()
			panic(http.ErrAbortHandler)
		})
		r.Get("/client-left", func(w http.ResponseWriter, r *http.Request) {
			core.MetaFrom(r.Context()).Protocol = "test"
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			close(left)
			panic(http.ErrAbortHandler)
		})
	})

	// Upstream-style abort: the client is still there and sees the body cut short.
	resp, err := http.Get(h.url + "/abort")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Errorf("read the whole body, want the connection dropped")
	}
	_ = resp.Body.Close()

	// A client that leaves mid-response.
	conn, err := net.Dial("tcp", strings.TrimPrefix(h.url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(conn, "GET /client-left HTTP/1.1\r\nHost: x\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	select {
	case <-left:
	case <-time.After(5 * time.Second):
		t.Fatal("the /client-left handler never saw the client leave")
	}

	lines := h.waitLines("request", 2)
	h.waitInFlight(0)

	abort := lineFor(t, lines, "/abort")
	if got, want := keysOf(abort), sorted(baseKeys, proxyKeys); !slices.Equal(got, want) {
		t.Errorf("/abort keys = %v, want %v", got, want)
	}
	if abort["status"] != float64(200) || abort["protocol"] != "test" {
		t.Errorf("/abort line = %v, want status 200 and protocol test", abort)
	}

	gone := lineFor(t, lines, "/client-left")
	if gone["client_disconnected"] != true {
		t.Errorf("/client-left line = %v, want client_disconnected true", gone)
	}
	if _, ok := gone["upstream_aborted"]; ok {
		t.Errorf("/client-left line has upstream_aborted: %v", gone)
	}
	for _, l := range h.lines() {
		if l["msg"] != "request" {
			t.Errorf("unexpected non-access line: %v", l)
		}
	}
}
