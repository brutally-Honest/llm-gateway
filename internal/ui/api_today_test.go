package ui

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
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
	// 017 E1: the totals spec 017 added are 0 too
	if m["changed"] != float64(0) || m["sites"] != float64(0) {
		t.Fatalf("today effects = %v", m)
	}
}

// 017 AC3: today's totals count the files sessions changed since the time (distinct per
// session) and the sites they connected to (distinct hosts); older effects and unassigned
// connections are left out.
func TestToday_FilesAndSites(t *testing.T) {
	e := withRules(t)
	at := func(s int) time.Time { return baseTime.Add(time.Duration(s) * time.Second) }
	file := func(key, path string, s int) core.Effect {
		return core.Effect{Kind: core.EffectFileChange, At: at(s), Principal: core.PrincipalLocal, Client: "cc",
			SessionKey: key, Root: "/w", Path: path, Change: core.FileModified, Mode: "git"}
	}
	conn := func(key, host string, s int) core.Effect {
		return core.Effect{Kind: core.EffectNetConn, At: at(s), EndedAt: at(s + 1), Principal: core.PrincipalLocal,
			Client: "cc", SessionKey: key, Host: host, Port: 443, Via: "connect", Outcome: core.NetOK}
	}
	effs := []core.Effect{
		file("k", "/w/old.go", 1), // before since
		file("k", "/w/a.go", 10), file("k", "/w/a.go", 11), file("k", "/w/b.go", 12),
		file("k2", "/w/a.go", 13), // the same path in another session counts again
		conn("k", "api.example", 10), conn("k2", "api.example", 11), conn("k2", "cdn.example", 12),
		conn("", "lost.example", 12), // unassigned
	}
	if err := e.s.SaveEffects(t.Context(), effs, nil); err != nil {
		t.Fatal(err)
	}
	m := e.today(sinceAt(5))
	if m["changed"] != float64(3) || m["sites"] != float64(2) {
		t.Fatalf("today changed = %v sites = %v, want 3 and 2", m["changed"], m["sites"])
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

// 017 AC3 (D30): Today's sessions are every session with any activity since the time,
// a model call or a hook; a hooks-only session counts, a session with both counts once,
// and a session whose hooks all came before the time does not.
func TestToday_SessionsCountHooksOnlyActivity(t *testing.T) {
	e := withRules(t)
	e.call("a", 10, 0, 3, "user:hello")                                  // session k
	e.hook("k", core.HarnessEvent{Phase: core.PhasePrompt}, 11)          // k again: still one
	e.hook("hooks-only", core.HarnessEvent{Phase: core.PhasePrompt}, 12) // counts
	e.hook("yesterday", core.HarnessEvent{Phase: core.PhasePrompt}, 1)   // before since
	e.hook("", core.HarnessEvent{Phase: core.PhaseNotice}, 13)           // no session
	m := e.today(sinceAt(5))
	if m["sessions"] != float64(2) || m["calls"] != float64(1) {
		t.Fatalf("today sessions = %v calls = %v, want 2 and 1", m["sessions"], m["calls"])
	}
}
