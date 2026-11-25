package ui

import (
	"container/heap"
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// digestCacheSize bounds the digest cache.
const digestCacheSize = 512

// digestMemo is what the cache keeps for a session at one store version: its digest and
// its stats, computed from one read of its exchanges and events. The stats are never
// changed after the memo is stored, so they are shared, not copied.
type digestMemo struct {
	digest timeline.Digest
	stats  timeline.Stats
	// row is what the sessions list shows from the session's view (017 AC1, AC2).
	row rowFacts

	// eff is the effect version the compute read before it read any data: with the store
	// version it was asked for, the key of the memo's on-disk entry (spec 030).
	eff store.EffectVersion
	// degraded: a content read failed for a reason other than not-found, so the memo is
	// never written to disk (spec 030 D16). In memory it is kept only so the list can
	// serve its facts; lookup treats it as absent, so it is rebuilt on every request that
	// needs the memo (spec 043).
	degraded bool
	// fromDisk: the memo came from the on-disk cache, so it is neither a compute nor
	// written again.
	fromDisk bool
}

// rowFacts are the counts and words a sessions-list row takes from the session's view, in
// the session page's own terms (spec 017 AC1, AC2).
type rowFacts struct {
	title      string
	folder     string
	folderFull string
	prompts    int
	mainCalls  int
	sideCalls  int
	commands   int
	clockMS    int64
	look       int
	lookKinds  []timeline.LookKind
}

// rowFactsOf reads a row's facts off a session view.
func rowFactsOf(v timeline.View) rowFacts {
	o := v.Overview
	return rowFacts{
		title: o.Title, folder: o.Folder, folderFull: o.FolderFull,
		prompts: o.Turns, mainCalls: o.Calls - len(v.SideCalls), sideCalls: len(v.SideCalls),
		commands: o.Commands.Count, clockMS: o.Time.ClockMS,
		look: len(o.NeedsLook) + o.MoreLook, lookKinds: o.LookKinds,
	}
}

// digestEntry is a session's latest computed memo and the version it was computed at.
// It is never changed after it is stored.
type digestEntry struct {
	id   store.SessionIdent
	ver  store.Version
	memo digestMemo
}

// digestHit is what a read returns: a copy of the memo and the version it was computed at.
type digestHit struct {
	memo digestMemo
	ver  store.Version
}

// digestRun is one compute and the callers waiting on it.
type digestRun struct {
	id      store.SessionIdent
	ver     store.Version
	compute func(context.Context) (digestMemo, error)
	done    chan struct{}
	res     *digestEntry
	err     error

	// prio is where the run ranks in the build gate; nil means some caller waits for it,
	// so it never enters the gate. Guarded by fmu.
	prio *gatePrio
	// queued: the run sits in the gate's queue (at index qi). inSlot: it holds a slot.
	// Guarded by fmu.
	queued bool
	qi     int
	inSlot bool
}

// digestRuns is a session's computes in flight: the one running and at most one queued,
// aimed at the newest version asked, however many versions arrive meanwhile.
type digestRuns struct{ cur, next *digestRun }

// DigestCache memoises session digests: per session, the latest one computed and the
// store version it was computed at. It is a pure cache: lost on restart and rebuilt on
// demand. It holds no value that depends on the clock. A session computes at most once at
// a time, on the cache's own context, so a compute outlives the request that started it.
type DigestCache struct {
	mu       sync.Mutex
	max      int
	ll       *list.List // front is most recently used
	items    map[store.SessionIdent]*list.Element
	computes atomic.Int64

	base context.Context
	log  *zap.Logger
	// disk is where computed memos are also written; nil is none. Set before use.
	disk *DiskCache
	// fmu guards active. Lock order: fmu, then mu.
	fmu    sync.Mutex
	active map[store.SessionIdent]*digestRuns
	// joined counts callers that joined a run already in flight at their version; tests
	// wait on it. Guarded by fmu.
	joined int

	// The build gate (spec 043 D12). Guarded by fmu.
	gate gateState
	// ticks numbers list requests: a newer request ranks above every older one.
	ticks atomic.Uint64
}

// NewDigestCache returns an empty cache holding at most 512 sessions, whose computes run
// on base, the server's lifetime, and log their panics to log (nil logs nothing).
func NewDigestCache(base context.Context, log *zap.Logger) *DigestCache {
	return newDigestCacheCtx(base, log, digestCacheSize)
}

func newDigestCache(max int) *DigestCache {
	return newDigestCacheCtx(context.Background(), nil, max)
}

// newDigestCacheCtx is a cache whose computes run on base, the server's lifetime, and
// log their panics to log.
func newDigestCacheCtx(base context.Context, log *zap.Logger, max int) *DigestCache {
	if base == nil {
		base = context.Background()
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &DigestCache{max: max, ll: list.New(), items: map[store.SessionIdent]*list.Element{},
		base: base, log: log, active: map[store.SessionIdent]*digestRuns{},
		gate: gateState{slots: gateSlots, inSlot: map[*digestRun]struct{}{}, changed: make(chan struct{})}}
}

// WithDisk makes the cache write each memo it computes to disk. It returns c; call it
// before the cache is used.
func (c *DigestCache) WithDisk(d *DiskCache) *DigestCache {
	c.disk = d
	return c
}

func cloneDigest(d timeline.Digest) timeline.Digest {
	m := make(map[string]int, len(d.ToolStates))
	for k, v := range d.ToolStates {
		m[k] = v
	}
	d.ToolStates = m
	return d
}

func cloneMemo(m digestMemo) digestMemo {
	m.digest = cloneDigest(m.digest)
	m.row.lookKinds = append([]timeline.LookKind(nil), m.row.lookKinds...)
	return m
}

func (e *digestEntry) hit() digestHit { return digestHit{memo: cloneMemo(e.memo), ver: e.ver} }

// lookup returns the session's cached entry when it is at ver.
func (c *DigestCache) lookup(id store.SessionIdent, ver store.Version) (*digestEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[id]
	if !ok || el.Value.(*digestEntry).ver != ver || el.Value.(*digestEntry).memo.degraded {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*digestEntry), true
}

// peek returns the session's cached entry whatever its version.
func (c *DigestCache) peek(id store.SessionIdent) (*digestEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[id]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*digestEntry), true
}

// heldAfter reports whether memory holds an entry for the session that is newer than ver.
func (c *DigestCache) heldAfter(id store.SessionIdent, ver store.Version) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[id]
	return ok && versionAfter(el.Value.(*digestEntry).ver, ver)
}

// store keeps e unless the session's cached entry is at e's version or newer. A degraded
// entry (spec 043 review P3) is kept so the list serves its facts, but it counts as no
// answer: lookup never returns it, so every request that needs the memo rebuilds it, and
// a good entry at the same version replaces it.
func (c *DigestCache) store(e *digestEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[e.id]; ok {
		old := el.Value.(*digestEntry)
		if !versionAfter(e.ver, old.ver) && (!old.memo.degraded || e.memo.degraded || e.ver != old.ver) {
			return // a compute that started on older data finished last
		}
		c.ll.Remove(el)
	}
	c.items[e.id] = c.ll.PushFront(e)
	for c.ll.Len() > c.max {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.items, last.Value.(*digestEntry).id)
	}
}

// get returns the session's memo at ver, waiting for its compute if need be. The compute
// runs on the cache's context and is stored even if every caller has timed out. A failed
// compute stores nothing and is shared only by those waiting on it.
func (c *DigestCache) get(ctx context.Context, id store.SessionIdent, ver store.Version,
	compute func(context.Context) (digestMemo, error)) (digestHit, error) {
	if e, ok := c.lookup(id, ver); ok {
		return e.hit(), nil
	}
	c.fmu.Lock()
	r := c.joinLocked(id, ver, nil, compute)
	c.fmu.Unlock()
	return c.wait(ctx, r)
}

// latest is get for a reader that can do with an older memo: when the session has one
// cached it returns it at once and starts or queues a compute at ver if it is not at ver.
// It waits only when nothing is cached. The hit's ver says which version it is.
func (c *DigestCache) latest(ctx context.Context, id store.SessionIdent, ver store.Version,
	compute func(context.Context) (digestMemo, error)) (digestHit, error) {
	if e, ok := c.lookup(id, ver); ok {
		return e.hit(), nil
	}
	c.fmu.Lock()
	r := c.joinLocked(id, ver, nil, compute)
	c.fmu.Unlock()
	if e, ok := c.peek(id); ok {
		return e.hit(), nil
	}
	return c.wait(ctx, r)
}

func (c *DigestCache) wait(ctx context.Context, r *digestRun) (digestHit, error) {
	select {
	case <-r.done:
		if r.err != nil {
			return digestHit{}, r.err
		}
		return r.res.hit(), nil
	case <-ctx.Done():
		return digestHit{}, ctx.Err()
	}
}

// joinLocked returns the run that will produce the memo at ver: the running one when it
// is for ver, else the queued follow-up, created if absent and aimed at the newest ver
// asked. With nothing running it starts one. fmu is held.
//
// gp is where the caller ranks in the build gate; nil means the caller waits for the
// result, so the run starts at once, and a run still queued in the gate that such a
// caller joins is promoted to start at once (D12). A list caller that joins a queued run
// only raises its rank.
func (c *DigestCache) joinLocked(id store.SessionIdent, ver store.Version, gp *gatePrio,
	compute func(context.Context) (digestMemo, error)) *digestRun {
	newRun := func() *digestRun {
		return &digestRun{id: id, ver: ver, compute: compute, done: make(chan struct{}), prio: gp.clone()}
	}
	rs := c.active[id]
	if rs == nil {
		r := newRun()
		c.active[id] = &digestRuns{cur: r}
		c.start(id, r)
		return r
	}
	if rs.cur.ver == ver {
		c.joined++
		c.rankLocked(id, rs.cur, gp)
		return rs.cur
	}
	if gp == nil && rs.cur.queued {
		cur := rs.cur
		if versionAfter(ver, cur.ver) {
			// A waiter wants a newer version than the run still queued in the gate. That
			// run has not started, so it is retargeted to the newer version and promoted:
			// the waiter must not sit behind the queue, and a follow-up would only start
			// after the queued run had waited for a slot. The callers already on it are
			// list callers or a background pass (getBackground waits on its run), and
			// each takes the memo at whatever version it gets (summarizeRows reads
			// hit.ver). A follow-up not newer than the retarget is aimed at it too: its
			// run then finds the memo and computes nothing, where it would compute an
			// older memo and write it over the newer disk file.
			cur.ver, cur.compute = ver, compute
			if n := rs.next; n != nil && !versionAfter(n.ver, ver) {
				n.ver, n.compute = ver, compute
			}
			c.joined++
			c.rankLocked(id, cur, nil)
			return cur
		}
		// Not newer: the waiter's version comes from the follow-up, which starts only
		// after cur. cur is still queued behind all list work, so it is promoted too,
		// or the waiter would wait for the queue after all (AC3).
		c.rankLocked(id, cur, nil)
	}
	if rs.next == nil {
		rs.next = newRun()
	} else {
		if versionAfter(ver, rs.next.ver) {
			rs.next.ver, rs.next.compute = ver, compute
		}
		c.rankLocked(id, rs.next, gp)
	}
	return rs.next
}

// rankLocked applies a joining caller's rank to r: a waiter makes it unranked (it starts
// at once if it was queued), a list caller raises the rank it has, never lowers it.
func (c *DigestCache) rankLocked(id store.SessionIdent, r *digestRun, gp *gatePrio) {
	if r.prio == nil {
		return
	}
	switch {
	case gp == nil:
		queued := r.queued
		c.gate.remove(r)
		r.prio = nil
		if queued {
			c.launch(id, r)
			c.gate.notify()
		}
	case gp.above(*r.prio):
		seq := r.prio.seq
		r.prio = gp.clone()
		r.prio.seq = seq
		if r.queued {
			heap.Fix(&c.gate.q, r.qi)
		}
	}
}

// start runs r now when a caller waits for it, else queues it in the gate. fmu is held.
func (c *DigestCache) start(id store.SessionIdent, r *digestRun) {
	if r.prio == nil {
		c.launch(id, r)
		return
	}
	r.prio.seq = c.gate.nextSeq()
	heap.Push(&c.gate.q, r)
	c.dispatchLocked()
}

// launch starts r's goroutine. Its timeout starts there, so a queued run's does not run.
func (c *DigestCache) launch(id store.SessionIdent, r *digestRun) {
	logging.Go(c.log, "ui_build", func() error { c.run(id, r); return nil })
}

// run computes r, stores the result, and starts the queued follow-up if one was asked for.
func (c *DigestCache) run(id store.SessionIdent, r *digestRun) {
	func() {
		ctx, cancel := context.WithTimeout(c.base, buildTimeout)
		defer cancel()
		defer func() {
			if p := recover(); p != nil {
				logging.LogPanic(c.log, "ui build panicked", p)
				r.err = fmt.Errorf("ui: build panicked: %v", p)
			}
		}()
		if e, ok := c.lookup(id, r.ver); ok { // computed meanwhile
			r.res = e
			return
		}
		c.computes.Add(1)
		m, err := r.compute(ctx)
		if err == nil && m.fromDisk {
			c.computes.Add(-1) // read from disk, not computed
		}
		if err != nil {
			// A recompute behind a stale read has nobody waiting, so this is its only trace.
			// A cancel at shutdown, or a compute past buildTimeout, is not a failure of the
			// compute, so it logs at Debug.
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				c.log.Debug("ui digest compute failed", zap.Error(err))
			} else {
				c.log.Warn("ui digest compute failed", zap.Error(err))
			}
			r.err = err
			return
		}
		e := &digestEntry{id: id, ver: r.ver, memo: cloneMemo(m)}
		r.res = e
		c.store(e)
		// a degraded memo is kept in memory only (store treats it as stale); the disk file
		// is one per session, so a memo older than memory's must not overwrite a newer one
		if !m.fromDisk && !m.degraded && !c.heldAfter(id, r.ver) {
			c.disk.PutMemo(id, r.ver, m.eff, m)
		}
	}()
	c.fmu.Lock()
	rs := c.active[id]
	close(r.done)
	if r.inSlot {
		r.inSlot = false
		delete(c.gate.inSlot, r)
	}
	if rs.next != nil {
		rs.cur, rs.next = rs.next, nil
		c.start(id, rs.cur)
	} else {
		delete(c.active, id)
	}
	c.dispatchLocked()
	c.fmu.Unlock()
}

func (c *DigestCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
