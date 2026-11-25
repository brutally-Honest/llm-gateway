package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// Tests of the incremental build's own rules (spec 040 AC6, AC9 and the edge cases); the
// oracle (incremental_oracle_test.go) checks that it equals a full build.

// incEnv is a store with one oracle session, and the pieces a build needs.
type incEnv struct {
	t     *testing.T
	s     *store.Store
	r     *store.Reader
	c     *store.CtxReader
	who   sessIdent
	rules ClientRules
	w     *oWorld
}

func newIncEnv(t *testing.T) *incEnv {
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
	return &incEnv{t: t, s: s, r: r, c: r.WithContext(t.Context()),
		who: sessIdent{core.PrincipalLocal, oracleClient, oracleSession}, rules: oracleRules{}, w: &oWorld{}}
}

func (e *incEnv) apply(steps ...oracleStep) {
	e.t.Helper()
	for _, st := range steps {
		if err := st.run(e.t.Context(), e.s); err != nil {
			e.t.Fatalf("%s: %v", st.desc, err)
		}
	}
}

// linear stores n calls c1..cn of one thread, each answering the one before.
func (e *incEnv) linear(from, to int) {
	e.t.Helper()
	conv := &oConv{}
	conv.user("the task")
	for i := 1; i <= to; i++ {
		id := fmt.Sprintf("c%d", i)
		cl := conv.call(id, i*10000, oReply(id, "Bash"))
		conv.result()
		if i >= from {
			e.apply(e.w.call(cl, core.ParseOK))
		}
	}
}

func (e *incEnv) full() builtSession {
	e.t.Helper()
	bs, err := buildSession(e.c, e.who, e.rules, entryCut)
	if err != nil {
		e.t.Fatal(err)
	}
	return bs
}

// sameBuild fails the test when two builds differ in what the session view reads.
func sameBuild(t *testing.T, got, want builtSession) {
	t.Helper()
	for _, d := range []string{
		entriesDiff(got.entries, want.entries),
		oracleDiff("facts", got.facts, want.facts),
		oracleDiff("calls", got.calls, want.calls),
		oracleDiff("byID", got.byID, want.byID),
		oracleDiff("rowID", got.rowID, want.rowID),
		oracleDiff("rows", got.rows, want.rows),
	} {
		if d != "" {
			t.Fatal(d)
		}
	}
}

// inc builds incrementally from prev and checks the result against a full build.
func (e *incEnv) inc(prev *builtSession) (builtSession, buildInfo) {
	e.t.Helper()
	bs, info, err := buildSessionInc(e.c, e.who, e.rules, entryCut, prev)
	if err != nil {
		e.t.Fatal(err)
	}
	sameBuild(e.t, bs, e.full())
	return bs, info
}

func setUpdate(t *testing.T, f func(*timeline.State, timeline.Source, store.SessionRow, []store.ExchangeRow) (*timeline.State, []timeline.Entry, timeline.UpdateStats, error)) {
	t.Helper()
	old := stateUpdate
	stateUpdate = f
	t.Cleanup(func() { stateUpdate = old })
}

func setStateCap(t *testing.T, n int64) {
	t.Helper()
	old := stateCap
	stateCap = n
	t.Cleanup(func() { stateCap = old })
}

// hideRow is a source whose snapshot lacks one exchange, as if the store had lost it.
type hideRow struct {
	sessionSource
	id string
}

func (h hideRow) SessionSnapshot(p, c, k string) ([]store.RowExchange, int64, error) {
	rows, max, err := h.sessionSource.SessionSnapshot(p, c, k)
	var out []store.RowExchange
	for _, r := range rows {
		if r.RequestID != h.id {
			out = append(out, r)
		}
	}
	return out, max, err
}

// otherRules is a client's rules that are not oracleRules; uncomparableRules cannot be compared.
type otherRules struct{ oracleRules }

type uncomparableRules struct {
	oracleRules
	names []string
}

// AC6: each fallback of spec F1-F6 builds in full, says why, and builds what a full build builds.
func TestIncremental_FallbackReasons(t *testing.T) {
	t.Run("F1_no_state", func(t *testing.T) {
		e := newIncEnv(t)
		e.linear(1, 3)
		bs, info := e.inc(nil)
		if info.Incremental || info.Reason != reasonNoState || bs.state == nil || bs.kind != buildFull {
			t.Fatalf("nil prev: info = %+v state = %v kind = %v", info, bs.state != nil, bs.kind)
		}
		prev := e.full()
		prev.state = nil
		if _, info := e.inc(&prev); info.Reason != reasonNoState {
			t.Fatalf("prev without state: info = %+v", info)
		}
	})
	t.Run("F2_missing_row", func(t *testing.T) {
		e := newIncEnv(t)
		e.linear(1, 3)
		prev := e.full()
		bs, info, err := buildSessionInc(hideRow{e.c, "c2"}, e.who, e.rules, entryCut, &prev)
		if err != nil {
			t.Fatal(err)
		}
		if info.Incremental || info.Reason != reasonMissingRow || bs.rows != 2 || bs.state == nil {
			t.Fatalf("info = %+v rows = %d state = %v", info, bs.rows, bs.state != nil)
		}
	})
	t.Run("F3_options", func(t *testing.T) {
		e := newIncEnv(t)
		e.linear(1, 3)
		prev := e.full()
		_, info, err := buildSessionInc(e.c, e.who, e.rules, entryCut+1, &prev)
		if err != nil || info.Reason != reasonOptions {
			t.Fatalf("a different cut: info = %+v err = %v", info, err)
		}
		for name, rules := range map[string]ClientRules{"other rules": otherRules{}, "no rules": nil,
			"rules that cannot be compared": uncomparableRules{names: []string{"a"}}} {
			_, info, err = buildSessionInc(e.c, e.who, rules, entryCut, &prev)
			if err != nil || info.Reason != reasonOptions {
				t.Fatalf("%s: info = %+v err = %v", name, info, err)
			}
		}
		// the same rules value is the same options
		if _, info := e.inc(&prev); !info.Incremental {
			t.Fatalf("same options: info = %+v", info)
		}
	})
	t.Run("F4_error", func(t *testing.T) {
		e := newIncEnv(t)
		e.linear(1, 3)
		prev := e.full()
		e.linear(4, 4)
		setUpdate(t, func(*timeline.State, timeline.Source, store.SessionRow, []store.ExchangeRow) (*timeline.State, []timeline.Entry, timeline.UpdateStats, error) {
			return nil, nil, timeline.UpdateStats{}, errors.New("boom")
		})
		bs, info := e.inc(&prev)
		if info.Incremental || info.Reason != reasonFailed || info.Err == nil || bs.kind != buildFull || bs.state == nil {
			t.Fatalf("info = %+v kind = %v", info, bs.kind)
		}
	})
	t.Run("F4_panic", func(t *testing.T) {
		e := newIncEnv(t)
		e.linear(1, 3)
		prev := e.full()
		e.linear(4, 4)
		setUpdate(t, func(*timeline.State, timeline.Source, store.SessionRow, []store.ExchangeRow) (*timeline.State, []timeline.Entry, timeline.UpdateStats, error) {
			panic("planted")
		})
		bs, info := e.inc(&prev)
		if info.Incremental || info.Reason != reasonFailed || info.Err == nil || !strings.Contains(info.Err.Error(), "panicked") || bs.state == nil {
			t.Fatalf("info = %+v", info)
		}
	})
	t.Run("F5_large_delta", func(t *testing.T) {
		e := newIncEnv(t)
		e.linear(1, 2)
		prev := e.full()
		e.linear(3, 6) // four of six exchanges are new
		bs, info := e.inc(&prev)
		if info.Incremental || info.Reason != reasonLargeDelta || bs.state == nil {
			t.Fatalf("info = %+v", info)
		}
		// exactly half is not more than half
		e = newIncEnv(t)
		e.linear(1, 2)
		prev = e.full()
		e.linear(3, 4)
		if _, info := e.inc(&prev); !info.Incremental || info.Rederived != 2 {
			t.Fatalf("half: info = %+v", info)
		}
	})
	t.Run("F6_too_large", func(t *testing.T) {
		e := newIncEnv(t)
		e.linear(1, 3)
		prev := e.full()
		if prev.state == nil {
			t.Fatal("no state under the cap")
		}
		e.linear(4, 4)
		setStateCap(t, 1)
		bs, info := e.inc(&prev)
		if info.Incremental || info.Reason != reasonTooLarge || bs.state != nil || !bs.tooLarge {
			t.Fatalf("update over the cap: info = %+v state = %v", info, bs.state != nil)
		}
		// no state is kept, so every later update is a full build
		e.linear(5, 5)
		if bs, info = e.inc(&bs); info.Reason != reasonTooLarge || bs.state != nil {
			t.Fatalf("next update: info = %+v", info)
		}
		// a full build over the cap keeps no state either
		if full := e.full(); full.state != nil || !full.tooLarge {
			t.Fatalf("full build over the cap kept state = %v", full.state != nil)
		}
	})
}

// D16: a snapshot that has not changed re-derives no exchange.
func TestIncremental_UnchangedSnapshotRederivesNothing(t *testing.T) {
	e := newIncEnv(t)
	e.linear(1, 6)
	e.apply(e.w.hook(5000, core.PhasePreTool, oTool{id: "tu_c1", name: "Bash", input: `{"command":"make c1"}`}, ""))
	prev := e.full()
	for i := range 3 {
		bs, info := e.inc(&prev)
		if !info.Incremental || info.Rederived != 0 || bs.kind != buildIncremental {
			t.Fatalf("round %d: info = %+v", i, info)
		}
		prev = bs
	}
}

// An append, a row before its parse and the parse landing each re-derive exactly one exchange.
func TestIncremental_AppendRederivesOne(t *testing.T) {
	e := newIncEnv(t)
	e.linear(1, 8)
	prev := e.full()
	e.linear(9, 9)
	bs, info := e.inc(&prev)
	if !info.Incremental || info.Rederived != 1 {
		t.Fatalf("append: info = %+v", info)
	}
	conv := &oConv{}
	conv.user("the task")
	var last oCall
	for i := 1; i <= 10; i++ {
		last = conv.call(fmt.Sprintf("c%d", i), i*10000, oReply(fmt.Sprintf("c%d", i), "Bash"))
		conv.result()
	}
	e.apply(e.w.row(last))
	bs, info = e.inc(&bs)
	if !info.Incremental || info.Rederived != 1 {
		t.Fatalf("row before its parse: info = %+v", info)
	}
	e.apply(e.w.parse(last, core.ParseOK))
	if _, info = e.inc(&bs); !info.Incremental || info.Rederived != 1 {
		t.Fatalf("parse landing: info = %+v", info)
	}
}

// E3 and D13: a late call that opens a thread re-derives one exchange; the later threads are
// renumbered by patching headers only.
func TestIncremental_LateCallOpeningAThreadRederivesOne(t *testing.T) {
	e := newIncEnv(t)
	main := &oConv{}
	main.user("main task")
	other := &oConv{}
	other.user("other task")
	late := &oConv{}
	late.user("late task")
	var steps []oracleStep
	for i, at := range []int{10000, 20000} {
		id := fmt.Sprintf("m%d", i+1)
		steps = append(steps, e.w.call(main.call(id, at, oReply(id, "Bash")), core.ParseOK))
		main.result()
	}
	for i, at := range []int{30000, 40000} {
		id := fmt.Sprintf("o%d", i+1)
		steps = append(steps, e.w.call(other.call(id, at, oReply(id, "Bash")), core.ParseOK))
		other.result()
	}
	e.apply(steps...)
	prev := e.full()
	e.apply(e.w.call(late.call("x", 25000, oReply("x", "Bash")), core.ParseOK))
	bs, info := e.inc(&prev)
	if !info.Incremental || info.Rederived != 1 {
		t.Fatalf("info = %+v", info)
	}
	thread := func(b builtSession, id string) int {
		for _, en := range b.entries {
			if en.Kind == timeline.KindExchange && en.RequestID == id {
				return en.Thread
			}
		}
		t.Fatalf("no exchange %s", id)
		return -1
	}
	if before, after := thread(prev, "o1"), thread(bs, "o1"); before != 1 || after != 2 {
		t.Fatalf("o1 thread = %d then %d, want 1 then 2", before, after)
	}
	if thread(bs, "x") != 1 || thread(bs, "o2") != 2 {
		t.Fatalf("threads x = %d o2 = %d", thread(bs, "x"), thread(bs, "o2"))
	}
}

// An update never changes the state it continues from: it can be used again.
func TestIncremental_BaseStateIsNotChanged(t *testing.T) {
	e := newIncEnv(t)
	e.linear(1, 4)
	prev := e.full()
	want := e.full()
	e.linear(5, 6)
	_, _ = e.inc(&prev)
	// a late call in the middle, from the same base
	late := &oConv{}
	late.user("late task")
	e.apply(e.w.call(late.call("x", 15000, oReply("x", "Bash")), core.ParseOK))
	_, _ = e.inc(&prev)
	_, _ = e.inc(&prev)
	if d := entriesDiff(prev.entries, want.entries); d != "" {
		t.Fatal(d)
	}
	if prev.state.Exchanges() != 4 {
		t.Fatalf("base state holds %d exchanges, want 4", prev.state.Exchanges())
	}
}

// D23: a session of hook events alone has a valid, empty state.
func TestIncremental_HooksOnlySessionIsAValidState(t *testing.T) {
	e := newIncEnv(t)
	tool := oTool{id: "tu_h1", name: "Bash", input: `{"command":"ls"}`}
	e.apply(e.w.hook(1000, core.PhasePreTool, tool, ""))
	prev := e.full()
	if prev.state == nil || prev.state.Exchanges() != 0 {
		t.Fatalf("state = %v", prev.state)
	}
	e.apply(e.w.hook(2000, core.PhasePostTool, tool, ""))
	bs, info := e.inc(&prev)
	if !info.Incremental || info.Rederived != 0 {
		t.Fatalf("hook after hook: info = %+v", info)
	}
	if len(bs.entries) != 2 {
		t.Fatalf("entries = %d, want the two hook lines", len(bs.entries))
	}
	e.apply(e.w.call((&oConv{items: []oItem{{role: "user", text: "go"}}}).call("a", 3000, oText("a")), core.ParseOK))
	if _, info = e.inc(&bs); info.Reason != reasonLargeDelta {
		t.Fatalf("first call after hooks: info = %+v", info)
	}
}

// buildSessionVia continues from the cache's entry, and the cache counts every build.
func TestTimelineCache_CountsIncrementalAndFullBuilds(t *testing.T) {
	e := newIncEnv(t)
	obs, logs := observer.New(zapcore.DebugLevel)
	cache := newTimelineCache(t.Context(), zap.New(obs), 4, 1<<30)
	ident := store.SessionIdent{Principal: e.who.principal, Client: e.who.client, Key: e.who.name}
	build := func(ctx context.Context, prev *cachedBuild) (builtSession, error) {
		return buildSessionVia(e.r.WithContext(ctx), e.who, e.rules, entryCut, prev)
	}
	get := func() *cachedBuild {
		t.Helper()
		vers, err := e.c.SessionVersions([]store.SessionIdent{ident})
		if err != nil {
			t.Fatal(err)
		}
		cb, err := cache.get(t.Context(), ident, vers[ident], build)
		if err != nil {
			t.Fatal(err)
		}
		return cb
	}
	e.linear(1, 3)
	if cb := get(); cb.bs.kind != buildFull || cb.bs.state == nil || cb.size <= 0 {
		t.Fatalf("first build: kind = %v state = %v size = %d", cb.bs.kind, cb.bs.state != nil, cb.size)
	}
	e.linear(4, 4)
	if cb := get(); cb.bs.kind != buildIncremental {
		t.Fatalf("second build: kind = %v", cb.bs.kind)
	}
	if got := cache.buildCounts(); got["full:no_state"] != 1 || got["incremental"] != 1 || len(got) != 2 {
		t.Fatalf("counts = %v", got)
	}
	// a fallback other than no_state is logged at Debug with its reason
	setStateCap(t, 1)
	e.linear(5, 5)
	get()
	if got := cache.buildCounts()["full:too_large"]; got != 1 {
		t.Fatalf("counts = %v", cache.buildCounts())
	}
	found := false
	for _, l := range logs.FilterField(zap.String("reason", reasonTooLarge)).All() {
		found = found || l.Level == zapcore.DebugLevel
	}
	if !found {
		t.Fatalf("no Debug log with the reason: %v", logs.All())
	}
	if len(logs.FilterField(zap.String("reason", reasonNoState)).All()) != 0 {
		t.Fatal("no_state was logged")
	}
}

// F4 and D19: a panic in the incremental update falls back, and the cache entry is the full build.
func TestTimelineCache_PanickedUpdateLeavesTheFullBuild(t *testing.T) {
	e := newIncEnv(t)
	obs, logs := observer.New(zapcore.DebugLevel)
	cache := newTimelineCache(t.Context(), zap.New(obs), 4, 1<<30)
	ident := store.SessionIdent{Principal: e.who.principal, Client: e.who.client, Key: e.who.name}
	build := func(ctx context.Context, prev *cachedBuild) (builtSession, error) {
		return buildSessionVia(e.r.WithContext(ctx), e.who, e.rules, entryCut, prev)
	}
	get := func() *cachedBuild {
		t.Helper()
		vers, err := e.c.SessionVersions([]store.SessionIdent{ident})
		if err != nil {
			t.Fatal(err)
		}
		cb, err := cache.get(t.Context(), ident, vers[ident], build)
		if err != nil {
			t.Fatal(err)
		}
		return cb
	}
	e.linear(1, 3)
	first := get()
	e.linear(4, 4)
	setUpdate(t, func(*timeline.State, timeline.Source, store.SessionRow, []store.ExchangeRow) (*timeline.State, []timeline.Entry, timeline.UpdateStats, error) {
		panic("planted")
	})
	cb := get()
	if cb.bs.kind != buildFull || cb.bs.how.Reason != reasonFailed || cb.bs.state == nil || cb.bs.state == first.bs.state {
		t.Fatalf("kind = %v how = %+v", cb.bs.kind, cb.bs.how)
	}
	if peek, ok := cache.peek(ident); !ok || peek != cb {
		t.Fatal("the cache entry is not the full build")
	}
	sameBuild(t, cb.bs, e.full())
	if got := cache.buildCounts()["full:failed"]; got != 1 {
		t.Fatalf("counts = %v", cache.buildCounts())
	}
	if len(logs.FilterLevelExact(zapcore.WarnLevel).All()) != 1 {
		t.Fatalf("logs = %v", logs.All())
	}
	// the next update continues from the full build's state
	stateUpdateReset := stateUpdate
	stateUpdate = (*timeline.State).Update
	t.Cleanup(func() { stateUpdate = stateUpdateReset })
	e.linear(5, 5)
	if cb = get(); cb.bs.kind != buildIncremental {
		t.Fatalf("after the fallback: kind = %v how = %+v", cb.bs.kind, cb.bs.how)
	}
}

// estimateSize counts the state and never encodes the build.
func TestEstimateSize_CountsTheState(t *testing.T) {
	e := newIncEnv(t)
	e.linear(1, 5)
	bs := e.full()
	with := estimateSize(bs)
	bs.state = nil
	without := estimateSize(bs)
	if with <= without || without <= 0 {
		t.Fatalf("estimate with state = %d, without = %d", with, without)
	}
}

// idle reports whether no view job is running or queued.
func (j *viewJobs) idle() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return !j.busy && len(j.order) == 0
}

func (e *diskEnv) waitJobsIdle() {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !e.dep.Views.jobs.idle() {
		if time.Now().After(deadline) {
			e.t.Fatal("the view job did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.flush()
}

// toIncremental serves the first view, stores one more call, and waits until the timeline
// cache holds an incremental build at the new version. The next view request is built from it.
func (e *diskEnv) toIncremental(path string) {
	e.t.Helper()
	e.call("a", 1, 0, 1, "user:one")
	e.mustGet(path)
	e.flush()
	e.call("b", 2, 0, 1, "user:one", "assistant:two", "user:three")
	e.mustGet(path) // serves the older build and asks for a rebuild
	deadline := time.Now().Add(5 * time.Second)
	for e.dep.Timelines.buildCounts()["incremental"] == 0 {
		if time.Now().After(deadline) {
			e.t.Fatalf("no incremental build: %v", e.dep.Timelines.buildCounts())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// countFullBuilds counts the full builds made outside the timeline cache's own first one.
func countFullBuilds(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	real := buildSessionFn
	buildSessionFn = func(c sessionSource, who sessIdent, rules ClientRules, cut int) (builtSession, error) {
		n.Add(1)
		return real(c, who, rules, cut)
	}
	t.Cleanup(func() { buildSessionFn = real })
	return &n
}

// 040 D20 and P3-4: a view built from an incremental build is not written itself; an inactive
// session's view reaches the disk once, from a background full build, and equals the view served.
func TestIncremental_InactiveSessionViewIsWrittenByAFullBuildJob(t *testing.T) {
	e := newDiskEnv(t)
	path := "/api/sessions/" + sid() + "/view"
	e.toIncremental(path)
	full := countFullBuilds(t)
	served := e.mustGet(path)
	e.mustGet(path)
	e.waitJobsIdle()
	ident := store.SessionIdent{Principal: core.PrincipalLocal, Client: "cc", Key: "k"}
	vers, err := e.d.Reader.WithContext(t.Context()).SessionVersions([]store.SessionIdent{ident})
	if err != nil {
		t.Fatal(err)
	}
	effs, err := e.d.Reader.WithContext(t.Context()).EffectVersions([]store.SessionIdent{ident})
	if err != nil {
		t.Fatal(err)
	}
	ent, ok := e.disk.GetView(t.Context(), ident, viewKeyOf(vers[ident], effs[ident]), e.now)
	if !ok {
		t.Fatal("the view at the current version was not written")
	}
	got, err := json.Marshal(ent.view)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(served, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("written view differs from the served one:\n%s\n%s", got, served)
	}
	if n := full.Load(); n != 1 {
		t.Fatalf("full builds = %d, want exactly one job", n)
	}
	if got := e.dep.Timelines.buildCounts(); got["full:no_state"] != 1 {
		t.Fatalf("the job's build entered the timeline cache: %v", got)
	}
}

// An active session's view is not written (D18): no job starts.
func TestIncremental_ActiveSessionStartsNoViewJob(t *testing.T) {
	e := newDiskEnv(t)
	e.now = baseTime.Add(4 * time.Second)
	path := "/api/sessions/" + sid() + "/view"
	e.toIncremental(path)
	full := countFullBuilds(t)
	e.mustGet(path)
	e.waitJobsIdle()
	if full.Load() != 0 || len(e.files("v")) != 0 {
		t.Fatalf("full builds = %d, view files = %v", full.Load(), e.files("v"))
	}
}

// Without an on-disk cache nothing runs.
func TestIncremental_NoDiskNoViewJob(t *testing.T) {
	e := newDiskEnv(t)
	e.dep.Disk = nil
	e.dep.Views = NewViewCache()
	e.srv = Handler(e.dep)
	path := "/api/sessions/" + sid() + "/view"
	e.toIncremental(path)
	full := countFullBuilds(t)
	e.mustGet(path)
	e.mustGet(path)
	e.waitJobsIdle()
	if full.Load() != 0 || len(e.files("v")) != 0 {
		t.Fatalf("full builds = %d, view files = %v", full.Load(), e.files("v"))
	}
}

// Two requests for one session and version start one job; a newer version replaces a
// queued older one, and one job runs at a time.
func TestViewJobs_DedupeReplaceAndSerialise(t *testing.T) {
	var j viewJobs
	id := store.SessionIdent{Principal: "p", Client: "c", Key: "k"}
	other := store.SessionIdent{Principal: "p", Client: "c", Key: "o"}
	gate := make(chan struct{})
	started := make(chan string, 8)
	var running, maxRunning atomic.Int32
	job := func(name string, block bool) func() {
		return func() {
			n := running.Add(1)
			for m := maxRunning.Load(); n > m && !maxRunning.CompareAndSwap(m, n); m = maxRunning.Load() {
			}
			started <- name
			if block {
				<-gate
			}
			running.Add(-1)
		}
	}
	j.submit(zap.NewNop(), other, "o1", job("o1", true))
	if got := <-started; got != "o1" {
		t.Fatal(got)
	}
	j.submit(zap.NewNop(), id, "k1", job("k1", false))
	j.submit(zap.NewNop(), id, "k1", job("k1-again", false)) // same key: dropped
	j.submit(zap.NewNop(), id, "k2", job("k2", false))       // replaces the queued k1
	j.submit(zap.NewNop(), other, "o1", job("o1-again", false))
	close(gate)
	deadline := time.Now().Add(5 * time.Second)
	for !j.idle() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(started)
	var names []string
	for n := range started {
		names = append(names, n)
	}
	if strings.Join(names, ",") != "k2" || maxRunning.Load() != 1 {
		t.Fatalf("ran %v, max concurrent %d", names, maxRunning.Load())
	}
}

// D7: a hook event or an effect pokes the session's view streams at once, not after its window.
func TestHub_HookAndEffectPokeViewStreamsAtOnce(t *testing.T) {
	h := testHub(4, &fakeRead{})
	defer h.Close()
	id := testSess.id()
	view, err := h.subscribeView(id, true)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := h.subscribeView(sessIdent{"local", "cc", "other"}.id(), true)
	expect := func(what string, s *subscriber, want bool) {
		t.Helper()
		select {
		case <-s.dirty:
			if !want {
				t.Fatalf("%s: poked", what)
			}
		case <-time.After(60 * time.Millisecond): // far under hookRefreshEvery and effectsEvery
			if want {
				t.Fatalf("%s: not poked at once", what)
			}
		}
	}
	h.PublishHook("local", "cc", "k")
	expect("hook", view, true)
	expect("hook, another session", other, false)
	h.PublishEffects("local", "cc", "k")
	expect("effect", view, true)
	h.PublishEffects("", "", "") // unassigned connections poke no session (D21)
	expect("unassigned effect", view, false)
}

// AC9 and D14: a burst of pokes during a slow build never drops the view stream and leaves
// one pending rebuild.
func TestHub_ViewStreamSurvivesPokeBurst(t *testing.T) {
	var builds atomic.Int32
	var slow atomic.Bool
	gate := make(chan struct{})
	e := newStreamEnvRules(t, func(h *Hub) {
		h.viewOf = func(ctx context.Context, _ sessIdent) (timeline.View, error) {
			builds.Add(1)
			if slow.Load() {
				select {
				case <-gate:
				case <-ctx.Done():
				}
			}
			return timeline.View{}, nil
		}
	}, map[string]ClientRules{"cc": testRules{}})
	s := e.open(e.sessionPath() + "?view=1")
	nextView := func() {
		t.Helper()
		for {
			if ev := s.next(); ev.event == "view" {
				return
			}
		}
	}
	nextView()
	waitBuilds := func(n int32) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for builds.Load() < n {
			if time.Now().After(deadline) {
				t.Fatalf("builds = %d, want %d", builds.Load(), n)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
	slow.Store(true)
	id := testSess.id()
	e.hub.pokeViews(id)
	waitBuilds(2) // the second build is under way and blocked
	for range 200 {
		e.hub.pokeViews(id)
	}
	if e.hub.Streams() != 1 {
		t.Fatalf("streams = %d: the view stream was dropped by the burst", e.hub.Streams())
	}
	slow.Store(false)
	close(gate)
	nextView() // the blocked build's
	nextView() // the one pending rebuild
	time.Sleep(3 * viewDebounce)
	if got := builds.Load(); got != 3 {
		t.Fatalf("builds = %d, want 3: the burst did not collapse into one", got)
	}
	e.hub.pokeViews(id)
	nextView() // still open
}
