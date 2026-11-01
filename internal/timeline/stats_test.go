package timeline_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

var testSession = store.SessionRow{PrincipalID: "p", Client: "c", SessionKey: "k"}

func TestBuild_EntryIndexIsPositionWithinExchange(t *testing.T) {
	f := newFake()
	f.thread("A", 1, "u:q1", "a:r1")
	f.toolCall("A", "c1", "Run", `{}`)
	f.thread("B", 2, "u:q1", "a:r1", "u:q2")
	f.exchange("F", 3, "failed")
	pos := map[string]int{}
	for _, e := range build(t, f, timeline.Options{}) {
		if e.Index != pos[e.RequestID] {
			t.Fatalf("%s %s index = %d, want %d", e.RequestID, e.Kind, e.Index, pos[e.RequestID])
		}
		pos[e.RequestID]++
	}
	if pos["A"] != 4 || pos["B"] != 2 || pos["F"] != 1 {
		t.Fatalf("entries per exchange = %v", pos)
	}
}

func TestBuild_CutOptionLeavesCLIDefault(t *testing.T) {
	long := strings.Repeat("x", 5000)
	f := newFake()
	f.thread("A", 1, "u:"+long)
	text := func(o timeline.Options) timeline.Entry {
		for _, e := range build(t, f, o) {
			if e.Kind == timeline.KindMessage {
				return e
			}
		}
		t.Fatal("no message")
		return timeline.Entry{}
	}
	if e := text(timeline.Options{}); len(e.Text) != timeline.DefaultEntryLimit || e.Omitted != 3000 {
		t.Fatalf("default: len %d omitted %d", len(e.Text), e.Omitted)
	}
	if e := text(timeline.Options{Cut: 4000}); len(e.Text) != 4000 || e.Omitted != 1000 {
		t.Fatalf("cut: len %d omitted %d", len(e.Text), e.Omitted)
	}
	if e := text(timeline.Options{Cut: 10, Full: true}); len(e.Text) != 5000 || e.Omitted != 0 {
		t.Fatalf("full: len %d omitted %d", len(e.Text), e.Omitted)
	}
}

func TestBuild_ThinkingTextFromStoredBlock(t *testing.T) {
	f := newFake()
	f.exchange("A", 1, "ok")
	think := f.put(`{"type":"thinking","thinking":"hmm"}`)
	// A reasoning block whose stored content holds no readable thinking text.
	unreadable := f.put(`{"data":"zzz"}`)
	text := f.put(`{"type":"text","text":"hi"}`)
	mk := func(blocks ...map[string]string) {
		p, _ := json.Marshal(map[string]any{"role": "assistant", "blocks": blocks})
		f.add("A", store.EventRow{Kind: "message", ContentHash: hashOf(string(p)), Payload: p})
	}
	mk(map[string]string{"hash": think, "type": "reasoning"}, map[string]string{"hash": text, "type": "text"})
	mk(map[string]string{"hash": unreadable, "type": "reasoning"})
	var got []timeline.Entry
	for _, e := range build(t, f, timeline.Options{}) {
		if e.Kind == timeline.KindMessage {
			got = append(got, e)
		}
	}
	if len(got) != 2 {
		t.Fatalf("message entries = %+v, want 2", got)
	}
	if got[0].Thinking != "hmm" || got[0].Text != "[reasoning]\nhi" {
		t.Fatalf("readable block: entry = %+v", got[0])
	}
	// No readable thinking text: nothing is invented, so no thinking entry follows from it.
	if got[1].Thinking != "" || got[1].ThinkingOmitted != 0 || got[1].Text != "[reasoning]" {
		t.Fatalf("unreadable block: entry = %+v", got[1])
	}
}

func TestBuild_EarlierStartedExchangeChangesLaterEntries(t *testing.T) {
	// Later alone: its messages are all new.
	f := newFake()
	f.thread("B", 20, "u:q1", "a:r1", "u:q2")
	before := texts(build(t, f, timeline.Options{}), "B")
	// An exchange that started earlier is stored afterwards; B now shows only its diff.
	g := newFake()
	g.thread("A", 10, "u:q1", "a:r1")
	g.thread("B", 20, "u:q1", "a:r1", "u:q2")
	after := texts(build(t, g, timeline.Options{}), "B")
	if equal(before, after) || !equal(after, []string{"q2"}) {
		t.Fatalf("before %v after %v", before, after)
	}
}

func TestSessionStats_MatchesBuildCountsAndUsage(t *testing.T) {
	f := newFake()
	f.thread("A", 1, "u:q1", "a:r1")
	f.request("A", "m1")
	f.toolCall("A", "c1", "Run", `{}`)
	f.usage("A", false, n(10), n(5), nil, nil)
	f.thread("B", 2, "u:q1", "a:r1", "u:q2")
	f.toolCall("B", "c2", "Run", `{}`)
	f.request("B", "m1")
	f.add("B", store.EventRow{Kind: "error", Payload: []byte(`{"status":500,"message":"bad"}`)})
	f.exchange("F", 3, "failed") // unparsed: folded
	f.exchange("G", 4, "pending")
	f.request("G", "m2") // call without usage: flagged missing, never 0
	f.exchange("H", 5, "failed")
	f.request("H", "m2")

	entries, sum, err := timeline.BuildWithUsage(f, testSession, timeline.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var calls, tools, errs int
	for _, e := range entries {
		switch e.Kind {
		case timeline.KindToolCall:
			tools++
		case timeline.KindError:
			errs++
		}
		if e.Call != nil {
			calls++
		}
	}
	st, err := timeline.SessionStats(f, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if st.ModelCalls != calls || st.ToolCalls != tools || st.Errors != errs {
		t.Fatalf("stats %+v, build calls %d tools %d errors %d", st, calls, tools, errs)
	}
	if st.ToolCalls != 2 || st.Errors != 1 || st.ModelCalls != 4 {
		t.Fatalf("unexpected counts %+v", st)
	}
	if st.Usage.Calls != sum.Calls || st.Usage.Total.Missing != sum.Total.Missing || st.Usage.Total.Missing == 0 ||
		*st.Usage.Total.Input.Value != *sum.Total.Input.Value || len(st.Usage.Models) != len(sum.Models) {
		t.Fatalf("usage %+v vs %+v", st.Usage, sum)
	}
}
