package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Content is one piece of parsed content in canonical form, keyed by its hash.
type Content struct {
	Hash  string // sha256, lowercase hex
	Bytes []byte // canonical form
}

// StoredEvent is a canonical event ready for the store: its payload refers to content
// by hash, and the columns a query filters on are lifted out of it.
type StoredEvent struct {
	Seq     int
	Kind    EventKind
	Source  Source // empty for kinds that have none
	Partial bool
	// ContentHash is a message's content, a tool call's input or a tool result's
	// content; empty for other kinds or when there is no content.
	ContentHash, ToolCallID string
	Payload                 []byte // canonical JSON with content by hash
}

// Canonicalize is the one place parsed content becomes hashes. Every JSON value is
// decoded with UseNumber and re-encoded with sorted keys, compact, without HTML
// escaping, then hashed with sha256.
//
// excluded (the parser's HashExcludedFields) is removed only from the top-level keys
// of a block and of each system entry and tool definition, never from tool input,
// tool result content or a tool event's raw JSON, where the same key is ordinary data.
//
// A message's content is its role plus each block's type and hash, in order. A
// tool_call block's hash covers {id, name, input_hash} and a tool_result block's
// {tool_call_id, is_error, content_hash}, taken from the first matching tool event
// after the message. Index, source and stop_reason are not part of it, so a response
// message and its resent copy in the next request share a hash.
//
// The stored events keep the order of events; contents are returned once per hash.
func Canonicalize(events []Event, excluded []string) ([]StoredEvent, []Content, error) {
	c := canonicalizer{excluded: excluded, seen: map[string]bool{}}
	stored := make([]StoredEvent, 0, len(events))
	for i := range events {
		se, err := c.event(events, i)
		if err != nil {
			return nil, nil, fmt.Errorf("event %d (%s): %w", i, events[i].Kind, err)
		}
		stored = append(stored, se)
	}
	return stored, c.contents, nil
}

type canonicalizer struct {
	excluded []string
	seen     map[string]bool
	contents []Content
}

func (c *canonicalizer) event(events []Event, i int) (StoredEvent, error) {
	ev := events[i]
	se := StoredEvent{Seq: i, Kind: ev.Kind, Partial: ev.Partial}
	p := map[string]any{
		"schema_version": SchemaVersion,
		"kind":           ev.Kind,
		"partial":        ev.Partial,
	}
	var err error
	switch {
	case ev.Kind == KindRequest && ev.Request != nil:
		err = c.request(ev.Request, p)
	case ev.Kind == KindMessage && ev.Message != nil:
		se.Source = ev.Message.Source
		se.ContentHash, err = c.message(events, i, p)
	case ev.Kind == KindToolCall && ev.ToolCall != nil:
		se.Source, se.ToolCallID = ev.ToolCall.Source, ev.ToolCall.ID
		se.ContentHash, err = c.toolCall(ev.ToolCall, p)
	case ev.Kind == KindToolResult && ev.ToolResult != nil:
		se.Source, se.ToolCallID = ev.ToolResult.Source, ev.ToolResult.ToolCallID
		se.ContentHash, err = c.toolResult(ev.ToolResult, p)
	case ev.Kind == KindUsage && ev.Usage != nil:
		err = c.usage(ev.Usage, p)
	case ev.Kind == KindError && ev.Error != nil:
		p["status"], p["type"], p["message"] = ev.Error.Status, ev.Error.Type, ev.Error.Message
	default:
		return se, errors.New("no payload matches the event kind")
	}
	if err != nil {
		return se, err
	}
	se.Payload, err = encode(p)
	return se, err
}

func (c *canonicalizer) request(r *RequestEvent, p map[string]any) error {
	system, err := c.store(r.System, entries)
	if err != nil {
		return fmt.Errorf("system: %w", err)
	}
	tools, err := c.store(r.Tools, entries)
	if err != nil {
		return fmt.Errorf("tools: %w", err)
	}
	names := r.ToolNames
	if names == nil {
		names = []string{}
	}
	p["model"] = r.Model
	p["stream"] = r.Stream
	p["max_tokens"] = r.MaxTokens
	p["system_hash"] = system
	p["tools_hash"] = tools
	p["has_system"] = r.HasSystem
	p["has_tools"] = r.HasTools
	p["cache_hints"] = r.CacheHints
	p["reasoning_requested"] = r.ReasoningRequested
	p["tool_names"] = names
	return nil
}

func (c *canonicalizer) message(events []Event, i int, p map[string]any) (string, error) {
	m := events[i].Message
	refs := make([]any, 0, len(m.Blocks))   // the message's content: type and hash
	blocks := make([]any, 0, len(m.Blocks)) // the payload's view, with the extras
	for j, b := range m.Blocks {
		var h string
		var err error
		switch b.Type {
		case BlockToolCall:
			h, err = c.toolCallRef(events, i, b.ToolCallID)
		case BlockToolResult:
			h, err = c.toolResultRef(events, i, b.ToolCallID)
		default:
			h, err = c.store(b.Content, object)
		}
		if err != nil {
			return "", fmt.Errorf("block %d: %w", j, err)
		}
		refs = append(refs, map[string]any{"type": b.Type, "hash": h})
		view := map[string]any{"type": b.Type, "hash": h}
		if b.Redacted {
			view["redacted"] = true
		}
		if b.ToolCallID != "" {
			view["tool_call_id"] = b.ToolCallID
		}
		blocks = append(blocks, view)
	}
	h, err := c.add(map[string]any{"role": m.Role, "blocks": refs})
	if err != nil {
		return "", err
	}
	p["index"] = m.Index
	p["role"] = m.Role
	p["source"] = m.Source
	p["stop_reason"] = m.StopReason
	p["content_hash"] = h
	p["blocks"] = blocks
	return h, nil
}

// toolCallRef hashes the reference a tool_call block stands for, from the first
// tool_call event with that id after the message.
func (c *canonicalizer) toolCallRef(events []Event, i int, id string) (string, error) {
	for _, ev := range events[i+1:] {
		if ev.Kind != KindToolCall || ev.ToolCall == nil || ev.ToolCall.ID != id {
			continue
		}
		in, err := c.store(ev.ToolCall.Input, verbatim)
		if err != nil {
			return "", fmt.Errorf("tool call %q input: %w", id, err)
		}
		return c.add(map[string]any{"id": id, "name": ev.ToolCall.Name, "input_hash": in})
	}
	return "", fmt.Errorf("no tool_call event %q follows the message", id)
}

// toolResultRef is toolCallRef for a tool_result block.
func (c *canonicalizer) toolResultRef(events []Event, i int, id string) (string, error) {
	for _, ev := range events[i+1:] {
		if ev.Kind != KindToolResult || ev.ToolResult == nil || ev.ToolResult.ToolCallID != id {
			continue
		}
		content, err := c.store(ev.ToolResult.Content, verbatim)
		if err != nil {
			return "", fmt.Errorf("tool result %q content: %w", id, err)
		}
		return c.add(map[string]any{
			"tool_call_id": id, "is_error": ev.ToolResult.IsError, "content_hash": content,
		})
	}
	return "", fmt.Errorf("no tool_result event %q follows the message", id)
}

func (c *canonicalizer) toolCall(t *ToolCallEvent, p map[string]any) (string, error) {
	in, err := c.store(t.Input, verbatim)
	if err != nil {
		return "", fmt.Errorf("input: %w", err)
	}
	raw, err := c.store(t.Raw, verbatim)
	if err != nil {
		return "", fmt.Errorf("raw: %w", err)
	}
	p["id"] = t.ID
	p["name"] = t.Name
	p["input_hash"] = in
	p["executed_by"] = t.ExecutedBy
	p["source"] = t.Source
	p["raw_hash"] = raw
	return in, nil
}

func (c *canonicalizer) toolResult(t *ToolResultEvent, p map[string]any) (string, error) {
	content, err := c.store(t.Content, verbatim)
	if err != nil {
		return "", fmt.Errorf("content: %w", err)
	}
	raw, err := c.store(t.Raw, verbatim)
	if err != nil {
		return "", fmt.Errorf("raw: %w", err)
	}
	p["tool_call_id"] = t.ToolCallID
	p["is_error"] = t.IsError
	p["content_hash"] = content
	p["executed_by"] = t.ExecutedBy
	p["source"] = t.Source
	p["raw_hash"] = raw
	return content, nil
}

func (c *canonicalizer) usage(u *UsageEvent, p map[string]any) error {
	p["input_tokens"] = u.InputTokens
	p["output_tokens"] = u.OutputTokens
	p["cache_write_tokens"] = u.CacheWriteTokens
	p["cache_read_tokens"] = u.CacheReadTokens
	p["detail"] = nil
	if len(u.Detail) > 0 {
		v, err := decode(u.Detail)
		if err != nil {
			return fmt.Errorf("detail: %w", err)
		}
		p["detail"] = v
	}
	return nil
}

// scope is where the excluded keys are removed from a content value.
type scope int

const (
	verbatim scope = iota // nowhere
	object                // the value's own top-level keys
	entries               // each array entry's top-level keys
)

// store canonicalizes raw, adds it to the contents and returns its hash. A nil or
// empty raw has no content and an empty hash.
func (c *canonicalizer) store(raw json.RawMessage, s scope) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	v, err := decode(raw)
	if err != nil {
		return "", err
	}
	switch s {
	case object:
		c.exclude(v)
	case entries:
		if arr, ok := v.([]any); ok {
			for _, e := range arr {
				c.exclude(e)
			}
		}
	case verbatim:
	}
	return c.add(v)
}

func (c *canonicalizer) exclude(v any) {
	if obj, ok := v.(map[string]any); ok {
		for _, k := range c.excluded {
			delete(obj, k)
		}
	}
}

// add encodes v canonically and records it once by hash.
func (c *canonicalizer) add(v any) (string, error) {
	b, err := encode(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:])
	if !c.seen[h] {
		c.seen[h] = true
		c.contents = append(c.contents, Content{Hash: h, Bytes: b})
	}
	return h, nil
}

// decode reads exactly one JSON value, keeping numbers as sent.
func decode(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid JSON: data after the value")
	}
	return v, nil
}

// encode is the canonical encoding: map keys sorted (encoding/json sorts them),
// compact, HTML characters kept as they are.
func encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
