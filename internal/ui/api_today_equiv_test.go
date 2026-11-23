package ui

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// referenceSource and referenceToday are the totals as they were computed before the
// memo: every exchange since the time read whole, each one's events read whole, and
// SessionStats over them. They stay here as the expectation the fast path must equal.
type referenceSource struct {
	*store.CtxReader
	exs []store.ExchangeRow
}

func (s referenceSource) SessionExchanges(string, string, string) ([]store.ExchangeRow, error) {
	return s.exs, nil
}

func (e *digestEnv) referenceToday(since time.Time) []byte {
	e.t.Helper()
	c := e.d.Reader.WithContext(e.t.Context())
	exs, err := c.ExchangesSince(since.UnixNano())
	if err != nil {
		e.t.Fatal(err)
	}
	st, err := timeline.SessionStats(referenceSource{c, exs}, store.SessionRow{})
	if err != nil {
		e.t.Fatal(err)
	}
	u := usageToJSON(timeline.Summary{Total: st.Usage.Total})
	seen := map[[3]string]bool{}
	stubs := make([]store.ExchangeStub, 0, len(exs))
	for _, ex := range exs {
		if ex.SessionKey != "" {
			seen[[3]string{ex.PrincipalID, ex.Client, ex.SessionKey}] = true
		}
		stubs = append(stubs, store.ExchangeStub{StartedAt: ex.StartedAt, PrincipalID: ex.PrincipalID,
			Client: ex.Client, SessionKey: ex.SessionKey})
	}
	hooked, err := c.HookSessionsSince(since.UnixNano())
	if err != nil {
		e.t.Fatal(err)
	}
	for _, id := range hooked {
		seen[[3]string{id.Principal, id.Client, id.Key}] = true
	}
	changed, sites, err := c.EffectTotalsSince(since.UnixNano())
	if err != nil {
		e.t.Fatal(err)
	}
	hourly, err := e.d.hourlyBuckets(c, since, stubs) // the hourly logic is unchanged and has its own tests
	if err != nil {
		e.t.Fatal(err)
	}
	out, err := json.Marshal(todayJSON{
		Sessions: len(seen), Calls: st.ModelCalls, Changed: changed, Sites: sites, Hourly: hourly,
		Input: u.Input, Output: u.Output, CacheWrite: u.CacheWrite, CacheRead: u.CacheRead, Cost: u.Cost,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

// exchangeAt stores an exchange with a protocol, client and whether a response arrived.
func (e *digestEnv) exchangeAt(id, protocol, client, key string, at, status int, ttfb bool) {
	e.t.Helper()
	ex := &core.Exchange{
		RequestID: id, PrincipalID: core.PrincipalLocal, Protocol: protocol, Client: client,
		Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/x", Status: status, SessionKey: key,
		Start: baseTime.Add(time.Duration(at) * time.Second),
	}
	ex.End = ex.Start.Add(time.Second)
	ex.HasTTFB = ttfb
	if err := e.s.SaveExchange(e.t.Context(), ex); err != nil {
		e.t.Fatal(err)
	}
}

func (e *digestEnv) parseWith(id string, status core.ParseStatus, evs ...core.StoredEvent) {
	e.t.Helper()
	if err := e.s.SaveParse(e.t.Context(), id, core.PrincipalLocal, status, evs, nil); err != nil {
		e.t.Fatal(err)
	}
}

func reqEv(seq int, payload string) core.StoredEvent {
	return core.StoredEvent{Seq: seq, Kind: core.KindRequest, Payload: []byte(payload)}
}

func usageEv(seq int, payload string, partial bool) core.StoredEvent {
	return core.StoredEvent{Seq: seq, Kind: core.KindUsage, Partial: partial, Payload: []byte(payload)}
}

func (e *digestEnv) todayBody(since time.Time) []byte {
	e.t.Helper()
	rec := e.get("/api/today?since=" + url.QueryEscape(since.UTC().Format(time.RFC3339)))
	if rec.Code != 200 {
		e.t.Fatalf("today = %d %s", rec.Code, rec.Body)
	}
	return append([]byte(nil), rec.Body.Bytes()[:rec.Body.Len()-1]...) // writeJSON ends with a newline
}

// sameAsReference checks the response equals the reference for a spread of since values,
// the exact start of a call and one second before it among them.
func (e *digestEnv) sameAsReference(label string) {
	e.t.Helper()
	for _, sec := range []int{-1000, 9, 10, 11, 25, 31, 1000} {
		since := baseTime.Add(time.Duration(sec) * time.Second).UTC()
		got, want := e.todayBody(since), e.referenceToday(since)
		if string(got) != string(want) {
			e.t.Fatalf("%s: since %+ds\n got %s\nwant %s", label, sec, got, want)
		}
	}
}

// The memoised path gives the bytes the plain path gives, on a store with priced, unpriced
// and unknown models, usage that is partial, half-set or missing, errored and unparsed
// and failed-parse calls, side calls, a call without a session, hook-only sessions and
// effects, for since values before, at and after calls, one handler serving all of them
// (a new since never gets an old since's totals).
func TestToday_MemoisedEqualsReference(t *testing.T) {
	e := withRules(t)
	e.call("old", 5, 0, 100, "user:old")
	e.call("m1a", 10, 0, 3, "user:hello")                                    // priced m1, at 10 s
	e.call("side", 11, 0, 4, "user:"+titlePrompt, `assistant:{"title":"T"}`) // side call
	e.exchangeAt("m2", "test", "cc", "k2", 12, 200, true)                    // model without a price
	e.parseWith("m2", core.ParseOK, reqEv(0, `{"model":"m2"}`), usageEv(1, `{"input_tokens":9,"output_tokens":2}`, false))
	e.exchangeAt("nomodel", "test", "cc", "k2", 13, 200, true) // no model: unknown
	e.parseWith("nomodel", core.ParseOK, reqEv(0, `{}`), usageEv(1, `{"input_tokens":5}`, false))
	e.exchangeAt("partial", "test", "cc", "k3", 14, 200, true)
	e.parseWith("partial", core.ParsePartial, reqEv(0, `{"model":"m1"}`), usageEv(1, `{"input_tokens":2,"output_tokens":1,"cache_read_tokens":4}`, true))
	e.exchangeAt("nousage", "test", "cc", "k3", 15, 200, true)
	e.parseWith("nousage", core.ParseOK, reqEv(0, `{"model":"m1"}`))
	e.exchangeAt("errored", "test", "cc", "k3", 16, 529, true)
	e.parseWith("errored", core.ParseOK, reqEv(0, `{"model":"m1"}`))
	e.exchangeAt("gwerr", "test", "cc", "k3", 17, 502, false) // a 5xx the gateway made up
	e.parseWith("gwerr", core.ParseOK, reqEv(0, `{"model":"m1"}`))
	e.exchangeAt("badpayload", "test", "cc", "k3", 18, 200, true)
	e.parseWith("badpayload", core.ParseOK, reqEv(0, `{"model":`), usageEv(1, `not json`, false))
	e.exchangeAt("unpriced-protocol", "other", "cc", "k4", 19, 200, true)
	e.parseWith("unpriced-protocol", core.ParseOK, reqEv(0, `{"model":"m1"}`), usageEv(1, `{"input_tokens":1,"output_tokens":1}`, false))
	e.exchangeAt("failed", "test", "cc", "k4", 20, 200, true) // failed parse: no events
	e.parseWith("failed", core.ParseFailed)
	e.exchangeAt("unparsed", "test", "cc", "k4", 21, 200, true) // parse not landed
	e.exchangeAt("nokey", "test", "cc", "", 22, 200, true)      // no session
	e.parseWith("nokey", core.ParseOK, reqEv(0, `{"model":"m1"}`), usageEv(1, `{"input_tokens":6,"output_tokens":1}`, false))
	e.exchangeAt("unknown-client", "test", "", "k5", 23, 200, true)
	e.parseWith("unknown-client", core.ParseOK, reqEv(0, `{"model":"m1"}`), usageEv(1, `{"input_tokens":1,"output_tokens":1}`, false))
	e.hook("hooks-only", core.HarnessEvent{Phase: core.PhasePrompt}, 24)
	e.hook("k", core.HarnessEvent{Phase: core.PhasePrompt}, 25)
	e.hook("hook-before", core.HarnessEvent{Phase: core.PhasePrompt}, 1)
	at := func(s int) time.Time { return baseTime.Add(time.Duration(s) * time.Second) }
	if err := e.s.SaveEffects(t.Context(), []core.Effect{
		{Kind: core.EffectFileChange, At: at(26), Principal: core.PrincipalLocal, Client: "cc", SessionKey: "k",
			Root: "/w", Path: "/w/a.go", Change: core.FileModified, Mode: "git"},
		{Kind: core.EffectNetConn, At: at(27), EndedAt: at(28), Principal: core.PrincipalLocal, Client: "cc",
			SessionKey: "k", Host: "api.example", Port: 443, Via: "connect", Outcome: core.NetOK},
	}, nil); err != nil {
		t.Fatal(err)
	}
	e.nowAt(100)

	e.sameAsReference("first")
	e.sameAsReference("memo warm")
	var m map[string]any
	if err := json.Unmarshal(e.todayBody(at(10)), &m); err != nil || m["calls"] == float64(0) {
		t.Fatalf("fixture is empty: %v %v", m, err)
	}
	if costOf(t, m)["priced"] == float64(0) {
		t.Fatalf("fixture prices nothing: %v", m)
	}

	// A new call, a parse that lands on the unparsed exchange, and a later hook change
	// the totals the next request gives; the memo never holds an unparsed call.
	e.call("late", 30, 0, 7, "user:later")
	e.parseWith("unparsed", core.ParseOK, reqEv(0, `{"model":"m1"}`), usageEv(1, `{"input_tokens":8,"output_tokens":3}`, false))
	e.hook("late-hook", core.HarnessEvent{Phase: core.PhasePrompt}, 31)
	e.sameAsReference("after changes")
	e.sameAsReference("after changes, warm")
}

// A memo that a request filled for one since is not stale for another: the same calls
// are counted per since, not per memo.
func TestToday_NewSinceNeverServesOldTotals(t *testing.T) {
	e := withRules(t)
	e.call("a", 10, 0, 3, "user:one")
	e.call("b", 20, 0, 4, "user:two")
	e.nowAt(100)
	first := e.today(sinceAt(0))
	second := e.today(sinceAt(15))
	if first["calls"] != float64(2) || second["calls"] != float64(1) ||
		counter(t, first, "input") != float64(7) || counter(t, second, "input") != float64(4) {
		t.Fatalf("first = %v, second = %v", first, second)
	}
	if again := e.today(sinceAt(0)); again["calls"] != float64(2) {
		t.Fatalf("back to the first since = %v", again)
	}
}
