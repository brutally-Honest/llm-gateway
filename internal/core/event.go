package core

import "encoding/json"

// SchemaVersion is the version of the canonical event model. Every stored event
// carries it, so a later parser change can re-parse stored raw bodies. It is separate
// from the database's schema version.
const SchemaVersion = 1

// EventKind names a canonical event.
type EventKind string

// The canonical event kinds. Everything downstream of a parser reads only these.
const (
	KindRequest    EventKind = "request"
	KindMessage    EventKind = "message"
	KindToolCall   EventKind = "tool_call"
	KindToolResult EventKind = "tool_result"
	KindUsage      EventKind = "usage"
	KindError      EventKind = "error"
)

// Source says where in the exchange a message or tool event was parsed from.
type Source string

// The event sources.
const (
	// SourceRequestHistory is the history the client sent.
	SourceRequestHistory Source = "request_history"
	// SourceResponse is the provider's reply to this exchange.
	SourceResponse Source = "response"
)

// ExecutedBy says who ran a tool.
type ExecutedBy string

// The tool origins.
const (
	// ExecutedByClient means the harness ran the tool.
	ExecutedByClient ExecutedBy = "client"
	// ExecutedByProvider means the provider ran it on its own side.
	ExecutedByProvider ExecutedBy = "provider"
)

// BlockType is the canonical type of a message content block.
type BlockType string

// The canonical block types.
const (
	BlockText       BlockType = "text"
	BlockReasoning  BlockType = "reasoning"
	BlockMedia      BlockType = "media"
	BlockToolCall   BlockType = "tool_call"
	BlockToolResult BlockType = "tool_result"
	BlockUnknown    BlockType = "unknown"
)

// Block is one content block of a message, in the order it was sent.
type Block struct {
	Type BlockType
	// Content is the block object as sent; nil for tool_call and tool_result blocks,
	// whose data lives in the tool event the block references.
	Content  json.RawMessage
	Redacted bool
	// ToolCallID links a tool_call or tool_result block to its event.
	ToolCallID string
}

// RequestEvent describes what the client asked for.
type RequestEvent struct {
	Model     string
	Stream    bool
	MaxTokens *int64
	// System and Tools are stored by hash, as system_hash and tools_hash.
	System, Tools                  json.RawMessage
	HasSystem, HasTools            bool
	CacheHints, ReasoningRequested bool
	ToolNames                      []string
}

// MessageEvent is one message of the request history or the response.
type MessageEvent struct {
	Index      int // -1 for the response message
	Role       string
	Source     Source
	StopReason string
	ResponseID string // the provider's id for the response; response message only
	Model      string // the model that answered; response message only
	Blocks     []Block
}

// ToolCallEvent is the source of truth for one tool call.
type ToolCallEvent struct {
	ID, Name   string
	Input      json.RawMessage
	ExecutedBy ExecutedBy
	Source     Source
	Raw        json.RawMessage // the block as sent
}

// ToolResultEvent is the source of truth for one tool result.
type ToolResultEvent struct {
	ToolCallID string
	IsError    bool
	Content    json.RawMessage
	ExecutedBy ExecutedBy
	Source     Source
	Raw        json.RawMessage // the block as sent
}

// UsageEvent is the final token counts as the provider reported them. A nil counter
// was not reported.
type UsageEvent struct {
	InputTokens, OutputTokens, CacheWriteTokens, CacheReadTokens *int64
	Detail                                                       json.RawMessage // any finer breakdown, as sent
}

// ErrorEvent is an upstream error, from an error status or an error in a stream.
type ErrorEvent struct {
	Status        int
	Type, Message string
}

// Event is one canonical event. Exactly one of the pointers is set, matching Kind.
// Every json.RawMessage in it must be valid JSON or nil.
type Event struct {
	Kind    EventKind
	Partial bool

	Request    *RequestEvent
	Message    *MessageEvent
	ToolCall   *ToolCallEvent
	ToolResult *ToolResultEvent
	Usage      *UsageEvent
	Error      *ErrorEvent
}
