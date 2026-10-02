package anthropic_test

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/protocols/anthropic"
)

// decodeLimit is capture.max_body_bytes' default, handed to the parser as the
// pipeline does.
const decodeLimit = 32 << 20

// parser is the Anthropic adapter's parser, as core finds it.
func parser(t *testing.T) core.Parser {
	t.Helper()
	var a core.Adapter = anthropic.Adapter{}
	pa, ok := a.(core.ParsingAdapter)
	if !ok {
		t.Fatal("anthropic.Adapter does not implement core.ParsingAdapter")
	}
	p := pa.Parser()
	if p == nil {
		t.Fatal("Parser() = nil")
	}
	return p
}

// parseMessages parses one POST /v1/messages exchange with the given request body
// and an empty 200 response.
func parseMessages(t *testing.T, body string) core.ParseResult {
	t.Helper()
	return parser(t).Parse(core.ParseInput{
		Method:         http.MethodPost,
		Path:           "/v1/messages",
		Status:         http.StatusOK,
		RequestHeader:  http.Header{"Content-Type": {"application/json"}},
		ResponseHeader: http.Header{},
		RequestBody:    []byte(body),
		DecodeLimit:    decodeLimit,
	})
}

// gzipped is b gzip-compressed.
func gzipped(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// requestPayload parses body, canonicalizes its events with the parser's exclusions,
// and returns the request event's stored payload.
func requestPayload(t *testing.T, body string) map[string]any {
	t.Helper()
	res := parseMessages(t, body)
	if res.Status != core.ParseOK {
		t.Fatalf("parse status = %q, want ok", res.Status)
	}
	stored, _, err := core.Canonicalize(res.Events, parser(t).HashExcludedFields())
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	for _, se := range stored {
		if se.Kind != core.KindRequest {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal(se.Payload, &p); err != nil {
			t.Fatalf("request payload: %v", err)
		}
		return p
	}
	t.Fatalf("no request event among %d events", len(stored))
	return nil
}

func TestParse_HashExcludedFields(t *testing.T) {
	got := parser(t).HashExcludedFields()
	if len(got) != 1 || got[0] != "cache_control" {
		t.Errorf("HashExcludedFields() = %q, want [cache_control]", got)
	}
}

// AC45: system_hash follows the system array's content, not its cache_control, and
// tools_hash is independent of it.
func TestParse_SystemChangeChangesHash(t *testing.T) {
	const tools = `[{"name":"Read","description":"read a file","input_schema":{"type":"object"}}]`
	req := func(system string) string {
		return `{"model":"m","max_tokens":8,"system":` + system + `,"tools":` + tools +
			`,"messages":[{"role":"user","content":"hi"}]}`
	}
	a := requestPayload(t, req(`[{"type":"text","text":"You are A."}]`))
	b := requestPayload(t, req(`[{"type":"text","text":"You are B."}]`))
	cached := requestPayload(t, req(`[{"type":"text","text":"You are A.","cache_control":{"type":"ephemeral"}}]`))

	for name, p := range map[string]map[string]any{"a": a, "b": b, "cached": cached} {
		if h, _ := p["system_hash"].(string); len(h) != 64 {
			t.Errorf("%s: system_hash = %v, want a sha256", name, p["system_hash"])
		}
		if h, _ := p["tools_hash"].(string); len(h) != 64 {
			t.Errorf("%s: tools_hash = %v, want a sha256", name, p["tools_hash"])
		}
		if p["has_system"] != true || p["has_tools"] != true {
			t.Errorf("%s: has_system %v, has_tools %v, want both true", name, p["has_system"], p["has_tools"])
		}
	}
	if a["system_hash"] == b["system_hash"] {
		t.Errorf("different systems share system_hash %v", a["system_hash"])
	}
	if a["tools_hash"] != b["tools_hash"] {
		t.Errorf("same tools, tools_hash %v and %v", a["tools_hash"], b["tools_hash"])
	}
	if a["system_hash"] != cached["system_hash"] {
		t.Errorf("cache_control changed system_hash: %v and %v", a["system_hash"], cached["system_hash"])
	}
	if a["cache_hints"] != false || cached["cache_hints"] != true {
		t.Errorf("cache_hints = %v without cache_control and %v with it, want false and true",
			a["cache_hints"], cached["cache_hints"])
	}
}

// AC46: reasoning_requested follows the top-level thinking field.
func TestParse_ReasoningRequestedFlag(t *testing.T) {
	cases := []struct {
		name, thinking string
		want           bool
	}{
		{"absent", ``, false},
		{"disabled", `,"thinking":{"type":"disabled"}`, false},
		{"enabled", `,"thinking":{"type":"enabled","budget_tokens":1024}`, true},
		{"unknown_type", `,"thinking":{"type":"some_future_mode"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := requestPayload(t, `{"model":"m","max_tokens":2048`+tc.thinking+
				`,"messages":[{"role":"user","content":"hi"}]}`)
			if p["reasoning_requested"] != tc.want {
				t.Errorf("reasoning_requested = %v, want %v", p["reasoning_requested"], tc.want)
			}
		})
	}
}

// AC47: every exchange but POST /v1/messages is skipped, with no events.
func TestParse_OtherPathsSkipped(t *testing.T) {
	cases := []struct {
		name, method, path, body string
	}{
		{"count_tokens", http.MethodPost, "/v1/messages/count_tokens",
			`{"model":"m","messages":[{"role":"user","content":"hi"}]}`},
		{"/v1/models", http.MethodGet, "/v1/models", ``},
		{"HEAD /api/hello", http.MethodHead, "/api/hello", ``},
		{"get_messages", http.MethodGet, "/v1/messages", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := parser(t).Parse(core.ParseInput{
				Method:         tc.method,
				Path:           tc.path,
				Status:         http.StatusOK,
				RequestHeader:  http.Header{},
				ResponseHeader: http.Header{"Content-Type": {"application/json"}},
				RequestBody:    []byte(tc.body),
				ResponseBody:   []byte(`{"data":[]}`),
				DecodeLimit:    decodeLimit,
			})
			if res.Status != core.ParseSkipped || len(res.Events) != 0 {
				t.Errorf("status %q with %d events, want skipped with none", res.Status, len(res.Events))
			}
		})
	}
}

// The request's fields become the request event: model, stream, max_tokens, tool
// names, and the presence flags.
func TestParse_RequestEvent(t *testing.T) {
	p := requestPayload(t, `{"model":"claude-x","stream":true,"max_tokens":32000,"metadata":{"k":"v"},`+
		`"tools":[{"name":"Read","input_schema":{}},{"type":"web_search_20250305","name":"web_search"}],`+
		`"messages":[{"role":"user","content":"hi"}]}`)
	if p["model"] != "claude-x" || p["stream"] != true {
		t.Errorf("model %v, stream %v, want claude-x and true", p["model"], p["stream"])
	}
	if p["max_tokens"] != float64(32000) {
		t.Errorf("max_tokens = %v, want 32000", p["max_tokens"])
	}
	names, _ := p["tool_names"].([]any)
	if len(names) != 2 || names[0] != "Read" || names[1] != "web_search" {
		t.Errorf("tool_names = %v, want [Read web_search]", p["tool_names"])
	}
	if p["has_system"] != false || p["system_hash"] != "" {
		t.Errorf("has_system %v, system_hash %v, want false and empty", p["has_system"], p["system_hash"])
	}

	bare := requestPayload(t, `{"model":"m","messages":[]}`)
	if bare["max_tokens"] != nil || bare["has_tools"] != false || bare["tools_hash"] != "" {
		t.Errorf("bare request: max_tokens %v, has_tools %v, tools_hash %v, want null, false, empty",
			bare["max_tokens"], bare["has_tools"], bare["tools_hash"])
	}
	if names, _ := bare["tool_names"].([]any); len(names) != 0 {
		t.Errorf("bare request: tool_names = %v, want none", bare["tool_names"])
	}
}

// cache_hints is set by a cache_control at any depth of the request body.
func TestParse_CacheHintsAnyDepth(t *testing.T) {
	p := requestPayload(t, `{"model":"m","messages":[{"role":"user","content":[`+
		`{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`)
	if p["cache_hints"] != true {
		t.Errorf("cache_hints = %v with cache_control in a message block, want true", p["cache_hints"])
	}
}

// Each request message becomes a message event with source request_history, its
// blocks typed in order, followed by its tool events.
func TestParse_RequestHistory(t *testing.T) {
	body := `{"model":"m","max_tokens":8,"messages":[` +
		`{"role":"user","content":"hello"},` +
		`{"role":"assistant","content":[` +
		`{"type":"thinking","thinking":"hmm","signature":"sig"},` +
		`{"type":"redacted_thinking","data":"opaque=="},` +
		`{"type":"text","text":"reading"},` +
		`{"type":"tool_use","id":"toolu_1","name":"Read","input":{"path":"notes.txt"}},` +
		`{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"go"}},` +
		`{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[]},` +
		`{"type":"mcp_tool_use","id":"mcptoolu_1","name":"echo","server_name":"s","input":{}},` +
		`{"type":"mcp_tool_result","tool_use_id":"mcptoolu_1","is_error":true,"content":[{"type":"text","text":"no"}]},` +
		`{"type":"search_result","source":"s","title":"t","content":[]},` +
		`{"type":"some_future_block","x":1}]},` +
		`{"role":"user","content":[` +
		`{"type":"tool_result","tool_use_id":"toolu_1","content":"three lines"},` +
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}},` +
		`{"type":"document","source":{"type":"text","media_type":"text/plain","data":"d"}}]}]}`
	res := parseMessages(t, body)
	if res.Status != core.ParseOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}

	var kinds []core.EventKind
	for _, ev := range res.Events {
		kinds = append(kinds, ev.Kind)
	}
	want := []core.EventKind{
		core.KindRequest,
		core.KindMessage,
		core.KindMessage, core.KindToolCall, core.KindToolCall, core.KindToolResult, core.KindToolCall, core.KindToolResult,
		core.KindMessage, core.KindToolResult,
	}
	if len(kinds) != len(want) {
		t.Fatalf("event kinds = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("event kinds = %v, want %v", kinds, want)
		}
	}

	user := res.Events[1].Message
	if user.Index != 0 || user.Role != "user" || user.Source != core.SourceRequestHistory ||
		len(user.Blocks) != 1 || user.Blocks[0].Type != core.BlockText {
		t.Errorf("first message = %+v, want index 0, user, request_history, one text block", user)
	}

	asst := res.Events[2].Message
	wantBlocks := []struct {
		typ      core.BlockType
		redacted bool
		id       string
	}{
		{core.BlockReasoning, false, ""},
		{core.BlockReasoning, true, ""},
		{core.BlockText, false, ""},
		{core.BlockToolCall, false, "toolu_1"},
		{core.BlockToolCall, false, "srvtoolu_1"},
		{core.BlockToolResult, false, "srvtoolu_1"},
		{core.BlockToolCall, false, "mcptoolu_1"},
		{core.BlockToolResult, false, "mcptoolu_1"},
		{core.BlockUnknown, false, ""},
		{core.BlockUnknown, false, ""},
	}
	if asst.Index != 1 || asst.Role != "assistant" || len(asst.Blocks) != len(wantBlocks) {
		t.Fatalf("assistant message = %+v, want index 1 with %d blocks", asst, len(wantBlocks))
	}
	for i, w := range wantBlocks {
		b := asst.Blocks[i]
		if b.Type != w.typ || b.Redacted != w.redacted || b.ToolCallID != w.id {
			t.Errorf("block %d = {%s redacted:%v id:%q}, want {%s redacted:%v id:%q}",
				i, b.Type, b.Redacted, b.ToolCallID, w.typ, w.redacted, w.id)
		}
		isTool := w.typ == core.BlockToolCall || w.typ == core.BlockToolResult
		if isTool != (b.Content == nil) {
			t.Errorf("block %d (%s): content %s, want nil only for tool blocks", i, b.Type, b.Content)
		}
	}
	if string(asst.Blocks[1].Content) != `{"type":"redacted_thinking","data":"opaque=="}` {
		t.Errorf("redacted block content = %s, want the block as sent", asst.Blocks[1].Content)
	}

	call := res.Events[3].ToolCall
	if call.ID != "toolu_1" || call.Name != "Read" || call.ExecutedBy != core.ExecutedByClient ||
		call.Source != core.SourceRequestHistory || string(call.Input) != `{"path":"notes.txt"}` ||
		string(call.Raw) != `{"type":"tool_use","id":"toolu_1","name":"Read","input":{"path":"notes.txt"}}` {
		t.Errorf("tool_use event = %+v", call)
	}
	for _, i := range []int{4, 6} {
		if tc := res.Events[i].ToolCall; tc.ExecutedBy != core.ExecutedByProvider {
			t.Errorf("event %d (%s) executed_by = %q, want provider", i, tc.ID, tc.ExecutedBy)
		}
	}
	if tr := res.Events[5].ToolResult; tr.ToolCallID != "srvtoolu_1" || tr.ExecutedBy != core.ExecutedByProvider {
		t.Errorf("web_search_tool_result event = %+v, want srvtoolu_1 by provider", tr)
	}
	if tr := res.Events[7].ToolResult; tr.ToolCallID != "mcptoolu_1" || !tr.IsError || tr.ExecutedBy != core.ExecutedByProvider {
		t.Errorf("mcp_tool_result event = %+v, want mcptoolu_1, is_error, by provider", tr)
	}

	last := res.Events[8].Message
	if last.Index != 2 || len(last.Blocks) != 3 || last.Blocks[0].Type != core.BlockToolResult ||
		last.Blocks[0].ToolCallID != "toolu_1" || last.Blocks[1].Type != core.BlockMedia || last.Blocks[2].Type != core.BlockMedia {
		t.Errorf("last message = %+v, want tool_result, media, media", last)
	}
	if tr := res.Events[9].ToolResult; tr.ToolCallID != "toolu_1" || tr.IsError || tr.ExecutedBy != core.ExecutedByClient ||
		tr.Source != core.SourceRequestHistory || string(tr.Content) != `"three lines"` {
		t.Errorf("tool_result event = %+v", tr)
	}

	// The events satisfy core's contract: every tool block has its event.
	if _, _, err := core.Canonicalize(res.Events, parser(t).HashExcludedFields()); err != nil {
		t.Errorf("Canonicalize: %v", err)
	}
}

// A string content and the same text as a block give one content hash.
func TestParse_StringContentIsTextBlock(t *testing.T) {
	hashOf := func(content string) string {
		res := parseMessages(t, `{"model":"m","messages":[{"role":"user","content":`+content+`}]}`)
		stored, _, err := core.Canonicalize(res.Events, parser(t).HashExcludedFields())
		if err != nil {
			t.Fatalf("Canonicalize: %v", err)
		}
		for _, se := range stored {
			if se.Kind == core.KindMessage {
				return se.ContentHash
			}
		}
		t.Fatal("no message event")
		return ""
	}
	if a, b := hashOf(`"hi"`), hashOf(`[{"type":"text","text":"hi"}]`); a != b {
		t.Errorf("string content hash %s, block content hash %s, want equal", a, b)
	}
}

// The request body is decoded through the shared helper before it is parsed.
func TestParse_RequestEncodings(t *testing.T) {
	const body = `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	in := func(enc string, b []byte) core.ParseInput {
		return core.ParseInput{
			Method:         http.MethodPost,
			Path:           "/v1/messages",
			Status:         http.StatusOK,
			RequestHeader:  http.Header{"Content-Encoding": {enc}},
			ResponseHeader: http.Header{},
			RequestBody:    b,
			DecodeLimit:    decodeLimit,
		}
	}
	t.Run("gzip", func(t *testing.T) {
		res := parser(t).Parse(in("gzip", gzipped(t, []byte(body))))
		if res.Status != core.ParseOK || len(res.Events) != 2 {
			t.Errorf("status %q with %d events, want ok with 2", res.Status, len(res.Events))
		}
	})
	t.Run("unknown_encoding", func(t *testing.T) {
		res := parser(t).Parse(in("x-custom", []byte(body)))
		if res.Status != core.ParseUnsupportedEncoding || len(res.Events) != 0 {
			t.Errorf("status %q with %d events, want unsupported_encoding with none", res.Status, len(res.Events))
		}
	})
	t.Run("cut_short", func(t *testing.T) {
		i := in("gzip", gzipped(t, []byte(body)))
		i.DecodeLimit = 10
		if res := parser(t).Parse(i); res.Status != core.ParsePartial {
			t.Errorf("status %q, want partial", res.Status)
		}
	})
}

// A malformed request body fails the parse; a truncated one, or one sealed before its
// end (request_incomplete), is partial.
func TestParse_MalformedRequest(t *testing.T) {
	const cut = `{"model":"m","messages":[{"role":"user","con`
	in := core.ParseInput{
		Method:         http.MethodPost,
		Path:           "/v1/messages",
		Status:         http.StatusOK,
		RequestHeader:  http.Header{},
		ResponseHeader: http.Header{},
		RequestBody:    []byte(cut),
		DecodeLimit:    decodeLimit,
	}
	if res := parser(t).Parse(in); res.Status != core.ParseFailed {
		t.Errorf("malformed: status %q, want failed", res.Status)
	}
	in.RequestTruncated = true
	if res := parser(t).Parse(in); res.Status != core.ParsePartial {
		t.Errorf("truncated: status %q, want partial", res.Status)
	}
	in.RequestTruncated, in.RequestIncomplete = false, true
	if res := parser(t).Parse(in); res.Status != core.ParsePartial {
		t.Errorf("request_incomplete: status %q, want partial", res.Status)
	}
}

// A request body cut in the middle of the messages array parses up to the cut: the
// request event, flagged partial, then every message complete before the cut with its
// tool events. The message the cut falls in has no event, and the parse is partial.
func TestParse_TruncatedRequestPartial(t *testing.T) {
	const cut = `{"model":"m","max_tokens":8,"messages":[` +
		`{"role":"user","content":"one"},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"par`
	res := parser(t).Parse(core.ParseInput{
		Method:           http.MethodPost,
		Path:             "/v1/messages",
		Status:           http.StatusOK,
		RequestHeader:    http.Header{},
		ResponseHeader:   http.Header{},
		RequestBody:      []byte(cut),
		RequestTruncated: true,
		DecodeLimit:      decodeLimit,
	})
	if res.Status != core.ParsePartial {
		t.Fatalf("status %q, want partial", res.Status)
	}
	want := []core.EventKind{core.KindRequest, core.KindMessage, core.KindMessage, core.KindToolCall}
	if len(res.Events) != len(want) {
		t.Fatalf("%d events, want %d: %+v", len(res.Events), len(want), res.Events)
	}
	for i, ev := range res.Events {
		if ev.Kind != want[i] {
			t.Errorf("event %d is %s, want %s", i, ev.Kind, want[i])
		}
	}
	req := res.Events[0]
	if !req.Partial || req.Request.Model != "m" || req.Request.MaxTokens == nil || *req.Request.MaxTokens != 8 {
		t.Errorf("request event = %+v (partial %v), want partial with model m and max_tokens 8",
			req.Request, req.Partial)
	}
	for i, ev := range res.Events[1:3] {
		if ev.Partial || ev.Message.Index != i || ev.Message.Source != core.SourceRequestHistory {
			t.Errorf("message event %d = %+v (partial %v), want whole message %d from history",
				i, ev.Message, ev.Partial, i)
		}
	}
	if call := res.Events[3].ToolCall; call.ID != "toolu_1" {
		t.Errorf("tool_call id = %q, want toolu_1", call.ID)
	}
}

// update rewrites the golden event files instead of comparing against them.
var update = flag.Bool("update", false, "rewrite testdata golden files")

// readFixture reads a file under testdata.
func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// parseExchange parses one POST /v1/messages exchange with a non-streamed JSON
// response of the given status.
func parseExchange(t *testing.T, reqBody, resBody []byte, status int, resTruncated bool) core.ParseResult {
	t.Helper()
	return parser(t).Parse(core.ParseInput{
		Method:            http.MethodPost,
		Path:              "/v1/messages",
		Status:            status,
		RequestHeader:     http.Header{"Content-Type": {"application/json"}},
		ResponseHeader:    http.Header{"Content-Type": {"application/json"}},
		RequestBody:       reqBody,
		ResponseBody:      resBody,
		ResponseTruncated: resTruncated,
		DecodeLimit:       decodeLimit,
	})
}

// goldenEvent is a stored event as the golden files hold it.
type goldenEvent struct {
	Seq         int             `json:"seq"`
	Kind        core.EventKind  `json:"kind"`
	Source      core.Source     `json:"source,omitempty"`
	Partial     bool            `json:"partial"`
	ContentHash string          `json:"content_hash,omitempty"`
	ToolCallID  string          `json:"tool_call_id,omitempty"`
	Payload     json.RawMessage `json:"payload"`
}

// canonical canonicalizes events with the parser's exclusions, as the pipeline does.
func canonical(t *testing.T, events []core.Event) []core.StoredEvent {
	t.Helper()
	stored, _, err := core.Canonicalize(events, parser(t).HashExcludedFields())
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	return stored
}

// checkGolden compares the stored events with the golden file, or rewrites it under
// -update.
func checkGolden(t *testing.T, name string, stored []core.StoredEvent) {
	t.Helper()
	gold := make([]goldenEvent, 0, len(stored))
	for _, se := range stored {
		gold = append(gold, goldenEvent{
			Seq: se.Seq, Kind: se.Kind, Source: se.Source, Partial: se.Partial,
			ContentHash: se.ContentHash, ToolCallID: se.ToolCallID, Payload: se.Payload,
		})
	}
	got, err := json.MarshalIndent(gold, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", filepath.FromSlash(name))
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("events differ from %s:\n got: %s\nwant: %s", path, got, want)
	}
}

// AC35: a non-streamed JSON response yields the response message with its stop
// reason, its tool events and the usage: the same events as its streamed equivalent,
// testdata/non_streaming/response.sse. The golden file pins what they are.
func TestParse_NonStreaming(t *testing.T) {
	res := parseExchange(t, readFixture(t, "non_streaming/request.json"),
		readFixture(t, "non_streaming/response.json"), http.StatusOK, false)
	if res.Status != core.ParseOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	var kinds []core.EventKind
	for _, ev := range res.Events {
		kinds = append(kinds, ev.Kind)
		if ev.Partial {
			t.Errorf("%s event is partial", ev.Kind)
		}
	}
	want := []core.EventKind{core.KindRequest, core.KindMessage, core.KindMessage, core.KindToolCall, core.KindUsage}
	if !slices.Equal(kinds, want) {
		t.Fatalf("event kinds = %v, want %v", kinds, want)
	}

	msg := res.Events[2].Message
	if msg.Index != -1 || msg.Role != "assistant" || msg.Source != core.SourceResponse || msg.StopReason != "tool_use" {
		t.Errorf("response message = index %d, role %q, source %q, stop_reason %q; want -1, assistant, response, tool_use",
			msg.Index, msg.Role, msg.Source, msg.StopReason)
	}
	if len(msg.Blocks) != 2 || msg.Blocks[0].Type != core.BlockText ||
		msg.Blocks[1].Type != core.BlockToolCall || msg.Blocks[1].ToolCallID != "toolu_01SyntheticRead" {
		t.Errorf("response blocks = %+v, want text then the tool_call toolu_01SyntheticRead", msg.Blocks)
	}
	call := res.Events[3].ToolCall
	if call.ID != "toolu_01SyntheticRead" || call.Name != "Read" || string(call.Input) != `{"file_path":"notes.txt"}` ||
		call.ExecutedBy != core.ExecutedByClient || call.Source != core.SourceResponse {
		t.Errorf("tool_call = %+v", call)
	}
	u := res.Events[4].Usage
	counters := []*int64{u.InputTokens, u.OutputTokens, u.CacheWriteTokens, u.CacheReadTokens}
	wantCounts := []int64{12, 58, 0, 41642}
	for i, c := range counters {
		if c == nil || *c != wantCounts[i] {
			t.Fatalf("usage counters = %v, want %v", counters, wantCounts)
		}
	}
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(u.Detail, &detail); err != nil {
		t.Fatalf("usage detail %s: %v", u.Detail, err)
	}
	if _, ok := detail["cache_creation"]; !ok || string(detail["service_tier"]) != `"standard"` {
		t.Errorf("usage detail = %s, want the cache_creation breakdown and service_tier", u.Detail)
	}
	if _, ok := detail["output_tokens"]; ok {
		t.Errorf("usage detail = %s repeats a counter", u.Detail)
	}

	stored := canonical(t, res.Events)
	checkGolden(t, "non_streaming/events.golden.json", stored)

	in := streamInput(readFixture(t, "non_streaming/response.sse"), "", false)
	in.RequestBody = readFixture(t, "non_streaming/request.json")
	streamed := parser(t).Parse(in)
	if streamed.Status != core.ParseOK {
		t.Fatalf("streamed equivalent: status = %q, want ok", streamed.Status)
	}
	if got := canonical(t, streamed.Events); !slices.EqualFunc(got, stored, storedEqual) {
		t.Errorf("streamed equivalent's events differ from the JSON response's:\n got: %s\nwant: %s",
			payloads(got), payloads(stored))
	}
}

// Q19: the response message carries the provider's response id and the model that
// answered, equal whether the response came as JSON or streamed; request history
// messages carry neither. Both land in the stored payload.
func TestParse_ResponseIDAndModel(t *testing.T) {
	const wantID, wantModel = "msg_01SyntheticNonStreaming", "claude-sonnet-5"
	request := readFixture(t, "non_streaming/request.json")
	jsonRes := parseExchange(t, request, readFixture(t, "non_streaming/response.json"), http.StatusOK, false)
	in := streamInput(readFixture(t, "non_streaming/response.sse"), "", false)
	in.RequestBody = request
	streamRes := parser(t).Parse(in)

	for name, res := range map[string]core.ParseResult{"json": jsonRes, "stream": streamRes} {
		t.Run(name, func(t *testing.T) {
			if res.Status != core.ParseOK {
				t.Fatalf("status = %q, want ok", res.Status)
			}
			msg, _ := responseEvents(t, res)
			if msg.ResponseID != wantID || msg.Model != wantModel {
				t.Errorf("response message id, model = %q, %q; want %q, %q", msg.ResponseID, msg.Model, wantID, wantModel)
			}
			for _, ev := range res.Events {
				if ev.Kind == core.KindMessage && ev.Message.Source == core.SourceRequestHistory &&
					(ev.Message.ResponseID != "" || ev.Message.Model != "") {
					t.Errorf("history message %d has id %q, model %q; want neither",
						ev.Message.Index, ev.Message.ResponseID, ev.Message.Model)
				}
			}
			for _, se := range canonical(t, res.Events) {
				if se.Kind != core.KindMessage || se.Source != core.SourceResponse {
					continue
				}
				var p struct {
					ResponseID string `json:"response_id"`
					Model      string `json:"model"`
				}
				if err := json.Unmarshal(se.Payload, &p); err != nil {
					t.Fatalf("payload %s: %v", se.Payload, err)
				}
				if p.ResponseID != wantID || p.Model != wantModel {
					t.Errorf("stored payload %s: want response_id %q and model %q", se.Payload, wantID, wantModel)
				}
			}
		})
	}
	jm, _ := responseEvents(t, jsonRes)
	sm, _ := responseEvents(t, streamRes)
	if jm.ResponseID != sm.ResponseID || jm.Model != sm.Model {
		t.Errorf("json gives %q, %q; stream gives %q, %q", jm.ResponseID, jm.Model, sm.ResponseID, sm.Model)
	}
}

// payloads lists the stored events' payloads, for a failure message.
func payloads(stored []core.StoredEvent) string {
	var b strings.Builder
	for _, se := range stored {
		b.Write(se.Payload)
		b.WriteByte('\n')
	}
	return b.String()
}

// AC36: an upstream error status with Anthropic's error body is one error event with
// the provider's type and message: from an error status's body, or from a stream that
// ends with an error event.
func TestParse_UpstreamError(t *testing.T) {
	t.Run("status_429", func(t *testing.T) {
		res := parseExchange(t, readFixture(t, "non_streaming/request.json"),
			readFixture(t, "error/response_429.json"), http.StatusTooManyRequests, false)
		if res.Status != core.ParseOK {
			t.Fatalf("status = %q, want ok", res.Status)
		}
		var errs []*core.ErrorEvent
		for _, ev := range res.Events {
			switch ev.Kind {
			case core.KindError:
				errs = append(errs, ev.Error)
			case core.KindUsage:
				t.Error("an error response has a usage event")
			case core.KindMessage:
				if ev.Message.Source == core.SourceResponse {
					t.Error("an error response has a response message")
				}
			}
		}
		if len(errs) != 1 {
			t.Fatalf("got %d error events, want 1", len(errs))
		}
		want := core.ErrorEvent{Status: http.StatusTooManyRequests, Type: "rate_limit_error",
			Message: "Number of request tokens has exceeded your per-minute rate limit."}
		if *errs[0] != want {
			t.Errorf("error event = %+v, want %+v", *errs[0], want)
		}
		if res.Events[len(res.Events)-1].Kind != core.KindError {
			t.Error("the error event is not after the request events")
		}
		canonical(t, res.Events)
	})
	t.Run("sse_error_event", func(t *testing.T) {
		res := parseStream(t, readFixture(t, "error/stream_error.sse"), "", false)
		// The text block never got its content_block_stop.
		if res.Status != core.ParsePartial {
			t.Fatalf("status = %q, want partial", res.Status)
		}
		last := res.Events[len(res.Events)-1]
		if last.Kind != core.KindError {
			t.Fatalf("last event = %s, want the error event", last.Kind)
		}
		want := core.ErrorEvent{Status: http.StatusOK, Type: "overloaded_error", Message: "Overloaded"}
		if *last.Error != want {
			t.Errorf("error event = %+v, want %+v", *last.Error, want)
		}
		msg, _ := responseEvents(t, res)
		if len(msg.Blocks) != 1 || !jsonEqual(t, msg.Blocks[0].Content, []byte(`{"type":"text","text":"Hello"}`)) {
			t.Errorf("blocks = %+v, want the text before the error", msg.Blocks)
		}
		canonical(t, res.Events)
	})
}

// A non-streamed body flagged truncated keeps the blocks complete before the cut and
// is partial; the same bytes not flagged truncated are malformed, so failed.
func TestParse_TruncatedJSONResponse(t *testing.T) {
	req := readFixture(t, "non_streaming/request.json")
	full := readFixture(t, "non_streaming/response.json")
	cut := full[:bytes.Index(full, []byte(`"name":"Read"`))]

	res := parseExchange(t, req, cut, http.StatusOK, true)
	if res.Status != core.ParsePartial {
		t.Fatalf("truncated: status = %q, want partial", res.Status)
	}
	var msg *core.MessageEvent
	for _, ev := range res.Events {
		switch {
		case ev.Kind == core.KindMessage && ev.Message.Source == core.SourceResponse:
			msg = ev.Message
			if !ev.Partial {
				t.Error("the response message of a truncated body is not partial")
			}
		case ev.Kind == core.KindToolCall, ev.Kind == core.KindUsage:
			t.Errorf("a %s event from past the cut", ev.Kind)
		}
	}
	if msg == nil {
		t.Fatal("no response message from the truncated body")
	}
	if len(msg.Blocks) != 1 || msg.Blocks[0].Type != core.BlockText ||
		string(msg.Blocks[0].Content) != `{"type":"text","text":"I'll read the file."}` {
		t.Errorf("blocks = %+v, want only the text block complete before the cut", msg.Blocks)
	}
	if msg.Role != "assistant" {
		t.Errorf("role = %q, want assistant, read before the cut", msg.Role)
	}
	canonical(t, res.Events)

	if res := parseExchange(t, req, cut, http.StatusOK, false); res.Status != core.ParseFailed {
		t.Errorf("malformed, not truncated: status = %q, want failed", res.Status)
	}
	if res := parseExchange(t, req, append(bytes.Clone(full), `{}`...), http.StatusOK, false); res.Status != core.ParseFailed {
		t.Errorf("data after the body: status = %q, want failed", res.Status)
	}
	if res := parseExchange(t, req, []byte(`[1]`), http.StatusOK, false); res.Status != core.ParseFailed {
		t.Errorf("not an object: status = %q, want failed", res.Status)
	}
}

// An error status whose body is not Anthropic's error envelope still yields an error
// event with the status; the body is malformed, so the parse is failed, or partial
// when it was truncated.
func TestParse_UpstreamErrorBodyUnreadable(t *testing.T) {
	req := readFixture(t, "non_streaming/request.json")
	for _, tc := range []struct {
		name      string
		body      string
		truncated bool
		want      core.ParseStatus
	}{
		{"html", `<html>bad gateway</html>`, false, core.ParseFailed},
		{"truncated", `{"type":"error","error":{"type":"overloaded_er`, true, core.ParsePartial},
		{"empty", ``, false, core.ParseOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := parseExchange(t, req, []byte(tc.body), http.StatusBadGateway, tc.truncated)
			if res.Status != tc.want {
				t.Errorf("status = %q, want %q", res.Status, tc.want)
			}
			last := res.Events[len(res.Events)-1]
			if last.Kind != core.KindError || *last.Error != (core.ErrorEvent{Status: http.StatusBadGateway}) {
				t.Errorf("last event = %+v, want an error event with status 502 only", last)
			}
		})
	}
}

// A streamed response is read by the stream parser, never as JSON.
func TestParse_StreamedResponseNotReadAsJSON(t *testing.T) {
	in := core.ParseInput{
		Method:         http.MethodPost,
		Path:           "/v1/messages",
		Status:         http.StatusOK,
		RequestHeader:  http.Header{},
		ResponseHeader: http.Header{"Content-Type": {"text/event-stream"}},
		RequestBody:    readFixture(t, "non_streaming/request.json"),
		ResponseBody:   readFixture(t, "stream.sse"),
		Stream:         true,
		DecodeLimit:    decodeLimit,
	}
	if res := parser(t).Parse(in); res.Status == core.ParseFailed {
		t.Errorf("status = %q for an SSE body", res.Status)
	}
}

// A response the gateway wrote itself is not an upstream error: upstream sent no
// status and no body, so the parser reads no error event and no response events from
// it, whatever the status. The request events stand.
func TestParse_GatewayMadeResponseNoError(t *testing.T) {
	req := readFixture(t, "non_streaming/request.json")
	envelope := func(reason string) []byte {
		_, body := anthropic.Adapter{}.ErrorBody(reason)
		return body
	}
	for _, tc := range []struct {
		name   string
		status int
		body   []byte
	}{
		{"client_left_499", 499, nil},
		{"upstream_unreachable_502", http.StatusBadGateway, envelope("upstream_unreachable")},
		{"upstream_timeout_504", http.StatusGatewayTimeout, envelope("upstream_timeout")},
		{"client_body_400", http.StatusBadRequest, envelope("client_body")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := parser(t).Parse(core.ParseInput{
				Method:          http.MethodPost,
				Path:            "/v1/messages",
				Status:          tc.status,
				RequestHeader:   http.Header{"Content-Type": {"application/json"}},
				ResponseHeader:  http.Header{"Content-Type": {"application/json"}},
				RequestBody:     req,
				ResponseBody:    tc.body,
				GatewayResponse: true,
				DecodeLimit:     decodeLimit,
			})
			if res.Status != core.ParseOK {
				t.Errorf("status = %q, want ok", res.Status)
			}
			var sawRequest bool
			for _, ev := range res.Events {
				switch {
				case ev.Kind == core.KindRequest:
					sawRequest = true
				case ev.Kind == core.KindError:
					t.Errorf("a gateway-made response has an error event: %+v", *ev.Error)
				case ev.Kind == core.KindUsage,
					ev.Kind == core.KindMessage && ev.Message.Source == core.SourceResponse:
					t.Errorf("a gateway-made response has a %s response event", ev.Kind)
				}
			}
			if !sawRequest {
				t.Error("the request event is missing")
			}
		})
	}
}

// AC30: the committed golden stream yields the request, the user message, the
// reassembled assistant message and the usage, with parse ok.
func TestParse_GoldenStream(t *testing.T) {
	res := parseStream(t, readFixture(t, "stream.sse"), "", false)
	if res.Status != core.ParseOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	var kinds []core.EventKind
	for _, ev := range res.Events {
		kinds = append(kinds, ev.Kind)
		if ev.Partial {
			t.Errorf("%s event is partial", ev.Kind)
		}
	}
	want := []core.EventKind{core.KindRequest, core.KindMessage, core.KindMessage, core.KindUsage}
	if !slices.Equal(kinds, want) {
		t.Fatalf("event kinds = %v, want %v", kinds, want)
	}
	msg := res.Events[2].Message
	if msg.Index != -1 || msg.Role != "assistant" || msg.Source != core.SourceResponse || msg.StopReason != "end_turn" {
		t.Errorf("response message = index %d, role %q, source %q, stop_reason %q; want -1, assistant, response, end_turn",
			msg.Index, msg.Role, msg.Source, msg.StopReason)
	}
	if len(msg.Blocks) != 1 || msg.Blocks[0].Type != core.BlockText ||
		!jsonEqual(t, msg.Blocks[0].Content, []byte(`{"type":"text","text":"Hey there, good to see you!"}`)) {
		t.Errorf("blocks = %+v, want the joined text", msg.Blocks)
	}
	u := res.Events[3].Usage
	counters := []*int64{u.InputTokens, u.OutputTokens, u.CacheWriteTokens, u.CacheReadTokens}
	wantCounts := []int64{2, 12, 41642, 0}
	for i, c := range counters {
		if c == nil || *c != wantCounts[i] {
			t.Fatalf("usage counters = %v, want %v", counters, wantCounts)
		}
	}
	checkGolden(t, "stream.events.golden.json", canonical(t, res.Events))
}

// AC31: the golden stream gzipped, as Anthropic really sends it, yields the same
// events as the identity-encoded one.
func TestParse_GoldenStreamGzip(t *testing.T) {
	body := readFixture(t, "stream.sse")
	want := canonical(t, parseStream(t, body, "", false).Events)
	if len(want) != 4 {
		t.Fatalf("the identity stream gave %d events, want 4", len(want))
	}
	res := parseStream(t, gzipped(t, body), "gzip", false)
	if res.Status != core.ParseOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	if got := canonical(t, res.Events); !slices.EqualFunc(got, want, storedEqual) {
		t.Errorf("gzip events differ from identity:\n got: %s\nwant: %s", payloads(got), payloads(want))
	}
}

// AC40: a redacted_thinking block is a reasoning block with redacted set and its
// data kept byte for byte, streamed (it arrives whole in content_block_start) or not.
func TestParse_RedactedThinkingKept(t *testing.T) {
	const block = `{"type":"redacted_thinking","data":"EmwKAhgBEgy3va+/=="}`
	check := func(t *testing.T, res core.ParseResult) {
		t.Helper()
		if res.Status != core.ParseOK {
			t.Fatalf("status = %q, want ok", res.Status)
		}
		msg, _ := responseEvents(t, res)
		if len(msg.Blocks) != 2 || msg.Blocks[0].Type != core.BlockReasoning || !msg.Blocks[0].Redacted {
			t.Fatalf("blocks = %+v, want a redacted reasoning block then text", msg.Blocks)
		}
		if string(msg.Blocks[0].Content) != block {
			t.Errorf("block = %s, want %s byte for byte", msg.Blocks[0].Content, block)
		}
		canonical(t, res.Events)
	}
	t.Run("streamed", func(t *testing.T) {
		check(t, parseStream(t, streamOf(blockStart("0", block), blockStop("0"),
			blockStart("1", `{"type":"text","text":""}`),
			blockDelta("1", `{"type":"text_delta","text":"ok"}`), blockStop("1")), "", false))
	})
	t.Run("non_streamed", func(t *testing.T) {
		resp := `{"type":"message","role":"assistant","content":[` + block +
			`,{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
		check(t, parseExchange(t, []byte(streamRequest), []byte(resp), http.StatusOK, false))
	})
}

// AC44: the golden stream in each supported content coding decodes and parses ok,
// with the identity events; an unknown coding is unsupported_encoding, and no
// response event is read from its raw bytes.
func TestParse_ContentEncodings(t *testing.T) {
	body := readFixture(t, "stream.sse")
	want := canonical(t, parseStream(t, body, "", false).Events)
	if len(want) != 4 {
		t.Fatalf("the identity stream gave %d events, want 4", len(want))
	}
	encode := func(t *testing.T, newWriter func(io.Writer) (io.WriteCloser, error)) []byte {
		t.Helper()
		var buf bytes.Buffer
		w, err := newWriter(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	for _, tc := range []struct {
		name, encoding string
		newWriter      func(io.Writer) (io.WriteCloser, error)
	}{
		{"gzip", "gzip", func(w io.Writer) (io.WriteCloser, error) { return gzip.NewWriter(w), nil }},
		{"deflate_zlib", "deflate", func(w io.Writer) (io.WriteCloser, error) { return zlib.NewWriter(w), nil }},
		{"deflate_raw", "deflate", func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, flate.DefaultCompression) }},
		{"br", "br", func(w io.Writer) (io.WriteCloser, error) { return brotli.NewWriter(w), nil }},
		{"zstd", "zstd", func(w io.Writer) (io.WriteCloser, error) { return zstd.NewWriter(w) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := parseStream(t, encode(t, tc.newWriter), tc.encoding, false)
			if res.Status != core.ParseOK {
				t.Fatalf("status = %q, want ok", res.Status)
			}
			if got := canonical(t, res.Events); !slices.EqualFunc(got, want, storedEqual) {
				t.Errorf("events differ from identity:\n got: %s\nwant: %s", payloads(got), payloads(want))
			}
		})
	}
	t.Run("unknown_encoding", func(t *testing.T) {
		res := parseStream(t, body, "x-custom", false)
		if res.Status != core.ParseUnsupportedEncoding {
			t.Fatalf("status = %q, want unsupported_encoding", res.Status)
		}
		for _, ev := range res.Events {
			fromRequest := ev.Kind == core.KindRequest ||
				ev.Kind == core.KindMessage && ev.Message.Source == core.SourceRequestHistory
			if !fromRequest {
				t.Errorf("a %s event read from an undecoded body", ev.Kind)
			}
		}
	})
}

// The recorded tool turn's client call, as tool_turn/response.sse streams it and
// tool_turn/next_request.json resends it.
const (
	turnToolID    = "toolu_019bEfdhX7BCUviMriXT6Dpj"
	turnToolInput = `{"file_path":"/tmp/t19/work/notes.txt"}`
)

// toolEvents are the tool_call and tool_result events among events, in order.
func toolEvents(events []core.Event) (calls []*core.ToolCallEvent, results []*core.ToolResultEvent) {
	for _, ev := range events {
		switch ev.Kind {
		case core.KindToolCall:
			calls = append(calls, ev.ToolCall)
		case core.KindToolResult:
			results = append(results, ev.ToolResult)
		}
	}
	return calls, results
}

// storedTool is the part of a stored tool_call or tool_result payload the tool tests
// read.
type storedTool struct {
	ID         string          `json:"id"`
	ToolCallID string          `json:"tool_call_id"`
	ExecutedBy core.ExecutedBy `json:"executed_by"`
	Source     core.Source     `json:"source"`
}

// storedTools are the stored payloads of the tool_call and tool_result events, by
// kind, in order, as the pipeline would store them.
func storedTools(t *testing.T, events []core.Event) map[core.EventKind][]storedTool {
	t.Helper()
	out := map[core.EventKind][]storedTool{}
	for _, se := range canonical(t, events) {
		if se.Kind != core.KindToolCall && se.Kind != core.KindToolResult {
			continue
		}
		var p storedTool
		if err := json.Unmarshal(se.Payload, &p); err != nil {
			t.Fatalf("payload %s: %v", se.Payload, err)
		}
		out[se.Kind] = append(out[se.Kind], p)
	}
	return out
}

// AC32: the recorded tool turn's streamed answer is a tool_call with its id, name and
// input, run by the client, from the response. The next request resends that call in
// its history (source request_history) and answers it with a tool_result whose
// tool_call_id is the wire's tool_use_id.
func TestParse_ToolUseTurn(t *testing.T) {
	t.Run("response", func(t *testing.T) {
		res := parseStream(t, readFixture(t, "tool_turn/response.sse"), "", false)
		if res.Status != core.ParseOK {
			t.Fatalf("status = %q, want ok", res.Status)
		}
		msg, rest := responseEvents(t, res)
		if msg.StopReason != "tool_use" || len(msg.Blocks) != 1 ||
			msg.Blocks[0].Type != core.BlockToolCall || msg.Blocks[0].ToolCallID != turnToolID {
			t.Fatalf("response message = %+v, want one tool_call block for %s, stop_reason tool_use", msg, turnToolID)
		}
		calls, results := toolEvents(rest)
		if len(calls) != 1 || len(results) != 0 {
			t.Fatalf("response gave %d tool calls and %d results, want 1 and 0", len(calls), len(results))
		}
		c := calls[0]
		if c.ID != turnToolID || c.Name != "Read" || !jsonEqual(t, c.Input, []byte(turnToolInput)) ||
			c.ExecutedBy != core.ExecutedByClient || c.Source != core.SourceResponse {
			t.Errorf("tool_call = {id %q name %q input %s executed_by %q source %q}, want {%s Read %s client response}",
				c.ID, c.Name, c.Input, c.ExecutedBy, c.Source, turnToolID, turnToolInput)
		}
		stored := storedTools(t, res.Events)
		want := []storedTool{{ID: turnToolID, ExecutedBy: core.ExecutedByClient, Source: core.SourceResponse}}
		if !slices.Equal(stored[core.KindToolCall], want) {
			t.Errorf("stored tool_call payloads = %+v, want %+v", stored[core.KindToolCall], want)
		}
	})

	t.Run("next_request", func(t *testing.T) {
		res := parseMessages(t, string(readFixture(t, "tool_turn/next_request.json")))
		if res.Status != core.ParseOK {
			t.Fatalf("status = %q, want ok", res.Status)
		}
		calls, results := toolEvents(res.Events)
		if len(calls) != 1 || len(results) != 1 {
			t.Fatalf("next request gave %d tool calls and %d results, want 1 and 1", len(calls), len(results))
		}
		c := calls[0]
		if c.ID != turnToolID || c.Name != "Read" || !jsonEqual(t, c.Input, []byte(turnToolInput)) ||
			c.ExecutedBy != core.ExecutedByClient || c.Source != core.SourceRequestHistory {
			t.Errorf("resent tool_call = {id %q name %q input %s executed_by %q source %q}, want {%s Read %s client request_history}",
				c.ID, c.Name, c.Input, c.ExecutedBy, c.Source, turnToolID, turnToolInput)
		}
		r := results[0]
		if r.ToolCallID != turnToolID || r.IsError || r.ExecutedBy != core.ExecutedByClient ||
			r.Source != core.SourceRequestHistory || !jsonEqual(t, r.Content, []byte(`"1\talpha\n2\tbravo\n3\tcharlie\n4\t"`)) {
			t.Errorf("tool_result = {tool_call_id %q is_error %v executed_by %q source %q content %s}, want {%s false client request_history the file's lines}",
				r.ToolCallID, r.IsError, r.ExecutedBy, r.Source, r.Content, turnToolID)
		}
		var wire struct {
			ToolUseID string `json:"tool_use_id"`
		}
		if err := json.Unmarshal(r.Raw, &wire); err != nil || wire.ToolUseID != r.ToolCallID {
			t.Errorf("tool_call_id %q, wire tool_use_id %q (%v): want them equal", r.ToolCallID, wire.ToolUseID, err)
		}
		stored := storedTools(t, res.Events)
		wantCalls := []storedTool{{ID: turnToolID, ExecutedBy: core.ExecutedByClient, Source: core.SourceRequestHistory}}
		wantResults := []storedTool{{ToolCallID: turnToolID, ExecutedBy: core.ExecutedByClient, Source: core.SourceRequestHistory}}
		if !slices.Equal(stored[core.KindToolCall], wantCalls) || !slices.Equal(stored[core.KindToolResult], wantResults) {
			t.Errorf("stored payloads = %+v, want calls %+v and results %+v", stored, wantCalls, wantResults)
		}
	})
}

// AC33: an assistant message of text, tool_use, text (tool_order, assembled from
// recorded blocks) keeps that order as text, tool_call, text, and the tool_call
// block names its tool_call event, in the parser's events and in the stored payload.
func TestParse_MessageKeepsToolBlockOrder(t *testing.T) {
	res := parseStream(t, readFixture(t, "tool_order/response.sse"), "", false)
	if res.Status != core.ParseOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	msg, rest := responseEvents(t, res)
	var types []core.BlockType
	for _, b := range msg.Blocks {
		types = append(types, b.Type)
	}
	if want := []core.BlockType{core.BlockText, core.BlockToolCall, core.BlockText}; !slices.Equal(types, want) {
		t.Fatalf("blocks = %v, want %v", types, want)
	}
	if !jsonEqual(t, msg.Blocks[0].Content, []byte(`{"type":"text","text":"As of October 1, 2026, the latest major version is **Go 1.27**. The newest stable patch release I found is 1.27.1.\n\n**Go 1.27 (current major release)**\n- "}`)) ||
		!jsonEqual(t, msg.Blocks[2].Content, []byte(`{"type":"text","text":". A newer patch may have come out since then. The official release history page is at go.dev/doc/devel/release.\n- **Language changes:** "}`)) {
		t.Errorf("text blocks = %s and %s, want the first and the second recorded text", msg.Blocks[0].Content, msg.Blocks[2].Content)
	}
	calls, _ := toolEvents(rest)
	if len(calls) != 1 || calls[0].ID != turnToolID || msg.Blocks[1].ToolCallID != calls[0].ID {
		t.Fatalf("tool_call block id %q, tool_call events %+v: want one event %s, named by the block",
			msg.Blocks[1].ToolCallID, calls, turnToolID)
	}

	var stored struct {
		Blocks []struct {
			Type       core.BlockType `json:"type"`
			ToolCallID string         `json:"tool_call_id"`
		} `json:"blocks"`
	}
	for _, se := range canonical(t, res.Events) {
		if se.Kind == core.KindMessage && se.Source == core.SourceResponse {
			if err := json.Unmarshal(se.Payload, &stored); err != nil {
				t.Fatalf("payload %s: %v", se.Payload, err)
			}
		}
	}
	if len(stored.Blocks) != 3 || stored.Blocks[0].Type != core.BlockText || stored.Blocks[1].Type != core.BlockToolCall ||
		stored.Blocks[2].Type != core.BlockText || stored.Blocks[1].ToolCallID != turnToolID {
		t.Errorf("stored blocks = %+v, want text, tool_call (%s), text", stored.Blocks, turnToolID)
	}
	if calls := storedTools(t, res.Events)[core.KindToolCall]; len(calls) != 1 || calls[0].ID != stored.Blocks[1].ToolCallID {
		t.Errorf("stored tool_call events %+v, want one whose id is the block's %q", calls, stored.Blocks[1].ToolCallID)
	}
}

// AC34: a streamed turn with a server_tool_use, its web_search_tool_result and a
// client tool_use (server_tool, assembled from recorded blocks): the server call and
// its result are run by the provider, the client call by the client, and every
// tool block and the result name the call they belong to.
func TestParse_ServerToolUse(t *testing.T) {
	const serverID = "srvtoolu_015XnX3PdVM3zYQgxbSYgDcW"
	res := parseStream(t, readFixture(t, "server_tool/response.sse"), "", false)
	if res.Status != core.ParseOK {
		t.Fatalf("status = %q, want ok", res.Status)
	}
	msg, rest := responseEvents(t, res)
	type block struct {
		typ core.BlockType
		id  string
	}
	var blocks []block
	for _, b := range msg.Blocks {
		blocks = append(blocks, block{b.Type, b.ToolCallID})
	}
	wantBlocks := []block{{core.BlockToolCall, serverID}, {core.BlockToolResult, serverID}, {core.BlockToolCall, turnToolID}}
	if !slices.Equal(blocks, wantBlocks) {
		t.Fatalf("blocks = %+v, want %+v", blocks, wantBlocks)
	}

	calls, results := toolEvents(rest)
	if len(calls) != 2 || len(results) != 1 {
		t.Fatalf("got %d tool calls and %d results, want 2 and 1", len(calls), len(results))
	}
	srv, client, result := calls[0], calls[1], results[0]
	if srv.ID != serverID || srv.Name != "web_search" || srv.ExecutedBy != core.ExecutedByProvider ||
		srv.Source != core.SourceResponse || !jsonEqual(t, srv.Input, []byte(`{"query":"latest Go release golang 2026"}`)) {
		t.Errorf("server tool_call = {id %q name %q input %s executed_by %q source %q}, want {%s web_search the query provider response}",
			srv.ID, srv.Name, srv.Input, srv.ExecutedBy, srv.Source, serverID)
	}
	if result.ToolCallID != srv.ID || result.ExecutedBy != core.ExecutedByProvider || result.Source != core.SourceResponse ||
		!bytes.HasPrefix(result.Content, []byte(`[{"type":"web_search_result"`)) {
		t.Errorf("server tool_result = {tool_call_id %q executed_by %q source %q}, want {%s provider response} with the search results",
			result.ToolCallID, result.ExecutedBy, result.Source, srv.ID)
	}
	if client.ID != turnToolID || client.Name != "Read" || client.ExecutedBy != core.ExecutedByClient ||
		client.Source != core.SourceResponse || !jsonEqual(t, client.Input, []byte(turnToolInput)) {
		t.Errorf("client tool_call = {id %q name %q input %s executed_by %q source %q}, want {%s Read %s client response}",
			client.ID, client.Name, client.Input, client.ExecutedBy, client.Source, turnToolID, turnToolInput)
	}

	stored := storedTools(t, res.Events)
	wantCalls := []storedTool{
		{ID: serverID, ExecutedBy: core.ExecutedByProvider, Source: core.SourceResponse},
		{ID: turnToolID, ExecutedBy: core.ExecutedByClient, Source: core.SourceResponse},
	}
	wantResults := []storedTool{{ToolCallID: serverID, ExecutedBy: core.ExecutedByProvider, Source: core.SourceResponse}}
	if !slices.Equal(stored[core.KindToolCall], wantCalls) || !slices.Equal(stored[core.KindToolResult], wantResults) {
		t.Errorf("stored payloads = %+v, want calls %+v and results %+v", stored, wantCalls, wantResults)
	}
}
