package ui

import (
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// builtSession is one build of a session's timeline: the entries, the per-call facts
// and the exchange rows they came from.
type builtSession struct {
	entries []timeline.Entry
	facts   map[string]*callFactsJSON
	byID    map[string]store.ExchangeRow
	rowID   map[string]int64
	rows    int
}

// eventsOnce serves each request's events from the first read, so the timeline build
// and the call facts share one load.
type eventsOnce struct {
	timeline.Source
	exs    []store.ExchangeRow
	events map[string][]store.EventRow
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
	src := &eventsOnce{Source: source, exs: exs, events: map[string][]store.EventRow{}}
	session := store.SessionRow{PrincipalID: who.principal, Client: who.client, SessionKey: who.name}
	entries, _, err := timeline.BuildWithUsage(src, session, timeline.Options{Cut: cut})
	if err != nil {
		return builtSession{}, err
	}
	facts, err := callFacts(src, entries, rules, src.events)
	if err != nil {
		return builtSession{}, err
	}
	return builtSession{entries: entries, facts: facts, byID: byID, rows: len(exs)}, nil
}
