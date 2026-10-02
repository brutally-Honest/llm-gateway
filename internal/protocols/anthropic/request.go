package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// wireRequest is the part of a Messages request the parser reads, but for its
// messages, which are read one at a time. Every other field stays in the raw body; an
// unknown one is never an error.
type wireRequest struct {
	Model     string          `json:"model"`
	Stream    bool            `json:"stream"`
	MaxTokens *int64          `json:"max_tokens"`
	System    json.RawMessage `json:"system"`
	Tools     json.RawMessage `json:"tools"`
	Thinking  json.RawMessage `json:"thinking"`
}

type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// wireBlock is the part of a content block the mapping reads. The block itself is
// kept as sent.
type wireBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID *string         `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// parseRequest maps a decoded request body to its request event, then one message
// event per message (source request_history), each followed by the tool events its
// blocks reference. It reads the body token by token, so a body cut short still
// yields every message complete before the cut: on an error the events read so far
// come back with it, the request event flagged partial, since the fields after the
// cut are unseen. An error means the body is cut short or not a Messages request.
func parseRequest(body []byte) ([]core.Event, error) {
	var r requestReader
	err := r.read(body)
	if !r.started {
		return nil, err // nothing of the body was read: no request to speak of
	}
	if ferr := r.decodeFields(); err == nil {
		err = ferr
	}
	system, tools := present(r.req.System), present(r.req.Tools)
	events := []core.Event{{
		Kind:    core.KindRequest,
		Partial: err != nil,
		Request: &core.RequestEvent{
			Model:              r.req.Model,
			Stream:             r.req.Stream,
			MaxTokens:          r.req.MaxTokens,
			System:             system,
			Tools:              tools,
			HasSystem:          system != nil,
			HasTools:           tools != nil,
			CacheHints:         r.hints,
			ReasoningRequested: reasoningRequested(r.req.Thinking),
			ToolNames:          toolNames(tools),
		},
	}}
	return append(events, r.messages...), err
}

// requestReader walks a request body's top-level members. Every member but messages
// is kept whole and decoded into req once the walk ends; each message is mapped as
// soon as it is read, so the messages before a cut survive it.
type requestReader struct {
	dec      *json.Decoder
	fields   []json.RawMessage // the top-level members but messages, each as {"key":value}
	req      wireRequest
	messages []core.Event
	index    int  // the next message's index
	hints    bool // a cache_control member anywhere read so far
	started  bool // the body's first token was read
}

func (r *requestReader) read(body []byte) error {
	r.dec = json.NewDecoder(bytes.NewReader(body))
	r.dec.UseNumber()
	tok, err := r.dec.Token()
	if err != nil {
		return err
	}
	r.started = true
	switch tok {
	case nil: // a null body is an empty request, as json.Unmarshal reads it
	case json.Delim('{'):
		if err := r.members(); err != nil {
			return err
		}
	default:
		return errors.New("the request body is not an object")
	}
	if _, err := r.dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("data after the request body")
	}
	return nil
}

// members reads the request object's members up to and including its closing brace.
func (r *requestReader) members() error {
	for r.dec.More() {
		tok, err := r.dec.Token()
		if err != nil {
			return err
		}
		key, _ := tok.(string)
		if key == "cache_control" {
			r.hints = true
		}
		// json.Unmarshal matches a member name to its field ignoring case.
		if strings.EqualFold(key, "messages") {
			if err := r.messagesValue(); err != nil {
				return err
			}
			continue
		}
		var v json.RawMessage
		if err := r.dec.Decode(&v); err != nil {
			return err
		}
		r.hints = r.hints || rawHasKey(v, "cache_control")
		field, err := json.Marshal(map[string]json.RawMessage{key: v})
		if err != nil {
			return err
		}
		r.fields = append(r.fields, field)
	}
	_, err := r.dec.Token() // the closing brace
	return err
}

// messagesValue reads the messages member's value, mapping each message as it ends.
// A repeated messages member replaces the earlier one, as json.Unmarshal does.
func (r *requestReader) messagesValue() error {
	r.messages, r.index = nil, 0
	tok, err := r.dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	if tok != json.Delim('[') {
		return errors.New("messages is not an array")
	}
	for r.dec.More() {
		var raw json.RawMessage
		if err := r.dec.Decode(&raw); err != nil {
			return err
		}
		var m wireMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		r.hints = r.hints || rawHasKey(raw, "cache_control")
		r.messages = append(r.messages, messageEvents(r.index, m.Role, m.Content, core.SourceRequestHistory)...)
		r.index++
	}
	_, err = r.dec.Token() // the closing bracket
	return err
}

// decodeFields decodes the kept members into req, in the order they were sent, so a
// repeated member's last value wins, as with json.Unmarshal.
func (r *requestReader) decodeFields() error {
	for _, f := range r.fields {
		if err := json.Unmarshal(f, &r.req); err != nil {
			return err
		}
	}
	return nil
}

// present is raw, or nil when the field is absent or null.
func present(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

// reasoningRequested is true for a top-level thinking field whose type is anything
// but disabled (research Q6), and false when it is absent or null.
func reasoningRequested(raw json.RawMessage) bool {
	if present(raw) == nil {
		return false
	}
	var t struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &t) // a thinking that isn't an object has no type: not disabled
	return t.Type != "disabled"
}

// toolNames lists the name of each tool offered, in order. Entries without a string
// name are left out.
func toolNames(tools json.RawMessage) []string {
	var entries []json.RawMessage
	if json.Unmarshal(tools, &entries) != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		var t struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(e, &t) == nil && t.Name != "" {
			names = append(names, t.Name)
		}
	}
	return names
}

// rawHasKey reports whether key names an object member anywhere in raw, a complete
// JSON value.
func rawHasKey(raw json.RawMessage, key string) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	return dec.Decode(&v) == nil && walkHasKey(v, key)
}

func walkHasKey(v any, key string) bool {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			if k == key || walkHasKey(e, key) {
				return true
			}
		}
	case []any:
		for _, e := range t {
			if walkHasKey(e, key) {
				return true
			}
		}
	}
	return false
}

// messageEvents is one message event and, after it, a tool_call or tool_result event
// for each tool block, in block order. A string content is one text block.
func messageEvents(index int, role string, content json.RawMessage, src core.Source) []core.Event {
	msg := &core.MessageEvent{Index: index, Role: role, Source: src}
	events := []core.Event{{Kind: core.KindMessage, Message: msg}}
	for _, raw := range contentBlocks(content) {
		b, tool := mapBlock(raw, src)
		msg.Blocks = append(msg.Blocks, b)
		if tool != nil {
			events = append(events, *tool)
		}
	}
	return events
}

// contentBlocks splits a message's content into its blocks as sent. A string is
// one text block; any other value that isn't an array is one block, kept as sent.
func contentBlocks(content json.RawMessage) []json.RawMessage {
	content = present(content)
	if content == nil {
		return nil
	}
	var blocks []json.RawMessage
	if json.Unmarshal(content, &blocks) == nil {
		return blocks
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		b, err := json.Marshal(map[string]string{"type": "text", "text": text})
		if err == nil {
			return []json.RawMessage{b}
		}
	}
	return []json.RawMessage{content}
}

// mapBlock types one content block and, for a tool block, builds the event it
// references. A block that can't be read, or whose type isn't mapped, is unknown
// with its JSON kept.
func mapBlock(raw json.RawMessage, src core.Source) (core.Block, *core.Event) {
	var w wireBlock
	if json.Unmarshal(raw, &w) != nil {
		return core.Block{Type: core.BlockUnknown, Content: raw}, nil
	}
	switch w.Type {
	case "text":
		return core.Block{Type: core.BlockText, Content: raw}, nil
	case "thinking":
		return core.Block{Type: core.BlockReasoning, Content: raw}, nil
	case "redacted_thinking":
		return core.Block{Type: core.BlockReasoning, Content: raw, Redacted: true}, nil
	case "image", "document":
		return core.Block{Type: core.BlockMedia, Content: raw}, nil
	case "tool_use":
		return toolCall(w, raw, core.ExecutedByClient, src)
	case "server_tool_use", "mcp_tool_use":
		return toolCall(w, raw, core.ExecutedByProvider, src)
	case "tool_result":
		return toolResult(w, raw, core.ExecutedByClient, src)
	}
	if w.Type == "mcp_tool_result" || (strings.HasSuffix(w.Type, "_tool_result") && w.ToolUseID != nil) {
		return toolResult(w, raw, core.ExecutedByProvider, src)
	}
	return core.Block{Type: core.BlockUnknown, Content: raw}, nil
}

func toolCall(w wireBlock, raw json.RawMessage, by core.ExecutedBy, src core.Source) (core.Block, *core.Event) {
	return core.Block{Type: core.BlockToolCall, ToolCallID: w.ID}, &core.Event{
		Kind: core.KindToolCall,
		ToolCall: &core.ToolCallEvent{
			ID: w.ID, Name: w.Name, Input: present(w.Input),
			ExecutedBy: by, Source: src, Raw: raw,
		},
	}
}

func toolResult(w wireBlock, raw json.RawMessage, by core.ExecutedBy, src core.Source) (core.Block, *core.Event) {
	var id string
	if w.ToolUseID != nil {
		id = *w.ToolUseID
	}
	return core.Block{Type: core.BlockToolResult, ToolCallID: id}, &core.Event{
		Kind: core.KindToolResult,
		ToolResult: &core.ToolResultEvent{
			ToolCallID: id, IsError: w.IsError, Content: present(w.Content),
			ExecutedBy: by, Source: src, Raw: raw,
		},
	}
}
