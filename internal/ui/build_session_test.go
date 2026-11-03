package ui

import (
	"reflect"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// countingSource counts Events reads per request.
type countingSource struct {
	timeline.Source
	reads map[string]int
}

func (c *countingSource) Events(id string) ([]store.EventRow, error) {
	c.reads[id]++
	return c.Source.Events(id)
}

func TestBuildSession_LoadsEventsOnce(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 1, "user:start")
	e.call("b", 2, 0, 1, "user:start", "assistant:ok", "user:next")
	e.call("c", 3, 0, 1, "user:start", "assistant:ok", "user:next", "assistant:k", "user:more")
	rows, err := e.d.Reader.SessionExchanges(core.PrincipalLocal, "cc", "k")
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows = %d err = %v", len(rows), err)
	}
	src := &eventsOnce{Source: &countingSource{Source: e.d.Reader, reads: map[string]int{}}, exs: rows, events: map[string][]store.EventRow{}}
	session := store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "k"}
	entries, _, err := timeline.BuildWithUsage(src, session, timeline.Options{Cut: entryCut})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := callFacts(src, entries, testRules{}, src.events); err != nil {
		t.Fatal(err)
	}
	for id, n := range src.Source.(*countingSource).reads {
		if n != 1 {
			t.Fatalf("events of %s read %d times", id, n)
		}
	}
	if len(src.Source.(*countingSource).reads) != 3 {
		t.Fatalf("reads = %v", src.Source.(*countingSource).reads)
	}
}

func TestCallFacts_UnchangedWithSharedEvents(t *testing.T) {
	built := []timeline.Entry{
		{Kind: timeline.KindExchange, RequestID: "a", Thread: 0, Call: &timeline.CallUsage{}},
		{Kind: timeline.KindExchange, RequestID: "b", Thread: 0, PrevID: "a", Call: &timeline.CallUsage{}},
	}
	src := &promptSource{}
	evA, _ := src.Events("a")
	evB, _ := src.Events("b")
	shared, err := callFacts(src, built, testRules{}, map[string][]store.EventRow{"a": evA, "b": evB})
	if err != nil {
		t.Fatal(err)
	}
	if !shared["a"].Main || shared["b"].PrevID != "a" || shared["b"].Thread != 0 {
		t.Fatalf("facts = %+v %+v", shared["a"], shared["b"])
	}
	again, _ := callFacts(src, built, testRules{}, map[string][]store.EventRow{"a": evA, "b": evB})
	if !reflect.DeepEqual(shared, again) {
		t.Fatal("facts differ between runs")
	}
}
