package core_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// excludedKey stands in for an adapter's hash-excluded wire field. Core names none of
// its own, so the tests pick a neutral one.
const excludedKey = "hint"

func canonicalize(t *testing.T, events []core.Event) ([]core.StoredEvent, map[string][]byte) {
	t.Helper()
	stored, contents, err := core.Canonicalize(events, []string{excludedKey})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	byHash := make(map[string][]byte, len(contents))
	for _, c := range contents {
		if _, dup := byHash[c.Hash]; dup {
			t.Errorf("content %s returned twice", c.Hash)
		}
		byHash[c.Hash] = c.Bytes
	}
	return stored, byHash
}

func textMessage(role string, blocks ...string) core.Event {
	m := &core.MessageEvent{Role: role, Source: core.SourceRequestHistory}
	for _, b := range blocks {
		m.Blocks = append(m.Blocks, core.Block{Type: core.BlockText, Content: json.RawMessage(b)})
	}
	return core.Event{Kind: core.KindMessage, Message: m}
}

// blockHashes reads the block hashes out of a stored message event's payload.
func blockHashes(t *testing.T, ev core.StoredEvent) []string {
	t.Helper()
	var p struct {
		Blocks []struct {
			Hash string `json:"hash"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatalf("payload %s: %v", ev.Payload, err)
	}
	hashes := make([]string, 0, len(p.Blocks))
	for _, b := range p.Blocks {
		hashes = append(hashes, b.Hash)
	}
	return hashes
}

func TestCanonicalize_KeyOrderAndNumbers(t *testing.T) {
	a := `{"type":"text","text":"hi","meta":{"z":1.50,"a":12345678901234567890}}`
	b := "{\n  \"meta\": {\"a\": 12345678901234567890, \"z\": 1.50},\n  \"text\": \"hi\", \"type\": \"text\"\n}"
	stored, contents := canonicalize(t, []core.Event{textMessage("user", a), textMessage("user", b)})

	ha, hb := blockHashes(t, stored[0]), blockHashes(t, stored[1])
	if len(ha) != 1 || len(hb) != 1 || ha[0] != hb[0] {
		t.Fatalf("block hashes %v and %v differ; key order and whitespace must not matter", ha, hb)
	}
	want := `{"meta":{"a":12345678901234567890,"z":1.50},"text":"hi","type":"text"}`
	if got := string(contents[ha[0]]); got != want {
		t.Errorf("canonical form = %s, want %s (sorted, compact, numbers as sent)", got, want)
	}
	if len(ha[0]) != 64 || strings.ToLower(ha[0]) != ha[0] {
		t.Errorf("hash %q is not lowercase hex sha256", ha[0])
	}
	if stored[0].ContentHash != stored[1].ContentHash {
		t.Error("the two messages carry the same blocks but hash differently")
	}
	if _, ok := contents[stored[0].ContentHash]; !ok {
		t.Error("the message's own content is not among the returned contents")
	}
}

func TestCanonicalize_HTMLKept(t *testing.T) {
	stored, contents := canonicalize(t, []core.Event{
		textMessage("user", `{"type":"text","text":"<a href=\"x\">&amp;</a>"}`),
	})
	got := string(contents[blockHashes(t, stored[0])[0]])
	if !strings.Contains(got, `<a href=\"x\">&amp;</a>`) {
		t.Errorf("canonical form %s escaped HTML characters", got)
	}
	for _, p := range [][]byte{stored[0].Payload, contents[stored[0].ContentHash]} {
		if strings.Contains(string(p), `<`) || strings.Contains(string(p), `&`) {
			t.Errorf("%s escaped HTML characters", p)
		}
	}
}

func toolCallEvent(id, input string) core.Event {
	return core.Event{Kind: core.KindToolCall, ToolCall: &core.ToolCallEvent{
		ID: id, Name: "lookup", Input: json.RawMessage(input),
		ExecutedBy: core.ExecutedByClient, Source: core.SourceRequestHistory,
		Raw: json.RawMessage(`{"id":"` + id + `","input":` + input + `}`),
	}}
}

func TestCanonicalize_ExclusionTopLevelOnly(t *testing.T) {
	t.Run("block top level excluded", func(t *testing.T) {
		stored, contents := canonicalize(t, []core.Event{
			textMessage("user", `{"type":"text","text":"x"}`),
			textMessage("user", `{"type":"text","text":"x","hint":{"k":"v"}}`),
		})
		h0, h1 := blockHashes(t, stored[0])[0], blockHashes(t, stored[1])[0]
		if h0 != h1 {
			t.Errorf("a top-level excluded key changed the block hash")
		}
		if strings.Contains(string(contents[h1]), excludedKey) {
			t.Errorf("stored block %s still has the excluded key", contents[h1])
		}
	})
	t.Run("nested in block kept", func(t *testing.T) {
		stored, _ := canonicalize(t, []core.Event{
			textMessage("user", `{"type":"text","text":"x","meta":{}}`),
			textMessage("user", `{"type":"text","text":"x","meta":{"hint":1}}`),
		})
		if blockHashes(t, stored[0])[0] == blockHashes(t, stored[1])[0] {
			t.Error("an excluded key below the block's top level was removed")
		}
	})
	t.Run("tool input never excluded", func(t *testing.T) {
		stored, contents := canonicalize(t, []core.Event{
			toolCallEvent("c1", `{"q":"x","opts":{}}`),
			toolCallEvent("c2", `{"q":"x","opts":{"hint":"a"}}`),
			toolCallEvent("c3", `{"q":"x","opts":{},"hint":"a"}`),
		})
		if stored[0].ContentHash == stored[1].ContentHash {
			t.Error("a nested excluded key in tool input did not change its hash")
		}
		if stored[0].ContentHash == stored[2].ContentHash {
			t.Error("a top-level excluded key in tool input did not change its hash")
		}
		if !strings.Contains(string(contents[stored[2].ContentHash]), excludedKey) {
			t.Error("the excluded key was removed from tool input")
		}
	})
	t.Run("tool result content never excluded", func(t *testing.T) {
		result := func(content string) core.Event {
			return core.Event{Kind: core.KindToolResult, ToolResult: &core.ToolResultEvent{
				ToolCallID: "c1", Content: json.RawMessage(content),
				ExecutedBy: core.ExecutedByClient, Source: core.SourceRequestHistory,
			}}
		}
		stored, _ := canonicalize(t, []core.Event{
			result(`[{"type":"text","text":"x"}]`),
			result(`[{"type":"text","text":"x","hint":{}}]`),
		})
		if stored[0].ContentHash == stored[1].ContentHash {
			t.Error("an excluded key in tool result content did not change its hash")
		}
	})
	t.Run("system and tools entries excluded", func(t *testing.T) {
		req := func(system, tools string) core.Event {
			return core.Event{Kind: core.KindRequest, Request: &core.RequestEvent{
				Model: "m", System: json.RawMessage(system), Tools: json.RawMessage(tools),
				HasSystem: true, HasTools: true,
			}}
		}
		hashes := func(ev core.StoredEvent) (string, string) {
			var p struct {
				System string `json:"system_hash"`
				Tools  string `json:"tools_hash"`
			}
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatalf("payload %s: %v", ev.Payload, err)
			}
			return p.System, p.Tools
		}
		stored, _ := canonicalize(t, []core.Event{
			req(`[{"type":"text","text":"s"}]`, `[{"name":"t","input_schema":{}}]`),
			req(`[{"type":"text","text":"s","hint":1}]`, `[{"name":"t","input_schema":{},"hint":1}]`),
			req(`[{"type":"text","text":"s"}]`, `[{"name":"t","input_schema":{"hint":1}}]`),
		})
		s0, t0 := hashes(stored[0])
		s1, t1 := hashes(stored[1])
		_, t2 := hashes(stored[2])
		if s0 == "" || t0 == "" {
			t.Fatalf("request payload %s has no system or tools hash", stored[0].Payload)
		}
		if s0 != s1 || t0 != t1 {
			t.Error("a top-level excluded key on a system entry or tool definition changed its hash")
		}
		if t0 == t2 {
			t.Error("an excluded key inside a tool definition was removed")
		}
	})
}

func TestCanonicalize_MessageHash(t *testing.T) {
	msg := func(stop string, idx int, src core.Source, blocks ...core.Block) core.Event {
		return core.Event{Kind: core.KindMessage, Message: &core.MessageEvent{
			Index: idx, Role: "assistant", Source: src, StopReason: stop, Blocks: blocks,
		}}
	}
	text := core.Block{Type: core.BlockText, Content: json.RawMessage(`{"type":"text","text":"a"}`)}
	reason := core.Block{Type: core.BlockReasoning, Content: json.RawMessage(`{"type":"r","text":"b"}`)}
	call := core.Block{Type: core.BlockToolCall, ToolCallID: "c1"}

	stored, _ := canonicalize(t, []core.Event{
		msg("end", -1, core.SourceResponse, reason, text, call),
		toolCallEvent("c1", `{"q":1}`),
		msg("", 3, core.SourceRequestHistory, reason, text, call),
		toolCallEvent("c1", `{"q":1}`),
		msg("", 3, core.SourceRequestHistory, text, reason, call),
		toolCallEvent("c1", `{"q":1}`),
		msg("", 3, core.SourceRequestHistory, reason, text, call),
		toolCallEvent("c1", `{"q":2}`),
	})
	response, resent, reordered, otherInput := stored[0], stored[2], stored[4], stored[6]
	if response.ContentHash == "" {
		t.Fatal("message event has no content hash")
	}
	if response.ContentHash != resent.ContentHash {
		t.Error("the same blocks hashed differently; stop_reason, index or source leaked into the hash")
	}
	if response.ContentHash == reordered.ContentHash {
		t.Error("reordered blocks kept the same hash")
	}
	if response.ContentHash == otherInput.ContentHash {
		t.Error("a tool call with different input kept the same message hash")
	}

	role, _ := canonicalize(t, []core.Event{
		textMessage("user", `{"type":"text","text":"a"}`),
		textMessage("assistant", `{"type":"text","text":"a"}`),
	})
	if role[0].ContentHash == role[1].ContentHash {
		t.Error("a different role kept the same message hash")
	}

	if _, _, err := core.Canonicalize([]core.Event{msg("", 0, core.SourceResponse, call)}, nil); err == nil {
		t.Error("a tool_call block with no tool_call event was accepted")
	}
}

func TestCanonicalize_Seq(t *testing.T) {
	events := []core.Event{
		{Kind: core.KindRequest, Request: &core.RequestEvent{Model: "m"}},
		textMessage("user", `{"type":"text","text":"a"}`),
		toolCallEvent("c1", `{}`),
		{Kind: core.KindUsage, Usage: &core.UsageEvent{}},
		{Kind: core.KindError, Error: &core.ErrorEvent{Status: 500, Type: "x", Message: "y"}},
	}
	stored, _ := canonicalize(t, events)
	if len(stored) != len(events) {
		t.Fatalf("got %d stored events, want %d", len(stored), len(events))
	}
	for i, ev := range stored {
		if ev.Seq != i || ev.Kind != events[i].Kind {
			t.Errorf("stored[%d] = seq %d kind %s, want seq %d kind %s", i, ev.Seq, ev.Kind, i, events[i].Kind)
		}
		var p struct {
			Version int            `json:"schema_version"`
			Kind    core.EventKind `json:"kind"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("payload %s: %v", ev.Payload, err)
		}
		if p.Version != core.SchemaVersion || p.Kind != events[i].Kind {
			t.Errorf("payload %s: want schema_version %d and kind %s", ev.Payload, core.SchemaVersion, events[i].Kind)
		}
	}
	if stored[2].ToolCallID != "c1" || stored[2].Source != core.SourceRequestHistory {
		t.Errorf("tool_call stored as %+v; want its id and source on the row", stored[2])
	}

	if _, _, err := core.Canonicalize([]core.Event{{Kind: core.KindMessage}}, nil); err == nil {
		t.Error("an event whose Kind has no matching payload was accepted")
	}
}
