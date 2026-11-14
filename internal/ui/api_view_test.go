package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// The session view API (spec 016 AC1, AC11, AC12): GET /api/sessions/{id}/view, a tool
// call's details, and the live view event of the session stream.

// viewEnv is a stream env with client rules whose calls are stored like digestEnv's.
func viewEnv(t *testing.T) (*streamEnv, *digestEnv) {
	t.Helper()
	e := newStreamEnvRules(t, nil, map[string]ClientRules{"cc": testRules{}})
	return e, &digestEnv{apiEnv: e.apiEnv, d: e.d}
}

// viewHook stores one hook event of session k; input is the tool's input as sent.
func viewHook(t *testing.T, e *streamEnv, phase core.HarnessPhase, tool string, at int, input string) {
	t.Helper()
	hookSeq++
	rec := &store.HookRecord{PrincipalID: core.PrincipalLocal, Client: "cc", Seq: hookSeq,
		ReceivedAt: baseTime.Add(time.Duration(at) * time.Second),
		Payload:    []byte(`{"phase":"` + string(phase) + `","tool":"` + tool + `"}`),
		Event: core.HarnessEvent{Phase: phase, ToolCallID: tool, ToolName: "Bash", SessionKey: "k",
			SourceName: string(phase), ToolInput: []byte(input)}}
	if err := e.s.SaveHook(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
}

// fetch GETs path from the env's listener, so the host check sees its real address.
func fetch(t *testing.T, e *streamEnv, path string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(e.ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}

func getView(t *testing.T, e *streamEnv) map[string]any {
	t.Helper()
	code, body := fetch(t, e, "/api/sessions/"+sid()+"/view")
	if code != 200 {
		t.Fatalf("view = %d %s", code, body)
	}
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func turnsOf(v map[string]any) []map[string]any {
	var out []map[string]any
	for _, t := range v["turns"].([]any) {
		out = append(out, t.(map[string]any))
	}
	return out
}

// AC1: the view is computed on the server: turns with their steps and tool lines, side
// calls apart, the title, the lists; empty fields are left out of the JSON.
func TestViewAPI_ServesView(t *testing.T) {
	e, d := viewEnv(t)
	d.callWith("a", 1, false, []toolSpec{{"Bash", `{"command":"npm test"}`}}, "user:fix the retry")
	d.callWith("q", 2, false, nil, "user:quota")
	d.callWith("b", 3, false, nil, "user:fix the retry", "assistant:Fixed.")
	v := getView(t, e)
	if v["version"] == "" {
		t.Error("no version")
	}
	if sc := v["side_calls"].([]any); len(sc) != 1 || sc[0].(map[string]any)["n"] != 2.0 {
		t.Errorf("side calls = %v", sc)
	}
	turns := turnsOf(v)
	if len(turns) != 1 {
		t.Fatalf("turns = %v", turns)
	}
	t1 := turns[0]
	if t1["n"] != 1.0 || t1["prompt"] != "fix the retry" || t1["hash"] == "" {
		t.Errorf("turn = %v", t1)
	}
	steps := t1["steps"].([]any)
	if len(steps) != 2 || steps[0].(map[string]any)["n"] != 1.0 || steps[1].(map[string]any)["n"] != 3.0 {
		t.Fatalf("steps = %v", steps)
	}
	tools := steps[0].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["id"] != "t-a-0" || tools[0].(map[string]any)["name"] != "Bash" {
		t.Errorf("tools = %v", tools)
	}
	o := v["overview"].(map[string]any)
	if o["title"] != "fix the retry" || o["title_source"] != "prompt" || o["calls"] != 3.0 || o["turns"] != 1.0 {
		t.Errorf("overview = %v", o)
	}
	for _, k := range []string{"files", "commands", "sites", "context", "between"} {
		if _, ok := v[k]; !ok {
			t.Errorf("view has no %s", k)
		}
	}
	if _, ok := t1["hooks_only"]; ok {
		t.Error("an empty field is sent: hooks_only")
	}

	if code, _ := fetch(t, e, "/api/sessions/"+sessionID(store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "nope"})+"/view"); code != 404 {
		t.Errorf("unknown session = %d, want 404", code)
	}
}

// AC11: a tool call's details carry its input and result, its state, reason and outcome,
// its hook lines, and the hook input when it differs, with the bytes the hook input lost.
func TestViewAPI_ToolDetails(t *testing.T) {
	e, d := viewEnv(t)
	d.callWith("a", 1, false, []toolSpec{{"Bash", `{"command":"npm test"}`}}, "user:fix the retry")
	long := `{"command":"npm test ` + strings.Repeat("x", 20*1024) + `"}`
	viewHook(t, e, core.PhasePreTool, "t-a-0", 2, long)
	viewHook(t, e, core.PhasePostTool, "t-a-0", 3, "")
	var got map[string]any
	code, body := fetch(t, e, "/api/sessions/"+sid()+"/tools/t-a-0")
	if code != 200 {
		t.Fatalf("tool = %d %s", code, body)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["name"] != "Bash" || got["exchange_id"] != "a" || got["input"] != `{"command":"npm test"}` {
		t.Errorf("tool = %v", got)
	}
	if got["input_differs"] != true || !strings.HasPrefix(got["hook_input"].(string), `{"command":"npm test xxx`) {
		t.Errorf("hook input: differs %v, %.40q", got["input_differs"], got["hook_input"])
	}
	if n, _ := got["hook_omitted_bytes"].(float64); n <= 0 {
		t.Errorf("hook_omitted_bytes = %v, want > 0", got["hook_omitted_bytes"])
	}
	if got["state"] == "" || got["chip"] == "" {
		t.Errorf("state %v, chip %v", got["state"], got["chip"])
	}
	hooks := got["hooks"].([]any)
	if len(hooks) != 2 || hooks[0].(map[string]any)["phase"] != "pre_tool" || hooks[1].(map[string]any)["phase"] != "post_tool" {
		t.Errorf("hooks = %v", hooks)
	}
	if code, _ := fetch(t, e, "/api/sessions/"+sid()+"/tools/nope"); code != 404 {
		t.Errorf("unknown tool = %d, want 404", code)
	}
}

// AC12: the view stream sends the whole view on open, then, after a change, only the turns
// whose hash changed, with the order of every turn the view now has.
func TestViewStream_SendsChangedTurns(t *testing.T) {
	e, d := viewEnv(t)
	d.callWith("a", 1, false, nil, "user:one", "assistant:r1")
	d.callWith("b", 3, false, nil, "user:one", "assistant:r1", "user:two")
	s := e.open(e.sessionPath() + "?view=1")
	type ev struct {
		Full  bool  `json:"full"`
		Order []int `json:"order"`
		Turns []struct {
			N    int    `json:"n"`
			Hash string `json:"hash"`
		} `json:"turns"`
	}
	read := func() ev {
		t.Helper()
		got := s.nextData()
		if got.event != "view" {
			t.Fatalf("event = %+v, want view", got)
		}
		var v ev
		if err := json.Unmarshal([]byte(got.data), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	nums := func(v ev) []int {
		out := []int{}
		for _, t := range v.Turns {
			out = append(out, t.N)
		}
		return out
	}
	first := read()
	if !first.Full || !equalInts(first.Order, []int{1, 2}) || !equalInts(nums(first), []int{1, 2}) {
		t.Fatalf("first = %+v", first)
	}

	// turn 2 gains a call: only turn 2 is sent
	d.callWith("c", 5, false, nil, "user:one", "assistant:r1", "user:two", "assistant:r2")
	e.publish("c")
	second := read()
	if second.Full || !equalInts(second.Order, []int{1, 2}) || !equalInts(nums(second), []int{2}) {
		t.Fatalf("second = %+v", second)
	}
	if second.Turns[0].Hash == first.Turns[1].Hash {
		t.Error("turn 2 was sent with its old hash")
	}

	// a new prompt starts turn 3: only turn 3 is sent
	d.callWith("f", 7, false, nil, "user:one", "assistant:r1", "user:two", "assistant:r2", "user:three")
	e.publish("f")
	third := read()
	if !equalInts(third.Order, []int{1, 2, 3}) || !equalInts(nums(third), []int{3}) {
		t.Fatalf("third = %+v", third)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// askRules reads every "Ask" call as one question, the result as its answer.
type askRules struct{ testRules }

func (askRules) Questions(name string, input []byte, result string) ([]core.Question, bool) {
	if name != "Ask" {
		return nil, false
	}
	return []core.Question{{Header: "H", Text: string(input), Options: []core.QuestionOption{{Label: "A", Description: "first"}}, Answer: result, Notes: "n"}}, true
}

// A tool call's details carry its questions and answers when the profile reads them, and
// leave them out otherwise: a profile without the reader, no result yet, or a cut input.
func TestViewAPI_ToolQuestions(t *testing.T) {
	res := "A"
	out := toolJSON{Name: "Ask", Input: "Which?", Result: &res}
	qs := toolQuestions(askRules{}, out)
	if len(qs) != 1 || qs[0].Text != "Which?" || qs[0].Answer != "A" || qs[0].Notes != "n" || qs[0].Options[0].Description != "first" {
		t.Fatalf("questions = %+v", qs)
	}
	if toolQuestions(testRules{}, out) != nil {
		t.Error("a profile without the reader gave questions")
	}
	if toolQuestions(askRules{}, toolJSON{Name: "Ask", Input: "Which?"}) != nil {
		t.Error("a call with no result gave questions")
	}
	if toolQuestions(askRules{}, toolJSON{Name: "Ask", Input: "Which?", Result: &res, InputTruncated: true}) != nil {
		t.Error("a cut input gave questions")
	}
	if toolQuestions(askRules{}, toolJSON{Name: "Bash", Input: "x", Result: &res}) != nil {
		t.Error("another tool gave questions")
	}
	raw, _ := json.Marshal(toolJSON{Name: "Bash", Hooks: []hookLineJSON{}})
	if strings.Contains(string(raw), "questions") {
		t.Errorf("questions not omitted: %s", raw)
	}
}

func getCall(t *testing.T, e *streamEnv, session, eid string) (int, map[string]any, string) {
	t.Helper()
	code, body := fetch(t, e, "/api/sessions/"+session+"/calls/"+eid)
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	return code, m, string(body)
}

// 017 AC5, AC6, AC9, AC10: a call's details are served by session and call: its facts,
// context, what it saw, its tool calls, health, raw links and its place among the calls.
func TestCallDetails_Served(t *testing.T) {
	e, d := viewEnv(t)
	d.callWith("a", 1, false, []toolSpec{{"Bash", `{"command":"npm test"}`}}, "user:fix the retry")
	d.callWith("q", 2, false, nil, "user:quota")
	d.callWith("b", 3, false, nil, "user:fix the retry", "assistant:Fixed.")
	code, m, body := getCall(t, e, sid(), "b")
	if code != 200 {
		t.Fatalf("call = %d %s", code, body)
	}
	facts := m["facts"].(map[string]any)
	if facts["model"] != "m1" || facts["thread"].(map[string]any)["kind"] != "main" {
		t.Errorf("facts = %v", facts)
	}
	nav := m["nav"].(map[string]any)
	if nav["index"] != 3.0 || nav["total"] != 3.0 || nav["prev"] != "q" || nav["next"] != "" {
		t.Errorf("nav = %v, want the third of three after the side call", nav)
	}
	saw := m["saw"].(map[string]any)
	if len(saw["messages"].([]any)) != 2 {
		t.Errorf("saw = %v", saw)
	}
	raw := m["raw"].(map[string]any)
	if raw["page"] != "#/exchanges/b" {
		t.Errorf("raw = %v", raw)
	}
	_, a, _ := getCall(t, e, sid(), "a")
	if tools := a["tools"].([]any); len(tools) != 1 || tools[0].(map[string]any)["id"] != "t-a-0" {
		t.Errorf("tools = %v", a["tools"])
	}
	_, q, _ := getCall(t, e, sid(), "q")
	if th := q["facts"].(map[string]any)["thread"].(map[string]any); th["kind"] != "side" {
		t.Errorf("side call thread = %v", th)
	}
}

// 017 E3: a call that is not in the session is a 404, whether the id is unknown or the
// exchange belongs to another session.
func TestCallDetails_NotInSession404(t *testing.T) {
	e, d := viewEnv(t)
	d.callWith("a", 1, false, nil, "user:hello")
	d.save("other", "k2", "cc", 2)
	for _, id := range []string{"nope", "other"} {
		if code, m, body := getCall(t, e, sid(), id); code != 404 || m["error"] == nil {
			t.Errorf("%s = %d %s, want a 404 error", id, code, body)
		}
	}
	if code, _, _ := getCall(t, e, "not-a-session", "a"); code != 404 {
		t.Errorf("bad session = %d", code)
	}
}

// 017 AC9, PLAN §5: the call details carry no header and no credential; the raw links
// point at the endpoints that serve the bodies.
func TestCallDetails_NoAuthHeaders(t *testing.T) {
	e, d := viewEnv(t)
	d.callWith("a", 1, false, nil, "user:hello")
	// an exchange whose stored headers hold a credential-looking value: capture redacts
	// these, but the call details must not carry headers at all
	ex := &core.Exchange{RequestID: "h", PrincipalID: core.PrincipalLocal, Protocol: "test", Client: "cc",
		Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/x", Status: 200, SessionKey: "k",
		Start: baseTime.Add(2 * time.Second), End: baseTime.Add(3 * time.Second),
		RequestHeader:  http.Header{"Authorization": {"Bearer sk-secret-xyz"}, "X-Test-Key": {"sk-secret-xyz"}},
		ResponseHeader: http.Header{"Set-Cookie": {"sk-secret-xyz"}}}
	if err := e.s.SaveExchange(t.Context(), ex); err != nil {
		t.Fatal(err)
	}
	d.parse("h", "m1", `{"input_tokens":3}`)
	code, hm, hbody := getCall(t, e, sid(), "h")
	if code != 200 {
		t.Fatalf("call h = %d %s", code, hbody)
	}
	if strings.Contains(hbody, "sk-secret-xyz") || strings.Contains(strings.ToLower(hbody), "authorization") {
		t.Errorf("call details carry a stored header: %s", hbody)
	}
	if hm["raw"].(map[string]any)["headers"] != "#/exchanges/h" {
		t.Errorf("headers link = %v", hm["raw"])
	}
	_, m, body := getCall(t, e, sid(), "a")
	for _, bad := range []string{"authorization", "x-test-key", "request_headers", "response_headers", "Bearer"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(bad)) {
			t.Errorf("call details mention %q: %s", bad, body)
		}
	}
	if m["raw"].(map[string]any)["headers"] != "#/exchanges/a" {
		t.Errorf("headers link = %v", m["raw"])
	}
}
