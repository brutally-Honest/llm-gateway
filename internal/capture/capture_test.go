package capture_test

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/core"
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
