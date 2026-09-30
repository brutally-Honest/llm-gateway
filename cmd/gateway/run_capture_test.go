package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// jsonUpstream answers every request with a small JSON body.
func jsonUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	return newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"type":"message"}`)
	})
}

// captureEnv is servedWith's env for a gateway capturing into dir against up.
func captureEnv(dir string, up *fakeUpstream, extra map[string]string) map[string]string {
	env := map[string]string{
		"GATEWAY_CAPTURE_DIR":                  dir,
		"GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL": up.URL,
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// requestIDs returns the request IDs of the access lines for path.
func (g *gateway) requestIDs(path string) []string {
	var ids []string
	for _, l := range g.requestLines(path) {
		id, _ := l["request_id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// waitQueued waits until n access lines for path say capture: queued.
func (g *gateway) waitQueued(path string, n int) {
	g.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c := 0
		for _, l := range g.requestLines(path) {
			if l["capture"] == core.CaptureQueued {
				c++
			}
		}
		if c == n {
			return
		}
		if c > n || time.Now().After(deadline) {
			g.t.Fatalf("got %d queued %s lines, want %d\n%s", c, path, n, g.stdout.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// openReader opens the store in dir read-only, closed at the end of the test.
func openReader(t *testing.T, dir string) *store.Reader {
	t.Helper()
	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// stoppedLine returns the one capture stopped line.
func stoppedLine(t *testing.T, g *gateway) map[string]any {
	t.Helper()
	lines := linesWith(g, "capture stopped")
	if len(lines) != 1 {
		t.Fatalf("got %d capture stopped lines, want 1\n%s", len(lines), g.stdout.String())
	}
	return lines[0]
}

// checkNoCaptureLeaks fails the test if, once run has returned, a goroutine through
// internal/capture or internal/store is still running (a worker Close left behind).
func checkNoCaptureLeaks(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var buf bytes.Buffer
		_ = pprof.Lookup("goroutine").WriteTo(&buf, 2)
		var leaked []string
		for _, s := range strings.Split(buf.String(), "\n\n") {
			if strings.Contains(s, "/internal/capture.") || strings.Contains(s, "/internal/store.") {
				leaked = append(leaked, s)
			}
		}
		if len(leaked) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%d goroutine(s) left behind:\n\n%s", len(leaked), strings.Join(leaked, "\n\n"))
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// AC7: capture.enabled false means no store: nothing under capture.dir, and every
// access line says capture: off.
func TestCapture_DisabledIsPassthrough(t *testing.T) {
	t.Run("dir_left_empty", func(t *testing.T) {
		dir := t.TempDir()
		up := jsonUpstream(t)
		g, url := servedWith(t, captureEnv(dir, up, map[string]string{"GATEWAY_CAPTURE_ENABLED": "false"}))
		if code, err := do(t, http.MethodPost, url+"/anthropic/v1/messages", nil, `{}`); err != nil || code != http.StatusOK {
			t.Fatalf("status %d, err %v", code, err)
		}
		g.waitCount("request", 1)
		if code := g.stop(); code != exitOK {
			t.Errorf("exit code = %d, want 0", code)
		}
		if l := g.requestLines("/anthropic/v1/messages"); len(l) != 1 || l[0]["capture"] != core.CaptureOff {
			t.Errorf("request lines = %v, want one with capture off", l)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Errorf("capture.dir holds %v (%v), want nothing", entries, err)
		}
		if n := len(linesWith(g, "capture stopped")); n != 0 {
			t.Errorf("got %d capture stopped lines, want none with capture off", n)
		}
	})
	// With capture off, the default location is never resolved, so a machine with
	// no HOME still starts.
	t.Run("default_dir_not_resolved", func(t *testing.T) {
		inTempDir(t, nil)
		g := startGateway(t, options{env: map[string]string{
			"GATEWAY_CAPTURE_ENABLED": "false",
			"XDG_DATA_HOME":           "relative",
		}})
		g.waitLine("gateway started")
		if code := g.stop(); code != exitOK {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
}

// AC16: a capture.dir the gateway can't create fails startup before bind, with one
// line naming the path and a fixed reason, and exit 1. Nothing here depends on the
// user, so root proves it too (research Q17).
func TestStore_OpenFailsFast(t *testing.T) {
	// The parent is a regular file, which no user can create a directory under.
	t.Run("uncreatable_dir", func(t *testing.T) {
		inTempDir(t, nil)
		parent := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(parent, "store")

		g := startGateway(t, options{env: map[string]string{"GATEWAY_CAPTURE_DIR": dir}})
		line := failsBeforeBind(t, g, exitRuntime)
		if line["level"] != "error" || line["msg"] != "cannot open store" || line["key"] != "capture.dir" ||
			line["path"] != dir || line["reason"] != "not a directory" {
			t.Errorf("line = %v, want cannot open store for %s with reason not a directory", line, dir)
		}
		if _, err := os.Stat(dir); !errors.Is(err, syscall.ENOTDIR) {
			t.Errorf("stat %s: %v, want it never created", dir, err)
		}
	})
	// Each open error maps to its fixed reason and anything else to a generic one, so
	// no driver text reaches the log. The permission errors are built, since root
	// ignores directory permissions.
	t.Run("fixed_reasons", func(t *testing.T) {
		wrap := func(err error) error {
			return fmt.Errorf("open store: %w", &fs.PathError{Op: "mkdir", Path: "/x", Err: err})
		}
		for _, tc := range []struct {
			err  error
			want string
		}{
			{wrap(syscall.EACCES), "permission denied"},
			{wrap(syscall.EPERM), "permission denied"},
			{wrap(syscall.EROFS), "read-only file system"},
			{wrap(syscall.ENOSPC), "no space left on device"},
			{wrap(syscall.ENOTDIR), "not a directory"},
			{errors.New("sqlite: SQL logic error: driver text"), "cannot open or migrate"},
		} {
			if got := storeOpenReason(tc.err); got != tc.want {
				t.Errorf("storeOpenReason(%v) = %q, want %q", tc.err, got, tc.want)
			}
		}
	})
}

// With capture on and neither capture.dir, an absolute XDG_DATA_HOME nor an absolute
// HOME, there is no directory to capture into: invalid value for capture.dir from
// source default, exit 2, nothing bound.
func TestRun_DefaultCaptureDirUnresolvable(t *testing.T) {
	inTempDir(t, nil)
	g := startGateway(t, options{env: map[string]string{"XDG_DATA_HOME": "relative", "HOME": "also-relative"}})
	line := failsBeforeBind(t, g, exitConfig)
	if line["msg"] != "invalid config" || line["key"] != "capture.dir" || line["source"] != "default" ||
		line["reason"] != "invalid value" {
		t.Errorf("line = %v, want invalid value for capture.dir from default", line)
	}
}

// AC22: every stored exchange and event carries principal_id local. The exchange half
// is proved here; the event half needs the Anthropic parser, so the messages exchange
// is pinned at parse skipped with no events until T20 flips that pin to require
// events (research Q16).
func TestCapture_PrincipalLocal(t *testing.T) {
	dir := t.TempDir()
	up := jsonUpstream(t)
	g, url := servedWith(t, captureEnv(dir, up, nil))
	// One user message: once T20 parses requests, this gives at least one event.
	body := `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
	if code, err := do(t, http.MethodPost, url+"/anthropic/v1/messages", map[string]string{"x-api-key": "k"}, body); err != nil || code != http.StatusOK {
		t.Fatalf("messages: status %d, err %v", code, err)
	}
	if code, err := do(t, http.MethodGet, url+"/anthropic/v1/models", nil, ""); err != nil || code != http.StatusOK {
		t.Fatalf("models: status %d, err %v", code, err)
	}
	g.waitQueued("/anthropic/v1/messages", 1)
	g.waitQueued("/anthropic/v1/models", 1)
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	checkNoCaptureLeaks(t)

	r := openReader(t, dir)
	for _, path := range []string{"/anthropic/v1/messages", "/anthropic/v1/models"} {
		ids := g.requestIDs(path)
		if len(ids) != 1 {
			t.Fatalf("got %d %s request lines, want 1", len(ids), path)
		}
		id := ids[0]
		ex, err := r.Exchange(id)
		if err != nil {
			t.Fatalf("exchange %s: %v", id, err)
		}
		if ex.PrincipalID != core.PrincipalLocal {
			t.Errorf("exchange %s principal_id = %q, want local", id, ex.PrincipalID)
		}
		events, err := r.Events(id)
		if err != nil {
			t.Fatalf("events %s: %v", id, err)
		}
		// No parser yet, so neither exchange has events. T20 replaces this for the
		// messages exchange with parse ok and at least one event.
		if ex.Parse != string(core.ParseSkipped) || len(events) != 0 {
			t.Errorf("%s: parse %q with %d events, want skipped with none until the parser lands (T20)",
				path, ex.Parse, len(events))
		}
		for _, ev := range events {
			if ev.PrincipalID != core.PrincipalLocal {
				t.Errorf("event %s/%d principal_id = %q, want local", id, ev.Seq, ev.PrincipalID)
			}
		}
	}
}

// slowStore is the real store with every SaveExchange held up by delay, or, with
// delay 0, held until its context is cancelled.
type slowStore struct {
	capture.Store
	delay   time.Duration
	entered chan struct{}
}

func (s *slowStore) SaveExchange(ctx context.Context, ex *core.Exchange) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	if s.delay == 0 {
		<-ctx.Done()
		return ctx.Err()
	}
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.Store.SaveExchange(ctx, ex)
}

// openSlow is a deps.openStore that wraps the real store in a slowStore.
func openSlow(delay time.Duration, entered chan struct{}) func(string, *zap.Logger) (capture.Store, error) {
	return func(dir string, log *zap.Logger) (capture.Store, error) {
		st, err := store.Open(dir, log)
		if err != nil {
			return nil, err
		}
		return &slowStore{Store: st, delay: delay, entered: entered}, nil
	}
}

// startCapturing is servedWith plus deps.openStore.
func startCapturing(t *testing.T, env map[string]string, open func(string, *zap.Logger) (capture.Store, error)) (*gateway, string) {
	t.Helper()
	inTempDir(t, nil)
	all := map[string]string{"GATEWAY_LISTEN_ADDR": "127.0.0.1:0", "GATEWAY_LOG_LEVEL": "debug"}
	for k, v := range env {
		all[k] = v
	}
	g := startGateway(t, options{env: all, realListen: true, openStore: open})
	addr, _ := g.waitLine("gateway started")["addr"].(string)
	return g, "http://" + addr
}

// AC26: shutdown closes the server, then drains the queue within what is left of
// shutdown_timeout, then closes the store, and counts what it could not drain.
func TestCapture_ShutdownDrainsQueue(t *testing.T) {
	const path = "/anthropic/v1/messages"

	t.Run("queued_stored", func(t *testing.T) {
		dir := t.TempDir()
		up := jsonUpstream(t)
		// One worker at 50ms an exchange: most of the five are still queued at stop.
		g, url := startCapturing(t, captureEnv(dir, up, map[string]string{"GATEWAY_CAPTURE_WORKERS": "1"}),
			openSlow(50*time.Millisecond, make(chan struct{}, 1)))
		const n = 5
		for range n {
			if code, err := do(t, http.MethodPost, url+path, nil, `{"hello":"world"}`); err != nil || code != http.StatusOK {
				t.Fatalf("status %d, err %v", code, err)
			}
		}
		g.waitQueued(path, n)
		if code := g.stop(); code != exitOK {
			t.Fatalf("exit code = %d, want 0", code)
		}
		checkNoCaptureLeaks(t)

		r := openReader(t, dir)
		for _, id := range g.requestIDs(path) {
			if _, err := r.Exchange(id); err != nil {
				t.Errorf("exchange %s not stored: %v", id, err)
			}
		}
		line := stoppedLine(t, g)
		if line["level"] != "info" || line["undrained"] != float64(0) || line["dropped_queue_full"] != float64(0) ||
			line["store_failed"] != float64(0) || line["parse_failed"] != float64(0) || line["dropped_memory"] != float64(0) {
			t.Errorf("capture stopped = %v, want nothing undrained, dropped or failed", line)
		}
		if peak, _ := line["memory_peak_bytes"].(float64); peak <= 0 {
			t.Errorf("memory_peak_bytes = %v, want above zero after a captured request", line["memory_peak_bytes"])
		}
		// The store closes before the last line, and gateway stopped comes after it.
		lines := g.lines()
		if last := lines[len(lines)-1]; last["msg"] != "gateway stopped" {
			t.Errorf("last line = %v, want gateway stopped", last)
		}
	})

	t.Run("stream_ends_during_server_shutdown", func(t *testing.T) {
		dir := t.TempDir()
		release := make(chan struct{})
		var once sync.Once
		end := func() { once.Do(func() { close(release) }) }
		up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: first\ndata: {}\n\n")
			w.(http.Flusher).Flush()
			<-release
			_, _ = io.WriteString(w, "event: last\ndata: {}\n\n")
		})
		t.Cleanup(end)
		g, url := servedWith(t, captureEnv(dir, up, nil))
		addr := strings.TrimPrefix(url, "http://")

		resp, err := http.Post(url+path, "application/json", strings.NewReader(`{"stream":true}`))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		first := "event: first\ndata: {}\n\n"
		buf := make([]byte, len(first))
		if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != first {
			t.Fatalf("first event = %q, %v", buf, err)
		}

		// Shutdown starts, and the stream ends once the listener is closed.
		g.cancel()
		deadline := time.Now().Add(5 * time.Second)
		for {
			c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
			if err != nil {
				break
			}
			_ = c.Close()
			if time.Now().After(deadline) {
				t.Fatal("the listener is still open after cancel")
			}
			time.Sleep(5 * time.Millisecond)
		}
		end()
		if _, err := io.ReadAll(resp.Body); err != nil {
			t.Fatalf("rest of stream: %v", err)
		}
		if code := g.wait(); code != exitOK {
			t.Fatalf("exit code = %d, want 0", code)
		}
		checkNoCaptureLeaks(t)

		ids := g.requestIDs(path)
		if len(ids) != 1 {
			t.Fatalf("got %d request lines, want 1", len(ids))
		}
		ex, err := openReader(t, dir).Exchange(ids[0])
		if err != nil {
			t.Fatalf("the stream's exchange was not stored: %v", err)
		}
		if !ex.Stream || ex.Status != http.StatusOK {
			t.Errorf("exchange stream = %v, status = %d; want a streamed 200", ex.Stream, ex.Status)
		}
		if line := stoppedLine(t, g); line["undrained"] != float64(0) {
			t.Errorf("capture stopped = %v, want undrained 0", line)
		}
	})

	t.Run("undrained_counted", func(t *testing.T) {
		dir := t.TempDir()
		up := jsonUpstream(t)
		entered := make(chan struct{}, 1)
		// One worker stuck on its first exchange until shutdown runs out of time.
		g, url := startCapturing(t, captureEnv(dir, up, map[string]string{
			"GATEWAY_CAPTURE_WORKERS":  "1",
			"GATEWAY_SHUTDOWN_TIMEOUT": "100ms",
		}), openSlow(0, entered))
		const n = 3
		for range n {
			if code, err := do(t, http.MethodPost, url+path, nil, `{}`); err != nil || code != http.StatusOK {
				t.Fatalf("status %d, err %v", code, err)
			}
		}
		g.waitQueued(path, n)
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("the worker never reached the store")
		}

		start := time.Now()
		if code := g.stop(); code != exitOK {
			t.Errorf("exit code = %d, want 0", code)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("shutdown took %v, want about 100ms", took)
		}
		checkNoCaptureLeaks(t)

		// The one in the store call fails on the cancel; the other two are undrained.
		line := stoppedLine(t, g)
		if line["undrained"] != float64(n-1) || line["store_failed"] != float64(1) {
			t.Errorf("capture stopped = %v, want undrained %d and store_failed 1", line, n-1)
		}
		if _, ok := line["dropped_memory"]; !ok {
			t.Errorf("capture stopped = %v, want dropped_memory", line)
		}
		if _, ok := line["memory_peak_bytes"]; !ok {
			t.Errorf("capture stopped = %v, want memory_peak_bytes", line)
		}
	})
}

// bodyUpstream is a loopback upstream that keeps every request body it reads in full
// and answers with handler. Unlike fakeUpstream, the body is handed on, so a test can
// compare the bytes upstream got with the stored copy.
type bodyUpstream struct {
	*httptest.Server
	mu     sync.Mutex
	bodies [][]byte
}

func newBodyUpstream(t *testing.T, handler http.HandlerFunc) *bodyUpstream {
	t.Helper()
	u := &bodyUpstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.bodies = append(u.bodies, b)
		u.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(u.Close)
	return u
}

// received returns the one request body upstream read.
func (u *bodyUpstream) received(t *testing.T) []byte {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.bodies) != 1 {
		t.Fatalf("upstream got %d requests, want 1", len(u.bodies))
	}
	return u.bodies[0]
}

// servedAgainst is servedWith capturing into dir, with upstream at base.
func servedAgainst(t *testing.T, dir, base string, extra map[string]string) (*gateway, string) {
	t.Helper()
	env := map[string]string{
		"GATEWAY_CAPTURE_DIR":                  dir,
		"GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL": base,
	}
	for k, v := range extra {
		env[k] = v
	}
	return servedWith(t, env)
}

// storedExchange waits for path's one access line to say queued, stops the gateway
// (which drains the queue), runs the leak check, and returns the stored exchange with
// a reader over the store.
func storedExchange(t *testing.T, g *gateway, dir, path string) (store.ExchangeRow, *store.Reader) {
	t.Helper()
	g.waitQueued(path, 1)
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	checkNoCaptureLeaks(t)
	ids := g.requestIDs(path)
	if len(ids) != 1 {
		t.Fatalf("got %d %s request lines, want 1", len(ids), path)
	}
	r := openReader(t, dir)
	ex, err := r.Exchange(ids[0])
	if err != nil {
		t.Fatalf("exchange %s not stored: %v", ids[0], err)
	}
	return ex, r
}

// storedBody reads a stored body back by its hash; "" is no body.
func storedBody(t *testing.T, r *store.Reader, hash string) []byte {
	t.Helper()
	if hash == "" {
		return nil
	}
	b, err := r.Content(hash)
	if err != nil {
		t.Fatalf("content %s: %v", hash, err)
	}
	return b
}

// abortFlags is the stored exchange's record of how it ended.
type abortFlags struct {
	incomplete, disconnected, aborted bool
	gatewayError                      string
}

func (f abortFlags) of(ex store.ExchangeRow) abortFlags {
	return abortFlags{ex.RequestIncomplete, ex.ClientDisconnected, ex.UpstreamAborted, ex.GatewayError}
}

// AC9: through run, with a real store, each exchange is stored with every field the
// spec's Exchange record lists, and both bodies read back byte-identical to the wire.
func TestCapture_ExchangeStored(t *testing.T) {
	const (
		path      = "/anthropic/v1/messages"
		query     = "beta=true&x=a%20b"
		reqBody   = `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`
		userAgent = "claude-cli/1.0.0 (external, cli)"
	)
	// send posts reqBody the way Claude Code would and returns the response and
	// every byte of its body.
	send := func(t *testing.T, url string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, url+path+"?"+query, strings.NewReader(reqBody))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Authorization", "Bearer sekrit-bearer")
		req.Header.Set("X-Custom", "kept")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return res, body
	}
	check := func(t *testing.T, ex store.ExchangeRow, r *store.Reader, res *http.Response, upBody, clientBody []byte,
		before, after time.Time, wantStatus int, wantStream bool) {
		t.Helper()
		if ex.RequestID != res.Header.Get("X-Request-Id") {
			t.Errorf("request_id = %q, want the gateway's %q", ex.RequestID, res.Header.Get("X-Request-Id"))
		}
		if ex.PrincipalID != core.PrincipalLocal || ex.Protocol != "anthropic" || ex.Client != "claude-code" ||
			ex.Auth != string(core.AuthBearer) {
			t.Errorf("principal, protocol, client, auth = %q, %q, %q, %q; want local, anthropic, claude-code, bearer",
				ex.PrincipalID, ex.Protocol, ex.Client, ex.Auth)
		}
		if ex.Method != http.MethodPost || ex.Path != "/v1/messages" || ex.Query != query {
			t.Errorf("method, path, query = %q, %q, %q; want POST, /v1/messages, %q", ex.Method, ex.Path, ex.Query, query)
		}
		if got := ex.RequestHeaders.Values("Authorization"); !slices.Equal(got, []string{core.Redacted}) {
			t.Errorf("request Authorization = %q, want [REDACTED]", got)
		}
		if ex.RequestHeaders.Get("X-Custom") != "kept" || ex.RequestHeaders.Get("User-Agent") != userAgent {
			t.Errorf("request headers = %v, want X-Custom and User-Agent as sent", ex.RequestHeaders)
		}
		if got := ex.ResponseHeaders.Values("Set-Cookie"); !slices.Equal(got, []string{core.Redacted}) {
			t.Errorf("response Set-Cookie = %q, want [REDACTED]", got)
		}
		if ex.ResponseHeaders.Get("X-Upstream") != "here" {
			t.Errorf("response headers = %v, want upstream's X-Upstream", ex.ResponseHeaders)
		}
		if ex.Status != wantStatus || ex.Stream != wantStream {
			t.Errorf("status, stream = %d, %v; want %d, %v", ex.Status, ex.Stream, wantStatus, wantStream)
		}
		// The exchange ends when the handler returns, which can be after the client has
		// read the whole body, so only the start is bounded by the client's clock.
		if ex.StartedAt < before.UnixNano() || ex.StartedAt > after.UnixNano() || ex.EndedAt < ex.StartedAt {
			t.Errorf("started_at %d, ended_at %d, want the start within [%d, %d] and the end after it",
				ex.StartedAt, ex.EndedAt, before.UnixNano(), after.UnixNano())
		}
		if ex.TTFBNS == nil || *ex.TTFBNS <= 0 || *ex.TTFBNS > ex.EndedAt-ex.StartedAt {
			t.Errorf("ttfb_ns = %v, want above zero and within the exchange", ex.TTFBNS)
		}
		if f := (abortFlags{}).of(ex); f != (abortFlags{}) || ex.Truncated || ex.RequestTruncated || ex.ResponseTruncated {
			t.Errorf("flags = %+v, truncated %v, want none for a completed exchange", f, ex.Truncated)
		}
		if got := storedBody(t, r, ex.RequestBody); !bytes.Equal(got, upBody) || string(upBody) != reqBody {
			t.Errorf("stored request %q, upstream got %q, client sent %q", got, upBody, reqBody)
		}
		if got := storedBody(t, r, ex.ResponseBody); !bytes.Equal(got, clientBody) {
			t.Errorf("stored response %q, client got %q", got, clientBody)
		}
	}

	t.Run("streamed", func(t *testing.T) {
		dir := t.TempDir()
		var sent bytes.Buffer
		up := newBodyUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("X-Upstream", "here")
			w.Header().Set("Set-Cookie", "session=sekrit-cookie")
			for i := range 3 {
				ev := fmt.Sprintf("event: e%d\ndata: {\"n\":%d}\n\n", i, i)
				sent.WriteString(ev)
				_, _ = io.WriteString(w, ev)
				w.(http.Flusher).Flush()
			}
		})
		g, url := servedAgainst(t, dir, up.URL, nil)
		before := time.Now()
		res, body := send(t, url)
		after := time.Now()
		if !bytes.Equal(body, sent.Bytes()) {
			t.Fatalf("client got %q, upstream sent %q", body, sent.Bytes())
		}
		ex, r := storedExchange(t, g, dir, path)
		check(t, ex, r, res, up.received(t), body, before, after, http.StatusOK, true)
	})

	t.Run("non_streamed", func(t *testing.T) {
		dir := t.TempDir()
		const resBody = `{"type":"message","content":[{"type":"text","text":"hello"}]}`
		up := newBodyUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Upstream", "here")
			w.Header().Set("Set-Cookie", "session=sekrit-cookie")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, resBody)
		})
		g, url := servedAgainst(t, dir, up.URL, nil)
		before := time.Now()
		res, body := send(t, url)
		after := time.Now()
		if string(body) != resBody {
			t.Fatalf("client got %q, upstream sent %q", body, resBody)
		}
		ex, r := storedExchange(t, g, dir, path)
		check(t, ex, r, res, up.received(t), body, before, after, http.StatusCreated, false)
	})
}

// AC10: a gzip response is stored as it crossed the wire, still compressed.
func TestCapture_EncodedBodyStoredAsSent(t *testing.T) {
	const path = "/anthropic/v1/messages"
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = io.WriteString(zw, `{"type":"message","content":[{"type":"text","text":"`+strings.Repeat("compressible ", 200)+`"}]}`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	up := newBodyUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gz.Bytes())
	})
	g, url := servedAgainst(t, dir, up.URL, nil)

	req, err := http.NewRequest(http.MethodPost, url+path, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	// Set by hand, so the client's transport hands back the body undecoded.
	req.Header.Set("Accept-Encoding", "gzip")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, gz.Bytes()) {
		t.Fatalf("client got %d bytes, not upstream's %d gzip bytes", len(body), gz.Len())
	}

	ex, r := storedExchange(t, g, dir, path)
	if got := ex.ResponseHeaders.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("stored Content-Encoding = %q, want gzip", got)
	}
	if got := storedBody(t, r, ex.ResponseBody); !bytes.Equal(got, gz.Bytes()) {
		t.Errorf("stored response is %d bytes, want upstream's %d gzip bytes unchanged", len(got), gz.Len())
	}
}

// AC11: a body over capture.max_body_bytes is forwarded whole; the stored copy is
// exactly the cap long and the exchange is truncated. Both directions are over it.
func TestCapture_BodyOverCapTruncated(t *testing.T) {
	const (
		path  = "/anthropic/v1/messages"
		limit = 1000
	)
	reqBody := strings.Repeat("q", 5*limit)
	resBody := strings.Repeat("r", 7*limit)
	dir := t.TempDir()
	up := newBodyUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Written in pieces, so the cap falls inside a write and not on its edge.
		for i := 0; i < len(resBody); i += 333 {
			_, _ = io.WriteString(w, resBody[i:min(i+333, len(resBody))])
		}
	})
	g, url := servedAgainst(t, dir, up.URL, map[string]string{"GATEWAY_CAPTURE_MAX_BODY_BYTES": fmt.Sprint(limit)})

	res, err := http.Post(url+path, "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || string(body) != resBody {
		t.Fatalf("client got %d bytes (%v), want all %d", len(body), err, len(resBody))
	}
	if got := up.received(t); string(got) != reqBody {
		t.Fatalf("upstream got %d bytes, want all %d", len(got), len(reqBody))
	}

	ex, r := storedExchange(t, g, dir, path)
	if !ex.Truncated || !ex.RequestTruncated || !ex.ResponseTruncated {
		t.Errorf("truncated = %v (request %v, response %v), want all true",
			ex.Truncated, ex.RequestTruncated, ex.ResponseTruncated)
	}
	if got := storedBody(t, r, ex.RequestBody); string(got) != reqBody[:limit] {
		t.Errorf("stored request is %d bytes, want the first %d", len(got), limit)
	}
	if got := storedBody(t, r, ex.ResponseBody); string(got) != resBody[:limit] {
		t.Errorf("stored response is %d bytes, want the first %d", len(got), limit)
	}
	if ex.RequestIncomplete {
		t.Error("request_incomplete is set for a body read to its end; truncated means the cap only")
	}
}

// AC12: an exchange that ends badly is still stored, with the flag saying how it
// ended and the bytes that were seen.
func TestCapture_AbortedExchangesRecorded(t *testing.T) {
	const (
		path  = "/anthropic/v1/messages"
		event = "event: first\ndata: {}\n\n"
	)

	t.Run("client_disconnect", func(t *testing.T) {
		dir := t.TempDir()
		held := make(chan struct{})
		cancelled := make(chan struct{})
		up := newBodyUpstream(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, event)
			w.(http.Flusher).Flush()
			close(held)
			select {
			case <-r.Context().Done():
				close(cancelled)
			case <-time.After(5 * time.Second):
			}
		})
		g, url := servedAgainst(t, dir, up.URL, nil)

		conn, err := net.Dial("tcp", strings.TrimPrefix(url, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		if _, err := io.WriteString(conn, "POST "+path+" HTTP/1.1\r\nHost: x\r\nContent-Length: 2\r\n\r\n{}"); err != nil {
			t.Fatal(err)
		}
		res, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(event))
		if _, err := io.ReadFull(res.Body, got); err != nil {
			t.Fatalf("first event: %v", err)
		}
		waitClosed(t, held, "upstream to hold the stream")
		_ = conn.Close()
		waitClosed(t, cancelled, "upstream's request to be cancelled")

		ex, r := storedExchange(t, g, dir, path)
		if f := (abortFlags{}).of(ex); f != (abortFlags{disconnected: true}) {
			t.Errorf("flags = %+v, want client_disconnected only", f)
		}
		if got := storedBody(t, r, ex.ResponseBody); string(got) != event {
			t.Errorf("stored response %q, want the %q seen before the client left", got, event)
		}
		if got := storedBody(t, r, ex.RequestBody); string(got) != "{}" {
			t.Errorf("stored request %q, want {}", got)
		}
	})

	t.Run("upstream_abort", func(t *testing.T) {
		dir := t.TempDir()
		// Headers and one chunk, then the connection is cut with no terminator.
		up := newBodyUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			_, _ = fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n"+
				"Transfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n", len(event), event)
			_ = buf.Flush()
		})
		g, url := servedAgainst(t, dir, up.URL, nil)

		res, err := http.Post(url+path, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if !errors.Is(err, io.ErrUnexpectedEOF) || string(body) != event {
			t.Fatalf("client got %q (%v), want %q then a broken body", body, err, event)
		}

		ex, r := storedExchange(t, g, dir, path)
		if f := (abortFlags{}).of(ex); f != (abortFlags{aborted: true}) {
			t.Errorf("flags = %+v, want upstream_aborted only", f)
		}
		if ex.Status != http.StatusOK || !ex.Stream {
			t.Errorf("status, stream = %d, %v; want upstream's 200 stream", ex.Status, ex.Stream)
		}
		if got := storedBody(t, r, ex.ResponseBody); string(got) != event {
			t.Errorf("stored response %q, want the %q upstream sent before it died", got, event)
		}
	})

	t.Run("gateway_502", func(t *testing.T) {
		dir := t.TempDir()
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		refused := "http://" + ln.Addr().String()
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}
		g, url := servedAgainst(t, dir, refused, nil)

		res, err := http.Post(url+path, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil || res.StatusCode != http.StatusBadGateway {
			t.Fatalf("status %d (%v), want 502", res.StatusCode, err)
		}

		ex, r := storedExchange(t, g, dir, path)
		// The dial failed, so the transport never read the request body: the copy is
		// sealed with none of it, which is request_incomplete by the spec's definition.
		want := abortFlags{incomplete: true, gatewayError: "upstream_unreachable"}
		if f := (abortFlags{}).of(ex); f != want {
			t.Errorf("flags = %+v, want %+v", f, want)
		}
		if ex.RequestBody != "" {
			t.Errorf("request_body = %q, want none: upstream never read it", ex.RequestBody)
		}
		if ex.Status != http.StatusBadGateway || ex.TTFBNS != nil {
			t.Errorf("status %d, ttfb_ns %v; want 502 with no upstream headers", ex.Status, ex.TTFBNS)
		}
		if got := storedBody(t, r, ex.ResponseBody); len(got) == 0 || !bytes.Equal(got, body) {
			t.Errorf("stored response %q, want the gateway's error body %q", got, body)
		}
	})

	t.Run("request_read_after_seal", func(t *testing.T) {
		const size = 32 << 20 // far more than loopback's socket buffers hold
		dir := t.TempDir()
		// Upstream answers at once and never reads the body, so the transport is
		// still sending it when the exchange ends.
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "early")
		}))
		t.Cleanup(up.Close)
		g, url := servedAgainst(t, dir, up.URL, map[string]string{"GATEWAY_CAPTURE_MAX_BODY_BYTES": fmt.Sprint(2 * size)})

		conn, err := net.Dial("tcp", strings.TrimPrefix(url, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\n\r\n", path, size); err != nil {
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
		g.waitQueued(path, 1)
		_ = conn.Close()
		<-written

		ex, r := storedExchange(t, g, dir, path)
		if !ex.RequestIncomplete {
			t.Error("request_incomplete = false for a request sealed before its body's end")
		}
		if ex.Truncated {
			t.Error("truncated is set; the body was under the cap, so truncated must stay false")
		}
		got := storedBody(t, r, ex.RequestBody)
		if int64(len(got)) >= size || strings.Trim(string(got), "b") != "" {
			t.Errorf("stored request is %d bytes, want a prefix of the %d-byte body", len(got), size)
		}
		if got := storedBody(t, r, ex.ResponseBody); string(got) != "early" {
			t.Errorf("stored response %q, want early", got)
		}
	})
}

// waitClosed fails the test if ch does not close within 5s.
func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// redactSentinels is a sentinel per forwarded auth header, sent in both directions.
// Each value holds sentinel and names its header, so a leak says which one.
var redactSentinels = map[string]string{
	"Authorization": "Bearer " + sentinel + "-authorization",
	"X-Api-Key":     sentinel + "-x-api-key",
	"Cookie":        "session=" + sentinel + "-cookie",
	"Set-Cookie":    "id=" + sentinel + "-set-cookie; Path=/",
}

// proxyAuthSentinel goes in Proxy-Authorization, which 001 strips as hop-by-hop.
const proxyAuthSentinel = "Basic " + sentinel + "-proxy-authorization"

// padding makes a body larger than store.InlineMax, so it lands in a blob.
var padding = strings.Repeat("p", 2*store.InlineMax)

// secretUpstream answers with every redactSentinels header and a Proxy-Authorization,
// and a body big enough for a blob. It keeps the headers each request arrived with.
type secretUpstream struct {
	*httptest.Server
	mu      sync.Mutex
	headers []http.Header
}

func newSecretUpstream(t *testing.T) *secretUpstream {
	t.Helper()
	u := &secretUpstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		u.mu.Lock()
		u.headers = append(u.headers, r.Header.Clone())
		u.mu.Unlock()
		for name, v := range redactSentinels {
			w.Header().Set(name, v)
		}
		w.Header().Set("Proxy-Authorization", proxyAuthSentinel)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"type":"message","pad":"`+padding+`"}`)
	}))
	t.Cleanup(u.Close)
	return u
}

// received returns the headers of the one request upstream got.
func (u *secretUpstream) received(t *testing.T) http.Header {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.headers) != 1 {
		t.Fatalf("upstream got %d requests, want 1", len(u.headers))
	}
	return u.headers[0]
}

// sendSecrets posts a blob-sized body to path with every redactSentinels header and a
// Proxy-Authorization, and returns the response headers once the body is read.
func sendSecrets(t *testing.T, url, path string) http.Header {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+path, strings.NewReader(`{"pad":"`+padding+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range redactSentinels {
		req.Header.Set(name, v)
	}
	req.Header.Set("Proxy-Authorization", proxyAuthSentinel)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("status %d, err %v", res.StatusCode, err)
	}
	return res.Header
}

// scanStore fails the test for every file under dir holding needle: the database
// files as they are on disk, and each blob decompressed. It returns how many blobs
// it read, so a caller can tell the blob half was not vacuous.
func scanStore(t *testing.T, r *store.Reader, dir, needle string) int {
	t.Helper()
	blobs := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		var data []byte
		if parts := strings.Split(rel, string(filepath.Separator)); len(parts) == 3 && parts[0] == "blobs" {
			data, err = r.Content(parts[1] + parts[2])
			blobs++
		} else {
			data, err = os.ReadFile(path)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		if bytes.Contains(data, []byte(needle)) {
			t.Errorf("%s holds %q", rel, needle)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan %s: %v", dir, err)
	}
	return blobs
}

// AC27: the forwarded auth headers' sentinels appear nowhere in the database or any
// decompressed blob, in either direction; their names are kept with [REDACTED].
// Proxy-Authorization is hop-by-hop and never forwarded, so its sentinel is only
// checked absent from the store and the logs.
func TestRedact_AuthHeaders(t *testing.T) {
	const path = "/anthropic/v1/messages"
	dir := t.TempDir()
	up := newSecretUpstream(t)
	g, url := servedAgainst(t, dir, up.URL, nil)
	sendSecrets(t, url, path)

	ex, r := storedExchange(t, g, dir, path)
	if blobs := scanStore(t, r, dir, sentinel); blobs == 0 {
		t.Error("no blob was scanned; the bodies were meant to land in blobs")
	}
	for dirName, h := range map[string]http.Header{"request": ex.RequestHeaders, "response": ex.ResponseHeaders} {
		for name := range redactSentinels {
			if got := h.Values(name); !slices.Equal(got, []string{core.Redacted}) {
				t.Errorf("stored %s header %s = %q, want [%s]", dirName, name, got, core.Redacted)
			}
		}
	}
	noValue(t, g, sentinel)
	noValue(t, g, proxyAuthSentinel)
}

// AC28: the same sentinels, in the headers that are forwarded, reach upstream and the
// client byte-identical while capture redacts its copy.
func TestRedact_ForwardedTrafficUntouched(t *testing.T) {
	const path = "/anthropic/v1/messages"
	dir := t.TempDir()
	up := newSecretUpstream(t)
	g, url := servedAgainst(t, dir, up.URL, nil)
	got := sendSecrets(t, url, path)

	ex, _ := storedExchange(t, g, dir, path)
	sent := up.received(t)
	for name, v := range redactSentinels {
		if vs := sent.Values(name); !slices.Equal(vs, []string{v}) {
			t.Errorf("upstream got %s = %q, want [%q]", name, vs, v)
		}
		if vs := got.Values(name); !slices.Equal(vs, []string{v}) {
			t.Errorf("client got %s = %q, want [%q]", name, vs, v)
		}
	}
	// The stored copy was redacted, so the forwarded values above were not the copy.
	if vs := ex.RequestHeaders.Values("X-Api-Key"); !slices.Equal(vs, []string{core.Redacted}) {
		t.Errorf("stored x-api-key = %q, want [%s]", vs, core.Redacted)
	}
}

// failingStore is the real store with SaveExchange failing for one path.
type failingStore struct {
	capture.Store
	path string
}

func (s *failingStore) SaveExchange(ctx context.Context, ex *core.Exchange) error {
	if ex.Path == s.path {
		return errors.New("injected store failure")
	}
	return s.Store.SaveExchange(ctx, ex)
}

// AC60: sentinels in the auth headers, the query and the body appear in no log line,
// across a stored, a dropped (dropped_memory) and a failed (store_failed) capture.
func TestCapture_SecretsNotLogged(t *testing.T) {
	const (
		stored  = "/anthropic/v1/messages"
		dropped = "/anthropic/v1/drop"
		failed  = "/anthropic/v1/fail"
		limit   = 64 << 10
	)
	dir := t.TempDir()
	up := jsonUpstream(t)
	g, url := startCapturing(t, captureEnv(dir, up, map[string]string{
		"GATEWAY_CAPTURE_MEMORY_LIMIT": fmt.Sprint(limit),
	}), func(dir string, log *zap.Logger) (capture.Store, error) {
		st, err := store.Open(dir, log)
		if err != nil {
			return nil, err
		}
		return &failingStore{Store: st, path: "/v1/fail"}, nil
	})

	header := map[string]string{}
	for name, v := range redactSentinels {
		header[name] = v
	}
	query := "?beta=true&key=" + sentinel
	small := `{"model":"m","messages":[{"role":"user","content":"` + sentinel + `"}]}`
	big := `{"model":"m","messages":[{"role":"user","content":"` + sentinel +
		strings.Repeat("b", 2*limit) + `"}]}`
	for _, c := range []struct{ path, body string }{{stored, small}, {dropped, big}, {failed, small}} {
		if code, err := do(t, http.MethodPost, url+c.path+query, header, c.body); err != nil || code != http.StatusOK {
			t.Fatalf("%s: status %d, err %v", c.path, code, err)
		}
	}
	g.waitQueued(stored, 1)
	g.waitQueued(failed, 1)
	if l := g.requestLines(dropped); len(l) != 1 || l[0]["capture"] != core.CaptureDroppedMemory {
		t.Fatalf("dropped line = %v, want capture %s", l, core.CaptureDroppedMemory)
	}
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	checkNoCaptureLeaks(t)

	line := stoppedLine(t, g)
	if line["dropped_memory"] != float64(1) || line["store_failed"] != float64(1) {
		t.Errorf("capture stopped = %v, want dropped_memory 1 and store_failed 1", line)
	}
	if len(linesWith(g, "capture_failed")) == 0 {
		t.Errorf("no capture_failed line for the failed store\n%s", g.stdout.String())
	}
	ids := g.requestIDs(stored)
	if len(ids) != 1 {
		t.Fatalf("got %d %s request lines, want 1", len(ids), stored)
	}
	if _, err := openReader(t, dir).Exchange(ids[0]); err != nil {
		t.Errorf("the stored exchange is missing: %v", err)
	}
	noValue(t, g, sentinel)
}
