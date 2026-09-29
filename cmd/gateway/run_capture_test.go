package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/pprof"
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
