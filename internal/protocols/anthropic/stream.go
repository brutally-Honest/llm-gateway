package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// sseEvent is one server-sent event: its name and its data lines joined.
type sseEvent struct {
	name string
	data []byte
}

// readSSE splits a decoded stream into its events. An event ends at a blank line;
// lines end in LF, CRLF or CR. A line starting with a colon is a comment. Whatever
// follows the last blank line is an event cut off before its end, and is dropped.
func readSSE(body []byte) []sseEvent {
	var (
		events []sseEvent
		name   string
		data   [][]byte
		fields bool
	)
	for {
		i := bytes.IndexAny(body, "\r\n")
		if i < 0 {
			return events
		}
		line, next := body[:i], i+1
		if body[i] == '\r' && next < len(body) && body[next] == '\n' {
			next++
		}
		body = body[next:]
		if len(line) == 0 {
			if fields {
				events = append(events, sseEvent{name: name, data: bytes.Join(data, []byte("\n"))})
			}
			name, data, fields = "", nil, false
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "event":
			name, fields = string(value), true
		case "data":
			data, fields = append(data, value), true
		}
	}
}

// wireStreamEvent is the part of a stream event's data the reassembly reads.
type wireStreamEvent struct {
	Type    string `json:"type"`
	Index   *int   `json:"index"`
	Message *struct {
		ID    string          `json:"id"`
		Model string          `json:"model"`
		Role  string          `json:"role"`
		Usage json.RawMessage `json:"usage"`
	} `json:"message"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        json.RawMessage `json:"delta"`
	Usage        json.RawMessage `json:"usage"`
	Error        *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// wireDelta is a content_block_delta's or a message_delta's delta.
type wireDelta struct {
	Type        string          `json:"type"`
	Text        string          `json:"text"`
	PartialJSON string          `json:"partial_json"`
	Thinking    string          `json:"thinking"`
	Signature   *string         `json:"signature"`
	Citation    json.RawMessage `json:"citation"`
	StopReason  *string         `json:"stop_reason"`
}

// streamEvents are the event types the reassembly reads; ping and any other type
// are skipped unread.
var streamEvents = map[string]bool{
	"message_start": true, "content_block_start": true, "content_block_delta": true,
	"content_block_stop": true, "message_delta": true, "message_stop": true, "error": true,
}

// blockBuilder reassembles one content block from its start and its deltas.
type blockBuilder struct {
	start     json.RawMessage // the block as content_block_start sent it
	changed   bool            // a delta applied
	text      strings.Builder
	hasText   bool
	thinking  strings.Builder
	hasThink  bool
	input     strings.Builder
	signature *string
	citations []json.RawMessage
	stopped   bool
}

// streamState is a stream read so far.
type streamState struct {
	id, model        string
	role, stopReason string
	started, stopped bool
	blocks           map[int]*blockBuilder
	usage            map[string]json.RawMessage
	errors           []*core.ErrorEvent
	lost             bool // a delta or stop for a block that never started
	malformed        bool // a known event whose data doesn't read
}

// parseStream reassembles a decoded SSE response into the same events a JSON
// response gives: the assistant message with its blocks whole, its tool events and
// the usage, then an error event for each error event in the stream. cut says the
// body was truncated or cut short.
//
// The parse is partial when the body was cut, the message never reached
// message_stop, a block never reached content_block_stop or its joined tool input
// isn't JSON, or a delta has no block to go to; the response events are flagged
// partial then. It is failed when a known event's data doesn't read, or a non-empty
// body holds no event at all, and the body wasn't cut.
func parseStream(status int, body []byte, cut bool) ([]core.Event, core.ParseStatus) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, bodyStatus(cut)
	}
	sse := readSSE(body)
	st := &streamState{blocks: map[int]*blockBuilder{}}
	for _, ev := range sse {
		st.read(status, ev)
	}
	if (st.malformed || len(sse) == 0) && !cut {
		return st.events(true, false), core.ParseFailed
	}
	partial := cut || !st.ended() || st.lost || st.malformed || st.badBlock()
	return st.events(partial, cut), bodyStatus(partial)
}

// badBlock says a block never reached content_block_stop or its joined tool input
// doesn't parse.
func (st *streamState) badBlock() bool {
	for _, b := range st.blocks {
		if !b.stopped || b.badInput() {
			return true
		}
	}
	return false
}

// ended says the stream reached an end: message_stop after message_start, or an
// error with no message begun.
func (st *streamState) ended() bool {
	if st.started {
		return st.stopped
	}
	return st.stopped || len(st.errors) > 0
}

// read applies one server-sent event.
func (st *streamState) read(status int, ev sseEvent) {
	name := ev.name
	if name == "" {
		var t struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(ev.data, &t) == nil {
			name = t.Type
		}
	}
	if !streamEvents[name] {
		return
	}
	var w wireStreamEvent
	if err := json.Unmarshal(ev.data, &w); err != nil {
		st.malformed = true
		return
	}
	if err := st.apply(name, status, w); err != nil {
		st.malformed = true
	}
}

var errNoIndex = errors.New("block event without an index")

func (st *streamState) apply(name string, status int, w wireStreamEvent) error {
	switch name {
	case "message_start":
		if w.Message == nil {
			return errors.New("message_start without a message")
		}
		st.started = true
		st.id, st.model, st.role = w.Message.ID, w.Message.Model, w.Message.Role
		st.mergeUsage(w.Message.Usage)
	case "content_block_start":
		if w.Index == nil {
			return errNoIndex
		}
		if present(w.ContentBlock) == nil {
			return errors.New("content_block_start without a block")
		}
		st.blocks[*w.Index] = &blockBuilder{start: w.ContentBlock}
	case "content_block_delta":
		if w.Index == nil {
			return errNoIndex
		}
		var d wireDelta
		if err := json.Unmarshal(w.Delta, &d); err != nil {
			return err
		}
		b := st.blocks[*w.Index]
		if b == nil {
			st.lost = true
			return nil
		}
		b.apply(d)
	case "content_block_stop":
		if w.Index == nil {
			return errNoIndex
		}
		if b := st.blocks[*w.Index]; b != nil {
			b.stopped = true
		} else {
			st.lost = true
		}
	case "message_delta":
		if present(w.Delta) != nil {
			var d wireDelta
			if err := json.Unmarshal(w.Delta, &d); err != nil {
				return err
			}
			if d.StopReason != nil {
				st.stopReason = *d.StopReason
			}
		}
		st.mergeUsage(w.Usage)
	case "message_stop":
		st.stopped = true
	case "error":
		e := &core.ErrorEvent{Status: status}
		if w.Error != nil {
			e.Type, e.Message = w.Error.Type, w.Error.Message
		}
		st.errors = append(st.errors, e)
	}
	return nil
}

// mergeUsage takes each key of a usage object; the last value reported wins, and a
// null is no report. A usage that isn't an object is ignored.
func (st *streamState) mergeUsage(raw json.RawMessage) {
	var fields map[string]json.RawMessage
	if present(raw) == nil || json.Unmarshal(raw, &fields) != nil {
		return
	}
	if st.usage == nil {
		st.usage = map[string]json.RawMessage{}
	}
	for k, v := range fields {
		if string(v) != "null" {
			st.usage[k] = v
		}
	}
}

// events is the assistant message, when message_start came, with its blocks in index
// order, tool events and usage flagged partial as given; then the error events,
// flagged partial only when the body was cut.
func (st *streamState) events(partial, cut bool) []core.Event {
	var events []core.Event
	if st.started {
		indexes := make([]int, 0, len(st.blocks))
		for i := range st.blocks {
			indexes = append(indexes, i)
		}
		slices.Sort(indexes)
		blocks := make([]json.RawMessage, 0, len(indexes))
		for _, i := range indexes {
			blocks = append(blocks, st.blocks[i].finish())
		}
		resp := wireResponse{
			ID: st.id, Model: st.model, Role: st.role, StopReason: st.stopReason, Content: blocks,
		}
		if st.usage != nil {
			resp.Usage, _ = json.Marshal(st.usage)
		}
		events = assistantEvents(resp, partial)
	}
	for _, e := range st.errors {
		events = append(events, core.Event{Kind: core.KindError, Error: e, Partial: cut})
	}
	return events
}

// apply adds one delta to the block. An unknown delta type is skipped.
func (b *blockBuilder) apply(d wireDelta) {
	switch d.Type {
	case "text_delta":
		b.text.WriteString(d.Text)
		b.hasText, b.changed = true, true
	case "thinking_delta":
		b.thinking.WriteString(d.Thinking)
		b.hasThink, b.changed = true, true
	case "input_json_delta":
		b.input.WriteString(d.PartialJSON)
		b.changed = true
	case "signature_delta":
		if d.Signature != nil {
			b.signature, b.changed = d.Signature, true
		}
	case "citations_delta":
		if present(d.Citation) != nil {
			b.citations, b.changed = append(b.citations, d.Citation), true
		}
	}
}

// badInput says the joined tool input can't stand as JSON: the block never reached
// content_block_stop (the input is read only there), or it doesn't parse.
func (b *blockBuilder) badInput() bool {
	if b.input.Len() == 0 {
		return false
	}
	return !b.stopped || !json.Valid([]byte(b.input.String()))
}

// finish is the block whole: as content_block_start sent it when no delta applied,
// else with its text and thinking joined onto the start's, its signature set, its
// citations appended, and its input the joined JSON. A joined input that can't be
// read (see badInput) is kept as the raw joined string.
func (b *blockBuilder) finish() json.RawMessage {
	if !b.changed {
		return b.start
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b.start, &fields) != nil || fields == nil {
		return b.start
	}
	set := func(key string, v any) {
		if raw, err := marshal(v); err == nil {
			fields[key] = raw
		}
	}
	if b.hasText {
		set("text", stringField(fields, "text")+b.text.String())
	}
	if b.hasThink {
		set("thinking", stringField(fields, "thinking")+b.thinking.String())
	}
	if b.signature != nil {
		set("signature", *b.signature)
	}
	if len(b.citations) > 0 {
		var existing []json.RawMessage
		_ = json.Unmarshal(fields["citations"], &existing) // absent or null: none yet
		set("citations", append(existing, b.citations...))
	}
	if joined := b.input.String(); joined != "" {
		if b.badInput() {
			set("input", joined)
		} else {
			fields["input"] = json.RawMessage(joined)
		}
	}
	out, err := marshal(fields)
	if err != nil {
		return b.start
	}
	return out
}

// stringField is fields[key] when it is a JSON string, else "".
func stringField(fields map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(fields[key], &s)
	return s
}

// marshal is JSON with HTML characters left as they are, so a joined text reads as
// the provider sent it.
func marshal(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
