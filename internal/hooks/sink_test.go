package hooks_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/hooks"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

type fakeStore struct {
	mu      sync.Mutex
	recs    []*store.HookRecord
	err     error
	gate    chan struct{} // when set, SaveHook waits for it to close
	started chan struct{}
	once    sync.Once
}

func (f *fakeStore) SaveHook(ctx context.Context, h *store.HookRecord) error {
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.recs = append(f.recs, h)
	return nil
}

func (f *fakeStore) records() []*store.HookRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*store.HookRecord(nil), f.recs...)
}

type mapperFunc func([]byte) core.HarnessEvent

func (m mapperFunc) MapHarness(b []byte) core.HarnessEvent { return m(b) }

func findMapper(name string, m core.HarnessMapper) func(string) core.HarnessMapper {
	return func(c string) core.HarnessMapper {
		if c == name {
			return m
		}
		return nil
	}
}

var toolMapper = mapperFunc(func([]byte) core.HarnessEvent {
	return core.HarnessEvent{Phase: core.PhasePreTool, SessionKey: "s1", ToolName: "t"}
})

func logger() (*zap.Logger, *bytes.Buffer, *sync.Mutex) {
	buf := &bytes.Buffer{}
	mu := &sync.Mutex{}
	core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&lockedWriter{buf, mu}), zapcore.DebugLevel)
	return zap.New(core), buf, mu
}

type lockedWriter struct {
	b  *bytes.Buffer
	mu *sync.Mutex
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func closeSink(t *testing.T, s *hooks.Sink) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.Close(ctx)
}

func cfg(q, w int, limit int64, m func(string) core.HarnessMapper) hooks.Config {
	return hooks.Config{QueueSize: q, Workers: w, MemoryLimit: limit, Mappers: m}
}

func TestSink_QueueFullDropsCountsAndLogsOnce(t *testing.T) {
	st := &fakeStore{gate: make(chan struct{}), started: make(chan struct{})}
	log, buf, mu := logger()
	s := hooks.NewSink(cfg(1, 1, 1<<20, findMapper("c", toolMapper)), st, log)
	defer closeSink(t, s)
	if !s.Submit("c", 0, time.Time{}, []byte(`{}`)) {
		t.Fatal("first submit refused")
	}
	<-st.started // the worker holds the first; the queue is empty
	if !s.Submit("c", 0, time.Time{}, []byte(`{}`)) {
		t.Fatal("second submit refused")
	}
	done := make(chan bool)
	go func() { done <- s.Submit("c", 0, time.Time{}, []byte(`{}`)) }()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("third submit accepted on a full queue")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Submit blocked")
	}
	if got := s.Counts().QueueFull; got != 1 {
		t.Fatalf("QueueFull = %d, want 1", got)
	}
	mu.Lock()
	lines := bytes.Count(buf.Bytes(), []byte("queue full"))
	mu.Unlock()
	if lines != 1 {
		t.Fatalf("queue full log lines = %d, want 1", lines)
	}
	close(st.gate)
}

func TestSink_MemoryLimitDrops(t *testing.T) {
	st := &fakeStore{gate: make(chan struct{}), started: make(chan struct{})}
	log, _, _ := logger()
	s := hooks.NewSink(cfg(8, 1, 10, findMapper("c", toolMapper)), st, log)
	defer closeSink(t, s)
	if !s.Submit("c", 0, time.Time{}, []byte(`{"a":123}`)) { // 9 bytes
		t.Fatal("first submit refused")
	}
	if s.Submit("c", 0, time.Time{}, []byte(`{"a":1}`)) { // 7 more crosses 10
		t.Fatal("submit over the memory limit accepted")
	}
	if got := s.Counts().Oversize; got != 1 {
		t.Fatalf("Oversize = %d, want 1", got)
	}
	close(st.gate)
}

func TestSink_UnknownClientStoredAsUnknown(t *testing.T) {
	st := &fakeStore{}
	log, _, _ := logger()
	s := hooks.NewSink(cfg(4, 1, 1<<20, findMapper("c", toolMapper)), st, log)
	body := []byte(`{"x":1}`)
	s.Submit("nobody", 0, time.Time{}, body)
	closeSink(t, s)
	recs := st.records()
	if len(recs) != 1 {
		t.Fatalf("stored %d, want 1", len(recs))
	}
	r := recs[0]
	if r.Client != "unknown" || r.Event.Phase != core.PhaseUnknown {
		t.Fatalf("client=%q phase=%q", r.Client, r.Event.Phase)
	}
	if !bytes.Equal(r.Payload, body) || r.PrincipalID != core.PrincipalLocal {
		t.Fatalf("payload/principal wrong: %q %q", r.Payload, r.PrincipalID)
	}
}

func TestSink_NotJSONStoredAndCounted(t *testing.T) {
	st := &fakeStore{}
	log, _, _ := logger()
	s := hooks.NewSink(cfg(4, 1, 1<<20, nil), st, log)
	s.Submit("c", 0, time.Time{}, []byte(`not json`))
	closeSink(t, s)
	if len(st.records()) != 1 || s.Counts().NotJSON != 1 {
		t.Fatalf("stored=%d notjson=%d", len(st.records()), s.Counts().NotJSON)
	}
}

func TestSink_StoreFailureCounted(t *testing.T) {
	st := &fakeStore{err: errors.New("disk full")}
	log, _, _ := logger()
	var stored atomic.Int64
	c := cfg(4, 1, 1<<20, findMapper("c", toolMapper))
	c.OnStored = func(hooks.HookStored) { stored.Add(1) }
	s := hooks.NewSink(c, st, log)
	s.Submit("c", 0, time.Time{}, []byte(`{}`))
	closeSink(t, s)
	if s.Counts().StoreFailed != 1 || stored.Load() != 0 {
		t.Fatalf("failed=%d onStored=%d", s.Counts().StoreFailed, stored.Load())
	}
}

func TestSink_MapperPanicDoesNotKillWorker(t *testing.T) {
	st := &fakeStore{}
	log, _, _ := logger()
	var calls atomic.Int64
	m := mapperFunc(func(b []byte) core.HarnessEvent {
		if calls.Add(1) == 1 {
			panic("boom")
		}
		return core.HarnessEvent{Phase: core.PhasePreTool}
	})
	s := hooks.NewSink(cfg(4, 1, 1<<20, findMapper("c", m)), st, log)
	s.Submit("c", 0, time.Time{}, []byte(`{}`))
	s.Submit("c", 0, time.Time{}, []byte(`{}`))
	closeSink(t, s)
	recs := st.records()
	if len(recs) != 2 {
		t.Fatalf("stored %d, want 2", len(recs))
	}
	if recs[0].Event.Phase != core.PhaseUnknown || recs[1].Event.Phase != core.PhasePreTool {
		t.Fatalf("phases %q %q", recs[0].Event.Phase, recs[1].Event.Phase)
	}
}

func TestSink_CloseDrainsQueue(t *testing.T) {
	st := &fakeStore{}
	log, _, _ := logger()
	s := hooks.NewSink(cfg(16, 2, 1<<20, findMapper("c", toolMapper)), st, log)
	for range 10 {
		s.Submit("c", 0, time.Time{}, []byte(`{}`))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if n := s.Close(ctx); n != 0 {
		t.Fatalf("undrained = %d", n)
	}
	if got := len(st.records()); got != 10 {
		t.Fatalf("stored %d, want 10", got)
	}
	if s.Submit("c", 0, time.Time{}, []byte(`{}`)) {
		t.Fatal("submit after close accepted")
	}
}

func TestSink_OnStoredCalledAfterSave(t *testing.T) {
	st := &fakeStore{}
	log, _, _ := logger()
	var got hooks.HookStored
	var sawRecord bool
	c := cfg(4, 1, 1<<20, findMapper("c", toolMapper))
	c.OnStored = func(h hooks.HookStored) {
		got = h
		sawRecord = len(st.records()) == 1
	}
	s := hooks.NewSink(c, st, log)
	s.Submit("c", 0, time.Time{}, []byte(`{}`))
	closeSink(t, s)
	// HookStored also carries the mapped event (spec 014), which holds a slice: the
	// identity is compared field by field, and the event by its session key.
	want := [3]string{core.PrincipalLocal, "c", "s1"}
	ident := [3]string{got.Principal, got.Client, got.SessionKey}
	if ident != want || got.Event.SessionKey != "s1" || !sawRecord {
		t.Fatalf("got %+v sawRecord=%v", got, sawRecord)
	}
}

func TestSink_ConcurrentSubmitAndClose(t *testing.T) {
	st := &fakeStore{}
	log, _, _ := logger()
	s := hooks.NewSink(cfg(4, 2, 1<<20, findMapper("c", toolMapper)), st, log)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				s.Submit("c", 0, time.Time{}, []byte(`{}`))
			}
		}()
	}
	closeSink(t, s)
	wg.Wait()
	closeSink(t, s)
}
