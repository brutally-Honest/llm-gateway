package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

var baseTime = time.Unix(1700000000, 0)

type apiEnv struct {
	t   *testing.T
	dir string
	s   *store.Store
	h   http.Handler
}

func newAPIEnv(t *testing.T) *apiEnv {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return openEnv(t, dir, s)
}

func openEnv(t *testing.T, dir string, s *store.Store) *apiEnv {
	t.Helper()
	e := &apiEnv{t: t, s: s, dir: dir}
	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	e.h = Handler(Deps{Addr: addr, Reader: r, Now: func() time.Time { return baseTime.Add(100 * time.Second) }})
	return e
}

// save stores an exchange at base+at seconds; key "" is ungrouped.
func (e *apiEnv) save(id, key, client string, at int) {
	e.t.Helper()
	ex := &core.Exchange{
		RequestID: id, PrincipalID: core.PrincipalLocal, Protocol: "test", Client: client,
		Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/x", Status: 200, SessionKey: key,
		Start: baseTime.Add(time.Duration(at) * time.Second),
	}
	ex.End = ex.Start.Add(time.Second)
	if err := e.s.SaveExchange(e.t.Context(), ex); err != nil {
		e.t.Fatal(err)
	}
}

func (e *apiEnv) parse(id, model, usage string) {
	e.t.Helper()
	evs := []core.StoredEvent{{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"` + model + `"}`)}}
	if usage != "" {
		evs = append(evs, core.StoredEvent{Seq: 1, Kind: core.KindUsage, Payload: []byte(usage)})
	}
	if err := e.s.SaveParse(e.t.Context(), id, core.PrincipalLocal, core.ParseOK, evs, nil); err != nil {
		e.t.Fatal(err)
	}
}

func (e *apiEnv) get(path string) *httptest.ResponseRecorder {
	e.t.Helper()
	return do(e.t, e.h, "GET", path, nil)
}

type page struct {
	Items     []map[string]any `json:"items"`
	NextAfter *string          `json:"next_after"`
}

func (e *apiEnv) page(path string) page {
	e.t.Helper()
	rec := e.get(path)
	if rec.Code != 200 {
		e.t.Fatalf("%s = %d %s", path, rec.Code, rec.Body)
	}
	var p page
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func keys(p page) []string {
	var out []string
	for _, it := range p.Items {
		out = append(out, it["session_key"].(string))
	}
	return out
}

func TestAPI_SessionsPagedNewestFirst(t *testing.T) {
	e := newAPIEnv(t)
	for i := range 5 {
		e.save(fmt.Sprintf("r%d", i), fmt.Sprintf("s%d", i), "cc", i+1)
	}
	p := e.page("/api/sessions?limit=2")
	if got := keys(p); fmt.Sprint(got) != "[s4 s3]" || p.NextAfter == nil {
		t.Fatalf("page 1 = %v next %v", got, p.NextAfter)
	}
	var all []string
	all = append(all, keys(p)...)
	for p.NextAfter != nil {
		p = e.page("/api/sessions?limit=2&after=" + *p.NextAfter)
		all = append(all, keys(p)...)
	}
	if fmt.Sprint(all) != "[s4 s3 s2 s1 s0]" {
		t.Fatalf("walk = %v", all)
	}
	// Default limit lists everything with no next_after.
	if p := e.page("/api/sessions"); len(p.Items) != 5 || p.NextAfter != nil {
		t.Fatalf("default page = %d next %v", len(p.Items), p.NextAfter)
	}
	// Above the cap clamps instead of failing.
	if rec := e.get("/api/sessions?limit=100000"); rec.Code != 200 {
		t.Fatalf("over cap = %d", rec.Code)
	}
}

func TestAPI_UngroupedSummaryShape(t *testing.T) {
	e := newAPIEnv(t)
	e.save("u1", "", "cc", 1)
	e.parse("u1", "m1", `{"input_tokens":10,"output_tokens":5}`)
	e.save("u2", "", "cc", 2)
	p := e.page("/api/ungrouped?limit=1")
	if len(p.Items) != 1 || p.NextAfter == nil {
		t.Fatalf("page = %+v", p)
	}
	if p.Items[0]["id"] != "u2" || p.Items[0]["exchange_count"] != 1.0 || p.Items[0]["model_calls"] != 0.0 {
		t.Fatalf("first = %v", p.Items[0])
	}
	p = e.page("/api/ungrouped?limit=5&after=" + *p.NextAfter)
	var ids []any
	for _, it := range p.Items {
		ids = append(ids, it["id"])
	}
	if fmt.Sprint(ids) != "[u1]" || p.NextAfter != nil {
		t.Fatalf("second page ids = %v", ids)
	}
	it := p.Items[0]
	usage := it["usage"].(map[string]any)
	if it["model_calls"] != 1.0 || usage["input"].(map[string]any)["value"] != 10.0 {
		t.Fatalf("u1 = %v", it)
	}
	if fmt.Sprint(it["models"]) != "[m1]" {
		t.Fatalf("models = %v", it["models"])
	}
}

func TestAPI_SessionSummary(t *testing.T) {
	e := newAPIEnv(t)
	e.save("a", "k", "cc", 10)
	e.parse("a", "m1", `{"input_tokens":10,"output_tokens":5}`)
	e.save("b", "k", "cc", 20)
	e.parse("b", "m2", "")
	e.save("old", "k2", "cc", -1000)
	list := e.page("/api/sessions")
	if list.Items[0]["session_key"] != "k" {
		t.Fatalf("order = %v", keys(list))
	}
	id := list.Items[0]["id"].(string)
	rec := e.get("/api/sessions/" + id)
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	var s map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	if fmt.Sprint(s) != fmt.Sprint(list.Items[0]) {
		t.Errorf("detail differs from list item:\n%v\n%v", s, list.Items[0])
	}
	if s["exchange_count"] != 2.0 || s["model_calls"] != 2.0 || s["client"] != "cc" || s["active"] != true {
		t.Errorf("summary = %v", s)
	}
	if fmt.Sprint(s["models"]) != "[m1 m2]" {
		t.Errorf("models = %v", s["models"])
	}
	u := s["usage"].(map[string]any)
	if u["missing"] != 1.0 || u["incomplete"] != true || u["input"].(map[string]any)["value"] != 10.0 {
		t.Errorf("usage = %v", u)
	}
	if s["first_at"] != baseTime.Add(10*time.Second).UTC().Format(time.RFC3339Nano) {
		t.Errorf("first_at = %v", s["first_at"])
	}
	if list.Items[1]["active"] != false {
		t.Errorf("old session active = %v", list.Items[1]["active"])
	}
	wantEnvelope(t, e.get("/api/sessions/nope"), 404, "not_found")
	other := encodeID(t, "local", "cc", "missing")
	wantEnvelope(t, e.get("/api/sessions/"+other), 404, "not_found")
}

func encodeID(t *testing.T, p, c, k string) string {
	t.Helper()
	return sessionID(store.SessionRow{PrincipalID: p, Client: c, SessionKey: k})
}

func TestAPI_EmptyStoreEmptyLists(t *testing.T) {
	e := newAPIEnv(t)
	// /api/sessions also carries total and scanned (spec 029); /api/ungrouped keeps its shape.
	for p, want := range map[string]string{
		"/api/sessions":  `{"items":[],"next_after":null,"total":0,"scanned":0}`,
		"/api/ungrouped": `{"items":[],"next_after":null}`,
	} {
		rec := e.get(p)
		if rec.Code != 200 || rec.Body.String() != want+"\n" {
			t.Fatalf("%s = %d %s", p, rec.Code, rec.Body)
		}
	}
}

func TestAPI_LimitValidation(t *testing.T) {
	e := newAPIEnv(t)
	for _, base := range []string{"/api/sessions", "/api/ungrouped"} {
		for _, q := range []string{"limit=0", "limit=-1", "limit=abc", "limit=", "limit=1.5"} {
			wantEnvelope(t, e.get(base+"?"+q), 400, "bad_request")
		}
		wantEnvelope(t, e.get(base+"?after=!!!"), 400, "bad_request")
		wantEnvelope(t, e.get(base+"?after="), 400, "bad_request")
	}
	for i := range 3 {
		e.save(fmt.Sprintf("r%d", i), fmt.Sprintf("s%d", i), "cc", i+1)
	}
	if p := e.page("/api/sessions?limit=1000000"); len(p.Items) != 3 {
		t.Errorf("clamped page = %d items", len(p.Items))
	}
}

func TestAPI_AfterFromOtherListIs400(t *testing.T) {
	e := newAPIEnv(t)
	for i := range 3 {
		e.save(fmt.Sprintf("r%d", i), fmt.Sprintf("s%d", i), "cc", i+1)
		e.save(fmt.Sprintf("u%d", i), "", "cc", i+1)
	}
	sp := e.page("/api/sessions?limit=1")
	up := e.page("/api/ungrouped?limit=1")
	if sp.NextAfter == nil || up.NextAfter == nil {
		t.Fatal("no next_after")
	}
	wantEnvelope(t, e.get("/api/ungrouped?after="+*sp.NextAfter), 400, "bad_request")
	wantEnvelope(t, e.get("/api/sessions?after="+*up.NextAfter), 400, "bad_request")
	tampered := *sp.NextAfter + "x"
	wantEnvelope(t, e.get("/api/sessions?after="+tampered), 400, "bad_request")
	if rec := e.get("/api/sessions?after=" + *sp.NextAfter); rec.Code != 200 {
		t.Errorf("own after = %d", rec.Code)
	}
}

func TestAPI_PagingWhileSessionMovesNoDuplicates(t *testing.T) {
	e := newAPIEnv(t)
	for i := range 4 {
		e.save(fmt.Sprintf("r%d", i), fmt.Sprintf("s%d", i), "cc", i+1)
	}
	p := e.page("/api/sessions?limit=2")
	seen := keys(p) // s3 s2
	// s2 (already shown) and s0 (not yet shown) gain newer exchanges mid-walk.
	e.save("m1", "s2", "cc", 50)
	e.save("m2", "s0", "cc", 60)
	for p.NextAfter != nil {
		p = e.page("/api/sessions?limit=2&after=" + *p.NextAfter)
		seen = append(seen, keys(p)...)
	}
	count := map[string]int{}
	for _, k := range seen {
		count[k]++
		if count[k] > 1 {
			t.Fatalf("%s listed twice: %v", k, seen)
		}
	}
	if count["s1"] != 1 {
		t.Fatalf("s1 missing: %v", seen)
	}
}

func TestAPISessions_HooksOnlyLabel(t *testing.T) {
	e := newAPIEnv(t)
	e.save("a", "withex", "cc", 1)
	e.hook("withex", core.HarnessEvent{Phase: core.PhaseNotice}, 2)
	e.hook("hookonly", core.HarnessEvent{Phase: core.PhaseNotice}, 3)
	byKey := map[string]map[string]any{}
	for _, it := range e.page("/api/sessions").Items {
		byKey[it["session_key"].(string)] = it
	}
	if byKey["hookonly"] == nil || byKey["hookonly"]["hooks_only"] != true {
		t.Fatalf("hooks-only session = %v", byKey["hookonly"])
	}
	if byKey["withex"] == nil || byKey["withex"]["hooks_only"] != false {
		t.Fatalf("session with exchange = %v", byKey["withex"])
	}
	id := sessionID(store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "hookonly"})
	p := e.timeline("/api/sessions/" + id + "/timeline")
	if len(p.Entries) != 1 || p.Entries[0]["kind"] != "hook_event" {
		t.Fatalf("hooks-only timeline = %v", p.Entries)
	}
}

// cmdRules is testRules whose profile names a Bash tool call a command, so a session view
// lists it under Commands (spec 016 AC5).
type cmdRules struct{ testRules }

func (cmdRules) SummarizeTool(name string, input []byte, _ string) core.ToolSummary {
	if name == "Bash" {
		return core.ToolSummary{Kind: core.ToolCommand, Arg: string(input), Added: -1, Removed: -1}
	}
	return core.ToolSummary{Kind: core.ToolOther, Added: -1, Removed: -1}
}

// rowList is a sessions-list row's "list" object (spec 017 AC1, AC2).
func rowList(t *testing.T, s map[string]any) map[string]any {
	t.Helper()
	l, ok := s["list"].(map[string]any)
	if !ok {
		t.Fatalf("row has no list facts: %v", s)
	}
	return l
}

// 017 AC1: a row carries the session page's counts: prompts, model calls with side calls
// apart, commands, the wall-clock time and the folder, and its title follows 016's rule.
func TestSessions_RowCounts(t *testing.T) {
	e := newDigestEnv(t, map[string]ClientRules{"cc": cmdRules{}})
	e.hook("k", core.HarnessEvent{Phase: core.PhasePrompt, WorkDir: "/work/proj"}, 0)
	e.callWith("a", 1, false, []toolSpec{{"Bash", "npm test"}}, "user:fix the retry")
	e.callWith("q", 3, false, nil, "user:quota")
	e.callWith("b", 4, false, nil, "user:fix the retry", "assistant:Fixed.")
	e.callWith("c", 6, false, nil, "user:fix the retry", "assistant:Fixed.", "user:now the docs")
	s := e.summary()
	if s["title"] != "fix the retry" {
		t.Errorf("title = %v", s["title"])
	}
	l := rowList(t, s)
	want := map[string]any{"prompts": 2.0, "main_calls": 3.0, "side_calls": 1.0, "commands": 1.0,
		"clock_ms": 7000.0, "folder": "/work/proj", "needs_look": 0.0}
	for k, v := range want {
		if l[k] != v {
			t.Errorf("list.%s = %v, want %v", k, l[k], v)
		}
	}
	if kinds := l["look_kinds"].([]any); len(kinds) != 0 {
		t.Errorf("look kinds = %v, want none", kinds)
	}
}

// 017 AC1, E5: a hooks-only session has no model calls to count; its title is "Hooks only"
// and its folder, never its key.
func TestSessions_HooksOnlyRowHasNoCallCounts(t *testing.T) {
	e := newDigestEnv(t, map[string]ClientRules{"cc": cmdRules{}})
	e.hook("k", core.HarnessEvent{Phase: core.PhasePrompt, WorkDir: "/work/proj"}, 1)
	e.hook("k", core.HarnessEvent{Phase: core.PhasePreTool, ToolCallID: "h1", ToolName: "Bash", ToolInput: []byte("ls")}, 2)
	e.hook("k", core.HarnessEvent{Phase: core.PhasePostTool, ToolCallID: "h1", ToolName: "Bash"}, 3)
	s := e.summary()
	if s["hooks_only"] != true || s["model_calls"] != 0.0 {
		t.Fatalf("summary = %v", s)
	}
	if s["title"] != "Hooks only · /work/proj" {
		t.Errorf("title = %v", s["title"])
	}
	l := rowList(t, s)
	if l["main_calls"] != 0.0 || l["side_calls"] != 0.0 || l["commands"] != 1.0 {
		t.Errorf("list = %v", l)
	}
}

// 017 AC2: a row says how many items need a look and of what kinds, in words.
func TestSessions_NeedsLookKinds(t *testing.T) {
	e := newDigestEnv(t, map[string]ClientRules{"cc": cmdRules{}})
	e.callWith("a", 1, false, []toolSpec{{"Bash", "npm test"}, {"Bash", "npm run lint"}}, "user:fix the retry")
	// both tools ran, failed, and the next request reported their results
	e.save("r", "k", "cc", 3)
	evs := []core.StoredEvent{{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)}}
	for i, id := range []string{"t-a-0", "t-a-1"} {
		e.hook("k", core.HarnessEvent{Phase: core.PhasePreTool, ToolCallID: id, ToolName: "Bash"}, 2)
		e.hook("k", core.HarnessEvent{Phase: core.PhasePostToolFailed, ToolCallID: id, ToolName: "Bash"}, 2)
		evs = append(evs, core.StoredEvent{Seq: 1 + i, Kind: core.KindToolResult, ToolCallID: id, ContentHash: sha("res"),
			Payload: []byte(`{"is_error":true}`)})
	}
	if err := e.s.SaveParse(t.Context(), "r", core.PrincipalLocal, core.ParseOK, evs,
		[]core.Content{{Hash: sha("res"), Bytes: []byte(`"boom"`)}}); err != nil {
		t.Fatal(err)
	}
	e.callWith("b", 5, true, nil, "user:fix the retry", "assistant:x")
	l := rowList(t, e.summary())
	if l["needs_look"] != 3.0 {
		t.Errorf("needs_look = %v, want 3", l["needs_look"])
	}
	var got []string
	for _, k := range l["look_kinds"].([]any) {
		m := k.(map[string]any)
		got = append(got, fmt.Sprintf("%v %v %v", m["kind"], m["count"], m["words"]))
	}
	want := []string{"failed_tool 2 2 failed tool calls", "failed_call 1 1 failed model call"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("look kinds = %v, want %v", got, want)
	}
}

// 017 AC1: tokens no call reported are null, never 0, so the row shows "—".
func TestSessions_NoUsageIsNotZero(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 0, "user:hello")
	u := e.summary()["usage"].(map[string]any)
	for _, k := range []string{"input", "output", "cache_read"} {
		if v := u[k].(map[string]any)["value"]; v != nil {
			t.Errorf("usage.%s = %v, want null", k, v)
		}
	}
}
