package claudecode

import (
	"bytes"
	"encoding/json"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// phases maps Claude Code hook event names to harness-neutral phases. This table is the
// only place the event names live.
var phases = map[string]core.HarnessPhase{
	"PreToolUse":         core.PhasePreTool,
	"PostToolUse":        core.PhasePostTool,
	"PostToolUseFailure": core.PhasePostToolFailed,
	"PostToolBatch":      core.PhaseToolBatchDone,
	"UserPromptSubmit":   core.PhasePrompt,
	"SessionStart":       core.PhaseSessionStart,
	"SessionEnd":         core.PhaseSessionEnd,
	"Stop":               core.PhaseTurnEnd,
	"StopFailure":        core.PhaseTurnFailed,
	"SubagentStart":      core.PhaseAgentStart,
	"SubagentStop":       core.PhaseAgentEnd,
	"PermissionRequest":  core.PhasePermissionRequested,
	"PermissionDenied":   core.PhasePermissionDenied,
	"Notification":       core.PhaseNotice,
	"PreCompact":         core.PhaseCompactStart,
	"PostCompact":        core.PhaseCompactEnd,
}

// MapHarness reads a Claude Code hook body leniently. A body that is not a JSON object,
// or has no readable hook_event_name, maps to PhaseUnknown with whatever was readable;
// a field of the wrong type is left empty. session_id is passed verbatim.
func (Profile) MapHarness(body []byte) core.HarnessEvent {
	ev := core.HarnessEvent{Phase: core.PhaseUnknown}
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return ev
	}
	str := func(k string) string {
		var s string
		if json.Unmarshal(m[k], &s) != nil {
			return ""
		}
		return s
	}
	ev.SourceName = str("hook_event_name")
	ev.SessionKey = str("session_id")
	ev.ToolCallID = str("tool_use_id")
	ev.ToolName = str("tool_name")
	ev.AgentID = str("agent_id")
	ev.AgentType = str("agent_type")
	ev.WorkDir = str("cwd")
	if raw, ok := m["tool_input"]; ok {
		var buf bytes.Buffer
		if json.Compact(&buf, raw) == nil && buf.String() != "null" {
			ev.ToolInput = buf.Bytes()
		}
	}
	if p, ok := phases[ev.SourceName]; ok {
		ev.Phase = p
	}
	return ev
}

var _ core.HarnessMapper = Profile{}
