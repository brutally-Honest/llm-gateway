package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// memoCall stores one exchange of session key whose messages ("role:text") resend the
// history. A message hash is a function of role and text, as a real one is. Texts in
// missing have no stored content; "nohash:" messages carry an empty content hash. tools
// adds a tool call and its result, errs an error event.
func (e *digestEnv) memoCall(id, key string, at int, missing map[string]bool, tools, errs int, msgs ...string) {
	e.t.Helper()
	e.save(id, key, "cc", at)
	evs := []core.StoredEvent{{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)}}
	var contents []core.Content
	for _, m := range msgs {
		noHash := strings.HasPrefix(m, "nohash:")
		m = strings.TrimPrefix(m, "nohash:")
		role, txt, _ := strings.Cut(m, ":")
		bh, mh := sha("b-"+m), sha("m-"+m)
		if !missing[m] {
			raw, _ := json.Marshal(map[string]string{"type": "text", "text": txt})
			contents = append(contents, core.Content{Hash: bh, Bytes: raw})
		}
		if noHash {
			mh = ""
		} else {
			contents = append(contents, core.Content{Hash: mh, Bytes: []byte(mh)})
		}
		p, _ := json.Marshal(map[string]any{"role": role, "blocks": []map[string]string{{"hash": bh, "type": "text"}}})
		evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindMessage, ContentHash: mh, Payload: p})
	}
	for i := range tools {
		call := fmt.Sprintf("t-%s-%d", id, i)
		in := sha("in" + call)
		contents = append(contents, core.Content{Hash: in, Bytes: []byte(`{"command":"ls"}`)},
			core.Content{Hash: sha("out" + call), Bytes: []byte(`"done"`)})
		evs = append(evs,
			core.StoredEvent{Seq: len(evs), Kind: core.KindToolCall, ToolCallID: call, ContentHash: in,
				Payload: []byte(`{"name":"Bash","id":"` + call + `"}`)},
			core.StoredEvent{Seq: len(evs) + 1, Kind: core.KindToolResult, ToolCallID: call, ContentHash: sha("out" + call),
				Payload: []byte(`{"is_error":false}`)})
	}
	for range errs {
		evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindError,
			Payload: []byte(`{"status":500,"type":"api_error","message":"boom"}`)})
	}
	evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindUsage, Payload: []byte(`{"input_tokens":10,"output_tokens":1}`)})
	if err := e.s.SaveParse(e.t.Context(), id, core.PrincipalLocal, core.ParseOK, evs, contents); err != nil {
		e.t.Fatal(err)
	}
}

// memoOutputs is everything a session build produces, marshalled.
func memoOutputs(t *testing.T, e *digestEnv, key string) map[string][]byte {
	t.Helper()
	src := e.d.Reader.WithContext(t.Context())
	row := store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: key}
	vd := viewDeps{rules: map[string]ClientRules{"cc": testRules{}}}
	m, err := computeMemo(src, vd, row)
	if err != nil {
		t.Fatal(err)
	}
	who := sessIdent{core.PrincipalLocal, "cc", key}
	bs, err := buildSession(src, who, testRules{}, entryCut)
	if err != nil {
		t.Fatal(err)
	}
	hooks, err := src.HookEvents(row.PrincipalID, row.Client, row.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	v, err := vd.sessionView(src, who, bs, hooks, false, "")
	if err != nil {
		t.Fatal(err)
	}
	enc := func(v any) []byte {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	return map[string][]byte{
		"digest":  enc(m.digest),
		"stats":   enc(m.stats),
		"row":     []byte(fmt.Sprintf("%+v", m.row)),
		"entries": enc(bs.entries),
		"facts":   enc(bs.facts),
		"calls":   enc(bs.calls),
		"view":    enc(v),
	}
}

// The text memo only skips repeated work: with it on or off, a session's digest, stats,
// list row, entries, call facts and view are byte-identical (spec 030 AC1).
func TestTextMemo_OutputUnchanged(t *testing.T) {
	e := withRules(t)
	none := map[string]bool{}
	gone := map[string]bool{"user:lost text": true}
	e.memoCall("a1", "k", 1, none, 0, 0, "user:fix the bug")
	e.memoCall("a2", "k", 2, none, 1, 0, "user:fix the bug", "assistant:looking", "user:more")
	e.memoCall("a3", "k", 3, gone, 2, 1, "user:fix the bug", "assistant:looking", "user:more", "assistant:ok", "user:lost text")
	e.memoCall("a4", "k", 4, gone, 0, 0, "user:fix the bug", "assistant:looking", "user:more", "assistant:ok", "user:lost text", "assistant:done", "user:same")
	e.memoCall("a5", "k", 5, none, 0, 0, "user:same", "assistant:same") // texts shared across roles
	e.memoCall("a6", "k", 6, none, 0, 2, "user:quota")                  // a side call
	e.memoCall("a7", "k", 7, none, 0, 0, "user:"+titlePrompt, `assistant:{"title":"Fix the bug"}`)
	e.memoCall("a8", "k", 8, none, 1, 0, "user:a second thread", "nohash:assistant:no hash", "user:go on")
	e.memoCall("a9", "k", 9, none, 0, 0, "user:a second thread", "nohash:assistant:no hash", "user:go on", "assistant:yes", "user:next")
	e.memoCall("b1", "k2", 10, none, 0, 0, "user:other session")
	e.memoCall("b2", "k2", 11, none, 1, 1, "user:other session", "assistant:ok", "user:fix the bug")

	for _, key := range []string{"k", "k2"} {
		textMemoOff = true
		want := memoOutputs(t, e, key)
		textMemoOff = false
		got := memoOutputs(t, e, key)
		for name, w := range want {
			if len(w) == 0 {
				t.Fatalf("%s %s: empty reference", key, name)
			}
			if !bytes.Equal(w, got[name]) {
				t.Errorf("%s %s differs:\n memo off: %s\n memo on:  %s", key, name, w, got[name])
			}
		}
	}
	if !bytes.Contains(memoOutputs(t, e, "k")["digest"], []byte(`"Title":"Fix the bug"`)) {
		t.Fatalf("seed has no generated title: %s", memoOutputs(t, e, "k")["digest"])
	}
}
