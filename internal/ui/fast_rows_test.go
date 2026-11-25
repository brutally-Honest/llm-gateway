package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// Spec 043: /api/sessions never waits for a build. These tests hold builds back with
// buildStub and check what the list answers meanwhile.

// buildStub replaces computeMemoFn: builds wait while held, are recorded, and a session in
// look gets one item that needs a look; every memo carries a title naming its session, so a
// row showing another session's facts is seen.
type buildStub struct {
	mu         sync.Mutex
	open       chan struct{} // closed when builds may proceed
	started    []string
	running    int
	maxRunning int
	look       map[string]bool
	count      atomic.Int32
}

// newBuildStub holds every build of the cache c until hold(false). Its cleanup runs before
// the env's: it lets the builds end, waits for them, and restores computeMemoFn.
func newBuildStub(t *testing.T, c *DigestCache) *buildStub {
	t.Helper()
	s := &buildStub{open: make(chan struct{}), look: map[string]bool{}}
	real := computeMemoFn
	computeMemoFn = func(src memoSource, vd viewDeps, row store.SessionRow) (digestMemo, error) {
		s.mu.Lock()
		s.started = append(s.started, row.SessionKey)
		s.running++
		s.maxRunning = max(s.maxRunning, s.running)
		ch := s.open
		s.mu.Unlock()
		s.count.Add(1)
		defer func() { s.mu.Lock(); s.running--; s.mu.Unlock() }()
		select {
		case <-ch:
		case <-time.After(30 * time.Second):
			return digestMemo{}, fmt.Errorf("test: build never released")
		}
		m, err := real(src, vd, row)
		m.row.title = "title-" + row.SessionKey
		if s.look[row.SessionKey] {
			m.row.look = 1
		}
		return m, err
	}
	t.Cleanup(func() {
		s.hold(false)
		settle(t, c)
		computeMemoFn = real
	})
	return s
}

func (s *buildStub) hold(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.open:
		if on {
			s.open = make(chan struct{})
		}
	default:
		if !on {
			close(s.open)
		}
	}
}

func (s *buildStub) startedKeys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.started...)
}

// answersWithin fails when f takes a second or more: with every build held, any wait is a hang.
func answersWithin(t *testing.T, what string, f func()) {
	t.Helper()
	start := time.Now()
	f()
	if d := time.Since(start); d > time.Second {
		t.Errorf("%s took %v, want an answer without waiting for a build", what, d)
	}
}

var pendingRowKeys = []string{"active", "client", "effects", "exchange_count", "first_at", "has_calls", "hooks_only",
	"id", "last_at", "list", "pending", "row_version", "session_key"}

// assertPendingShape checks a pending row's own shape: the cheap keys, no heavy one.
func assertPendingShape(t *testing.T, it map[string]any) {
	t.Helper()
	var got []string
	for k := range it {
		got = append(got, k)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, pendingRowKeys) {
		t.Errorf("pending row keys = %v, want %v", got, pendingRowKeys)
	}
	list, _ := it["list"].(map[string]any)
	if len(list) != 2 || list["folder"] == nil || list["folder_full"] == nil {
		t.Errorf("pending list = %v, want folder and folder_full only", it["list"])
	}
	if it["pending"] != true || it["row_version"] == nil {
		t.Errorf("pending row = %v", it)
	}
}

func isPending(it map[string]any) bool { return it["pending"] == true }

func (e *cacheEnv) seedSessions(n int) {
	for i := range n {
		e.save(fmt.Sprintf("r%d", i), fmt.Sprintf("s%03d", i), "cc", i+1)
	}
}

// settled asks for query until no row is pending and nothing is being checked.
func (e *cacheEnv) settled(query string) sessPage { return e.sess(query) }

// AC1: with an empty cache and every build held, page 1, 2 and 5 answer at once with
// pending rows of the pending shape.
func TestFastRows_EmptyCachePagesAnswerAtOnce(t *testing.T) {
	e := newCacheEnv(t, 64)
	stub := newBuildStub(t, e.cache)
	stub.hold(true)
	e.seedSessions(60)
	e.hook("hk", core.HarnessEvent{Phase: core.PhaseNotice, WorkDir: "/work/projx"}, 90)

	var after string
	for name, query := range map[string]string{
		"page 1": "?limit=10", "page 2": "?limit=10&offset=10", "page 5": "?limit=10&offset=40",
	} {
		var p sessPage
		answersWithin(t, name, func() { p = e.sessNow(query) })
		if len(p.Items) != 10 {
			t.Fatalf("%s: %d rows", name, len(p.Items))
		}
		for _, it := range p.Items {
			assertPendingShape(t, it)
		}
		if name == "page 1" {
			if p.NextAfter == nil {
				t.Fatal("page 1 has no next_after")
			}
			after = *p.NextAfter
		}
	}
	var p sessPage
	answersWithin(t, "after page", func() { p = e.sessNow("?limit=10&after=" + url.QueryEscape(after)) })
	if len(p.Items) != 10 || !isPending(p.Items[0]) {
		t.Fatalf("after page = %v", p.keys())
	}

	// the hooks-only session shows its folder and no call
	first := e.sessNow("?limit=1").Items[0]
	if first["session_key"] != "hk" || first["hooks_only"] != true || first["has_calls"] != false {
		t.Fatalf("hooks-only row = %v", first)
	}
	if l := first["list"].(map[string]any); l["folder_full"] != "/work/projx" || l["folder"] != "/work/projx" {
		t.Errorf("folder = %v", l)
	}
	if first["exchange_count"] != 0.0 || first["effects"] == nil {
		t.Errorf("cheap fields = %v", first)
	}
}

// AC1, AC3: once the builds are let go, the same request returns the same rows with their
// facts, not pending, in the same order and with the same next_after.
func TestFastRows_FactsFillIn(t *testing.T) {
	e := newCacheEnv(t, 64)
	stub := newBuildStub(t, e.cache)
	stub.hold(true)
	e.seedSessions(12)
	before := e.sessNow("?limit=5")
	for _, it := range before.Items {
		assertPendingShape(t, it)
	}
	stub.hold(false)
	after := e.settled("?limit=5")
	if !reflect.DeepEqual(before.keys(), after.keys()) {
		t.Errorf("order changed: %v then %v", before.keys(), after.keys())
	}
	if before.NextAfter == nil || after.NextAfter == nil || *before.NextAfter != *after.NextAfter {
		t.Errorf("next_after %v then %v", before.NextAfter, after.NextAfter)
	}
	for i, it := range after.Items {
		if isPending(it) || it["version"] == nil || it["title"] != "title-"+after.keys()[i] || it["models"] == nil {
			t.Errorf("row %d has no facts: %v", i, it)
		}
		if it["row_version"] == nil || !reflect.DeepEqual(it["row_version"], before.Items[i]["row_version"]) {
			t.Errorf("row_version %v then %v", before.Items[i]["row_version"], it["row_version"])
		}
		if _, has := it["pending"]; has {
			t.Errorf("a full row carries pending: %v", it)
		}
	}
}

// AC2: a memo on disk at the row's current key is served with its facts on the first
// request, with no compute.
func TestFastRows_DiskHitServedAtOnce(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	warm := e.mustGetReady(listPath(0))
	e.flush()
	if len(e.files("m")) == 0 {
		t.Fatal("no memo on disk")
	}
	e.restart("v1")
	var n atomic.Int32
	real := computeMemoFn
	computeMemoFn = func(src memoSource, vd viewDeps, row store.SessionRow) (digestMemo, error) {
		n.Add(1)
		return real(src, vd, row)
	}
	t.Cleanup(func() { settle(t, e.dep.Digests); computeMemoFn = real })
	got := e.mustGet(listPath(0))
	var p sessPage
	if err := json.Unmarshal(got, &p); err != nil {
		t.Fatal(err)
	}
	for _, it := range p.Items {
		if isPending(it) || it["version"] == nil || it["title"] == nil {
			t.Errorf("a disk hit was served without its facts: %v", it)
		}
	}
	if string(got) != string(warm) {
		t.Errorf("after restart:\n%s\nbefore:\n%s", got, warm)
	}
	if n.Load() != 0 || e.computes() != 0 {
		t.Errorf("computes = %d (%d), want none", n.Load(), e.computes())
	}
	// the memo is in memory now, so a second request needs no disk read either
	if _, ok := e.dep.Digests.peek(ident(p.keys()[0])); !ok {
		t.Error("the disk memo was not kept in memory")
	}
}

// AC4: kind=active and kind=hooks are exact with nothing built; a q that matches a cheap
// field returns pending rows; a q matching only a title is counted in checking.
func TestFastRows_FiltersWithNothingBuilt(t *testing.T) {
	e := newCacheEnv(t, 64)
	stub := newBuildStub(t, e.cache)
	stub.hold(true)
	for i := range 4 {
		e.save(fmt.Sprintf("r%d", i), fmt.Sprintf("sess%d", i), "cc", i+1)
	}
	e.save("old", "ancient", "cc", -1000)
	e.hook("hk", core.HarnessEvent{Phase: core.PhaseNotice, WorkDir: "/work/projx"}, 50)
	// rows: hk (50 s), sess3 .. sess0, ancient

	var p sessPage
	answersWithin(t, "kind=active", func() { p = e.sessNow("?kind=active") })
	if fmt.Sprint(p.keys()) != "[hk sess3 sess2 sess1 sess0]" || p.Checking != 0 {
		t.Errorf("active = %v checking %d", p.keys(), p.Checking)
	}
	answersWithin(t, "kind=hooks", func() { p = e.sessNow("?kind=hooks") })
	if fmt.Sprint(p.keys()) != "[hk]" || p.Checking != 0 || !isPending(p.Items[0]) {
		t.Errorf("hooks = %v checking %d", p.keys(), p.Checking)
	}
	answersWithin(t, "q folder", func() { p = e.sessNow("?q=PROJX") })
	if fmt.Sprint(p.keys()) != "[hk]" || !isPending(p.Items[0]) || p.Checking != 5 || p.Scanned != 6 {
		t.Errorf("q folder = %v checking %d scanned %d", p.keys(), p.Checking, p.Scanned)
	}
	answersWithin(t, "q key", func() { p = e.sessNow("?q=sess2") })
	if fmt.Sprint(p.keys()) != "[sess2]" || !isPending(p.Items[0]) || p.Checking != 5 {
		t.Errorf("q key = %v checking %d", p.keys(), p.Checking)
	}
	answersWithin(t, "q title", func() { p = e.sessNow("?q=title-sess1") })
	if len(p.Items) != 0 || p.Checking != 6 {
		t.Errorf("q title = %v checking %d, want none and 6 being checked", p.keys(), p.Checking)
	}
	answersWithin(t, "kind=active and q", func() { p = e.sessNow("?kind=active&q=title-sess1") })
	if len(p.Items) != 0 || p.Checking != 5 {
		t.Errorf("active+q = %v checking %d, want the 5 active rows checked", p.keys(), p.Checking)
	}
	// kind=active builds are queued only for the rows that can match
	stub.hold(false)
	if p = e.settled("?q=title-sess1"); fmt.Sprint(p.keys()) != "[sess1]" || p.Checking != 0 {
		t.Errorf("q title after the builds = %v checking %d", p.keys(), p.Checking)
	}
}

// versionsSpy replaces sessionVersionsFn and records each call's keys, by whether it was
// the deferred read after a filtered answer.
type versionsSpy struct {
	mu       sync.Mutex
	request  [][]string
	deferred [][]string
}

func spyVersions(t *testing.T) *versionsSpy {
	t.Helper()
	s := &versionsSpy{}
	real := sessionVersionsFn
	sessionVersionsFn = func(c *store.CtxReader, ids []store.SessionIdent, deferred bool) (map[store.SessionIdent]store.Version, error) {
		keys := make([]string, len(ids))
		for i, id := range ids {
			keys[i] = id.Key
		}
		s.mu.Lock()
		if deferred {
			s.deferred = append(s.deferred, keys)
		} else {
			s.request = append(s.request, keys)
		}
		s.mu.Unlock()
		return real(c, ids, deferred)
	}
	t.Cleanup(func() { sessionVersionsFn = real })
	return s
}

func (s *versionsSpy) counts() (request, deferred int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.request {
		request += len(k)
	}
	for _, k := range s.deferred {
		deferred += len(k)
	}
	return
}

// D18: a filtered request reads versions only for the rows it may return. Undecided rows
// (nothing in memory, nothing on disk, no cheap match) are counted in checking, and their
// versions are read and their builds queued after the answer, not during it.
func TestFastRows_UndecidedRowsReadVersionsAfterTheAnswer(t *testing.T) {
	e := newCacheEnv(t, 64)
	stub := newBuildStub(t, e.cache)
	stub.look["s003"] = true
	stub.hold(true)
	e.seedSessions(6)
	spy := spyVersions(t)

	var p sessPage
	answersWithin(t, "kind=look and a title q", func() { p = e.sessNow("?kind=look&q=title-s") })
	if len(p.Items) != 0 || p.Checking != 6 || p.Scanned != 6 {
		t.Fatalf("look+q = %v checking %d scanned %d, want none, 6 and 6", p.keys(), p.Checking, p.Scanned)
	}
	deadline := time.Now().Add(5 * time.Second)
	for stub.count.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the undecided rows' builds were never queued")
		}
		time.Sleep(2 * time.Millisecond)
	}
	req, def := spy.counts()
	if req != 0 || def != 6 {
		t.Errorf("version reads: %d during the request, %d after, want 0 and 6", req, def)
	}
	// the builds queue in row order: the first started are the newest rows
	got := stub.startedKeys()
	sort.Sort(sort.Reverse(sort.StringSlice(got)))
	if want := []string{"s005", "s004", "s003", "s002", "s001", "s000"}; len(got) == 0 || !reflect.DeepEqual(got, want[:len(got)]) {
		t.Errorf("started builds = %v, want the first rows of the request", got)
	}
	stub.hold(false)
	if p = e.settled("?kind=look&q=title-s"); fmt.Sprint(p.keys()) != "[s003]" || p.Checking != 0 {
		t.Errorf("after the builds = %v checking %d", p.keys(), p.Checking)
	}

	// a q that matches a cheap field decides its row: only that row is read in the request
	e2 := newCacheEnv(t, 64)
	stub2 := newBuildStub(t, e2.cache)
	stub2.hold(true)
	e2.seedSessions(6)
	spy2 := spyVersions(t)
	answersWithin(t, "q key", func() { p = e2.sessNow("?q=s002") })
	if fmt.Sprint(p.keys()) != "[s002]" || p.Checking != 5 || !isPending(p.Items[0]) {
		t.Fatalf("q key = %v checking %d", p.keys(), p.Checking)
	}
	for time.Now().Before(deadline.Add(5 * time.Second)) {
		if _, def := spy2.counts(); def == 5 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	spy2.mu.Lock()
	defer spy2.mu.Unlock()
	if len(spy2.request) != 1 || fmt.Sprint(spy2.request[0]) != "[s002]" || len(spy2.deferred) != 1 || len(spy2.deferred[0]) != 5 {
		t.Errorf("reads: request %v, deferred %v, want [[s002]] and one read of 5", spy2.request, spy2.deferred)
	}
}

// AC4: with some facts built and some held, kind=look returns exactly the known matches
// and counts the undecided rows scanned.
func TestFastRows_LookFilterPartlyKnown(t *testing.T) {
	e := newCacheEnv(t, 64)
	e.cache.gate.slots = 10 // every build starts at once, the held ones stay held
	stub := newBuildStub(t, e.cache)
	stub.look["s000"], stub.look["s002"], stub.look["s004"] = true, true, true
	e.seedSessions(6)
	// build s000..s002 only: hold the others by keeping the whole stub open for them
	real := computeMemoFn
	var heldOpen = make(chan struct{})
	computeMemoFn = func(src memoSource, vd viewDeps, row store.SessionRow) (digestMemo, error) {
		if row.SessionKey > "s002" {
			select {
			case <-heldOpen:
			case <-time.After(30 * time.Second):
			}
		}
		return real(src, vd, row)
	}
	t.Cleanup(func() { close(heldOpen) })
	stub.hold(false)
	e.sessNow("") // queues every row
	deadline := time.Now().Add(5 * time.Second)
	for e.cache.len() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d memos built", e.cache.len())
		}
		time.Sleep(2 * time.Millisecond)
	}

	var p sessPage
	answersWithin(t, "kind=look", func() { p = e.sessNow("?kind=look") })
	if fmt.Sprint(p.keys()) != "[s002 s000]" || p.Checking != 3 || p.Scanned != 6 {
		t.Errorf("look = %v checking %d scanned %d, want [s002 s000], 3 and 6", p.keys(), p.Checking, p.Scanned)
	}
	for _, it := range p.Items {
		if isPending(it) || it["list"].(map[string]any)["needs_look"] != 1.0 {
			t.Errorf("look row = %v", it)
		}
	}
}

// E2, AC1: when the store moves after a build, the served row keeps the facts' own
// version, row_version is the current one, and no session wears another's facts.
func TestFastRows_VersionsAndSessionsNeverMix(t *testing.T) {
	e := newCacheEnv(t, 64)
	stub := newBuildStub(t, e.cache)
	stub.hold(false)
	e.save("a1", "A", "cc", 10)
	e.save("b1", "B", "cc", 20)
	p := e.settled("")
	if fmt.Sprint(p.keys()) != "[B A]" {
		t.Fatalf("rows = %v", p.keys())
	}
	stub.hold(true)
	e.save("a2", "A", "cc", 30) // the store moves: A is newest now
	p = e.sessNow("")
	if fmt.Sprint(p.keys()) != "[A B]" {
		t.Fatalf("rows = %v", p.keys())
	}
	a, b := p.Items[0], p.Items[1]
	if a["title"] != "title-A" || b["title"] != "title-B" {
		t.Errorf("titles = %v and %v", a["title"], b["title"])
	}
	if a["version"].(map[string]any)["exchanges"] != 1.0 || a["row_version"].(map[string]any)["exchanges"] != 2.0 {
		t.Errorf("A: version %v row_version %v, want facts at 1 and the row at 2", a["version"], a["row_version"])
	}
	if a["exchange_count"] != 2.0 || b["exchange_count"] != 1.0 {
		t.Errorf("counts = %v and %v", a["exchange_count"], b["exchange_count"])
	}
	if !reflect.DeepEqual(b["version"], b["row_version"]) {
		t.Errorf("B: version %v, row_version %v", b["version"], b["row_version"])
	}
	stub.hold(false)
	waitFor(t, "A rebuilt at the new version", func() bool {
		p = e.sessNow("")
		return p.Items[0]["version"] != nil && p.Items[0]["version"].(map[string]any)["exchanges"] == 2.0
	})
}

// E3: a memo evicted from memory, with none on disk, makes the row pending again; the
// facts that remain belong to their own sessions.
func TestFastRows_EvictedMemoIsPendingNotAnotherSessions(t *testing.T) {
	e := newCacheEnv(t, 1)
	stub := newBuildStub(t, e.cache)
	stub.hold(false)
	e.save("a", "A", "cc", 1)
	e.save("b", "B", "cc", 2)
	e.sessNow("") // only one memo fits, so the page can never be all full rows
	settle(t, e.cache)
	stub.hold(true)
	p := e.sessNow("")
	var full, pending int
	for _, it := range p.Items {
		if isPending(it) {
			pending++
			assertPendingShape(t, it)
			continue
		}
		full++
		if it["title"] != "title-"+it["session_key"].(string) {
			t.Errorf("row %v carries %v", it["session_key"], it["title"])
		}
	}
	if full != 1 || pending != 1 {
		t.Errorf("full %d pending %d, want one each", full, pending)
	}
}

// D11: the stream's opening snapshot does not wait for builds.
func TestFastRows_SnapshotNeverWaits(t *testing.T) {
	e := newCacheEnv(t, 64)
	stub := newBuildStub(t, e.cache)
	stub.hold(true)
	e.seedSessions(5)
	var snap snapshot
	var err error
	answersWithin(t, "snapshot", func() { snap, err = readSnapshot(t.Context(), e.d, "") })
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Sessions) != 5 {
		t.Fatalf("sessions = %d", len(snap.Sessions))
	}
	for _, s := range snap.Sessions {
		if !s.Pending || s.RowVersion == nil {
			t.Errorf("snapshot row %s: pending %v", s.SessionKey, s.Pending)
		}
	}
}

// --- the gate ---

// gateRun is a queued compute for the gate tests: it records when it starts and ends when
// let go.
type gateRun struct {
	started chan struct{}
	release chan struct{}
}

func newGateRun() *gateRun {
	return &gateRun{started: make(chan struct{}), release: make(chan struct{})}
}

func (g *gateRun) compute(order *[]string, mu *sync.Mutex, name string) func(context.Context) (digestMemo, error) {
	return func(context.Context) (digestMemo, error) {
		mu.Lock()
		*order = append(*order, name)
		mu.Unlock()
		close(g.started)
		<-g.release
		return digestMemo{}, nil
	}
}

func gateCache(slots int) *DigestCache {
	c := newDigestCache(64)
	c.gate.slots = slots
	return c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func listPrio(tick uint64, pos int) gatePrio { return gatePrio{class: classList, tick: tick, pos: pos} }

// AC3: no more computes run at once than the gate has slots.
func TestBuildGate_AtMostNRun(t *testing.T) {
	c := gateCache(2)
	var running, maxRunning atomic.Int32
	release := make(chan struct{})
	for i := range 8 {
		c.queue(ident(fmt.Sprintf("k%d", i)), store.Version{ExCount: 1}, listPrio(1, i), func(context.Context) (digestMemo, error) {
			n := running.Add(1)
			for {
				m := maxRunning.Load()
				if n <= m || maxRunning.CompareAndSwap(m, n) {
					break
				}
			}
			<-release
			running.Add(-1)
			return digestMemo{}, nil
		})
	}
	waitFor(t, "two runs", func() bool { return running.Load() == 2 })
	time.Sleep(20 * time.Millisecond)
	if running.Load() != 2 {
		t.Fatalf("running = %d, want 2 with 8 queued", running.Load())
	}
	close(release)
	settle(t, c)
	if maxRunning.Load() != 2 {
		t.Errorf("max running = %d, want 2", maxRunning.Load())
	}
}

// AC3: a newer request's rows start before an older one's queued rows, in row order; a
// request that asks again for a queued run raises it; the background pass is last.
func TestBuildGate_NewerRequestFirstBackgroundLast(t *testing.T) {
	c := gateCache(1)
	var mu sync.Mutex
	var order []string
	runs := map[string]*gateRun{}
	add := func(name string, gp gatePrio) {
		runs[name] = newGateRun()
		c.queue(ident(name), store.Version{ExCount: 1}, gp, runs[name].compute(&order, &mu, name))
	}
	add("hold", listPrio(1, 0))
	<-runs["hold"].started
	runs["bg"] = newGateRun()
	go func() {
		_, _ = c.getBackground(t.Context(), ident("bg"), store.Version{ExCount: 1}, runs["bg"].compute(&order, &mu, "bg"))
	}()
	waitFor(t, "the background run is queued", func() bool {
		c.fmu.Lock()
		defer c.fmu.Unlock()
		return len(c.gate.q) == 1
	})
	add("old1", listPrio(2, 1))
	add("old2", listPrio(2, 2))
	add("new0", listPrio(3, 0))
	add("new1", listPrio(3, 1))
	// an even newer request asks again for old2: it moves up
	c.queue(ident("old2"), store.Version{ExCount: 1}, listPrio(4, 0), nil)
	close(runs["hold"].release)
	for _, name := range []string{"old2", "new0", "new1", "old1", "bg"} {
		select {
		case <-runs[name].started:
		case <-time.After(5 * time.Second):
			mu.Lock()
			t.Fatalf("%s did not start next; started so far %v", name, order)
		}
		close(runs[name].release)
	}
	settle(t, c)
	want := []string{"hold", "old2", "new0", "new1", "old1", "bg"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("start order = %v, want %v", order, want)
	}
}

// AC3: a caller that waits for a run still queued in the gate promotes it: it starts at
// once, past the slots, and the waiter gets its result.
func TestBuildGate_WaitedGetPromotesAQueuedRun(t *testing.T) {
	c := gateCache(1)
	var mu sync.Mutex
	var order []string
	hold, want := newGateRun(), newGateRun()
	c.queue(ident("hold"), store.Version{ExCount: 1}, listPrio(1, 0), hold.compute(&order, &mu, "hold"))
	<-hold.started
	c.queue(ident("want"), store.Version{ExCount: 1}, listPrio(1, 1), want.compute(&order, &mu, "want"))
	time.Sleep(20 * time.Millisecond)
	select {
	case <-want.started:
		t.Fatal("a queued run started with the slot taken")
	default:
	}
	got := make(chan error, 1)
	go func() {
		_, err := c.get(t.Context(), ident("want"), store.Version{ExCount: 1}, nil)
		got <- err
	}()
	select {
	case <-want.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the waited run did not start while the slot was taken")
	}
	close(want.release)
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	close(hold.release)
	settle(t, c)
}

// AC3: a waited get with nothing queued runs at once even when every slot is taken.
func TestBuildGate_WaitedGetBypassesFullSlots(t *testing.T) {
	c := gateCache(1)
	var mu sync.Mutex
	var order []string
	hold := newGateRun()
	c.queue(ident("hold"), store.Version{ExCount: 1}, listPrio(1, 0), hold.compute(&order, &mu, "hold"))
	<-hold.started
	done := make(chan error, 1)
	go func() {
		_, err := c.get(t.Context(), ident("other"), store.Version{ExCount: 1},
			func(context.Context) (digestMemo, error) { return digestMemo{}, nil })
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a waited compute was held by the gate")
	}
	close(hold.release)
	settle(t, c)
}

// AC3: the pass waits while list work is queued or running, not for the pass's own work.
func TestBuildGate_WaitListIdle(t *testing.T) {
	c := gateCache(1)
	var mu sync.Mutex
	var order []string
	l1, l2, bg := newGateRun(), newGateRun(), newGateRun()
	waitIdle := func() chan struct{} {
		done := make(chan struct{})
		go func() { c.waitListIdle(t.Context()); close(done) }()
		return done
	}
	// only background work: no wait
	go func() {
		_, _ = c.getBackground(t.Context(), ident("bg"), store.Version{ExCount: 1}, bg.compute(&order, &mu, "bg"))
	}()
	<-bg.started
	select {
	case <-waitIdle():
	case <-time.After(5 * time.Second):
		t.Fatal("the pass waited for its own work")
	}
	c.queue(ident("l1"), store.Version{ExCount: 1}, listPrio(1, 0), l1.compute(&order, &mu, "l1"))
	c.queue(ident("l2"), store.Version{ExCount: 1}, listPrio(1, 1), l2.compute(&order, &mu, "l2"))
	idle := waitIdle()
	select {
	case <-idle:
		t.Fatal("the pass did not wait for queued list work")
	case <-time.After(30 * time.Millisecond):
	}
	close(bg.release)
	<-l1.started
	close(l1.release)
	<-l2.started // l2 still runs: still not idle
	select {
	case <-idle:
		t.Fatal("the pass woke while list work was running")
	case <-time.After(30 * time.Millisecond):
	}
	close(l2.release)
	select {
	case <-idle:
	case <-time.After(5 * time.Second):
		t.Fatal("the pass did not wake when the list work ended")
	}
	settle(t, c)
}

// A cancelled context ends the wait.
func TestBuildGate_WaitListIdleEndsWithItsContext(t *testing.T) {
	c := gateCache(1)
	var mu sync.Mutex
	var order []string
	l := newGateRun()
	c.queue(ident("l"), store.Version{ExCount: 1}, listPrio(1, 0), l.compute(&order, &mu, "l"))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { c.waitListIdle(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the wait outlived its context")
	}
	close(l.release)
	settle(t, c)
}

// AC3: the background pass starts no session while list work is queued.
func TestPrefill_WaitsForListWork(t *testing.T) {
	e := newCacheEnv(t, 64)
	e.cache.gate.slots = 2
	stub := newBuildStub(t, e.cache)
	stub.hold(true)
	e.seedSessions(6)
	e.sessNow("?limit=2") // two list builds start and are held
	waitFor(t, "two builds", func() bool { return len(stub.startedKeys()) == 2 })
	done := make(chan struct{})
	go func() { Prefill(t.Context(), e.d); close(done) }()
	time.Sleep(150 * time.Millisecond)
	if got := stub.startedKeys(); len(got) != 2 {
		t.Fatalf("started %v: the pass built while list work was running", got)
	}
	stub.hold(false)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the pass did not finish after the list work ended")
	}
	if got := len(stub.startedKeys()); got != 6 {
		t.Errorf("builds = %d, want all 6 sessions built once", got)
	}
}

// A join at the same version of a run that is running does not queue it twice, and a
// session never computes twice at once through the gate.
func TestBuildGate_SessionComputesOnceAtATime(t *testing.T) {
	c := gateCache(4)
	var mu sync.Mutex
	var order []string
	r := newGateRun()
	for i := range 5 {
		c.queue(ident("k"), store.Version{ExCount: 1}, listPrio(uint64(i+1), 0), r.compute(&order, &mu, "k"))
	}
	<-r.started
	close(r.release)
	settle(t, c)
	if len(order) != 1 {
		t.Errorf("computes = %v, want one", order)
	}
}

// Review P1: a waiter that asks for a newer version than a run still queued in the gate
// must not wait for the queue. The queued run has not started, so it is retargeted to the
// newer version and promoted: the waiter's compute starts at once, past the full slots.
func TestBuildGate_WaitedGetAtNewerVersionPromotesQueuedRun(t *testing.T) {
	c := gateCache(1)
	var mu sync.Mutex
	var order []string
	hold, old, newer := newGateRun(), newGateRun(), newGateRun()
	c.queue(ident("hold"), store.Version{ExCount: 1}, listPrio(1, 0), hold.compute(&order, &mu, "hold"))
	<-hold.started
	c.queue(ident("x"), store.Version{ExCount: 1}, listPrio(1, 1), old.compute(&order, &mu, "x-v1"))
	type result struct {
		h   digestHit
		err error
	}
	got := make(chan result, 1)
	go func() {
		h, err := c.get(t.Context(), ident("x"), store.Version{ExCount: 2}, newer.compute(&order, &mu, "x-v2"))
		got <- result{h, err}
	}()
	select {
	case <-newer.started:
	case <-time.After(2 * time.Second):
		t.Fatal("a waited get at a newer version sat behind the queued run")
	}
	close(newer.release)
	if r := <-got; r.err != nil || r.h.ver.ExCount != 2 {
		t.Errorf("hit version = %+v, err %v, want the waited version 2", r.h.ver, r.err)
	}
	close(hold.release)
	settle(t, c)
	select {
	case <-old.started:
		t.Error("the superseded v1 compute ran")
	default:
	}
}

// AC7: a filtered scan reads versions only for the rows it returns. With every row a cheap
// match and a limit below the batch size, the request reads `limit` rows, not the batch.
func TestFastRows_FilteredScanReadsOnlyReturnedRows(t *testing.T) {
	e := newCacheEnv(t, 64)
	stub := newBuildStub(t, e.cache)
	stub.hold(true)
	e.seedSessions(10)
	spy := spyVersions(t)
	var p sessPage
	answersWithin(t, "q key with a small limit", func() { p = e.sessNow("?q=s0&limit=3") })
	if fmt.Sprint(p.keys()) != "[s009 s008 s007]" || p.NextAfter == nil || p.Checking != 0 || p.Scanned != 3 {
		t.Fatalf("page = %v next %v checking %d scanned %d", p.keys(), p.NextAfter, p.Checking, p.Scanned)
	}
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.request) != 1 || len(spy.request[0]) != 3 {
		t.Errorf("version reads during the request = %v, want one read of the 3 returned rows", spy.request)
	}
}

// AC1: the cheap folder wins on the waiting path too (the session page, live events).
func TestFastRows_WaitingPathUsesTheSQLFolder(t *testing.T) {
	e := newCacheEnv(t, 64)
	stub := newBuildStub(t, e.cache)
	stub.hold(false)
	e.save("r0", "A", "cc", 1)
	p := e.sess("") // the memo is built before any hook names a work dir
	id := p.Items[0]["id"].(string)
	e.hook("A", core.HarnessEvent{Phase: core.PhaseNotice, WorkDir: "/work/projx"}, 50)
	rec := e.get("/api/sessions/" + id)
	if rec.Code != 200 {
		t.Fatalf("session = %d %s", rec.Code, rec.Body)
	}
	var s map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	list, _ := s["list"].(map[string]any)
	if list["folder_full"] != "/work/projx" {
		t.Errorf("list = %v, want the SQL folder /work/projx", list)
	}
}

// Review P4: a memo newer than the version just read is served as it is and no build is
// queued at the older version.
func TestFastRows_NewerMemoQueuesNoBuildAtTheOlderVersion(t *testing.T) {
	e := newCacheEnv(t, 64)
	stub := newBuildStub(t, e.cache)
	stub.hold(false)
	e.save("r0", "A", "cc", 1)
	e.sess("")
	settle(t, e.cache)
	n := stub.count.Load()
	ctx := t.Context()
	c := e.d.Reader.WithContext(ctx)
	rows, _, err := c.SessionPageTotal(nil, 5)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	id := sessionIdent(rows[0])
	vers, err := c.SessionVersions([]store.SessionIdent{id})
	if err != nil {
		t.Fatal(err)
	}
	// the memo in memory moves ahead of the version the list reads
	newer := vers[id]
	newer.ExCount++
	e.cache.store(&digestEntry{id: id, ver: newer, memo: digestMemo{}})
	if _, _, err := e.d.summarizeRowsNoWait(ctx, c, []string{sessionID(rows[0])}, rows, e.now, e.d.newListRank()); err != nil {
		t.Fatal(err)
	}
	settle(t, e.cache)
	if got := stub.count.Load(); got != n {
		t.Errorf("builds = %d, want %d: a build was queued at the older version", got, n)
	}
}
