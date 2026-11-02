package claudecode

import (
	"fmt"
	"sync"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

func TestMapHarness_EachInScopeEvent(t *testing.T) {
	want := map[string]core.HarnessPhase{
		"PreToolUse": core.PhasePreTool, "PostToolUse": core.PhasePostTool,
		"PostToolUseFailure": core.PhasePostToolFailed, "PostToolBatch": core.PhaseToolBatchDone,
		"UserPromptSubmit": core.PhasePrompt, "SessionStart": core.PhaseSessionStart,
		"SessionEnd": core.PhaseSessionEnd, "Stop": core.PhaseTurnEnd, "StopFailure": core.PhaseTurnFailed,
		"SubagentStart": core.PhaseAgentStart, "SubagentStop": core.PhaseAgentEnd,
		"PermissionRequest": core.PhasePermissionRequested, "PermissionDenied": core.PhasePermissionDenied,
		"Notification": core.PhaseNotice, "PreCompact": core.PhaseCompactStart, "PostCompact": core.PhaseCompactEnd,
	}
	if len(want) != 16 {
		t.Fatalf("table has %d events", len(want))
	}
	for name, phase := range want {
		body := fmt.Sprintf(`{"hook_event_name":%q,"session_id":"s1","tool_use_id":"tu1","tool_name":"Bash","tool_input":{ "command": "ls" },"agent_id":"a1","agent_type":"Explore"}`, name)
		ev := Profile{}.MapHarness([]byte(body))
		if ev.Phase != phase || ev.SourceName != name || ev.SessionKey != "s1" || ev.ToolCallID != "tu1" ||
			ev.ToolName != "Bash" || ev.AgentID != "a1" || ev.AgentType != "Explore" ||
			string(ev.ToolInput) != `{"command":"ls"}` {
			t.Errorf("%s: got %+v", name, ev)
		}
	}
}

func TestMapHarness_UnknownEventKeepsName(t *testing.T) {
	ev := Profile{}.MapHarness([]byte(`{"hook_event_name":"FutureThing","session_id":"s"}`))
	if ev.Phase != core.PhaseUnknown || ev.SourceName != "FutureThing" || ev.SessionKey != "s" {
		t.Errorf("got %+v", ev)
	}
}

func TestMapHarness_NotJSONIsUnknown(t *testing.T) {
	for _, b := range []string{"not json", "[1,2]", "null", `{"hook_event_name":`} {
		ev := Profile{}.MapHarness([]byte(b))
		if ev.Phase != core.PhaseUnknown || ev.SourceName != "" {
			t.Errorf("%q: got %+v", b, ev)
		}
	}
}

func TestMapHarness_EmptyAndNoEventName(t *testing.T) {
	for _, b := range []string{"", `{}`, `{"session_id":"s9"}`, `{"hook_event_name":5}`} {
		ev := Profile{}.MapHarness([]byte(b))
		if ev.Phase != core.PhaseUnknown || ev.SourceName != "" {
			t.Errorf("%q: got %+v", b, ev)
		}
	}
	if ev := (Profile{}).MapHarness([]byte(`{"session_id":"s9"}`)); ev.SessionKey != "s9" {
		t.Errorf("readable session lost: %+v", ev)
	}
}

func TestMapHarness_ConcurrentSubagentsKeepOwnIDs(t *testing.T) {
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("agent-%d", i)
			ev := Profile{}.MapHarness([]byte(fmt.Sprintf(`{"hook_event_name":"SubagentStart","session_id":"s","agent_id":%q,"agent_type":"t%d"}`, id, i)))
			if ev.AgentID != id || ev.AgentType != fmt.Sprintf("t%d", i) {
				t.Errorf("agent %d: got %+v", i, ev)
			}
		}()
	}
	wg.Wait()
}

func TestMapHarness_SessionIDVerbatim(t *testing.T) {
	for _, id := range []string{" Abc-DEF ", "UPPER", "aéb", ""} {
		ev := Profile{}.MapHarness([]byte(fmt.Sprintf(`{"hook_event_name":"Stop","session_id":%q}`, id)))
		if ev.SessionKey != id {
			t.Errorf("%q became %q", id, ev.SessionKey)
		}
	}
}
