package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// agentRules is testRules that also names the "Agent" tool as spawning sub-agents.
type agentRules struct{ testRules }

func (agentRules) IsAgentTool(n string) bool { return n == "Agent" }

type toolSpec struct{ name, input string }

// callWith stores a call of session k like digestEnv.call, plus tool calls after the
// last message, an error event when failed and a usage event.
func (e *digestEnv) callWith(id string, at int, failed bool, tools []toolSpec, msgs ...string) {
	e.t.Helper()
	e.save(id, "k", "cc", at)
	evs := []core.StoredEvent{{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)}}
	var contents []core.Content
	for i, m := range msgs {
		role, txt, _ := strings.Cut(m, ":")
		bh := sha(fmt.Sprintf("%s-%d", id, i))
		raw, _ := json.Marshal(map[string]string{"type": "text", "text": txt})
		mh := sha("m-" + m)
		contents = append(contents, core.Content{Hash: bh, Bytes: raw}, core.Content{Hash: mh, Bytes: []byte(mh)})
		p, _ := json.Marshal(map[string]any{"role": role, "blocks": []map[string]string{{"hash": bh, "type": "text"}}})
		evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindMessage, ContentHash: mh, Payload: p})
	}
	for i, t := range tools {
		h := sha(fmt.Sprintf("tool-%s-%d", id, i))
		contents = append(contents, core.Content{Hash: h, Bytes: []byte(t.input)})
		evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindToolCall, ToolCallID: fmt.Sprintf("t-%s-%d", id, i),
			ContentHash: h, Payload: []byte(`{"name":"` + t.name + `"}`)})
	}
	if failed {
		evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindError,
			Payload: []byte(`{"status":429,"type":"rate_limit","message":"slow"}`)})
	} else {
		evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindUsage, Payload: []byte(`{"input_tokens":10,"output_tokens":1}`)})
	}
	if err := e.s.SaveParse(e.t.Context(), id, core.PrincipalLocal, core.ParseOK, evs, contents); err != nil {
		e.t.Fatal(err)
	}
}

// facts returns the call facts of each model call by exchange id.
func (e *digestEnv) facts() map[string]map[string]any {
	e.t.Helper()
	p := e.timeline("/api/sessions/" + sid() + "/timeline")
	out := map[string]map[string]any{}
	for _, en := range p.Entries {
		if en["kind"] == "model_call" {
			c, ok := en["call"].(map[string]any)
			if !ok {
				e.t.Fatalf("model_call without call: %v", en)
			}
			out[en["exchange_id"].(string)] = c
		}
	}
	return out
}

func TestTimeline_ModelCallCarriesCallFacts(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 5, "user:one")
	e.call("b", 2, 0, 5, "user:one", "assistant:two", "user:three")
	f := e.facts()
	a, b := f["a"], f["b"]
	if a["n"] != 1.0 || a["thread"] != 0.0 || a["prev_id"] != "" || a["main"] != true || a["side_call"] != false ||
		a["carried"] != 0.0 || a["new"] != 1.0 {
		t.Fatalf("a = %v", a)
	}
	if b["n"] != 2.0 || b["thread"] != 0.0 || b["prev_id"] != "a" || b["main"] != true ||
		b["carried"] != 1.0 || b["new"] != 2.0 {
		t.Fatalf("b = %v", b)
	}
}

func TestTimeline_SideCallFlag(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 5, "user:quota")
	e.call("b", 2, 0, 5, "user:hello")
	f := e.facts()
	if f["a"]["side_call"] != true || f["a"]["main"] != false {
		t.Fatalf("a = %v", f["a"])
	}
	if f["b"]["side_call"] != false || f["b"]["main"] != true {
		t.Fatalf("b = %v", f["b"])
	}
}

func TestTimeline_CallNumbersCountEveryThread(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 5, "user:one")
	e.call("b", 2, 0, 5, "user:quota")
	e.call("c", 3, 0, 5, "user:one", "assistant:x", "user:more")
	f := e.facts()
	if f["a"]["n"] != 1.0 || f["b"]["n"] != 2.0 || f["c"]["n"] != 3.0 {
		t.Fatalf("n = %v %v %v", f["a"]["n"], f["b"]["n"], f["c"]["n"])
	}
	if f["b"]["thread"] == f["a"]["thread"] || f["c"]["thread"] != f["a"]["thread"] || f["c"]["prev_id"] != "a" {
		t.Fatalf("threads: %v", f)
	}
}

func TestTimeline_SubAgentThreadNotMain(t *testing.T) {
	e := newDigestEnv(t, map[string]ClientRules{"cc": agentRules{}})
	e.callWith("a", 1, false, []toolSpec{{"Agent", `{"prompt":"dig into it"}`}}, "user:start")
	e.callWith("s", 2, false, nil, "user:dig into it")
	e.callWith("b", 3, false, nil, "user:start", "assistant:ok", "user:next")
	f := e.facts()
	if f["s"]["main"] != false || f["s"]["side_call"] != false {
		t.Fatalf("sub-agent = %v", f["s"])
	}
	if f["a"]["main"] != true || f["b"]["main"] != true || f["b"]["prev_id"] != "a" {
		t.Fatalf("main calls = %v %v", f["a"], f["b"])
	}
}

func TestTimeline_SubAgentByAgentLabels(t *testing.T) {
	built := []timeline.Entry{
		{Kind: timeline.KindExchange, RequestID: "a", Thread: 0, Call: &timeline.CallUsage{}},
		{Kind: timeline.KindToolCall, RequestID: "a", Name: "bash", AgentID: "ag1"},
	}
	// stub events: both calls hold a user prompt
	src := &promptSource{}
	facts, err := callFacts(src, built, testRules{}, map[string]store.ExchangeRow{"a": {RequestID: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if facts["a"].Main {
		t.Fatalf("labelled thread counted main: %+v", facts["a"])
	}
}

// promptSource serves one user message "hi" for every exchange.
type promptSource struct{ timeline.Source }

func (promptSource) Events(id string) ([]store.EventRow, error) {
	p, _ := json.Marshal(map[string]any{"role": "user", "blocks": []map[string]string{{"hash": "h", "type": "text"}}})
	return []store.EventRow{{RequestID: id, Kind: "message", Payload: p}}, nil
}

func (promptSource) Content(string) ([]byte, error) {
	return []byte(`{"type":"text","text":"hi"}`), nil
}

func TestTimeline_CompactionThreadStaysMain(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 5, "user:one")
	e.call("b", 2, 0, 5, "user:summary of the work so far")
	f := e.facts()
	if f["b"]["thread"] == f["a"]["thread"] || f["b"]["main"] != true {
		t.Fatalf("b = %v", f["b"])
	}
}

func TestTimeline_NoRulesMeansNoSideCalls(t *testing.T) {
	e := newDigestEnv(t, nil)
	e.call("a", 1, 0, 5, "user:quota")
	e.call("b", 2, 0, 5, "user:"+titlePrompt)
	f := e.facts()
	for _, id := range []string{"a", "b"} {
		if f[id]["side_call"] != false || f[id]["main"] != true {
			t.Fatalf("%s = %v", id, f[id])
		}
	}
}

func TestTimeline_ErroredSideCallFlaggedAndNotMain(t *testing.T) {
	e := withRules(t)
	e.callWith("a", 1, true, nil, "user:quota")
	f := e.facts()
	if f["a"]["side_call"] != true || f["a"]["main"] != false {
		t.Fatalf("a = %v", f["a"])
	}
}

func TestStream_ExchangeEventCarriesCallFacts(t *testing.T) {
	e := newStreamEnvRules(t, nil, map[string]ClientRules{"cc": testRules{}})
	s := e.open("/api/sessions/" + sid() + "/stream")
	e.save("a", "k", "cc", 10)
	e.parseMsgs("a", "ok", []any{"user", text("quota")})
	e.hub.Publish(capture.Committed{RequestID: "a", PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "k"})
	ev := s.nextData()
	entries := decodeEntries(t, ev.data)
	c, ok := entries[0]["call"].(map[string]any)
	if !ok || c["n"] != 1.0 || c["side_call"] != true || c["main"] != false {
		t.Fatalf("entries = %v", entries)
	}
}
