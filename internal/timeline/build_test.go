package timeline_test

import (
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

func ids(entries []timeline.Entry) []string {
	var out []string
	for _, e := range entries {
		if e.Kind == timeline.KindExchange || e.Kind == timeline.KindFolded {
			out = append(out, e.RequestID)
		}
	}
	return out
}

func TestBuild_OrdersExchangesByStartThenRequestID(t *testing.T) {
	f := newFake()
	// The reader hands them over ordered by (start, id); Build keeps that order.
	f.thread("A", 10, "u:one")
	f.thread("B", 20, "u:one", "a:two")
	f.thread("C", 20, "u:one", "a:two", "u:three")
	got := ids(build(t, f, timeline.Options{}))
	if !equal(got, []string{"A", "B", "C"}) {
		t.Fatalf("order = %v", got)
	}
}

func TestBuild_EventOrderWithinExchange(t *testing.T) {
	f := newFake()
	f.exchange("A", 1, "ok")
	f.msg("A", "user", "run it")
	f.msg("A", "assistant", "ok")
	f.toolCall("A", "c1", "Run", `{"x":1}`)
	f.toolResult("A", "c1", `"done"`, false)
	f.add("A", store.EventRow{Kind: "error", Payload: []byte(`{"status":500,"type":"boom","message":"bad"}`)})
	f.add("A", store.EventRow{Kind: "usage"})
	var kinds []timeline.Kind
	for _, e := range build(t, f, timeline.Options{}) {
		kinds = append(kinds, e.Kind)
	}
	want := []timeline.Kind{timeline.KindExchange, timeline.KindMessage, timeline.KindMessage,
		timeline.KindToolCall, timeline.KindToolResult, timeline.KindError}
	if len(kinds) != len(want) {
		t.Fatalf("kinds = %v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
}

func TestBuild_ShowsOnlyMessagesAfterSharedPrefix(t *testing.T) {
	f := newFake()
	f.thread("A", 1, "u:q1", "a:r1")
	f.thread("B", 2, "u:q1", "a:r1", "u:q2", "a:r2")
	out := build(t, f, timeline.Options{})
	if got := texts(out, "A"); !equal(got, []string{"q1", "r1"}) {
		t.Fatalf("A = %v", got)
	}
	if got := texts(out, "B"); !equal(got, []string{"q2", "r2"}) {
		t.Fatalf("B = %v", got)
	}
}

func TestBuild_RepeatedUserMessageShownAgain(t *testing.T) {
	f := newFake()
	f.thread("A", 1, "u:continue", "a:r1")
	f.thread("B", 2, "u:continue", "a:r1", "u:continue", "a:r2")
	if got := texts(build(t, f, timeline.Options{}), "B"); !equal(got, []string{"continue", "r2"}) {
		t.Fatalf("B = %v", got)
	}
}

func TestBuild_TieBreaksPrefixEqualThenMostRecent(t *testing.T) {
	// Both A (longer, diverges after "q1") and B (equals the shared prefix) share one
	// leading message with C; B's own list equals the prefix, so B wins: no marker.
	f := newFake()
	f.thread("A", 1, "u:q1", "a:x")
	f.thread("B", 2, "u:q1")
	f.thread("C", 3, "u:q1", "a:y")
	if got := texts(build(t, f, timeline.Options{}), "C"); !equal(got, []string{"y"}) {
		t.Fatalf("exact match must win, C = %v", got)
	}
	// With no exact candidate, the most recent wins: D shares "q1" with A and B (both
	// longer), picks B, and B's list is longer than the prefix, so D is marked.
	g := newFake()
	g.thread("A", 1, "u:q1", "a:x", "u:x2")
	g.thread("B", 2, "u:q1", "a:z")
	g.thread("D", 3, "u:q1", "a:w")
	out := texts(build(t, g, timeline.Options{}), "D")
	if !equal(out, []string{"!", "w"}) {
		t.Fatalf("D = %v", out)
	}
	// Same lengths but the earlier one is the exact match: the later, longer one
	// does not win a tie on length alone.
	h := newFake()
	h.thread("A", 1, "u:q1", "a:r1")
	h.thread("B", 2, "u:q1", "a:r1", "u:q2")
	h.thread("C", 3, "u:q1", "a:r1", "u:q3")
	// A equals the prefix of 2; B shares 2 but is longer; A wins -> no marker.
	if got := texts(build(t, h, timeline.Options{}), "C"); !equal(got, []string{"q3"}) {
		t.Fatalf("C = %v", got)
	}
}

func TestBuild_RewriteMarksHistoryDiverged(t *testing.T) {
	f := newFake()
	f.thread("A", 1, "u:q1", "a:r1", "u:q2", "a:r2")
	f.thread("B", 2, "u:q1", "u:summary", "u:q3")
	out := build(t, f, timeline.Options{})
	if got := texts(out, "B"); !equal(got, []string{"!", "summary", "q3"}) {
		t.Fatalf("B = %v", got)
	}
	for _, e := range out {
		if e.Kind == timeline.KindMarker && e.Text != timeline.MarkerDiverged {
			t.Fatalf("marker text = %q", e.Text)
		}
	}
}

func TestBuild_NoMarkerWhenBestMatchIsPrefix(t *testing.T) {
	f := newFake()
	f.thread("A", 1, "u:q1", "a:r1")
	f.thread("B", 2, "u:q1", "a:r1", "u:q2")
	for _, e := range build(t, f, timeline.Options{}) {
		if e.Kind == timeline.KindMarker {
			t.Fatalf("unexpected marker on %s", e.RequestID)
		}
	}
}

func TestBuild_NewThreadWhenNoSharedMessage(t *testing.T) {
	f := newFake()
	f.thread("A", 1, "u:q1", "a:r1")
	f.thread("B", 2, "u:other", "a:reply")
	if got := texts(build(t, f, timeline.Options{}), "B"); !equal(got, []string{"other", "reply"}) {
		t.Fatalf("B = %v", got)
	}
}

func TestBuild_SubAgentInterleaveNoMarkers(t *testing.T) {
	f := newFake()
	f.thread("P1", 1, "u:p", "a:p1")
	f.thread("S1", 2, "u:task", "a:s1")
	f.thread("P2", 3, "u:p", "a:p1", "u:p2", "a:p2")
	f.thread("S2", 4, "u:task", "a:s1", "u:s2", "a:s2")
	out := build(t, f, timeline.Options{})
	if got := texts(out, "S1"); !equal(got, []string{"task", "s1"}) {
		t.Fatalf("S1 = %v", got)
	}
	if got := texts(out, "P2"); !equal(got, []string{"p2", "p2"}) {
		t.Fatalf("P2 = %v", got)
	}
	if got := texts(out, "S2"); !equal(got, []string{"s2", "s2"}) {
		t.Fatalf("S2 = %v", got)
	}
	for _, e := range out {
		if e.Kind == timeline.KindMarker {
			t.Fatalf("unexpected marker on %s", e.RequestID)
		}
	}
}

func TestBuild_FoldsExchangesWithoutMessages(t *testing.T) {
	f := newFake()
	ex := f.exchange("H", 1, "ok")
	ex.Method, ex.Path, ex.Status = "HEAD", "/api/hello", 204
	f.add("H", store.EventRow{Kind: "request"})
	f.thread("A", 2, "u:q1")
	out := build(t, f, timeline.Options{})
	if len(out) < 1 || out[0].Kind != timeline.KindFolded {
		t.Fatalf("first = %+v", out)
	}
	e := out[0]
	if e.Method != "HEAD" || e.Path != "/api/hello" || e.Status != 204 || e.Start != 1 {
		t.Fatalf("folded = %+v", e)
	}
	if got := texts(out, "H"); len(got) != 0 {
		t.Fatalf("folded exchange shows messages: %v", got)
	}
	// A folded exchange is not a match candidate: A is a new thread, shown whole.
	if got := texts(out, "A"); !equal(got, []string{"q1"}) {
		t.Fatalf("A = %v", got)
	}
}

func TestBuild_FoldsFailedSkippedUnsupported(t *testing.T) {
	f := newFake()
	for i, p := range []string{"failed", "skipped", "unsupported_encoding"} {
		id := string(rune('A' + i))
		f.exchange(id, int64(i), p)
		f.msg(id, "user", "should not show")
	}
	out := build(t, f, timeline.Options{})
	if len(out) != 3 {
		t.Fatalf("entries = %+v", out)
	}
	for i, p := range []string{"failed", "skipped", "unsupported_encoding"} {
		e := out[i]
		if e.Kind != timeline.KindFolded || e.Parse != p || e.RequestID != string(rune('A'+i)) ||
			e.Path != "/v1/x" || e.Status != 200 {
			t.Fatalf("entry %d = %+v", i, e)
		}
	}
}

func TestBuild_PartialExchangeShowsEventsMarked(t *testing.T) {
	f := newFake()
	f.exchange("A", 1, "partial")
	f.msg("A", "user", "hello")
	f.add("A", store.EventRow{Kind: "error", Partial: true, Payload: []byte(`{"status":0,"message":"cut"}`)})
	out := build(t, f, timeline.Options{})
	if len(out) != 3 {
		t.Fatalf("entries = %+v", out)
	}
	for _, e := range out {
		if !e.Partial {
			t.Fatalf("entry %+v not marked partial", e)
		}
	}
	// A complete exchange's events are not marked.
	g := newFake()
	g.thread("A", 1, "u:q")
	for _, e := range build(t, g, timeline.Options{}) {
		if e.Partial {
			t.Fatalf("entry %+v marked partial", e)
		}
	}
}

func TestBuild_ToolEventsOfHiddenMessagesAreHidden(t *testing.T) {
	f := newFake()
	f.exchange("A", 1, "ok")
	f.msg("A", "user", "q")
	f.msg("A", "assistant", "calling")
	f.toolCall("A", "c1", "Run", `{"a":1}`)
	f.exchange("B", 2, "ok")
	f.msg("B", "user", "q")
	f.msg("B", "assistant", "calling")
	f.toolCall("B", "c1", "Run", `{"a":1}`)
	f.toolResult("B", "c1", `"out"`, false)
	f.msg("B", "user", "next")
	f.toolResult("B", "c2", `"new"`, true)
	var got []string
	for _, e := range build(t, f, timeline.Options{}) {
		if e.RequestID == "B" && e.Kind == timeline.KindToolCall {
			got = append(got, "call:"+e.ToolCallID)
		}
		if e.RequestID == "B" && e.Kind == timeline.KindToolResult {
			got = append(got, "result:"+e.ToolCallID)
		}
	}
	// B's first two messages and the call/result that follow them are in A's prefix
	// only for the call (the result follows the second message, also in the prefix):
	// both hidden; the result after "next" is new.
	if !equal(got, []string{"result:c2"}) {
		t.Fatalf("B tool events = %v", got)
	}
}

func TestBuild_CutsLongEntriesAndNotesBytes(t *testing.T) {
	f := newFake()
	f.exchange("A", 1, "ok")
	long := strings.Repeat("é", timeline.DefaultEntryLimit) // 2 bytes each: cut lands mid-rune
	f.msg("A", "user", long)
	f.toolCall("A", "c1", "Run", strings.Repeat("x", timeline.DefaultEntryLimit+50))
	f.toolResult("A", "c1", strings.Repeat("y", timeline.DefaultEntryLimit+7), false)
	out := build(t, f, timeline.Options{})
	if len(out) != 4 {
		t.Fatalf("entries = %+v", out)
	}
	m := out[1]
	if len(m.Text) > timeline.DefaultEntryLimit || m.Omitted != 2*timeline.DefaultEntryLimit-len(m.Text) {
		t.Fatalf("message cut: len %d omitted %d", len(m.Text), m.Omitted)
	}
	if strings.ContainsRune(m.Text, '�') || len(m.Text)%2 != 0 {
		t.Fatalf("cut inside a rune")
	}
	if out[2].Omitted != 50 || out[3].Omitted != 7 || len(out[2].Text) != timeline.DefaultEntryLimit {
		t.Fatalf("tool cuts: %d %d", out[2].Omitted, out[3].Omitted)
	}
}

func TestBuild_FullKeepsEverything(t *testing.T) {
	f := newFake()
	f.exchange("A", 1, "ok")
	f.msg("A", "user", strings.Repeat("a", 5000))
	f.toolCall("A", "c1", "Run", strings.Repeat("x", 5000))
	for _, e := range build(t, f, timeline.Options{Full: true}) {
		if e.Omitted != 0 {
			t.Fatalf("Full cut %+v", e.Kind)
		}
		if (e.Kind == timeline.KindMessage || e.Kind == timeline.KindToolCall) && len(e.Text) < 5000 {
			t.Fatalf("Full text len %d", len(e.Text))
		}
	}
}
