package ui

import (
	"container/list"
	"context"
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
	ver     store.Version
	compute func(context.Context) (digestMemo, error)
	done    chan struct{}
	res     *digestEntry
	err     error
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
	// fmu guards active. Lock order: fmu, then mu.
	fmu    sync.Mutex
	active map[store.SessionIdent]*digestRuns
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
		base: base, log: log, active: map[store.SessionIdent]*digestRuns{}}
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
	return m
}

func (e *digestEntry) hit() digestHit { return digestHit{memo: cloneMemo(e.memo), ver: e.ver} }

// lookup returns the session's cached entry when it is at ver.
func (c *DigestCache) lookup(id store.SessionIdent, ver store.Version) (*digestEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[id]
	if !ok || el.Value.(*digestEntry).ver != ver {
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

// store keeps e unless the session's cached entry is at e's version or newer.
func (c *DigestCache) store(e *digestEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[e.id]; ok {
		if !versionAfter(e.ver, el.Value.(*digestEntry).ver) {
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
	r := c.joinLocked(id, ver, compute)
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
	r := c.joinLocked(id, ver, compute)
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
func (c *DigestCache) joinLocked(id store.SessionIdent, ver store.Version,
	compute func(context.Context) (digestMemo, error)) *digestRun {
	newRun := func() *digestRun { return &digestRun{ver: ver, compute: compute, done: make(chan struct{})} }
	rs := c.active[id]
	if rs == nil {
		r := newRun()
		c.active[id] = &digestRuns{cur: r}
		c.start(id, r)
		return r
	}
	if rs.cur.ver == ver {
		return rs.cur
	}
	if rs.next == nil {
		rs.next = newRun()
	} else if versionAfter(ver, rs.next.ver) {
		rs.next.ver, rs.next.compute = ver, compute
	}
	return rs.next
}

func (c *DigestCache) start(id store.SessionIdent, r *digestRun) {
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
		if err != nil {
			// A recompute behind a stale read has nobody waiting, so this is its only trace.
			c.log.Warn("ui digest compute failed", zap.Error(err))
			r.err = err
			return
		}
		e := &digestEntry{id: id, ver: r.ver, memo: cloneMemo(m)}
		c.store(e)
		r.res = e
	}()
	c.fmu.Lock()
	rs := c.active[id]
	close(r.done)
	if rs.next != nil {
		rs.cur, rs.next = rs.next, nil
		c.start(id, rs.cur)
	} else {
		delete(c.active, id)
	}
	c.fmu.Unlock()
}

func (c *DigestCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
