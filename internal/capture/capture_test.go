package capture_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// AC6: with a store that never returns, a stream still reaches the client event by
// event: each one before upstream sends the next.
func TestCapture_StreamNotDelayed(t *testing.T) {
	checkNoLeaksAtEnd(t)
	events := []string{
		"event: start\ndata: {\"n\":0}\n\n",
		"event: ping\ndata: {}\n\n",
		"event: delta\ndata: {\"n\":1}\n\n",
		"event: stop\ndata: {\"n\":2}\n\n",
	}
	next := make(chan struct{})
	base := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/stream" {
			_, _ = io.WriteString(w, "ok")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i, ev := range events {
			_, _ = io.WriteString(w, ev)
			w.(http.Flusher).Flush()
			if i == len(events)-1 {
				return
			}
			select {
			case <-next: // the client has this event; send the next
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Second):
				t.Errorf("event %d never reached the client", i)
				return
			}
		}
	})
	log, logs := newLogger()
	st := newBlockingStore()
	sink := capture.NewSink(capture.Config{QueueSize: 8, Workers: 1}, st, log)
	g := startGateway(t, base, log, logs, sink, core.NewBudget(1<<20))

	// The only worker is now stuck in the store for good.
	g.post(t, "/t/v1/first", "request body", 2*time.Second)
	st.waitEntered(t, 1)

	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Get(g.url + "/t/v1/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	for i, want := range events {
		got := make([]byte, len(want))
		if _, err := io.ReadFull(res.Body, got); err != nil {
			t.Fatalf("event %d: waiting for %q before upstream sends the next: %v", i, want, err)
		}
		if string(got) != want {
			t.Fatalf("event %d = %q, want %q", i, got, want)
		}
		if i < len(events)-1 {
			next <- struct{}{}
		}
	}
	if rest, err := io.ReadAll(res.Body); err != nil || len(rest) != 0 {
		t.Errorf("after the last event: %q, %v; want the end of the stream", rest, err)
	}
	if line := g.accessLine(t, "/t/v1/stream"); line["capture"] != core.CaptureQueued {
		t.Errorf("capture = %v, want %s", line["capture"], core.CaptureQueued)
	}
}

// AC23: with the queue full the exchange is dropped, not waited for: the request
// succeeds at once and its access line says dropped_queue_full.
func TestCapture_QueueFullDrops(t *testing.T) {
	checkNoLeaksAtEnd(t)
	base := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "response body")
	})
	log, logs := newLogger()
	st := newBlockingStore()
	sink := capture.NewSink(capture.Config{QueueSize: 1, Workers: 1}, st, log)
	budget := core.NewBudget(1 << 20)
	g := startGateway(t, base, log, logs, sink, budget)

	// The first exchange holds the only worker, the second fills the queue.
	g.post(t, "/t/v1/in_store", "request body", 2*time.Second)
	st.waitEntered(t, 1)
	g.post(t, "/t/v1/in_queue", "request body", 2*time.Second)
	if line := g.accessLine(t, "/t/v1/in_queue"); line["capture"] != core.CaptureQueued {
		t.Fatalf("capture = %v, want %s: the queue should have had room", line["capture"], core.CaptureQueued)
	}
	held := budget.InUse()

	for i := range 3 {
		path := fmt.Sprintf("/t/v1/dropped_%d", i)
		if got := g.post(t, path, "request body", time.Second); got != "response body" {
			t.Errorf("%s: client got %q, want the whole response", path, got)
		}
		if line := g.accessLine(t, path); line["capture"] != core.CaptureDroppedQueueFull {
			t.Errorf("%s: capture = %v, want %s", path, line["capture"], core.CaptureDroppedQueueFull)
		}
	}
	if got := sink.Counts().DroppedQueueFull; got != 3 {
		t.Errorf("DroppedQueueFull = %d, want 3", got)
	}
	if got := budget.InUse(); got != held {
		t.Errorf("InUse = %d, want %d: a dropped exchange must give its memory back", got, held)
	}
}

// echoUpstream keeps each request body it is sent, by upstream path (the adapter's
// prefix stripped), and answers every path with its own fixed body.
type echoUpstream struct {
	mu  sync.Mutex
	got map[string][]byte
}

func newEchoUpstream(t *testing.T) (*echoUpstream, http.HandlerFunc) {
	t.Helper()
	u := &echoUpstream{got: map[string][]byte{}}
	return u, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("upstream read %s: %v", r.URL.Path, err)
		}
		u.mu.Lock()
		u.got[r.URL.Path] = body
		u.mu.Unlock()
		_, _ = io.WriteString(w, answerFor(r.URL.Path))
	}
}

func answerFor(path string) string { return "answer for " + path }

func (u *echoUpstream) received(path string) []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.got[path]
}

// captureRig is the proxy for adapter a in front of an echo upstream, capturing
// through a real Sink into st.
type captureRig struct {
	g    *gateway
	logs *syncBuffer
	sink *capture.Sink
	up   *echoUpstream
}

func startRig(t *testing.T, a core.Adapter, st capture.Store, log *zap.Logger, logs *syncBuffer) *captureRig {
	t.Helper()
	up, respond := newEchoUpstream(t)
	base := newUpstream(t, respond)
	sink := capture.NewSink(capture.Config{QueueSize: 16, Workers: 2, DecodeLimit: 1 << 20}, st, log)
	g := startGatewayFor(t, a, base, log, logs, sink, core.NewBudget(1<<24))
	return &captureRig{g: g, logs: logs, sink: sink, up: up}
}

// send posts body to path (with the prefix), checks that upstream got the body and the client got
// upstream's answer, both byte for byte, and returns the exchange's request ID.
func (c *captureRig) send(t *testing.T, path, body string) string {
	t.Helper()
	upPath := strings.TrimPrefix(path, testAdapter{}.Prefix())
	if got := c.g.post(t, path, body, 5*time.Second); got != answerFor(upPath) {
		t.Errorf("%s: client got %q, want %q", path, got, answerFor(upPath))
	}
	if got := c.up.received(upPath); !bytes.Equal(got, []byte(body)) {
		t.Errorf("%s: upstream got %d bytes, want the %d sent", path, len(got), len(body))
	}
	id, _ := c.g.accessLine(t, path)["request_id"].(string)
	if id == "" {
		t.Fatalf("%s: no request_id on the access line", path)
	}
	return id
}

// wantFailures checks that each exchange got exactly one capture_failed line, with
// the given stage.
func wantFailures(t *testing.T, logs *syncBuffer, stage string, ids []string) {
	t.Helper()
	for _, id := range ids {
		lines := captureFailed(t, logs, id)
		if len(lines) != 1 {
			t.Errorf("exchange %s: %d capture_failed lines, want 1:\n%s", id, len(lines), logs.String())
			continue
		}
		if lines[0]["stage"] != stage {
			t.Errorf("exchange %s: stage %v, want %s", id, lines[0]["stage"], stage)
		}
	}
}

// AC25: after startup, a store that fails never touches a request. Each request is
// forwarded byte for byte, and its exchange gets one capture_failed line with stage
// store, counted.
func TestCapture_StoreDeadClientUnaffected(t *testing.T) {
	t.Run("db_deleted", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		dir := t.TempDir()
		log, logs := newLogger()
		st, err := store.Open(dir, log)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		rig := startRig(t, testAdapter{}, st, log, logs)

		db := filepath.Join(dir, "gateway.db")
		if err := os.Remove(db); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for i := range 3 {
			ids = append(ids, rig.send(t, fmt.Sprintf("/t/v1/after_delete_%d", i), "request body"))
		}
		drainSink(t, rig.sink)
		wantFailures(t, logs, "store", ids)
		if got := rig.sink.Counts().StoreFailed; got != int64(len(ids)) {
			t.Errorf("StoreFailed = %d, want %d", got, len(ids))
		}

		t.Run("deleted_logged_once", func(t *testing.T) {
			lines := logLines(t, logs, "store file deleted or replaced; restart the gateway to resume capture")
			if len(lines) != 1 {
				t.Errorf("%d deleted-or-replaced lines across %d exchanges, want 1", len(lines), len(ids))
			}
			if _, err := os.Stat(db); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("gateway.db after the delete: %v, want it still gone", err)
			}
		})
	})

	// The directory is made read-only after startup. The open database handles keep
	// working, so what fails is every write that needs a new file: a body over the
	// inline limit needs a blob (research Q11).
	t.Run("db_read_only", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		dir := t.TempDir()
		log, logs := newLogger()
		st, err := store.Open(dir, log)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		rig := startRig(t, testAdapter{}, st, log, logs)

		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		var ids []string
		for i := range 3 {
			path := fmt.Sprintf("/t/v1/read_only_%d", i)
			ids = append(ids, rig.send(t, path, path+strings.Repeat("x", 8<<10)))
		}
		drainSink(t, rig.sink)
		wantFailures(t, logs, "store", ids)
		if got := rig.sink.Counts().StoreFailed; got != int64(len(ids)) {
			t.Errorf("StoreFailed = %d, want %d", got, len(ids))
		}
	})

	t.Run("write_fails", func(t *testing.T) {
		checkNoLeaksAtEnd(t)
		log, logs := newLogger()
		st := &failingStore{}
		rig := startRig(t, testAdapter{}, st, log, logs)
		var ids []string
		for i := range 3 {
			ids = append(ids, rig.send(t, fmt.Sprintf("/t/v1/write_fails_%d", i), "request body"))
		}
		drainSink(t, rig.sink)
		wantFailures(t, logs, "store", ids)
		if got := rig.sink.Counts().StoreFailed; got != int64(len(ids)) {
			t.Errorf("StoreFailed = %d, want %d", got, len(ids))
		}
	})
}

// panicSentinel is what the panicking parser panics with. It stands in for request
// data, so it must never reach a log line.
const panicSentinel = "panic-sentinel-from-the-body"

// panicAdapter is the test adapter with a parser that panics on every exchange.
type panicAdapter struct{ testAdapter }

func (panicAdapter) Parser() core.Parser { return panicParser{} }

type panicParser struct{}

func (panicParser) Parse(core.ParseInput) core.ParseResult { panic(panicSentinel) }
func (panicParser) HashExcludedFields() []string           { return nil }

// A parser that panics fails that exchange's parse, not the worker: the raw exchange
// stays stored with parse failed, one capture_failed line says stage parse without
// the panic's value, and the next exchange is handled the same way.
func TestCapture_ParsePanicIsFailed(t *testing.T) {
	checkNoLeaksAtEnd(t)
	dir := t.TempDir()
	log, logs := newLogger()
	st, err := store.Open(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	rig := startRig(t, panicAdapter{}, st, log, logs)

	bodies := map[string]string{}
	var ids []string
	for i := range 2 {
		path := fmt.Sprintf("/t/v1/panics_%d", i)
		bodies[path] = "request body " + path
		ids = append(ids, rig.send(t, path, bodies[path]))
	}
	drainSink(t, rig.sink)
	wantFailures(t, logs, "parse", ids)
	if c := rig.sink.Counts(); c.ParseFailed != 2 || c.StoreFailed != 0 {
		t.Errorf("counts %+v, want 2 parse failures and no store failure", c)
	}
	if strings.Contains(logs.String(), panicSentinel) {
		t.Error("the panic's value reached a log line")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	for i, id := range ids {
		row, err := r.Exchange(id)
		if err != nil {
			t.Fatalf("exchange %s: %v", id, err)
		}
		if row.Parse != string(core.ParseFailed) {
			t.Errorf("exchange %s: parse %q, want %s", id, row.Parse, core.ParseFailed)
		}
		body, err := r.Content(row.RequestBody)
		if err != nil {
			t.Fatalf("exchange %s: request body: %v", id, err)
		}
		if path := fmt.Sprintf("/t/v1/panics_%d", i); string(body) != bodies[path] {
			t.Errorf("exchange %s: stored request body %q, want %q", id, body, bodies[path])
		}
		if evs, err := r.Events(id); err != nil || len(evs) != 0 {
			t.Errorf("exchange %s: events %v, %v; want none", id, evs, err)
		}
	}
}
