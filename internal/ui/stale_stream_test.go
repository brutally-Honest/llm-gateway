package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// settleTimelines waits until c has no build running or queued. It waits on the runs' own
// done channels; the timer only turns a hang into a failure.
func settleTimelines(t *testing.T, c *TimelineCache) {
	t.Helper()
	for {
		c.fmu.Lock()
		var r *buildRun
		for _, sb := range c.active {
			r = sb.cur
			break
		}
		c.fmu.Unlock()
		if r == nil {
			return
		}
		select {
		case <-r.done:
		case <-time.After(10 * time.Second):
			t.Fatal("a timeline build did not end")
		}
	}
}

// heldBuild occupies session k's timeline build slot at the store's current version with
// a build that runs the real one only when release is called.
func (e *streamEnv) heldBuild() (release func()) {
	e.t.Helper()
	who := sessIdent{core.PrincipalLocal, "cc", "k"}
	id := store.SessionIdent{Principal: who.principal, Client: who.client, Key: who.name}
	vers, err := e.d.Reader.WithContext(e.t.Context()).SessionVersions([]store.SessionIdent{id})
	if err != nil {
		e.t.Fatal(err)
	}
	started, gate := make(chan struct{}), make(chan struct{})
	held := func(ctx context.Context) (builtSession, error) {
		close(started)
		<-gate
		return e.d.timelineBuild(who)(ctx)
	}
	if _, err := e.hub.Timelines().latest(e.t.Context(), id, vers[id], held); err != nil {
		e.t.Fatal(err)
	}
	<-started
	return func() { close(gate) }
}

func exchangeOf(t *testing.T, ev sseEvent) string {
	t.Helper()
	return decodeEntries(t, ev.data)[0]["exchange_id"].(string)
}

// A session stream opened during a slow rebuild gets its snapshot from the cached build,
// with that build's position, at once; once the rebuild lands it gets what the snapshot
// lacked with no further commit.
func TestStream_SessionSnapshotFromCachedBuildThenCatchesUp(t *testing.T) {
	e := newStreamEnv(t, nil)
	e.commitA()
	resp, err := http.Get(e.ts.URL + "/api/sessions/" + sid() + "/timeline") // cached with a only
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("timeline = %d", resp.StatusCode)
	}
	e.saveToolExchange("b", 20)
	release := e.heldBuild()

	s := noWait(t, "session stream open", func() *sse { return e.openRaw(e.sessionPath()) })
	first := noWait(t, "session snapshot", func() sseEvent { return s.nextData() })
	if first.event != "session" {
		t.Fatalf("snapshot first event = %+v", first)
	}
	s.skipSession, s.skipEmptyHooks = true, true
	snap := noWait(t, "session snapshot exchanges", func() sseEvent { return s.nextData() })
	if snap.event != "exchange" || exchangeOf(t, snap) != "a" {
		t.Fatalf("snapshot = %+v, want the cached build's exchange a", snap)
	}
	release()
	for {
		ev := s.nextData() // fails on its read timeout if no catch-up comes
		if ev.event == "exchange" && exchangeOf(t, ev) == "b" {
			return
		}
	}
}

// deferred waits until the hub holds a catch-up for session k with exactly ids deferred:
// the dispatcher has read every commit and answered from the cached build. It polls the
// hub's own state; the deadline only turns a hang into a failure.
func (e *streamEnv) deferred(ids ...string) {
	e.t.Helper()
	who := sessIdent{core.PrincipalLocal, "cc", "k"}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		e.hub.mu.Lock()
		p := e.hub.catching[who]
		got := p != nil && p.entries && reflect.DeepEqual(p.ids, ids)
		e.hub.mu.Unlock()
		if got {
			return
		}
		time.Sleep(time.Millisecond)
	}
	e.t.Fatalf("the hub never deferred %v", ids)
}

// Live commits arriving while a build is slower than the gap between them are not lost
// and do not drop the stream: the reads answer from the cached build without starting a
// second build beside the held one, and once the builds land the commits arrive as entries
// batches, the last with its id, and the next read is at the newest version.
func TestStream_LiveCommitsDuringSlowBuildArriveOnceItLands(t *testing.T) {
	e := newStreamEnv(t, nil)
	e.commitA()
	s := e.open(e.sessionPath())
	if snap := s.take(1); snap[0].event != "exchange" || exchangeOf(t, snap[0]) != "a" {
		t.Fatalf("snapshot = %+v", snap)
	}
	settleTimelines(t, e.hub.Timelines())
	before := e.hub.Timelines().builds.Load()

	e.saveToolExchange("b", 20)
	release := e.heldBuild()
	e.publish("b")
	e.saveToolExchange("c", 30)
	e.publish("c")
	e.saveToolExchange("d", 40)
	e.publish("d")
	e.deferred("b", "c", "d")
	if n := e.hub.Timelines().builds.Load() - before; n != 1 {
		t.Fatalf("builds started while one is held = %d, want only the held one", n)
	}
	release()

	got := map[string]bool{}
	var last sseEvent
	for !got["b"] || !got["c"] || !got["d"] {
		ev := s.nextData() // fails on its read timeout if a commit never arrives
		if ev.event == "entries" {
			for _, en := range decodeEntries(t, ev.data) {
				got[en["exchange_id"].(string)] = true
			}
			last = ev
		}
	}
	if last.id == "" || !strings.HasPrefix(last.data, "[") {
		t.Fatalf("last entries event = %+v, want the batch's id on it", last)
	}
	settleTimelines(t, e.hub.Timelines())
	resp, err := http.Get(e.ts.URL + "/api/sessions/" + sid() + "/timeline")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var page struct {
		Version versionJSON `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.Version.Exchanges != 4 {
		t.Fatalf("timeline after the writer stopped is at %+v, want all 4 exchanges", page.Version)
	}
}
