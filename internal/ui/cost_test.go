package ui

import (
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/cost"
)

// The ui tests store exchanges under protocol "test" with model m1; this book prices m1 at
// $4 input and $20 output per million tokens, so a call of 10 input and 1 output costs
// 60 µ$, and one of 7 and 1 costs 48 µ$.
func init() {
	tb, err := cost.ParseTable(strings.NewReader(`model,from,prompt_over,input,output,cache_read,cache_write_5m,cache_write_1h
m1,,,4,20,0.20,5,8
`))
	if err != nil {
		panic(err)
	}
	cost.Register("test", cost.Book{Source: "Test", Table: tb})
}

func costOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	c, ok := m["cost"].(map[string]any)
	if !ok {
		t.Fatalf("no cost in %v", m)
	}
	return c
}

// spec 020 AC7, AC8: the Today line's cost counts every call since the time, side calls
// included.
func TestToday_Cost(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 100, "user:old")
	e.call("b", 10, 0, 3, "user:hello")
	e.call("c", 11, 0, 4, "user:"+titlePrompt, `assistant:{"title":"T"}`)
	c := costOf(t, e.today(sinceAt(5)))
	// (3 + 4) × $4/M + 2 × $20/M = 28 + 40 µ$
	if c["usd"] != "0.000068" || c["priced"] != 2.0 || c["source"] != "Test" {
		t.Fatalf("today cost = %v", c)
	}
}

// spec 020 AC8: a sessions-list row carries the session's cost, side calls included.
func TestSummary_Cost(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 0, 3, "user:hello")
	e.call("b", 2, 0, 4, "user:"+titlePrompt, `assistant:{"title":"T"}`)
	c := costOf(t, e.summary()["usage"].(map[string]any))
	if c["usd"] != "0.000068" || c["priced"] != 2.0 {
		t.Fatalf("session cost = %v", c)
	}
}

// spec 020 AC7, AC8: the session overview's cost includes side calls, and a side call's
// details show its own cost with the breakdown by token kind.
func TestView_CostOverviewAndSideCall(t *testing.T) {
	e, d := viewEnv(t)
	d.callWith("a", 1, false, nil, "user:fix the retry")
	d.callWith("q", 2, false, nil, "user:quota") // a side call under testRules
	code, m, body := getCall(t, e, sid(), "q")
	if code != 200 {
		t.Fatalf("call = %d %s", code, body)
	}
	if m["facts"].(map[string]any)["thread"].(map[string]any)["kind"] != "side" {
		t.Fatalf("q is not a side call: %s", body)
	}
	c := costOf(t, m["context"].(map[string]any))
	lines, _ := c["lines"].([]any)
	if c["status"] != "priced" || c["usd"] != "0.00006" || len(lines) != 5 {
		t.Fatalf("side call cost = %v", c)
	}
	in := lines[0].(map[string]any)
	if in["kind"] != "input" || in["tokens"] != 10.0 || in["usd_per_mtok"] != "4" || in["usd"] != "0.00004" {
		t.Errorf("input line = %v", in)
	}
	// callWith leaves the cache counters unset, so the cost is a lower bound (E1).
	if c["at_least"] != true {
		t.Errorf("unset cache counters not at least: %v", c)
	}

	code, vb := fetch(t, e, "/api/sessions/"+sid()+"/view")
	if code != 200 || !strings.Contains(string(vb), `"cost":{"usd":"0.00012"`) {
		t.Fatalf("view = %d, want the overview cost 0.00012 (both calls): %s", code, vb)
	}
}
