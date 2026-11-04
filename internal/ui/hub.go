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
	// hookRefreshEvery is the longest a session's hook events wait before one re-read (AC26).
	hookRefreshEvery = 250 * time.Millisecond
	// hooksExchange is the request id of the pseudo-exchange that carries a session's hook
	// lines. Its entries have no exchange id.
	hooksExchange = "hooks"
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
// changed after it is built. Both kinds get Summary; a session stream also gets Exchanges.
type liveMsg struct {
	Summary   *sessionSummary
	Exchanges []liveExchange // the committed exchanges, in row id order
	All       []liveExchange // every exchange of the session, in timeline order
	Inactive  bool           // the session's last exchange started over ActiveWindow ago (E21)
	Refresh   bool           // a hook refresh: send every exchange in All that differs from what was sent
	// Position is how far into the store the build behind Exchanges and All reaches.
	// A stream drops a message built from less than its snapshot saw.
	Position eventPositionJSON
}

// readFn reads what a batch of commits to one session needs: the summary and/or the
// entries of the exchanges with these request ids, in row id order.
type readFn func(ctx context.Context, who sessIdent, ids []string, summary, entries bool) (liveMsg, error)

// pendingRefresh is a session's armed hook refresh. It is a pointer so a fired timer can
// tell whether it is still the session's current one.
type pendingRefresh struct{ timer *time.Timer }

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
	hookEvery time.Duration

	// sendMu orders a session's read and send: an exchange commit and a hook refresh never
	// interleave, so no stream gets an older read after a newer one.
	sendMu sync.Mutex

	q         chan capture.Committed
	done      chan struct{}
	closeOnce sync.Once
	overflow  atomic.Bool
	ctx       context.Context
	cancel    context.CancelFunc

	mu   sync.Mutex
	subs map[*subscriber]struct{}
	// pending holds one refresh timer per session with hook events not yet re-read.
	pending map[sessIdent]*pendingRefresh
}

// NewHub returns a running hub that reads from d.Reader.
func NewHub(d Deps) *Hub {
	h := newHub(d, hubQueueSize)
	h.start()
	return h
}

func newHub(d Deps, queue int) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	if d.Digests == nil {
		d.Digests = newDigestCacheCtx(ctx, digestCacheSize)
	}
	if d.Timelines == nil {
		d.Timelines = newTimelineCache(ctx, timelineCacheSessions, timelineCacheBytes)
	}
	log := d.Log
	if log == nil {
		log = zap.NewNop()
	}
	h := &Hub{
		log: log, heartbeat: heartbeatEvery, hookEvery: hookRefreshEvery,
		pending: map[sessIdent]*pendingRefresh{},
		q:       make(chan capture.Committed, queue), done: make(chan struct{}),
		subs: map[*subscriber]struct{}{},
	}
	h.ctx, h.cancel = ctx, cancel
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

// PublishHook notes that a hook event of this session is stored. Events of one session
// inside the refresh window share one re-read, taken when the window ends, so the last
// event always lands. It never waits, and does nothing when nobody watches the session.
func (h *Hub) PublishHook(principal, client, sessionKey string) {
	if sessionKey == "" {
		return
	}
	who := sessIdent{principal, client, sessionKey}
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.done:
		return
	default:
	}
	if _, ok := h.pending[who]; ok || !h.watchedLocked(who.id()) {
		return
	}
	p := &pendingRefresh{}
	p.timer = time.AfterFunc(h.hookEvery, func() {
		logging.Go(h.log, "ui_hub_hook", func() error { h.refresh(who, p); return nil })
	})
	h.pending[who] = p
}

// watchedLocked reports whether any open stream wants updates of session id. h.mu is held.
func (h *Hub) watchedLocked(id string) bool {
	for s := range h.subs {
		if s.session == "" || s.session == id {
			return true
		}
	}
	return false
}

// refresh re-reads a session after its hook window and sends the result. p is the refresh
// that fired; if it is no longer the session's pending one, it was cancelled.
func (h *Hub) refresh(who sessIdent, p *pendingRefresh) {
	h.mu.Lock()
	if h.pending[who] != p {
		h.mu.Unlock()
		return
	}
	delete(h.pending, who)
	h.mu.Unlock()
	h.sendMu.Lock()
	defer h.sendMu.Unlock()
	select {
	case <-h.done:
		return
	default:
	}
	id := who.id()
	global, session := h.watchers(id)
	if !global && !session {
		return
	}
	h.readAndSend(who, nil, global, session, true)
}

// Close stops the dispatcher and ends every open stream. It is safe to call twice.
func (h *Hub) Close() {
	h.closeOnce.Do(func() {
		close(h.done)
		h.cancel()
		h.mu.Lock()
		for who, p := range h.pending {
			p.timer.Stop()
			delete(h.pending, who)
		}
		h.mu.Unlock()
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

// unsubscribe removes s and stops the refresh timer of every session nobody watches now.
func (h *Hub) unsubscribe(s *subscriber) {
	h.mu.Lock()
	delete(h.subs, s)
	for who, p := range h.pending {
		if !h.watchedLocked(who.id()) {
			p.timer.Stop()
			delete(h.pending, who)
		}
	}
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
	for who, p := range h.pending {
		p.timer.Stop()
		delete(h.pending, who)
	}
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
		h.sendMu.Lock()
		global, session := h.watchers(g.who.id())
		if global || session {
			h.readAndSend(g.who, g.ids, global, session, false)
		}
		h.sendMu.Unlock()
	}
}

// readAndSend reads one session and hands the result to its watchers. A failed read drops
// them, so they resync by snapshot. sendMu is held. A refresh sends the session streams
// every exchange that differs, instead of the committed ones.
func (h *Hub) readAndSend(who sessIdent, ids []string, global, session, refresh bool) {
	id := who.id()
	ctx, cancel := context.WithTimeout(h.ctx, storeTimeout)
	m, err := h.read(ctx, who, ids, true, session)
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
		return
	}
	if global && m.Summary != nil {
		for _, s := range h.targets(id, true) {
			h.send(s, liveMsg{Summary: m.Summary})
		}
	}
	if session && (refresh || len(m.Exchanges) > 0) {
		for _, s := range h.targets(id, false) {
			h.send(s, liveMsg{Summary: m.Summary, Exchanges: m.Exchanges, All: m.All, Inactive: m.Inactive, Refresh: refresh, Position: m.Position})
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
		// No stored row means no summary to send, not a failed read.
		row, err := c.SessionByKey(who.principal, who.client, who.name)
		switch {
		case err == nil:
			s, err := d.summarize(ctx, c, who.id(), row, d.now())
			if err != nil {
				return liveMsg{}, err
			}
			m.Summary = &s
		case !errors.Is(err, store.ErrNotFound):
			return liveMsg{}, err
		}
	}
	if entries {
		all, ver, err := sessionExchanges(ctx, d, who)
		if err != nil {
			return liveMsg{}, err
		}
		m.Position = positionOf(ver)
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
		last, real := int64(0), false
		for _, x := range all {
			if x.RequestID != hooksExchange { // the hooks item is not an exchange
				last, real = max(last, x.StartedAt), true
			}
		}
		if real {
			m.Inactive = d.now().Sub(time.Unix(0, last)) >= ActiveWindow
		}
	}
	return m, nil
}

// sessionExchanges builds a session's whole timeline from one snapshot read and returns
// each stored exchange with its entries, in timeline order, then the hooks item (its
// Entries are [] when the session has no hook line, never null). Exchanges with no entry
// are left out. The build comes from the timeline cache, and a caller that needs the
// latest data waits for a running rebuild, so the result is at least as new as the
// store was when this was called. It also returns the version the build was asked for.
func sessionExchanges(ctx context.Context, d Deps, who sessIdent) ([]liveExchange, store.Version, error) {
	rules := d.Rules[who.client]
	ident := store.SessionIdent{Principal: who.principal, Client: who.client, Key: who.name}
	vers, err := d.Reader.WithContext(ctx).SessionVersions([]store.SessionIdent{ident})
	if err != nil {
		return nil, store.Version{}, err
	}
	ver := vers[ident]
	var bs builtSession
	if d.Timelines == nil {
		bs, err = buildSession(d.Reader.WithContext(ctx), who, rules, entryCut)
	} else {
		var cb *cachedBuild
		cb, err = d.Timelines.get(ctx, ident, ver, func(bctx context.Context) (builtSession, error) {
			return buildSession(d.Reader.WithContext(bctx), who, rules, entryCut)
		})
		if cb != nil {
			bs = cb.bs
		}
	}
	if err != nil {
		return nil, ver, err
	}
	built, facts, byID, rowID := bs.entries, bs.facts, bs.byID, bs.rowID
	var out []liveExchange
	at := map[string]int{}
	for _, e := range mapEntries(built, byID, facts, rules) {
		key := e.ExchangeID
		if e.Kind == kindHookEvent {
			key = hooksExchange // hook lines belong to no exchange: they travel as one pseudo-exchange
		}
		i, ok := at[key]
		if !ok {
			i = len(out)
			at[key] = i
			out = append(out, liveExchange{RowID: rowID[key], RequestID: key, StartedAt: byID[key].StartedAt})
		}
		out[i].Entries = append(out[i].Entries, e)
	}
	if _, ok := at[hooksExchange]; !ok {
		// The hooks item is always present, empty included, so a stream can replace its group.
		out = append(out, liveExchange{RequestID: hooksExchange, Entries: []entryJSON{}})
	}
	for i := range out {
		b, err := json.Marshal(out[i].Entries)
		if err != nil {
			return nil, ver, err
		}
		sum := sha256.Sum256(b)
		out[i].Hash = string(sum[:])
	}
	return out, ver, nil
}

// snapshot is what a stream sends on open: the first page of sessions (global stream) or
// every exchange of the session in timeline order.
type snapshot struct {
	Sessions  []sessionSummary
	Exchanges []liveExchange
	// Version and Position are the build behind Exchanges; the stream resumes from
	// Position, the exchange row and hook event ids it reaches.
	Version  versionJSON
	Position eventPositionJSON
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
		who := sessIdent{principal, client, key}
		var out snapshot
		row, err := c.SessionByKey(principal, client, key)
		switch {
		case err == nil:
			s, err := d.summarize(ctx, c, session, row, d.now())
			if err != nil {
				return snapshot{}, err
			}
			out.Sessions = append(out.Sessions, s)
		case !errors.Is(err, store.ErrNotFound):
			return snapshot{}, err
		}
		x, ver, err := sessionExchanges(ctx, d, who)
		out.Exchanges = x
		out.Version, out.Position = versionToJSON(ver), positionOf(ver)
		return out, err
	}
	rows, err := c.SessionPage(nil, defaultListLimit)
	if err != nil {
		return snapshot{}, err
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = sessionID(row)
	}
	sums, err := d.summarizeRows(ctx, c, ids, rows, d.now())
	if err != nil {
		return snapshot{}, err
	}
	return snapshot{Sessions: sums}, nil
}
