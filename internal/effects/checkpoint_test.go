package effects

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/hooks"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

var testSession = store.SessionIdent{Principal: "local", Client: "client-a", Key: "s1"}

type testStore struct {
	dir string
	st  *store.Store
}

func openStore(t *testing.T) testStore {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return testStore{dir: dir, st: st}
}

// effects lists every stored effect of the test session.
func (ts testStore) effects(t *testing.T) []store.EffectRow {
	t.Helper()
	r, err := store.OpenReader(ts.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	rows, err := r.WithContext(t.Context()).ListEffects(testSession, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (ts testStore) content(t *testing.T, hash string) string {
	t.Helper()
	r, err := store.OpenReader(ts.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	b, err := r.Content(hash)
	if err != nil {
		t.Fatalf("content %q: %v", hash, err)
	}
	return string(b)
}

// newTestRunner is a runner on st whose temp and home rules let t.TempDir be watched.
func newTestRunner(t *testing.T, st *store.Store) *Runner {
	t.Helper()
	r := NewRunner(config.Defaults().Effects, st, nil, nil, zap.NewNop())
	r.temps, r.home = nil, ""
	t.Cleanup(func() { r.Close(context.Background()) })
	return r
}

// checkpointIn runs one checkpoint of the test session working in dir, in this goroutine.
func checkpointIn(t *testing.T, r *Runner, dir string) {
	t.Helper()
	r.mu.Lock()
	s := r.sessions[testSession]
	if s == nil {
		s = &session{ident: testSession, tops: map[string]string{}, workDir: dir}
		r.sessions[testSession] = s
	}
	r.mu.Unlock()
	if err := r.runCheckpoint(t.Context(), s); err != nil {
		t.Fatal(err)
	}
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed; git mode is not checked here")
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// byPath maps each effect's path to its row; a path with two effects fails the test.
func byPath(t *testing.T, rows []store.EffectRow) map[string]store.EffectRow {
	t.Helper()
	out := map[string]store.EffectRow{}
	for _, e := range rows {
		if _, dup := out[e.Path]; dup {
			t.Fatalf("two effects for %s", e.Path)
		}
		out[e.Path] = e
	}
	return out
}

func repo(t *testing.T) string {
	t.Helper()
	needGit(t)
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	return dir
}

// AC2, AC3, AC5: the first checkpoint records the repo and stores nothing; the next one
// reports a create, a modify and a delete, with a copy of the new content and the last
// committed version as the modified file's before.
func TestCheckpoint_GitRepoChanges(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	write(t, filepath.Join(dir, "b.txt"), "gone soon\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	ts := openStore(t)
	r := newTestRunner(t, ts.st)

	checkpointIn(t, r, dir)
	if rows := ts.effects(t); len(rows) != 0 {
		t.Fatalf("baseline stored %d effects, want none", len(rows))
	}

	write(t, filepath.Join(dir, "a.txt"), "one\ntwo\n")
	write(t, filepath.Join(dir, "c.txt"), "new\n")
	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	checkpointIn(t, r, dir)
	got := byPath(t, ts.effects(t))
	if len(got) != 3 {
		t.Fatalf("effects = %+v, want 3", got)
	}
	a, c, b := got[filepath.Join(dir, "a.txt")], got[filepath.Join(dir, "c.txt")], got[filepath.Join(dir, "b.txt")]
	if a.Change != "modified" || a.Mode != "git" || a.Size != 8 || ts.content(t, a.AfterHash) != "one\ntwo\n" || ts.content(t, a.BeforeHash) != "one\n" {
		t.Errorf("a.txt = %+v", a)
	}
	if c.Change != "created" || ts.content(t, c.AfterHash) != "new\n" || c.BeforeHash != "" {
		t.Errorf("c.txt = %+v", c)
	}
	if b.Change != "deleted" || b.AfterHash != "" {
		t.Errorf("b.txt = %+v", b)
	}

	// A later change diffs against the previous copy, not against the commit.
	write(t, filepath.Join(dir, "a.txt"), "one\ntwo\nthree\n")
	checkpointIn(t, r, dir)
	rows := ts.effects(t)
	last := rows[len(rows)-1]
	if last.Path != filepath.Join(dir, "a.txt") || ts.content(t, last.BeforeHash) != "one\ntwo\n" {
		t.Errorf("second modify = %+v", last)
	}
}

// AC3, AC4: a folder with no git is walked: create, modify and delete are reported, and
// a path under an ignored folder is not.
func TestCheckpoint_PlainFolder(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "keep.txt"), "v1\n")
	write(t, filepath.Join(dir, "old.txt"), "x\n")
	ts := openStore(t)
	r := newTestRunner(t, ts.st)
	checkpointIn(t, r, dir)

	write(t, filepath.Join(dir, "keep.txt"), "v2 longer\n")
	write(t, filepath.Join(dir, "sub", "new.txt"), "n\n")
	write(t, filepath.Join(dir, "node_modules", "dep.js"), "x\n")
	if err := os.Remove(filepath.Join(dir, "old.txt")); err != nil {
		t.Fatal(err)
	}
	checkpointIn(t, r, dir)
	got := byPath(t, ts.effects(t))
	want := map[string]string{
		filepath.Join(dir, "keep.txt"):       "modified",
		filepath.Join(dir, "sub", "new.txt"): "created",
		filepath.Join(dir, "old.txt"):        "deleted",
	}
	if len(got) != len(want) {
		t.Fatalf("effects = %+v, want %v", got, want)
	}
	for p, change := range want {
		if e := got[p]; e.Change != change || e.Mode != "scan" {
			t.Errorf("%s = %+v, want %s in scan mode", p, e, change)
		}
	}
	if keep := got[filepath.Join(dir, "keep.txt")]; keep.BeforeHash != "" || ts.content(t, keep.AfterHash) != "v2 longer\n" {
		t.Errorf("keep.txt = %+v; no earlier copy, so no before and the full content", keep)
	}
}

// AC4, E7: a git-ignored .env is checked; the contents of a git-ignored folder are not.
func TestCheckpoint_GitIgnoredEnvSeen(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, ".gitignore"), ".env\ncache-dir/\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	ts := openStore(t)
	r := newTestRunner(t, ts.st)
	checkpointIn(t, r, dir)

	write(t, filepath.Join(dir, ".env"), "TOKEN=abc\n")
	write(t, filepath.Join(dir, "cache-dir", "big.bin"), "x\n")
	checkpointIn(t, r, dir)
	got := byPath(t, ts.effects(t))
	env, ok := got[filepath.Join(dir, ".env")]
	if !ok || env.Change != "created" || len(got) != 1 {
		t.Fatalf("effects = %+v, want only .env created", got)
	}
}

// AC2, E10: a new runner on the same store compares against the saved snapshot, so a
// change made while the gateway was down is reported, not taken as a new baseline.
func TestCheckpoint_RestartKeepsSnapshot(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "f.txt"), "before\n")
	ts := openStore(t)
	first := newTestRunner(t, ts.st)
	checkpointIn(t, first, dir)
	first.Close(context.Background())

	write(t, filepath.Join(dir, "f.txt"), "after the restart\n")
	second := newTestRunner(t, ts.st)
	checkpointIn(t, second, dir)
	rows := ts.effects(t)
	if len(rows) != 1 || rows[0].Change != "modified" || rows[0].Path != filepath.Join(dir, "f.txt") {
		t.Fatalf("effects = %+v, want f.txt modified", rows)
	}
}

// AC6, E8: watched folders are learned from tool paths outside them (the git work tree,
// or a written file's folder), folded when one holds another, and never /, the home
// folder or a temp folder.
func TestRoots_LearnedAndRejected(t *testing.T) {
	rs := newRootSet("/home/u", defaultTemps())
	for _, dir := range []string{"/", "/home", "/home/u", "/tmp", "/tmp/x", filepath.Join(os.TempDir(), "y")} {
		if added, _ := rs.add(dir); added {
			t.Errorf("%s was added; it must be rejected", dir)
		}
	}
	if added, _ := rs.add("/home/u/a/b"); !added {
		t.Fatal("/home/u/a/b not added")
	}
	if added, folded := rs.add("/home/u/a"); !added || len(folded) != 1 || folded[0] != "/home/u/a/b" {
		t.Errorf("adding /home/u/a: added=%v folded=%v, want /home/u/a/b folded", added, folded)
	}
	if added, _ := rs.add("/home/u/a/c"); added {
		t.Error("a folder inside a watched one was added")
	}

	needGit(t)
	work, other, plain := t.TempDir(), repo(t), t.TempDir()
	write(t, filepath.Join(other, "lib", "x.go"), "package x\n")
	ts := openStore(t)
	r := newTestRunner(t, ts.st)
	r.mu.Lock()
	s := &session{ident: testSession, tops: map[string]string{}, workDir: work,
		learn: []learned{
			{path: filepath.Join(other, "lib", "x.go")},               // a read in a work tree: its top level
			{path: filepath.Join(plain, "out", "w.txt"), write: true}, // a write outside any: its folder
			{path: filepath.Join(plain, "r.txt")},                     // a read outside any: nothing
		}}
	r.sessions[testSession] = s
	r.mu.Unlock()
	if err := r.runCheckpoint(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	got := s.roots.list()
	want := []string{work, other, filepath.Join(plain, "out")}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("roots = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != filepath.Clean(want[i]) {
			t.Errorf("roots = %v, want %v", got, want)
		}
	}
}

// AC1: a burst of hook events while a checkpoint runs costs exactly one follow-up
// checkpoint: one running and one queued, never one per event.
func TestRunner_BurstRunsOneFollowUp(t *testing.T) {
	ts := openStore(t)
	r := newTestRunner(t, ts.st)
	var runs atomic.Int32
	r.check = func(ctx context.Context, s *session) error {
		runs.Add(1)
		time.Sleep(200 * time.Millisecond)
		return nil
	}
	ev := core.HarnessEvent{Phase: core.PhasePostTool, SessionKey: testSession.Key, WorkDir: t.TempDir()}
	for range 50 {
		r.OnStored(hooks.HookStored{Principal: testSession.Principal, Client: testSession.Client,
			SessionKey: testSession.Key, At: time.Now(), Event: ev})
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r.mu.Lock()
		busy := r.sessions[testSession].running
		r.mu.Unlock()
		if !busy || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := runs.Load(); n != 2 {
		t.Fatalf("checkpoints run = %d, want 2", n)
	}
}

// spec 015 AC1, E7: a file change carries its window, from the previous checkpoint's
// start to the end of the scan that found it; a runner with no previous checkpoint in
// memory starts the window at the session's last hook before the one that made it.
func TestCheckpoint_StoresWindow(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "f.txt"), "one\n")
	ts := openStore(t)
	clock := time.Unix(1_800_000_000, 0)
	var calls []time.Time
	tick := func() time.Time {
		clock = clock.Add(time.Second)
		calls = append(calls, clock)
		return clock
	}
	r := newTestRunner(t, ts.st)
	r.now = tick
	checkpointIn(t, r, dir)
	firstStart := calls[0]
	write(t, filepath.Join(dir, "f.txt"), "two\n")
	checkpointIn(t, r, dir)
	rows := ts.effects(t)
	if len(rows) != 1 {
		t.Fatalf("effects = %+v, want one", rows)
	}
	if got, want := rows[0].WindowFrom, firstStart.UnixNano(); got != want {
		t.Errorf("window_from = %d, want the first checkpoint's start %d", got, want)
	}
	if got, want := rows[0].WindowTo, calls[len(calls)-1].UnixNano(); got != want {
		t.Errorf("window_to = %d, want the second scan's end %d", got, want)
	}
	r.Close(context.Background())

	// After a restart: two hooks were stored, the second one makes the memory entry.
	var ids []int64
	for i, at := range []time.Time{clock.Add(time.Second), clock.Add(2 * time.Second)} {
		rec := &store.HookRecord{PrincipalID: testSession.Principal, Client: testSession.Client, Seq: int64(i + 1),
			ReceivedAt: at, Payload: []byte(`{}`), Event: core.HarnessEvent{Phase: core.PhasePrompt, SessionKey: testSession.Key, WorkDir: dir}}
		if err := ts.st.SaveHook(t.Context(), rec); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, rec.ID)
	}
	second := newTestRunner(t, ts.st)
	second.sessions[testSession] = &session{ident: testSession, tops: map[string]string{}, workDir: dir, firstHookID: ids[1]}
	write(t, filepath.Join(dir, "f.txt"), "three\n")
	checkpointIn(t, second, dir)
	rows = ts.effects(t)
	if len(rows) != 2 {
		t.Fatalf("effects = %+v, want two", rows)
	}
	if got, want := rows[1].WindowFrom, clock.Add(time.Second).UnixNano(); got != want {
		t.Errorf("after a restart window_from = %d, want the earlier hook's time %d", got, want)
	}
}
