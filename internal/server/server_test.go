package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/logging"
)

// sentinel stands in for a secret. It must never reach the logs.
const sentinel = "s3ntinel-v4lue"

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

// harness is a Server listening on 127.0.0.1 with its logs in a buffer.
type harness struct {
	t    *testing.T
	srv  *Server
	url  string
	logs *syncBuffer
}

func start(t *testing.T, mount ...func(chi.Router)) *harness {
	t.Helper()
	logs := &syncBuffer{}
	srv := New(logging.New(logs, "debug"), mount...)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &harness{t: t, srv: srv, url: "http://" + ln.Addr().String(), logs: logs}
}

// do sends a request and returns the response with its body read.
func (h *harness) do(req *http.Request) (*http.Response, string) {
	h.t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp, string(body)
}

func (h *harness) get(path string) (*http.Response, string) {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodGet, h.url+path, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.do(req)
}

// lines parses every log line as JSON, failing the test on any that isn't.
func (h *harness) lines() []map[string]any {
	h.t.Helper()
	var parsed []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(h.logs.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			h.t.Fatalf("log line is not JSON: %q", line)
		}
		parsed = append(parsed, m)
	}
	return parsed
}

// waitLines waits for n lines with msg: the access line is written after the
// handler returns, which can be after the client has its response.
func (h *harness) waitLines(msg string, n int) []map[string]any {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var found []map[string]any
		for _, l := range h.lines() {
			if l["msg"] == msg {
				found = append(found, l)
			}
		}
		if len(found) >= n || time.Now().After(deadline) {
			if len(found) != n {
				h.t.Fatalf("got %d %q lines, want %d\n%s", len(found), msg, n, h.logs.String())
			}
			return found
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitInFlight waits until InFlight reports want.
func (h *harness) waitInFlight(want int64) {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for h.srv.InFlight() != want {
		if time.Now().After(deadline) {
			h.t.Fatalf("InFlight() = %d, want %d", h.srv.InFlight(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// AC2, automated half.
func TestHealthz(t *testing.T) {
	h := start(t)
	resp, body := h.get("/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if body != `{"status":"ok"}` {
		t.Errorf("body = %q", body)
	}
}

// AC18: every response has a server-generated ID; an incoming one is ignored for the
// ID but left in the request's headers.
func TestRequestID(t *testing.T) {
	var (
		mu      sync.Mutex
		seenCtx []string
		seenHdr []string
	)
	h := start(t, func(r chi.Router) {
		r.Get("/echo", func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seenCtx = append(seenCtx, RequestID(r.Context()))
			seenHdr = append(seenHdr, r.Header.Get(requestIDHeader))
			mu.Unlock()
		})
	})

	var ids []string
	for range 2 {
		req, err := http.NewRequest(http.MethodGet, h.url+"/echo", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(requestIDHeader, "client-chosen-id")
		resp, _ := h.do(req)
		ids = append(ids, resp.Header.Get(requestIDHeader))
	}

	for _, id := range ids {
		if len(id) != 26 || id == "client-chosen-id" {
			t.Errorf("X-Request-Id = %q, want a 26-character server ID", id)
		}
	}
	if ids[0] == ids[1] {
		t.Errorf("two requests got the same ID %q", ids[0])
	}
	if !slices.Equal(seenCtx, ids) {
		t.Errorf("RequestID(ctx) = %v, want the response IDs %v", seenCtx, ids)
	}
	if !slices.Equal(seenHdr, []string{"client-chosen-id", "client-chosen-id"}) {
		t.Errorf("incoming header seen by the handler = %v, want it untouched", seenHdr)
	}
}

// The access line has exactly its fields, logs r.URL.Path only, and carries nothing
// from the query, the headers or the body.
func TestAccessLog_PathOnly(t *testing.T) {
	h := start(t)
	req, err := http.NewRequest(http.MethodGet, h.url+"/healthz?key="+sentinel, strings.NewReader(sentinel))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+sentinel)
	req.Header.Set("x-api-key", sentinel)
	resp, _ := h.do(req)

	line := h.waitLines("request", 1)[0]
	if strings.Contains(h.logs.String(), sentinel) {
		t.Errorf("logs contain the sentinel\n%s", h.logs.String())
	}
	want := []string{"bytes", "caller", "duration_ms", "level", "method", "msg", "path", "remote_addr", "request_id", "status", "ts"}
	var got []string
	for k := range line {
		got = append(got, k)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("access line keys = %v, want %v", got, want)
	}
	checks := map[string]any{
		"level": "info", "method": "GET", "path": "/healthz",
		"status": float64(200), "bytes": float64(len(`{"status":"ok"}`)),
		"request_id": resp.Header.Get(requestIDHeader),
	}
	for k, v := range checks {
		if line[k] != v {
			t.Errorf("%s = %v, want %v", k, line[k], v)
		}
	}
}

// AC17's server half: a panic after the response has started keeps that response,
// not a 500, and still logs one line.
func TestRecoverer_HeadersAlreadyWritten(t *testing.T) {
	h := start(t, func(r chi.Router) {
		r.Get("/late-panic", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte("partial"))
			panic("late")
		})
	})
	resp, body := h.get("/late-panic")
	if resp.StatusCode != http.StatusAccepted || body != "partial" {
		t.Errorf("got %d %q, want 202 \"partial\"", resp.StatusCode, body)
	}
	line := h.waitLines("panic recovered", 1)[0]
	if line["level"] != "error" || line["panic_type"] != "string" || line["request_id"] != resp.Header.Get(requestIDHeader) {
		t.Errorf("panic line = %v", line)
	}
	if stack, _ := line["stack"].(string); !strings.Contains(stack, "goroutine") {
		t.Errorf("stack field = %q, want a goroutine trace", stack)
	}
	if _, ok := line["panic"]; ok {
		t.Errorf("panic line carries a panic field: %v", line)
	}
}

// AC41: the panic value can hold request data, so only its type and a stack are logged.
func TestRecoverer_LogsPanicTypeNotValue(t *testing.T) {
	const secret = "PANICVALUE-sk-ant-7f3a"
	h := start(t, func(r chi.Router) {
		r.Get("/panic", func(http.ResponseWriter, *http.Request) { panic(secret) })
		r.Get("/panic-err", func(http.ResponseWriter, *http.Request) { panic(errors.New(secret)) })
	})
	for _, path := range []string{"/panic", "/panic-err"} {
		if resp, body := h.get(path); resp.StatusCode != http.StatusInternalServerError || strings.Contains(body, secret) {
			t.Errorf("%s: got %d %q", path, resp.StatusCode, body)
		}
	}
	lines := h.waitLines("panic recovered", 2)
	if lines[0]["panic_type"] != "string" || lines[1]["panic_type"] != "*errors.errorString" {
		t.Errorf("panic_type = %v, %v", lines[0]["panic_type"], lines[1]["panic_type"])
	}
	for _, l := range lines {
		if stack, _ := l["stack"].(string); !strings.Contains(stack, "goroutine") {
			t.Errorf("stack = %q, want a goroutine trace", stack)
		}
		if _, ok := l["panic"]; ok {
			t.Errorf("line has a panic field: %v", l)
		}
	}
	if strings.Contains(h.logs.String(), secret) {
		t.Errorf("secret appears in the log output:\n%s", h.logs.String())
	}
}

// A panic before anything is written gives a 500 and one line, and the server keeps
// serving. inflight is outermost, so the panicking request is counted and released.
func TestRecoverer_PanicGives500(t *testing.T) {
	h := start(t, func(r chi.Router) {
		r.Get("/panic", func(http.ResponseWriter, *http.Request) { panic("boom") })
	})
	if resp, _ := h.get("/panic"); resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	h.waitLines("panic recovered", 1)
	h.waitInFlight(0)
	if resp, _ := h.get("/healthz"); resp.StatusCode != http.StatusOK {
		t.Errorf("healthz after panic: status = %d, want 200", resp.StatusCode)
	}
	// The access line still records the 500: recoverer runs inside accessLog.
	for _, l := range h.waitLines("request", 2) {
		if l["path"] == "/panic" && l["status"] != float64(500) {
			t.Errorf("access line for /panic has status %v, want 500", l["status"])
		}
	}
}

// http.ErrAbortHandler is re-panicked: net/http drops the connection without a
// response, recoverer logs nothing (the access line is the only line, Q8), and the
// request still leaves the in-flight count.
func TestRecoverer_AbortHandler(t *testing.T) {
	h := start(t, func(r chi.Router) {
		r.Get("/abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	})
	resp, err := http.Get(h.url + "/abort")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("got a response (%d), want the connection aborted", resp.StatusCode)
	}
	h.waitInFlight(0)
	h.waitLines("request", 1)
	for _, l := range h.lines() {
		if l["msg"] != "request" {
			t.Errorf("abort logged more than its access line: %v\n%s", l, h.logs.String())
		}
	}
}

// InFlight counts handlers still running.
func TestInFlight(t *testing.T) {
	release := make(chan struct{})
	h := start(t, func(r chi.Router) {
		r.Get("/hang", func(http.ResponseWriter, *http.Request) { <-release })
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		if resp, err := http.Get(h.url + "/hang"); err == nil {
			_ = resp.Body.Close()
		}
	}()
	h.waitInFlight(1)
	close(release)
	<-done
	h.waitInFlight(0)
}

// net/http's own messages go through zap as JSON warn lines from the "http" logger.
func TestErrorLog(t *testing.T) {
	h := start(t)
	h.srv.srv.ErrorLog.Printf("http: Accept error: %s", "too many open files")
	lines := h.lines()
	if len(lines) != 1 || lines[0]["level"] != "warn" || lines[0]["logger"] != "http" {
		t.Errorf("ErrorLog wrote %v, want one warn line from the http logger", lines)
	}
}
