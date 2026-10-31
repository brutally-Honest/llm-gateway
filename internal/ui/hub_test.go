package ui

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/capture"
)

// fakeRead records every read and answers from the ids it was given.
type fakeRead struct {
	mu    sync.Mutex
	calls []fakeCall
	block chan struct{} // when set, a read waits for it to close
	enter chan struct{} // when set, a read signals here before waiting
}

type fakeCall struct {
	ids              []string
	summary, entries bool
}

func (f *fakeRead) fn(ctx context.Context, who sessIdent, ids []string, summary, entries bool) (liveMsg, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{append([]string(nil), ids...), summary, entries})
	f.mu.Unlock()
	if f.enter != nil {
		select {
		case f.enter <- struct{}{}:
		default:
		}
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return liveMsg{}, ctx.Err()
		}
	}
	var m liveMsg
	if summary {
		m.Summary = &sessionSummary{ID: who.id(), ExchangeCount: len(ids)}
	}
	if entries {
		for i, id := range ids {
			m.Exchanges = append(m.Exchanges, liveExchange{RowID: int64(i + 1), RequestID: id,
				Entries: []entryJSON{{ID: id + ":0", ExchangeID: id, Kind: kindExchange}}})
		}
	}
	return m, nil
}

func (f *fakeRead) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func testHub(queue int, f *fakeRead) *Hub {
	h := newHub(Deps{}, queue)
	h.read = f.fn
	return h
}

func commit(id string) capture.Committed {
	return capture.Committed{RequestID: id, PrincipalID: "local", Client: "cc", SessionKey: "k"}
}

var testSess = sessIdent{"local", "cc", "k"}

func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %s", what, d)
	}
}

func recv(t *testing.T, s *subscriber) liveMsg {
	t.Helper()
	select {
	case m := <-s.ch:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("no message")
		return liveMsg{}
	}
}

func TestHub_PublishNeverBlocks(t *testing.T) {
	f := &fakeRead{}
	h := testHub(4, f) // dispatcher never started: nothing drains the queue
	defer h.Close()
	for i := 0; i < 5; i++ {
		if _, err := h.subscribe(""); err != nil {
			t.Fatal(err)
		}
	}
	within(t, 2*time.Second, "10000 publishes", func() {
		for i := 0; i < 10000; i++ {
			h.Publish(commit(fmt.Sprint(i)))
		}
	})
}

func TestHub_PublishAfterCloseIsNoOp(t *testing.T) {
	f := &fakeRead{}
	h := testHub(2, f)
	h.start()
	h.Close()
	h.Close() // twice is fine
	var wg sync.WaitGroup
	within(t, 2*time.Second, "publish after close", func() {
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 500; i++ {
					h.Publish(commit("x"))
				}
			}()
		}
		wg.Wait()
	})
	if _, err := h.subscribe(""); err == nil {
		t.Fatal("subscribe after close succeeded")
	}
}

func TestHub_PublishRacingCloseNeverPanics(t *testing.T) {
	for i := 0; i < 50; i++ {
		h := testHub(1, &fakeRead{})
		h.start()
		var wg sync.WaitGroup
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					h.Publish(commit("x"))
				}
			}()
		}
		h.Close()
		wg.Wait()
	}
}

func TestHub_CoalescesQueuedCommitsPerSession(t *testing.T) {
	f := &fakeRead{}
	h := testHub(16, f)
	defer h.Close()
	sess, _ := h.subscribe(testSess.id())
	glob, _ := h.subscribe("")
	for i := 0; i < 5; i++ {
		h.Publish(commit(fmt.Sprintf("e%d", i)))
	}
	h.start() // all five are queued before the dispatcher runs
	m := recv(t, sess)
	if len(m.Exchanges) != 5 {
		t.Fatalf("batch carries %d exchanges, want 5", len(m.Exchanges))
	}
	if g := recv(t, glob); g.Summary == nil {
		t.Fatal("global stream got no summary")
	}
	if n := f.count(); n != 1 {
		t.Fatalf("%d session reads, want 1", n)
	}
}

func TestHub_UnwatchedSessionGetsStatsOnly(t *testing.T) {
	f := &fakeRead{}
	h := testHub(16, f)
	defer h.Close()
	glob, _ := h.subscribe("")
	other, _ := h.subscribe(sessIdent{"local", "cc", "other"}.id())
	h.start()
	h.Publish(commit("a"))
	if m := recv(t, glob); m.Summary == nil {
		t.Fatal("no session event for the global stream")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 || f.calls[0].entries || !f.calls[0].summary {
		t.Fatalf("reads = %+v, want one summary-only read", f.calls)
	}
	select {
	case <-other.ch:
		t.Fatal("a stream for another session got a message")
	default:
	}
}

func TestHub_NobodyWatchingDoesNoRead(t *testing.T) {
	// The dispatcher is driven by hand, so whether anyone was subscribed when a commit
	// was handled never depends on goroutine timing.
	f := &fakeRead{}
	h := testHub(16, f)
	defer h.Close()
	h.dispatch([]capture.Committed{commit("a")})
	if n := f.count(); n != 0 {
		t.Fatalf("reads with nobody watching = %d, want 0", n)
	}
	glob, _ := h.subscribe("")
	h.dispatch([]capture.Committed{commit("b")})
	recv(t, glob)
	if n := f.count(); n != 1 {
		t.Fatalf("reads after a global subscriber joined = %d, want 1", n)
	}
}

func TestHub_FullBufferDropsOnlyThatSubscriber(t *testing.T) {
	h := testHub(4, &fakeRead{})
	defer h.Close()
	slow, _ := h.subscribe("")
	fast, _ := h.subscribe("")
	for i := 0; i < subscriberBuffer; i++ {
		h.send(slow, liveMsg{})
		h.send(fast, liveMsg{})
		<-fast.ch
	}
	select {
	case <-slow.gone:
		t.Fatal("dropped at exactly 64 buffered")
	default:
	}
	h.send(slow, liveMsg{}) // the 65th
	h.send(fast, liveMsg{})
	select {
	case <-slow.gone:
	default:
		t.Fatal("slow subscriber not dropped")
	}
	select {
	case <-fast.gone:
		t.Fatal("fast subscriber dropped")
	default:
	}
	h.mu.Lock()
	_, slowIn := h.subs[slow]
	_, fastIn := h.subs[fast]
	h.mu.Unlock()
	if slowIn || !fastIn {
		t.Fatalf("registry slow=%v fast=%v", slowIn, fastIn)
	}
}

func TestHub_PublishReturnsWhenQueueFull(t *testing.T) {
	f := &fakeRead{block: make(chan struct{}), enter: make(chan struct{}, 1)}
	h := testHub(2, f)
	defer h.Close()
	a, _ := h.subscribe("")
	b, _ := h.subscribe(testSess.id())
	h.start()
	h.Publish(commit("first"))
	select {
	case <-f.enter: // the dispatcher is now stuck inside its read
	case <-time.After(3 * time.Second):
		t.Fatal("dispatcher never read")
	}
	within(t, 2*time.Second, "publish into a full queue", func() {
		for i := 0; i < 100; i++ {
			h.Publish(commit(fmt.Sprint(i)))
		}
	})
	if !h.overflow.Load() {
		t.Fatal("overflow not flagged")
	}
	close(f.block)
	for _, s := range []*subscriber{a, b} {
		select {
		case <-s.gone:
		case <-time.After(3 * time.Second):
			t.Fatal("stream not closed on overflow")
		}
	}
	var n atomic.Int32
	n.Store(int32(f.count()))
	if n.Load() < 1 {
		t.Fatal("no read")
	}
}
