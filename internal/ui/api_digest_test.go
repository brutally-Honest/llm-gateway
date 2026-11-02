package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// testRules mimics a client's rules: "quota" alone is a side call, so is a "<session>"
// prompt asking to "Write the title"; a title reply is {"title":"…"}.
type testRules struct{}

func (testRules) IsSideCall(texts []string) bool {
	if len(texts) == 1 && texts[0] == "quota" {
		return true
	}
	for _, t := range texts {
		if strings.HasPrefix(t, "<session>") && strings.Contains(t, "Write the title") {
			return true
		}
	}
	return false
}

func (testRules) StripReminders(s string) string {
	for {
		i := strings.Index(s, "<r>")
		j := strings.Index(s, "</r>")
		if i < 0 || j < i {
			return strings.TrimSpace(s)
		}
		s = s[:i] + s[j+4:]
	}
}

func (testRules) SplitReminders(s string) (string, []string) {
	var out []string
	for {
		i := strings.Index(s, "<r>")
		j := strings.Index(s, "</r>")
		if i < 0 || j < i {
			return strings.TrimSpace(s), out
		}
		out = append(out, s[i:j+4])
		s = s[:i] + s[j+4:]
	}
}

func (testRules) TitleOf(s string) (string, bool) {
	var v struct {
		Title string `json:"title"`
	}
	if json.Unmarshal([]byte(s), &v) != nil || v.Title == "" {
		return "", false
	}
	return v.Title, true
}

func (testRules) IsAgentTool(string) bool { return false }

const titlePrompt = "<session>\nfix bug\n</session>\nWrite the title"

type digestEnv struct {
	*apiEnv
	d Deps
}

func newDigestEnv(t *testing.T, rules map[string]ClientRules) *digestEnv {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	d := Deps{Addr: addr, Reader: r, Rules: rules, Now: func() time.Time { return baseTime.Add(100 * time.Second) }}
	return &digestEnv{apiEnv: &apiEnv{t: t, s: s, dir: dir, h: Handler(d)}, d: d}
}

func withRules(t *testing.T) *digestEnv {
	return newDigestEnv(t, map[string]ClientRules{"cc": testRules{}})
}

// call stores an exchange of session k holding the given messages ("role:text"), a
// request event, errs error events and, when in is not 0, a usage event.
func (e *digestEnv) call(id string, at int, errs, in int, msgs ...string) {
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
	for range errs {
		evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindError,
			Payload: []byte(`{"status":500,"type":"api_error","message":"boom"}`)})
	}
	if in != 0 {
		evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindUsage,
			Payload: []byte(fmt.Sprintf(`{"input_tokens":%d,"output_tokens":1}`, in))})
	}
	if err := e.s.SaveParse(e.t.Context(), id, core.PrincipalLocal, core.ParseOK, evs, contents); err != nil {
		e.t.Fatal(err)
	}
}

func (e *digestEnv) summary() map[string]any {
	e.t.Helper()
	p := e.page("/api/sessions")
	if len(p.Items) != 1 {
		e.t.Fatalf("items = %d", len(p.Items))
	}
	return p.Items[0]
}

func TestSummary_TitleIsLatestGenerated(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 0, "user:hello there")
	e.call("b", 2, 0, 0, "user:"+titlePrompt, `assistant:{"title":"First"}`)
	e.call("c", 3, 0, 0, "user:"+titlePrompt+" again", `assistant:{"title":"Second"}`)
	if got := e.summary()["title"]; got != "Second" {
		t.Fatalf("title = %v", got)
	}
}

func TestSummary_TitleFallsBackToFirstPrompt(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 0, "user:"+strings.Repeat("x", 100))
	e.call("b", 2, 0, 0, "user:second")
	if got := e.summary()["title"]; got != strings.Repeat("x", 80) {
		t.Fatalf("title = %v", got)
	}
}

func TestSummary_FailedTitleIgnored(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 0, "user:hello")
	e.call("b", 2, 0, 0, "user:"+titlePrompt, `assistant:{"title":"Good"}`)
	e.call("c", 3, 1, 0, "user:"+titlePrompt+" x", `assistant:{"title":"Bad"}`)
	e.call("d", 4, 0, 0, "user:"+titlePrompt+" y", `assistant:`)
	e.call("e", 5, 0, 0, "user:"+titlePrompt+" z", `assistant:not json`)
	if got := e.summary()["title"]; got != "Good" {
		t.Fatalf("title = %v, want the earlier stored title", got)
	}
	e2 := withRules(t)
	e2.call("a", 1, 0, 0, "user:hello")
	e2.call("c", 3, 1, 0, "user:"+titlePrompt, `assistant:{"title":"Bad"}`)
	if got := e2.summary()["title"]; got != "hello" {
		t.Fatalf("title = %v, want the first prompt", got)
	}
}

func TestSummary_TitleSkipsReminderOnlyPrompt(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 0, "user:<r>only a reminder</r>")
	e.call("b", 2, 0, 0, "user:<r>x</r>real prompt")
	if got := e.summary()["title"]; got != "real prompt" {
		t.Fatalf("title = %v", got)
	}
}

func TestSummary_NoPromptTitle(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 0, "user:<r>only a reminder</r>", "assistant:hi")
	if got := e.summary()["title"]; got != "" {
		t.Fatalf("title = %q, want empty", got)
	}
}

func TestSummary_OnlySideCalls(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 0, "user:quota", "assistant:ok")
	s := e.summary()
	if s["title"] != "" || s["main_errors"] != float64(0) || s["has_calls"] != true {
		t.Fatalf("summary = %v", s)
	}
	e.call("b", 2, 0, 0, "user:"+titlePrompt, `assistant:{"title":"T"}`)
	if got := e.summary()["title"]; got != "T" {
		t.Fatalf("title = %v", got)
	}
}

func TestSummary_NoRulesShowsPromptsWhole(t *testing.T) {
	e := newDigestEnv(t, nil)
	e.call("a", 1, 2, 0, "user:quota")
	e.call("b", 2, 0, 0, "user:<r>kept</r> text", `assistant:{"title":"T"}`)
	s := e.summary()
	if s["title"] != "quota" || s["main_errors"] != float64(2) || s["side_errors"] != float64(0) {
		t.Fatalf("summary = %v", s)
	}
}

func TestSummary_MainErrorsExcludeSideCalls(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 1, 0, "user:hello")
	e.call("b", 2, 2, 0, "user:quota")
	s := e.summary()
	if s["main_errors"] != float64(1) || s["side_errors"] != float64(2) {
		t.Fatalf("summary = %v", s)
	}
}

func TestSummary_ErrorsStaysAllErrors(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 1, 0, "user:hello")
	e.call("b", 2, 2, 0, "user:quota")
	id := sessionID(store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "k"})
	for name, s := range map[string]map[string]any{"list": e.summary(), "get": e.getMap("/api/sessions/" + id)} {
		if s["errors"] != float64(3) || s["errors"] != s["main_errors"].(float64)+s["side_errors"].(float64) {
			t.Errorf("%s: summary = %v", name, s)
		}
	}
}

func (e *digestEnv) getMap(path string) map[string]any {
	e.t.Helper()
	rec := e.get(path)
	if rec.Code != 200 {
		e.t.Fatalf("%s = %d %s", path, rec.Code, rec.Body)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

func TestSummary_SideCallsStillCountInTokens(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 10, "user:hello")
	e.call("b", 2, 0, 5, "user:quota")
	u := e.summary()["usage"].(map[string]any)
	if u["calls"] != float64(2) || u["input"].(map[string]any)["value"] != float64(15) {
		t.Fatalf("usage = %v", u)
	}
}

func TestSummary_ToolStateCounts(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 0, "user:hello")
	for _, id := range []string{"t1", "t2"} {
		e.addTool("a", id)
	}
	st := e.summary()["tool_states"].(map[string]any)
	if st["no-hooks"] != float64(2) || len(st) != 6 {
		t.Fatalf("tool_states = %v", st)
	}
	hookSeq++
	rec := &store.HookRecord{PrincipalID: core.PrincipalLocal, Client: "cc", Seq: hookSeq, ReceivedAt: baseTime,
		Payload: []byte(`{"n":1}`), Event: core.HarnessEvent{Phase: core.PhasePostTool, ToolCallID: "t1", ToolName: "bash", SessionKey: "k"}}
	if err := e.s.SaveHook(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	st = e.summary()["tool_states"].(map[string]any)
	if st["pending"] != float64(2) || st["no-hooks"] != float64(0) {
		t.Fatalf("tool_states = %v", st)
	}
}

// addTool re-parses exchange id with one more tool call; it replaces nothing else.
func (e *digestEnv) addTool(id, call string) {
	e.t.Helper()
	// Tool calls go in a new exchange that extends the first one's message.
	rid := id + "-" + call
	e.save(rid, "k", "cc", 10+len(call)+int(call[len(call)-1]))
	mh := sha("m-user:hello")
	bh := sha(id + "-0")
	p, _ := json.Marshal(map[string]any{"role": "user", "blocks": []map[string]string{{"hash": bh, "type": "text"}}})
	ap, _ := json.Marshal(map[string]any{"role": "assistant", "blocks": []map[string]string{}})
	evs := []core.StoredEvent{
		{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)},
		{Seq: 1, Kind: core.KindMessage, ContentHash: mh, Payload: p},
		{Seq: 2, Kind: core.KindMessage, ContentHash: sha("m-asst" + call), Payload: ap},
		{Seq: 3, Kind: core.KindToolCall, ToolCallID: call, ContentHash: sha("in" + call), Payload: []byte(`{"name":"bash","id":"` + call + `"}`)},
	}
	if err := e.s.SaveParse(e.t.Context(), rid, core.PrincipalLocal, core.ParseOK, evs,
		[]core.Content{{Hash: sha("in" + call), Bytes: []byte(`{}`)},
			{Hash: sha("m-asst" + call), Bytes: []byte("a")}, {Hash: mh, Bytes: []byte(mh)}, {Hash: bh, Bytes: []byte(`{"text":"hello"}`)}}); err != nil {
		e.t.Fatal(err)
	}
}

func TestSummary_Sources(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 0, "user:hello")
	s := e.summary()
	if s["has_calls"] != true || s["has_hooks"] != false {
		t.Fatalf("api only = %v", s)
	}
	hookSeq++
	rec := &store.HookRecord{PrincipalID: core.PrincipalLocal, Client: "cc", Seq: hookSeq, ReceivedAt: baseTime,
		Payload: []byte(`{"n":1}`), Event: core.HarnessEvent{Phase: core.PhaseNotice, SessionKey: "k"}}
	if err := e.s.SaveHook(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	rec2 := *rec
	rec2.Seq, rec2.Event.SessionKey, rec2.Payload = hookSeq+1000, "solo", []byte(`{"n":2}`)
	if err := e.s.SaveHook(t.Context(), &rec2); err != nil {
		t.Fatal(err)
	}
	for _, it := range e.page("/api/sessions").Items {
		calls, hooks := it["has_calls"], it["has_hooks"]
		switch it["session_key"] {
		case "k":
			if calls != true || hooks != true {
				t.Errorf("both = %v", it)
			}
		case "solo":
			if calls != false || hooks != true {
				t.Errorf("hooks only = %v", it)
			}
		}
	}
}

func TestHub_SessionEventCarriesNewTitle(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 0, "user:hello")
	who := sessIdent{core.PrincipalLocal, "cc", "k"}
	m, err := readLive(t.Context(), e.d, who, nil, true, false)
	if err != nil || m.Summary == nil || m.Summary.Title != "hello" {
		t.Fatalf("before: %+v, %v", m.Summary, err)
	}
	e.call("b", 2, 0, 0, "user:"+titlePrompt, `assistant:{"title":"New title"}`)
	m, err = readLive(t.Context(), e.d, who, nil, true, false)
	if err != nil || m.Summary == nil || m.Summary.Title != "New title" {
		t.Fatalf("after: %+v, %v", m.Summary, err)
	}
	snap, err := readSnapshot(t.Context(), e.d, who.id())
	if err != nil || len(snap.Sessions) != 1 || snap.Sessions[0].Title != "New title" {
		t.Fatalf("snapshot: %+v, %v", snap, err)
	}
}
