package timeline

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"unsafe"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// Incremental session build (spec 040). The full build (buildFull) and State.Update share
// one function per rule: deriveExchange (what one exchange's own row and events give),
// placeExchange (where it sits among earlier exchanges), exchangeBody (its entries) and
// applyHooks (tool-call states and hook lines). Only the driver differs: the full build
// reads every exchange, Update reuses what the snapshot says has not changed. The
// incremental driver is what the oracle test in internal/ui checks against the full one.

// ErrMissingRow means a row the state holds is not in the snapshot (spec F2).
var ErrMissingRow = errors.New("timeline: a row of the state is missing from the snapshot")

// ErrLargeDelta means more than half of the exchanges would be read again (spec F5).
var ErrLargeDelta = errors.New("timeline: more than half of the exchanges changed")

// Estimated bytes of a map entry beyond its key and value, and of a slice or string header.
const (
	mapEntryBytes = 80
	headerBytes   = 16
)

// exchangeRec is what the full build derives from one exchange's own row and events,
// before any placement. It is immutable once made and shared between states.
type exchangeRec struct {
	row     store.ExchangeRow
	folded  bool     // shown as one line: no message, or a parse that was not kept
	hashes  []string // message hashes, interned; a hash-less message has a unique value
	reqMsgs int      // the request's own message count, the reply left out
	call    *CallUsage
	tools   []toolDatum // what the hook pass reads, see toolDataOf
	texts   []string    // user message texts (what CallFacts reads), for model calls
	size    int64
}

// foldedEntry is the one line of a folded exchange.
func (r *exchangeRec) foldedEntry() Entry {
	fe := folded(r.row)
	fe.Call = r.call
	return fe
}

// deriveExchange derives an exchange's record from its row and its events, which it reads
// through src. evs are the events of a non-folded exchange (nil of a folded one) for the
// caller to make entries from. withTexts also reads the user texts of a model call.
func deriveExchange(src Source, ex store.ExchangeRow, in *interner, withTexts bool) (*exchangeRec, []store.EventRow, error) {
	evs, err := loadEvents(src, ex)
	if err != nil {
		return nil, nil, err
	}
	rec := &exchangeRec{row: ex, hashes: messageHashes(evs, in), tools: toolDataOf(evs, in)}
	if len(rec.hashes) == 0 || foldedParse(ex.Parse) {
		rec.folded = true
		// A folded-parse exchange may still be a model call; its events are read only to
		// find the request event.
		callEvs := evs
		if foldedParse(ex.Parse) {
			if callEvs, err = src.Events(ex.RequestID); err != nil {
				return nil, nil, fmt.Errorf("timeline: events of %s: %w", ex.RequestID, err)
			}
		}
		rec.call = callOf(ex, callEvs)
		rec.measure()
		return rec, evs, nil
	}
	rec.reqMsgs = requestMessageCount(evs)
	rec.call = callOf(ex, evs)
	if withTexts && rec.call != nil {
		if rec.texts, err = UserTextsOf(src, evs); err != nil {
			return nil, nil, err
		}
	}
	rec.measure()
	return rec, evs, nil
}

func (r *exchangeRec) measure() {
	n := int64(unsafe.Sizeof(*r)) + rowSize(&r.row) + int64(len(r.hashes)+len(r.texts))*headerBytes +
		int64(len(r.tools))*int64(unsafe.Sizeof(toolDatum{}))
	for _, h := range r.hashes {
		if len(h) > 0 && h[0] == 0 { // a hash-less message's value is its own string
			n += int64(len(h))
		}
	}
	if r.call != nil {
		n += int64(unsafe.Sizeof(*r.call)) + int64(len(r.call.Model))
	}
	r.size = n
}

func rowSize(r *store.ExchangeRow) int64 {
	n := int64(unsafe.Sizeof(*r))
	for _, s := range []string{r.RequestID, r.PrincipalID, r.Protocol, r.Client, r.Auth, r.Method, r.Path, r.Query,
		r.GatewayError, r.RequestBody, r.ResponseBody, r.Parse, r.SessionKey} {
		n += int64(len(s))
	}
	for _, h := range []http.Header{r.RequestHeaders, r.ResponseHeaders} {
		for k, vs := range h {
			n += int64(len(k)) + mapEntryBytes + headerBytes
			for _, v := range vs {
				n += int64(len(v)) + headerBytes
			}
		}
	}
	return n
}

// placement is where an exchange sits among the earlier ones.
type placement struct {
	best     int // position of the earlier exchange it continues, -1 for a new thread
	tid      int
	prev     string
	shared   int  // leading messages it shares with that exchange
	diverged bool // the earlier exchange has more messages than are shared
}

// bodyKey is the part of a placement the entries' body depends on (spec D13); the rest of
// it only sets the header fields of the first entry.
func (p placement) bodyKey() [2]int {
	if p.diverged {
		return [2]int{p.shared, 1}
	}
	return [2]int{p.shared, 0}
}

// placeExchange finds the best earlier match of a non-folded exchange; threads counts the
// conversation threads started so far and grows when the exchange starts one.
func placeExchange(earlier []thread, threads *int, rec *exchangeRec) placement {
	shared, bi := bestMatchIdx(earlier, rec.hashes)
	pl := placement{best: -1, tid: *threads, shared: shared}
	if bi < 0 {
		*threads++
		return pl
	}
	b := earlier[bi]
	pl.best, pl.tid, pl.prev, pl.diverged = b.ex, b.id, b.reqID, len(b.hashes) > shared
	return pl
}

// setHeader sets the fields the placement and the record give an exchange's first entry.
func setHeader(en *Entry, rec *exchangeRec, pl placement) {
	en.Thread, en.PrevID = pl.tid, pl.prev
	en.Messages, en.Carried = len(rec.hashes), pl.shared
	en.RequestMessages = rec.reqMsgs
	en.Call = rec.call
}

// exchangeBody makes a non-folded exchange's entries, header included, before hook states.
func exchangeBody(src Source, rec *exchangeRec, evs []store.EventRow, pl placement, opts Options) ([]Entry, error) {
	entries, err := exchangeEntries(src, rec.row, evs, pl.shared, pl.diverged, opts)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].Index = i
	}
	setHeader(&entries[0], rec, pl)
	return entries, nil
}

// slot is one exchange of the state: its record, placement and entries before hook states.
// first is the exchange's first entry (the only one the placement's header fields touch);
// rest is the others, never changed once made and shared between states.
type slot struct {
	rec   *exchangeRec
	pl    placement
	first Entry
	rest  []Entry
	size  int64 // bytes of the entries, set by State.finish
}

// State is what an incremental update continues from: per exchange in timeline order its
// record, placement and entries, an append-only interner of hashes and ids, and the memos
// of repeated reads. An update never changes its base state's slots; the interner and the
// memo only grow, and are shared by every state of a session, so they are changed only
// inside the session's serialised build run (spec D15, D17).
type State struct {
	opts  Options
	slots []slot
	at    map[string]int // request id -> position in slots
	in    *interner
	memo  *Memo
	size  int64
}

// finish indexes the slots and sums the size estimate.
func (s *State) finish() {
	s.at = make(map[string]int, len(s.slots))
	n := int64(unsafe.Sizeof(*s))
	for i := range s.slots {
		sl := &s.slots[i]
		s.at[sl.rec.row.RequestID] = i
		if sl.size == 0 {
			sl.size = EntrySize(&sl.first) + EntriesSize(sl.rest)
		}
		n += sl.rec.size + sl.size + int64(unsafe.Sizeof(*sl)) + int64(len(sl.rec.row.RequestID)) + mapEntryBytes
	}
	s.size = n + s.in.bytes + s.memo.Size()
}

// Size is the state's estimated retained bytes: records, entries, interner and memos. It
// is a sum of sizes kept per record, not a walk over the content, so it is cheap.
func (s *State) Size() int64 { return s.size }

// Options are the options the state was built with.
func (s *State) Options() Options { return s.opts }

// Exchanges is how many exchanges the state holds.
func (s *State) Exchanges() int { return len(s.slots) }

// UserTexts is the user message texts of a request, as CallFacts reads them.
func (s *State) UserTexts(requestID string) ([]string, error) {
	i, ok := s.at[requestID]
	if !ok {
		return nil, nil
	}
	return s.slots[i].rec.texts, nil
}

func memoOf(s *State) *Memo {
	if s == nil {
		return nil
	}
	return s.memo
}

// UpdateStats says what an update did.
type UpdateStats struct {
	// Rederived is how many exchanges had their events read again.
	Rederived int
}

// Update returns the state and entries for the session's exchanges rows (a fresh snapshot,
// in timeline order), reading only what changed since s: it diffs rows against s by request
// id and row value, derives the changed and new exchanges from their own events, places
// every exchange from the first position where the order or a record changed, makes entries
// again only where the body key changed, and then runs the hook pass whole. s is not
// changed. ErrMissingRow and ErrLargeDelta ask the caller for a full build.
func (s *State) Update(src Source, session store.SessionRow, rows []store.ExchangeRow) (*State, []Entry, UpdateStats, error) {
	src = memoSource{src, s.memo}
	var stats UpdateStats

	// Diff the snapshot against the state.
	recs := make([]*exchangeRec, len(rows))
	matched, changed := 0, 0
	for j := range rows {
		i, ok := s.at[rows[j].RequestID]
		if ok {
			matched++
			if rowsEqual(&s.slots[i].rec.row, &rows[j]) {
				recs[j] = s.slots[i].rec
				continue
			}
		}
		changed++
	}
	if matched != len(s.slots) {
		return nil, nil, stats, ErrMissingRow
	}
	if 2*changed > len(rows) {
		return nil, nil, stats, ErrLargeDelta
	}
	evsOf := map[int][]store.EventRow{} // events of the exchanges derived now, to make entries from
	for j := range rows {
		if recs[j] != nil {
			continue
		}
		rec, evs, err := deriveExchange(src, rows[j], s.in, true)
		if err != nil {
			return nil, nil, stats, err
		}
		recs[j] = rec
		stats.Rederived++
		if !rec.folded {
			evsOf[j] = evs
		}
	}

	// The first position where the order or a record differs.
	p := 0
	for p < len(rows) && p < len(s.slots) && recs[p] == s.slots[p].rec {
		p++
	}

	next := &State{opts: s.opts, in: s.in, memo: s.memo, slots: make([]slot, len(rows))}
	copy(next.slots, s.slots[:min(p, len(s.slots))])
	var (
		earlier []thread
		threads int
	)
	for j := 0; j < p; j++ {
		sl := &next.slots[j]
		if sl.rec.folded {
			continue
		}
		earlier = append(earlier, thread{hashes: sl.rec.hashes, id: sl.pl.tid, reqID: sl.rec.row.RequestID, ex: j})
		if sl.pl.best < 0 {
			threads++
		}
	}
	// Place from p. An exchange whose body key is unchanged keeps its entries; the others
	// are listed to be made again.
	var remake []int
	for j := p; j < len(rows); j++ {
		rec := recs[j]
		if rec.folded {
			next.slots[j] = slot{rec: rec, first: rec.foldedEntry()}
			continue
		}
		pl := placeExchange(earlier, &threads, rec)
		earlier = append(earlier, thread{hashes: rec.hashes, id: pl.tid, reqID: rec.row.RequestID, ex: j})
		sl := slot{rec: rec, pl: pl}
		if i, ok := s.at[rec.row.RequestID]; ok && s.slots[i].rec == rec && s.slots[i].pl.bodyKey() == pl.bodyKey() {
			old := &s.slots[i]
			sl.first, sl.rest, sl.size = old.first, old.rest, old.size
			setHeader(&sl.first, rec, pl)
		} else {
			remake = append(remake, j)
		}
		next.slots[j] = sl
	}
	reread := stats.Rederived
	for _, j := range remake {
		if _, have := evsOf[j]; !have {
			reread++
		}
	}
	if 2*reread > len(rows) {
		return nil, nil, stats, ErrLargeDelta
	}
	stats.Rederived = reread
	for _, j := range remake {
		sl := &next.slots[j]
		evs, have := evsOf[j]
		if !have {
			var err error
			if evs, err = loadEvents(src, sl.rec.row); err != nil {
				return nil, nil, stats, err
			}
		}
		entries, err := exchangeBody(src, sl.rec, evs, sl.pl, s.opts)
		if err != nil {
			return nil, nil, stats, err
		}
		sl.first, sl.rest, sl.size = entries[0], entries[1:], 0
	}

	// The global passes, over a fresh concatenation: the hook pass writes entries in place.
	total := 0
	for i := range next.slots {
		total += 1 + len(next.slots[i].rest)
	}
	out := make([]Entry, 0, total)
	tools := make([][]toolDatum, len(next.slots))
	for i := range next.slots {
		sl := &next.slots[i]
		out = append(out, sl.first)
		out = append(out, sl.rest...)
		tools[i] = sl.rec.tools
	}
	hooks, err := sessionHooks(src, session)
	if err != nil {
		return nil, nil, stats, err
	}
	out, err = applyHooks(hooks, tools, out, readerOf(src), s.memo, s.opts)
	if err != nil {
		return nil, nil, stats, err
	}
	next.finish()
	return next, out, stats, nil
}

// rowsEqual reports whether two exchange rows are the same in every field. It is
// explicit, so that a field added to the row is a compile-visible decision; the test
// walks the row's fields by reflection to prove each one is compared.
func rowsEqual(a, b *store.ExchangeRow) bool {
	if a.RequestID != b.RequestID || a.PrincipalID != b.PrincipalID || a.Protocol != b.Protocol ||
		a.Client != b.Client || a.Auth != b.Auth || a.Method != b.Method || a.Path != b.Path ||
		a.Query != b.Query || a.Status != b.Status || a.StartedAt != b.StartedAt ||
		a.EndedAt != b.EndedAt || a.Stream != b.Stream ||
		a.RequestTruncated != b.RequestTruncated || a.ResponseTruncated != b.ResponseTruncated ||
		a.RequestIncomplete != b.RequestIncomplete || a.Truncated != b.Truncated ||
		a.ClientDisconnected != b.ClientDisconnected || a.UpstreamAborted != b.UpstreamAborted ||
		a.GatewayError != b.GatewayError || a.RequestBody != b.RequestBody || a.ResponseBody != b.ResponseBody ||
		a.Parse != b.Parse || a.SessionKey != b.SessionKey {
		return false
	}
	if (a.TTFBNS == nil) != (b.TTFBNS == nil) || (a.TTFBNS != nil && *a.TTFBNS != *b.TTFBNS) {
		return false
	}
	return headersEqual(a.RequestHeaders, b.RequestHeaders) && headersEqual(a.ResponseHeaders, b.ResponseHeaders)
}

func headersEqual(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || !slices.Equal(av, bv) {
			return false
		}
	}
	return true
}

// EntrySize estimates the bytes an entry holds: the struct and its strings.
func EntrySize(e *Entry) int64 {
	n := int64(unsafe.Sizeof(*e))
	for _, s := range []string{e.RequestID, e.Method, e.Path, e.Parse, e.Role, e.Name, e.ToolCallID, e.Text,
		e.Thinking, e.State, e.Reason, e.Outcome, e.HookReason, e.HookOutcome, e.Chip, e.HookInput, e.Phase,
		e.AgentID, e.AgentType, e.PrevID} {
		n += int64(len(s))
	}
	return n
}

// EntriesSize is EntrySize summed over a list.
func EntriesSize(es []Entry) int64 {
	var n int64
	for i := range es {
		n += EntrySize(&es[i])
	}
	return n
}

// memoSource serves a build's text memo from the state's Memo, whatever src is.
type memoSource struct {
	Source
	memo *Memo
}

func (m memoSource) MemoText(hash, role string) (string, bool, bool) {
	return m.memo.MemoText(hash, role)
}
func (m memoSource) PutText(hash, role, text string, ok bool) { m.memo.PutText(hash, role, text, ok) }

type textKey struct{ hash, role string }

type memoText struct {
	text string
	ok   bool
}

// cmpKey identifies a hook input compared with a captured call input, under one cut.
type cmpKey struct {
	pre, call string
	limit     int
	full      bool
}

type inputDiffs struct {
	differs   bool
	hookInput string
	omitted   int
}

// Memo keeps what a session's builds read more than once: the text of a message by content
// hash and role, and the comparison of a hook's input with a call's by the two hashes and
// the cut. Both are functions of content hashes, so a value never goes stale. A Memo is
// used by one build at a time and has no lock. Its size counts in the state's estimate.
type Memo struct {
	texts  map[textKey]memoText
	cmp    map[cmpKey]inputDiffs
	size   int64
	noText bool // the text memo is bypassed (the equivalence test's reference build)
}

// NewMemo returns an empty memo.
func NewMemo() *Memo { return &Memo{texts: map[textKey]memoText{}, cmp: map[cmpKey]inputDiffs{}} }

// NewMemoWithoutText returns a memo whose text memo is bypassed: it never finds and never
// stores a text. The equivalence test builds its memo-free reference with it.
func NewMemoWithoutText() *Memo {
	m := NewMemo()
	m.noText = true
	return m
}

// MemoText implements TextMemo.
func (m *Memo) MemoText(hash, role string) (string, bool, bool) {
	if m.noText {
		return "", false, false
	}
	t, found := m.texts[textKey{hash, role}]
	return t.text, t.ok, found
}

// PutText implements TextMemo.
func (m *Memo) PutText(hash, role, text string, ok bool) {
	if m.noText {
		return
	}
	k := textKey{hash, role}
	if _, have := m.texts[k]; have {
		return
	}
	m.texts[k] = memoText{text, ok}
	m.size += int64(len(hash)+len(role)+len(text)) + mapEntryBytes + 2*headerBytes
}

func (m *Memo) cmpGet(k cmpKey) (inputDiffs, bool) {
	if m == nil {
		return inputDiffs{}, false
	}
	d, ok := m.cmp[k]
	return d, ok
}

func (m *Memo) cmpPut(k cmpKey, d inputDiffs) {
	if m == nil {
		return
	}
	if _, have := m.cmp[k]; have {
		return
	}
	m.cmp[k] = d
	m.size += int64(len(k.pre)+len(k.call)+len(d.hookInput)) + mapEntryBytes + 3*headerBytes
}

// Size is the memo's estimated bytes.
func (m *Memo) Size() int64 {
	if m == nil {
		return 0
	}
	return m.size
}
