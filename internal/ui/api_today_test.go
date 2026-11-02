package ui

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"
)

func sinceAt(sec int) string {
	return url.QueryEscape(baseTime.Add(time.Duration(sec) * time.Second).UTC().Format(time.RFC3339))
}

func (e *digestEnv) today(since string) map[string]any {
	e.t.Helper()
	rec := e.get("/api/today?since=" + since)
	if rec.Code != 200 {
		e.t.Fatalf("today = %d %s", rec.Code, rec.Body)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func counter(t *testing.T, m map[string]any, name string) any {
	t.Helper()
	return m[name].(map[string]any)["value"]
}

func TestToday_CountsOnlyCallsSince(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 100, "user:old")
	e.call("b", 10, 0, 7, "user:new")
	m := e.today(sinceAt(5))
	if m["calls"] != float64(1) || m["sessions"] != float64(1) || counter(t, m, "input") != float64(7) || counter(t, m, "output") != float64(1) {
		t.Fatalf("today = %v", m)
	}
}

func TestToday_CountsSideCalls(t *testing.T) {
	e := withRules(t)
	e.call("a", 10, 0, 3, "user:hello")
	e.call("b", 11, 0, 4, "user:"+titlePrompt, `assistant:{"title":"T"}`)
	m := e.today(sinceAt(5))
	if m["calls"] != float64(2) || counter(t, m, "input") != float64(7) {
		t.Fatalf("today = %v", m)
	}
}

func TestToday_EmptyStoreZeros(t *testing.T) {
	e := newAPIEnv(t)
	rec := e.get("/api/today?since=" + sinceAt(0))
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	if m["calls"] != float64(0) || m["sessions"] != float64(0) || counter(t, m, "input") != nil {
		t.Fatalf("today = %v", m)
	}
}

func TestToday_BadSinceRejected(t *testing.T) {
	e := newAPIEnv(t)
	for _, q := range []string{"", "?since=", "?since=yesterday", "?since=2024-01-01"} {
		if rec := e.get("/api/today" + q); rec.Code != 400 {
			t.Fatalf("%q = %d", q, rec.Code)
		}
	}
}

func TestToday_SessionSpanningMidnightCountsTodayCallsOnly(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 100, "user:yesterday")
	e.call("b", 2, 0, 100, "user:yesterday2")
	e.call("c", 20, 0, 5, "user:today")
	m := e.today(sinceAt(10))
	if m["calls"] != float64(1) || m["sessions"] != float64(1) || counter(t, m, "input") != float64(5) {
		t.Fatalf("today = %v", m)
	}
}
