package ui

import (
	"errors"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// builtSession is one build of a session's timeline: the entries, the per-call facts
// and the exchange rows they came from.
type builtSession struct {
	entries []timeline.Entry
	facts   map[string]*callFactsJSON
	// calls is the timeline's own facts of each model call, which the session view reads.
	calls map[string]*timeline.CallFact
	byID  map[string]store.ExchangeRow
	rowID map[string]int64
	rows  int
}

// eventsOnce serves each request's events from the first read, so the timeline build
// and the call facts share one load.
type eventsOnce struct {
	timeline.Source
	exs    []store.ExchangeRow
	events map[string][]store.EventRow
	texts  map[textKey]memoText
}

// textMemoOff turns the text memo off; the equivalence test sets it to build the
// reference output.
var textMemoOff bool

// textKey and memoText are one entry of the build's timeline.TextMemo.
type textKey struct{ hash, role string }

type memoText struct {
	text string
	ok   bool
}

// MemoText and PutText make eventsOnce a timeline.TextMemo: a build reads the text of
// each message hash once, although every request resends the history.
func (s *eventsOnce) MemoText(hash, role string) (string, bool, bool) {
	if textMemoOff {
		return "", false, false
	}
	m, found := s.texts[textKey{hash, role}]
	return m.text, m.ok, found
}

func (s *eventsOnce) PutText(hash, role, text string, ok bool) {
	if textMemoOff {
		return
	}
	if s.texts == nil {
		s.texts = map[textKey]memoText{}
	}
	s.texts[textKey{hash, role}] = memoText{text, ok}
}

// sameExchanges reports whether a and b are the same slice.
func sameExchanges(a, b []store.ExchangeRow) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

func (s *eventsOnce) SessionExchanges(string, string, string) ([]store.ExchangeRow, error) {
	return s.exs, nil
}

func (s *eventsOnce) Events(id string) ([]store.EventRow, error) {
	if evs, ok := s.events[id]; ok {
		return evs, nil
	}
	evs, err := s.Source.Events(id)
	if err != nil {
		return nil, err
	}
	s.events[id] = evs
	return evs, nil
}

// sessionSource is what a session build reads: the timeline's events and content, and
// the one-snapshot read of the session's exchanges. *store.CtxReader is one.
type sessionSource interface {
	timeline.Source
	SessionSnapshot(principal, client, key string) ([]store.RowExchange, int64, error)
}

// buildSessionFn is how every caller builds a session; tests replace it with a slow build.
var buildSessionFn = buildSession

// buildSession reads a session's exchanges in one snapshot and builds it.
func buildSession(c sessionSource, who sessIdent, rules ClientRules, cut int) (builtSession, error) {
	rows, _, err := c.SessionSnapshot(who.principal, who.client, who.name)
	if err != nil {
		return builtSession{}, err
	}
	exs := make([]store.ExchangeRow, len(rows))
	rowID := make(map[string]int64, len(rows))
	for i, x := range rows {
		exs[i] = x.ExchangeRow
		rowID[x.RequestID] = x.RowID
	}
	bs, err := buildFrom(c, who, exs, rules, cut)
	bs.rowID = rowID
	return bs, err
}

// buildFrom builds the timeline of exs, loading each exchange's events once for both
// the entries and the call facts.
func buildFrom(source timeline.Source, who sessIdent, exs []store.ExchangeRow, rules ClientRules, cut int) (builtSession, error) {
	byID := make(map[string]store.ExchangeRow, len(exs))
	for _, x := range exs {
		byID[x.RequestID] = x
	}
	src, ok := source.(*eventsOnce)
	if !ok || !sameExchanges(src.exs, exs) {
		src = &eventsOnce{Source: source, exs: exs, events: map[string][]store.EventRow{}}
	}
	session := store.SessionRow{PrincipalID: who.principal, Client: who.client, SessionKey: who.name}
	entries, _, err := timeline.BuildWithUsage(src, session, timeline.Options{Cut: cut})
	if err != nil {
		return builtSession{}, err
	}
	facts, calls, err := callFactsBoth(src, entries, rules, src.events)
	if err != nil {
		return builtSession{}, err
	}
	return builtSession{entries: entries, facts: facts, calls: calls, byID: byID, rows: len(exs)}, nil
}

// buildInfo says how a build was made: incremental, or full with the fallback reason (spec 040 F1-F6).
type buildInfo struct {
	Incremental bool
	Reason      string // "" when incremental; else "no_state", "missing_row", "options", "failed", "large_delta", "too_large"
	Rederived   int    // exchanges whose events were read again
}

// errIncrementalNotBuilt is returned until the incremental build lands (spec 040 step 4).
var errIncrementalNotBuilt = errors.New("ui: incremental build not built yet")

// buildSessionInc builds a session's timeline from prev, the session's previous build (nil
// for none), reading only what changed since (spec 040). It is a stub until step 4.
func buildSessionInc(c sessionSource, who sessIdent, rules ClientRules, cut int, prev *builtSession) (builtSession, buildInfo, error) {
	return builtSession{}, buildInfo{}, errIncrementalNotBuilt
}
