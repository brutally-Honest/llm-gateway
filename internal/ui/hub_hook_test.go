package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
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
	s.sessionFirst()
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
	sess.skipSession = true // the hook creates the row; its summary is not under test here
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
	if err := writeLive(w, liveMsg{All: all, Refresh: true}, sent, eventPositionJSON{}); err != nil {
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
	if err := writeLive(w, liveMsg{All: all, Refresh: true}, sent, eventPositionJSON{}); err != nil || w.Body.Len() != 0 {
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
	// The window never ends on its own here: the test ends it (below), so the refresh
	// runs after the commit and before the next one on every machine. With a 150 ms
	// window it could fire between saving d and publishing it on a slow runner, and send
	// d itself (each change still once, but as a refresh: spec 026 D11).
	e := newStreamEnv(t, func(h *Hub) { h.hookEvery = 10 * time.Second })
	e.toolSession(`{"a":1}`)
	s := e.openRaw(e.sessionPath())
	s.sessionFirst()
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
	// The window ends: its refresh runs now, as its timer would.
	who := sessIdent{core.PrincipalLocal, "cc", "k"}
	e.hub.mu.Lock()
	p := e.hub.pending[who]
	e.hub.mu.Unlock()
	if p == nil || !p.timer.Stop() {
		t.Fatal("no hook refresh armed for the session")
	}
	e.hub.refresh(who, p)
	e.quiet() // a stale refresh leaves a catch-up that reads the store when its build lands
	// Nothing else follows, the refresh included: the next event is the next commit.
	e.save("d", "k", "cc", 20)
	e.parseMsgs("d", "ok", []any{"user", text("last")})
	e.publish("d")
	if ev := s.nextData(); ev.event != "entries" || entriesOf(t, ev)[0]["exchange_id"] != "d" {
		t.Fatalf("extra event: %+v", ev)
	}
}

// applyEvent applies one stream event to a client's entries the way the 013 page did: a
// hooks event replaces the hook group, any other event its exchanges.
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

// A stream client replaces entries by their exchange_id. Hook lines travel with one and the
// same exchange_id in the timeline page, the connect snapshot and a live refresh, so a live
// hook update replaces the lines it already holds instead of adding a second set. The server's
// own names for them ("~hook" for paging, "hooks" for the sent-record) never reach the wire.
func TestStream_LiveHookUpdateReplacesFirstSnapshotHookLines(t *testing.T) {
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
	s.sessionFirst()
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
	s.sessionFirst()
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

// saveResult stores a later exchange of session k whose only tool entry is the tool_result
// of call_1: the call's state changes in the earlier exchange that asked for it.
func (e *streamEnv) saveResult(id string, at int) {
	e.t.Helper()
	e.save(id, "k", "cc", at)
	mp, _ := json.Marshal(map[string]any{"role": "user", "blocks": []map[string]string{{"hash": sha("umsg"), "type": "text"}}})
	evs := []core.StoredEvent{
		{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)},
		{Seq: 1, Kind: core.KindMessage, ContentHash: sha("m2"), Payload: mp},
		{Seq: 2, Kind: core.KindToolResult, ToolCallID: "call_1", ContentHash: sha("out"),
			Payload: []byte(`{"is_error":false}`)},
	}
	contents := []core.Content{{Hash: sha("umsg"), Bytes: []byte(`{"type":"text","text":"next"}`)},
		{Hash: sha("m2"), Bytes: []byte(sha("m2"))}, {Hash: sha("out"), Bytes: []byte("file.txt")}}
	if err := e.s.SaveParse(e.t.Context(), id, core.PrincipalLocal, core.ParseOK, evs, contents); err != nil {
		e.t.Fatal(err)
	}
}

// callStateIn returns the state of tool call id in the entries, failing when it has none.
func callStateIn(t *testing.T, es []map[string]any, id string) string {
	t.Helper()
	for _, en := range es {
		if en["kind"] == "tool_call" && en["tool_call_id"] == id {
			st, _ := en["state"].(string)
			return st
		}
	}
	t.Fatalf("no tool_call %s in %v", id, es)
	return ""
}

// canonicalEntries sorts entries by their JSON, so two views compare as sets.
func canonicalEntries(t *testing.T, es []map[string]any) []string {
	t.Helper()
	out := make([]string, 0, len(es))
	for _, en := range es {
		b, err := json.Marshal(en)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	slices.Sort(out)
	return out
}

// A call asked in exchange a, run by hooks, and answered by a later commit b: the stream's
// last state for the call is asked+ran+reported, the same as a reload. The answer changes a's
// entries, so a is re-sent after b's entries; writeLive must send earlier exchanges too.
func TestHooksLive_CallStateConvergesAcrossLaterCommit(t *testing.T) {
	e := newStreamEnv(t, fastHooks)
	e.toolSession(`{"a":1}`)
	s := e.openRaw(e.sessionPath())
	s.sessionFirst()
	var view []map[string]any
	apply := func(n int) {
		t.Helper()
		for range n {
			ev := s.nextData()
			view = applyEvent(view, ev.event, entriesOf(t, ev))
		}
	}
	apply(2) // exchange a, then the empty hooks group

	e.hookPre(2)
	apply(1)
	if st := callStateIn(t, view, "call_1"); st != timeline.StatePending {
		t.Fatalf("after the pre-tool hook state = %q", st)
	}

	e.hook("k", core.HarnessEvent{Phase: core.PhasePostTool, ToolCallID: "call_1", ToolName: "bash"}, 3)
	e.hub.PublishHook(core.PrincipalLocal, "cc", "k")
	apply(1)
	if st := callStateIn(t, view, "call_1"); st != timeline.StateRanNotReported {
		t.Fatalf("after the post-tool hook state = %q", st)
	}

	e.saveResult("b", 4)
	e.publish("b")
	apply(2) // b's entries, then exchange a again with the answered call
	if st := callStateIn(t, view, "call_1"); st != timeline.StateAskedRanReported {
		t.Fatalf("after the later commit state = %q", st)
	}
	fresh := e.freshTimeline()
	if got, want := canonicalEntries(t, view), canonicalEntries(t, fresh); !slices.Equal(got, want) {
		t.Fatalf("live != reload:\nlive   %v\nreload %v", got, want)
	}
}

// A hooks-only session whose last hook arrived within the last second is active and not
// marked inactive, on the live read and in the sessions list.
func TestHooksOnly_HookWithinLastSecondNotInactive(t *testing.T) {
	e := newStreamEnv(t, nil)
	now := baseTime.Add(100 * time.Second) // the stream env's clock
	hookSeq++
	ev := core.HarnessEvent{Phase: core.PhaseNotice, SessionKey: "solo"}
	rec := &store.HookRecord{PrincipalID: core.PrincipalLocal, Client: "cc", Seq: hookSeq,
		ReceivedAt: now.Add(-500 * time.Millisecond), Payload: []byte(`{"n":1}`), Event: ev}
	if err := e.s.SaveHook(t.Context(), rec); err != nil {
		t.Fatal(err)
	}

	m, err := e.hub.read(t.Context(), sessIdent{core.PrincipalLocal, "cc", "solo"}, nil, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if m.Summary == nil || !m.Summary.HooksOnly || !m.Summary.Active {
		t.Fatalf("summary = %+v", m.Summary)
	}
	if m.Inactive {
		t.Fatal("hooks-only session with a hook one second ago is marked inactive")
	}
	resp, err := http.Get(e.ts.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range list.Items {
		if it["session_key"] == "solo" {
			found = true
			if it["active"] != true {
				t.Fatalf("listed hooks-only session = %v, want active", it)
			}
		}
	}
	if !found {
		t.Fatal("hooks-only session not listed")
	}
}

func TestHub_HookRefreshSendsSessionSummary(t *testing.T) {
	e := newStreamEnv(t, fastHooks)
	e.toolSession(`{"a":1}`)
	s := e.openRaw(e.sessionPath())
	s.sessionFirst()
	s.take(2) // exchange a, empty hooks
	s.skipSession = false
	e.hookPre(2)
	ev := s.nextData()
	if ev.event != "session" {
		t.Fatalf("first event after the hook = %q, want session", ev.event)
	}
	var want sessionSummary
	if err := json.Unmarshal([]byte(e.reload()), &want); err != nil {
		t.Fatal(err)
	}
	got := s.summaryUntil(ev, want)
	if got.ID != sid() || got.ToolCalls != 1 || !reflect.DeepEqual(got, want) {
		t.Fatalf("summary %+v != reload %+v", got, want)
	}
}

// A stream on a session with no stored row stays open and sends no session event when a
// refresh finds the row still missing; the event comes once the row exists.
func TestHub_NoRowStreamStaysOpenOnRefresh(t *testing.T) {
	e := newStreamEnv(t, func(h *Hub) {
		fastHooks(h)
		h.heartbeat = 10 * time.Millisecond
	})
	s := e.openRaw(e.sessionPath())
	if ev := s.nextData(); ev.event != "hooks" || ev.data != "[]" {
		t.Fatalf("first = %+v, want the empty hooks event", ev)
	}
	e.hub.PublishHook(core.PrincipalLocal, "cc", "k")
	// Read past the refresh window: next fails the test if the stream ends.
	for end := time.Now().Add(120 * time.Millisecond); time.Now().Before(end); {
		if ev := s.next(); ev.event != "" {
			t.Fatalf("event %+v on a session with no row, want only pings", ev)
		}
	}
	e.hookPre(2)
	if ev := s.nextData(); ev.event != "session" {
		t.Fatalf("first event once the row exists = %+v, want session", ev)
	}
}

// A hooks-only session's stream gets its summary in the snapshot and on refresh, and stays open.
func TestHub_HooksOnlySessionStreamGetsSummary(t *testing.T) {
	e := newStreamEnv(t, fastHooks)
	e.hook("k", core.HarnessEvent{Phase: core.PhasePreTool, ToolCallID: "call_1", ToolName: "bash", ToolInput: []byte(`{"a":2}`)}, 2)
	s := e.openRaw(e.sessionPath())
	assertSummary := func(ev sseEvent, wantLast time.Time) {
		t.Helper()
		var got sessionSummary
		if ev.event != "session" || json.Unmarshal([]byte(ev.data), &got) != nil {
			t.Fatalf("event = %+v, want session", ev)
		}
		if !got.HooksOnly || got.ExchangeCount != 0 || got.LastAt != nsTime(wantLast.UnixNano()) {
			t.Fatalf("summary = %+v, want hooks only with last %v", got, wantLast)
		}
	}
	assertSummary(s.nextData(), baseTime.Add(2*time.Second))
	if ev := s.nextData(); ev.event != "hooks" || ev.data == "[]" {
		t.Fatalf("snapshot = %+v, want the hook lines", ev)
	}
	e.hookPre(3)
	assertSummary(s.nextData(), baseTime.Add(3*time.Second))
	if ev := s.nextData(); ev.event != "hooks" {
		t.Fatalf("after the summary = %+v, want hooks", ev)
	}
}

// lateRefresh forces the interleaving the same-window test can meet on a slow runner: a
// hook refresh whose read is stale arms a catch-up, and that catch-up's fresh wait is held
// open until the test says so. gate is closed to let it go on; while it is held, hold is
// set so only that wait blocks.
type lateRefresh struct {
	hold, released atomic.Bool
	gate           chan struct{}
	// read is closed when the released catch-up has read the store; it still holds sendMu
	// then, so a commit published after it is dispatched after the catch-up's send.
	read chan struct{}
	once sync.Once
}

func (l *lateRefresh) tune(h *Hub) {
	h.hookEvery = 10 * time.Second
	read, fresh := h.read, h.fresh
	h.read = func(ctx context.Context, who sessIdent, ids []string, summary, entries bool) (liveMsg, error) {
		m, err := read(ctx, who, ids, summary, entries)
		if !l.hold.Load() && ids == nil && entries && l.released.Load() {
			l.once.Do(func() { close(l.read) })
		}
		if l.hold.Load() && ids == nil && entries {
			m.StaleEntries = true // the refresh read from a cached build behind the store
		}
		return m, err
	}
	h.fresh = func(ctx context.Context, who sessIdent, entries bool) error {
		if l.hold.Load() {
			select {
			case <-l.gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return fresh(ctx, who, entries)
	}
}

func TestHub_ForcedLateCatchUpSendsRefreshBeforeCommit(t *testing.T) {
	l := &lateRefresh{gate: make(chan struct{}), read: make(chan struct{})}
	e := newStreamEnv(t, l.tune)
	e.toolSession(`{"a":1}`)
	s := e.openRaw(e.sessionPath())
	s.sessionFirst()
	s.take(2)
	e.save("c", "k", "cc", 10)
	e.parseMsgs("c", "ok", []any{"user", text("later")})
	e.hook("k", core.HarnessEvent{Phase: core.PhasePreTool, ToolCallID: "call_1", ToolName: "bash"}, 2)
	e.hub.PublishHook(core.PrincipalLocal, "cc", "k")
	e.publish("c")
	s.take(2)
	who := sessIdent{core.PrincipalLocal, "cc", "k"}
	e.hub.mu.Lock()
	p := e.hub.pending[who]
	e.hub.mu.Unlock()
	if p == nil || !p.timer.Stop() {
		t.Fatal("no hook refresh armed for the session")
	}
	l.hold.Store(true)
	e.hub.refresh(who, p) // stale: nothing sent, a catch-up armed and held
	e.hub.mu.Lock()
	armed := e.hub.catching[who] != nil
	e.hub.mu.Unlock()
	if !armed {
		t.Fatal("the stale refresh armed no catch-up")
	}
	e.save("d", "k", "cc", 20)
	e.parseMsgs("d", "ok", []any{"user", text("last")})
	l.hold.Store(false)
	l.released.Store(true)
	close(l.gate) // the catch-up lands after d is saved and before it is published
	<-l.read
	e.publish("d")
	// The hub's contract (AC54, spec 026 D11): a refresh sends every exchange that differs
	// from what the stream last sent, a saved-but-unpublished d included, and the commit of
	// d then still sends its entries event. Both are in order; the first is no extra event.
	ev := s.nextData()
	if ev.event != "exchange" || entriesOf(t, ev)[0]["exchange_id"] != "d" {
		t.Fatalf("first event = %+v, want the refresh of d", ev)
	}
	ev = s.nextData()
	if ev.event != "entries" || entriesOf(t, ev)[0]["exchange_id"] != "d" {
		t.Fatalf("second event = %+v, want the commit's entries of d", ev)
	}
}

// quietNow reports whether the hub has no catch-up waiting or running. Catching empty is
// not enough: a catch-up leaves it before it takes sendMu, then reads and sends.
func (e *streamEnv) quietNow() bool {
	e.hub.mu.Lock()
	defer e.hub.mu.Unlock()
	return len(e.hub.catching) == 0 && e.hub.catchRuns == 0
}

// quiet waits until quietNow. It polls the hub's own state; the deadline only turns a
// hang into a failure.
func (e *streamEnv) quiet() {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if e.quietNow() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	e.t.Fatal("the hub's catch-ups never ended")
}

// quiet must not report the hub idle in either half of a catch-up: while it waits for its
// build, and in the gap after it left catching and before its send is done. The test holds
// sendMu to keep the catch-up in that gap.
func TestHub_QuietCoversTheGapBeforeACatchUpSends(t *testing.T) {
	l := &lateRefresh{gate: make(chan struct{}), read: make(chan struct{})}
	e := newStreamEnv(t, l.tune)
	e.toolSession(`{"a":1}`)
	s := e.openRaw(e.sessionPath())
	s.sessionFirst()
	s.take(2)
	who := sessIdent{core.PrincipalLocal, "cc", "k"}
	e.hook("k", core.HarnessEvent{Phase: core.PhasePreTool, ToolCallID: "call_1", ToolName: "bash"}, 2)
	e.hub.PublishHook(core.PrincipalLocal, "cc", "k")
	e.hub.mu.Lock()
	p := e.hub.pending[who]
	e.hub.mu.Unlock()
	if p == nil || !p.timer.Stop() {
		t.Fatal("no hook refresh armed for the session")
	}
	l.hold.Store(true)
	e.hub.refresh(who, p)
	if e.quietNow() {
		t.Fatal("quiet while the catch-up waits for its build")
	}
	e.hub.sendMu.Lock()
	l.hold.Store(false)
	close(l.gate) // the build lands; the catch-up leaves catching and waits for sendMu
	left := false
	deadline := time.Now().Add(10 * time.Second)
	for !left && time.Now().Before(deadline) {
		e.hub.mu.Lock()
		left = len(e.hub.catching) == 0
		e.hub.mu.Unlock()
		if !left {
			time.Sleep(time.Millisecond)
		}
	}
	if !left {
		t.Fatal("the catch-up never left catching")
	}
	if e.quietNow() {
		t.Fatal("quiet in the gap between leaving catching and sending")
	}
	e.hub.sendMu.Unlock()
	e.quiet()
	if ev := s.nextData(); ev.event != "exchange" {
		t.Fatalf("event = %+v, want the catch-up's refresh", ev)
	}
}
