package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// closeLogStore wraps the real store and notes what happened to it, in order.
type closeLogStore struct {
	capture.Store
	mu     sync.Mutex
	saved  int
	closed bool
}

func (s *closeLogStore) SaveExchange(ctx context.Context, ex *core.Exchange) error {
	err := s.Store.SaveExchange(ctx, ex)
	s.mu.Lock()
	s.saved++
	s.mu.Unlock()
	return err
}

func (s *closeLogStore) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return s.Store.Close()
}

func (s *closeLogStore) state() (saved int, closed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saved, s.closed
}

// Spec 047 D16: UI work held past its cancel never takes the capture drain's budget. The
// held work blocks until its own wait ends; the sink still drains everything under the
// drain context, which is not spent when close returns. The wait for the UI comes after
// the sink drained and before the store (and the reader) close.
func TestCaptureRun_CloseDrainsBeforeItWaitsForTheUI(t *testing.T) {
	const n = 5
	var st *closeLogStore
	d := deps{openStore: func(dir string, log *zap.Logger) (capture.Store, error) {
		real, err := store.Open(dir, log)
		st = &closeLogStore{Store: real}
		return st, err
	}}
	capt, code := startCapture(t.Context(), config.Capture{Dir: t.TempDir(), QueueSize: 16, Workers: 2}, config.Hooks{}, true, nil, d, zap.NewNop())
	if capt == nil {
		t.Fatalf("startCapture exit code = %d", code)
	}
	capt.uiBudget = 100 * time.Millisecond
	var heldSaved int
	var heldStoreClosed bool
	var heldReadErr error
	held := false
	capt.uiHold = func(ctx context.Context) {
		held = true
		heldSaved, heldStoreClosed = st.state()
		_, heldReadErr = capt.reader.WithContext(ctx).MaxEffectID()
		<-ctx.Done() // the UI work is stuck until its own budget ends
	}
	for i := range n {
		ex := &core.Exchange{RequestID: "r" + string(rune('a'+i)), PrincipalID: core.PrincipalLocal, Protocol: "test",
			Client: "cc", Method: "POST", Path: "/v1/x", Status: 200, Start: time.Now(), End: time.Now()}
		if !capt.sink.Submit(ex) {
			t.Fatalf("Submit %d refused", i)
		}
	}
	drain, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	undrained, err := capt.close(drain)
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if !held {
		t.Fatal("close never waited for the UI work")
	}
	if undrained != 0 {
		t.Errorf("undrained = %d, want 0", undrained)
	}
	if drain.Err() != nil {
		t.Errorf("the drain context ended (%v): the UI wait spent its budget", drain.Err())
	}
	if heldSaved != n {
		t.Errorf("the UI wait began with %d of %d exchanges stored: it came before the drain ended", heldSaved, n)
	}
	if heldStoreClosed {
		t.Error("the store was closed before the UI wait")
	}
	if heldReadErr != nil {
		t.Errorf("the reader was closed before the UI wait ended: %v", heldReadErr)
	}
	if _, closed := st.state(); !closed {
		t.Error("the store was not closed after the UI wait")
	}
}
