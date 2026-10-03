package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// wireResponse is the part of a non-streamed Messages response the parser reads.
// Every other field stays in the raw body.
type wireResponse struct {
	ID, Model  string
	Role       string
	StopReason string
	Content    []json.RawMessage
	Usage      json.RawMessage
}

// usageCounters are the four counters the usage event names; every other key of
// the usage object is the finer breakdown, kept in its detail.
var usageCounters = map[string]func(u *core.UsageEvent) **int64{
	"input_tokens":                func(u *core.UsageEvent) **int64 { return &u.InputTokens },
	"output_tokens":               func(u *core.UsageEvent) **int64 { return &u.OutputTokens },
	"cache_creation_input_tokens": func(u *core.UsageEvent) **int64 { return &u.CacheWriteTokens },
	"cache_read_input_tokens":     func(u *core.UsageEvent) **int64 { return &u.CacheReadTokens },
}

// parseJSONResponse maps a decoded, non-streamed response body to its events. A 2xx
// body is the assistant message (source response) with its tool events, then the
// usage; any other status is one error event. cut says the body was truncated or cut
// short: then whatever was read before the cut is kept and the result is partial.
// A body that doesn't read as a response and wasn't cut is failed. An empty body has
// nothing to parse.
func parseJSONResponse(status int, body []byte, cut bool) ([]core.Event, core.ParseStatus) {
	if status < 200 || status > 299 {
		return parseErrorResponse(status, body, cut)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, bodyStatus(cut)
	}
	resp, opened, err := readResponse(body)
	switch {
	case err != nil && !cut:
		return nil, core.ParseFailed
	case !opened:
		// Cut before the response object began: nothing to keep.
		return nil, core.ParsePartial
	}
	partial := err != nil || cut
	return assistantEvents(resp, partial), bodyStatus(partial)
}

// assistantEvents is the response's assistant message (source response) with its
// id, model, stop reason and blocks, then the tool event each tool block references,
// then the usage when there is a usage object. Every event is flagged partial or not
// alike. The JSON and the stream parser both end here, so a response yields the same
// events either way.
func assistantEvents(resp wireResponse, partial bool) []core.Event {
	events := messageEvents(-1, resp.Role, nil, core.SourceResponse)
	msg := events[0].Message
	msg.ResponseID, msg.Model, msg.StopReason = resp.ID, resp.Model, resp.StopReason
	for _, raw := range resp.Content {
		b, tool := mapBlock(raw, core.SourceResponse)
		msg.Blocks = append(msg.Blocks, b)
		if tool != nil {
			events = append(events, *tool)
		}
	}
	if u, ok := usageEvent(resp.Usage); ok {
		events = append(events, core.Event{Kind: core.KindUsage, Usage: u})
	}
	for i := range events {
		events[i].Partial = partial
	}
	return events
}

// parseErrorResponse is one error event for a non-2xx status, with the provider's
// type and message when the body is Anthropic's error envelope. A body that isn't
// leaves the event with the status only, and the parse failed, or partial when the
// body was cut. An empty body is the status alone.
func parseErrorResponse(status int, body []byte, cut bool) ([]core.Event, core.ParseStatus) {
	ev := &core.ErrorEvent{Status: status}
	events := []core.Event{{Kind: core.KindError, Error: ev, Partial: cut}}
	if len(bytes.TrimSpace(body)) == 0 {
		return events, bodyStatus(cut)
	}
	var env errorEnvelope
	if err := decodeWhole(body, &env); err != nil {
		if cut {
			return events, core.ParsePartial
		}
		return events, core.ParseFailed
	}
	ev.Type, ev.Message = env.Error.Type, env.Error.Message
	return events, bodyStatus(cut)
}

func bodyStatus(cut bool) core.ParseStatus {
	if cut {
		return core.ParsePartial
	}
	return core.ParseOK
}

// readResponse reads the response object token by token, so a body cut part-way
// keeps every field and every content block complete before the cut. The error says
// the body ended early or isn't a response object; resp holds what came before it,
// and opened says the object began at all.
func readResponse(body []byte) (resp wireResponse, opened bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := expectDelim(dec, '{'); err != nil {
		return resp, false, err
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return resp, true, err
		}
		key, _ := tok.(string)
		switch key {
		case "id":
			err = decodeOptional(dec, &resp.ID)
		case "model":
			err = decodeOptional(dec, &resp.Model)
		case "role":
			err = decodeOptional(dec, &resp.Role)
		case "stop_reason":
			err = decodeOptional(dec, &resp.StopReason)
		case "usage":
			err = dec.Decode(&resp.Usage)
		case "content":
			err = readBlocks(dec, &resp.Content)
		default:
			var skip json.RawMessage
			err = dec.Decode(&skip)
		}
		if err != nil {
			return resp, true, err
		}
	}
	if err := expectDelim(dec, '}'); err != nil {
		return resp, true, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return resp, true, errors.New("data after the response body")
	}
	return resp, true, nil
}

// readBlocks appends each content block, as sent, until the array ends or a block
// can't be read whole. A null content is no blocks.
func readBlocks(dec *json.Decoder, blocks *[]json.RawMessage) error {
	tok, err := dec.Token()
	if err != nil || tok == nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return errors.New("content is not an array")
	}
	for dec.More() {
		var b json.RawMessage
		if err := dec.Decode(&b); err != nil {
			return err
		}
		*blocks = append(*blocks, b)
	}
	return expectDelim(dec, ']')
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return errors.New("unexpected token in the response body")
	}
	return nil
}

// decodeOptional reads a string field that may be null.
func decodeOptional(dec *json.Decoder, s *string) error {
	var p *string
	if err := dec.Decode(&p); err != nil {
		return err
	}
	if p != nil {
		*s = *p
	}
	return nil
}

// decodeWhole decodes body into v and fails on anything after it.
func decodeWhole(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("data after the body")
	}
	return nil
}

// usageEvent is the usage object's four counters, with every other key kept as the
// detail. ok is false when there is no usage object; a counter that isn't an integer
// is left unreported.
func usageEvent(raw json.RawMessage) (*core.UsageEvent, bool) {
	var fields map[string]json.RawMessage
	if present(raw) == nil || json.Unmarshal(raw, &fields) != nil {
		return nil, false
	}
	u := &core.UsageEvent{}
	detail := map[string]json.RawMessage{}
	for k, v := range fields {
		counter, ok := usageCounters[k]
		if !ok {
			detail[k] = v
			continue
		}
		var n int64
		if json.Unmarshal(v, &n) == nil {
			*counter(u) = &n
		}
	}
	if len(detail) > 0 {
		if b, err := json.Marshal(detail); err == nil {
			u.Detail = b
		}
	}
	return u, true
}
