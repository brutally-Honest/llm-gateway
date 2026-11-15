package timeline_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/cost"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// costProtocol is the protocol the cost tests' exchanges name; its book prices m1 at $4
// input until 2026-06-01 and $2 from it, and reads {"w5":n,"w1":n} as the split.
const costProtocol = "timeline-cost-test"

var periodChange = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func init() {
	tb, err := cost.ParseTable(strings.NewReader(`model,from,prompt_over,input,output,cache_read,cache_write_5m,cache_write_1h
m1,,,4,20,0.20,5,8
m1,2026-06-01,,2,10,0.10,2.50,4
`))
	if err != nil {
		panic(err)
	}
	cost.Register(costProtocol, cost.Book{Source: "Test", Table: tb, Facts: func(d json.RawMessage) cost.Facts {
		var s struct{ W5, W1 *int64 }
		if json.Unmarshal(d, &s) != nil || s.W5 == nil || s.W1 == nil {
			return cost.Facts{}
		}
		return cost.Facts{Split: &cost.Split{FiveMin: *s.W5, OneHour: *s.W1}}
	}})
}

// pricedCall adds a model call that started at at, with a usage event and detail.
func pricedCall(f *fake, id, model string, at time.Time, in, out, cw, cr int64, detail string) {
	ex := f.exchange(id, at.UnixNano(), "ok")
	ex.Protocol, ex.TTFBNS = costProtocol, ttfb()
	f.request(id, model)
	payload, _ := json.Marshal(map[string]any{"input_tokens": in, "output_tokens": out,
		"cache_write_tokens": cw, "cache_read_tokens": cr, "detail": json.RawMessage(detail)})
	f.add(id, store.EventRow{Kind: "usage", Payload: payload})
}

func sessionCost(t *testing.T, f *fake) cost.Total {
	t.Helper()
	st, err := timeline.SessionStats(f, store.SessionRow{})
	if err != nil {
		t.Fatal(err)
	}
	return st.Usage.Total.Cost
}

func TestSessionCost_PricePeriodChangesMidSession(t *testing.T) {
	f := newFake()
	pricedCall(f, "A", "m1", periodChange.Add(-time.Minute), 1_000_000, 0, 0, 0, "null")
	pricedCall(f, "B", "m1", periodChange.Add(time.Minute), 1_000_000, 0, 0, 0, "null")
	tot := sessionCost(t, f)
	if got := cost.USD(tot.Amount); got != "6" || tot.Partial() || tot.Priced != 2 {
		t.Fatalf("session = %s partial %v priced %d; want 6 (4 + 2), whole", got, tot.Partial(), tot.Priced)
	}
}

func TestSessionCost_SplitAndNoSplit(t *testing.T) {
	f := newFake()
	at := periodChange.Add(-time.Hour)
	pricedCall(f, "A", "m1", at, 0, 0, 1_000_000, 0, `{"w5":0,"w1":1000000}`) // $8, exact
	pricedCall(f, "B", "m1", at, 0, 0, 1_000_000, 0, "null")                  // $5, estimated
	tot := sessionCost(t, f)
	if got := cost.USD(tot.Amount); got != "13" || tot.Estimated != 1 || tot.Partial() {
		t.Fatalf("session = %s estimated %d partial %v; want 13, 1, whole", got, tot.Estimated, tot.Partial())
	}
}

func TestSessionCost_UnknownModelMakesTotalPartial(t *testing.T) {
	f := newFake()
	at := periodChange.Add(-time.Hour)
	pricedCall(f, "A", "m1", at, 1_000_000, 0, 0, 0, "null")
	pricedCall(f, "B", "nobody-1", at, 1_000_000, 0, 0, 0, "null")
	tot := sessionCost(t, f)
	if got := cost.USD(tot.Amount); got != "4" || !tot.Partial() || tot.NoPrice != 1 || tot.NoPriceModels[0] != "nobody-1" {
		t.Fatalf("session = %+v; want 4, partial, nobody-1 left out", tot)
	}
	c, _ := timeline.CallUsageOf(f.exchanges[1], f.events["B"])
	if j := c.Cost.JSON(); j.Status != cost.StatusNoPrice || j.Reason != "no price for nobody-1" || j.USD != "" {
		t.Errorf("unknown model's call cost = %+v", j)
	}
}

func TestSessionCost_CallsWithNoUsage(t *testing.T) {
	f := newFake()
	at := periodChange.Add(-time.Hour)
	pricedCall(f, "A", "m1", at, 1_000_000, 0, 0, 0, "null")
	ex := f.exchange("E", at.UnixNano(), "ok") // an error response: nothing billed
	ex.Protocol, ex.Status, ex.TTFBNS = costProtocol, 429, ttfb()
	f.request("E", "m1")
	tot := sessionCost(t, f)
	if tot.Partial() || tot.Errored != 1 || cost.USD(tot.Amount) != "4" {
		t.Fatalf("with an errored call: %+v; want whole 4 (E3)", tot)
	}
	c, _ := timeline.CallUsageOf(f.exchanges[1], f.events["E"])
	if j := c.Cost.JSON(); j.Status != cost.StatusErrored || j.USD != "" || j.Reason == "" {
		t.Errorf("errored call cost = %+v", j)
	}

	cut := f.exchange("C", at.UnixNano(), "ok") // cut before any response: may have used tokens
	cut.Protocol = costProtocol
	f.request("C", "m1")
	tot = sessionCost(t, f)
	if !tot.Partial() || tot.NoUsage != 1 {
		t.Fatalf("with a cut call: %+v; want partial (E3)", tot)
	}
}
