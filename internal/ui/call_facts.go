package ui

import (
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// callFactsJSON is what the browser needs to place a model call: the call it continues,
// whether the client's rules call it a side call, and whether it is a main-thread call.
// Carried and New split the request's messages into those shared with PrevID and the
// rest. A call that was folded (its events were not parsed) has thread -1 and is never
// main.
//
// Nothing on the wire may depend on a call's position among other calls, or one late
// commit would change, and so resend, every later exchange. So the call's number is not
// sent: the browser numbers calls from the entry's start and exchange id. Thread is kept
// off the wire for the same reason, since a late call that starts a thread renumbers the
// threads after it.
type callFactsJSON struct {
	Thread   int    `json:"-"`
	PrevID   string `json:"prev_id"`
	SideCall bool   `json:"side_call"`
	Main     bool   `json:"main"`
	Carried  int    `json:"carried"`
	New      int    `json:"new"`
}

// callFacts classifies every model call of a session, keyed by request id, by the
// timeline's one rule (timeline.CallFacts). A nil rules means no side calls, and prompts
// are taken whole. evs holds the events already loaded for each request.
func callFacts(src timeline.Source, built []timeline.Entry, rules ClientRules, evs map[string][]store.EventRow) (map[string]*callFactsJSON, error) {
	facts, _, err := callFactsBoth(src, built, rules, evs)
	return facts, err
}

// callFactsBoth is callFacts that also returns the timeline's own facts, which the session
// view reads.
func callFactsBoth(src timeline.Source, built []timeline.Entry, rules ClientRules, evs map[string][]store.EventRow) (map[string]*callFactsJSON, map[string]*timeline.CallFact, error) {
	var fr timeline.FactRules
	if rules != nil {
		fr = rules
	}
	raw, err := timeline.CallFacts(src, built, fr, evs)
	if err != nil {
		return nil, nil, err
	}
	out := make(map[string]*callFactsJSON, len(raw))
	for id, f := range raw {
		out[id] = &callFactsJSON{Thread: f.Thread, PrevID: f.PrevID, SideCall: f.SideCall, Main: f.Main, Carried: f.Carried, New: f.New}
	}
	return out, raw, nil
}
