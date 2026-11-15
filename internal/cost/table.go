// Package cost prices model calls at published API prices: what a call's captured
// tokens would cost on the provider's API ("API-equivalent", spec 020), not what was
// charged. It names no provider: each protocol hands over its own price table and its
// reading of the usage detail as a Book.
//
// Money is exact integer arithmetic: a price is micro-dollars per million tokens, and
// tokens × price is pico-dollars (10⁻¹² USD) with no rounding (research D7).
package cost

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Rate is a price in micro-dollars per million tokens: $4 / MTok is 4_000_000.
type Rate int64

// Pico is an amount in pico-dollars (10⁻¹² USD). int64 holds $9.2 million.
type Pico int64

// Prices are one row's five prices.
type Prices struct {
	Input, Output, CacheRead, CacheWrite5m, CacheWrite1h Rate
}

// Row is one model's prices for one period, from its effective date (zero: since the
// start) until the next row's, and for prompts above PromptOver tokens (0: any prompt).
type Row struct {
	Model      string
	From       time.Time
	PromptOver int64
	Prices
}

// Table is a provider's price rows by model.
type Table struct {
	rows map[string][]Row // per model, by From then PromptOver
}

// columns is the header a table file must start with.
var columns = []string{"model", "from", "prompt_over", "input", "output", "cache_read", "cache_write_5m", "cache_write_1h"}

// ParseTable reads a price table: a CSV with the header in columns, '#' comment lines,
// one row per model per period (and tier). from is a UTC day (2006-01-02) or empty for
// "since the start"; prompt_over is a token count or empty for 0; prices are decimal USD
// per million tokens with at most six decimals. Every period of a model needs a row for
// any prompt (prompt_over 0); duplicates are an error.
func ParseTable(r io.Reader) (Table, error) {
	cr := csv.NewReader(r)
	cr.Comment = '#'
	cr.TrimLeadingSpace = true
	recs, err := cr.ReadAll()
	if err != nil {
		return Table{}, fmt.Errorf("cost: price table: %w", err)
	}
	if len(recs) == 0 || !slices.Equal(recs[0], columns) {
		return Table{}, fmt.Errorf("cost: price table: header must be %s", strings.Join(columns, ","))
	}
	t := Table{rows: map[string][]Row{}}
	for i, rec := range recs[1:] {
		row, err := parseRow(rec)
		if err != nil {
			return Table{}, fmt.Errorf("cost: price table row %d: %w", i+2, err)
		}
		t.rows[row.Model] = append(t.rows[row.Model], row)
	}
	for m, rows := range t.rows {
		slices.SortFunc(rows, func(a, b Row) int {
			if c := a.From.Compare(b.From); c != 0 {
				return c
			}
			return int(min(max(a.PromptOver-b.PromptOver, -1), 1))
		})
		for i, r := range rows {
			if i > 0 && rows[i-1].From.Equal(r.From) && rows[i-1].PromptOver == r.PromptOver {
				return Table{}, fmt.Errorf("cost: price table: %s has two rows for one period and tier", m)
			}
			if (i == 0 || !rows[i-1].From.Equal(r.From)) && r.PromptOver != 0 {
				return Table{}, fmt.Errorf("cost: price table: %s has a period with no row for any prompt", m)
			}
		}
	}
	return t, nil
}

func parseRow(rec []string) (Row, error) {
	if rec[0] == "" {
		return Row{}, errors.New("model is empty")
	}
	row := Row{Model: rec[0]}
	if rec[1] != "" {
		d, err := time.Parse(time.DateOnly, rec[1])
		if err != nil {
			return Row{}, fmt.Errorf("from: %w", err)
		}
		row.From = d
	}
	if rec[2] != "" {
		n, err := strconv.ParseInt(rec[2], 10, 64)
		if err != nil || n < 0 {
			return Row{}, fmt.Errorf("prompt_over %q is not a token count", rec[2])
		}
		row.PromptOver = n
	}
	dst := []*Rate{&row.Input, &row.Output, &row.CacheRead, &row.CacheWrite5m, &row.CacheWrite1h}
	for i, p := range dst {
		r, err := ParseRate(rec[3+i])
		if err != nil {
			return Row{}, fmt.Errorf("%s: %w", columns[3+i], err)
		}
		*p = r
	}
	return row, nil
}

// ParseRate reads a decimal USD price per million tokens ("4", "0.20", "0.125") into
// micro-dollars per million tokens, exactly. More than six decimals is an error.
func ParseRate(s string) (Rate, error) {
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" || len(frac) > 6 || strings.Trim(whole+frac, "0123456789") != "" {
		return 0, fmt.Errorf("price %q is not a decimal with at most six places", s)
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w > 1_000_000 {
		return 0, fmt.Errorf("price %q is out of range", s)
	}
	f := int64(0)
	if frac != "" {
		f, _ = strconv.ParseInt(frac+strings.Repeat("0", 6-len(frac)), 10, 64)
	}
	return Rate(w*1_000_000 + f), nil
}

// Models lists the models the table prices, sorted.
func (t Table) Models() []string {
	out := make([]string, 0, len(t.rows))
	for m := range t.rows {
		out = append(out, m)
	}
	slices.Sort(out)
	return out
}

// Find returns the prices in force for model at the instant at, for a prompt of prompt
// tokens: the latest period starting at or before at, and in it the row with the highest
// threshold the prompt is above. False when the model has no row, or no period has
// started by at.
func (t Table) Find(model string, at time.Time, prompt int64) (Prices, bool) {
	var (
		best  *Row
		found bool
	)
	for i := range t.rows[model] {
		r := &t.rows[model][i]
		if r.From.After(at) {
			break
		}
		if best != nil && !best.From.Equal(r.From) {
			best = nil // a later period replaces the earlier one whole
		}
		if r.PromptOver == 0 || prompt > r.PromptOver {
			best, found = r, true
		}
	}
	if !found || best == nil {
		return Prices{}, false
	}
	return best.Prices, true
}
