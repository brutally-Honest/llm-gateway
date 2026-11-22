package ui

import (
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// B12: a session whose only hook events are session ends (a run that failed at once) is
// never active; a hooks-only session with other events keeps the window rule.

func TestEndOnly_ListNeverActive(t *testing.T) {
	e := newAPIEnv(t) // now is base+100s
	e.hook("failed", core.HarnessEvent{Phase: core.PhaseSessionEnd}, 95)
	e.hook("mixed", core.HarnessEvent{Phase: core.PhaseSessionStart}, 94)
	e.hook("mixed", core.HarnessEvent{Phase: core.PhaseSessionEnd}, 95)
	e.save("a", "exch", "cc", 95)

	active := map[string]bool{}
	for _, it := range e.sess("").Items {
		active[it["session_key"].(string)] = it["active"].(bool)
	}
	want := map[string]bool{"failed": false, "mixed": true, "exch": true}
	for k, w := range want {
		if got, ok := active[k]; !ok || got != w {
			t.Errorf("active[%s] = %v (present %v), want %v", k, got, ok, w)
		}
	}
	if got := e.sess("?kind=active").keys(); len(got) != 2 {
		t.Errorf("kind=active = %v, want the mixed and exchange sessions only", got)
	}
	for _, k := range e.sess("?kind=active").keys() {
		if k == "failed" {
			t.Error("kind=active lists the end-only session")
		}
	}
}

func TestEndOnly_ViewNotActiveAndCacheHits(t *testing.T) {
	e := newStreamEnv(t, nil)
	e.hook("k", core.HarnessEvent{Phase: core.PhaseSessionEnd}, 95)
	d := e.d
	d.Views = NewViewCache()
	d.Timelines = e.hub.timelines
	who := sessIdent{core.PrincipalLocal, "cc", "k"}

	v, cached, err := d.buildView(t.Context(), who, true)
	if err != nil || cached {
		t.Fatalf("first read cached=%v err=%v", cached, err)
	}
	if v.Overview.Status.Word != "ended" {
		t.Errorf("an end-only session's view status = %q, want ended", v.Overview.Status.Word)
	}
	if _, cached, err = d.buildView(t.Context(), who, true); err != nil || !cached {
		t.Fatalf("second read cached=%v err=%v, want a cache hit", cached, err)
	}
}

func TestEndOnly_ViewMixedStaysActive(t *testing.T) {
	e := newStreamEnv(t, nil)
	e.hook("k", core.HarnessEvent{Phase: core.PhaseSessionStart}, 94)
	e.hook("k", core.HarnessEvent{Phase: core.PhaseSessionEnd}, 95)
	d := e.d
	d.Views = NewViewCache()
	d.Timelines = e.hub.timelines
	who := sessIdent{core.PrincipalLocal, "cc", "k"}
	v, _, err := d.buildView(t.Context(), who, true)
	if err != nil || v.Overview.Status.Word != "active" {
		t.Fatalf("mixed hooks-only view status=%q err=%v", v.Overview.Status.Word, err)
	}
	if _, cached, _ := d.buildView(t.Context(), who, true); !cached {
		t.Error("second read missed the cache")
	}
}
