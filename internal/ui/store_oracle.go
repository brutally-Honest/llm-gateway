//go:build storeoracle

package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// The store oracle of spec 040 (AC3): every session of a real store copy is replayed, one
// stored input at a time in capture order, through the incremental path (from the previous
// build) and, at chosen steps, the full path; the entries, call facts, exchange map, row ids
// and the session view built from each must be byte-for-byte equal. The copy is opened
// read-only; what a step shows is a time-travel view over the session's stored rows.

// StoreOracleDifference is one difference between the incremental and the full build.
type StoreOracleDifference struct {
	Session   string
	Exchanges int
	Step      int
	Input     string
	Detail    string
}

// StoreOracleReport is what a store oracle run did.
type StoreOracleReport struct {
	Sessions        int
	Inputs          int
	FullComparisons int
	Differences     []StoreOracleDifference
	// Builds counts the incremental builds ("incremental") and the full ones ("full:<reason>").
	Builds map[string]int
	// UnassignedPassthrough is true: the unassigned connections a view names are read as
	// stored, for both builds alike, whatever the step.
	UnassignedPassthrough bool
	// Largest is the exchange count of the largest session.
	Largest int
}

// maxFallbackPercent is the share of builds that may be full for a reason other than a
// session's first (no_state) before the replay counts as failed.
const maxFallbackPercent = 5

const (
	// oracleEveryInput is the largest session (in exchanges) compared in full after every input.
	oracleEveryInput = 150
	// oracleSparseStride and oracleSparseTail pick the full comparisons of a larger session:
	// every stride-th input, every input among the last tail, and the final state.
	oracleSparseStride = 20
	oracleSparseTail   = 40
)

// oracleInput is one stored input of a session.
type oracleInput struct {
	at    int64
	rank  int   // exchange 0, hook 1, effect 2
	id    int64 // exchange rowid, hook id, effect id
	sub   int   // 0 the row, 1 its parse
	desc  string
	apply func(*timeTravel)
}

// timeTravel is a session's data shown as it was after some inputs: it is what a build reads.
type timeTravel struct {
	r       *store.CtxReader
	who     sessIdent
	exs     []store.RowExchange // all, in snapshot order
	exState []uint8             // 0 absent, 1 row, 2 parsed
	hooks   []store.HookRow
	hookOn  []bool
	effs    []store.EffectRow
	effOn   []bool

	mu       sync.Mutex
	events   map[string][]store.EventRow
	contents map[string][]byte
	cBytes   int
}

const oracleContentCap = 256 << 20

func (t *timeTravel) SessionSnapshot(_, _, _ string) ([]store.RowExchange, int64, error) {
	var out []store.RowExchange
	var max int64
	for i, x := range t.exs {
		if t.exState[i] == 0 {
			continue
		}
		if t.exState[i] == 1 {
			x.Parse = ""
		}
		out = append(out, x)
		max = maxInt64(max, x.RowID)
	}
	return out, max, nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (t *timeTravel) SessionExchanges(string, string, string) ([]store.ExchangeRow, error) {
	rows, _, _ := t.SessionSnapshot("", "", "")
	out := make([]store.ExchangeRow, len(rows))
	for i, x := range rows {
		out[i] = x.ExchangeRow
	}
	return out, nil
}

// Events shows none for an exchange whose parse has not landed.
func (t *timeTravel) Events(id string) ([]store.EventRow, error) {
	for i, x := range t.exs {
		if x.RequestID == id {
			if t.exState[i] < 2 {
				return nil, nil
			}
			break
		}
	}
	t.mu.Lock()
	evs, ok := t.events[id]
	t.mu.Unlock()
	if ok {
		return evs, nil
	}
	evs, err := t.r.Events(id)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.events[id] = evs
	t.mu.Unlock()
	return evs, nil
}

func (t *timeTravel) Content(hash string) ([]byte, error) {
	t.mu.Lock()
	b, ok := t.contents[hash]
	t.mu.Unlock()
	if ok {
		return b, nil
	}
	b, err := t.r.Content(hash)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	if t.cBytes+len(b) <= oracleContentCap {
		t.contents[hash] = b
		t.cBytes += len(b)
	}
	t.mu.Unlock()
	return b, nil
}

func (t *timeTravel) HookEvents(_, _, _ string) ([]store.HookRow, error) {
	var out []store.HookRow
	for i, h := range t.hooks {
		if t.hookOn[i] {
			out = append(out, h)
		}
	}
	return out, nil
}

func (t *timeTravel) SessionEffects(store.SessionIdent) ([]store.EffectRow, error) {
	var out []store.EffectRow
	for i, e := range t.effs {
		if t.effOn[i] {
			out = append(out, e)
		}
	}
	return out, nil
}

func (t *timeTravel) UnassignedConnsBetween(from, to int64, limit int) ([]store.EffectRow, error) {
	return t.r.UnassignedConnsBetween(from, to, limit)
}

// inputs lists the session's inputs in capture order.
func (t *timeTravel) inputs() []oracleInput {
	var in []oracleInput
	for i, x := range t.exs {
		at := x.EndedAt
		if at == 0 {
			at = x.StartedAt
		}
		i := i
		in = append(in, oracleInput{at: at, rank: 0, id: x.RowID, sub: 0, desc: "exchange row " + x.RequestID,
			apply: func(t *timeTravel) { t.exState[i] = 1 }})
		if x.Parse != "" {
			in = append(in, oracleInput{at: at, rank: 0, id: x.RowID, sub: 1, desc: "exchange parse " + x.RequestID + " (" + x.Parse + ")",
				apply: func(t *timeTravel) { t.exState[i] = 2 }})
		}
	}
	for i, h := range t.hooks {
		i := i
		in = append(in, oracleInput{at: h.ReceivedAt, rank: 1, id: h.ID, desc: fmt.Sprintf("hook %d %s", h.ID, h.Phase),
			apply: func(t *timeTravel) { t.hookOn[i] = true }})
	}
	for i, e := range t.effs {
		i := i
		at := e.At
		if e.Kind == "net_conn" && e.EndedAt > 0 {
			at = e.EndedAt
		}
		in = append(in, oracleInput{at: at, rank: 2, id: e.ID, desc: fmt.Sprintf("effect %d %s", e.ID, e.Kind),
			apply: func(t *timeTravel) { t.effOn[i] = true }})
	}
	sort.SliceStable(in, func(a, b int) bool {
		x, y := in[a], in[b]
		if x.at != y.at {
			return x.at < y.at
		}
		if x.rank != y.rank {
			return x.rank < y.rank
		}
		if x.id != y.id {
			return x.id < y.id
		}
		return x.sub < y.sub
	})
	return in
}

// RunStoreOracle replays every session of the store in dir and reports what it found. It
// opens the store read-only and writes nothing.
func RunStoreOracle(dir string, rules map[string]ClientRules, logf func(string, ...any)) (StoreOracleReport, error) {
	rep := StoreOracleReport{Builds: map[string]int{}, UnassignedPassthrough: true}
	r, err := store.OpenReader(dir)
	if err != nil {
		return rep, err
	}
	defer func() { _ = r.Close() }()
	ctx := context.Background()
	c := r.WithContext(ctx)
	sessions, err := r.SessionRows(0)
	if err != nil {
		return rep, err
	}
	vd := viewDeps{rules: rules, skew: 50 * time.Millisecond}
	logf("store oracle: %d sessions", len(sessions))
	for si, sr := range sessions {
		who := sessIdent{sr.PrincipalID, sr.Client, sr.SessionKey}
		rs := rules[who.client]
		if err := replaySession(c, vd, who, rs, &rep, logf, si, len(sessions)); err != nil {
			return rep, fmt.Errorf("session %s: %w", sr.SessionKey, err)
		}
	}
	// A replay that fell back to full builds proves little about the incremental path: each
	// session's first build is a legitimate no_state, anything else counts against it.
	all := 0
	for _, n := range rep.Builds {
		all += n
	}
	if fallbacks := all - rep.Builds["incremental"] - rep.Builds["full:no_state"]; all > 0 && fallbacks*100 > all*maxFallbackPercent {
		return rep, fmt.Errorf("fallback rate too high: %d of %d builds were full for a reason other than no_state (limit %d%%): %v",
			fallbacks, all, maxFallbackPercent, rep.Builds)
	}
	return rep, nil
}

func replaySession(c *store.CtxReader, vd viewDeps, who sessIdent, rules ClientRules, rep *StoreOracleReport, logf func(string, ...any), si, total int) error {
	exs, _, err := c.SessionSnapshot(who.principal, who.client, who.name)
	if err != nil {
		return err
	}
	hooks, err := c.HookEvents(who.principal, who.client, who.name)
	if err != nil {
		return err
	}
	effs, err := c.SessionEffects(store.SessionIdent{Principal: who.principal, Client: who.client, Key: who.name})
	if err != nil {
		return err
	}
	tt := &timeTravel{r: c, who: who, exs: exs, exState: make([]uint8, len(exs)), hooks: hooks, hookOn: make([]bool, len(hooks)),
		effs: effs, effOn: make([]bool, len(effs)), events: map[string][]store.EventRow{}, contents: map[string][]byte{}}
	inputs := tt.inputs()
	rep.Sessions++
	rep.Largest = max(rep.Largest, len(exs))
	started := time.Now()
	var prev *builtSession
	for i, in := range inputs {
		in.apply(tt)
		rep.Inputs++
		inc, info, err := buildSessionInc(tt, who, rules, entryCut, prev)
		if err != nil {
			return fmt.Errorf("step %d (%s): incremental build: %w", i, in.desc, err)
		}
		if info.Incremental {
			rep.Builds["incremental"]++
		} else {
			rep.Builds["full:"+info.Reason]++
		}
		prev = &inc
		last := i == len(inputs)-1
		if len(exs) > oracleEveryInput && !last && i%oracleSparseStride != 0 && i < len(inputs)-oracleSparseTail {
			continue
		}
		rep.FullComparisons++
		d, err := compareBuilds(tt, vd, who, rules, inc)
		if err != nil {
			return fmt.Errorf("step %d (%s): %w", i, in.desc, err)
		}
		if d != "" {
			rep.Differences = append(rep.Differences, StoreOracleDifference{Session: who.name, Exchanges: len(exs), Step: i, Input: in.desc, Detail: d})
			logf("DIFFERENCE session %s (%d exchanges) step %d/%d %s:\n%s", who.name, len(exs), i, len(inputs), in.desc, d)
			return nil
		}
	}
	logf("session %d/%d %s: %d exchanges, %d inputs, %s", si+1, total, who.name, len(exs), len(inputs), time.Since(started).Round(time.Millisecond))
	return nil
}

// compareBuilds builds the session in full and compares it, and the views of both, with inc.
func compareBuilds(tt *timeTravel, vd viewDeps, who sessIdent, rules ClientRules, inc builtSession) (string, error) {
	full, err := buildSession(tt, who, rules, entryCut)
	if err != nil {
		return "", fmt.Errorf("full build: %w", err)
	}
	for _, d := range []string{
		storeEntriesDiff(inc, full),
		storeDiff("facts", inc.facts, full.facts),
		storeDiff("calls", inc.calls, full.calls),
		storeDiff("byID", inc.byID, full.byID),
		storeDiff("rowID", inc.rowID, full.rowID),
		storeDiff("rows", inc.rows, full.rows),
	} {
		if d != "" {
			return d, nil
		}
	}
	hooks, err := tt.HookEvents(who.principal, who.client, who.name)
	if err != nil {
		return "", err
	}
	vInc, err := vd.sessionView(tt, who, inc, hooks, false, "v")
	if err != nil {
		return "", fmt.Errorf("view of incremental build: %w", err)
	}
	vFull, err := vd.sessionView(tt, who, full, hooks, false, "v")
	if err != nil {
		return "", fmt.Errorf("view of full build: %w", err)
	}
	return storeDiff("view", vInc, vFull), nil
}

func storeEntriesDiff(inc, full builtSession) string {
	for i := 0; i < len(inc.entries) && i < len(full.entries); i++ {
		e := full.entries[i]
		if d := storeDiff(fmt.Sprintf("entries[%d] (kind %s, request %s)", i, e.Kind, e.RequestID), inc.entries[i], e); d != "" {
			return d
		}
	}
	if len(inc.entries) != len(full.entries) {
		return fmt.Sprintf("entries: incremental has %d, full has %d", len(inc.entries), len(full.entries))
	}
	return ""
}

// storeDiff describes the first difference between two JSON encodings; "" when equal.
func storeDiff(what string, a, b any) string {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return fmt.Sprintf("%s: marshal: %v / %v", what, errA, errB)
	}
	if bytes.Equal(ja, jb) {
		return ""
	}
	i := 0
	for i < len(ja) && i < len(jb) && ja[i] == jb[i] {
		i++
	}
	around := func(j []byte) string { return string(j[max(i-200, 0):min(i+200, len(j))]) }
	return fmt.Sprintf("%s differ at byte %d (incremental %d bytes, full %d bytes)\n  incremental: ...%s...\n  full:        ...%s...",
		what, i, len(ja), len(jb), around(ja), around(jb))
}
