package ui

import (
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// chipsOf counts the chips of a timeline's tool calls (captured ones, and hook-seen ones
// once per tool call id), reading only the API's chip field.
func chipsOf(t *testing.T, entries []map[string]any) map[string]float64 {
	t.Helper()
	out := map[string]float64{}
	seen := map[string]bool{}
	for _, en := range entries {
		chip, _ := en["chip"].(string)
		switch en["kind"] {
		case "tool_call":
			if chip == "" {
				t.Fatalf("tool call without a chip: %v", en)
			}
			out[chip]++
		case "hook_event":
			id, _ := en["tool_call_id"].(string)
			if id == "" || seen[id] {
				continue
			}
			if chip == "" {
				t.Fatalf("hook tool call without a chip: %v", en)
			}
			seen[id] = true
			out[chip]++
		}
	}
	return out
}

func assertListMatchesTimeline(t *testing.T, e *apiEnv, key string, want map[string]float64) {
	t.Helper()
	var row map[string]any
	for _, it := range e.page("/api/sessions").Items {
		if it["session_key"] == key {
			row = it
		}
	}
	if row == nil {
		t.Fatalf("session %q not listed", key)
	}
	states := row["tool_states"].(map[string]any)
	if len(states) != 6 {
		t.Fatalf("tool_states keys = %v, want the six chips", states)
	}
	got := chipsOf(t, e.timeline("/api/sessions/"+row["id"].(string)+"/timeline").Entries)
	for chip, n := range states {
		if got[chip] != n.(float64) {
			t.Errorf("chip %s: list %v, timeline %v", chip, n, got[chip])
		}
	}
	for chip, n := range want {
		if states[chip] != n {
			t.Errorf("chip %s: list %v, want %v (list %v)", chip, states[chip], n, states)
		}
	}
}

// A captured failed tool shows as failed in both views, never ok.
func TestChips_ListAndTimelineAgreeWithAFailedTool(t *testing.T) {
	e := newAPIEnv(t)
	e.toolSession(`{"a":1}`)
	e.hook("k", core.HarnessEvent{Phase: core.PhasePreTool, ToolCallID: "call_1", ToolName: "bash"}, 2)
	e.hook("k", core.HarnessEvent{Phase: core.PhasePostToolFailed, ToolCallID: "call_1", ToolName: "bash"}, 3)
	e.hook("k", core.HarnessEvent{Phase: core.PhasePostTool, ToolCallID: "ghost", ToolName: "ls"}, 4)
	got := chipsOf(t, e.timeline("/api/sessions/"+sessionID(store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "k"})+"/timeline").Entries)
	// call_1 ran but no later request reported its result, so it is pending; ghost is unmatched.
	if got["pending"] != 1 || got["unmatched"] != 1 {
		t.Fatalf("timeline chips = %v", got)
	}
	assertListMatchesTimeline(t, e, "k", map[string]float64{"pending": 1, "unmatched": 1, "ok": 0})
}

// A reported tool whose hook says failed is the failed chip in both views.
func TestChips_ReportedFailedToolIsFailed(t *testing.T) {
	e := newAPIEnv(t)
	e.toolSession(`{"a":1}`)
	e.save("b", "k", "cc", 5)
	evs := []core.StoredEvent{
		{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)},
		{Seq: 1, Kind: core.KindToolResult, ToolCallID: "call_1", ContentHash: sha("res"), Payload: []byte(`{"is_error":true}`)},
	}
	if err := e.s.SaveParse(t.Context(), "b", core.PrincipalLocal, core.ParseOK, evs,
		[]core.Content{{Hash: sha("res"), Bytes: []byte(`"boom"`)}}); err != nil {
		t.Fatal(err)
	}
	e.hook("k", core.HarnessEvent{Phase: core.PhasePostToolFailed, ToolCallID: "call_1", ToolName: "bash"}, 2)
	assertListMatchesTimeline(t, e, "k", map[string]float64{"failed": 1, "ok": 0})
}

// A hooks-only session shows every hook-seen tool call as unmatched, once per call; the
// hook-derived outcome and reason ride along on the hook entries for display only.
func TestChips_HooksOnlySession(t *testing.T) {
	e := newAPIEnv(t)
	e.hook("h", core.HarnessEvent{Phase: core.PhasePreTool, ToolCallID: "t1", ToolName: "bash"}, 1)
	e.hook("h", core.HarnessEvent{Phase: core.PhasePostToolFailed, ToolCallID: "t1", ToolName: "bash"}, 2)
	e.hook("h", core.HarnessEvent{Phase: core.PhasePostTool, ToolCallID: "t2", ToolName: "ls"}, 3)
	e.hook("h", core.HarnessEvent{Phase: core.PhasePermissionDenied, ToolCallID: "t3", ToolName: "rm"}, 4)
	assertListMatchesTimeline(t, e, "h", map[string]float64{"unmatched": 3, "failed": 0, "ok": 0, "not-run": 0, "pending": 0})
	id := sessionID(store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "h"})
	want := map[string][2]string{"t1": {"", "failed"}, "t2": {"", "succeeded"}, "t3": {"denied", ""}}
	for _, en := range e.timeline("/api/sessions/" + id + "/timeline").Entries {
		w, ok := want[en["tool_call_id"].(string)]
		if en["kind"] != "hook_event" || !ok {
			continue
		}
		reason, _ := en["reason"].(string)
		outcome, _ := en["outcome"].(string)
		if en["state"] != "ran-not-asked-or-not-captured" || reason != w[0] || outcome != w[1] {
			t.Errorf("hook entry = %v, want reason %q outcome %q", en, w[0], w[1])
		}
	}
}
