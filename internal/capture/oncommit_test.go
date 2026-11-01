package capture_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// orderStore records every store call in order, and can fail either write.
type orderStore struct {
	failSave, failParse bool

	mu    sync.Mutex
	calls []string
}

func (s *orderStore) note(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}

func (s *orderStore) log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *orderStore) SaveExchange(_ context.Context, ex *core.Exchange) error {
	s.note("save_exchange " + ex.RequestID)
	if s.failSave {
		return errStoreFailed
	}
	return nil
}

func (s *orderStore) SaveParse(_ context.Context, requestID, _ string, _ core.ParseStatus, _ []core.StoredEvent, _ []core.Content) error {
	s.note("save_parse " + requestID)
	if s.failParse {
		return errStoreFailed
	}
	return nil
}

func (s *orderStore) Close() error { return nil }

// callsFor keeps the calls about one exchange, in order.
func callsFor(calls []string, id string) []string {
	var out []string
	for _, c := range calls {
		if strings.HasSuffix(c, " "+id) {
			out = append(out, c)
		}
	}
	return out
}

// AC27: the committed callback runs after the exchange is stored and its parse is
// saved, so the page is told only about rows that exist.
func TestSink_OnCommitAfterStore(t *testing.T) {
	checkNoLeaksAtEnd(t)
	exs, budget := capturedExchanges(t, 3)
	log, _ := newLogger()
	st := &orderStore{}
	sink := capture.NewSink(capture.Config{
		QueueSize: 8, Workers: 1,
		OnCommit: func(c capture.Committed) { st.note("committed " + c.RequestID) },
	}, st, log)
	for i, ex := range exs {
		if !sink.Submit(ex) {
			t.Fatalf("Submit %d = false with room in the queue", i)
		}
	}
	drainSink(t, sink)

	calls := st.log()
	for _, ex := range exs {
		want := []string{
			"save_exchange " + ex.RequestID,
			"save_parse " + ex.RequestID,
			"committed " + ex.RequestID,
		}
		got := callsFor(calls, ex.RequestID)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("exchange %s: calls %v, want %v", ex.RequestID, got, want)
		}
	}
	if got := budget.InUse(); got != 0 {
		t.Errorf("InUse = %d, want 0", got)
	}
}

// eventsParser returns two canonical events for every exchange.
type eventsParser struct{}

func (eventsParser) Parse(core.ParseInput) core.ParseResult {
	tokens := int64(3)
	return core.ParseResult{Status: core.ParseOK, Events: []core.Event{
		{Kind: core.KindRequest, Request: &core.RequestEvent{Model: "test-model"}},
		{Kind: core.KindUsage, Usage: &core.UsageEvent{OutputTokens: &tokens}},
	}}
}
func (eventsParser) HashExcludedFields() []string { return nil }

// eventsAdapter is the test adapter with eventsParser.
type eventsAdapter struct{ testAdapter }

func (eventsAdapter) Parser() core.Parser { return eventsParser{} }

// AC27: inside the callback, a read-only connection sees the exchange's parse status
// and all of its events.
func TestSink_OnCommitSeesEventsThroughReadOnlyReader(t *testing.T) {
	checkNoLeaksAtEnd(t)
	dir := t.TempDir()
	log, logs := newLogger()
	st, err := store.Open(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	var mu sync.Mutex
	parse := map[string]string{}
	events := map[string]int{}
	sink := capture.NewSink(capture.Config{
		QueueSize: 8, Workers: 1, DecodeLimit: 1 << 20,
		OnCommit: func(c capture.Committed) {
			r, err := store.OpenReader(dir)
			if err != nil {
				t.Errorf("OpenReader in the callback: %v", err)
				return
			}
			defer func() { _ = r.Close() }()
			row, err := r.Exchange(c.RequestID)
			if err != nil {
				t.Errorf("exchange %s in the callback: %v", c.RequestID, err)
				return
			}
			evs, err := r.Events(c.RequestID)
			if err != nil {
				t.Errorf("events %s in the callback: %v", c.RequestID, err)
				return
			}
			mu.Lock()
			parse[c.RequestID] = row.Parse
			events[c.RequestID] = len(evs)
			mu.Unlock()
		},
	}, st, log)

	base := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "response body")
	})
	g := startGatewayFor(t, eventsAdapter{}, base, log, logs, sink, core.NewBudget(1<<20))
	var ids []string
	for i := range 2 {
		path := "/t/v1/events_" + string(rune('a'+i))
		g.post(t, path, "request body", 5*time.Second)
		id, _ := g.accessLine(t, path)["request_id"].(string)
		if id == "" {
			t.Fatalf("%s: no request_id on the access line", path)
		}
		ids = append(ids, id)
	}
	drainSink(t, sink)

	mu.Lock()
	defer mu.Unlock()
	for _, id := range ids {
		if parse[id] != string(core.ParseOK) {
			t.Errorf("exchange %s: parse seen in the callback %q, want %s", id, parse[id], core.ParseOK)
		}
		if events[id] != 2 {
			t.Errorf("exchange %s: %d events seen in the callback, want 2", id, events[id])
		}
	}
}

// Research D7: when SaveParse fails for good, the callback still runs once, after
// the failed SaveParse, and the exchange is counted as a store failure.
func TestSink_OnCommitAfterSaveParseFails(t *testing.T) {
	checkNoLeaksAtEnd(t)
	exs, _ := capturedExchanges(t, 2)
	log, _ := newLogger()
	st := &orderStore{failParse: true}
	var committed atomic.Int64
	sink := capture.NewSink(capture.Config{
		QueueSize: 8, Workers: 1,
		OnCommit: func(c capture.Committed) {
			committed.Add(1)
			st.note("committed " + c.RequestID)
		},
	}, st, log)
	for i, ex := range exs {
		if !sink.Submit(ex) {
			t.Fatalf("Submit %d = false with room in the queue", i)
		}
	}
	drainSink(t, sink)

	if got := committed.Load(); got != int64(len(exs)) {
		t.Errorf("callback ran %d times, want once per exchange (%d)", got, len(exs))
	}
	calls := st.log()
	for _, ex := range exs {
		want := []string{
			"save_exchange " + ex.RequestID,
			"save_parse " + ex.RequestID,
			"committed " + ex.RequestID,
		}
		if got := callsFor(calls, ex.RequestID); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("exchange %s: calls %v, want %v", ex.RequestID, got, want)
		}
	}
	if got := sink.Counts().StoreFailed; got != int64(len(exs)) {
		t.Errorf("StoreFailed = %d, want %d", got, len(exs))
	}
}

// Research D7: a failed SaveExchange means no row exists, so nothing is published.
func TestSink_NoCallbackWhenSaveExchangeFails(t *testing.T) {
	checkNoLeaksAtEnd(t)
	exs, budget := capturedExchanges(t, 2)
	log, _ := newLogger()
	st := &orderStore{failSave: true}
	var committed atomic.Int64
	sink := capture.NewSink(capture.Config{
		QueueSize: 8, Workers: 1,
		OnCommit: func(capture.Committed) { committed.Add(1) },
	}, st, log)
	for i, ex := range exs {
		if !sink.Submit(ex) {
			t.Fatalf("Submit %d = false with room in the queue", i)
		}
	}
	drainSink(t, sink)

	if got := committed.Load(); got != 0 {
		t.Errorf("callback ran %d times, want none when SaveExchange failed", got)
	}
	if got := sink.Counts().StoreFailed; got != int64(len(exs)) {
		t.Errorf("StoreFailed = %d, want %d", got, len(exs))
	}
	if got := budget.InUse(); got != 0 {
		t.Errorf("InUse = %d, want 0", got)
	}
}

// A panicking callback is recovered: the worker goes on storing, every exchange is
// stored, the panic's value never reaches a log line, and each panic is logged once
// with the commit stage.
func TestSink_PanickingOnCommitIsRecovered(t *testing.T) {
	checkNoLeaksAtEnd(t)
	const sentinel = "on-commit-panic-sentinel"
	exs, budget := capturedExchanges(t, 3)
	log, logs := newLogger()
	st := &recordingStore{}
	var calls atomic.Int64
	sink := capture.NewSink(capture.Config{
		QueueSize: 8, Workers: 1,
		OnCommit: func(capture.Committed) {
			calls.Add(1)
			panic(sentinel)
		},
	}, st, log)
	for i, ex := range exs {
		if !sink.Submit(ex) {
			t.Fatalf("Submit %d = false with room in the queue", i)
		}
	}
	drainSink(t, sink)

	if got := calls.Load(); got != int64(len(exs)) {
		t.Errorf("callback ran %d times, want %d", got, len(exs))
	}
	if got := st.count(); got != len(exs) {
		t.Errorf("stored %d exchanges, want all %d", got, len(exs))
	}
	if c := sink.Counts(); c.StoreFailed != 0 || c.ParseFailed != 0 {
		t.Errorf("counts %+v, want no failures from a panicking callback", c)
	}
	if strings.Contains(logs.String(), sentinel) {
		t.Error("the callback's panic value reached a log line")
	}
	lines := logLines(t, logs, "capture_failed")
	if len(lines) != len(exs) {
		t.Fatalf("%d capture_failed lines, want %d", len(lines), len(exs))
	}
	for _, line := range lines {
		if line["stage"] != "commit" {
			t.Errorf("stage %v, want commit", line["stage"])
		}
	}
	if got := budget.InUse(); got != 0 {
		t.Errorf("InUse = %d, want 0", got)
	}
}
