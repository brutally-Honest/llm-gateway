package timeline

import (
	"net/url"
	"sort"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// CallDetails is one model call as the call details dialog shows it (spec 017 AC5–AC11):
// labelled facts, its context, what it saw and said, its health and its raw links, with the
// calls before and after it in the session. A number the call lacks is nil and shown as
// "—", with a reason saying why.
type CallDetails struct {
	N          int         `json:"n"`
	ExchangeID string      `json:"exchange_id"`
	Turn       int         `json:"turn,omitempty"`
	Facts      CallFactsV  `json:"facts"`
	Context    CallContext `json:"context"`
	Saw        CallSaw     `json:"saw"`
	Thinking   bool        `json:"thinking"`
	Response   CallReply   `json:"response"`
	Tools      []ToolLine  `json:"tools"`
	Health     CallHealth  `json:"health"`
	Raw        CallRaw     `json:"raw"`
	Nav        CallNav     `json:"nav"`
}

// CallSaw is what the call saw (AC8): its request's messages, each marked carried over
// from the previous call of its thread or new, with the counts; First marks a thread's
// first call. Unavailable says why there are no messages to show.
type CallSaw struct {
	First       bool         `json:"first"`
	Carried     *int         `json:"carried"`
	New         *int         `json:"new"`
	Messages    []SawMessage `json:"messages"`
	Unavailable string       `json:"unavailable,omitempty"`
}

// SawMessage is one request message, cut like the timeline.
type SawMessage struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
	New       bool   `json:"new"`
}

// CallReply is what the call said: the reply's text and stop reason, or why it is missing.
type CallReply struct {
	Text        string `json:"text"`
	StopReason  string `json:"stop_reason,omitempty"`
	Unavailable string `json:"unavailable,omitempty"`
}

// CallRaw links the stored request, the response stream, the headers and the full exchange
// page. A body that was not stored has no link and says so.
type CallRaw struct {
	Request         string `json:"request,omitempty"`
	RequestMissing  string `json:"request_missing,omitempty"`
	Response        string `json:"response,omitempty"`
	ResponseMissing string `json:"response_missing,omitempty"`
	Headers         string `json:"headers"`
	Page            string `json:"page"`
}

// UnavailableReason is the code for why an exchange's events hold nothing to show for one
// part ("request" or "response"): parse_failed, parse_skipped, parse_unsupported_encoding,
// <part>_body_not_stored, capture_truncated or nothing_parsed.
func UnavailableReason(ex store.ExchangeRow, part string, bodyStored, truncated bool) string {
	switch {
	case ex.Parse == "failed" || ex.Parse == "skipped" || ex.Parse == "unsupported_encoding":
		return "parse_" + ex.Parse
	case !bodyStored:
		return part + "_body_not_stored"
	case truncated:
		return "capture_truncated"
	}
	return "nothing_parsed"
}

// reasonWords says an UnavailableReason code in words.
func reasonWords(code string) string {
	switch code {
	case "parse_failed":
		return "The stored body could not be parsed."
	case "parse_skipped":
		return "The stored body was not parsed."
	case "parse_unsupported_encoding":
		return "The stored body uses an encoding the gateway cannot read."
	case "request_body_not_stored":
		return "The request body was not stored."
	case "response_body_not_stored":
		return "The response body was not stored."
	case "capture_truncated":
		return "The stored body is cut at the capture limit, so it could not be read in full."
	}
	return "Nothing could be read from the stored body."
}

// CallFactsV are the call's labelled facts (AC6).
type CallFactsV struct {
	Model  string     `json:"model"`
	DurMS  *int64     `json:"dur_ms"`
	Start  string     `json:"start"`
	Stop   CallStop   `json:"stop"`
	Thread CallThread `json:"thread"`
	Ctx    *int64     `json:"ctx"`
}

// CallStop is how the call ended, as a word: the provider's stop reason, or the error
// status. Bad marks a failed main or sub-agent call; a side call's failure is not (E2).
type CallStop struct {
	Word string `json:"word"`
	Bad  bool   `json:"bad,omitempty"`
}

// CallThread is the thread a call belongs to: main, a sub-agent by name, or a side call by
// the kind the client's profile gives it.
type CallThread struct {
	Kind  string `json:"kind"` // main, sub, side
	Label string `json:"label"`
}

// CallContext is the call's context (AC7): its size, the change since the previous call of
// its thread and what that added, in words, and the three counters it is made of.
type CallContext struct {
	Size       *int64 `json:"size"`
	Partial    bool   `json:"partial,omitempty"`
	Delta      *int64 `json:"delta"`
	PrevN      int    `json:"prev_n,omitempty"`
	First      bool   `json:"first,omitempty"`
	Why        string `json:"why,omitempty"`
	Input      *int64 `json:"input"`
	CacheWrite *int64 `json:"cache_write"`
	CacheRead  *int64 `json:"cache_read"`
	Output     *int64 `json:"output"`
	Note       string `json:"note,omitempty"`
}

// CallHealth is the call's disconnect, abort, error and partial-capture facts in words
// (AC9); OK is true when none apply.
type CallHealth struct {
	Status string       `json:"status"`
	OK     bool         `json:"ok"`
	Items  []HealthItem `json:"items"`
}

// HealthItem is one health fact: a tone (bad, warn) and the words.
type HealthItem struct {
	Tone string `json:"tone"`
	Text string `json:"text"`
}

// CallNav places the call among the session's calls, side calls included, in call order:
// its position, and the calls before and after it ("" at either end, E4).
type CallNav struct {
	Index int    `json:"index"`
	Total int    `json:"total"`
	Prev  string `json:"prev"`
	Next  string `json:"next"`
}

// CallInput is what the call details read beyond the session's view: the call's exchange,
// its usage (nil when it made no model call), its thread fact (nil when unknown) and what
// its stored events say (nil when they could not be read).
type CallInput struct {
	Exchange store.ExchangeRow
	Usage    *CallUsage
	Fact     *CallFact
	Detail   *Detail
}

// callPlace is where a call sits in the view.
type callPlace struct {
	step  *Step
	side  *SideCall
	turn  int
	agent string // the sub-agent's label, for a sub-agent's call
}

// callPlaces lists every call of the view, steps (sub-agents' included) and side calls,
// keyed by exchange id, and their ids in call order.
func callPlaces(v View) (map[string]callPlace, []string) {
	places := map[string]callPlace{}
	type at struct {
		n  int
		id string
	}
	var order []at
	var walk func(steps []Step, turn int, agent string)
	walk = func(steps []Step, turn int, agent string) {
		for i := range steps {
			s := &steps[i]
			places[s.ExchangeID] = callPlace{step: s, turn: turn, agent: agent}
			order = append(order, at{s.N, s.ExchangeID})
			for _, l := range s.Tools {
				if l.Sub != nil {
					walk(l.Sub.Steps, turn, l.Sub.Label)
				}
			}
		}
	}
	for _, t := range v.Turns {
		walk(t.Steps, t.N, "")
	}
	for i := range v.SideCalls {
		s := &v.SideCalls[i]
		places[s.ExchangeID] = callPlace{side: s}
		order = append(order, at{s.N, s.ExchangeID})
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].n < order[j].n })
	ids := make([]string, len(order))
	for i, o := range order {
		ids[i] = o.id
	}
	return places, ids
}

// ctxRowOf is the call's Context tab row: main thread first, then the sub-agents'.
func ctxRowOf(v View, id string) (CtxRow, *CtxRow, bool) {
	find := func(rows []CtxRow) (CtxRow, *CtxRow, bool) {
		for i, r := range rows {
			if r.ExchangeID == id {
				var prev *CtxRow
				if i > 0 {
					prev = &rows[i-1]
				}
				return r, prev, true
			}
		}
		return CtxRow{}, nil, false
	}
	if r, p, ok := find(v.Context.Main); ok {
		return r, p, true
	}
	for _, th := range v.Context.Subs {
		if r, p, ok := find(th.Rows); ok {
			return r, p, true
		}
	}
	return CtxRow{}, nil, false
}

// HasCall says whether the view holds a model call, side calls included, with that
// exchange id.
func HasCall(v View, id string) bool {
	places, _ := callPlaces(v)
	_, ok := places[id]
	return ok
}

// words turns a provider's stop reason into words: "end_turn" -> "end turn".
func words(s string) string { return strings.ReplaceAll(s, "_", " ") }

// CallDetailsOf builds one call's details from the session's view and the call's own
// input. ok is false when the view has no call with that exchange id (E3).
func CallDetailsOf(v View, id string, in CallInput) (CallDetails, bool) {
	places, ids := callPlaces(v)
	p, ok := places[id]
	if !ok {
		return CallDetails{}, false
	}
	d := CallDetails{ExchangeID: id, Turn: p.turn}
	d.Nav = navOf(ids, id)
	d.Facts = callFacts(p, in)
	if p.step != nil {
		d.N = p.step.N
	} else {
		d.N = p.side.N
	}
	d.Context = callContext(v, id, p, in)
	d.Facts.Ctx = d.Context.Size
	d.Health = callHealth(in.Exchange)
	d.Saw, d.Response = callSaw(in), callReply(in)
	d.Tools = []ToolLine{}
	if p.step != nil {
		d.Thinking = p.step.Thinking
		d.Tools = append(d.Tools, p.step.Tools...)
	}
	d.Raw = callRaw(in.Exchange)
	return d, true
}

func navOf(ids []string, id string) CallNav {
	nav := CallNav{Total: len(ids)}
	for i, x := range ids {
		if x != id {
			continue
		}
		nav.Index = i + 1
		if i > 0 {
			nav.Prev = ids[i-1]
		}
		if i+1 < len(ids) {
			nav.Next = ids[i+1]
		}
	}
	return nav
}

func callFacts(p callPlace, in CallInput) CallFactsV {
	ex := in.Exchange
	f := CallFactsV{Start: rfc(ex.StartedAt)}
	if in.Usage != nil {
		f.Model = in.Usage.Model
	}
	if ex.EndedAt > 0 {
		dur := ms(ex.EndedAt - ex.StartedAt)
		f.DurMS = &dur
	}
	switch {
	case p.side != nil:
		f.Thread = CallThread{Kind: "side", Label: p.side.Name}
		if p.side.Status != "ok" {
			f.Stop = CallStop{Word: p.side.Status} // a side call's failure is not red (E2)
		}
	case p.agent != "":
		f.Thread = CallThread{Kind: "sub", Label: "sub-agent " + p.agent}
	default:
		f.Thread = CallThread{Kind: "main", Label: "main"}
	}
	if p.step != nil && p.step.Errored {
		f.Stop = CallStop{Word: p.step.Status, Bad: true}
	}
	if f.Stop.Word == "" && in.Detail != nil && in.Detail.StopReason != "" {
		f.Stop.Word = words(in.Detail.StopReason)
	}
	return f
}

func callContext(v View, id string, p callPlace, in CallInput) CallContext {
	var c CallContext
	if u := in.Usage; u != nil {
		c.Input, c.CacheWrite, c.CacheRead, c.Output = u.Input, u.CacheWrite, u.CacheRead, u.Output
		c.Partial = u.Partial
		switch {
		case u.NoUsage == NoUsageErrored:
			c.Note = "No usage reported: the call failed before the provider counted tokens."
		case u.NoUsage == NoUsageMissing || !u.HasUsage:
			c.Note = "No usage reported: the stored reply has no usage counts."
		case u.Partial:
			c.Note = "Usage from a reply cut short: the counts may be low."
		}
	} else {
		c.Note = "No usage reported: the call's events were not parsed."
	}
	if p.side != nil {
		c.Size = sumCtx(c.Input, c.CacheWrite, c.CacheRead)
		c.Why = "a side call: a separate request, not part of a thread's context"
		return c
	}
	row, prev, ok := ctxRowOf(v, id)
	if !ok {
		c.Size = sumCtx(c.Input, c.CacheWrite, c.CacheRead)
		return c
	}
	c.Size, c.Delta, c.Why = row.Ctx, row.Delta, row.Why
	if prev == nil {
		c.First = true
	} else {
		c.PrevN = prev.N
	}
	return c
}

// sumCtx is input + cache write + cache read; nil when any is unreported.
func sumCtx(parts ...*int64) *int64 {
	var n int64
	for _, p := range parts {
		if p == nil {
			return nil
		}
		n += *p
	}
	return &n
}

func callHealth(ex store.ExchangeRow) CallHealth {
	h := CallHealth{Status: "—", Items: []HealthItem{}}
	if ex.Status > 0 {
		h.Status = statusWord(ex.Status, "")
	}
	add := func(tone, text string) { h.Items = append(h.Items, HealthItem{Tone: tone, Text: text}) }
	if ex.Status >= 400 {
		add("bad", "The provider answered "+h.Status+".")
	}
	if ex.GatewayError != "" {
		add("bad", "Gateway error: "+ex.GatewayError+".")
	}
	if ex.UpstreamAborted {
		add("bad", "The provider's reply stopped before it finished.")
	}
	if ex.ClientDisconnected {
		add("warn", "The client disconnected before the reply finished.")
	}
	if ex.RequestIncomplete {
		add("warn", "The request body was not received in full.")
	}
	if ex.RequestTruncated {
		add("warn", "The stored request is cut at the capture limit.")
	}
	if ex.ResponseTruncated {
		add("warn", "The stored reply is cut at the capture limit.")
	}
	switch ex.Parse {
	case "failed", "skipped", "unsupported_encoding":
		add("warn", "The stored bodies could not be parsed ("+words(ex.Parse)+").")
	}
	if ex.EndedAt == 0 {
		add("warn", "The call had not finished when it was stored.")
	}
	h.OK = len(h.Items) == 0
	return h
}

func callSaw(in CallInput) CallSaw {
	ex := in.Exchange
	s := CallSaw{Messages: []SawMessage{}}
	if f := in.Fact; f != nil && f.Thread >= 0 && f.PrevID == "" {
		s.First = true
	}
	if in.Detail == nil || len(in.Detail.Messages) == 0 {
		s.Unavailable = reasonWords(UnavailableReason(ex, "request", ex.RequestBody != "", ex.RequestTruncated))
		return s
	}
	msgs := in.Detail.Messages
	carried := 0
	if in.Fact != nil {
		// The carried count covers the reply too, which the next request resends; only the
		// request's own messages are listed.
		carried = min(in.Fact.Carried, len(msgs))
	}
	fresh := NewMessages(len(msgs), carried)
	s.Carried, s.New = &carried, &fresh
	for i, m := range msgs {
		s.Messages = append(s.Messages, SawMessage{Role: m.Role, Text: m.Text, Truncated: m.Truncated, New: i >= carried})
	}
	return s
}

func callReply(in CallInput) CallReply {
	ex := in.Exchange
	if in.Detail == nil || !in.Detail.HasResponse {
		return CallReply{Unavailable: reasonWords(UnavailableReason(ex, "response", ex.ResponseBody != "", ex.ResponseTruncated))}
	}
	return CallReply{Text: in.Detail.ResponseText, StopReason: words(in.Detail.StopReason)}
}

func callRaw(ex store.ExchangeRow) CallRaw {
	id := url.PathEscape(ex.RequestID)
	r := CallRaw{Headers: "#/exchanges/" + id, Page: "#/exchanges/" + id}
	if ex.RequestBody != "" {
		r.Request = "/api/exchanges/" + id + "/body?part=request"
	} else {
		r.RequestMissing = reasonWords("request_body_not_stored")
	}
	if ex.ResponseBody != "" {
		r.Response = "/api/exchanges/" + id + "/body?part=response"
	} else {
		r.ResponseMissing = reasonWords("response_body_not_stored")
	}
	return r
}
