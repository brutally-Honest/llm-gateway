package ui

import (
	"container/list"
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"

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

// buildKey names a build: a session at a store version.
type buildKey struct {
	id  store.SessionIdent
	ver store.Version
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
	flights  *flightGroup[buildKey, *cachedBuild]
	builds   atomic.Int64
}

// NewTimelineCache returns an empty cache with the production bounds.
func NewTimelineCache() *TimelineCache {
	return newTimelineCache(context.Background(), timelineCacheSessions, timelineCacheBytes)
}

// newTimelineCache is a cache whose builds run on base, the server's lifetime.
func newTimelineCache(base context.Context, sessions int, bytes int64) *TimelineCache {
	return &TimelineCache{maxSess: sessions, maxBytes: bytes, ll: list.New(),
		items:   map[store.SessionIdent]*list.Element{},
		flights: newFlightGroup[buildKey, *cachedBuild](base)}
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

func (c *TimelineCache) store(id store.SessionIdent, b *cachedBuild) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[id]; ok { // an older version of the session
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

// get returns the build of session id at ver. build runs at most once per (session,
// version) at a time, on the cache's context, and a successful result is stored even if
// every request waiting for it has timed out. A failed build is not stored.
func (c *TimelineCache) get(ctx context.Context, id store.SessionIdent, ver store.Version,
	build func(context.Context) (builtSession, error)) (*cachedBuild, error) {
	if b, ok := c.lookup(id, ver); ok {
		return b, nil
	}
	return c.flights.do(ctx, buildKey{id, ver}, func(bctx context.Context) (*cachedBuild, error) {
		if b, ok := c.lookup(id, ver); ok { // stored by a flight that just ended
			return b, nil
		}
		c.builds.Add(1)
		bs, err := build(bctx)
		if err != nil {
			return nil, err
		}
		b := &cachedBuild{bs: bs, ver: ver, id: id, size: estimateSize(bs)}
		c.store(id, b)
		return b, nil
	})
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
