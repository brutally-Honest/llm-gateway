package ui

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

type tlPage struct {
	Entries     []map[string]any `json:"entries"`
	NextAfter   *string          `json:"next_after"`
	StreamSince int64            `json:"stream_since"`
}

func (e *apiEnv) timeline(path string) tlPage {
	e.t.Helper()
	rec := e.get(path)
	if rec.Code != 200 {
		e.t.Fatalf("%s = %d %s", path, rec.Code, rec.Body)
	}
	var p tlPage
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// msg is one stored message block set to parse: role, kind of block and its content.
type blk struct{ typ, content string }

// parseMsgs stores events for id: a request event, then one message per blocks set.
func (e *apiEnv) parseMsgs(id, parse string, msgs ...[]any) {
	e.t.Helper()
	evs := []core.StoredEvent{{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)}}
	var contents []core.Content
	for i, m := range msgs {
		role := m[0].(string)
		var blocks []map[string]string
		var key strings.Builder
		key.WriteString(role)
		for j, b := range m[1:] {
			bl := b.(blk)
			h := sha(fmt.Sprintf("%s-%d-%d", id, i, j))
			contents = append(contents, core.Content{Hash: h, Bytes: []byte(bl.content)})
			blocks = append(blocks, map[string]string{"hash": h, "type": bl.typ})
			key.WriteString("|" + bl.content)
		}
		p, _ := json.Marshal(map[string]any{"role": role, "blocks": blocks})
		mh := sha("m-" + key.String())
		contents = append(contents, core.Content{Hash: mh, Bytes: []byte(mh)})
		evs = append(evs, core.StoredEvent{Seq: i + 1, Kind: core.KindMessage, ContentHash: mh, Payload: p})
	}
	if err := e.s.SaveParse(e.t.Context(), id, core.PrincipalLocal, core.ParseStatus(parse), evs, contents); err != nil {
		e.t.Fatal(err)
	}
}

func text(s string) blk { return blk{"text", fmt.Sprintf(`{"type":"text","text":%q}`, s)} }

func sid() string {
	return sessionID(store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "k"})
}

func TestAPI_TimelineStableIdsAndOrder(t *testing.T) {
	e := newAPIEnv(t)
	e.save("b", "k", "cc", 20) // stored first, starts later
	e.parseMsgs("b", "ok", []any{"user", text("one")}, []any{"assistant", text("two")})
	e.save("a", "k", "cc", 10)
	e.parseMsgs("a", "ok", []any{"user", text("zero")})
	url := "/api/sessions/" + sid() + "/timeline"
	full := e.timeline(url)
	var ids []string
	for _, en := range full.Entries {
		ids = append(ids, en["id"].(string))
	}
	if got := fmt.Sprint(ids); got != "[a:0 a:1 b:0 b:1 b:2]" {
		t.Fatalf("ids = %s", got)
	}
	if full.NextAfter != nil || full.StreamSince != 2 {
		t.Fatalf("next %v since %d", full.NextAfter, full.StreamSince)
	}
	if again := e.get(url).Body.String(); again != e.get(url).Body.String() {
		t.Fatal("two calls differ")
	}
	// Walk in pages of 2: the same ids, no repeat, no skip.
	var walked []string
	p := e.timeline(url + "?limit=2")
	for {
		for _, en := range p.Entries {
			walked = append(walked, en["id"].(string))
		}
		if p.NextAfter == nil {
			break
		}
		p = e.timeline(url + "?limit=2&after=" + *p.NextAfter)
	}
	if fmt.Sprint(walked) != fmt.Sprint(ids) {
		t.Fatalf("walk = %v want %v", walked, ids)
	}
	if full.Entries[0]["kind"] != "model_call" || full.Entries[1]["kind"] != "user_prompt" || full.Entries[4]["kind"] != "assistant_text" {
		t.Fatalf("kinds = %v", full.Entries)
	}
}

func TestAPI_TimelineCutsAt16KiB(t *testing.T) {
	e := newAPIEnv(t)
	e.save("a", "k", "cc", 1)
	e.parseMsgs("a", "ok", []any{"user", text(strings.Repeat("x", 20000))}, []any{"assistant", text("short")})
	p := e.timeline("/api/sessions/" + sid() + "/timeline")
	long, short := p.Entries[1], p.Entries[2]
	if len(long["text"].(string)) != 16*1024 || long["truncated"] != true {
		t.Fatalf("long: len %d truncated %v", len(long["text"].(string)), long["truncated"])
	}
	if short["truncated"] != nil || short["text"] != "short" {
		t.Fatalf("short = %v", short)
	}
}

func TestUI_TimelineMatchesCLIViewer(t *testing.T) {
	e := newAPIEnv(t)
	e.save("a", "k", "cc", 1)
	e.parseMsgs("a", "ok", []any{"user", text("q1")}, []any{"assistant", text("r1"), blk{"reasoning", `{"data":"zzz"}`}})
	e.save("b", "k", "cc", 2)
	e.parseMsgs("b", "ok", []any{"user", text("q1")}, []any{"assistant", text("r1"), blk{"reasoning", `{"data":"zzz"}`}}, []any{"user", text("q2")})
	e.save("c", "k", "cc", 3)
	e.parseMsgs("c", "failed")
	r, err := store.OpenReader(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	cli, err := timeline.Build(r, store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "k"}, timeline.Options{})
	if err != nil {
		t.Fatal(err)
	}
	api := e.timeline("/api/sessions/" + sid() + "/timeline").Entries
	if len(api) != len(cli) {
		t.Fatalf("api %d entries, cli %d", len(api), len(cli))
	}
	for i, c := range cli {
		a := api[i]
		if a["exchange_id"] != c.RequestID || a["index"] != float64(c.Index) {
			t.Errorf("entry %d: %v vs %+v", i, a, c)
		}
		if c.Text != "" && a["text"] != c.Text {
			t.Errorf("entry %d text %v vs %q", i, a["text"], c.Text)
		}
		if c.Kind == timeline.KindMarker && a["kind"] != "marker" {
			t.Errorf("entry %d kind %v", i, a["kind"])
		}
	}
}

func TestAPI_TimelineFoldedExchangeFlagsMissingUsage(t *testing.T) {
	e := newAPIEnv(t)
	e.save("f", "k", "cc", 1)
	e.parseMsgs("f", "failed")
	p := e.timeline("/api/sessions/" + sid() + "/timeline")
	if len(p.Entries) != 1 || p.Entries[0]["kind"] != "model_call" {
		t.Fatalf("entries = %v", p.Entries)
	}
	u := p.Entries[0]["usage"].(map[string]any)
	if u["has_usage"] != false || u["no_usage"] != "missing" || u["input"] != nil || u["output"] != nil {
		t.Fatalf("usage = %v", u)
	}
}

func TestAPI_TimelineOmitsUnreadableReasoning(t *testing.T) {
	e := newAPIEnv(t)
	e.save("a", "k", "cc", 1)
	e.parseMsgs("a", "ok", []any{"assistant", blk{"reasoning", `{"data":"zzz"}`}})
	e.save("b", "k", "cc", 2)
	e.parseMsgs("b", "ok", []any{"assistant", blk{"reasoning", `{"type":"thinking","thinking":"hmm"}`}, text("hi")})
	p := e.timeline("/api/sessions/" + sid() + "/timeline")
	var thinking []string
	for _, en := range p.Entries {
		if en["kind"] == "thinking" {
			thinking = append(thinking, en["exchange_id"].(string)+"="+en["text"].(string))
		}
		if en["kind"] == "assistant_text" && en["exchange_id"] == "a" && en["text"] != "[reasoning]" {
			t.Errorf("a text = %v", en["text"])
		}
	}
	if fmt.Sprint(thinking) != "[b=hmm]" {
		t.Fatalf("thinking entries = %v", thinking)
	}
}

func TestAPI_ExchangeDetail(t *testing.T) {
	e := newAPIEnv(t)
	ex := &core.Exchange{
		RequestID: "x1", PrincipalID: core.PrincipalLocal, Protocol: "test", Client: "cc",
		Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/x", Status: 200, SessionKey: "k",
		Start: baseTime, End: baseTime.Add(2 * time.Second), HasTTFB: true, TTFB: 500 * time.Millisecond,
		ClientDisconnected: true,
		RequestHeader:      http.Header{"X-A": {"1"}},
		ResponseHeader:     http.Header{"Content-Type": {"application/json"}},
	}
	ex.Response.Truncated = true
	if err := e.s.SaveExchange(t.Context(), ex); err != nil {
		t.Fatal(err)
	}
	e.parse("x1", "m1", `{"input_tokens":10,"output_tokens":5}`)
	rec := e.get("/api/exchanges/x1")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var d map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &d)
	if d["method"] != "POST" || d["status"] != 200.0 || d["model"] != "m1" || d["ttfb_ms"] != 500.0 ||
		d["duration_ms"] != 2000.0 || d["session_id"] != sid() || d["client_disconnected"] != true ||
		d["response_truncated"] != true || d["request_body_stored"] != false {
		t.Fatalf("detail = %v", d)
	}
	if d["request_headers"].(map[string]any)["X-A"] == nil || d["usage"].(map[string]any)["input"] != 10.0 {
		t.Fatalf("headers/usage = %v", d)
	}
}

func (e *apiEnv) saveBody(id string, h http.Header, body []byte, response bool) {
	e.t.Helper()
	ex := &core.Exchange{
		RequestID: id, PrincipalID: core.PrincipalLocal, Protocol: "test", Client: "cc",
		Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/x", Status: 200, Start: baseTime, End: baseTime.Add(time.Second),
	}
	b := core.Body{Chunks: [][]byte{body}, Size: int64(len(body))}
	if response {
		ex.ResponseHeader, ex.Response = h, b
	} else {
		ex.RequestHeader, ex.Request = h, b
	}
	if err := e.s.SaveExchange(e.t.Context(), ex); err != nil {
		e.t.Fatal(err)
	}
}

func TestAPI_BodyTruncatesAt5MiB(t *testing.T) {
	e := newAPIEnv(t)
	big := bytes.Repeat([]byte("a"), 5<<20+10)
	e.saveBody("big", http.Header{"Content-Type": {"text/plain"}}, big, true)
	rec := e.get("/api/exchanges/big/body?part=response")
	if rec.Code != 200 || rec.Body.Len() != 5<<20 || rec.Header().Get("X-Body-Truncated") != "true" ||
		rec.Header().Get("Content-Type") != "text/plain" {
		t.Fatalf("code %d len %d trunc %q ct %q", rec.Code, rec.Body.Len(), rec.Header().Get("X-Body-Truncated"), rec.Header().Get("Content-Type"))
	}
	// Exactly at the cap is whole; a gzip body is returned decompressed.
	e.saveBody("exact", http.Header{"Content-Type": {"text/plain"}}, big[:5<<20], false)
	rec = e.get("/api/exchanges/exact/body?part=request")
	if rec.Body.Len() != 5<<20 || rec.Header().Get("X-Body-Truncated") != "" {
		t.Fatalf("exact: len %d trunc %q", rec.Body.Len(), rec.Header().Get("X-Body-Truncated"))
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(`{"hello":"world"}`))
	_ = zw.Close()
	e.saveBody("gz", http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}}, gz.Bytes(), true)
	rec = e.get("/api/exchanges/gz/body?part=response")
	if rec.Body.String() != `{"hello":"world"}` || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("gzip body = %q", rec.Body)
	}
}

func TestAPI_BodyNotStored404(t *testing.T) {
	e := newAPIEnv(t)
	e.save("n", "", "cc", 1)
	wantEnvelope(t, e.get("/api/exchanges/n/body?part=request"), 404, "body_not_stored")
	wantEnvelope(t, e.get("/api/exchanges/n/body?part=response"), 404, "body_not_stored")
}

func TestAPI_NotFoundAndBadRequestCodes(t *testing.T) {
	e := newAPIEnv(t)
	e.save("a", "k", "cc", 1)
	url := "/api/sessions/" + sid() + "/timeline"
	wantEnvelope(t, e.get("/api/sessions/"+encodeID(t, "local", "cc", "nope")+"/timeline"), 404, "not_found")
	wantEnvelope(t, e.get("/api/sessions/zzz/timeline"), 404, "not_found")
	wantEnvelope(t, e.get("/api/exchanges/missing"), 404, "not_found")
	wantEnvelope(t, e.get("/api/exchanges/missing/body?part=request"), 404, "not_found")
	for _, q := range []string{"?limit=0", "?limit=-1", "?limit=x", "?after=!!", "?since=x", "?since=-1",
		"?after=" + encodeAfter(afterToken{List: listSessions, At: 1})} {
		wantEnvelope(t, e.get(url+q), 400, "bad_request")
	}
	if rec := e.get(url + "?limit=999999"); rec.Code != 200 {
		t.Fatalf("over cap = %d", rec.Code)
	}
	wantEnvelope(t, e.get("/api/exchanges/a/body"), 400, "bad_request")
	wantEnvelope(t, e.get("/api/exchanges/a/body?part=other"), 400, "bad_request")
}

func TestAPI_ToolEntriesCarryToolCallID(t *testing.T) {
	e := newAPIEnv(t)
	e.save("a", "k", "cc", 1)
	input, result := []byte(`{"cmd":"ls"}`), []byte("file.txt")
	msg := []byte(`{"type":"text","text":"run ls"}`)
	mp, _ := json.Marshal(map[string]any{"role": "assistant", "blocks": []map[string]string{{"hash": sha("msg"), "type": "text"}}})
	evs := []core.StoredEvent{
		{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)},
		{Seq: 1, Kind: core.KindMessage, ContentHash: sha("m"), Payload: mp},
		{Seq: 2, Kind: core.KindToolCall, ToolCallID: "call_1", ContentHash: sha("in"),
			Payload: []byte(`{"name":"bash","id":"call_1"}`)},
		{Seq: 3, Kind: core.KindToolResult, ToolCallID: "call_1", ContentHash: sha("out"),
			Payload: []byte(`{"is_error":false}`)},
	}
	contents := []core.Content{{Hash: sha("msg"), Bytes: msg}, {Hash: sha("m"), Bytes: []byte(sha("m"))},
		{Hash: sha("in"), Bytes: input}, {Hash: sha("out"), Bytes: result}}
	if err := e.s.SaveParse(e.t.Context(), "a", core.PrincipalLocal, core.ParseStatus("ok"), evs, contents); err != nil {
		t.Fatal(err)
	}
	p := e.timeline("/api/sessions/" + sid() + "/timeline")
	seen := map[string]bool{}
	for _, en := range p.Entries {
		kind, _ := en["kind"].(string)
		if kind != "tool_call" && kind != "tool_result" {
			continue
		}
		seen[kind] = true
		if en["tool_call_id"] != "call_1" {
			t.Fatalf("%s tool_call_id = %v in %v", kind, en["tool_call_id"], en)
		}
		// The entry's fields are exactly the API's tool fields: no other id name rides along.
		allowed := map[string]bool{"id": true, "exchange_id": true, "index": true, "start": true, "kind": true,
			"name": true, "tool_call_id": true, "text": true, "is_error": true, "truncated": true, "partial": true,
			"state": true}
		for k := range en {
			if !allowed[k] {
				t.Fatalf("%s carries unexpected field %q: %v", kind, k, en)
			}
		}
	}
	if !seen["tool_call"] || !seen["tool_result"] {
		t.Fatalf("missing tool entries: %v", p.Entries)
	}
}

func TestUI_BodyEndpointSendsStricterCSP(t *testing.T) {
	e := newAPIEnv(t)
	html := []byte(`<html><script>alert(1)</script></html>`)
	e.saveBody("html", http.Header{"Content-Type": {"text/html"}}, html, true)
	rec := e.get("/api/exchanges/html/body?part=response")
	if rec.Code != 200 || rec.Body.String() != string(html) {
		t.Fatalf("body = %d %q", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'none'; sandbox" {
		t.Errorf("body CSP = %q, want default-src 'none'; sandbox", got)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("body endpoint lost nosniff/referrer headers")
	}
	// The endpoint's own errors carry the strict policy too.
	for _, c := range []struct {
		name, path string
		code       int
		errCode    string
	}{
		{"missing part", "/api/exchanges/html/body", 400, "bad_request"},
		{"bad part", "/api/exchanges/html/body?part=both", 400, "bad_request"},
		{"unknown exchange", "/api/exchanges/nope/body?part=response", 404, "not_found"},
		{"body not stored", "/api/exchanges/html/body?part=request", 404, "body_not_stored"},
	} {
		rec := e.get(c.path)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), `"code":"`+c.errCode+`"`) {
			t.Errorf("%s: %d %s, want %d %s", c.name, rec.Code, rec.Body, c.code, c.errCode)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'none'; sandbox" {
			t.Errorf("%s: CSP = %q, want default-src 'none'; sandbox", c.name, got)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: lost nosniff/referrer headers", c.name)
		}
	}
}

// toolSession stores one exchange holding a captured tool call "call_1" (input in).
func (e *apiEnv) toolSession(input string) {
	e.t.Helper()
	e.save("a", "k", "cc", 1)
	mp, _ := json.Marshal(map[string]any{"role": "assistant", "blocks": []map[string]string{{"hash": sha("msg"), "type": "text"}}})
	evs := []core.StoredEvent{
		{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)},
		{Seq: 1, Kind: core.KindMessage, ContentHash: sha("m"), Payload: mp},
		{Seq: 2, Kind: core.KindToolCall, ToolCallID: "call_1", ContentHash: sha("in"),
			Payload: []byte(`{"name":"bash","id":"call_1"}`)},
	}
	contents := []core.Content{{Hash: sha("msg"), Bytes: []byte(`{"type":"text","text":"run"}`)},
		{Hash: sha("m"), Bytes: []byte(sha("m"))}, {Hash: sha("in"), Bytes: []byte(input)}}
	if err := e.s.SaveParse(e.t.Context(), "a", core.PrincipalLocal, core.ParseOK, evs, contents); err != nil {
		e.t.Fatal(err)
	}
}

var hookSeq int64

// hook stores a hook event of session "k" received at base+at seconds.
func (e *apiEnv) hook(key string, ev core.HarnessEvent, at int) {
	e.t.Helper()
	hookSeq++
	ev.SessionKey = key
	rec := &store.HookRecord{PrincipalID: core.PrincipalLocal, Client: "cc", Seq: hookSeq,
		ReceivedAt: baseTime.Add(time.Duration(at) * time.Second),
		Payload:    []byte(fmt.Sprintf(`{"n":%d}`, hookSeq)), Event: ev}
	if err := e.s.SaveHook(e.t.Context(), rec); err != nil {
		e.t.Fatal(err)
	}
}

func TestAPITimeline_StatesFlagsAndHookLines(t *testing.T) {
	e := newAPIEnv(t)
	e.toolSession(`{"a":1}`)
	e.hook("k", core.HarnessEvent{Phase: core.PhasePreTool, ToolCallID: "call_1", ToolName: "bash", ToolInput: []byte(`{"a":2}`)}, 2)
	e.hook("k", core.HarnessEvent{Phase: core.PhasePostTool, ToolCallID: "call_1", ToolName: "bash"}, 3)
	e.hook("k", core.HarnessEvent{Phase: core.PhasePostTool, ToolCallID: "ghost", ToolName: "ls", SourceName: "evt-post"}, 4)
	e.hook("k", core.HarnessEvent{Phase: core.PhaseNotice, SourceName: "evt-notice"}, 5)
	p := e.timeline("/api/sessions/" + sid() + "/timeline")
	var call map[string]any
	var hooks []map[string]any
	for _, en := range p.Entries {
		switch en["kind"] {
		case "tool_call":
			call = en
		case "hook_event":
			hooks = append(hooks, en)
		}
	}
	if call == nil || call["state"] != "ran-not-reported" || call["input_differs"] != true || call["hook_input"] != `{"a":2}` {
		t.Fatalf("tool call = %v", call)
	}
	if len(hooks) != 2 {
		t.Fatalf("hook lines = %v", hooks)
	}
	if hooks[0]["tool_call_id"] != "ghost" || hooks[0]["state"] != "ran-not-asked-or-not-captured" || hooks[0]["phase"] != "post_tool" || hooks[0]["name"] != "ls" {
		t.Errorf("unmatched hook line = %v", hooks[0])
	}
	if hooks[1]["phase"] != "notice" || hooks[1]["id"] == hooks[0]["id"] {
		t.Errorf("notice hook line = %v", hooks[1])
	}
	// Paging by next_after walks past hook lines without repeating or skipping.
	var ids []string
	path := "/api/sessions/" + sid() + "/timeline?limit=1"
	for range 20 {
		pg := e.timeline(path)
		for _, en := range pg.Entries {
			ids = append(ids, en["id"].(string))
		}
		if pg.NextAfter == nil {
			break
		}
		path = "/api/sessions/" + sid() + "/timeline?limit=1&after=" + *pg.NextAfter
	}
	if len(ids) != len(p.Entries) {
		t.Fatalf("paged ids %v, full %d entries", ids, len(p.Entries))
	}
	for i, en := range p.Entries {
		if ids[i] != en["id"] {
			t.Fatalf("paged %v differs from full at %d", ids, i)
		}
	}
}

func TestAPITimeline_TruncatedHookInputCarriesOmittedCount(t *testing.T) {
	e := newAPIEnv(t)
	e.toolSession(`{"a":1}`)
	big := `{"a":"` + strings.Repeat("x", entryCut+100) + `"}`
	e.hook("k", core.HarnessEvent{Phase: core.PhasePreTool, ToolCallID: "call_1", ToolName: "bash", ToolInput: []byte(big)}, 2)
	p := e.timeline("/api/sessions/" + sid() + "/timeline")
	for _, en := range p.Entries {
		if en["kind"] == "tool_call" {
			n, _ := en["hook_omitted_bytes"].(float64)
			if en["input_differs"] != true || n < 100 || len(en["hook_input"].(string))+int(n) != len(big) {
				t.Fatalf("tool call = hook_omitted_bytes %v, hook_input len %d", en["hook_omitted_bytes"], len(en["hook_input"].(string)))
			}
			return
		}
	}
	t.Fatal("no tool call")
}

func TestAPITimeline_AgentLabels(t *testing.T) {
	e := newAPIEnv(t)
	e.toolSession(`{"a":1}`)
	e.hook("k", core.HarnessEvent{Phase: core.PhasePreTool, ToolCallID: "call_1", AgentID: "ag1", AgentType: "explore"}, 2)
	e.hook("k", core.HarnessEvent{Phase: core.PhaseAgentStart, AgentID: "ag2", AgentType: "plan", SourceName: "evt-agent-start"}, 3)
	p := e.timeline("/api/sessions/" + sid() + "/timeline")
	seen := map[string]bool{}
	for _, en := range p.Entries {
		switch en["kind"] {
		case "tool_call":
			seen["call"] = en["agent_id"] == "ag1" && en["agent_type"] == "explore"
		case "hook_event":
			seen["hook"] = en["agent_id"] == "ag2" && en["agent_type"] == "plan"
		}
	}
	if !seen["call"] || !seen["hook"] {
		t.Fatalf("agent labels missing: %v", p.Entries)
	}
}

// A late-arriving earlier hook event must not move the ids of the hook lines that
// already exist: an id is the stored row id, not the line's place on the page.
func TestAPITimeline_HookLineIdsSurviveEarlierArrival(t *testing.T) {
	e := newAPIEnv(t)
	e.hook("k", core.HarnessEvent{Phase: core.PhaseNotice, SourceName: "evt-a"}, 2)
	e.hook("k", core.HarnessEvent{Phase: core.PhaseNotice, SourceName: "evt-b"}, 3)
	e.hook("k", core.HarnessEvent{Phase: core.PhaseNotice, SourceName: "evt-c"}, 4)
	before := map[string]string{} // hook source name -> id
	for _, en := range e.timeline("/api/sessions/" + sid() + "/timeline").Entries {
		if en["kind"] == "hook_event" {
			before[en["text"].(string)] = en["id"].(string)
		}
	}
	if len(before) != 3 {
		t.Fatalf("hook lines before = %v", before)
	}
	// Received before all three, with a sequence number from before a restart.
	early := &store.HookRecord{PrincipalID: core.PrincipalLocal, Client: "cc", Seq: 1,
		ReceivedAt: baseTime.Add(time.Second), Payload: []byte(`{"early":true}`),
		Event: core.HarnessEvent{Phase: core.PhaseNotice, SourceName: "evt-early", SessionKey: "k"}}
	if err := e.s.SaveHook(e.t.Context(), early); err != nil {
		t.Fatal(err)
	}
	after := map[string]string{}
	for _, en := range e.timeline("/api/sessions/" + sid() + "/timeline").Entries {
		if en["kind"] == "hook_event" {
			after[en["text"].(string)] = en["id"].(string)
		}
	}
	if len(after) != 4 {
		t.Fatalf("hook lines after = %v", after)
	}
	for name, id := range before {
		if after[name] != id {
			t.Errorf("hook line %s: id %q became %q", name, id, after[name])
		}
	}
}

// A late-arriving earlier hook event must not repeat or skip hook lines on the next
// page: the after token is keyed on the line's receive time, seq and row id, not on
// its place among the hook lines.
func TestAPITimeline_HookPagingSurvivesEarlierArrival(t *testing.T) {
	e := newAPIEnv(t)
	e.hook("k", core.HarnessEvent{Phase: core.PhaseNotice, SourceName: "evt-a"}, 2)
	e.hook("k", core.HarnessEvent{Phase: core.PhaseNotice, SourceName: "evt-b"}, 3)
	e.hook("k", core.HarnessEvent{Phase: core.PhaseNotice, SourceName: "evt-c"}, 4)
	e.hook("k", core.HarnessEvent{Phase: core.PhaseNotice, SourceName: "evt-d"}, 5)
	url := "/api/sessions/" + sid() + "/timeline"
	var want []string // ids of the lines that exist before page 1 is loaded
	for _, en := range e.timeline(url).Entries {
		want = append(want, en["id"].(string))
	}
	if len(want) != 4 {
		t.Fatalf("hook lines = %v", want)
	}
	page1 := e.timeline(url + "?limit=2")
	if page1.NextAfter == nil {
		t.Fatalf("page 1 has no next_after: %v", page1.Entries)
	}
	// Received before the page boundary, so it sorts ahead of every line on page 1.
	early := &store.HookRecord{PrincipalID: core.PrincipalLocal, Client: "cc", Seq: 1,
		ReceivedAt: baseTime.Add(time.Second), Payload: []byte(`{"early":true}`),
		Event: core.HarnessEvent{Phase: core.PhaseNotice, SourceName: "evt-early", SessionKey: "k"}}
	if err := e.s.SaveHook(e.t.Context(), early); err != nil {
		t.Fatal(err)
	}
	page2 := e.timeline(url + "?limit=2&after=" + *page1.NextAfter)
	var got []string
	for _, en := range page2.Entries {
		got = append(got, en["id"].(string))
	}
	if len(got) != 2 || got[0] != want[2] || got[1] != want[3] {
		t.Errorf("page 2 ids = %v, want the lines after page 1: %v", got, want[2:])
	}
}

// Hook lines sort by receive time, seq and stored row id on every path. Seq restarts
// with the gateway, so rows tie on receive time and seq; the store read, the one-page
// timeline and the concatenation of all pages must still agree on the order.
func TestAPITimeline_HookOrderTiesOnTimeAndSeq(t *testing.T) {
	e := newAPIEnv(t)
	rng := rand.New(rand.NewSource(19))
	phases := []core.HarnessPhase{core.PhaseNotice, core.PhasePreTool, core.PhasePostTool}
	for n := range 40 {
		rec := &store.HookRecord{PrincipalID: core.PrincipalLocal, Client: "cc",
			Seq:        int64(1 + rng.Intn(3)),
			ReceivedAt: baseTime.Add(time.Duration(2+rng.Intn(3)) * time.Second),
			Payload:    []byte(fmt.Sprintf(`{"n":%d,"r":%d}`, n, rng.Int())),
			Event: core.HarnessEvent{Phase: phases[rng.Intn(len(phases))],
				SourceName: fmt.Sprintf("evt-%d", n), SessionKey: "k"}}
		if err := e.s.SaveHook(e.t.Context(), rec); err != nil {
			t.Fatal(err)
		}
	}
	r, err := store.OpenReader(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	stored, err := r.HookEvents(core.PrincipalLocal, "cc", "k")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, h := range stored {
		want = append(want, "hook:"+strconv.FormatInt(h.ID, 10))
	}
	if len(want) != 40 {
		t.Fatalf("stored hook rows = %d, want 40", len(want))
	}

	url := "/api/sessions/" + sid() + "/timeline"
	ids := func(p tlPage) []string {
		var out []string
		for _, en := range p.Entries {
			out = append(out, en["id"].(string))
		}
		return out
	}
	if got := ids(e.timeline(url)); !slices.Equal(got, want) {
		t.Fatalf("one page = %v, want the store order %v", got, want)
	}
	var paged []string
	after := ""
	for {
		u := url + "?limit=3"
		if after != "" {
			u += "&after=" + after
		}
		p := e.timeline(u)
		paged = append(paged, ids(p)...)
		if p.NextAfter == nil {
			break
		}
		after = *p.NextAfter
	}
	if !slices.Equal(paged, want) {
		t.Errorf("concatenated pages = %v, want the store order %v", paged, want)
	}
}
