package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// reply is the response side of a stored call.
type reply struct {
	text, stop string
	tools      []string
}

// callReply stores a call of session k: request messages ("role:text"), then the
// parsed reply (a response-source assistant message and tool calls) and a usage event.
func (e *digestEnv) callReply(id string, at int, usage string, r *reply, msgs ...string) {
	e.t.Helper()
	e.save(id, "k", "cc", at)
	evs := []core.StoredEvent{{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)}}
	var contents []core.Content
	add := func(role, txt, src, stop string, key string) {
		bh := sha(fmt.Sprintf("%s-%d", id, len(evs)))
		raw, _ := json.Marshal(map[string]string{"type": "text", "text": txt})
		mh := sha("m-" + key)
		contents = append(contents, core.Content{Hash: bh, Bytes: raw}, core.Content{Hash: mh, Bytes: []byte(mh)})
		p, _ := json.Marshal(map[string]any{"role": role, "stop_reason": stop,
			"blocks": []map[string]string{{"hash": bh, "type": "text"}}})
		evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindMessage, ContentHash: mh,
			Source: core.Source(src), Payload: p})
	}
	for _, m := range msgs {
		role, txt, _ := strings.Cut(m, ":")
		add(role, txt, "request_history", "", m)
	}
	if r != nil {
		add("assistant", r.text, "response", r.stop, "assistant:"+r.text)
		for _, tid := range r.tools {
			h := sha("tc-" + tid)
			contents = append(contents, core.Content{Hash: h, Bytes: []byte(`{}`)})
			evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindToolCall, ToolCallID: tid,
				Source: core.SourceResponse, ContentHash: h, Payload: []byte(`{"name":"Bash"}`)})
		}
	}
	if usage != "" {
		evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindUsage, Payload: []byte(usage)})
	}
	if err := e.s.SaveParse(e.t.Context(), id, core.PrincipalLocal, core.ParseOK, evs, contents); err != nil {
		e.t.Fatal(err)
	}
}

func (e *digestEnv) exchange(id string) map[string]any {
	e.t.Helper()
	return e.getMap("/api/exchanges/" + id)
}

func TestExchange_SideCallField(t *testing.T) {
	e := withRules(t)
	e.callReply("q", 1, `{"input_tokens":5}`, nil, "user:quota")
	e.callReply("m", 2, `{"input_tokens":5}`, nil, "user:hello")
	if q := e.exchange("q"); q["side_call"] != true {
		t.Fatalf("q = %v", q["side_call"])
	}
	if m := e.exchange("m"); m["side_call"] != false {
		t.Fatalf("m = %v", m["side_call"])
	}
	n := newDigestEnv(t, nil)
	n.callReply("q", 1, `{"input_tokens":5}`, nil, "user:quota")
	if n.exchange("q")["side_call"] != false {
		t.Fatal("no rules must mean no side call")
	}
}

func TestExchange_MessagesCarriedAndNew(t *testing.T) {
	e := withRules(t)
	e.callReply("a", 1, `{"input_tokens":5}`, &reply{text: "two", stop: "end_turn"}, "user:one")
	e.callReply("b", 2, `{"input_tokens":9}`, nil, "user:one", "assistant:two", "user:three")
	b := e.exchange("b")
	if b["prev_id"] != "a" || b["thread_new"] != false || b["carried"] != 2.0 || b["new_count"] != 1.0 {
		t.Fatalf("b = %v", b)
	}
	ms := b["messages"].([]any)
	if len(ms) != 3 {
		t.Fatalf("messages = %v", ms)
	}
	for i, wantNew := range []bool{false, false, true} {
		m := ms[i].(map[string]any)
		if m["new"] != wantNew {
			t.Fatalf("message %d = %v", i, m)
		}
	}
	if m := ms[2].(map[string]any); m["role"] != "user" || m["text"] != "three" {
		t.Fatalf("last = %v", m)
	}
}

func TestExchange_NewThreadMarked(t *testing.T) {
	e := withRules(t)
	e.callReply("a", 1, `{"input_tokens":5}`, nil, "user:one", "user:uno")
	a := e.exchange("a")
	if a["thread_new"] != true || a["prev_id"] != "" || a["carried"] != 0.0 || a["new_count"] != 2.0 {
		t.Fatalf("a = %v", a)
	}
	for _, m := range a["messages"].([]any) {
		if m.(map[string]any)["new"] != true {
			t.Fatalf("message = %v", m)
		}
	}
}

func TestExchange_ResponseAndStopReason(t *testing.T) {
	e := withRules(t)
	e.callReply("a", 1, `{"input_tokens":5,"output_tokens":3}`, &reply{text: "hi there", stop: "end_turn"}, "user:one")
	r := e.exchange("a")["response"].(map[string]any)
	if r["text"] != "hi there" || r["stop_reason"] != "end_turn" || r["unavailable"] != nil {
		t.Fatalf("response = %v", r)
	}
}

func TestExchange_ResponseToolCallIDs(t *testing.T) {
	e := withRules(t)
	e.callReply("a", 1, `{"input_tokens":5}`, &reply{text: "x", stop: "stop-x", tools: []string{"t1", "t2"}}, "user:one")
	ids := e.exchange("a")["response_tool_call_ids"].([]any)
	if fmt.Sprint(ids) != "[t1 t2]" {
		t.Fatalf("ids = %v", ids)
	}
	e.callReply("b", 2, `{"input_tokens":5}`, nil, "user:one")
	if ids := e.exchange("b")["response_tool_call_ids"].([]any); len(ids) != 0 {
		t.Fatalf("ids = %v", ids)
	}
}

// E4: a call without usage answers null numbers, never zeros, and the rest still works.
func TestExchange_NoUsageNoNumbers(t *testing.T) {
	e := withRules(t)
	e.callReply("a", 1, "", &reply{text: "x", stop: "end_turn"}, "user:one")
	a := e.exchange("a")
	u := a["usage"].(map[string]any)
	if u["has_usage"] != false || u["input"] != nil || u["output"] != nil || u["cache_write"] != nil || u["cache_read"] != nil {
		t.Fatalf("usage = %v", u)
	}
	if a["response"].(map[string]any)["text"] != "x" || len(a["messages"].([]any)) != 1 {
		t.Fatalf("a = %v", a)
	}
}

// AC29: an exchange with no stored events or bodies still answers 200 with reasons.
func TestExchange_BodyNotStoredStillAnswers(t *testing.T) {
	e := withRules(t)
	e.save("x", "k", "cc", 1)
	x := e.exchange("x")
	r := x["response"].(map[string]any)
	if r["unavailable"] != "response_body_not_stored" || r["text"] != "" {
		t.Fatalf("response = %v", r)
	}
	if x["messages_unavailable"] != "request_body_not_stored" || len(x["messages"].([]any)) != 0 {
		t.Fatalf("x = %v", x)
	}
	if x["id"] != "x" || x["status"] != 200.0 {
		t.Fatalf("existing fields lost: %v", x)
	}
	// A failed parse names the parse, not the body.
	e.save("f", "k", "cc", 2)
	if err := e.s.SaveParse(e.t.Context(), "f", core.PrincipalLocal, core.ParseFailed, nil, nil); err != nil {
		t.Fatal(err)
	}
	f := e.exchange("f")
	if f["response"].(map[string]any)["unavailable"] != "parse_failed" {
		t.Fatalf("f = %v", f["response"])
	}
}
