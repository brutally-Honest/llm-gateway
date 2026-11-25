package ui

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// builtSession is one build of a session's timeline: the entries, the per-call facts
// and the exchange rows they came from.
type builtSession struct {
	// kind says whether this build was made incrementally; how, how it was made (spec 040).
	kind buildKind
	how  buildInfo
	// state is what the next incremental build continues from; nil when none was kept.
	// rules and cut are what it was built with (F3). tooLarge marks a full build that
	// dropped its state for size (F6).
	state    *timeline.State
	rules    ClientRules
	cut      int
	tooLarge bool

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

// snapshotSource serves a stateful full build: the snapshot's exchanges, and events straight
// from the reader. That build reads no exchange's events twice (call-fact texts come from the
// state), so nothing is cached and a large session's events are not held until the build ends.
type snapshotSource struct {
	timeline.Source
	exs []store.ExchangeRow
}

func (s snapshotSource) SessionExchanges(string, string, string) ([]store.ExchangeRow, error) {
	return s.exs, nil
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

// buildSessionFn is how every caller builds a session in full; tests replace it with a slow
// build. Callers go through buildSessionVia, which continues from a cached build when it can.
var buildSessionFn = buildSession

// stateCap is the most estimated bytes of incremental state kept for one session (spec D4,
// F6); a larger one is dropped and every update is a full build. Tests lower it.
var stateCap int64 = 128 << 20

// stateUpdate is the incremental update; tests replace it to make it fail or panic.
var stateUpdate = (*timeline.State).Update

// buildKind says how a build was made.
type buildKind uint8

const (
	buildFull buildKind = iota
	buildIncremental
)

// The fallback reasons of buildInfo.Reason (spec 040 F1-F6).
const (
	reasonNoState    = "no_state"
	reasonMissingRow = "missing_row"
	reasonOptions    = "options"
	reasonFailed     = "failed"
	reasonLargeDelta = "large_delta"
	reasonTooLarge   = "too_large"
)

// buildSession reads a session's exchanges in one snapshot and builds it in full, keeping
// the incremental state unless it is over its cap.
func buildSession(c sessionSource, who sessIdent, rules ClientRules, cut int) (builtSession, error) {
	rows, _, err := c.SessionSnapshot(who.principal, who.client, who.name)
	if err != nil {
		return builtSession{}, err
	}
	return buildSessionRows(c, who, rows, rules, cut)
}

// buildSessionRows is buildSession from rows already read.
func buildSessionRows(c sessionSource, who sessIdent, rows []store.RowExchange, rules ClientRules, cut int) (builtSession, error) {
	exs := make([]store.ExchangeRow, len(rows))
	rowID := make(map[string]int64, len(rows))
	for i, x := range rows {
		exs[i] = x.ExchangeRow
		rowID[x.RequestID] = x.RowID
	}
	byID := make(map[string]store.ExchangeRow, len(exs))
	for _, x := range exs {
		byID[x.RequestID] = x
	}
	src := snapshotSource{Source: c, exs: exs}
	session := store.SessionRow{PrincipalID: who.principal, Client: who.client, SessionKey: who.name}
	memo := timeline.NewMemo()
	if textMemoOff {
		memo = timeline.NewMemoWithoutText()
	}
	entries, _, st, err := timeline.BuildWithState(src, session, timeline.Options{Cut: cut}, memo)
	if err != nil {
		return builtSession{}, err
	}
	facts, calls, err := callFactsFrom(entries, rules, st.UserTexts)
	if err != nil {
		return builtSession{}, err
	}
	bs := builtSession{entries: entries, facts: facts, calls: calls, byID: byID, rows: len(exs), rowID: rowID,
		state: st, rules: rules, cut: cut}
	if stateEstimate(st) > stateCap {
		bs.state, bs.tooLarge = nil, true
	}
	return bs, nil
}

// buildFrom builds the timeline of exs, loading each exchange's events once for both
// the entries and the call facts. It keeps no incremental state.
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
	// Err is why an incremental update failed (reason "failed"); the caller logs it.
	Err error
}

// buildSessionVia is the one function every caller builds a session with: incrementally
// from prev, the session's cached build, when it holds state, else in full through
// buildSessionFn.
func buildSessionVia(c sessionSource, who sessIdent, rules ClientRules, cut int, prev *cachedBuild) (builtSession, error) {
	if prev != nil && (prev.bs.state != nil || prev.bs.tooLarge) {
		bs, _, err := buildSessionInc(c, who, rules, cut, &prev.bs)
		return bs, err
	}
	bs, err := buildSessionFn(c, who, rules, cut)
	bs.kind, bs.how = buildFull, buildInfo{Reason: reasonNoState}
	return bs, err
}

// sameRules reports whether two client rules are the same value; rules that cannot be
// compared are never the same.
func sameRules(a, b ClientRules) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	return ta == tb && ta.Comparable() && a == b
}

// buildSessionInc builds a session's timeline from prev, the session's previous build (nil
// for none), reading only what changed since (spec 040). It falls back to a full build,
// which returns the reason in buildInfo, when prev cannot be continued: F1-F6.
func buildSessionInc(c sessionSource, who sessIdent, rules ClientRules, cut int, prev *builtSession) (builtSession, buildInfo, error) {
	full := func(reason string, cause error, rows []store.RowExchange) (builtSession, buildInfo, error) {
		var (
			bs  builtSession
			err error
		)
		if rows != nil {
			bs, err = buildSessionRows(c, who, rows, rules, cut)
		} else {
			bs, err = buildSession(c, who, rules, cut)
		}
		info := buildInfo{Reason: reason, Err: cause}
		bs.kind, bs.how = buildFull, info
		return bs, info, err
	}
	switch {
	case prev == nil || (prev.state == nil && !prev.tooLarge):
		return full(reasonNoState, nil, nil)
	case prev.tooLarge:
		return full(reasonTooLarge, nil, nil)
	case prev.cut != cut || !sameRules(prev.rules, rules):
		return full(reasonOptions, nil, nil)
	}
	rows, _, err := c.SessionSnapshot(who.principal, who.client, who.name)
	if err != nil {
		return builtSession{}, buildInfo{}, err
	}
	bs, stats, err := incrementalUpdate(c, who, rules, cut, prev, rows)
	switch {
	case err == nil:
		if stateEstimate(bs.state) > stateCap {
			return full(reasonTooLarge, nil, rows)
		}
		info := buildInfo{Incremental: true, Rederived: stats.Rederived}
		bs.kind, bs.how = buildIncremental, info
		return bs, info, nil
	case errors.Is(err, timeline.ErrMissingRow):
		return full(reasonMissingRow, err, rows)
	case errors.Is(err, timeline.ErrLargeDelta):
		return full(reasonLargeDelta, err, rows)
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return builtSession{}, buildInfo{}, err
	}
	return full(reasonFailed, err, rows)
}

// incrementalUpdate continues prev's state with a fresh snapshot. A panic is returned as an
// error, so the caller falls back to a full build.
func incrementalUpdate(c sessionSource, who sessIdent, rules ClientRules, cut int, prev *builtSession, rows []store.RowExchange) (bs builtSession, stats timeline.UpdateStats, err error) {
	defer func() {
		if p := recover(); p != nil {
			bs, err = builtSession{}, fmt.Errorf("ui: incremental build panicked: %v", p)
		}
	}()
	exs := make([]store.ExchangeRow, len(rows))
	rowID := make(map[string]int64, len(rows))
	byID := make(map[string]store.ExchangeRow, len(rows))
	for i, x := range rows {
		exs[i] = x.ExchangeRow
		rowID[x.RequestID] = x.RowID
		byID[x.RequestID] = x.ExchangeRow
	}
	session := store.SessionRow{PrincipalID: who.principal, Client: who.client, SessionKey: who.name}
	st, entries, stats, err := stateUpdate(prev.state, c, session, exs)
	if err != nil {
		return builtSession{}, stats, err
	}
	facts, calls, err := callFactsFrom(entries, rules, st.UserTexts)
	if err != nil {
		return builtSession{}, stats, err
	}
	return builtSession{entries: entries, facts: facts, calls: calls, byID: byID, rowID: rowID, rows: len(exs),
		state: st, rules: rules, cut: cut}, stats, nil
}
