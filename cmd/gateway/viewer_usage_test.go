package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

type call struct {
	id, client, key, model string
	at                     int
	in, out                int
}

// seedCalls writes one model call per entry: a request event and a usage event.
func seedCalls(t *testing.T, cs ...call) string {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		start := viewerBase.Add(time.Duration(c.at) * time.Second)
		ex := &core.Exchange{RequestID: c.id, PrincipalID: core.PrincipalLocal, Protocol: "test",
			Client: orDefault(c.client, "cc"), Auth: core.AuthAPIKey, Method: http.MethodPost,
			Path: "/v1/m", Status: 200, Start: start, End: start.Add(time.Second), SessionKey: c.key}
		if err := s.SaveExchange(t.Context(), ex); err != nil {
			t.Fatal(err)
		}
		req, _ := json.Marshal(map[string]any{"model": c.model})
		use, _ := json.Marshal(map[string]any{"input_tokens": c.in, "output_tokens": c.out,
			"cache_write_tokens": 0, "cache_read_tokens": 0})
		evs := []core.StoredEvent{{Seq: 0, Kind: "request", Payload: req}, {Seq: 1, Kind: "usage", Payload: use}}
		if err := s.SaveParse(t.Context(), c.id, ex.PrincipalID, core.ParseStatus("ok"), evs, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTimelineCmd_PrintsUsageTotals(t *testing.T) {
	dir := seedCalls(t,
		call{id: "a", key: "k", model: "m1", at: 1, in: 10, out: 20},
		call{id: "b", key: "k", model: "m2", at: 2, in: 5, out: 1},
		call{id: "c", key: "k", model: "m1", at: 3, in: 1, out: 2},
	)
	code, out := viewerRun(t, dir, "timeline", "k")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	ls := lines(out)
	tail := ls[len(ls)-3:]
	want := []string{
		"== usage total: input 16, output 23, cache-write 0, cache-read 0",
		"   m1: input 11, output 22, cache-write 0, cache-read 0",
		"   m2: input 5, output 1, cache-write 0, cache-read 0",
	}
	for i := range want {
		if tail[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, tail[i], want[i])
		}
	}

	_, js := viewerRun(t, dir, "timeline", "-json", "k")
	jl := lines(js)
	var m map[string]any
	if err := json.Unmarshal([]byte(jl[len(jl)-1]), &m); err != nil || m["type"] != "usage_total" {
		t.Fatalf("last line %q: %v", jl[len(jl)-1], err)
	}
	if m["usage"].(map[string]any)["input_tokens"] != float64(16) || len(m["models"].([]any)) != 2 {
		t.Errorf("usage_total = %v", m)
	}
}

func TestTimelineCmd_TwoSessionsSameKeyEachHasTotals(t *testing.T) {
	dir := seedCalls(t,
		call{id: "a", key: "k", client: "cc", model: "m1", at: 1, in: 10, out: 20},
		call{id: "b", key: "k", client: "other", model: "m1", at: 2, in: 3, out: 4},
	)
	code, out := viewerRun(t, dir, "timeline", "k")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if strings.Count(out, "== usage total:") != 2 ||
		!strings.Contains(out, "== usage total: input 10, output 20,") ||
		!strings.Contains(out, "== usage total: input 3, output 4,") {
		t.Errorf("want one total per session:\n%s", out)
	}
	if strings.Index(out, "== usage total: input 10") > strings.Index(out, "### session") &&
		strings.Count(out, "### session") != 2 {
		t.Errorf("headings:\n%s", out)
	}
}

func TestSessionsCmd_RowTotalsAndModelLines(t *testing.T) {
	dir := seedCalls(t,
		call{id: "a", key: "k", model: "m1", at: 1, in: 10, out: 20},
		call{id: "b", key: "k", model: "m2", at: 2, in: 5, out: 1},
		call{id: "c", key: "s", model: "m1", at: 3, in: 1, out: 2},
	)
	code, out := viewerRun(t, dir, "sessions")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	ls := lines(out)
	if len(ls) != 4 {
		t.Fatalf("lines = %q", ls)
	}
	// newest first: s has one model (no model lines), then k with two.
	if !strings.HasSuffix(ls[0], "\tinput 1, output 2, cache-write 0, cache-read 0") ||
		!strings.HasSuffix(ls[1], "\tinput 15, output 21, cache-write 0, cache-read 0") ||
		ls[2] != "   m1: input 10, output 20, cache-write 0, cache-read 0" ||
		ls[3] != "   m2: input 5, output 1, cache-write 0, cache-read 0" {
		t.Errorf("out:\n%s", out)
	}
}

func TestListSessions_UsageDoesNotWrite(t *testing.T) {
	dir := seedCalls(t, call{id: "a", key: "k", model: "m1", at: 1, in: 1, out: 1})
	before := storeFiles(t, dir)
	for _, args := range [][]string{{"sessions"}, {"sessions", "-json"}} {
		if code, out := viewerRun(t, dir, args...); code != exitOK {
			t.Fatalf("%v: exit %d\n%s", args, code, out)
		}
	}
	assertUnchanged(t, before, storeFiles(t, dir))
}

func TestSessionsCmd_UngroupedRowsAndTotals(t *testing.T) {
	dir := seedCalls(t,
		call{id: "u1", model: "m1", at: 1, in: 10, out: 20},
		call{id: "u2", model: "m2", at: 2, in: 5, out: 1},
	)
	code, out := viewerRun(t, dir, "sessions", "-ungrouped")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	ls := lines(out)
	if len(ls) != 6 {
		t.Fatalf("lines = %q", ls)
	}
	if !strings.HasSuffix(ls[0], "\t200 tokens in=5 out=1 cache_write=0 cache_read=0") ||
		!strings.HasSuffix(ls[1], "\t200 tokens in=10 out=20 cache_write=0 cache_read=0") ||
		ls[2] != "total over all 2 ungrouped exchanges" ||
		ls[3] != "== usage total: input 15, output 21, cache-write 0, cache-read 0" ||
		ls[4] != "   m1: input 10, output 20, cache-write 0, cache-read 0" ||
		ls[5] != "   m2: input 5, output 1, cache-write 0, cache-read 0" {
		t.Errorf("out:\n%s", out)
	}

	_, js := viewerRun(t, dir, "sessions", "-ungrouped", "-json")
	jl := lines(js)
	var row, tot map[string]any
	if err := json.Unmarshal([]byte(jl[0]), &row); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(jl[len(jl)-1]), &tot); err != nil {
		t.Fatal(err)
	}
	if row["usage"].(map[string]any)["input_tokens"] != float64(5) ||
		tot["type"] != "ungrouped_usage" || tot["exchanges"] != float64(2) ||
		tot["usage"].(map[string]any)["input_tokens"] != float64(15) || len(tot["models"].([]any)) != 2 {
		t.Errorf("row %v total %v", row, tot)
	}
}

func TestSessionsCmd_UngroupedSummaryLineCarriesTotals(t *testing.T) {
	dir := seedCalls(t,
		call{id: "g", key: "k", model: "m1", at: 9, in: 100, out: 100},
		call{id: "u1", model: "m1", at: 1, in: 10, out: 20},
		call{id: "u2", model: "m1", at: 2, in: 5, out: 1},
	)
	_, out := viewerRun(t, dir, "sessions")
	ls := lines(out)
	last := ls[len(ls)-1]
	if !strings.HasPrefix(last, "ungrouped\t") ||
		!strings.HasSuffix(last, "\tinput 15, output 21, cache-write 0, cache-read 0") {
		t.Errorf("last line = %q", last)
	}
	_, js := viewerRun(t, dir, "sessions", "-json")
	jl := lines(js)
	var m map[string]any
	if err := json.Unmarshal([]byte(jl[len(jl)-1]), &m); err != nil || m["type"] != "ungrouped" ||
		m["usage"].(map[string]any)["output_tokens"] != float64(21) {
		t.Errorf("json %q: %v", jl[len(jl)-1], err)
	}
}

func TestSessionsCmd_UngroupedNoModelCalls(t *testing.T) {
	dir := seedViewer(t, vx{id: "u1", at: 1})
	_, out := viewerRun(t, dir, "sessions", "-ungrouped")
	if !strings.Contains(out, "== usage total: no model calls") || strings.Contains(out, "input 0") {
		t.Errorf("out:\n%s", out)
	}
	_, js := viewerRun(t, dir, "sessions", "-ungrouped", "-json")
	jl := lines(js)
	var m map[string]any
	if err := json.Unmarshal([]byte(jl[len(jl)-1]), &m); err != nil || m["usage"] != nil || m["calls"] != float64(0) {
		t.Errorf("json %q: %v", jl[len(jl)-1], err)
	}
}

func TestSessionsCmd_UngroupedNonModelExchangeIgnored(t *testing.T) {
	dir := seedCalls(t, call{id: "u1", model: "m1", at: 1, in: 10, out: 20})
	// a second ungrouped exchange with no events (like HEAD /api/hello)
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	start := viewerBase.Add(5 * time.Second)
	ex := &core.Exchange{RequestID: "h", PrincipalID: core.PrincipalLocal, Protocol: "test", Client: "cc",
		Auth: core.AuthAPIKey, Method: http.MethodHead, Path: "/api/hello", Status: 200, Start: start, End: start.Add(time.Second)}
	if err := s.SaveExchange(t.Context(), ex); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, out := viewerRun(t, dir, "sessions", "-ungrouped")
	ls := lines(out)
	if !strings.HasSuffix(ls[0], "\tHEAD\t/api/hello\t200") {
		t.Errorf("non-model row = %q", ls[0])
	}
	if !strings.Contains(out, "total over all 2 ungrouped exchanges") ||
		!strings.Contains(out, "== usage total: input 10, output 20, cache-write 0, cache-read 0\n") {
		t.Errorf("out:\n%s", out)
	}
}

func TestSessionsCmd_UngroupedTotalScopeWithLimit(t *testing.T) {
	dir := seedCalls(t,
		call{id: "u1", model: "m1", at: 1, in: 10, out: 20},
		call{id: "u2", model: "m1", at: 2, in: 5, out: 1},
		call{id: "u3", model: "m1", at: 3, in: 1, out: 1},
	)
	_, out := viewerRun(t, dir, "sessions", "-ungrouped", "-n", "1")
	ls := lines(out)
	if len(ls) != 4 || !strings.HasPrefix(ls[0], "u3\t") ||
		ls[1] != "total over all 3 ungrouped exchanges" ||
		ls[2] != "== usage total: input 16, output 22, cache-write 0, cache-read 0" {
		t.Errorf("out:\n%s", out)
	}
	_, js := viewerRun(t, dir, "sessions", "-ungrouped", "-n", "1", "-json")
	jl := lines(js)
	var m map[string]any
	if err := json.Unmarshal([]byte(jl[len(jl)-1]), &m); err != nil || m["exchanges"] != float64(3) {
		t.Errorf("json %q: %v", jl[len(jl)-1], err)
	}
}
