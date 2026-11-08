package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

type cacheEnv struct {
	*digestEnv
	cache *DigestCache
	now   time.Time
}

func newCacheEnv(t *testing.T, size int) *cacheEnv {
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
	e := &cacheEnv{cache: newDigestCache(size), now: baseTime.Add(100 * time.Second)}
	d := Deps{Addr: addr, Reader: r, Rules: map[string]ClientRules{"cc": testRules{}}, Digests: e.cache,
		Now: func() time.Time { return e.now }}
	e.digestEnv = &digestEnv{apiEnv: &apiEnv{t: t, s: s, dir: dir, h: Handler(d)}, d: d}
	return e
}

func (e *cacheEnv) computes() int64 { return e.cache.computes.Load() }

func TestDigestCache_UnchangedSessionNotRecomputed(t *testing.T) {
	e := newCacheEnv(t, 8)
	e.call("a", 1, 0, 0, "user:hello")
	first := e.summary()
	for range 3 {
		e.summary()
	}
	if got := e.computes(); got != 1 {
		t.Fatalf("computes = %d, want 1", got)
	}
	if first["title"] != "hello" {
		t.Fatalf("title = %v", first["title"])
	}
}

func TestDigestCache_ChangedSessionRecomputed(t *testing.T) {
	e := newCacheEnv(t, 8)
	e.call("a", 1, 0, 0, "user:hello")
	e.summary()
	want := int64(1)
	step := func(name string, write func()) {
		t.Helper()
		write()
		e.summary()
		want++
		if got := e.computes(); got != want {
			t.Fatalf("%s: computes = %d, want %d", name, got, want)
		}
	}
	step("new exchange", func() { e.call("b", 2, 1, 0, "user:again") })
	step("backfilled exchange", func() {
		e.save("c", "", "cc", 3)
		if _, err := e.s.SetSessionKeys(t.Context(), []store.KeyAssignment{{RequestID: "c", Key: "k"}}); err != nil {
			t.Fatal(err)
		}
	})
	step("new hook line", func() { e.hook("k", core.HarnessEvent{Phase: core.PhaseNotice}, 4) })
	e.summary()
	if got := e.computes(); got != want {
		t.Fatalf("unchanged read recomputed: %d, want %d", got, want)
	}
}

func TestDigestCache_ParseLandingRecomputes(t *testing.T) {
	e := newCacheEnv(t, 8)
	e.save("a", "k", "cc", 1) // stored with no parse yet
	s := e.summary()
	if s["title"] != "" {
		t.Fatalf("title before parse = %v", s["title"])
	}
	mh, bh := sha("m-hello"), sha("body-hello")
	raw, _ := json.Marshal(map[string]string{"type": "text", "text": "hello parse"})
	p, _ := json.Marshal(map[string]any{"role": "user", "blocks": []map[string]string{{"hash": bh, "type": "text"}}})
	evs := []core.StoredEvent{
		{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)},
		{Seq: 1, Kind: core.KindMessage, ContentHash: mh, Payload: p},
		{Seq: 2, Kind: core.KindToolCall, ToolCallID: "t1", ContentHash: sha("in-t1"), Payload: []byte(`{"name":"bash","id":"t1"}`)},
	}
	if err := e.s.SaveParse(t.Context(), "a", core.PrincipalLocal, core.ParseOK, evs,
		[]core.Content{{Hash: bh, Bytes: raw}, {Hash: mh, Bytes: []byte(mh)}, {Hash: sha("in-t1"), Bytes: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	s = e.summary()
	if s["title"] != "hello parse" {
		t.Fatalf("title after parse = %v", s["title"])
	}
	if st := s["tool_states"].(map[string]any); st["no-hooks"] != float64(1) {
		t.Fatalf("tool_states = %v", st)
	}
	if got := e.computes(); got != 2 {
		t.Fatalf("computes = %d, want 2", got)
	}
}

func TestDigestCache_ActiveFlagNotMemoised(t *testing.T) {
	e := newCacheEnv(t, 8)
	e.call("a", 1, 0, 0, "user:hello")
	if e.summary()["active"] != true {
		t.Fatal("not active at base+100s")
	}
	e.now = baseTime.Add(time.Hour)
	if e.summary()["active"] != false {
		t.Fatal("still active an hour later: clock value was memoised")
	}
	if got := e.computes(); got != 1 {
		t.Fatalf("computes = %d, want 1", got)
	}
}

func TestDigestCache_BoundedAndRaceFree(t *testing.T) {
	e := newCacheEnv(t, 2)
	ids := []string{"a", "b", "c", "d"}
	for i, id := range ids {
		e.save(id, "k"+id, "cc", i+1)
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 15 {
				if g%2 == 0 {
					if rec := do(t, e.h, "GET", "/api/sessions", nil); rec.Code != 200 {
						t.Errorf("list = %d", rec.Code)
						return
					}
					continue
				}
				who := sessIdent{core.PrincipalLocal, "cc", "k" + ids[g%len(ids)]}
				if _, err := readLive(t.Context(), e.d, who, nil, true, false); err != nil {
					t.Errorf("readLive: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if n := e.cache.len(); n > 2 {
		t.Fatalf("cache holds %d entries, bound is 2", n)
	}
}

func TestDigestCache_ProductionBound(t *testing.T) {
	if c := NewDigestCache(context.Background(), nil); c.max != 512 {
		t.Fatalf("max = %d", c.max)
	}
}

func serve(h http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "http://"+addr+"/api/sessions?limit=50", nil))
	return rec
}

func BenchmarkSessionList_WarmCache(b *testing.B) {
	dir := b.TempDir()
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	r, err := store.OpenReader(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	cache := NewDigestCache(context.Background(), nil)
	d := Deps{Addr: addr, Reader: r, Rules: map[string]ClientRules{"cc": testRules{}}, Digests: cache,
		Now: func() time.Time { return baseTime.Add(100 * time.Second) }}
	h := Handler(d)
	e := &apiEnv{s: s}
	for i := range 50 {
		ex := &core.Exchange{RequestID: "r" + string(rune('A'+i/26)) + string(rune('a'+i%26)), PrincipalID: core.PrincipalLocal,
			Protocol: "test", Client: "cc", Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/x", Status: 200,
			SessionKey: "k" + string(rune('A'+i/26)) + string(rune('a'+i%26)), Start: baseTime.Add(time.Duration(i) * time.Second)}
		ex.End = ex.Start.Add(time.Second)
		if err := e.s.SaveExchange(b.Context(), ex); err != nil {
			b.Fatal(err)
		}
	}
	if rec := serve(h); rec.Code != 200 {
		b.Fatalf("warm-up = %d", rec.Code)
	}
	if cache.len() != 50 {
		b.Fatalf("warm cache holds %d", cache.len())
	}
	b.ResetTimer()
	for range b.N {
		serve(h)
	}
}

// A hub reads through the digest cache it is given rather than making its own.
func TestHub_KeepsGivenDigestCache(t *testing.T) {
	c := NewDigestCache(t.Context(), nil)
	if got := newHub(Deps{Digests: c}, 1).Digests(); got != c {
		t.Fatalf("hub cache %p, want %p", got, c)
	}
}

// settle waits until c has no digest compute running or queued. It waits on the runs'
// own done channels; the timer only turns a hang into a failure.
func settle(t *testing.T, c *DigestCache) {
	t.Helper()
	for {
		c.fmu.Lock()
		var r *digestRun
		for _, rs := range c.active {
			r = rs.cur
			break
		}
		c.fmu.Unlock()
		if r == nil {
			return
		}
		select {
		case <-r.done:
		case <-time.After(10 * time.Second):
			t.Fatal("a digest compute did not end")
		}
	}
}
