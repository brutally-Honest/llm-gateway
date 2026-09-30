package anthropic_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"testing"

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

// A malformed request body fails the parse; a truncated one is partial.
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
}
