package anthropic

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/cost"
)

// TestCostBook_TableParsesWithEveryModelSeen holds the embedded table to parse and to
// price every model the real store held when it was written (research Notes).
func TestCostBook_TableParsesWithEveryModelSeen(t *testing.T) {
	b := Adapter{}.CostBook()
	at := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	for _, m := range []string{"claude-opus-5-5", "claude-fable-5-2", "claude-sonnet-5-5", "claude-haiku-5-5"} {
		if _, ok := b.Table.Find(m, at, 0); !ok {
			t.Errorf("no price for %s", m)
		}
	}
	p, _ := b.Table.Find("claude-opus-5-5", at, 600_000)
	want := cost.Prices{Input: 4_000_000, Output: 20_000_000, CacheRead: 200_000, CacheWrite5m: 5_000_000, CacheWrite1h: 8_000_000}
	if p != want {
		t.Errorf("opus 5.5 at 600k prompt = %+v, want %+v (no long-context tier)", p, want)
	}
	if p, _ := b.Table.Find("claude-haiku-5-5", at, 100_001); p.Output != 2_500_000 {
		t.Errorf("haiku 5.5 over 100k: output %d, want 2.50/MTok", p.Output)
	}
}

// TestCostBook_RealUsageDetail prices a redacted copy of a real usage event (2 input,
// 317 output, 199 1h writes, 226,054 reads) from the store.
func TestCostBook_RealUsageDetail(t *testing.T) {
	detail := `{"cache_creation":{"ephemeral_1h_input_tokens":199,"ephemeral_5m_input_tokens":0},"inference_geo":"global",` +
		`"iterations":[{"type":"message"}],"output_tokens_details":{"thinking_tokens":124},"service_tier":"standard","speed":"standard"}`
	cost.Register(Name, Adapter{}.CostBook())
	n := func(v int64) *int64 { return &v }
	c := cost.Price(cost.Usage{Protocol: Name, Model: "claude-opus-5-5", At: time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC),
		Input: n(2), Output: n(317), CacheWrite: n(199), CacheRead: n(226_054), Detail: json.RawMessage(detail)})
	if c.Status != cost.StatusPriced || c.Estimated || cost.USD(c.Amount) != "0.0531508" {
		t.Fatalf("cost = %+v (%s), want priced exact 0.0531508", c, cost.USD(c.Amount))
	}
}

func TestCostFacts(t *testing.T) {
	for _, tc := range []struct {
		detail  string
		split   *cost.Split
		variant string
	}{
		{`{"cache_creation":{"ephemeral_5m_input_tokens":3,"ephemeral_1h_input_tokens":4}}`, &cost.Split{FiveMin: 3, OneHour: 4}, ""},
		{`{"cache_creation":{"ephemeral_5m_input_tokens":3}}`, nil, ""},
		{`{}`, nil, ""},
		{`not json`, nil, ""},
		{`{"speed":"fast"}`, nil, "fast"},
		{`{"speed":"standard","inference_geo":"us"}`, nil, "us"},
		{`{"speed":"standard","inference_geo":"global"}`, nil, ""},
	} {
		f := costFacts(json.RawMessage(tc.detail))
		if (f.Split == nil) != (tc.split == nil) || (f.Split != nil && *f.Split != *tc.split) || f.Variant != tc.variant {
			t.Errorf("%s: facts = %+v, want split %v variant %q", tc.detail, f, tc.split, tc.variant)
		}
	}
}
