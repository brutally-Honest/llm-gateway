package timeline_test

import (
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

func ttfb() *int64 { return n(1000) }

// call builds one exchange with the given status/ttfb and returns its call usage.
func call(t *testing.T, f *fake, status int, hasTTFB bool) (timeline.CallUsage, bool) {
	t.Helper()
	ex := f.exchanges[0]
	ex.Status = status
	if hasTTFB {
		ex.TTFBNS = ttfb()
	}
	return timeline.CallUsageOf(ex, f.events[ex.RequestID])
}

func one(model string, mk func(f *fake, id string)) timeline.CallUsage {
	f := newFake()
	f.exchange("A", 1, "ok")
	f.request("A", model)
	mk(f, "A")
	c, _ := timeline.CallUsageOf(f.exchanges[0], f.events["A"])
	return c
}

func withUsage(model string, partial bool, in, out, cw, cr *int64) timeline.CallUsage {
	return one(model, func(f *fake, id string) { f.usage(id, partial, in, out, cw, cr) })
}

func noUsage(model string, errored bool) timeline.CallUsage {
	f := newFake()
	ex := f.exchange("A", 1, "ok")
	f.request("A", model)
	if errored {
		ex.Status, ex.TTFBNS = 429, ttfb()
	}
	c, _ := timeline.CallUsageOf(*ex, f.events["A"])
	return c
}

func val(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func zero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func TestCallUsage_PartialFromEventFlag(t *testing.T) {
	for _, partial := range []bool{true, false} {
		c := withUsage("m", partial, n(1), n(2), n(3), n(4))
		if c.Partial != partial || !c.HasUsage || c.NoUsage != "" {
			t.Errorf("partial=%v: got %+v", partial, c)
		}
	}
}

func TestCallUsage_NoResponseFallbackStatusIsMissing(t *testing.T) {
	for _, status := range []int{0, 499, 502} {
		f := newFake()
		f.exchange("A", 1, "ok")
		f.request("A", "m")
		c, ok := call(t, f, status, false)
		if !ok || c.NoUsage != timeline.NoUsageMissing {
			t.Errorf("status %d without ttfb: %+v", status, c)
		}
	}
}

func TestCallUsage_NilCounterIsUnknownNotZero(t *testing.T) {
	c := withUsage("m", false, n(5), nil, n(0), nil)
	if val(c.Input) != 5 || c.Output != nil || c.CacheWrite == nil || *c.CacheWrite != 0 || c.CacheRead != nil {
		t.Errorf("counters = %+v", c.Counters)
	}
}

func TestCallUsage_NoUsageErroredVsMissing(t *testing.T) {
	if c := noUsage("m", true); c.NoUsage != timeline.NoUsageErrored || c.HasUsage {
		t.Errorf("429 with ttfb: %+v", c)
	}
	f := newFake()
	f.exchange("A", 1, "ok")
	f.request("A", "m")
	if c, _ := call(t, f, 200, true); c.NoUsage != timeline.NoUsageMissing {
		t.Errorf("200: %+v", c)
	}
}

func TestCallUsage_EmptyModelIsUnknown(t *testing.T) {
	if c := withUsage("", false, n(1), n(1), n(1), n(1)); c.Model != timeline.UnknownModel {
		t.Errorf("model = %q", c.Model)
	}
}

func TestCallUsage_FailedParse2xxIsMissing(t *testing.T) {
	f := newFake()
	f.exchange("A", 1, "failed")
	f.request("A", "m")
	c, ok := call(t, f, 200, true)
	if !ok || c.NoUsage != timeline.NoUsageMissing {
		t.Errorf("got %+v", c)
	}
}

func TestCallUsage_NonModelExchangeIsNotACall(t *testing.T) {
	f := newFake()
	f.exchange("A", 1, "skipped")
	if _, ok := call(t, f, 200, true); ok {
		t.Error("exchange without a request event is a call")
	}
}

func TestSummarize_TotalsAndModelOrder(t *testing.T) {
	s := timeline.Summarize([]timeline.CallUsage{
		withUsage("zeta", false, n(1), n(2), n(3), n(4)),
		withUsage("alpha", false, n(10), n(20), n(30), n(40)),
		withUsage("alpha", false, n(1), n(1), n(1), n(1)),
	})
	if s.Calls != 3 || val(s.Total.Input.Value) != 12 || val(s.Total.Output.Value) != 23 ||
		val(s.Total.CacheWrite.Value) != 34 || val(s.Total.CacheRead.Value) != 45 {
		t.Errorf("total = %+v", s.Total)
	}
	if len(s.Models) != 2 || s.Models[0].Model != "alpha" || s.Models[1].Model != "zeta" ||
		val(s.Models[0].Input.Value) != 11 {
		t.Errorf("models = %+v", s.Models)
	}
}

func TestSummarize_MissingMakesIncompleteErroredDoesNot(t *testing.T) {
	s := timeline.Summarize([]timeline.CallUsage{
		withUsage("m", false, n(1), n(1), n(1), n(1)), noUsage("m", true)})
	if s.Total.Incomplete() || s.Total.Errored != 1 || s.Total.Missing != 0 {
		t.Errorf("errored: %+v", s.Total)
	}
	s = timeline.Summarize([]timeline.CallUsage{
		withUsage("m", false, n(1), n(1), n(1), n(1)), noUsage("m", false)})
	if !s.Total.Incomplete() || s.Total.Missing != 1 || s.Total.Reporting != 1 {
		t.Errorf("missing: %+v", s.Total)
	}
}

func TestSummarize_NilCounterIncomplete(t *testing.T) {
	s := timeline.Summarize([]timeline.CallUsage{
		withUsage("m", false, n(1), nil, n(1), n(1)),
		withUsage("m", false, n(2), n(7), n(1), n(1))})
	g := s.Total
	if !g.Incomplete() || g.Output.Unreported != 1 || val(g.Output.Value) != 7 || g.Input.Unreported != 0 || g.Missing != 0 {
		t.Errorf("got %+v", g)
	}
}

func TestSummarize_NoReporterIsUnknown(t *testing.T) {
	s := timeline.Summarize([]timeline.CallUsage{withUsage("m", false, n(1), nil, n(1), n(1))})
	if s.Total.Output.Value != nil || s.Total.Output.Unreported != 1 {
		t.Errorf("got %+v", s.Total.Output)
	}
}

func TestSummarize_BothIncompleteCounts(t *testing.T) {
	s := timeline.Summarize([]timeline.CallUsage{
		withUsage("m", false, n(1), nil, n(1), n(1)), noUsage("m", false)})
	if s.Total.Missing != 1 || s.Total.Output.Unreported != 1 || s.Total.Reporting != 1 {
		t.Errorf("got %+v", s.Total)
	}
}

func TestSummarize_PartialAddedAndCounted(t *testing.T) {
	s := timeline.Summarize([]timeline.CallUsage{
		withUsage("m", true, n(1), n(1), n(1), n(1)),
		withUsage("m", false, n(2), n(2), n(2), n(2))})
	if s.Total.PartialCalls != 1 || val(s.Total.Input.Value) != 3 {
		t.Errorf("got %+v", s.Total)
	}
}

func TestSummarize_UnknownModelLast(t *testing.T) {
	s := timeline.Summarize([]timeline.CallUsage{
		withUsage("", false, n(1), n(1), n(1), n(1)),
		withUsage("zz", false, n(1), n(1), n(1), n(1)),
		withUsage("aa", false, n(1), n(1), n(1), n(1))})
	got := []string{s.Models[0].Model, s.Models[1].Model, s.Models[2].Model}
	if !equal(got, []string{"aa", "zz", "unknown"}) {
		t.Errorf("order = %v", got)
	}
}

func TestSummarize_NonModelExchangeIgnored(t *testing.T) {
	f := newFake()
	f.exchange("H", 1, "skipped")
	c, ok := timeline.CallUsageOf(f.exchanges[0], f.events["H"])
	if ok {
		t.Fatalf("call = %+v", c)
	}
	s := timeline.Summarize(nil)
	if s.Calls != 0 || s.Total.Missing != 0 || len(s.Models) != 0 {
		t.Errorf("got %+v", s)
	}
}

func TestSummarize_TotalEqualsSumOfModels(t *testing.T) {
	s := timeline.Summarize([]timeline.CallUsage{
		withUsage("a", false, n(1), n(2), n(3), n(4)),
		withUsage("b", true, n(5), nil, n(7), n(8)),
		noUsage("b", false), noUsage("c", true)})
	var in, out, cw, cr int64
	var calls, miss, errd, part int
	for _, m := range s.Models {
		in += zero(m.Input.Value)
		out += zero(m.Output.Value)
		cw += zero(m.CacheWrite.Value)
		cr += zero(m.CacheRead.Value)
		calls += m.Calls
		miss += m.Missing
		errd += m.Errored
		part += m.PartialCalls
	}
	g := s.Total
	if in != zero(g.Input.Value) || out != zero(g.Output.Value) || cw != zero(g.CacheWrite.Value) ||
		cr != zero(g.CacheRead.Value) || calls != g.Calls || miss != g.Missing || errd != g.Errored || part != g.PartialCalls {
		t.Errorf("total %+v != models %+v", g, s.Models)
	}
}

func TestSummarize_AllMissingUnknownNofN(t *testing.T) {
	s := timeline.Summarize([]timeline.CallUsage{noUsage("m", false), noUsage("m", false)})
	g := s.Total
	if g.Input.Value != nil || g.Output.Value != nil || g.CacheWrite.Value != nil || g.CacheRead.Value != nil ||
		!g.Incomplete() || g.Missing != 2 || g.Reporting != 0 || g.AllErrored() {
		t.Errorf("got %+v", g)
	}
}

func TestSummarize_AllErroredNoUsage(t *testing.T) {
	s := timeline.Summarize([]timeline.CallUsage{noUsage("m", true)})
	for _, g := range []timeline.Group{s.Total, s.Models[0].Group} {
		if !g.AllErrored() || g.Incomplete() || g.Errored != 1 || g.Input.Value != nil {
			t.Errorf("got %+v", g)
		}
	}
}
