package anthropic_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// streamRequest is the request the golden stream answers, as the replay test sends it.
const streamRequest = `{"model":"claude-sonnet-5","max_tokens":64,"stream":true,` +
	`"messages":[{"role":"user","content":"Say hello in five words."}]}`

// streamInput is one POST /v1/messages exchange with a streamed 200 response.
func streamInput(body []byte, encoding string, truncated bool) core.ParseInput {
	h := http.Header{"Content-Type": {"text/event-stream"}}
	if encoding != "" {
		h.Set("Content-Encoding", encoding)
	}
	return core.ParseInput{
		Method:            http.MethodPost,
		Path:              "/v1/messages",
		Status:            http.StatusOK,
		RequestHeader:     http.Header{"Content-Type": {"application/json"}},
		ResponseHeader:    h,
		RequestBody:       []byte(streamRequest),
		ResponseBody:      body,
		ResponseTruncated: truncated,
		Stream:            true,
		DecodeLimit:       decodeLimit,
	}
}

// parseStream parses body as the exchange's streamed response.
func parseStream(t *testing.T, body []byte, encoding string, truncated bool) core.ParseResult {
	t.Helper()
	return parser(t).Parse(streamInput(body, encoding, truncated))
}

// sse is one server-sent event with the given name and data.
func sse(name, data string) string {
	return "event: " + name + "\ndata: " + data + "\n\n"
}

// Pieces of small streams built in the tests.
const (
	msgStart = `{"type":"message_start","message":{"id":"msg_01Test","type":"message","role":"assistant",` +
		`"model":"claude-sonnet-5","content":[],"stop_reason":null,"usage":{"input_tokens":3,"output_tokens":1}}}`
	msgDelta = `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}`
	msgStop  = `{"type":"message_stop"}`
)

func blockStart(index, block string) string {
	return sse("content_block_start", `{"type":"content_block_start","index":`+index+`,"content_block":`+block+`}`)
}

func blockDelta(index, delta string) string {
	return sse("content_block_delta", `{"type":"content_block_delta","index":`+index+`,"delta":`+delta+`}`)
}

func blockStop(index string) string {
	return sse("content_block_stop", `{"type":"content_block_stop","index":`+index+`}`)
}

func inputDelta(index, partial string) string {
	b, _ := json.Marshal(partial)
	return blockDelta(index, `{"type":"input_json_delta","partial_json":`+string(b)+`}`)
}

// streamOf is a whole stream around the given block events.
func streamOf(blocks ...string) []byte {
	return []byte(sse("message_start", msgStart) + strings.Join(blocks, "") +
		sse("message_delta", msgDelta) + sse("message_stop", msgStop))
}

// responseEvents are the events the response gave: the response message, then its
// tool, usage and error events.
func responseEvents(t *testing.T, res core.ParseResult) (*core.MessageEvent, []core.Event) {
	t.Helper()
	for i, ev := range res.Events {
		if ev.Kind == core.KindMessage && ev.Message.Source == core.SourceResponse {
			return ev.Message, res.Events[i:]
		}
	}
	t.Fatalf("no response message among %d events", len(res.Events))
	return nil, nil
}

// jsonEqual reports whether a and b hold the same JSON value.
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("%s: %v", a, err)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	ja, _ := json.Marshal(va)
	jb, _ := json.Marshal(vb)
	return bytes.Equal(ja, jb)
}

// AC37: an unknown content-block type is an unknown block with its JSON as sent, and
// an unknown event (whatever its data) or delta type is skipped. The parse is ok.
func TestParse_UnknownBlockAndEventKept(t *testing.T) {
	const hologram = `{"type":"hologram","shape":"cube","sides":6}`
	body := streamOf(
		blockStart("0", hologram),
		sse("sparkle", `{"type":"sparkle","glow":1}`),
		sse("mystery", `not json at all`),
		blockStop("0"),
		blockStart("1", `{"type":"text","text":""}`),
		blockDelta("1", `{"type":"text_delta","text":"hi"}`),
		blockDelta("1", `{"type":"sparkle_delta","glow":2}`),
		sse("ping", `{"type": "ping"}`),
		blockStop("1"),
	)
	res := parseStream(t, body, "", false)
	if res.Status != core.ParseOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	msg, _ := responseEvents(t, res)
	if len(msg.Blocks) != 2 || msg.Blocks[0].Type != core.BlockUnknown || msg.Blocks[1].Type != core.BlockText {
		t.Fatalf("blocks = %+v, want unknown then text", msg.Blocks)
	}
	if string(msg.Blocks[0].Content) != hologram {
		t.Errorf("unknown block = %s, want %s as sent", msg.Blocks[0].Content, hologram)
	}
	if !jsonEqual(t, msg.Blocks[1].Content, []byte(`{"type":"text","text":"hi"}`)) {
		t.Errorf("text block = %s, want the text joined and the unknown delta skipped", msg.Blocks[1].Content)
	}
	canonical(t, res.Events)
}

// AC38: a tool input split over several input_json_delta events is joined, and read
// as JSON once, at content_block_stop.
func TestParse_ToolInputSplitAcrossDeltas(t *testing.T) {
	body := streamOf(
		blockStart("0", `{"type":"tool_use","id":"toolu_01Split","name":"Read","input":{}}`),
		inputDelta("0", ""),
		inputDelta("0", `{"file_`),
		inputDelta("0", `path": "no`),
		inputDelta("0", `tes.txt", "lim`),
		inputDelta("0", `it": 3}`),
		blockStop("0"),
	)
	res := parseStream(t, body, "", false)
	if res.Status != core.ParseOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	msg, rest := responseEvents(t, res)
	if len(msg.Blocks) != 1 || msg.Blocks[0].Type != core.BlockToolCall || msg.Blocks[0].ToolCallID != "toolu_01Split" {
		t.Fatalf("blocks = %+v, want one tool_call toolu_01Split", msg.Blocks)
	}
	if len(rest) < 2 || rest[1].Kind != core.KindToolCall {
		t.Fatalf("no tool_call event after the response message")
	}
	call := rest[1].ToolCall
	if call.ID != "toolu_01Split" || call.Name != "Read" || call.ExecutedBy != core.ExecutedByClient ||
		call.Source != core.SourceResponse || rest[1].Partial {
		t.Errorf("tool_call = %+v (partial %v)", call, rest[1].Partial)
	}
	if !jsonEqual(t, call.Input, []byte(`{"file_path":"notes.txt","limit":3}`)) {
		t.Errorf("input = %s, want the joined object", call.Input)
	}
	if !jsonEqual(t, call.Raw, []byte(`{"type":"tool_use","id":"toolu_01Split","name":"Read","input":{"file_path":"notes.txt","limit":3}}`)) {
		t.Errorf("raw block = %s, want the block with its joined input", call.Raw)
	}
	canonical(t, res.Events)
}

// AC39: a reasoning block holds its joined thinking text and the signature_delta's
// signature.
func TestParse_ThinkingSignatureKept(t *testing.T) {
	body := streamOf(
		blockStart("0", `{"type":"thinking","thinking":"","signature":""}`),
		blockDelta("0", `{"type":"thinking_delta","thinking":"Let me "}`),
		blockDelta("0", `{"type":"thinking_delta","thinking":"think."}`),
		blockDelta("0", `{"type":"signature_delta","signature":"EqQBCkYIBxgCKkBsig=="}`),
		blockStop("0"),
		blockStart("1", `{"type":"text","text":""}`),
		blockDelta("1", `{"type":"text_delta","text":"Done."}`),
		blockStop("1"),
	)
	res := parseStream(t, body, "", false)
	if res.Status != core.ParseOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	msg, _ := responseEvents(t, res)
	if len(msg.Blocks) != 2 || msg.Blocks[0].Type != core.BlockReasoning || msg.Blocks[0].Redacted {
		t.Fatalf("blocks = %+v, want reasoning (not redacted) then text", msg.Blocks)
	}
	want := `{"type":"thinking","thinking":"Let me think.","signature":"EqQBCkYIBxgCKkBsig=="}`
	if !jsonEqual(t, msg.Blocks[0].Content, []byte(want)) {
		t.Errorf("reasoning block = %s, want %s", msg.Blocks[0].Content, want)
	}
	canonical(t, res.Events)
}

// AC41: each usage counter holds the last value the stream reported; message_start
// gives the first values and every message_delta may update them. A null is not a
// report. Keys other than the four counters are the detail, last value too.
func TestParse_UsageLastValueWins(t *testing.T) {
	start := `{"type":"message_start","message":{"id":"msg_01Usage","type":"message","role":"assistant",` +
		`"content":[],"usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":5,` +
		`"cache_read_input_tokens":0,"service_tier":"standard"}}}`
	body := []byte(sse("message_start", start) +
		blockStart("0", `{"type":"text","text":""}`) +
		blockDelta("0", `{"type":"text_delta","text":"ok"}`) +
		blockStop("0") +
		sse("message_delta", `{"type":"message_delta","delta":{"stop_reason":null},"usage":{"output_tokens":7}}`) +
		sse("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20,`+
			`"input_tokens":11,"cache_read_input_tokens":3,"cache_creation_input_tokens":null,"service_tier":"priority"}}`) +
		sse("message_stop", msgStop))
	res := parseStream(t, body, "", false)
	if res.Status != core.ParseOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	msg, rest := responseEvents(t, res)
	if msg.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn", msg.StopReason)
	}
	var u *core.UsageEvent
	for _, ev := range rest {
		if ev.Kind == core.KindUsage {
			u = ev.Usage
		}
	}
	if u == nil {
		t.Fatal("no usage event")
	}
	got := []*int64{u.InputTokens, u.OutputTokens, u.CacheWriteTokens, u.CacheReadTokens}
	want := []int64{11, 20, 5, 3}
	for i, c := range got {
		if c == nil || *c != want[i] {
			t.Fatalf("usage counters = %v, want %v", got, want)
		}
	}
	if string(u.Detail) != `{"service_tier":"priority"}` {
		t.Errorf("usage detail = %s, want the last service_tier", u.Detail)
	}
}

// AC42: a stream cut mid-block yields the events up to the cut, the response ones
// flagged partial, and the parse is partial, whether the cut falls in plain text or
// inside the compressed body.
func TestParse_TruncatedStreamPartial(t *testing.T) {
	full := readFixture(t, "stream.sse")
	check := func(t *testing.T, res core.ParseResult) {
		t.Helper()
		if res.Status != core.ParsePartial {
			t.Fatalf("status = %q, want partial", res.Status)
		}
		if res.Events[0].Kind != core.KindRequest || res.Events[0].Partial {
			t.Errorf("first event = %s (partial %v), want the whole request event", res.Events[0].Kind, res.Events[0].Partial)
		}
		msg, rest := responseEvents(t, res)
		for _, ev := range rest {
			if !ev.Partial {
				t.Errorf("response %s event is not partial", ev.Kind)
			}
		}
		if msg.StopReason != "" {
			t.Errorf("stop_reason = %q from past the cut", msg.StopReason)
		}
		if len(msg.Blocks) != 1 || msg.Blocks[0].Type != core.BlockText {
			t.Fatalf("blocks = %+v, want the one text block begun before the cut", msg.Blocks)
		}
		var b struct{ Text string }
		if err := json.Unmarshal(msg.Blocks[0].Content, &b); err != nil {
			t.Fatal(err)
		}
		if b.Text == "" || !strings.HasPrefix("Hey there, good to see you!", b.Text) || b.Text == "Hey there, good to see you!" {
			t.Errorf("text = %q, want a non-empty prefix of the whole text", b.Text)
		}
		canonical(t, res.Events)
	}
	t.Run("identity", func(t *testing.T) {
		cut := full[:bytes.Index(full, []byte(`" good"`))]
		check(t, parseStream(t, cut, "", true))
	})
	t.Run("gzip", func(t *testing.T) {
		// The cut falls where the compressed bytes decode to past the first text
		// deltas but short of the block's content_block_stop.
		z := gzipped(t, full)
		from, to := bytes.Index(full, []byte(`" good"`)), bytes.Index(full, []byte("content_block_stop"))
		for n := range len(z) {
			zr, err := gzip.NewReader(bytes.NewReader(z[:n]))
			if err != nil {
				continue
			}
			out, _ := io.ReadAll(zr)
			if len(out) > from && len(out) < to {
				check(t, parseStream(t, z[:n], "gzip", true))
				return
			}
		}
		t.Fatal("no cut of the gzip body decodes to inside the text block")
	})
}

// A stream that ends before message_stop, with no truncation flag (upstream closed
// it early), parses as far as it goes and is partial.
func TestParse_StreamWithoutStopPartial(t *testing.T) {
	body := []byte(sse("message_start", msgStart) +
		blockStart("0", `{"type":"text","text":""}`) +
		blockDelta("0", `{"type":"text_delta","text":"hi"}`) +
		blockStop("0"))
	res := parseStream(t, body, "", false)
	if res.Status != core.ParsePartial {
		t.Fatalf("status = %q, want partial", res.Status)
	}
	msg, _ := responseEvents(t, res)
	if len(msg.Blocks) != 1 {
		t.Errorf("blocks = %+v, want the text block", msg.Blocks)
	}
}

// AC43: a tool input with no content_block_stop, or whose joined JSON doesn't parse,
// keeps the raw joined string as its input, and the exchange is partial, not failed.
func TestParse_TruncatedToolInputPartial(t *testing.T) {
	start := blockStart("0", `{"type":"tool_use","id":"toolu_01Cut","name":"Read","input":{}}`)
	for _, tc := range []struct {
		name      string
		body      []byte
		truncated bool
		raw       string
	}{
		{"cut_inside_input", []byte(sse("message_start", msgStart) + start + inputDelta("0", `{"file_path": "no`) +
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"inp`),
			true, `{"file_path": "no`},
		{"valid_prefix_no_stop", []byte(sse("message_start", msgStart) + start + inputDelta("0", `{"a":1}`)),
			true, `{"a":1}`},
		{"invalid_joined_json", streamOf(start, inputDelta("0", `{"file_path": `), inputDelta("0", `]`), blockStop("0")),
			false, `{"file_path": ]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := parseStream(t, tc.body, "", tc.truncated)
			if res.Status != core.ParsePartial {
				t.Fatalf("status = %q, want partial", res.Status)
			}
			msg, rest := responseEvents(t, res)
			if len(msg.Blocks) != 1 || msg.Blocks[0].Type != core.BlockToolCall || msg.Blocks[0].ToolCallID != "toolu_01Cut" {
				t.Fatalf("blocks = %+v, want the tool_call toolu_01Cut", msg.Blocks)
			}
			if len(rest) < 2 || rest[1].Kind != core.KindToolCall {
				t.Fatal("no tool_call event after the response message")
			}
			call := rest[1].ToolCall
			wantInput, _ := json.Marshal(tc.raw)
			if string(call.Input) != string(wantInput) {
				t.Errorf("input = %s, want the raw joined string %s", call.Input, wantInput)
			}
			var raw struct{ Input json.RawMessage }
			if err := json.Unmarshal(call.Raw, &raw); err != nil || string(raw.Input) != string(wantInput) {
				t.Errorf("raw block = %s, want its input to be the raw joined string", call.Raw)
			}
			if !rest[1].Partial {
				t.Error("the tool_call event is not partial")
			}
			canonical(t, res.Events)
		})
	}
}

// A known event whose data isn't JSON, in a body that wasn't cut, is malformed: the
// parse fails. So does a body with no server-sent events in it.
func TestParse_MalformedStreamFailed(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"bad_event_data", streamOf(blockStart("0", `{"type":"text"`), blockStop("0"))},
		{"not_sse", []byte("<html>bad gateway</html>\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if res := parseStream(t, tc.body, "", false); res.Status != core.ParseFailed {
				t.Errorf("status = %q, want failed", res.Status)
			}
		})
	}
	if res := parseStream(t, nil, "", false); res.Status != core.ParseOK {
		t.Errorf("empty stream: status = %q, want ok", res.Status)
	}
}

// Events split by CRLF line ends, with data over several lines and comment lines,
// read the same as plain LF ones.
func TestParse_StreamLineEnds(t *testing.T) {
	lf := readFixture(t, "stream.sse")
	want := canonical(t, parseStream(t, lf, "", false).Events)
	crlf := bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
	crlf = append([]byte(": a comment\r\n\r\n"), crlf...)
	res := parseStream(t, crlf, "", false)
	if res.Status != core.ParseOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	if got := canonical(t, res.Events); !slices.EqualFunc(got, want, storedEqual) {
		t.Errorf("CRLF stream events differ from the LF ones")
	}
	multi := []byte(strings.Replace(string(streamOf(blockStart("0", `{"type":"text","text":""}`),
		blockDelta("0", `{"type":"text_delta","text":"hi"}`), blockStop("0"))),
		`data: {"type":"content_block_delta",`, "data: {\"type\":\"content_block_delta\",\ndata: ", 1))
	res = parseStream(t, multi, "", false)
	msg, _ := responseEvents(t, res)
	if res.Status != core.ParseOK || len(msg.Blocks) != 1 || !jsonEqual(t, msg.Blocks[0].Content, []byte(`{"type":"text","text":"hi"}`)) {
		t.Errorf("multi-line data: status %q, blocks %+v", res.Status, msg.Blocks)
	}
}

// storedEqual compares two stored events field by field.
func storedEqual(a, b core.StoredEvent) bool {
	return a.Seq == b.Seq && a.Kind == b.Kind && a.Source == b.Source && a.Partial == b.Partial &&
		a.ContentHash == b.ContentHash && a.ToolCallID == b.ToolCallID && bytes.Equal(a.Payload, b.Payload)
}
