package ui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// A hub reads through the cache it is given rather than making its own.
func TestHub_KeepsGivenTimelineCache(t *testing.T) {
	c := NewTimelineCache(t.Context(), nil)
	if got := newHub(Deps{Timelines: c}, 1).Timelines(); got != c {
		t.Fatalf("hub cache %p, want %p", got, c)
	}
}

// A burst of version changes during a build asks for exactly one follow-up build, aimed
// at the newest version asked.
func TestLiveBuild_BurstDuringBuildOneFollowUp(t *testing.T) {
	c := NewTimelineCache(context.Background(), nil)
	fill(c, "k", store.Version{ExCount: 1}, builtSession{rows: 1})
	started, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var seen []int
	build := func(rows int) func(context.Context) (builtSession, error) {
		return func(context.Context) (builtSession, error) {
			mu.Lock()
			seen = append(seen, rows)
			first := len(seen) == 1
			mu.Unlock()
			if first {
				close(started)
				<-release
			}
			return builtSession{rows: rows}, nil
		}
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, err := c.get(t.Context(), ident("k"), store.Version{ExCount: 2}, build(2)); err != nil {
			t.Errorf("get: %v", err)
		}
	})
	<-started
	for n := 3; n <= 8; n++ { // live events while the build runs
		b, err := c.latest(t.Context(), ident("k"), store.Version{ExCount: n}, build(n))
		if err != nil || b.bs.rows != 1 {
			t.Fatalf("burst reader = %+v, %v; want the cached build", b, err)
		}
	}
	close(release)
	wg.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := c.lookup(ident("k"), store.Version{ExCount: 8}); ok {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(seen, []int{2, 8}) {
		t.Fatalf("builds = %v, want the running one and one follow-up for the newest version", seen)
	}
	if _, ok := c.lookup(ident("k"), store.Version{ExCount: 8}); !ok {
		t.Fatal("follow-up not cached")
	}
}

// While a rebuild runs, a reader gets the cached older build at once; a fresh reader
// (the stream) waits for the build it needs.
func TestLiveBuild_RequestDuringRebuildGetsOlderBuildWithoutWaiting(t *testing.T) {
	c := NewTimelineCache(context.Background(), nil)
	v1, v2 := store.Version{ExCount: 1}, store.Version{ExCount: 2}
	fill(c, "k", v1, builtSession{rows: 1})
	started, release := make(chan struct{}), make(chan struct{})
	build := func(context.Context) (builtSession, error) {
		close(started)
		<-release
		return builtSession{rows: 2}, nil
	}
	done := make(chan *cachedBuild, 1)
	go func() {
		b, _ := c.get(t.Context(), ident("k"), v2, build)
		done <- b
	}()
	<-started
	got := make(chan *cachedBuild, 1)
	go func() {
		b, _ := c.latest(t.Context(), ident("k"), v2, build)
		got <- b
	}()
	select {
	case b := <-got:
		if b.bs.rows != 1 || b.ver != v1 {
			t.Fatalf("reader got rows=%d ver=%+v, want the older build", b.bs.rows, b.ver)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader waited for the rebuild")
	}
	select {
	case <-done:
		t.Fatal("the fresh reader returned before the rebuild ended")
	default:
	}
	close(release)
	if b := <-done; b == nil || b.bs.rows != 2 {
		t.Fatalf("fresh reader = %+v, want the rebuilt one", b)
	}
	if b, err := c.latest(t.Context(), ident("k"), v2, build); err != nil || b.bs.rows != 2 {
		t.Fatalf("after the rebuild = %+v, %v", b, err)
	}
}

// A stale entry with no build running starts a rebuild and is served at once; the rebuild
// then lands in the cache for the next reader.
func TestLiveBuild_StaleWithNoBuildRunningServesStaleAndRebuilds(t *testing.T) {
	c := NewTimelineCache(t.Context(), nil)
	v1, v2 := store.Version{ExCount: 1}, store.Version{ExCount: 2}
	fill(c, "k", v1, builtSession{rows: 1})
	started, release := make(chan struct{}), make(chan struct{})
	builds := 0
	build := func(context.Context) (builtSession, error) {
		builds++
		close(started)
		<-release
		return builtSession{rows: 2}, nil
	}
	stale := make(chan *cachedBuild, 1)
	go func() {
		b, _ := c.latest(t.Context(), ident("k"), v2, build)
		stale <- b
	}()
	<-started // the rebuild did start
	select {
	case b := <-stale:
		if b == nil || b.ver != v1 || b.bs.rows != 1 {
			t.Fatalf("latest = %+v, want the stale build", b)
		}
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("the reader waited for the rebuild")
	}
	close(release)
	got, err := c.get(t.Context(), ident("k"), v2, build) // joins the same run
	if err != nil || got.bs.rows != 2 {
		t.Fatalf("after the rebuild = %+v, %v", got, err)
	}
	if builds != 1 {
		t.Fatalf("builds = %d, want 1", builds)
	}
}

// With nothing cached a reader has nothing to be served, so it waits for the first build.
func TestLiveBuild_NothingCachedWaitsForFirstBuild(t *testing.T) {
	c := NewTimelineCache(t.Context(), nil)
	started, release := make(chan struct{}), make(chan struct{})
	got := make(chan *cachedBuild, 1)
	go func() {
		b, _ := c.latest(t.Context(), ident("k"), store.Version{ExCount: 1}, func(context.Context) (builtSession, error) {
			close(started)
			<-release
			return builtSession{rows: 1}, nil
		})
		got <- b
	}()
	<-started
	select {
	case b := <-got:
		t.Fatalf("returned %+v before the build ended", b)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if b := <-got; b == nil || b.bs.rows != 1 {
		t.Fatalf("latest = %+v, want the first build", b)
	}
}

// A rebuild that fails behind a stale read is logged, nothing replaces the stale entry,
// and the next read tries again.
func TestLiveBuild_FailedRebuildIsLoggedAndKeepsStale(t *testing.T) {
	obs, logs := observer.New(zap.WarnLevel)
	c := NewTimelineCache(t.Context(), zap.New(obs))
	v1, v2 := store.Version{ExCount: 1}, store.Version{ExCount: 2}
	fill(c, "k", v1, builtSession{rows: 1})
	failed := make(chan struct{})
	b, err := c.latest(t.Context(), ident("k"), v2, func(context.Context) (builtSession, error) {
		defer close(failed)
		return builtSession{}, errors.New("store down")
	})
	if err != nil || b.ver != v1 {
		t.Fatalf("latest = %+v, %v; want the stale build", b, err)
	}
	<-failed
	deadline := time.Now().Add(5 * time.Second)
	for logs.FilterMessage("ui timeline build failed").Len() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the failed rebuild was not logged")
		}
		time.Sleep(time.Millisecond)
	}
	if got, ok := c.peek(ident("k")); !ok || got.ver != v1 {
		t.Fatalf("cached = %+v, %v; want the stale build kept", got, ok)
	}
}

func timelineOver(t *testing.T, e *streamEnv, path string) tlPage {
	t.Helper()
	resp, err := http.Get(e.ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var p tlPage
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

// Pages read before a commit, the stream event for the commit and pages read after it
// add up to the final idle build.
func TestLiveBuild_PagedPlusStreamEqualsFinalIdleBuild(t *testing.T) {
	e := newStreamEnv(t, nil)
	e.seedThree()
	e.publish("a", "b", "c")
	s := e.open(e.sessionPath())
	s.take(3) // the snapshot

	view := map[string][]map[string]any{}
	order := func() []map[string]any {
		var evs []sseEvent
		for _, es := range view {
			b, _ := json.Marshal(es)
			evs = append(evs, sseEvent{event: "exchange", data: string(b)})
		}
		return applyEvents(evs)
	}
	take := func(p tlPage) {
		for _, en := range p.Entries {
			id := en["exchange_id"].(string)
			replaced := false
			for i, have := range view[id] {
				if have["id"] == en["id"] {
					view[id][i], replaced = en, true
				}
			}
			if !replaced {
				view[id] = append(view[id], en)
			}
		}
	}
	first := timelineOver(t, e, sessionPath()+"?limit=1")
	take(first)
	if first.NextAfter == nil {
		t.Fatal("want more pages")
	}
	e.save("d", "k", "cc", 40) // a live commit while the reader is mid-paging
	e.parseMsgs("d", "ok", []any{"user", text("q4")})
	e.publish("d")
	ev := s.nextData()
	if ev.event != "entries" {
		t.Fatalf("live event = %+v", ev)
	}
	var live []map[string]any
	if err := json.Unmarshal([]byte(ev.data), &live); err != nil {
		t.Fatal(err)
	}
	view[live[0]["exchange_id"].(string)] = live
	for next := first.NextAfter; next != nil; {
		p := timelineOver(t, e, sessionPath()+"?limit=1&after="+*next)
		take(p)
		next = p.NextAfter
	}
	if got, want := order(), e.freshTimeline(); !reflect.DeepEqual(got, want) {
		t.Fatalf("paged + stream = %v\nidle build = %v", got, want)
	}
}

// A stream resumes from the position of the snapshot it sent: a live message built from
// less of the store than that is dropped, one built from as much or more goes through.
func TestStream_ResumesFromBuildPosition(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	e := newStreamEnv(t, func(h *Hub) {
		h.read = func(context.Context, sessIdent, []string, bool, bool) (liveMsg, error) {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			x := liveExchange{RowID: int64(n), RequestID: "x", Hash: string(rune('0' + n)),
				Entries: []entryJSON{{ID: "x", ExchangeID: "x", Start: "s"}}}
			pos := eventPositionJSON{Row: 3}
			if n == 1 {
				pos = eventPositionJSON{Row: 1} // an older build than the snapshot's
			}
			return liveMsg{Exchanges: []liveExchange{x}, All: []liveExchange{x}, Position: pos}, nil
		}
	})
	e.seedThree()
	s := e.openRaw(e.sessionPath())
	first := s.next()
	var sum struct {
		Position eventPositionJSON `json:"event_position"`
	}
	if first.event != "session" || json.Unmarshal([]byte(first.data), &sum) != nil || sum.Position.Row != 3 {
		t.Fatalf("snapshot session event = %+v (position %+v), want row 3", first, sum.Position)
	}
	s.skipSession, s.skipEmptyHooks = true, true
	s.take(3)
	e.publish("a") // read 1: built from row 1, behind the snapshot
	for {
		mu.Lock()
		n := calls
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	e.publish("b") // read 2: built from row 3
	ev := s.nextData()
	if ev.event != "entries" || ev.id != "2" {
		t.Fatalf("event = %+v, want the second read's entries (the first was behind the snapshot)", ev)
	}
}
