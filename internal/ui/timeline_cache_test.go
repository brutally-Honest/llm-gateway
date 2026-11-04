package ui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

type tlEnv struct {
	*digestEnv
	cache *TimelineCache
}

func newTLEnv(t *testing.T) *tlEnv {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	cache := NewTimelineCache()
	d := Deps{Addr: addr, Reader: r, Rules: map[string]ClientRules{"cc": testRules{}}, Timelines: cache,
		Now: func() time.Time { return baseTime.Add(100 * time.Second) }}
	return &tlEnv{digestEnv: &digestEnv{apiEnv: &apiEnv{t: t, s: s, dir: dir, h: Handler(d)}, d: d}, cache: cache}
}

func sessionPath() string {
	return "/api/sessions/" + sessIdent{core.PrincipalLocal, "cc", "k"}.id() + "/timeline"
}

func TestTimelineCache_IdleSessionFivePagesOneBuild(t *testing.T) {
	e := newTLEnv(t)
	for i := range 5 {
		e.call(string(rune('a'+i)), i+1, 0, 1, "user:hello")
	}
	seen, url, pages := 0, sessionPath()+"?limit=1", 0
	for {
		p := e.timeline(url)
		pages++
		seen += len(p.Entries)
		if p.NextAfter == nil {
			break
		}
		url = sessionPath() + "?limit=1&after=" + *p.NextAfter
	}
	if pages < 5 || seen < 5 {
		t.Fatalf("pages = %d entries = %d", pages, seen)
	}
	if got := e.cache.builds.Load(); got != 1 {
		t.Fatalf("builds = %d over %d pages, want 1", got, pages)
	}
}

func ident(key string) store.SessionIdent {
	return store.SessionIdent{Principal: core.PrincipalLocal, Client: "cc", Key: key}
}

func TestTimelineCache_TimedOutBuildIsCachedAndRetryHits(t *testing.T) {
	c := NewTimelineCache()
	release := make(chan struct{})
	build := func(context.Context) (builtSession, error) {
		<-release
		return builtSession{rows: 7}, nil
	}
	ver := store.Version{ExCount: 1}
	timedOut, cancel := context.WithCancel(t.Context())
	cancel() // the request's deadline has passed
	if _, err := c.get(timedOut, ident("k"), ver, build); !errors.Is(err, context.Canceled) {
		t.Fatalf("timed-out request err = %v", err)
	}
	close(release)
	b, err := c.get(t.Context(), ident("k"), ver, build)
	if err != nil || b.bs.rows != 7 {
		t.Fatalf("retry = %+v, %v", b, err)
	}
	if got := c.builds.Load(); got != 1 {
		t.Fatalf("builds = %d, want 1: the timed-out build was not kept", got)
	}
	if _, ok := c.lookup(ident("k"), ver); !ok {
		t.Fatal("build not cached")
	}
}

func TestTimelineCache_FailedBuildIsNotCached(t *testing.T) {
	c := NewTimelineCache()
	boom := errors.New("boom")
	if _, err := c.get(t.Context(), ident("k"), store.Version{}, func(context.Context) (builtSession, error) {
		return builtSession{}, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if c.len() != 0 {
		t.Fatal("failed build cached")
	}
	if _, err := c.get(t.Context(), ident("k"), store.Version{}, func(context.Context) (builtSession, error) {
		return builtSession{}, nil
	}); err != nil || c.builds.Load() != 2 {
		t.Fatalf("retry err = %v builds = %d", err, c.builds.Load())
	}
}

func TestTimelineCache_ConcurrentRequestsOneBuild(t *testing.T) {
	c := NewTimelineCache()
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	build := func(context.Context) (builtSession, error) {
		once.Do(func() { close(started) })
		<-release
		return builtSession{rows: 1}, nil
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := c.get(t.Context(), ident("k"), store.Version{ExCount: 1}, build); err != nil {
				t.Errorf("get: %v", err)
			}
		})
	}
	<-started
	close(release)
	wg.Wait()
	if got := c.builds.Load(); got != 1 {
		t.Fatalf("builds = %d for 16 concurrent requests, want 1", got)
	}
}

func fill(c *TimelineCache, key string, ver store.Version, bs builtSession) {
	_, _ = c.get(context.Background(), ident(key), ver, func(context.Context) (builtSession, error) { return bs, nil })
}

func TestTimelineCache_LRUEvictsOldestSession(t *testing.T) {
	c := newTimelineCache(t.Context(), 2, 1<<30)
	v := store.Version{ExCount: 1}
	fill(c, "a", v, builtSession{})
	fill(c, "b", v, builtSession{})
	if _, ok := c.lookup(ident("a"), v); !ok { // a is now the most recently used
		t.Fatal("a missing")
	}
	fill(c, "c", v, builtSession{})
	if c.len() != 2 {
		t.Fatalf("len = %d, want 2", c.len())
	}
	if _, ok := c.lookup(ident("b"), v); ok {
		t.Fatal("b, the least recently used, was kept")
	}
	for _, k := range []string{"a", "c"} {
		if _, ok := c.lookup(ident(k), v); !ok {
			t.Fatalf("%s evicted", k)
		}
	}
}

func TestTimelineCache_NewVersionReplacesOldOfSameSession(t *testing.T) {
	c := newTimelineCache(t.Context(), 4, 1<<30)
	fill(c, "a", store.Version{ExCount: 1}, builtSession{})
	fill(c, "a", store.Version{ExCount: 2}, builtSession{})
	if c.len() != 1 {
		t.Fatalf("len = %d: the older version of a session still counts", c.len())
	}
	if _, ok := c.lookup(ident("a"), store.Version{ExCount: 1}); ok {
		t.Fatal("stale version served")
	}
}

func TestTimelineCache_SizeCapEvicts(t *testing.T) {
	big := func() builtSession { return builtSession{rowID: map[string]int64{strings.Repeat("x", 1000): 1}} }
	one := estimateSize(big())
	if one < 1000 {
		t.Fatalf("estimate = %d", one)
	}
	c := newTimelineCache(t.Context(), 4, one*3/2) // room for one build, not two
	v := store.Version{ExCount: 1}
	fill(c, "a", v, big())
	fill(c, "b", v, big())
	if c.len() != 1 {
		t.Fatalf("len = %d, want 1 under the byte cap", c.len())
	}
	if _, ok := c.lookup(ident("b"), v); !ok {
		t.Fatal("newest build evicted")
	}
	if c.bytes > c.maxBytes {
		t.Fatalf("bytes = %d over cap %d", c.bytes, c.maxBytes)
	}
}

func TestDigestCache_TimedOutComputeIsCachedAndRetryHits(t *testing.T) {
	c := newDigestCache(8)
	release := make(chan struct{})
	compute := func(context.Context) (timeline.Digest, error) {
		<-release
		return timeline.Digest{Title: "t", ToolStates: map[string]int{"ok": 1}}, nil
	}
	k := digestKey{id: ident("k"), ver: store.Version{ExCount: 1}}
	timedOut, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.get(timedOut, k, compute); !errors.Is(err, context.Canceled) {
		t.Fatalf("timed-out request err = %v", err)
	}
	close(release)
	d, err := c.get(t.Context(), k, compute)
	if err != nil || d.Title != "t" {
		t.Fatalf("retry = %+v, %v", d, err)
	}
	if got := c.computes.Load(); got != 1 {
		t.Fatalf("computes = %d, want 1", got)
	}
	if c.len() != 1 {
		t.Fatal("digest not cached")
	}
}

func TestDigestCache_ConcurrentRequestsOneCompute(t *testing.T) {
	c := newDigestCache(8)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	compute := func(context.Context) (timeline.Digest, error) {
		once.Do(func() { close(started) })
		<-release
		return timeline.Digest{Title: "t", ToolStates: map[string]int{}}, nil
	}
	k := digestKey{id: ident("k"), ver: store.Version{ExCount: 1}}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := c.get(t.Context(), k, compute); err != nil {
				t.Errorf("get: %v", err)
			}
		})
	}
	<-started
	close(release)
	wg.Wait()
	if got := c.computes.Load(); got != 1 {
		t.Fatalf("computes = %d for 16 concurrent requests, want 1", got)
	}
}

func TestTimeline_ResponseCarriesVersionAndEventPosition(t *testing.T) {
	e := newTLEnv(t)
	e.call("a", 1, 0, 1, "user:hello")
	e.call("b", 2, 0, 1, "user:hello")
	who := ident("k")
	vers, err := e.d.Reader.WithContext(t.Context()).SessionVersions([]store.SessionIdent{who})
	if err != nil {
		t.Fatal(err)
	}
	v := vers[who]
	if v.ExCount != 2 || v.ExMaxRow == 0 {
		t.Fatalf("version = %+v", v)
	}
	type shape struct {
		Version       map[string]float64 `json:"version"`
		EventPosition map[string]float64 `json:"event_position"`
	}
	check := func(what, body string) {
		t.Helper()
		var s shape
		if err := json.Unmarshal([]byte(body), &s); err != nil {
			t.Fatal(err)
		}
		if s.Version["exchanges"] != 2 || s.Version["max_row"] != float64(v.ExMaxRow) || s.Version["parsed"] != 2 {
			t.Fatalf("%s version = %v", what, s.Version)
		}
		if s.EventPosition["row"] != float64(v.ExMaxRow) || s.EventPosition["hook"] != 0 {
			t.Fatalf("%s event_position = %v", what, s.EventPosition)
		}
	}
	check("timeline", e.get(sessionPath()).Body.String())
	check("summary", e.get("/api/sessions/"+who2id()).Body.String())
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(e.get("/api/sessions").Body.Bytes(), &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("list: %v", err)
	}
	check("list item", string(list.Items[0]))
}

func who2id() string { return sessIdent{core.PrincipalLocal, "cc", "k"}.id() }
