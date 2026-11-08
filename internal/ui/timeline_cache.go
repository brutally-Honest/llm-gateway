package ui

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

const (
	// timelineCacheSessions is the most sessions whose builds the cache keeps.
	timelineCacheSessions = 4
	// timelineCacheBytes is the most estimated retained bytes it keeps (about 512 MB).
	timelineCacheBytes = 512 << 20
	// sizeFactor turns the marshalled size of a build into its retained size: Go
	// strings, maps and slice headers weigh more than their JSON. It is a documented
	// estimate, not a heap measurement; research.md Notes records the measured figure.
	sizeFactor = 4
)

// cachedBuild is one finished build and the store version it was asked for. It is
// immutable once stored and shared by every request that reads it.
type cachedBuild struct {
	id   store.SessionIdent
	bs   builtSession
	ver  store.Version
	size int64
}

// eventPositionJSON is how far into the store a build reaches: the highest exchange row
// id and hook event id of the version it was built for. A stream that resumes from it
// misses nothing; a build that saw a little more only makes it resend.
type eventPositionJSON struct {
	Row  int64 `json:"row"`
	Hook int64 `json:"hook"`
}

// versionJSON is a session's store version as the API shows it.
type versionJSON struct {
	Exchanges int   `json:"exchanges"`
	MaxRow    int64 `json:"max_row"`
	Parsed    int   `json:"parsed"`
	Hooks     int   `json:"hooks"`
	MaxHook   int64 `json:"max_hook"`
}

func versionToJSON(v store.Version) versionJSON {
	return versionJSON{Exchanges: v.ExCount, MaxRow: v.ExMaxRow, Parsed: v.Parsed, Hooks: v.HookCount, MaxHook: v.HookMaxID}
}

func positionOf(v store.Version) eventPositionJSON {
	return eventPositionJSON{Row: v.ExMaxRow, Hook: v.HookMaxID}
}

// TimelineCache memoises whole-session timeline builds. It is a pure cache, lost on
// restart. It keeps the latest build of at most timelineCacheSessions sessions, least
// recently used out first, and no more than a byte budget. Concurrent requests for one
// build share one run, which goes on after a request times out and is cached.
type TimelineCache struct {
	mu       sync.Mutex
	maxSess  int
	maxBytes int64
	bytes    int64
	ll       *list.List // front is most recently used
	items    map[store.SessionIdent]*list.Element
	builds   atomic.Int64

	// base is the server's lifetime: builds run on it, not on a request's context.
	base context.Context
	log  *zap.Logger
	// fmu guards active. Lock order: fmu, then mu.
	fmu    sync.Mutex
	active map[store.SessionIdent]*sessionBuilds
}

// buildRun is one build and the callers waiting on it.
type buildRun struct {
	ver   store.Version
	build func(context.Context) (builtSession, error)
	done  chan struct{}
	res   *cachedBuild
	err   error
}

// sessionBuilds is a session's builds in flight: the one running and at most one
// queued. The queued one is the dirty flag: a version change or live event during a
// build asks for exactly one follow-up, however many arrive.
type sessionBuilds struct{ cur, next *buildRun }

// NewTimelineCache returns an empty cache with the production bounds, whose builds run
// on base, the server's lifetime, and log their panics to log (nil logs nothing).
func NewTimelineCache(base context.Context, log *zap.Logger) *TimelineCache {
	return newTimelineCache(base, log, timelineCacheSessions, timelineCacheBytes)
}

// newTimelineCache is a cache whose builds run on base, the server's lifetime.
func newTimelineCache(base context.Context, log *zap.Logger, sessions int, bytes int64) *TimelineCache {
	if base == nil {
		base = context.Background()
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &TimelineCache{maxSess: sessions, maxBytes: bytes, ll: list.New(),
		items: map[store.SessionIdent]*list.Element{},
		base:  base, log: log, active: map[store.SessionIdent]*sessionBuilds{}}
}

func (c *TimelineCache) lookup(id store.SessionIdent, ver store.Version) (*cachedBuild, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[id]
	if !ok || el.Value.(*cachedBuild).ver != ver {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*cachedBuild), true
}

// versionAfter reports whether a is newer than b. Every field only grows, because the
// store never deletes, so two versions of a session are ordered unless something
// unforeseen moved a field back. Such a pair counts as newer: refusing it would pin the
// cache to its entry for good, while storing it costs at most one rebuild.
func versionAfter(a, b store.Version) bool {
	older := a.ExCount <= b.ExCount && a.ExMaxRow <= b.ExMaxRow && a.Parsed <= b.Parsed &&
		a.HookCount <= b.HookCount && a.HookMaxID <= b.HookMaxID
	return !older
}

// store keeps b unless the session's cached build is at b's version or newer.
func (c *TimelineCache) store(id store.SessionIdent, b *cachedBuild) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[id]; ok {
		if !versionAfter(b.ver, el.Value.(*cachedBuild).ver) {
			return // a build that started on older data finished last
		}
		c.bytes -= el.Value.(*cachedBuild).size
		c.ll.Remove(el)
	}
	c.items[id] = c.ll.PushFront(b)
	c.bytes += b.size
	for c.ll.Len() > c.maxSess || c.bytes > c.maxBytes {
		last := c.ll.Back()
		c.ll.Remove(last)
		c.bytes -= last.Value.(*cachedBuild).size
		delete(c.items, last.Value.(*cachedBuild).id)
	}
}

// peek returns the session's cached build whatever its version.
func (c *TimelineCache) peek(id store.SessionIdent) (*cachedBuild, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[id]
	if !ok {
		return nil, false
	}
	return el.Value.(*cachedBuild), true
}

// get returns the build of session id at ver, waiting for one if need be. build runs on
// the cache's context, once per session at a time, and a successful result is stored
// even if every request waiting for it has timed out. A failed build is not stored and
// is shared only by those waiting on it.
func (c *TimelineCache) get(ctx context.Context, id store.SessionIdent, ver store.Version,
	build func(context.Context) (builtSession, error)) (*cachedBuild, error) {
	if b, ok := c.lookup(id, ver); ok {
		return b, nil
	}
	c.fmu.Lock()
	r := c.joinLocked(id, ver, build)
	c.fmu.Unlock()
	return c.wait(ctx, r)
}

// latest is get for a reader that can do with an older build: when some build of the
// session is cached it returns that one at once, and starts or queues a rebuild at ver if
// it is not at ver. It waits only when nothing is cached.
func (c *TimelineCache) latest(ctx context.Context, id store.SessionIdent, ver store.Version,
	build func(context.Context) (builtSession, error)) (*cachedBuild, error) {
	if b, ok := c.lookup(id, ver); ok {
		return b, nil
	}
	c.fmu.Lock()
	r := c.joinLocked(id, ver, build)
	c.fmu.Unlock()
	if old, ok := c.peek(id); ok {
		return old, nil
	}
	return c.wait(ctx, r)
}

func (c *TimelineCache) wait(ctx context.Context, r *buildRun) (*cachedBuild, error) {
	select {
	case <-r.done:
		return r.res, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// joinLocked returns the run that will produce a build for ver: the running one when it
// is for ver, else the queued follow-up, created if absent and aimed at the newest ver
// asked. With nothing running it starts one. fmu is held.
func (c *TimelineCache) joinLocked(id store.SessionIdent, ver store.Version,
	build func(context.Context) (builtSession, error)) *buildRun {
	newRun := func() *buildRun { return &buildRun{ver: ver, build: build, done: make(chan struct{})} }
	sb := c.active[id]
	if sb == nil {
		r := newRun()
		c.active[id] = &sessionBuilds{cur: r}
		c.start(id, r)
		return r
	}
	if sb.cur.ver == ver {
		return sb.cur
	}
	if sb.next == nil {
		sb.next = newRun()
	} else if versionAfter(ver, sb.next.ver) {
		sb.next.ver, sb.next.build = ver, build
	}
	return sb.next
}

func (c *TimelineCache) start(id store.SessionIdent, r *buildRun) {
	logging.Go(c.log, "ui_build", func() error { c.run(id, r); return nil })
}

// run builds r, stores the result, and starts the queued follow-up if one was asked for.
func (c *TimelineCache) run(id store.SessionIdent, r *buildRun) {
	func() {
		ctx, cancel := context.WithTimeout(c.base, buildTimeout)
		defer cancel()
		defer func() {
			if p := recover(); p != nil {
				logging.LogPanic(c.log, "ui timeline build panicked", p)
				r.err = fmt.Errorf("ui: build panicked: %v", p)
			}
		}()
		if b, ok := c.lookup(id, r.ver); ok { // built meanwhile
			r.res = b
			return
		}
		c.builds.Add(1)
		bs, err := r.build(ctx)
		if err != nil {
			// A rebuild behind a stale read has nobody waiting, so this is its only trace.
			c.log.Warn("ui timeline build failed", zap.Error(err))
			r.err = err
			return
		}
		b := &cachedBuild{bs: bs, ver: r.ver, id: id, size: estimateSize(bs)}
		c.store(id, b)
		r.res = b
	}()
	c.fmu.Lock()
	sb := c.active[id]
	close(r.done)
	if sb.next != nil {
		sb.cur, sb.next = sb.next, nil
		c.start(id, sb.cur)
	} else {
		delete(c.active, id)
	}
	c.fmu.Unlock()
}

func (c *TimelineCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}

// estimateSize is the marshalled size of a build times sizeFactor.
func estimateSize(bs builtSession) int64 {
	n := 0
	for _, v := range []any{bs.entries, bs.facts, bs.byID, bs.rowID} {
		b, _ := json.Marshal(v)
		n += len(b)
	}
	return int64(n) * sizeFactor
}
