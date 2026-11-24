package store_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// The stubs are the exchanges since the time, in ExchangesSince order, with the columns
// the day's totals read; a parse lands as Parsed; a session-less exchange has key "".
func TestReader_ExchangeStubsSinceMatchExchangesSince(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	save := func(id, key string, at int64, ttfb bool) {
		ex := exchange(id, []byte("q"+id), []byte("r"+id))
		ex.SessionKey = key
		ex.Start = time.Unix(1700000000+at, 0)
		ex.End = ex.Start.Add(time.Second)
		ex.HasTTFB = ttfb
		if err := s.SaveExchange(t.Context(), ex); err != nil {
			t.Fatal(err)
		}
	}
	save("old", "k", 1, true)
	save("b", "k", 10, true)
	save("a", "", 10, false) // same start as b: request_id breaks the tie
	save("c", "k2", 11, true)
	if err := s.SaveParse(t.Context(), "c", core.PrincipalLocal, core.ParseOK,
		[]core.StoredEvent{{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{}`)}}, nil); err != nil {
		t.Fatal(err)
	}
	r := openReader(t, dir)
	since := time.Unix(1700000010, 0).UnixNano() // exactly at b and a: included
	view := r.WithContext(t.Context())
	got, err := view.ExchangeStubsSince(since)
	if err != nil {
		t.Fatal(err)
	}
	full, err := view.ExchangesSince(since)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || len(full) != 3 {
		t.Fatalf("stubs = %d, exchanges = %d, want 3 each", len(got), len(full))
	}
	for i, ex := range full {
		want := store.ExchangeStub{RequestID: ex.RequestID, PrincipalID: ex.PrincipalID, Protocol: ex.Protocol,
			Client: ex.Client, SessionKey: ex.SessionKey, Status: ex.Status, StartedAt: ex.StartedAt,
			TTFBNS: ex.TTFBNS, Parsed: ex.Parse != ""}
		if !reflect.DeepEqual(got[i], want) {
			t.Fatalf("stub %d = %+v, want %+v", i, got[i], want)
		}
	}
	if got[0].RequestID != "a" || got[0].SessionKey != "" || got[0].TTFBNS != nil || got[0].Parsed {
		t.Fatalf("first stub = %+v", got[0])
	}
	if !got[2].Parsed {
		t.Fatalf("parsed stub = %+v", got[2])
	}
}

// CallEvents gives the request and usage events in seq order and none of the others;
// an exchange without events gives none.
func TestReader_CallEventsOnlyRequestAndUsage(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	for _, id := range []string{"x", "y"} {
		if err := s.SaveExchange(t.Context(), exchange(id, []byte("q"), []byte("r"))); err != nil {
			t.Fatal(err)
		}
	}
	msg := []byte(`{"text":"hi"}`)
	evs := []core.StoredEvent{
		{Seq: 3, Kind: core.KindUsage, Partial: true, Payload: []byte(`{"input_tokens":1}`)},
		{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m"}`)},
		{Seq: 1, Kind: core.KindMessage, ContentHash: sum(msg), Payload: []byte(`{}`)},
		{Seq: 2, Kind: core.KindToolCall, ToolCallID: "t", Payload: []byte(`{}`)},
	}
	if err := s.SaveParse(t.Context(), "x", core.PrincipalLocal, core.ParseOK, evs,
		[]core.Content{{Hash: sum(msg), Bytes: msg}}); err != nil {
		t.Fatal(err)
	}
	r := openReader(t, dir)
	got, err := r.WithContext(t.Context()).CallEvents("x")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != "request" || got[0].Seq != 0 || string(got[0].Payload) != `{"model":"m"}` ||
		got[1].Kind != "usage" || got[1].Seq != 3 || !got[1].Partial || got[1].RequestID != "x" {
		t.Fatalf("events = %+v", got)
	}
	none, err := r.WithContext(t.Context()).CallEvents("y")
	if err != nil || len(none) != 0 {
		t.Fatalf("events of y = %v, %v", none, err)
	}
}
