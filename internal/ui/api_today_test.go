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

// nowAt sets the clock the handler reads, seconds after baseTime.
func (e *digestEnv) nowAt(sec int) {
	e.d.Now = func() time.Time { return baseTime.Add(time.Duration(sec) * time.Second) }
	e.h = Handler(e.d)
	waitBackground(e.t, e.d)
}

func hourly(t *testing.T, m map[string]any) (calls, sessions []float64, h map[string]any) {
	t.Helper()
	h = m["hourly"].(map[string]any)
	f := func(k string) []float64 {
		var out []float64
		for _, v := range h[k].([]any) {
			out = append(out, v.(float64))
		}
		return out
	}
	return f("calls"), f("sessions"), h
}

func eqInts(got, want []float64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// 027 C6b: calls per whole hour from since, side calls included; the last bucket is the
// hour holding now.
func TestTodayHourly_CallsPerBucketSideCallsIncluded(t *testing.T) {
	e := withRules(t)
	const h = 3600
	e.call("a", 10, 0, 1, "user:one")
	e.call("b", h-1, 0, 1, "user:two")
	e.call("c", h, 0, 1, "user:"+titlePrompt, `assistant:{"title":"T"}`) // side call, bucket 1
	e.call("d", 3*h+5, 0, 1, "user:four")
	e.nowAt(3*h + 100)
	calls, _, hj := hourly(t, e.today(sinceAt(0)))
	if !eqInts(calls, []float64{2, 1, 0, 1}) || hj["peak_calls"] != float64(2) {
		t.Fatalf("hourly = %v", hj)
	}
	if hj["start"] != baseTime.UTC().Format(time.RFC3339) {
		t.Fatalf("start = %v", hj["start"])
	}
}

// Sessions per bucket come from exchanges and from hooks; an empty session key is not
// counted; one session counts once per bucket.
func TestTodayHourly_SessionsFromCallsAndHooks(t *testing.T) {
	e := withRules(t)
	const h = 3600
	e.save("x1", "s1", "cc", 10)
	e.save("x2", "s1", "cc", 20) // same session, same bucket
	e.save("x3", "s2", "cc", 30)
	e.save("x4", "", "cc", 40) // empty key: a call, no session
	e.hook("s1", core.HarnessEvent{Phase: core.PhasePrompt}, h+5)
	e.hook("hooks-only", core.HarnessEvent{Phase: core.PhasePrompt}, h+6)
	e.hook("", core.HarnessEvent{Phase: core.PhaseNotice}, h+7)
	e.nowAt(2*h + 1)
	calls, sessions, hj := hourly(t, e.today(sinceAt(0)))
	if !eqInts(calls, []float64{4, 0, 0}) || !eqInts(sessions, []float64{2, 2, 0}) ||
		hj["peak_calls"] != float64(4) || hj["peak_sessions"] != float64(2) {
		t.Fatalf("hourly = %v", hj)
	}
}

func TestTodayHourly_CappedAt48Buckets(t *testing.T) {
	e := withRules(t)
	e.call("a", 10, 0, 1, "user:hi")
	e.nowAt(100 * 3600)
	calls, sessions, _ := hourly(t, e.today(sinceAt(0)))
	if len(calls) != 48 || len(sessions) != 48 || calls[0] != 1 {
		t.Fatalf("len = %d %d, calls[0] = %v", len(calls), len(sessions), calls[0])
	}
}

func TestTodayHourly_NowBeforeSinceIsEmpty(t *testing.T) {
	e := withRules(t)
	e.call("a", 500, 0, 1, "user:hi")
	e.nowAt(100)
	m := e.today(sinceAt(200))
	calls, sessions, hj := hourly(t, m)
	if len(calls) != 0 || len(sessions) != 0 || hj["peak_calls"] != float64(0) || hj["peak_sessions"] != float64(0) {
		t.Fatalf("hourly = %v", hj)
	}
	if m["calls"] != float64(1) {
		t.Fatalf("the totals must not change: %v", m)
	}
}
