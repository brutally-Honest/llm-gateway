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
	t *testing.T
	s *store.Store
	h http.Handler
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
	e := &apiEnv{t: t, s: s}
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
	for _, p := range []string{"/api/sessions", "/api/ungrouped"} {
		rec := e.get(p)
		if rec.Code != 200 || rec.Body.String() != `{"items":[],"next_after":null}`+"\n" {
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
