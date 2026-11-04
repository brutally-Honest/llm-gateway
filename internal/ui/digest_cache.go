package ui

import (
	"container/list"
	"context"
	"sync"
	"sync/atomic"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// digestCacheSize bounds the digest cache.
const digestCacheSize = 512

// digestKey is a session and the store version its digest was computed at.
type digestKey struct {
	id  store.SessionIdent
	ver store.Version
}

type digestEntry struct {
	at  digestKey
	val timeline.Digest
}

// DigestCache memoises session digests by session and store version. It is a pure
// cache: lost on restart and rebuilt on demand. It holds the digest only, never a
// value that depends on the clock.
type DigestCache struct {
	mu       sync.Mutex
	max      int
	ll       *list.List // front is most recently used
	items    map[digestKey]*list.Element
	flights  *flightGroup[digestKey, timeline.Digest]
	computes atomic.Int64
}

// NewDigestCache returns an empty cache holding at most 512 digests.
func NewDigestCache() *DigestCache { return newDigestCache(digestCacheSize) }

func newDigestCache(max int) *DigestCache { return newDigestCacheCtx(context.Background(), max) }

// newDigestCacheCtx is a cache whose computes run on base, the server's lifetime.
func newDigestCacheCtx(base context.Context, max int) *DigestCache {
	return &DigestCache{max: max, ll: list.New(), items: map[digestKey]*list.Element{},
		flights: newFlightGroup[digestKey, timeline.Digest](base)}
}

func cloneDigest(d timeline.Digest) timeline.Digest {
	m := make(map[string]int, len(d.ToolStates))
	for k, v := range d.ToolStates {
		m[k] = v
	}
	d.ToolStates = m
	return d
}

// lookup returns the cached digest at k.
func (c *DigestCache) lookup(k digestKey) (timeline.Digest, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return timeline.Digest{}, false
	}
	c.ll.MoveToFront(el)
	return cloneDigest(el.Value.(*digestEntry).val), true
}

func (c *DigestCache) store(k digestKey, d timeline.Digest) {
	stored := cloneDigest(d)
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		el.Value.(*digestEntry).val = stored
		c.ll.MoveToFront(el)
		return
	}
	c.items[k] = c.ll.PushFront(&digestEntry{at: k, val: stored})
	for c.ll.Len() > c.max {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.items, last.Value.(*digestEntry).at)
	}
}

// get returns the digest at k. A miss computes it once however many requests ask: the
// compute runs on the cache's own context and stores its result, so a request that
// times out leaves the digest cached for its retry. A failed compute stores nothing.
func (c *DigestCache) get(ctx context.Context, k digestKey, compute func(context.Context) (timeline.Digest, error)) (timeline.Digest, error) {
	if d, ok := c.lookup(k); ok {
		return d, nil
	}
	d, err := c.flights.do(ctx, k, func(bctx context.Context) (timeline.Digest, error) {
		if d, ok := c.lookup(k); ok { // stored by a flight that just ended
			return d, nil
		}
		c.computes.Add(1)
		d, err := compute(bctx)
		if err != nil {
			return timeline.Digest{}, err
		}
		c.store(k, d)
		return d, nil
	})
	if err != nil {
		return timeline.Digest{}, err
	}
	return cloneDigest(d), nil
}

func (c *DigestCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
