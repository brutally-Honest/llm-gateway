package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// These tests use computes that block until the test releases them, so a compute takes
// longer than the gap between the versions that arrive meanwhile. They assert behaviour
// only: who answered, from which version, and how many computes ran.

// noWait runs f and fails if it blocks: f must answer while a compute it could wait for
// is held. The timer only turns a hang into a failure; nothing asserts a duration.
func noWait[T any](t *testing.T, what string, f func() T) T {
	t.Helper()
	got := make(chan T, 1)
	go func() { got <- f() }()
	select {
	case v := <-got:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("%s waited for the held compute", what)
		panic("unreachable")
	}
}

func memoTitled(title string) digestMemo {
	return digestMemo{digest: timeline.Digest{Title: title, ToolStates: map[string]int{}}}
}

// A compute slower than the gap between versions: every reader during it gets the cached
// memo at once, one compute runs at a time, the burst costs one follow-up aimed at the
// newest version, and once it lands the next read is at that version.
func TestDigestCache_SlowComputeServesCachedOneAtATime(t *testing.T) {
	c := newDigestCache(8)
	id := ident("k")
	ver := func(n int) store.Version { return store.Version{ExCount: n, ExMaxRow: int64(n)} }
	if _, err := c.get(t.Context(), id, ver(1), func(context.Context) (digestMemo, error) {
		return memoTitled("v1"), nil
	}); err != nil {
		t.Fatal(err)
	}
	var running, most atomic.Int32
	var mu sync.Mutex
	var asked []int
	started, release := make(chan struct{}, 16), make(chan struct{})
	slow := func(n int) func(context.Context) (digestMemo, error) {
		return func(context.Context) (digestMemo, error) {
			r := running.Add(1)
			for {
				m := most.Load()
				if r <= m || most.CompareAndSwap(m, r) {
					break
				}
			}
			mu.Lock()
			asked = append(asked, n)
			mu.Unlock()
			started <- struct{}{}
			<-release
			running.Add(-1)
			return memoTitled(fmt.Sprintf("v%d", n)), nil
		}
	}
	for n := 2; n <= 8; n++ { // live versions arriving while the first recompute runs
		hit := noWait(t, fmt.Sprintf("read at v%d", n), func() digestHit {
			h, err := c.latest(t.Context(), id, ver(n), slow(n))
			if err != nil {
				t.Errorf("latest: %v", err)
			}
			return h
		})
		if hit.ver != ver(1) || hit.memo.digest.Title != "v1" {
			t.Fatalf("read at v%d = %q at %+v, want the cached v1", n, hit.memo.digest.Title, hit.ver)
		}
		if n == 2 {
			<-started
		}
	}
	close(release)
	settle(t, c)
	if got := most.Load(); got != 1 {
		t.Fatalf("%d computes ran at once, want 1", got)
	}
	mu.Lock()
	if !reflect.DeepEqual(asked, []int{2, 8}) {
		t.Fatalf("computes asked = %v, want the running one and one follow-up at the newest version", asked)
	}
	mu.Unlock()
	hit, err := c.latest(t.Context(), id, ver(8), slow(99))
	if err != nil || hit.ver != ver(8) || hit.memo.digest.Title != "v8" {
		t.Fatalf("after the writer stopped = %q at %+v, %v; want v8", hit.memo.digest.Title, hit.ver, err)
	}
	if got := c.computes.Load(); got != 3 {
		t.Fatalf("computes = %d, want 3 (v1, v2, the v8 follow-up)", got)
	}
}

// A memo computed on older data that finishes after a newer one never replaces it.
func TestDigestCache_OlderComputeDoesNotReplaceNewer(t *testing.T) {
	c := newDigestCache(8)
	id := ident("k")
	newer, older := store.Version{ExCount: 3, ExMaxRow: 3}, store.Version{ExCount: 2, ExMaxRow: 2}
	c.store(&digestEntry{id: id, ver: newer, memo: memoTitled("new")})
	c.store(&digestEntry{id: id, ver: older, memo: memoTitled("old")})
	e, ok := c.peek(id)
	if !ok || e.ver != newer || e.memo.digest.Title != "new" {
		t.Fatalf("cached = %+v, want the newer memo", e)
	}
}

// heldCompute occupies session k's compute slot at the store's current version with a
// compute that runs the real one only when release is closed.
func heldCompute(t *testing.T, d Deps, c *DigestCache) (release func()) {
	t.Helper()
	row := store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "k"}
	id := sessionIdent(row)
	vers, err := d.Reader.WithContext(t.Context()).SessionVersions([]store.SessionIdent{id})
	if err != nil {
		t.Fatal(err)
	}
	started, gate := make(chan struct{}), make(chan struct{})
	held := func(ctx context.Context) (digestMemo, error) {
		close(started)
		<-gate
		return d.digestCompute(row)(ctx)
	}
	if _, err := c.latest(t.Context(), id, vers[id], held); err != nil {
		t.Fatal(err)
	}
	<-started
	return func() { close(gate) }
}

func titleAndExchanges(t *testing.T, item map[string]any) (string, float64) {
	t.Helper()
	v, _ := item["version"].(map[string]any)
	return fmt.Sprint(item["title"]), v["exchanges"].(float64)
}

// While a session's digest recomputes, the list and the session summary answer from the
// cached digest with its version, never waiting; a write meanwhile queues one follow-up;
// once it lands the next read is at the store's version.
func TestSummary_ListAndSummaryAnswerFromCacheDuringSlowRecompute(t *testing.T) {
	e := newCacheEnv(t, 8)
	e.call("a", 1, 0, 0, "user:"+titlePrompt, `assistant:{"title":"T1"}`)
	e.summary() // cached at version 1
	e.call("b", 2, 0, 0, "user:"+titlePrompt, `assistant:{"title":"T2"}`)
	release := heldCompute(t, e.d, e.cache)

	read := func(what, path string) *httptest.ResponseRecorder {
		return noWait(t, what, func() *httptest.ResponseRecorder { return e.get(path) })
	}
	assertCached := func(what string, rows int) {
		t.Helper()
		if rec := read(what+" list", "/api/sessions"); rec.Code != 200 {
			t.Fatalf("%s list = %d %s", what, rec.Code, rec.Body)
		}
		p := e.page("/api/sessions")
		if title, n := titleAndExchanges(t, p.Items[0]); title != "T1" || n != 1 {
			t.Fatalf("%s list = %q at %v exchanges, want the cached T1 at 1", what, title, n)
		}
		rec := read(what+" summary", "/api/sessions/"+sid())
		if rec.Code != 200 {
			t.Fatalf("%s summary = %d %s", what, rec.Code, rec.Body)
		}
		var sum sessionSummary
		if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
			t.Fatal(err)
		}
		if sum.Title != "T1" || sum.Version == nil || sum.Version.Exchanges != 1 || sum.ExchangeCount != rows {
			t.Fatalf("%s summary = %q at %+v, %d exchanges; want the cached T1 at version 1 with the fresh row count",
				what, sum.Title, sum.Version, sum.ExchangeCount)
		}
	}
	assertCached("during the recompute", 2)
	e.call("c", 3, 0, 0, "user:"+titlePrompt, `assistant:{"title":"T3"}`) // the writer goes on
	assertCached("after another write", 3)

	release()
	settle(t, e.cache)
	title, n := titleAndExchanges(t, e.page("/api/sessions").Items[0])
	if title != "T3" || n != 3 {
		t.Fatalf("after the writer stopped = %q at %v exchanges, want T3 at 3", title, n)
	}
	if got := e.computes(); got != 3 {
		t.Fatalf("computes = %d, want 3 (first, held, one follow-up)", got)
	}
}

// The global stream's snapshot answers from the cached digest during a recompute, and the
// stream gets the recomputed summary once it lands, with no further commit.
func TestStream_GlobalSnapshotFromCacheThenCatchesUp(t *testing.T) {
	e := newStreamEnv(t, nil)
	e.commitA()
	e.reload() // cached at version 1
	e.saveToolExchange("b", 20)
	release := heldCompute(t, e.d, e.hub.Digests())

	s := noWait(t, "global snapshot", func() *sse { return e.openRaw("/api/stream") })
	first := noWait(t, "global snapshot event", func() sseEvent { return s.nextData() })
	if first.event != "session" {
		t.Fatalf("snapshot = %+v, want a session event", first)
	}
	if sum := sessionSummaries(t, []sseEvent{first})[0]; sum.Version == nil || sum.Version.Exchanges != 1 {
		t.Fatalf("snapshot version = %+v, want the cached version 1", sum.Version)
	}
	release()
	for {
		ev := s.nextData() // fails on its read timeout if no catch-up comes
		if ev.event != "session" {
			continue
		}
		if sum := sessionSummaries(t, []sseEvent{ev})[0]; sum.Version != nil && sum.Version.Exchanges == 2 {
			return
		}
	}
}

// The stats come from the memo with the digest: while a recompute is held, a summary
// shows the cached version's model calls, not a fresh count, so a list row costs no
// stats pass; once the recompute lands they are the store's.
func TestSummary_StatsComeFromTheMemo(t *testing.T) {
	e := newCacheEnv(t, 8)
	e.call("a", 1, 0, 1, "user:hello")
	e.summary() // cached at version 1
	e.call("b", 2, 0, 1, "user:hello", "assistant:ok", "user:next")
	release := heldCompute(t, e.d, e.cache)
	rec := noWait(t, "summary", func() *httptest.ResponseRecorder { return e.get("/api/sessions/" + sid()) })
	var sum sessionSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.ModelCalls != 1 || sum.Usage.Calls != 1 || sum.Version == nil || sum.Version.Exchanges != 1 {
		t.Fatalf("held summary: model calls %d, usage calls %d at %+v; want the memo's 1 at version 1",
			sum.ModelCalls, sum.Usage.Calls, sum.Version)
	}
	release()
	if s := e.summary(); s["model_calls"] != float64(2) {
		t.Fatalf("after the recompute: model calls = %v, want 2", s["model_calls"])
	}
}

// One memo compute reads each exchange's events once for the stats and the digest.
func TestComputeMemo_LoadsEventsOnce(t *testing.T) {
	e := withRules(t)
	e.call("a", 1, 1, 1, "user:start")
	e.call("b", 2, 0, 1, "user:start", "assistant:ok", "user:next")
	src := &countingSource{CtxReader: e.d.Reader.WithContext(t.Context()), reads: map[string]int{}}
	row := store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "k"}
	m, err := computeMemo(src, testRules{}, row)
	if err != nil {
		t.Fatal(err)
	}
	if m.stats.ModelCalls != 2 || m.stats.Errors != 1 || m.digest.MainErrors != 1 {
		t.Fatalf("memo = %+v", m)
	}
	if len(src.reads) != 2 || src.reads["a"] != 1 || src.reads["b"] != 1 {
		t.Fatalf("events reads = %v, want each exchange once", src.reads)
	}
}
