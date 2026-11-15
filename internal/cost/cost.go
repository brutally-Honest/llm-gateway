package cost

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Facts are what a protocol reads from a usage event's detail for pricing.
type Facts struct {
	// Split is the cache writes by lifetime; nil when the usage reports none.
	Split *Split
	// Variant names a price variant the table does not hold ("fast", "us"); "" for the
	// standard price. A call with a variant is not priced (research D6).
	Variant string
}

// Split is a call's cache-write tokens by cache lifetime.
type Split struct {
	FiveMin, OneHour int64
}

// Book is one protocol's pricing: its table, the provider's name for labels, and how to
// read the usage detail. Facts may be nil (no split, no variant).
type Book struct {
	Source string
	Table  Table
	Facts  func(detail json.RawMessage) Facts
}

var (
	mu    sync.RWMutex
	books = map[string]Book{}
)

// Register sets the book for a protocol. It is called at start, before any call is
// priced; a later call for the same protocol replaces the book (research D9).
func Register(protocol string, b Book) {
	mu.Lock()
	defer mu.Unlock()
	books[protocol] = b
}

func bookOf(protocol string) (Book, bool) {
	mu.RLock()
	defer mu.RUnlock()
	b, ok := books[protocol]
	return b, ok
}

// Usage is one call's stored usage, as the pricer reads it. A nil counter was left
// unset by the usage event.
type Usage struct {
	Protocol, Model string
	At              time.Time
	Input, Output   *int64
	CacheWrite      *int64
	CacheRead       *int64
	Detail          json.RawMessage
	// Partial: the usage came from a reply cut short, so the counts may be low.
	Partial bool
}

// The statuses of a call's cost.
const (
	StatusPriced  = "priced"
	StatusNoPrice = "no_price" // no row for the model (or its variant) at the call's time
	StatusNoUsage = "no_usage" // no usage counts; may have used tokens, so left out
	StatusErrored = "errored"  // an error response with no usage: costs nothing
)

// The token kinds of a breakdown, in the order they are listed.
const (
	KindInput        = "input"
	KindCacheWrite5m = "cache_write_5m"
	KindCacheWrite1h = "cache_write_1h"
	KindCacheRead    = "cache_read"
	KindOutput       = "output"
)

// Line is one token kind's part of a call's cost: tokens × rate = amount.
type Line struct {
	Kind   string
	Tokens int64
	Rate   Rate
	Amount Pico
}

// Call is the API-equivalent cost of one model call.
type Call struct {
	Status string
	Reason string // words for no_price, no_usage and errored
	Source string // the provider whose prices were used
	Model  string
	Lines  []Line
	Amount Pico
	// Estimated: the usage had no 5m/1h split, so every cache write is at the 5m rate.
	Estimated bool
	// AtLeast: the counts may be low (a reply cut short, or a counter left unset).
	AtLeast bool
}

// NoUsage is the cost of a call with no usage counts: errored when its response was an
// error (nothing billed), else left out of totals. reason is shown as it is.
func NoUsage(errored bool, reason string) Call {
	if errored {
		return Call{Status: StatusErrored, Reason: reason}
	}
	return Call{Status: StatusNoUsage, Reason: reason}
}

func val(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// Price prices one call's usage at the row in force when it started.
func Price(u Usage) Call {
	c := Call{Model: u.Model, AtLeast: u.Partial ||
		u.Input == nil || u.Output == nil || u.CacheWrite == nil || u.CacheRead == nil}
	noPrice := func(variant string) Call {
		c.Status, c.Reason = StatusNoPrice, "no price for "+u.Model
		if variant != "" {
			c.Reason += " (" + variant + ")"
		}
		c.AtLeast = false
		return c
	}
	b, ok := bookOf(u.Protocol)
	if !ok {
		return noPrice("")
	}
	c.Source = b.Source
	var f Facts
	if b.Facts != nil && len(u.Detail) > 0 {
		f = b.Facts(u.Detail)
	}
	if f.Variant != "" {
		return noPrice(f.Variant)
	}
	in, out, write, read := val(u.Input), val(u.Output), val(u.CacheWrite), val(u.CacheRead)
	p, ok := b.Table.Find(u.Model, u.At, in+write+read)
	if !ok {
		return noPrice("")
	}
	// Without a split that adds up (E2), every write is at the 5m rate; a call that wrote
	// nothing has nothing to estimate.
	w5, w1 := write, int64(0)
	if s := f.Split; s != nil && u.CacheWrite != nil && s.FiveMin >= 0 && s.OneHour >= 0 && s.FiveMin+s.OneHour == write {
		w5, w1 = s.FiveMin, s.OneHour
	} else {
		c.Estimated = write > 0
	}
	c.Status = StatusPriced
	for _, l := range []Line{
		{KindInput, in, p.Input, 0},
		{KindCacheWrite5m, w5, p.CacheWrite5m, 0},
		{KindCacheWrite1h, w1, p.CacheWrite1h, 0},
		{KindCacheRead, read, p.CacheRead, 0},
		{KindOutput, out, p.Output, 0},
	} {
		l.Amount = Pico(l.Tokens) * Pico(l.Rate)
		c.Amount += l.Amount
		c.Lines = append(c.Lines, l)
	}
	return c
}

// Total is the cost of a set of calls: the priced ones summed, the rest counted.
type Total struct {
	Amount        Pico
	Priced        int
	NoPrice       int
	NoPriceModels []string // sorted, distinct
	NoUsage       int
	Errored       int
	Estimated     int
	AtLeast       int
	Source        string // the provider of the priced calls; "" when none or several
	sources       int
}

// Add counts one call into the total.
func (t *Total) Add(c Call) {
	switch c.Status {
	case StatusPriced:
		t.Priced++
		t.Amount += c.Amount
		if c.Estimated {
			t.Estimated++
		}
		if c.AtLeast {
			t.AtLeast++
		}
		switch {
		case t.sources == 0:
			t.Source, t.sources = c.Source, 1
		case t.Source != c.Source:
			t.Source = ""
		}
	case StatusNoPrice:
		t.NoPrice++
		if !slices.Contains(t.NoPriceModels, c.Model) {
			t.NoPriceModels = append(t.NoPriceModels, c.Model)
			slices.Sort(t.NoPriceModels)
		}
	case StatusNoUsage:
		t.NoUsage++
	case StatusErrored:
		t.Errored++
	}
}

// Partial reports whether the amount is a lower bound: a call was left out, or a priced
// call's counts may be low.
func (t Total) Partial() bool { return t.NoPrice > 0 || t.NoUsage > 0 || t.AtLeast > 0 }

// USD writes an amount as an exact decimal string in dollars, trailing zeros trimmed:
// 45_210_800_000 is "0.0452108", 0 is "0".
func USD(p Pico) string {
	neg := p < 0
	if neg {
		p = -p
	}
	s := strconv.FormatInt(int64(p), 10)
	if len(s) <= 12 {
		s = strings.Repeat("0", 13-len(s)) + s
	}
	whole, frac := s[:len(s)-12], strings.TrimRight(s[len(s)-12:], "0")
	out := whole
	if frac != "" {
		out += "." + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

// RateUSD writes a rate as decimal USD per million tokens: 200_000 is "0.2".
func RateUSD(r Rate) string { return USD(Pico(r) * 1_000_000) }
