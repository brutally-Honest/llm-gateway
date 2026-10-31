package ui

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

const (
	// subscriberBuffer is the live messages a subscriber may hold; a full buffer drops it (AC29).
	subscriberBuffer = 64
	// maxStreams is the most SSE streams open at once (AC30).
	maxStreams = 32
	// hubQueueSize is the commits the hub holds for its dispatcher before it overflows.
	hubQueueSize = 1024
	// heartbeatEvery is the longest an open stream stays silent (AC26).
	heartbeatEvery = 15 * time.Second
)

var (
	errTooManyStreams = errors.New("ui: too many streams")
	errHubClosed      = errors.New("ui: hub closed")
)

// sessIdent is a session's identity.
type sessIdent struct{ principal, client, name string }

func (s sessIdent) id() string {
	return sessionID(store.SessionRow{PrincipalID: s.principal, Client: s.client, SessionKey: s.name})
}

// liveExchange is one committed exchange: its store row id and full timeline entry list.
type liveExchange struct {
	RowID     int64
	RequestID string
	StartedAt int64
	Hash      string // of the marshalled entry list; a stream compares it with what it last sent
	Entries   []entryJSON
}

// liveMsg is what a subscriber receives. It is shared between subscribers and never
// changed after it is built. A global stream gets Summary; a session stream gets Exchanges.
type liveMsg struct {
	Summary   *sessionSummary
	Exchanges []liveExchange // the committed exchanges, in row id order
	All       []liveExchange // every exchange of the session, in timeline order
	Inactive  bool           // the session's last exchange started over ActiveWindow ago (E21)
}

// readFn reads what a batch of commits to one session needs: the summary and/or the
// entries of the exchanges with these request ids, in row id order.
type readFn func(ctx context.Context, who sessIdent, ids []string, summary, entries bool) (liveMsg, error)

// subscriber is one open stream. Its buffer is the only way the hub talks to it.
type subscriber struct {
	session string // "" for the global stream
	ch      chan liveMsg
	gone    chan struct{}
	once    sync.Once
}

func (s *subscriber) drop() { s.once.Do(func() { close(s.gone) }) }

// Hub fans committed exchanges out to open streams. Publish never blocks.
type Hub struct {
	log       *zap.Logger
	read      readFn
	snap      snapFn
	heartbeat time.Duration

	q         chan capture.Committed
	done      chan struct{}
	closeOnce sync.Once
	overflow  atomic.Bool
	ctx       context.Context
	cancel    context.CancelFunc

	mu   sync.Mutex
	subs map[*subscriber]struct{}
}

// NewHub returns a running hub that reads from d.Reader.
func NewHub(d Deps) *Hub {
	h := newHub(d, hubQueueSize)
	h.start()
	return h
}

func newHub(d Deps, queue int) *Hub {
	log := d.Log
	if log == nil {
		log = zap.NewNop()
	}
	h := &Hub{
		log: log, heartbeat: heartbeatEvery,
		q: make(chan capture.Committed, queue), done: make(chan struct{}),
		subs: map[*subscriber]struct{}{},
	}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	h.read = func(ctx context.Context, who sessIdent, ids []string, summary, entries bool) (liveMsg, error) {
		return readLive(ctx, d, who, ids, summary, entries)
	}
	h.snap = func(ctx context.Context, session string) (snapshot, error) {
		return readSnapshot(ctx, d, session)
	}
	return h
}

// start runs the dispatcher through logging.Go, so a panic in it is logged, not fatal.
func (h *Hub) start() {
	logging.Go(h.log, "ui_hub", func() error { h.run(); return nil })
}

// Publish queues a commit. It never waits: a full queue sets the overflow flag, and after
// Close it does nothing. The queue is never closed, so the send cannot panic.
func (h *Hub) Publish(c capture.Committed) {
	select {
	case <-h.done:
		return
	default:
	}
	select {
	case <-h.done:
	case h.q <- c:
	default:
		h.overflow.Store(true)
	}
}

// Close stops the dispatcher and ends every open stream. It is safe to call twice.
func (h *Hub) Close() {
	h.closeOnce.Do(func() {
		close(h.done)
		h.cancel()
	})
}

// subscribe registers a stream; session "" is the global stream.
func (h *Hub) subscribe(session string) (*subscriber, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.done:
		return nil, errHubClosed
	default:
	}
	if len(h.subs) >= maxStreams {
		return nil, errTooManyStreams
	}
	s := &subscriber{session: session, ch: make(chan liveMsg, subscriberBuffer), gone: make(chan struct{})}
	h.subs[s] = struct{}{}
	return s, nil
}

func (h *Hub) unsubscribe(s *subscriber) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

// send hands m to s without waiting; a full buffer drops s alone.
func (h *Hub) send(s *subscriber, m liveMsg) {
	select {
	case s.ch <- m:
	default:
		h.unsubscribe(s)
		s.drop()
	}
}

// dropAll ends every stream; each reconnects and gets a snapshot.
func (h *Hub) dropAll() {
	h.mu.Lock()
	all := h.subs
	h.subs = map[*subscriber]struct{}{}
	h.mu.Unlock()
	for s := range all {
		s.drop()
	}
}

// watchers reports whether a global stream is open, and whether a session stream watches id.
func (h *Hub) watchers(id string) (global, session bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		switch s.session {
		case "":
			global = true
		case id:
			session = true
		}
	}
	return global, session
}

// targets lists the global streams, or the streams watching session id.
func (h *Hub) targets(id string, global bool) []*subscriber {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*subscriber
	for s := range h.subs {
		if (global && s.session == "") || (!global && s.session == id) {
			out = append(out, s)
		}
	}
	return out
}

// run is the one dispatcher: it takes a commit, drains the rest already queued, and
// handles them per session.
func (h *Hub) run() {
	for {
		var first capture.Committed
		select {
		case <-h.done:
			return
		case first = <-h.q:
		}
		batch := []capture.Committed{first}
	drain:
		for {
			select {
			case c := <-h.q:
				batch = append(batch, c)
			default:
				break drain
			}
		}
		if h.overflow.Swap(false) {
			h.log.Warn("ui hub overflow: dropping all streams")
			h.dropAll()
			continue
		}
		h.dispatch(batch)
	}
}

type group struct {
	who sessIdent
	ids []string
}

func (h *Hub) dispatch(batch []capture.Committed) {
	var groups []*group
	index := map[sessIdent]*group{}
	seen := map[string]bool{}
	for _, c := range batch {
		if c.SessionKey == "" || seen[c.RequestID] {
			continue // ungrouped exchanges have no session to update
		}
		seen[c.RequestID] = true
		who := sessIdent{c.PrincipalID, c.Client, c.SessionKey}
		g := index[who]
		if g == nil {
			g = &group{who: who}
			index[who] = g
			groups = append(groups, g)
		}
		g.ids = append(g.ids, c.RequestID)
	}
	for _, g := range groups {
		select {
		case <-h.done:
			return
		default:
		}
		id := g.who.id()
		global, session := h.watchers(id)
		if !global && !session {
			continue
		}
		ctx, cancel := context.WithTimeout(h.ctx, storeTimeout)
		m, err := h.read(ctx, g.who, g.ids, global, session)
		cancel()
		if err != nil {
			// A stream that misses this update would stay stale: end them so they resync.
			if h.ctx.Err() == nil {
				h.log.Warn("ui hub read failed", zap.Error(err))
			}
			for _, s := range append(h.targets(id, true), h.targets(id, false)...) {
				h.unsubscribe(s)
				s.drop()
			}
			continue
		}
		if global && m.Summary != nil {
			for _, s := range h.targets(id, true) {
				h.send(s, liveMsg{Summary: m.Summary})
			}
		}
		if session && len(m.Exchanges) > 0 {
			for _, s := range h.targets(id, false) {
				h.send(s, liveMsg{Exchanges: m.Exchanges, All: m.All, Inactive: m.Inactive})
			}
		}
	}
}

// readLive is the production readFn: the summary needs SessionStats only; the entries
// need one full timeline build, done only when a session stream asks.
func readLive(ctx context.Context, d Deps, who sessIdent, ids []string, summary, entries bool) (liveMsg, error) {
	if d.Reader == nil {
		return liveMsg{}, errors.New("ui: no reader")
	}
	c := d.Reader.WithContext(ctx)
	var m liveMsg
	if summary {
		row, err := c.SessionByKey(who.principal, who.client, who.name)
		if err != nil {
			return liveMsg{}, err
		}
		st, err := timeline.SessionStats(c, row)
		if err != nil {
			return liveMsg{}, err
		}
		s := buildSummary(who.id(), row, st, d.now())
		m.Summary = &s
	}
	if entries {
		all, err := sessionExchanges(c, who)
		if err != nil {
			return liveMsg{}, err
		}
		want := make(map[string]bool, len(ids))
		for _, id := range ids {
			want[id] = true
		}
		for _, x := range all {
			if want[x.RequestID] {
				m.Exchanges = append(m.Exchanges, x)
			}
		}
		sort.Slice(m.Exchanges, func(i, j int) bool { return m.Exchanges[i].RowID < m.Exchanges[j].RowID })
		m.All = all
		if n := len(all); n > 0 {
			last := all[0].StartedAt
			for _, x := range all {
				last = max(last, x.StartedAt)
			}
			m.Inactive = d.now().Sub(time.Unix(0, last)) >= ActiveWindow
		}
	}
	return m, nil
}

// sessionExchanges builds a session's whole timeline from one snapshot read and returns
// each stored exchange with its entries, in timeline order. Exchanges with no entry are
// left out.
func sessionExchanges(c *store.CtxReader, who sessIdent) ([]liveExchange, error) {
	rows, _, err := c.SessionSnapshot(who.principal, who.client, who.name)
	if err != nil {
		return nil, err
	}
	exs := make([]store.ExchangeRow, len(rows))
	byID := make(map[string]store.ExchangeRow, len(rows))
	rowID := make(map[string]int64, len(rows))
	for i, x := range rows {
		exs[i] = x.ExchangeRow
		byID[x.RequestID] = x.ExchangeRow
		rowID[x.RequestID] = x.RowID
	}
	session := store.SessionRow{PrincipalID: who.principal, Client: who.client, SessionKey: who.name}
	built, _, err := timeline.BuildWithUsage(snapshotSource{c, exs}, session, timeline.Options{Cut: entryCut})
	if err != nil {
		return nil, err
	}
	var out []liveExchange
	at := map[string]int{}
	for _, e := range mapEntries(built, byID) {
		i, ok := at[e.ExchangeID]
		if !ok {
			i = len(out)
			at[e.ExchangeID] = i
			out = append(out, liveExchange{RowID: rowID[e.ExchangeID], RequestID: e.ExchangeID,
				StartedAt: byID[e.ExchangeID].StartedAt})
		}
		out[i].Entries = append(out[i].Entries, e)
	}
	for i := range out {
		b, err := json.Marshal(out[i].Entries)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out[i].Hash = string(sum[:])
	}
	return out, nil
}

// snapshot is what a stream sends on open: the first page of sessions (global stream) or
// every exchange of the session in timeline order.
type snapshot struct {
	Sessions  []sessionSummary
	Exchanges []liveExchange
}

// snapFn reads the connect snapshot; session "" is the global stream. Tests replace it.
type snapFn func(ctx context.Context, session string) (snapshot, error)

func readSnapshot(ctx context.Context, d Deps, session string) (snapshot, error) {
	if d.Reader == nil {
		return snapshot{}, errors.New("ui: no reader")
	}
	c := d.Reader.WithContext(ctx)
	if session != "" {
		principal, client, key, _ := parseSessionID(session)
		x, err := sessionExchanges(c, sessIdent{principal, client, key})
		return snapshot{Exchanges: x}, err
	}
	rows, err := c.SessionPage(nil, defaultListLimit)
	if err != nil {
		return snapshot{}, err
	}
	now := d.now()
	var out snapshot
	for _, row := range rows {
		st, err := timeline.SessionStats(c, row)
		if err != nil {
			return snapshot{}, err
		}
		out.Sessions = append(out.Sessions, buildSummary(sessionID(row), row, st, now))
	}
	return out, nil
}
