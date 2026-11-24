package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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

// The thread, message and reply facts left the exchange JSON for the call detail, and the
// browser reads none of them from it: the exchange answer no longer carries their keys.
func TestExchange_DrawerKeysAreGone(t *testing.T) {
	e := withRules(t)
	e.callReply("a", 1, `{"input_tokens":5}`, &reply{text: "x", stop: "end_turn", tools: []string{"t1"}}, "user:one")
	a := e.exchange("a")
	for _, k := range []string{"side_call", "prev_id", "thread_new", "messages", "messages_unavailable",
		"carried", "new_count", "response", "response_tool_call_ids"} {
		if _, ok := a[k]; ok {
			t.Errorf("exchange JSON still has %q: %v", k, a[k])
		}
	}
	if a["id"] != "a" || a["status"] != 200.0 {
		t.Fatalf("existing fields lost: %v", a)
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
}

// AC29: an exchange with no stored events or bodies still answers 200.
func TestExchange_BodyNotStoredStillAnswers(t *testing.T) {
	e := withRules(t)
	e.save("x", "k", "cc", 1)
	x := e.exchange("x")
	if x["id"] != "x" || x["status"] != 200.0 {
		t.Fatalf("existing fields lost: %v", x)
	}
}

// The call's new count (timeline call.new): the request's messages missing from the
// thread's previous call, the reply never counted.
func TestCallFacts_NewCountFromTimeline(t *testing.T) {
	e := withRules(t)
	e.callReply("a", 1, `{"input_tokens":5}`, &reply{text: "two", stop: "end_turn"}, "user:one")
	e.callReply("b", 2, `{"input_tokens":5}`, &reply{text: "four", stop: "end_turn"}, "user:one", "assistant:two", "user:three")
	e.callReply("c", 3, `{"input_tokens":5}`, &reply{text: "six", stop: "end_turn"}, "user:one", "assistant:two", "user:three", "assistant:four", "user:five")
	f := e.facts()
	for id, want := range map[string]float64{"a": 1, "b": 1, "c": 1} {
		if got := f[id]["new"]; got != want {
			t.Errorf("%s call.new = %v, want %v", id, got, want)
		}
	}
}

// Spec 030 AC11: a cached view serves the title and #n only at the session's whole current
// key. A new effect moves only the effect version (the store version is the same), and the
// fallback title's start comes from a range that includes effects, so that view is stale.
// An unchanged effect version still serves them (TestExchange_CachedCurrentViewGivesTitleAndNumber
// covers the no-effect key; here the effect-bearing key is checked before the change).
func TestExchange_ViewAtOlderEffectVersionGivesNoTitleOrNumber(t *testing.T) {
	e, d := viewEnv(t)
	d.callWith("a", 1, false, nil, "user:fix the retry")
	d.callWith("b", 2, false, nil, "user:fix the retry", "assistant:Fixed.")
	saveEffect := func(path string, at int) {
		t.Helper()
		ef := core.Effect{Kind: core.EffectFileChange, At: baseTime.Add(time.Duration(at) * time.Second),
			Principal: core.PrincipalLocal, Client: "cc", SessionKey: "k", Root: "/w", Path: path,
			Change: core.FileCreated, Mode: "git"}
		if err := e.s.SaveEffects(t.Context(), []core.Effect{ef}, nil); err != nil {
			t.Fatal(err)
		}
	}
	saveEffect("/w/a.txt", 1)
	if code, _ := fetch(t, e, "/api/sessions/"+sid()+"/view"); code != 200 {
		t.Fatalf("view = %d", code)
	}
	m := exchangeFields(t, e, "b")
	if m["session_title"] != "fix the retry" || m["call_n"] != 2.0 {
		t.Fatalf("unchanged effect version: title %v, n %v, want the title and 2", m["session_title"], m["call_n"])
	}
	saveEffect("/w/b.txt", 2) // only the effect version moves
	m = exchangeFields(t, e, "b")
	if _, ok := m["session_title"]; ok {
		t.Errorf("session_title = %v from a view at an older effect version, want none", m["session_title"])
	}
	if _, ok := m["call_n"]; ok {
		t.Errorf("call_n = %v from a view at an older effect version, want none", m["call_n"])
	}
}
