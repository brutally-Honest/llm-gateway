package ui

import (
	"strconv"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

const (
	// entryCut is the most bytes of a text field the timeline API returns (AC18).
	entryCut = 16 * 1024
	// maxBodyBytes is the most bytes of a stored body the body route returns (AC20).
	maxBodyBytes = 5 << 20
)

// The API entry kinds (D2).
const (
	kindUserPrompt    = "user_prompt"
	kindAssistantText = "assistant_text"
	kindThinking      = "thinking"
	kindToolCall      = "tool_call"
	kindToolResult    = "tool_result"
	kindModelCall     = "model_call"
	kindError         = "error"
	kindExchange      = "exchange"
	kindMarker        = "marker"
	kindMessage       = "message" // a role other than user or assistant
)

// callUsageJSON is the usage of a model call. A call without a usage event has
// has_usage false and null counters, never zeros.
type callUsageJSON struct {
	HasUsage   bool   `json:"has_usage"`
	NoUsage    string `json:"no_usage,omitempty"` // "missing" or "errored"
	Partial    bool   `json:"partial"`
	Input      *int64 `json:"input"`
	Output     *int64 `json:"output"`
	CacheWrite *int64 `json:"cache_write"`
	CacheRead  *int64 `json:"cache_read"`
}

func callToJSON(c *timeline.CallUsage) *callUsageJSON {
	return &callUsageJSON{HasUsage: c.HasUsage, NoUsage: c.NoUsage, Partial: c.Partial,
		Input: c.Input, Output: c.Output, CacheWrite: c.CacheWrite, CacheRead: c.CacheRead}
}

// entryJSON is one timeline entry. Which fields are set depends on Kind.
type entryJSON struct {
	ID         string `json:"id"`
	ExchangeID string `json:"exchange_id"`
	Index      int    `json:"index"`
	Start      string `json:"start"`
	Kind       string `json:"kind"`

	Role       string `json:"role,omitempty"`
	Text       string `json:"text,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	IsError    *bool  `json:"is_error,omitempty"`
	Method     string `json:"method,omitempty"`
	Path       string `json:"path,omitempty"`
	Status     *int   `json:"status,omitempty"`
	Parse      string `json:"parse,omitempty"`
	Partial    bool   `json:"partial,omitempty"`
	Model      string `json:"model,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`

	Usage *callUsageJSON `json:"usage,omitempty"`
}

func ptr[T any](v T) *T { return &v }

// mapEntries turns timeline entries into API entries. A message that holds readable
// thinking gets a thinking entry before it. Indexes are recounted per exchange over
// the API entries, so `<exchange_id>:<index>` is unique and stable (AC18).
func mapEntries(entries []timeline.Entry, exs map[string]store.ExchangeRow) []entryJSON {
	out := make([]entryJSON, 0, len(entries))
	next := map[string]int{}
	add := func(e timeline.Entry, en entryJSON) {
		i := next[e.RequestID]
		next[e.RequestID] = i + 1
		en.ID = e.RequestID + ":" + strconv.Itoa(i)
		en.ExchangeID, en.Index, en.Start = e.RequestID, i, nsTime(e.Start)
		out = append(out, en)
	}
	for _, e := range entries {
		switch e.Kind {
		case timeline.KindExchange, timeline.KindFolded:
			en := entryJSON{Kind: kindExchange, Method: e.Method, Path: e.Path, Status: ptr(e.Status),
				Parse: e.Parse, Partial: e.Partial}
			if e.Call != nil {
				en.Kind, en.Model, en.Usage = kindModelCall, e.Call.Model, callToJSON(e.Call)
				if ex, ok := exs[e.RequestID]; ok {
					en.DurationMS = ptr((ex.EndedAt - ex.StartedAt) / 1e6)
				}
			}
			add(e, en)
		case timeline.KindMarker:
			add(e, entryJSON{Kind: kindMarker, Text: e.Text, Partial: e.Partial})
		case timeline.KindMessage:
			if e.Thinking != "" {
				add(e, entryJSON{Kind: kindThinking, Role: e.Role, Text: e.Thinking,
					Truncated: e.ThinkingOmitted > 0, Partial: e.Partial})
			}
			kind := kindMessage
			switch e.Role {
			case "user":
				kind = kindUserPrompt
			case "assistant":
				kind = kindAssistantText
			}
			add(e, entryJSON{Kind: kind, Role: e.Role, Text: e.Text, Truncated: e.Omitted > 0, Partial: e.Partial})
		case timeline.KindToolCall:
			add(e, entryJSON{Kind: kindToolCall, Name: e.Name, ToolCallID: e.ToolCallID, Text: e.Text,
				Truncated: e.Omitted > 0, Partial: e.Partial})
		case timeline.KindToolResult:
			add(e, entryJSON{Kind: kindToolResult, ToolCallID: e.ToolCallID, IsError: ptr(e.IsError),
				Text: e.Text, Truncated: e.Omitted > 0, Partial: e.Partial})
		case timeline.KindError:
			add(e, entryJSON{Kind: kindError, Name: e.Name, Status: ptr(e.Status), Text: e.Text,
				Truncated: e.Omitted > 0, Partial: e.Partial})
		}
	}
	return out
}
