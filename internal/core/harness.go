package core

// HarnessEvent is a lifecycle event a client's harness reports beside the exchanges:
// a tool about to run, a permission asked, a turn ended. It is not a StoredEvent of an
// exchange; it is stored in its own table (ADR 0012). Every field is harness-neutral.
type HarnessEvent struct {
	Phase      HarnessPhase
	SessionKey string // the client's session id, verbatim; joins exchanges.session_key
	ToolCallID string
	ToolName   string
	ToolInput  []byte // the tool's input as sent, canonical JSON; empty when none
	AgentID    string
	AgentType  string
	// SourceName is the event's name as the client sent it. It is kept for display and
	// for an unknown phase only; nothing downstream branches on it.
	SourceName string
}

// HarnessPhase is what a harness event means, in words no client owns.
type HarnessPhase string

// The harness phases. Any event a profile cannot place is PhaseUnknown.
const (
	PhasePreTool             HarnessPhase = "pre_tool"
	PhasePermissionRequested HarnessPhase = "permission_requested"
	PhasePermissionDenied    HarnessPhase = "permission_denied"
	PhasePostTool            HarnessPhase = "post_tool"
	PhasePostToolFailed      HarnessPhase = "post_tool_failed"
	PhaseToolBatchDone       HarnessPhase = "tool_batch_done"
	PhasePrompt              HarnessPhase = "prompt"
	PhaseSessionStart        HarnessPhase = "session_start"
	PhaseSessionEnd          HarnessPhase = "session_end"
	PhaseTurnEnd             HarnessPhase = "turn_end"
	PhaseTurnFailed          HarnessPhase = "turn_failed"
	PhaseAgentStart          HarnessPhase = "agent_start"
	PhaseAgentEnd            HarnessPhase = "agent_end"
	PhaseNotice              HarnessPhase = "notice"
	PhaseCompactStart        HarnessPhase = "compact_start"
	PhaseCompactEnd          HarnessPhase = "compact_end"
	PhaseUnknown             HarnessPhase = "unknown"
)

// HarnessMapper is an optional profile interface. A profile that implements it turns
// its client's hook body into a HarnessEvent; the event names live only in that
// profile. Parsing is lenient: a body that is not JSON, or has no event name, maps to
// PhaseUnknown with whatever was readable.
type HarnessMapper interface {
	MapHarness(body []byte) HarnessEvent
}
