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
	// StaleSummary and StaleEntries report that the summary or the entries were served
	// from a cache older than the store; the hub then sends a catch-up once the rebuild
	// lands. They never reach a stream.
	StaleSummary, StaleEntries bool
	// ReadAt is the store's version when the entries were read, and BuiltAt the version
	// the build behind them reaches. A commit read at ReadAt is covered by any build at
	// that version or newer. They never reach a stream.
	ReadAt, BuiltAt store.Version
}

// freshFn waits until the caches hold the session at the store's current version: the
// digest, and the timeline build too when entries is true. Tests replace it.
type freshFn func(ctx context.Context, who sessIdent, entries bool) error

// readFn reads what a batch of commits to one session needs: the summary and/or the
// entries of the exchanges with these request ids, in row id order.
type readFn func(ctx context.Context, who sessIdent, ids []string, summary, entries bool) (liveMsg, error)

// pendingCatch is what a session's catch-up must send once the rebuild lands: the summary
// always; when entries is true, the session streams' entries too, as a batch of the
// deferred commits in ids or, with none, as a refresh. need[i] is the store version ids[i]
// was read at: a build at that version or newer covers it.
type pendingCatch struct {
	entries bool
	ids     []string
	need    []store.Version
}

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
	timelines *TimelineCache
	digests   *DigestCache
	read      readFn
	snap      snapFn
	fresh     freshFn
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
	// catching holds the sessions with a catch-up waiting on a rebuild.
	catching map[sessIdent]*pendingCatch
}

// NewHub returns a running hub that reads from d.Reader.
func NewHub(d Deps) *Hub {
	h := newHub(d, hubQueueSize)
	h.start()
	return h
}

// Timelines is the timeline cache the hub reads through.
func (h *Hub) Timelines() *TimelineCache { return h.timelines }

// Digests is the digest cache the hub reads through.
func (h *Hub) Digests() *DigestCache { return h.digests }

func newHub(d Deps, queue int) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	if d.Digests == nil {
		d.Digests = newDigestCacheCtx(ctx, d.Log, digestCacheSize)
	}
	if d.Timelines == nil {
		d.Timelines = newTimelineCache(ctx, d.Log, timelineCacheSessions, timelineCacheBytes)
	}
	log := d.Log
	if log == nil {
		log = zap.NewNop()
	}
	h := &Hub{
		log: log, timelines: d.Timelines, digests: d.Digests, heartbeat: heartbeatEvery, hookEvery: hookRefreshEvery,
		pending: map[sessIdent]*pendingRefresh{}, catching: map[sessIdent]*pendingCatch{},
		q: make(chan capture.Committed, queue), done: make(chan struct{}),
		subs: map[*subscriber]struct{}{},
	}
	h.ctx, h.cancel = ctx, cancel
	h.read = func(ctx context.Context, who sessIdent, ids []string, summary, entries bool) (liveMsg, error) {
		return readLive(ctx, d, who, ids, summary, entries)
	}
	h.snap = func(ctx context.Context, session string) (snapshot, error) {
		return readSnapshot(ctx, d, session)
	}
	h.fresh = func(ctx context.Context, who sessIdent, entries bool) error {
		return waitFresh(ctx, d, who, entries)
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
	h.readAndSend(who, nil, nil, global, session, true, false)
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
			h.readAndSend(g.who, g.ids, nil, global, session, false, false)
		}
		h.sendMu.Unlock()
	}
}

// readAndSend reads one session and hands the result to its watchers. A failed read drops
// them, so they resync by snapshot. sendMu is held. A refresh sends the session streams
// every exchange that differs, instead of the committed ones. need[i] is the store version
// ids[i] was first read at; nil means now, for commits the dispatcher has just seen. landed
// is true when a catch-up calls it after a build landed.
func (h *Hub) readAndSend(who sessIdent, ids []string, need []store.Version, global, session, refresh, landed bool) {
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
	// Entries from a build older than the store are sent for what that build covers, and
	// the rest wait for the catch-up: a commit read at version v is covered by a build at v
	// or newer. Under steady writes every read is stale again by the time a build lands,
	// so waiting for a caught-up build would hold the session streams back until the
	// writes stop (F5).
	deferred := session && m.StaleEntries
	send, restIDs, restNeed := m.Exchanges, []string(nil), []store.Version(nil)
	if deferred && !refresh {
		covered := map[string]bool{}
		for i, x := range ids {
			at := m.ReadAt
			if need != nil {
				at = need[i]
			}
			if versionAfter(at, m.BuiltAt) {
				restIDs, restNeed = append(restIDs, x), append(restNeed, at)
			} else {
				covered[x] = true
			}
		}
		send = nil
		for _, x := range m.Exchanges {
			if covered[x.RequestID] {
				send = append(send, x)
			}
		}
	}
	// A stale refresh has nothing newer to send until a build lands: it waits for one.
	waitForBuild := deferred && refresh && !landed
	if session && (refresh || len(send) > 0) && !waitForBuild {
		for _, s := range h.targets(id, false) {
			h.send(s, liveMsg{Summary: m.Summary, Exchanges: send, All: m.All, Inactive: m.Inactive, Refresh: refresh, Position: m.Position})
		}
	}
	switch {
	case deferred && refresh:
		h.catchUp(who, true, nil, nil) // a refresh is resent once a newer build lands
	case deferred && len(restIDs) > 0:
		h.catchUp(who, true, restIDs, restNeed)
	case m.StaleSummary:
		h.catchUp(who, false, nil, nil)
	}
}

// catchUp sends a session's watchers what a stale read left out. It waits, off the
// dispatcher, for the rebuild the stale read asked for, then reads again and sends: the
// deferred commits as one batch, or with none and stale entries a refresh of every
// exchange that differs from what a stream last sent, or else the summary alone. A session
// has at most one catch-up waiting, which gathers what later stale reads defer; a read that
// is stale again arms the next, so once writes stop the last one sends the store as it is.
func (h *Hub) catchUp(who sessIdent, entries bool, ids []string, need []store.Version) {
	h.mu.Lock()
	select {
	case <-h.done:
		h.mu.Unlock()
		return
	default:
	}
	if p, ok := h.catching[who]; ok {
		p.entries = p.entries || entries
		p.ids = append(p.ids, ids...)
		p.need = append(p.need, need...)
		h.mu.Unlock()
		return
	}
	h.catching[who] = &pendingCatch{entries: entries, ids: append([]string(nil), ids...), need: append([]store.Version(nil), need...)}
	h.mu.Unlock()
	logging.Go(h.log, "ui_hub_catchup", func() error {
		ctx, cancel := context.WithTimeout(h.ctx, buildTimeout)
		err := h.fresh(ctx, who, entries)
		cancel()
		h.mu.Lock()
		p := h.catching[who]
		delete(h.catching, who)
		h.mu.Unlock()
		if err != nil {
			if h.ctx.Err() == nil {
				h.log.Warn("ui hub catch-up failed", zap.Error(err))
			}
			return nil
		}
		h.sendMu.Lock()
		defer h.sendMu.Unlock()
		select {
		case <-h.done:
			return nil
		default:
		}
		global, session := h.watchers(who.id())
		switch {
		case session && len(p.ids) > 0:
			h.readAndSend(who, p.ids, p.need, global, session, false, true)
		case session && p.entries:
			h.readAndSend(who, nil, nil, global, session, true, true)
		case global || session:
			h.resendSummary(who)
		}
		return nil
	})
}

// resendSummary reads a session's summary and sends it to every stream that watches the
// session, global and session streams alike. A failed read drops them, as readAndSend
// does. sendMu is held.
func (h *Hub) resendSummary(who sessIdent) {
	id := who.id()
	ctx, cancel := context.WithTimeout(h.ctx, storeTimeout)
	m, err := h.read(ctx, who, nil, true, false)
	cancel()
	targets := append(h.targets(id, true), h.targets(id, false)...)
	if err != nil {
		if h.ctx.Err() == nil {
			h.log.Warn("ui hub read failed", zap.Error(err))
		}
		for _, s := range targets {
			h.unsubscribe(s)
			s.drop()
		}
		return
	}
	if m.Summary != nil {
		for _, s := range targets {
			h.send(s, liveMsg{Summary: m.Summary})
		}
	}
	if m.StaleSummary {
		h.catchUp(who, false, nil, nil)
	}
}

// waitFresh is the production freshFn: it waits for the session's digest at the store's
// current version, and its timeline build when entries is true, starting each one if none
// is under way.
func waitFresh(ctx context.Context, d Deps, who sessIdent, entries bool) error {
	if d.Reader == nil {
		return errors.New("ui: no reader")
	}
	c := d.Reader.WithContext(ctx)
	ident := store.SessionIdent{Principal: who.principal, Client: who.client, Key: who.name}
	vers, err := c.SessionVersions([]store.SessionIdent{ident})
	if err != nil {
		return err
	}
	if d.Digests != nil {
		row, err := c.SessionByKey(who.principal, who.client, who.name)
		if err != nil {
			return err
		}
		if _, err := d.Digests.get(ctx, ident, vers[ident], d.digestCompute(row)); err != nil {
			return err
		}
	}
	if entries && d.Timelines != nil {
		if _, err := d.Timelines.get(ctx, ident, vers[ident], d.timelineBuild(who)); err != nil {
			return err
		}
	}
	return nil
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
			s, stale, err := d.summarize(ctx, c, who.id(), row, d.now())
			if err != nil {
				return liveMsg{}, err
			}
			m.Summary, m.StaleSummary = &s, stale
		case !errors.Is(err, store.ErrNotFound):
			return liveMsg{}, err
		}
	}
	if entries {
		all, ver, read, stale, err := sessionExchanges(ctx, d, who)
		if err != nil {
			return liveMsg{}, err
		}
		m.Position, m.StaleEntries = positionOf(ver), stale
		m.ReadAt, m.BuiltAt = read, ver
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
// are left out. The build is the timeline cache's latest for the session, served at once
// when one is cached: it returns the version that build is at, and stale when that is
// older than the store, in which case a rebuild was asked for. It also returns the store's
// version when it was read, which a build at that version or newer covers.
func sessionExchanges(ctx context.Context, d Deps, who sessIdent) ([]liveExchange, store.Version, store.Version, bool, error) {
	rules := d.Rules[who.client]
	ident := store.SessionIdent{Principal: who.principal, Client: who.client, Key: who.name}
	vers, err := d.Reader.WithContext(ctx).SessionVersions([]store.SessionIdent{ident})
	if err != nil {
		return nil, store.Version{}, store.Version{}, false, err
	}
	ver := vers[ident]
	read := ver
	stale := false
	var bs builtSession
	if d.Timelines == nil {
		bs, err = buildSession(d.Reader.WithContext(ctx), who, rules, entryCut)
	} else {
		var cb *cachedBuild
		cb, err = d.Timelines.latest(ctx, ident, ver, d.timelineBuild(who))
		if cb != nil {
			bs, stale, ver = cb.bs, versionAfter(ver, cb.ver), cb.ver
		}
	}
	if err != nil {
		return nil, ver, read, false, err
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
			return nil, ver, read, false, err
		}
		sum := sha256.Sum256(b)
		out[i].Hash = string(sum[:])
	}
	return out, ver, read, stale, nil
}

// timelineBuild builds a session's timeline on bctx through the server's reader, so it
// still lands in the cache when the request that asked for it has timed out.
func (d Deps) timelineBuild(who sessIdent) func(context.Context) (builtSession, error) {
	rules := d.Rules[who.client]
	return func(bctx context.Context) (builtSession, error) {
		return buildSession(d.Reader.WithContext(bctx), who, rules, entryCut)
	}
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
	// Stale lists what the snapshot served from a cache older than the store. The stream
	// asks the hub to catch each one up after it sends the snapshot.
	Stale []staleRead
}

// staleRead is a session a read served from an older cache: its summary, and its entries
// too when entries is true.
type staleRead struct {
	who     sessIdent
	entries bool
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
			s, stale, err := d.summarize(ctx, c, session, row, d.now())
			if err != nil {
				return snapshot{}, err
			}
			out.Sessions = append(out.Sessions, s)
			if stale {
				out.Stale = append(out.Stale, staleRead{who: who})
			}
		case !errors.Is(err, store.ErrNotFound):
			return snapshot{}, err
		}
		x, ver, _, stale, err := sessionExchanges(ctx, d, who)
		out.Exchanges = x
		out.Version, out.Position = versionToJSON(ver), positionOf(ver)
		if stale {
			out.Stale = append(out.Stale, staleRead{who: who, entries: true})
		}
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
	sums, stale, err := d.summarizeRows(ctx, c, ids, rows, d.now())
	if err != nil {
		return snapshot{}, err
	}
	out := snapshot{Sessions: sums}
	for _, s := range stale {
		out.Stale = append(out.Stale, staleRead{who: sessIdent{s.Principal, s.Client, s.Key}})
	}
	return out, nil
}
