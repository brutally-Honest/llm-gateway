package cost

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testTable has a model whose price changes on 2026-06-01 and a tiered one.
const testTable = `# test prices
model,from,prompt_over,input,output,cache_read,cache_write_5m,cache_write_1h
m1,,,4,20,0.20,5,8
m1,2026-06-01,,2,10,0.10,2.50,4
tiered,,,0.10,0.50,0.01,0.125,0.20
tiered,,100000,0.50,2.50,0.05,0.625,1
`

func p(n int64) *int64 { return &n }

// testFacts reads {"w5":n,"w1":n,"variant":"…"} as the split and the variant.
func testFacts(detail json.RawMessage) Facts {
	var d struct {
		W5, W1  *int64
		Variant string
	}
	_ = json.Unmarshal(detail, &d)
	f := Facts{Variant: d.Variant}
	if d.W5 != nil && d.W1 != nil {
		f.Split = &Split{FiveMin: *d.W5, OneHour: *d.W1}
	}
	return f
}

// register sets a book for a protocol named after the test and returns that name.
func register(t *testing.T) string {
	t.Helper()
	tb, err := ParseTable(strings.NewReader(testTable))
	if err != nil {
		t.Fatal(err)
	}
	Register(t.Name(), Book{Source: "Test", Table: tb, Facts: testFacts})
	return t.Name()
}

var before = time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC)
var after = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func usage(proto string, in, out, write, read int64, detail string) Usage {
	u := Usage{Protocol: proto, Model: "m1", At: before, Input: p(in), Output: p(out), CacheWrite: p(write), CacheRead: p(read)}
	if detail != "" {
		u.Detail = json.RawMessage(detail)
	}
	return u
}

func line(t *testing.T, c Call, kind string) Line {
	t.Helper()
	for _, l := range c.Lines {
		if l.Kind == kind {
			return l
		}
	}
	t.Fatalf("no %s line in %+v", kind, c)
	return Line{}
}

func TestPrice_EachTokenKind(t *testing.T) {
	proto := register(t)
	split := `{"w5":0,"w1":0}`
	for _, tc := range []struct {
		name                 string
		in, out, write, read int64
		detail               string
		kind                 string
		wantUSD              string
	}{
		{"input", 1_000_000, 0, 0, 0, split, KindInput, "4"},
		{"output", 0, 1_000_000, 0, 0, split, KindOutput, "20"},
		{"cache read", 0, 0, 0, 1_000_000, split, KindCacheRead, "0.2"},
		{"cache write 5m", 0, 0, 1_000_000, 0, `{"w5":1000000,"w1":0}`, KindCacheWrite5m, "5"},
		{"cache write 1h", 0, 0, 1_000_000, 0, `{"w5":0,"w1":1000000}`, KindCacheWrite1h, "8"},
		{"one input token", 1, 0, 0, 0, split, KindInput, "0.000004"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Price(usage(proto, tc.in, tc.out, tc.write, tc.read, tc.detail))
			if c.Status != StatusPriced || c.Estimated || c.AtLeast {
				t.Fatalf("call = %+v, want priced, exact", c)
			}
			if got := USD(c.Amount); got != tc.wantUSD {
				t.Errorf("amount = %s, want %s", got, tc.wantUSD)
			}
			if got := USD(line(t, c, tc.kind).Amount); got != tc.wantUSD {
				t.Errorf("%s line = %s, want %s", tc.kind, got, tc.wantUSD)
			}
			if c.Source != "Test" {
				t.Errorf("source = %q", c.Source)
			}
		})
	}
}

func TestPrice_AllKindsAddUp(t *testing.T) {
	proto := register(t)
	// 2 input, 317 output, 199 1h writes, 226,054 reads (a real call's shape):
	// 2×4 + 317×20 + 199×8 + 226054×0.2 = 8 + 6340 + 1592 + 45210.8 µ$ = 0.0531508 $
	c := Price(usage(proto, 2, 317, 199, 226_054, `{"w5":0,"w1":199}`))
	if got := USD(c.Amount); got != "0.0531508" {
		t.Fatalf("amount = %s, want 0.0531508", got)
	}
	var sum Pico
	for _, l := range c.Lines {
		if l.Amount != Pico(l.Tokens)*Pico(l.Rate) {
			t.Errorf("%s: %d × %d ≠ %d", l.Kind, l.Tokens, l.Rate, l.Amount)
		}
		sum += l.Amount
	}
	if sum != c.Amount {
		t.Errorf("lines sum to %d, amount %d", sum, c.Amount)
	}
}

func TestPrice_NoSplitPricesWritesAt5mEstimated(t *testing.T) {
	proto := register(t)
	for _, tc := range []struct{ name, detail string }{
		{"no detail", ""},
		{"detail without split", `{}`},
		{"split that does not add up (E2)", `{"w5":10,"w1":10}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Price(usage(proto, 0, 0, 1_000_000, 0, tc.detail))
			if !c.Estimated {
				t.Errorf("not estimated: %+v", c)
			}
			if got := USD(c.Amount); got != "5" {
				t.Errorf("amount = %s, want 5 (all at the 5m rate)", got)
			}
			if l := line(t, c, KindCacheWrite1h); l.Tokens != 0 {
				t.Errorf("1h line has %d tokens", l.Tokens)
			}
		})
	}
	if c := Price(usage(proto, 5, 5, 0, 5, "")); c.Estimated {
		t.Errorf("a call with no cache writes is estimated: %+v", c)
	}
}

func TestPrice_SplitIsUsed(t *testing.T) {
	proto := register(t)
	c := Price(usage(proto, 0, 0, 1_000_000, 0, `{"w5":250000,"w1":750000}`))
	// 0.25 × 5 + 0.75 × 8 = 7.25
	if got := USD(c.Amount); got != "7.25" || c.Estimated {
		t.Fatalf("amount = %s estimated %v, want 7.25 exact", got, c.Estimated)
	}
}

func TestPrice_PricePeriodChangesMidSession(t *testing.T) {
	proto := register(t)
	u1 := usage(proto, 1_000_000, 0, 0, 0, "")
	u2 := u1
	u2.At = after
	var tot Total
	tot.Add(Price(u1))
	tot.Add(Price(u2))
	// $4 before 2026-06-01, $2 from it.
	if got := USD(tot.Amount); got != "6" || tot.Partial() || tot.Priced != 2 {
		t.Fatalf("total = %s partial %v priced %d, want 6, whole, 2", got, tot.Partial(), tot.Priced)
	}
}

func TestPrice_UnknownModelMakesTotalPartial(t *testing.T) {
	proto := register(t)
	known := usage(proto, 1_000_000, 0, 0, 0, "")
	unknown := known
	unknown.Model = "nobody-1"
	c := Price(unknown)
	if c.Status != StatusNoPrice || c.Reason != "no price for nobody-1" {
		t.Fatalf("call = %+v", c)
	}
	var tot Total
	tot.Add(Price(known))
	tot.Add(c)
	tot.Add(c)
	if !tot.Partial() || tot.NoPrice != 2 || USD(tot.Amount) != "4" {
		t.Fatalf("total = %+v, want partial, 2 left out, 4", tot)
	}
	j := tot.JSON()
	if len(j.NoPriceModels) != 1 || j.NoPriceModels[0] != "nobody-1" || !j.Partial {
		t.Errorf("json = %+v", j)
	}
}

func TestPrice_NoRowsBeforeFirstPeriodOrNoBook(t *testing.T) {
	u := usage("no-such-protocol", 1, 1, 1, 1, "")
	if c := Price(u); c.Status != StatusNoPrice {
		t.Errorf("unknown protocol: %+v", c)
	}
	tb, _ := ParseTable(strings.NewReader("model,from,prompt_over,input,output,cache_read,cache_write_5m,cache_write_1h\nlate,2026-06-01,,1,1,1,1,1\n"))
	Register(t.Name()+"late", Book{Table: tb})
	u = usage(t.Name()+"late", 1, 0, 0, 0, "")
	u.Model = "late"
	if c := Price(u); c.Status != StatusNoPrice { // E4
		t.Errorf("before the first period: %+v", c)
	}
}

func TestPrice_VariantIsNotPriced(t *testing.T) {
	proto := register(t)
	c := Price(usage(proto, 1, 1, 0, 0, `{"variant":"fast"}`))
	if c.Status != StatusNoPrice || c.Reason != "no price for m1 (fast)" {
		t.Fatalf("call = %+v", c)
	}
}

func TestPrice_NoUsage(t *testing.T) {
	cut := NoUsage(false, "the call was cut before usage was reported")
	errd := NoUsage(true, "the call failed")
	if cut.JSON().USD != "" || errd.JSON().USD != "" {
		t.Errorf("a call with no usage has an amount")
	}
	var tot Total
	tot.Add(errd)
	if tot.Partial() || tot.Errored != 1 {
		t.Errorf("an error response makes the total partial: %+v (E3)", tot)
	}
	tot.Add(cut)
	if !tot.Partial() || tot.NoUsage != 1 {
		t.Errorf("a cut call does not make the total partial: %+v (E3)", tot)
	}
}

func TestPrice_LowCountsAreALowerBound(t *testing.T) {
	proto := register(t)
	u := usage(proto, 1, 1, 0, 0, "")
	u.Partial = true
	if c := Price(u); !c.AtLeast {
		t.Errorf("partial usage not at least: %+v", c)
	}
	u = usage(proto, 1, 1, 0, 0, "")
	u.CacheRead = nil
	c := Price(u)
	if !c.AtLeast || c.Status != StatusPriced {
		t.Errorf("unset counter (E1): %+v", c)
	}
	var tot Total
	tot.Add(c)
	if !tot.Partial() {
		t.Errorf("total of a lower-bound call is whole")
	}
}

func TestPrice_TierThreshold(t *testing.T) {
	proto := register(t)
	price := func(in, write, read int64) string {
		u := usage(proto, in, 1_000_000, write, read, `{"w5":0,"w1":0}`)
		if write > 0 {
			u.Detail = json.RawMessage(`{"w5":` + strconv.FormatInt(write, 10) + `,"w1":0}`)
		}
		u.Model = "tiered"
		return USD(line(t, Price(u), KindOutput).Amount)
	}
	if got := price(100_000, 0, 0); got != "0.5" {
		t.Errorf("at the threshold: output = %s, want 0.5", got)
	}
	if got := price(1, 50_000, 50_000); got != "2.5" {
		t.Errorf("one over the threshold, counting cache: output = %s, want 2.5", got)
	}
}

func TestParseTable_Errors(t *testing.T) {
	head := "model,from,prompt_over,input,output,cache_read,cache_write_5m,cache_write_1h\n"
	for name, body := range map[string]string{
		"bad header":      "model,input\nm,1\n",
		"bad price":       head + "m,,,4.1234567,1,1,1,1\n",
		"negative":        head + "m,,,-1,1,1,1,1\n",
		"bad date":        head + "m,June,,1,1,1,1,1\n",
		"duplicate":       head + "m,,,1,1,1,1,1\nm,,,2,2,2,2,2\n",
		"tier only":       head + "m,,1000,1,1,1,1,1\n",
		"missing columns": head + "m,,,1,1\n",
		"empty model":     head + ",,,1,1,1,1,1\n",
	} {
		if _, err := ParseTable(strings.NewReader(body)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestParseRate(t *testing.T) {
	for in, want := range map[string]Rate{"4": 4_000_000, "0.20": 200_000, "0.125": 125_000, "12.50": 12_500_000, "0.000001": 1} {
		if got, err := ParseRate(in); err != nil || got != want {
			t.Errorf("ParseRate(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
}

func TestUSD(t *testing.T) {
	for in, want := range map[Pico]string{0: "0", 1: "0.000000000001", 45_210_800_000: "0.0452108", 4_000_000_000_000: "4", 12_345_678_900_000_000: "12345.6789"} {
		if got := USD(in); got != want {
			t.Errorf("USD(%d) = %s, want %s", in, got, want)
		}
	}
}
