package ui

import (
	"container/list"
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
	computes atomic.Int64
}

// NewDigestCache returns an empty cache holding at most 512 digests.
func NewDigestCache() *DigestCache { return newDigestCache(digestCacheSize) }

func newDigestCache(max int) *DigestCache {
	return &DigestCache{max: max, ll: list.New(), items: map[digestKey]*list.Element{}}
}

func cloneDigest(d timeline.Digest) timeline.Digest {
	m := make(map[string]int, len(d.ToolStates))
	for k, v := range d.ToolStates {
		m[k] = v
	}
	d.ToolStates = m
	return d
}

// get returns the digest at k, computing it outside the lock on a miss. Two callers
// may compute the same entry at once; the values are equal, the last write wins.
func (c *DigestCache) get(k digestKey, compute func() (timeline.Digest, error)) (timeline.Digest, error) {
	c.mu.Lock()
	if el, ok := c.items[k]; ok {
		c.ll.MoveToFront(el)
		v := cloneDigest(el.Value.(*digestEntry).val)
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	c.computes.Add(1)
	d, err := compute()
	if err != nil {
		return timeline.Digest{}, err
	}
	stored := cloneDigest(d)

	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		el.Value.(*digestEntry).val = stored
		c.ll.MoveToFront(el)
		return d, nil
	}
	c.items[k] = c.ll.PushFront(&digestEntry{at: k, val: stored})
	for c.ll.Len() > c.max {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.items, last.Value.(*digestEntry).at)
	}
	return d, nil
}

func (c *DigestCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
