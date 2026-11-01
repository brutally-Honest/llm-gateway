package ui

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

func fastHooks(h *Hub) { h.hookEvery = 40 * time.Millisecond }

func (e *streamEnv) hookPre(at int) {
	e.hook("k", core.HarnessEvent{Phase: core.PhasePreTool, ToolCallID: "call_1", ToolName: "bash", ToolInput: []byte(`{"a":2}`)}, at)
	e.hub.PublishHook(core.PrincipalLocal, "cc", "k")
}

func entriesOf(t *testing.T, ev sseEvent) []map[string]any {
	t.Helper()
	var es []map[string]any
	if err := json.Unmarshal([]byte(ev.data), &es); err != nil {
		t.Fatal(err)
	}
	return es
}

func TestHub_HookEventPushesSessionTimeline(t *testing.T) {
	e := newStreamEnv(t, fastHooks)
	e.toolSession(`{"a":1}`)
	s := e.open(e.sessionPath())
	s.take(1)
	e.hookPre(2)
	ev := s.nextData()
	if ev.event != "exchange" || ev.id != "" {
		t.Fatalf("event = %+v, want exchange without id", ev)
	}
	var state any
	for _, en := range entriesOf(t, ev) {
		if en["kind"] == "tool_call" {
			state = en["state"]
		}
	}
	if state == nil || state == "" {
		t.Fatalf("tool call has no state: %s", ev.data)
	}
}

func TestHub_HookEventPushesSessionsList(t *testing.T) {
	e := newStreamEnv(t, fastHooks)
	e.toolSession(`{"a":1}`)
	s := e.open("/api/stream")
	s.take(1)
	e.hookPre(2)
	ev := s.nextData()
	if ev.event != "session" {
		t.Fatalf("event = %q", ev.event)
	}
	var sum sessionSummary
	if err := json.Unmarshal([]byte(ev.data), &sum); err != nil || sum.ID != sid() {
		t.Fatalf("summary = %+v %v", sum, err)
	}
}

func TestHub_HookOnlySessionAppears(t *testing.T) {
	e := newStreamEnv(t, fastHooks)
	global := e.open("/api/stream")
	sess := e.open(e.sessionPath())
	e.hookPre(2)
	ev := global.nextData()
	var sum sessionSummary
	if err := json.Unmarshal([]byte(ev.data), &sum); err != nil || ev.event != "session" || !sum.HooksOnly || sum.ID != sid() {
		t.Fatalf("global = %+v %+v %v", ev, sum, err)
	}
	ev = sess.nextData()
	es := entriesOf(t, ev)
	if ev.event != "exchange" || len(es) != 1 || es[0]["kind"] != "hook_event" {
		t.Fatalf("session = %+v", ev)
	}
}

func TestHub_HookPublishNeverBlocks(t *testing.T) {
	f := &fakeRead{block: make(chan struct{})}
	h := testHub(4, f)
	h.hookEvery = time.Millisecond
	defer h.Close()
	if _, err := h.subscribe(testSess.id()); err != nil {
		t.Fatal(err)
	}
	within(t, 2*time.Second, "PublishHook", func() {
		for i := 0; i < 1000; i++ {
			h.PublishHook("local", "cc", "k")
		}
	})
	h.Close()
	within(t, 2*time.Second, "PublishHook after Close", func() { h.PublishHook("local", "cc", "k") })
}

func TestHub_HookRefreshesCoalescePerSession(t *testing.T) {
	f := &fakeRead{all: []liveExchange{{RequestID: "x", Hash: "h"}}}
	h := testHub(4, f)
	h.hookEvery = 80 * time.Millisecond
	defer h.Close()
	sub, _ := h.subscribe(testSess.id())
	h.PublishHook("local", "cc", "k")
	h.mu.Lock()
	first := h.pending[testSess]
	h.mu.Unlock()
	for i := 0; i < 10; i++ {
		h.PublishHook("local", "cc", "k")
	}
	h.mu.Lock()
	same := h.pending[testSess] == first && first != nil
	h.mu.Unlock()
	if !same {
		t.Fatal("a later event in the window replaced the armed refresh")
	}
	m := recv(t, sub)
	if !m.Refresh || len(m.All) != 1 {
		t.Fatalf("msg = %+v", m)
	}
	time.Sleep(200 * time.Millisecond)
	if n := f.count(); n != 1 {
		t.Fatalf("reads = %d, want 1", n)
	}
	// The read happens after the last event of the burst.
	f.set([]liveExchange{{RequestID: "x", Hash: "h2"}})
	h.PublishHook("local", "cc", "k")
	if m := recv(t, sub); m.All[0].Hash != "h2" {
		t.Fatalf("last state not sent: %+v", m)
	}
}

func TestHub_HookRefreshSendsOnlyChangedExchanges(t *testing.T) {
	all := []liveExchange{
		{RequestID: "a", Hash: "1", Entries: []entryJSON{{ID: "a:0"}}},
		{RequestID: "hooks", Hash: "2", Entries: []entryJSON{{ID: "hook:0"}}},
		{RequestID: "b", Hash: "3", Entries: []entryJSON{{ID: "b:0"}}},
	}
	sent := map[string]string{"a": "1", "b": "old"}
	w := httptest.NewRecorder()
	if err := writeLive(w, liveMsg{All: all, Refresh: true}, sent); err != nil {
		t.Fatal(err)
	}
	body := w.Body.String()
	if strings.Contains(body, "a:0") || !strings.Contains(body, "hook:0") || !strings.Contains(body, "b:0") {
		t.Fatalf("body = %q", body)
	}
	if strings.Contains(body, "id:") || strings.Count(body, "event: exchange") != 2 {
		t.Fatalf("body = %q", body)
	}
	w = httptest.NewRecorder()
	if err := writeLive(w, liveMsg{All: all, Refresh: true}, sent); err != nil || w.Body.Len() != 0 {
		t.Fatalf("second write = %q %v", w.Body.String(), err)
	}
}

func TestHub_HookRefreshCancelledWhenLastStreamCloses(t *testing.T) {
	f := &fakeRead{}
	h := testHub(4, f)
	h.hookEvery = 60 * time.Millisecond
	defer h.Close()
	a, _ := h.subscribe(testSess.id())
	b, _ := h.subscribe("")
	h.PublishHook("local", "cc", "k")
	h.refresh(testSess, &pendingRefresh{}) // a timer that fired as it was stopped does nothing
	h.unsubscribe(a)
	h.mu.Lock()
	n := len(h.pending)
	h.mu.Unlock()
	if n != 1 {
		t.Fatalf("pending = %d while the global stream is open", n)
	}
	h.unsubscribe(b)
	h.mu.Lock()
	n = len(h.pending)
	h.mu.Unlock()
	if n != 0 {
		t.Fatalf("pending = %d after the last stream closed", n)
	}
	time.Sleep(200 * time.Millisecond)
	if f.count() != 0 {
		t.Fatalf("a read fired after the last stream closed")
	}
}

func TestHub_HookRefreshAndCommitInSameWindowSendEachChangeOnce(t *testing.T) {
	e := newStreamEnv(t, func(h *Hub) { h.hookEvery = 150 * time.Millisecond })
	e.toolSession(`{"a":1}`)
	s := e.open(e.sessionPath())
	s.take(1)
	e.save("c", "k", "cc", 10)
	e.parseMsgs("c", "ok", []any{"user", text("later")})
	e.hook("k", core.HarnessEvent{Phase: core.PhasePreTool, ToolCallID: "call_1", ToolName: "bash"}, 2)
	e.hub.PublishHook(core.PrincipalLocal, "cc", "k")
	e.publish("c")
	got := s.take(2)
	counts := map[string]int{}
	for _, ev := range got {
		counts[ev.event+":"+entriesOf(t, ev)[0]["exchange_id"].(string)]++
	}
	if counts["entries:c"] != 1 || counts["exchange:a"] != 1 {
		t.Fatalf("events = %v", counts)
	}
	// Nothing else follows: the next event is the next commit.
	e.save("d", "k", "cc", 20)
	e.parseMsgs("d", "ok", []any{"user", text("last")})
	e.publish("d")
	if ev := s.nextData(); ev.event != "entries" || entriesOf(t, ev)[0]["exchange_id"] != "d" {
		t.Fatalf("extra event: %+v", ev)
	}
}
