package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
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
	s := e.openRaw(e.sessionPath())
	s.take(2) // exchange a, then the empty hooks snapshot
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
	sess := e.openRaw(e.sessionPath())
	if ev := sess.nextData(); ev.event != "hooks" || ev.data != "[]" {
		t.Fatalf("snapshot = %+v", ev)
	}
	e.hookPre(2)
	ev := global.nextData()
	var sum sessionSummary
	if err := json.Unmarshal([]byte(ev.data), &sum); err != nil || ev.event != "session" || !sum.HooksOnly || sum.ID != sid() {
		t.Fatalf("global = %+v %+v %v", ev, sum, err)
	}
	ev = sess.nextData()
	es := entriesOf(t, ev)
	if ev.event != "hooks" || len(es) != 1 || es[0]["kind"] != "hook_event" {
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
	if strings.Contains(body, "id:") || strings.Count(body, "event: exchange") != 1 || strings.Count(body, "event: hooks") != 1 {
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
	s := e.openRaw(e.sessionPath())
	s.take(2)
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

// webReplaceExchange and webReplaceHooks are the replace functions of web/entries.js, without
// their sort; the test below pins that source, so the two cannot drift apart unnoticed.
const webReplaceExchange = `export function replaceExchange(entries, list) {
  if (!list || list.length === 0) return entries;
  const ids = new Set(list.map((e) => e.exchange_id));
  const out = entries.filter((e) => !ids.has(e.exchange_id));
  out.push(...list);
  out.sort(compareEntries);
  return out;
}`

const webReplaceHooks = `export function replaceHooks(entries, list) {
  const out = entries.filter((e) => e.kind !== "hook_event");
  out.push(...(list || []));
  out.sort(compareEntries);
  return out;
}`

// applyEvent applies one stream event to the client's entries as app.js does.
func applyEvent(entries []map[string]any, event string, list []map[string]any) []map[string]any {
	if event == "hooks" {
		var out []map[string]any
		for _, e := range entries {
			if e["kind"] != kindHookEvent {
				out = append(out, e)
			}
		}
		return append(out, list...)
	}
	return applyReplace(entries, list)
}

func applyReplace(entries, list []map[string]any) []map[string]any {
	ids := map[any]bool{}
	for _, e := range list {
		ids[e["exchange_id"]] = true
	}
	var out []map[string]any
	for _, e := range entries {
		if !ids[e["exchange_id"]] {
			out = append(out, e)
		}
	}
	return append(out, list...)
}

func hookLineIDs(entries []map[string]any) []string {
	var out []string
	for _, e := range entries {
		if e["kind"] == kindHookEvent {
			out = append(out, e["id"].(string))
		}
	}
	return out
}

// The web client replaces entries by their exchange_id. Hook lines travel with one and the
// same exchange_id in the timeline page, the connect snapshot and a live refresh, so a live
// hook update replaces the lines it already holds instead of adding a second set. The server's
// own names for them ("~hook" for paging, "hooks" for the sent-record) never reach the wire.
func TestStream_LiveHookUpdateReplacesFirstSnapshotHookLines(t *testing.T) {
	js := webFiles(t)["entries.js"]
	if !strings.Contains(js, webReplaceExchange) || !strings.Contains(js, webReplaceHooks) {
		t.Fatal("a replace function changed in web/entries.js: re-check that hook lines still replace, then update this test")
	}
	e := newStreamEnv(t, fastHooks)
	e.toolSession(`{"a":1}`)
	e.hook("k", core.HarnessEvent{Phase: core.PhaseNotice, SourceName: "evt-a"}, 2)

	resp, err := http.Get(e.ts.URL + "/api/sessions/" + sid() + "/timeline")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var page tlPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	client := page.Entries // what the page holds after loading the timeline
	first := hookLineIDs(client)
	if len(first) != 1 {
		t.Fatalf("timeline hook lines = %v", first)
	}

	s := e.openRaw(e.sessionPath())
	var wire []map[string]any
	for i, want := range []string{"exchange", "hooks"} { // the connect snapshot: exchange a, then the hook lines
		ev := s.nextData()
		if ev.event != want {
			t.Fatalf("snapshot event %d = %q, want %q", i, ev.event, want)
		}
		list := entriesOf(t, ev)
		wire = append(wire, list...)
		client = applyEvent(client, ev.event, list)
	}
	if got := hookLineIDs(client); !slices.Equal(got, first) {
		t.Fatalf("after the snapshot hook lines = %v, want %v", got, first)
	}

	e.hook("k", core.HarnessEvent{Phase: core.PhaseNotice, SourceName: "evt-b"}, 3)
	e.hub.PublishHook(core.PrincipalLocal, "cc", "k")
	ev := s.nextData()
	live := entriesOf(t, ev)
	if ev.event != "hooks" || len(hookLineIDs(live)) != 2 {
		t.Fatalf("live update = %+v", ev)
	}
	wire = append(wire, live...)
	client = applyEvent(client, ev.event, live)

	got := hookLineIDs(client)
	if len(got) != 2 || got[0] != first[0] || got[1] == first[0] {
		t.Fatalf("after the live update hook lines = %v, want %s and one new line", got, first[0])
	}
	for _, en := range append(wire, page.Entries...) {
		if en["kind"] == kindHookEvent && en["exchange_id"] != "" {
			t.Errorf("hook line %v has exchange_id %q, want the empty one on every path", en["id"], en["exchange_id"])
		}
	}
}

// A session with no hook line still gets a hooks event on connect, so the client has a
// group to replace; when its only hook line becomes matched, the group is sent empty.
func TestStream_HooksSnapshotEmptyIsSentAndRemovesGroup(t *testing.T) {
	e := newStreamEnv(t, fastHooks)
	e.toolSession(`{"a":1}`)
	s := e.openRaw(e.sessionPath())
	if ev := s.nextData(); ev.event != "exchange" {
		t.Fatalf("first = %+v", ev)
	}
	if ev := s.nextData(); ev.event != "hooks" || ev.data != "[]" {
		t.Fatalf("connect snapshot hooks = %+v, want []", ev)
	}
	// An unmatched hook line (an unknown tool call id) is a hook line of its own.
	e.hook("k", core.HarnessEvent{Phase: core.PhasePreTool, ToolCallID: "nope", ToolName: "bash"}, 2)
	e.hub.PublishHook(core.PrincipalLocal, "cc", "k")
	// The hook gives the earlier call a state, so exchange a is re-sent before the hooks.
	ev := s.nextData()
	if ev.event == "exchange" {
		ev = s.nextData()
	}
	if ev.event != "hooks" || len(hookLineIDs(entriesOf(t, ev))) != 1 {
		t.Fatalf("after the hook = %+v", ev)
	}
	// Its tool call arrives: the line is matched, the group is empty.
	e.save("n", "k", "cc", 10)
	evs := []core.StoredEvent{
		{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)},
		{Seq: 1, Kind: core.KindToolCall, ToolCallID: "nope", ContentHash: sha("nin"),
			Payload: []byte(`{"name":"bash","id":"nope"}`)},
	}
	if err := e.s.SaveParse(t.Context(), "n", core.PrincipalLocal, core.ParseOK, evs,
		[]core.Content{{Hash: sha("nin"), Bytes: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	e.publish("n")
	var last sseEvent
	for ev := s.nextData(); ; ev = s.nextData() {
		last = ev
		if ev.event == "hooks" {
			break
		}
	}
	if last.data != "[]" {
		t.Fatalf("matched hooks = %s, want []", last.data)
	}
}
