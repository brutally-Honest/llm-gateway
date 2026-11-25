package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// Spec 043, code review round 1: the Go server findings.

// P2: a waiter whose version is not newer than the run still queued in the gate must not
// wait for the queue either: the queued run is promoted, and the waiter's run starts
// right after it (AC3).
func TestReview_WaitedGetOlderThanQueuedRunIsNotStarved(t *testing.T) {
	c := gateCache(1)
	var mu sync.Mutex
	var order []string
	hold, waiter := newGateRun(), newGateRun()
	c.queue(ident("hold"), store.Version{ExCount: 1}, listPrio(1, 0), hold.compute(&order, &mu, "hold"))
	<-hold.started
	c.queue(ident("x"), store.Version{ExCount: 2}, listPrio(1, 1),
		func(context.Context) (digestMemo, error) { return digestMemo{}, nil })
	got := make(chan error, 1)
	go func() {
		_, err := c.get(t.Context(), ident("x"), store.Version{ExCount: 1}, waiter.compute(&order, &mu, "x-v1"))
		got <- err
	}()
	select {
	case <-waiter.started:
	case <-time.After(2 * time.Second):
		t.Fatal("a waited get at an older version sat behind the queued run")
	}
	close(waiter.release)
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	close(hold.release)
	settle(t, c)
}

// P3: after a waiter retargets the queued run to a newer version, a follow-up at an
// older or equal version is aimed at it too, so no older memo is computed and written
// over the newer disk file.
func TestReview_RetargetAimsTheFollowUpToo(t *testing.T) {
	e := newDiskEnv(t)
	c := e.dep.Digests
	c.gate.slots = 1
	var mu sync.Mutex
	var order []string
	hold, waiter := newGateRun(), newGateRun()
	named := func(name string) func(context.Context) (digestMemo, error) {
		return func(context.Context) (digestMemo, error) {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return digestMemo{}, nil
		}
	}
	c.queue(ident("hold"), store.Version{ExCount: 1}, listPrio(1, 0), hold.compute(&order, &mu, "hold"))
	<-hold.started
	c.queue(ident("x"), store.Version{ExCount: 1}, listPrio(1, 1), named("x-v1"))
	c.queue(ident("x"), store.Version{ExCount: 2}, listPrio(1, 2), named("x-v2")) // the follow-up
	got := make(chan error, 1)
	go func() {
		_, err := c.get(t.Context(), ident("x"), store.Version{ExCount: 3}, waiter.compute(&order, &mu, "x-v3"))
		got <- err
	}()
	<-waiter.started
	close(waiter.release)
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	close(hold.release)
	settle(t, c)
	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(order, ","); got != "hold,x-v3" {
		t.Errorf("computes = %s, want hold,x-v3: an older follow-up ran", got)
	}
}

// P3: a memo older than what memory already holds for the session is not written to
// disk: the file is one per session.
func TestReview_OlderMemoIsNotWrittenOverNewerOne(t *testing.T) {
	e := newDiskEnv(t)
	e.save("r0", "x", "cc", 1)
	c := e.dep.Digests
	id := ident("x")
	// the writer takes a memo only at the store's current version, so the compute is at
	// that one and the "newer" memo in memory is one version ahead of it
	vers, err := e.dep.Reader.WithContext(t.Context()).SessionVersions([]store.SessionIdent{id})
	if err != nil {
		t.Fatal(err)
	}
	cur, ahead := vers[id], vers[id]
	ahead.ExCount++
	started, release := make(chan struct{}), make(chan struct{})
	got := make(chan error, 1)
	go func() {
		_, err := c.get(t.Context(), id, cur, func(context.Context) (digestMemo, error) {
			close(started)
			<-release
			return digestMemo{}, nil
		})
		got <- err
	}()
	<-started
	c.putFromDisk(id, ahead, digestMemo{}) // a newer memo arrives meanwhile
	close(release)
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	settle(t, c)
	e.flush()
	if n := e.disk.written.Load(); n != 0 {
		t.Errorf("memos written = %d, want 0: the older memo went over the newer file", n)
	}
}

// P3: a degraded memo is served by the list (not pending), is rebuilt on the next
// request that needs it, and a good memo at the same version replaces it.
func TestReview_DegradedMemoServedThenReplaced(t *testing.T) {
	e := newCacheEnv(t, 64)
	var degrade atomic.Bool
	degrade.Store(true)
	real := computeMemoFn
	computeMemoFn = func(src memoSource, vd viewDeps, row store.SessionRow) (digestMemo, error) {
		m, err := real(src, vd, row)
		m.row.title = "title-" + row.SessionKey
		m.degraded = degrade.Load()
		return m, err
	}
	t.Cleanup(func() { settle(t, e.cache); computeMemoFn = real })
	e.save("r0", "A", "cc", 1)

	first := e.sessNow("") // nothing in memory: pending, a build is queued
	if len(first.Items) != 1 || !isPending(first.Items[0]) {
		t.Fatalf("first = %v", first.Items)
	}
	settle(t, e.cache)
	n := e.computes()
	second := e.sessNow("")
	if len(second.Items) != 1 || isPending(second.Items[0]) || second.Items[0]["title"] != "title-A" {
		t.Fatalf("a degraded memo was not served: %v", second.Items)
	}
	settle(t, e.cache)
	if got := e.computes(); got != n+1 {
		t.Errorf("computes = %d, want %d: the degraded memo was not rebuilt on the next request", got, n+1)
	}
	degrade.Store(false)
	e.sessNow("") // rebuilds, now good
	settle(t, e.cache)
	pe, ok := e.cache.peek(ident("A"))
	if !ok || pe.memo.degraded {
		t.Fatalf("the good memo did not replace the degraded one: %+v %v", pe, ok)
	}
	if _, ok := e.cache.lookup(ident("A"), pe.ver); !ok {
		t.Error("the good memo does not answer a lookup")
	}
}

// P3 (AC5, D10): the version a row carries is read in the same snapshot as the row, so
// an exchange landing between the list read and the version read is in neither.
func TestReview_SnapshotRowsAndVersionsAgree(t *testing.T) {
	e := newCacheEnv(t, 8)
	e.save("r0", "A", "cc", 1)
	c, done, err := e.d.Reader.WithContext(t.Context()).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	rows, _, err := c.SessionPageTotal(nil, 10)
	if err != nil || len(rows) != 1 || rows[0].Count != 1 {
		t.Fatal(rows, err)
	}
	e.save("r1", "A", "cc", 2) // lands between the list read and the version read
	vers, err := c.SessionVersions([]store.SessionIdent{sessionIdent(rows[0])})
	if err != nil {
		t.Fatal(err)
	}
	if got := vers[sessionIdent(rows[0])].ExCount; got != rows[0].Count {
		t.Errorf("version counts %d exchanges, the row it labels %d", got, rows[0].Count)
	}
	// without a snapshot the two reads disagree, which is what the snapshot prevents
	plain := e.d.Reader.WithContext(t.Context())
	pv, err := plain.SessionVersions([]store.SessionIdent{sessionIdent(rows[0])})
	if err != nil || pv[sessionIdent(rows[0])].ExCount != 2 {
		t.Fatalf("plain read = %+v %v", pv, err)
	}
}

// P3 at the list: an exchange landing between the list query and the version read gives
// no row whose row_version is newer than its exchange_count.
func TestReview_ListRowVersionNeverNewerThanItsCount(t *testing.T) {
	e := newCacheEnv(t, 8)
	stub := newBuildStub(t, e.cache)
	stub.hold(true)
	e.save("r0", "A", "cc", 1)
	var once sync.Once
	real := sessionVersionsFn
	sessionVersionsFn = func(c *store.CtxReader, ids []store.SessionIdent, deferred bool) (map[store.SessionIdent]store.Version, error) {
		once.Do(func() { e.save("r1", "A", "cc", 2) }) // after the list query, before the versions
		return real(c, ids, deferred)
	}
	t.Cleanup(func() { sessionVersionsFn = real })
	p := e.sessNow("")
	if len(p.Items) != 1 {
		t.Fatal(p.Items)
	}
	it := p.Items[0]
	rv, _ := it["row_version"].(map[string]any)
	if cnt, ver := it["exchange_count"], rv["exchanges"]; fmt.Sprint(cnt) != fmt.Sprint(ver) {
		t.Errorf("exchange_count %v but row_version %v", cnt, rv)
	}
}

// P3: a filtered scan whose first rows have their facts in memory and match: every row
// after the room-th match is neither read nor queued (AC7).
func TestReview_ScanQueuesNothingPastTheStopRow(t *testing.T) {
	e := newCacheEnv(t, 64)
	stub := newBuildStub(t, e.cache)
	stub.hold(true)
	e.seedSessions(10) // s009 first
	c := e.d.Reader.WithContext(t.Context())
	rows, _, err := c.SessionPageTotal(nil, 20)
	if err != nil || len(rows) != 10 {
		t.Fatal(len(rows), err)
	}
	idents := make([]store.SessionIdent, 2)
	for i := range idents {
		idents[i] = sessionIdent(rows[i])
	}
	vers, err := c.SessionVersions(idents)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range idents { // the two newest rows have a memo whose title matches
		m := digestMemo{}
		m.row.title = "needle " + id.Key
		e.cache.store(&digestEntry{id: id, ver: vers[id], memo: m})
	}
	spy := spyVersions(t)
	var p sessPage
	answersWithin(t, "q with fact matches", func() { p = e.sessNow("?q=needle&limit=2") })
	if fmt.Sprint(p.keys()) != "[s009 s008]" || p.NextAfter == nil || p.Scanned != 2 || p.Checking != 0 {
		t.Fatalf("page = %v next %v scanned %d checking %d", p.keys(), p.NextAfter, p.Scanned, p.Checking)
	}
	time.Sleep(150 * time.Millisecond) // the deferred queueing runs off the request
	spy.mu.Lock()
	defer spy.mu.Unlock()
	if len(spy.deferred) != 0 {
		t.Errorf("deferred reads = %v, want none: rows after the stop row were queued", spy.deferred)
	}
	if len(spy.request) != 1 || len(spy.request[0]) != 2 {
		t.Errorf("request version reads = %v, want one read of the 2 returned rows", spy.request)
	}
	if got := stub.startedKeys(); len(got) != 0 {
		t.Errorf("builds started = %v, want none", got)
	}
}

// P4 (AC2): a row whose memo in memory is older checks the disk at the current key
// before a build is queued.
func TestReview_OlderMemoChecksDiskBeforeQueueing(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	for i := range 3 {
		e.mustGetReady(listPath(i))
	}
	e.flush()
	if len(e.files("m")) != 3 {
		t.Fatalf("memos on disk = %v, want 3", e.files("m"))
	}
	e.restart("v1")
	var n atomic.Int32
	real := computeMemoFn
	computeMemoFn = func(src memoSource, vd viewDeps, row store.SessionRow) (digestMemo, error) {
		n.Add(1)
		return real(src, vd, row)
	}
	t.Cleanup(func() { settle(t, e.dep.Digests); computeMemoFn = real })
	// memory holds an older memo of every session; the current one is on disk
	c := e.dep.Reader.WithContext(t.Context())
	rows, _, err := c.SessionPageTotal(nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		id := sessionIdent(row)
		vers, err := c.SessionVersions([]store.SessionIdent{id})
		if err != nil {
			t.Fatal(err)
		}
		old := vers[id]
		old.ExCount--
		e.dep.Digests.store(&digestEntry{id: id, ver: old, memo: digestMemo{}})
	}
	// no build may start, so a queued one cannot hide a missed disk read
	dc := e.dep.Digests
	dc.fmu.Lock()
	dc.gate.slots = 0
	dc.fmu.Unlock()
	e.mustGet("/api/sessions?limit=10")
	for _, row := range rows {
		id := sessionIdent(row)
		vers, err := c.SessionVersions([]store.SessionIdent{id})
		if err != nil {
			t.Fatal(err)
		}
		if pe, ok := dc.peek(id); !ok || pe.ver != vers[id] {
			t.Errorf("%s: memory holds %+v, want the memo at the current version %+v from disk", id.Key, pe, vers[id])
		}
	}
	dc.fmu.Lock()
	dc.gate.slots = gateSlots
	dc.dispatchLocked()
	dc.fmu.Unlock()
	settle(t, dc)
	if n.Load() != 0 || e.computes() != 0 {
		t.Errorf("computes = %d (%d), want none: the current memo was on disk", n.Load(), e.computes())
	}
}
