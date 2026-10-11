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
