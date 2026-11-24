package ui

import (
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/cost"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// todayJSON is the totals since a time (AC4): sessions with any activity (a model call or
// a hook, 017 D30), model calls with side calls included, and since spec 017 AC3 the files
// the sessions changed (distinct per session) and the sites they connected to (distinct
// hosts).
type todayJSON struct {
	Sessions   int         `json:"sessions"`
	Calls      int         `json:"calls"`
	Changed    int         `json:"changed"`
	Sites      int         `json:"sites"`
	Input      counterJSON `json:"input"`
	Output     counterJSON `json:"output"`
	CacheWrite counterJSON `json:"cache_write"`
	CacheRead  counterJSON `json:"cache_read"`
	// Cost is the API-equivalent cost of the calls since the time (spec 020 AC8).
	Cost cost.TotalJSON `json:"cost"`
	// Hourly is the same totals per whole hour from since, for the sparklines (spec 027 C6b).
	Hourly hourlyJSON `json:"hourly"`
}

// maxHourlyBuckets caps the hourly arrays whatever since a client sends.
const maxHourlyBuckets = 48

// hourlyJSON: bucket i covers [start + i h, start + (i+1) h); buckets run up to and
// including the hour holding now. Calls counts model calls (side calls included),
// Sessions distinct sessions with a call or a hook in the bucket. The peaks are the
// largest values, so a client scales bars without computing.
type hourlyJSON struct {
	Start        string `json:"start"`
	Calls        []int  `json:"calls"`
	Sessions     []int  `json:"sessions"`
	PeakCalls    int    `json:"peak_calls"`
	PeakSessions int    `json:"peak_sessions"`
}

func (d Deps) hourlyBuckets(c *store.CtxReader, since time.Time, exs []store.ExchangeStub) (hourlyJSON, error) {
	n := 0
	if now := d.now(); !now.Before(since) {
		n = min(int(now.Sub(since)/time.Hour)+1, maxHourlyBuckets)
	}
	h := hourlyJSON{Start: since.Format(time.RFC3339), Calls: make([]int, n), Sessions: make([]int, n)}
	if n == 0 {
		return h, nil
	}
	sess := make([]map[[3]string]bool, n)
	for i := range sess {
		sess[i] = map[[3]string]bool{}
	}
	bucket := func(ns int64) (int, bool) {
		i := int((ns - since.UnixNano()) / int64(time.Hour))
		return i, ns >= since.UnixNano() && i < n
	}
	for _, ex := range exs {
		i, ok := bucket(ex.StartedAt)
		if !ok {
			continue
		}
		h.Calls[i]++
		if ex.SessionKey != "" {
			sess[i][[3]string{ex.PrincipalID, ex.Client, ex.SessionKey}] = true
		}
	}
	hooked, err := c.HookSessionsByHour(since.UnixNano(), n)
	if err != nil {
		return h, err
	}
	for _, hb := range hooked {
		if hb.Bucket >= 0 && hb.Bucket < n {
			sess[hb.Bucket][[3]string{hb.Session.Principal, hb.Session.Client, hb.Session.Key}] = true
		}
	}
	for i := range n {
		h.Sessions[i] = len(sess[i])
		h.PeakCalls = max(h.PeakCalls, h.Calls[i])
		h.PeakSessions = max(h.PeakSessions, h.Sessions[i])
	}
	return h, nil
}

// callMemoMax bounds the memoised calls; past it the memo starts over, which only costs
// one slow request.
const callMemoMax = 50000

// CallMemo remembers, per request id, what a parsed call contributes to the totals (nil:
// the exchange is not a model call). A parse lands once, in one transaction with its
// events, and the price tables are fixed, so a parsed call's contribution never changes
// and is read from the store once. It is keyed by the call, not by the time the totals
// start from, so a new day reuses every call it shares with the old one. Safe for
// concurrent use.
type CallMemo struct {
	mu sync.Mutex
	m  map[string]*timeline.CallUsage
}

// NewCallMemo returns an empty memo.
func NewCallMemo() *CallMemo { return &CallMemo{m: map[string]*timeline.CallUsage{}} }

func (c *CallMemo) get(id string) (*timeline.CallUsage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	u, ok := c.m[id]
	return u, ok
}

func (c *CallMemo) put(id string, u *timeline.CallUsage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= callMemoMax {
		c.m = map[string]*timeline.CallUsage{}
	}
	c.m[id] = u
}

// callUsageOf is the usage of one exchange as the session totals read it: from its
// request and usage events. An unparsed exchange has no events and is not memoised, since
// its parse may still land.
func (d Deps) callUsageOf(c *store.CtxReader, s store.ExchangeStub) (*timeline.CallUsage, error) {
	if s.Parsed {
		if u, ok := d.Calls.get(s.RequestID); ok {
			return u, nil
		}
	}
	evs, err := c.CallEvents(s.RequestID)
	if err != nil {
		return nil, err
	}
	u := timeline.CallOf(store.ExchangeRow{Protocol: s.Protocol, StartedAt: s.StartedAt,
		TTFBNS: s.TTFBNS, Status: s.Status}, evs)
	if s.Parsed {
		d.Calls.put(s.RequestID, u)
	}
	return u, nil
}

func mountToday(api chi.Router, d Deps) { api.Get("/today", d.getToday) }

func (d Deps) getToday(w http.ResponseWriter, r *http.Request) {
	since, err := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "since must be an RFC 3339 time")
		return
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	exs, err := c.ExchangeStubsSince(since.UnixNano())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	calls := make([]timeline.CallUsage, 0, len(exs))
	for _, ex := range exs {
		u, err := d.callUsageOf(c, ex)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		if u != nil {
			calls = append(calls, *u)
		}
	}
	u := usageToJSON(timeline.Summary{Total: timeline.Summarize(calls).Total})
	seen := map[[3]string]bool{}
	for _, ex := range exs {
		if ex.SessionKey != "" {
			seen[[3]string{ex.PrincipalID, ex.Client, ex.SessionKey}] = true
		}
	}
	// 017 AC3 (D30): a session with only hooks since the time counts too.
	hooked, err := c.HookSessionsSince(since.UnixNano())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	for _, id := range hooked {
		seen[[3]string{id.Principal, id.Client, id.Key}] = true
	}
	changed, sites, err := c.EffectTotalsSince(since.UnixNano())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	hourly, err := d.hourlyBuckets(c, since, exs)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, todayJSON{
		Sessions: len(seen), Calls: len(calls), Changed: changed, Sites: sites, Hourly: hourly,
		Input: u.Input, Output: u.Output, CacheWrite: u.CacheWrite, CacheRead: u.CacheRead, Cost: u.Cost,
	})
}
