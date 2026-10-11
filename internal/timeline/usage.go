package timeline

import (
	"encoding/json"
	"slices"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// UnknownModel names the group of calls whose request event carries no model.
const UnknownModel = "unknown"

// The reasons a model call has no usage.
const (
	NoUsageErrored = "errored"
	NoUsageMissing = "missing"
)

// Counters are one call's token counts; nil means the usage event left it unset.
type Counters struct {
	Input, Output, CacheWrite, CacheRead *int64
}

// CallUsage is the usage of one model call: an exchange with a request event.
type CallUsage struct {
	Model string
	Counters
	Partial bool   // the usage event came from a partial parse
	NoUsage string // "", NoUsageErrored or NoUsageMissing; set only without a usage event
	// HasUsage is true when the exchange has a usage event.
	HasUsage bool
}

// callUsage derives the call record of an exchange from its events; the bool is
// false when the exchange is not a model call (no request event).
func callUsage(ex store.ExchangeRow, evs []store.EventRow) (CallUsage, bool) {
	var (
		call    CallUsage
		isCall  bool
		usageEv *store.EventRow
	)
	for i := range evs {
		switch evs[i].Kind {
		case "request":
			if !isCall {
				isCall = true
				var p struct {
					Model string `json:"model"`
				}
				_ = json.Unmarshal(evs[i].Payload, &p) // malformed: model unknown
				call.Model = p.Model
			}
		case "usage":
			usageEv = &evs[i] // a second one is not expected; the last wins
		}
	}
	if !isCall {
		return CallUsage{}, false
	}
	if call.Model == "" {
		call.Model = UnknownModel
	}
	if usageEv != nil {
		call.HasUsage, call.Partial = true, usageEv.Partial
		var p struct {
			Input      *int64 `json:"input_tokens"`
			Output     *int64 `json:"output_tokens"`
			CacheWrite *int64 `json:"cache_write_tokens"`
			CacheRead  *int64 `json:"cache_read_tokens"`
		}
		if json.Unmarshal(usageEv.Payload, &p) == nil { // malformed: all counters nil
			call.Counters = Counters{p.Input, p.Output, p.CacheWrite, p.CacheRead}
		}
		return call, true
	}
	// Without a recorded upstream response (TTFBNS nil) the stored status is the
	// gateway's own fallback, which can look like a 4xx/5xx; only a recorded
	// error response counts as errored.
	call.NoUsage = NoUsageMissing
	if ex.TTFBNS != nil && ex.Status >= 400 && ex.Status <= 599 {
		call.NoUsage = NoUsageErrored
	}
	return call, true
}

// Sum is one counter summed over the calls that reported it.
type Sum struct {
	Value      *int64 // nil when no call reported the counter
	Unreported int    // calls with a usage event that left it nil
}

func (s *Sum) add(v *int64) {
	if v == nil {
		s.Unreported++
		return
	}
	if s.Value == nil {
		s.Value = new(int64)
	}
	*s.Value += *v
}

// Group is the usage total of a set of model calls.
type Group struct {
	Calls                                int
	Reporting                            int // calls with a usage event
	Missing                              int // calls without one, not errored
	Errored                              int // calls without one, whose response was an error
	PartialCalls                         int
	Input, Output, CacheWrite, CacheRead Sum
}

// Incomplete reports whether some call is missing usage or left a counter unset.
func (g Group) Incomplete() bool {
	return g.Missing > 0 || g.Input.Unreported > 0 || g.Output.Unreported > 0 ||
		g.CacheWrite.Unreported > 0 || g.CacheRead.Unreported > 0
}

// AllErrored reports whether every call errored without usage.
func (g Group) AllErrored() bool { return g.Reporting == 0 && g.Missing == 0 && g.Errored > 0 }

func (g *Group) add(c CallUsage) {
	g.Calls++
	switch {
	case c.HasUsage:
		g.Reporting++
		if c.Partial {
			g.PartialCalls++
		}
		g.Input.add(c.Input)
		g.Output.add(c.Output)
		g.CacheWrite.add(c.CacheWrite)
		g.CacheRead.add(c.CacheRead)
	case c.NoUsage == NoUsageErrored:
		g.Errored++
	default:
		g.Missing++
	}
}

// NamedGroup is the total of one model's calls.
type NamedGroup struct {
	Model string
	Group
}

// Summary is the usage of a set of exchanges: the total and one group per model,
// sorted by name with unknown last. Total and Models come from the same calls.
type Summary struct {
	Calls  int
	Total  Group
	Models []NamedGroup
}

// Summarize totals the calls overall and per model.
func Summarize(calls []CallUsage) Summary {
	s := Summary{Calls: len(calls)}
	byModel := map[string]*Group{}
	for _, c := range calls {
		s.Total.add(c)
		g := byModel[c.Model]
		if g == nil {
			g = &Group{}
			byModel[c.Model] = g
		}
		g.add(c)
	}
	for m, g := range byModel {
		s.Models = append(s.Models, NamedGroup{Model: m, Group: *g})
	}
	slices.SortFunc(s.Models, func(a, b NamedGroup) int {
		switch {
		case a.Model == b.Model:
			return 0
		case a.Model == UnknownModel:
			return 1
		case b.Model == UnknownModel:
			return -1
		case a.Model < b.Model:
			return -1
		}
		return 1
	})
	return s
}
