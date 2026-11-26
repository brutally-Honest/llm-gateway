package ui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/cost"
	"github.com/brutally-honest/llm-gateway/internal/observability"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// Hashing the test binary for every cache would slow the tests for nothing: a fixed
// executable hash stands in. TestHashExecutable_ReadsTheRunningBinary covers the real one.
func init() {
	hashExe = func() ([sha256.Size]byte, error) { return [sha256.Size]byte{7}, nil }
}

// diskEnv is a store and a UI over it with an on-disk cache; restart builds the same UI
// again with empty memory, as a new process would.
type diskEnv struct {
	*digestEnv
	now    time.Time
	disk   *DiskCache
	dep    Deps
	srv    http.Handler
	logs   *observer.ObservedLogs
	cancel context.CancelFunc
	reg    *observability.Metrics
}

func newDiskEnv(t *testing.T) *diskEnv {
	t.Helper()
	e := &diskEnv{digestEnv: withRules(t), now: baseTime.Add(time.Hour)}
	e.restart("v1")
	return e
}

// restart replaces the caches, the cache's writer and the handler with new ones, over the
// same store and folder.
func (e *diskEnv) restart(version string) {
	e.t.Helper()
	if e.cancel != nil {
		e.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.t.Cleanup(cancel)
	core, logs := observer.New(zap.DebugLevel)
	e.logs = logs
	log := zap.New(core)
	e.reg = observability.NewMetrics("test")
	disk, err := OpenDiskCache(ctx, e.dir, version, 0, e.d.Reader, log, e.reg)
	if err != nil {
		e.t.Fatal(err)
	}
	e.disk = disk
	e.dep = Deps{Addr: addr, Reader: e.d.Reader, Rules: map[string]ClientRules{"cc": testRules{}}, Ctx: ctx, Log: log,
		Digests: NewDigestCache(ctx, log).WithDisk(disk), Timelines: NewTimelineCache(ctx, log), Views: NewViewCache(),
		Disk: disk, Metrics: e.reg, Now: func() time.Time { return e.now }}
	e.srv = Handler(e.dep)
}

func (e *diskEnv) get(path string) (int, []byte) {
	e.t.Helper()
	rec := httptest.NewRecorder()
	e.srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+addr+path, nil))
	return rec.Code, rec.Body.Bytes()
}

func (e *diskEnv) mustGet(path string) []byte {
	e.t.Helper()
	code, b := e.get(path)
	if code != 200 {
		e.t.Fatalf("GET %s = %d %s", path, code, b)
	}
	return b
}

// mustGetReady is mustGet, asked again until no row is pending: the list answers at once
// with the cheap fields (spec 043) and the facts follow.
func (e *diskEnv) mustGetReady(path string) []byte {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		b := e.mustGet(path)
		if !bytes.Contains(b, []byte(`"pending":true`)) {
			return b
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("GET %s: rows still pending after 10 s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *diskEnv) flush() { e.disk.flush(context.Background()) }

func (e *diskEnv) computes() int64 { return e.dep.Digests.computes.Load() }

// files lists the entries in the build folder's m or v folder, temp files left out.
func (e *diskEnv) files(kind string) []string {
	e.t.Helper()
	ents, err := os.ReadDir(filepath.Join(e.disk.root, kind))
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range ents {
		if !strings.HasPrefix(f.Name(), diskTmp) {
			out = append(out, f.Name())
		}
	}
	return out
}

func (e *diskEnv) fileOf(kind string, k string) string {
	return filepath.Join(e.disk.root, kind, fileName(store.SessionIdent{Principal: core.PrincipalLocal, Client: "cc", Key: k}))
}

// seed stores session k (one call with a prompt) and two more sessions, all idle at now.
func (e *diskEnv) seed() {
	e.call("a", 1, 0, 0, "user:fix the retry")
	e.save("o1", "k2", "cc", 2)
	e.save("o2", "k3", "cc", 3)
}

func listPath(offset int) string { return "/api/sessions?limit=1&offset=" + string(rune('0'+offset)) }

// effect stores a file change of session k.
func (e *diskEnv) effect(name string) {
	e.t.Helper()
	ef := core.Effect{Kind: core.EffectFileChange, At: baseTime.Add(2 * time.Second), Principal: core.PrincipalLocal,
		Client: "cc", SessionKey: "k", Root: "/w", Path: "/w/" + name, Change: core.FileCreated, Size: 1, Mode: "git"}
	if err := e.s.SaveEffects(e.t.Context(), []core.Effect{ef}, nil); err != nil {
		e.t.Fatal(err)
	}
}

// With entries on disk, a new process answers page 1, page 2 and a later page of the list
// and the session page without computing or building anything, and with the same bytes.
func TestDiskCache_RestartServesFromDisk(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	var lists, views [][]byte
	for i := range 3 {
		lists = append(lists, e.mustGetReady(listPath(i)))
	}
	views = append(views, e.mustGet("/api/sessions/"+sid()+"/view"))
	e.flush()
	if got := len(e.files("m")); got != 3 {
		t.Fatalf("memo files = %d, want 3", got)
	}
	if got := len(e.files("v")); got != 1 {
		t.Fatalf("view files = %d, want 1", got)
	}

	e.restart("v1")
	builds := countBuilds(t)
	// the requests arrive before any background pass: pages 1, 2 and a later one
	for i := range 3 {
		if got := e.mustGet(listPath(i)); !bytes.Equal(got, lists[i]) {
			t.Errorf("page %d differs after restart:\n%s\n%s", i, got, lists[i])
		}
	}
	if got := e.mustGet("/api/sessions/" + sid() + "/view"); !bytes.Equal(got, views[0]) {
		t.Error("the view differs after restart")
	}
	if n := e.computes(); n != 0 {
		t.Errorf("computes after restart = %d, want 0", n)
	}
	if n := builds.Load(); n != 0 {
		t.Errorf("session builds after restart = %d, want 0", n)
	}
	if n := e.disk.damaged.Load(); n != 0 {
		t.Errorf("damaged = %d", n)
	}
	text := scrapeMetrics(t, e.reg)
	for _, want := range []string{
		`gateway_ui_view_cache_reads_total{kind="memo",result="hit"} 3`,
		`gateway_ui_view_cache_reads_total{kind="view",result="hit"} 1`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("metrics have no %q", want)
		}
	}
}

func scrapeMetrics(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

// No entry: the memo and the view are computed, then written.
func TestDiskCache_MissComputesThenWrites(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.mustGetReady(listPath(0))
	if e.computes() == 0 {
		t.Fatal("nothing computed on a miss")
	}
	e.mustGet("/api/sessions/" + sid() + "/view")
	e.flush()
	if len(e.files("m")) == 0 || len(e.files("v")) != 1 {
		t.Errorf("after a miss: %d memo files, %d view files", len(e.files("m")), len(e.files("v")))
	}
	text := scrapeMetrics(t, e.reg)
	if !strings.Contains(text, `gateway_ui_view_cache_reads_total{kind="view",result="miss"} 1`) {
		t.Errorf("the view miss was not counted:\n%s", text)
	}
}

// A new exchange in the session changes its version: the old entries miss (a plain miss,
// not damage), the session is rebuilt with it and a new entry is written.
func TestDiskCache_StaleSessionVersionMisses(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.mustGet(listPath(0))
	e.mustGet("/api/sessions/" + sid() + "/view")
	e.flush()

	e.call("b", 4, 0, 0, "user:fix the retry", "assistant:done")
	e.restart("v1")
	before := e.computes()
	var v map[string]any
	if err := json.Unmarshal(e.mustGet("/api/sessions/"+sid()+"/view"), &v); err != nil {
		t.Fatal(err)
	}
	if o := v["overview"].(map[string]any); o["calls"] != 2.0 {
		t.Errorf("calls = %v, want the view of the new version (2)", o["calls"])
	}
	sess := e.mustGet("/api/sessions/" + sid())
	if !bytes.Contains(sess, []byte(`"exchanges":2`)) {
		t.Errorf("summary is not at the new version: %s", sess)
	}
	if e.computes() == before {
		t.Error("the memo was not recomputed")
	}
	if n := e.disk.damaged.Load(); n != 0 {
		t.Errorf("a key mismatch counted as damage: %d", n)
	}
	e.flush()

	e.restart("v1") // the rebuilt entries now hit
	builds := countBuilds(t)
	e.mustGet("/api/sessions/" + sid())
	e.mustGet("/api/sessions/" + sid() + "/view")
	if e.computes() != 0 || builds.Load() != 0 {
		t.Errorf("the new entries did not hit: %d computes, %d builds", e.computes(), builds.Load())
	}
}

// A new effect changes the effect version: both entries miss.
func TestDiskCache_EffectVersionChangeMisses(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.mustGet("/api/sessions/" + sid())
	e.mustGet("/api/sessions/" + sid() + "/view")
	e.flush()

	e.effect("a.txt")
	e.restart("v1")
	builds := countBuilds(t)
	body := e.mustGet("/api/sessions/" + sid() + "/view")
	e.mustGet("/api/sessions/" + sid())
	if e.computes() == 0 {
		t.Error("the memo hit across an effect change")
	}
	if builds.Load() == 0 {
		t.Error("the view hit across an effect change")
	}
	if !bytes.Contains(body, []byte("a.txt")) {
		t.Errorf("the view does not show the new effect: %s", body)
	}
}

// A connection with no session stored later, inside the session's range, makes its view a
// miss in memory and on disk (D15).
func TestDiskCache_UnassignedConnectionStoredLaterMisses(t *testing.T) {
	late := func(e *diskEnv) {
		c := core.Effect{Kind: core.EffectNetConn, At: baseTime.Add(1500 * time.Millisecond), Principal: core.PrincipalLocal,
			Client: "cc", Host: "late.example.com", Port: 443, Via: "connect", EndedAt: baseTime.Add(2 * time.Second), Outcome: core.NetOK}
		if err := e.s.SaveEffects(t.Context(), []core.Effect{c}, nil); err != nil {
			t.Fatal(err)
		}
	}
	view := func(e *diskEnv) []byte { return e.mustGet("/api/sessions/" + sid() + "/view") }
	for _, tc := range []string{"memory", "disk"} {
		t.Run(tc, func(t *testing.T) {
			e := newDiskEnv(t)
			e.seed()
			if body := view(e); bytes.Contains(body, []byte("late.example.com")) {
				t.Fatal("the connection is shown before it exists")
			}
			e.flush()
			late(e)
			if tc == "disk" {
				e.restart("v1")
			}
			if body := view(e); !bytes.Contains(body, []byte("late.example.com")) {
				t.Errorf("%s: the view served is older than the store: %s", tc, body)
			}
		})
	}
}

// A new gateway version is a new build: its entries miss, and the old build's folder goes
// when it has sat idle long enough.
func TestDiskCache_NewVersionMissesAndOldBuildIsCleaned(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.mustGet("/api/sessions/" + sid())
	e.mustGet("/api/sessions/" + sid() + "/view")
	e.flush()
	old := e.disk.root

	e.restart("v2")
	if e.disk.root == old {
		t.Fatal("two versions share a build folder")
	}
	builds := countBuilds(t)
	e.mustGet("/api/sessions/" + sid())
	e.mustGet("/api/sessions/" + sid() + "/view")
	if e.computes() == 0 || builds.Load() == 0 {
		t.Errorf("a new version served old entries: %d computes, %d builds", e.computes(), builds.Load())
	}
	e.disk.Cleanup() // the old folder is recent: it stays
	if _, err := os.Stat(old); err != nil {
		t.Errorf("a recent build folder was deleted: %v", err)
	}
	long := time.Now().Add(-time.Hour)
	for _, p := range []string{old, filepath.Join(old, "m"), filepath.Join(old, "v"), filepath.Join(old, "high-water")} {
		if err := os.Chtimes(p, long, long); err != nil {
			t.Fatal(err)
		}
	}
	e.disk.Cleanup()
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an idle old build folder is still there: %v", err)
	}
	if _, err := os.Stat(e.disk.root); err != nil {
		t.Errorf("the current build folder is gone: %v", err)
	}
}

// Every other input of the build identity changes it.
func TestDiskCache_BuildIdentity(t *testing.T) {
	e := newDiskEnv(t)
	open := func(version string, skew time.Duration, exe byte) string {
		real := hashExe
		hashExe = func() ([sha256.Size]byte, error) { return [sha256.Size]byte{exe}, nil }
		defer func() { hashExe = real }()
		c, err := OpenDiskCache(t.Context(), t.TempDir(), version, skew, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return c.Build()
	}
	base := open("v", 0, 1)
	if got := open("v", 0, 1); got != base {
		t.Error("the same inputs gave two builds")
	}
	for name, got := range map[string]string{
		"version": open("w", 0, 1), "skew": open("v", time.Second, 1), "executable": open("v", 0, 2),
	} {
		if got == base {
			t.Errorf("%s does not change the build", name)
		}
	}
	if len(base) != 16 || e.disk.Build() == "" {
		t.Errorf("build id %q", base)
	}
}

func TestHashExecutable_ReadsTheRunningBinary(t *testing.T) {
	sum, err := hashExecutable()
	if err != nil || sum == ([sha256.Size]byte{}) {
		t.Fatalf("hashExecutable = %x, %v", sum, err)
	}
}

// With an executable that cannot be hashed there is no cache: an error, and the nil cache
// that comes of it is safe to use.
func TestDiskCache_NoExecutableHashMeansNoCache(t *testing.T) {
	real := hashExe
	hashExe = func() ([sha256.Size]byte, error) { return [sha256.Size]byte{}, errors.New("no exe") }
	defer func() { hashExe = real }()
	c, err := OpenDiskCache(t.Context(), t.TempDir(), "v", 0, nil, nil, nil)
	if err == nil || c != nil {
		t.Fatalf("OpenDiskCache = %v, %v, want an error and no cache", c, err)
	}
	id := store.SessionIdent{Principal: "p", Client: "c", Key: "k"}
	if _, ok := c.GetMemo(id, "x"); ok {
		t.Error("nil cache hit")
	}
	if _, ok := c.GetView(t.Context(), id, "x", time.Now()); ok || c.HasView(t.Context(), id, "x", time.Now()) {
		t.Error("nil cache hit")
	}
	c.PutMemo(id, store.Version{}, store.EffectVersion{}, digestMemo{})
	c.PutView(&viewEntry{id: id}, store.Version{}, store.EffectVersion{})
	c.WaitRoom(t.Context())
	c.Trim(t.Context())
	c.Cleanup()
	c.flush(t.Context())
}

// A damaged file is a miss, counted, warned about once and rebuilt; a key mismatch is not
// damage.
func TestDiskCache_DamagedEntries(t *testing.T) {
	breakIt := map[string]func(t *testing.T, path string){
		"truncated": func(t *testing.T, p string) {
			b, _ := os.ReadFile(p)
			if err := os.WriteFile(p, b[:len(b)/2], 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"bad checksum": func(t *testing.T, p string) {
			b, _ := os.ReadFile(p)
			b[len(b)-3] ^= 1
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"garbage": func(t *testing.T, p string) {
			if err := os.WriteFile(p, []byte("not a cache entry\x00\x01"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"unknown format": func(t *testing.T, p string) {
			b, _ := os.ReadFile(p)
			b = bytes.Replace(b, []byte(`"format":1`), []byte(`"format":99`), 1)
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"another session": func(t *testing.T, p string) {
			b, _ := os.ReadFile(p)
			b = bytes.Replace(b, []byte(`"k"`), []byte(`"z"`), 1)
			if err := os.WriteFile(p, b, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, damage := range breakIt {
		for _, kind := range []string{"m", "v"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				e := newDiskEnv(t)
				e.seed()
				e.mustGet("/api/sessions/" + sid())
				e.mustGet("/api/sessions/" + sid() + "/view")
				e.flush()
				damage(t, e.fileOf(kind, "k"))

				e.restart("v1")
				builds := countBuilds(t)
				e.mustGet("/api/sessions/" + sid())
				e.mustGet("/api/sessions/" + sid() + "/view")
				if kind == "m" && e.computes() == 0 {
					t.Error("a damaged memo was served")
				}
				if kind == "v" && builds.Load() == 0 {
					t.Error("a damaged view was served")
				}
				if n := e.disk.damaged.Load(); n < 1 {
					t.Errorf("damaged = %d, want at least 1", n)
				}
				if n := e.logs.FilterMessage("ui view cache: damaged entries ignored and removed").Len(); n != 1 {
					t.Errorf("%d warnings, want 1", n)
				}
				text := scrapeMetrics(t, e.reg)
				kindName := map[string]string{"m": "memo", "v": "view"}[kind]
				if !strings.Contains(text, `kind="`+kindName+`",result="damaged"} 1`) {
					t.Errorf("damage not counted for %s:\n%s", kindName, text)
				}
				// rebuilt: the file is whole again, so a third process hits
				e.flush()
				e.restart("v1")
				before := e.disk.damaged.Load()
				e.mustGet("/api/sessions/" + sid())
				e.mustGet("/api/sessions/" + sid() + "/view")
				if e.disk.damaged.Load() != before || e.computes() != 0 {
					t.Errorf("not repaired: damaged %d, computes %d", e.disk.damaged.Load()-before, e.computes())
				}
			})
		}
	}
}

// Warnings about damage come at most once a minute, each with the count since the last.
func TestDiskCache_DamageWarningsAreRateLimited(t *testing.T) {
	e := newDiskEnv(t)
	clock := time.Unix(1_000_000, 0)
	e.disk.now = func() time.Time { return clock }
	id := store.SessionIdent{Principal: core.PrincipalLocal, Client: "cc", Key: "k"}
	for range 5 {
		e.disk.damage(diskMemoDir, e.fileOf("m", "k"), "", errors.New("x"))
	}
	clock = clock.Add(30 * time.Second)
	e.disk.damage(diskMemoDir, e.fileOf("m", "k"), "", errors.New("x"))
	clock = clock.Add(31 * time.Second)
	e.disk.damage(diskMemoDir, e.fileOf("m", "k"), "", errors.New("x"))
	got := e.logs.FilterMessage("ui view cache: damaged entries ignored and removed").All()
	if len(got) != 2 {
		t.Fatalf("%d warnings, want 2", len(got))
	}
	if got[0].ContextMap()["count"] != int64(1) || got[1].ContextMap()["count"] != int64(6) {
		t.Errorf("counts = %v, %v, want 1 then 6", got[0].ContextMap()["count"], got[1].ContextMap()["count"])
	}
	_ = id
}

// The writer deletes a damaged file only if it is still the bad one: a fresh entry renamed
// in meanwhile stays.
func TestDiskCache_DeleteSparesAFreshFile(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.mustGet("/api/sessions/" + sid())
	e.flush()
	path := e.fileOf("m", "k")
	good, _ := os.ReadFile(path)
	bad := append([]byte("junk"), good...)
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bad)
	if err := os.WriteFile(path, good, 0o600); err != nil { // the fresh file lands before the writer runs
		t.Fatal(err)
	}
	e.disk.remove(path, hexOf(sum))
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a fresh entry was deleted: %v", err)
	}
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	e.disk.remove(path, hexOf(sum))
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the bad file is still there: %v", err)
	}
}

func hexOf(s [sha256.Size]byte) string {
	const d = "0123456789abcdef"
	out := make([]byte, 0, 64)
	for _, b := range s {
		out = append(out, d[b>>4], d[b&15])
	}
	return string(out)
}

// Deleting the folder while the gateway runs is safe: reads miss, the next write brings it
// back.
func TestDiskCache_FolderDeletedMidRun(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.mustGet("/api/sessions/" + sid())
	e.flush()
	if err := os.RemoveAll(filepath.Join(e.dir, diskDirName)); err != nil {
		t.Fatal(err)
	}
	e.dep.Digests = NewDigestCache(t.Context(), nil).WithDisk(e.disk)
	e.dep.Views = NewViewCache()
	e.srv = Handler(e.dep)
	e.mustGet("/api/sessions/" + sid())
	e.mustGet("/api/sessions/" + sid() + "/view")
	e.flush()
	if len(e.files("m")) != 1 || len(e.files("v")) != 1 {
		t.Errorf("the folder was not recreated: %v %v", e.files("m"), e.files("v"))
	}
	if _, err := os.Stat(filepath.Join(e.disk.root, diskHighWat)); err != nil {
		t.Errorf("no high-water file after the folder came back: %v", err)
	}
}

// A session that moves while its view is built is not written under the older key (AC5);
// the next read rebuilds and serves data at least as new as the store version it read.
func TestDiskCache_LiveUpdateDuringBuildIsNotWritten(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	real := buildSessionFn
	var once atomic.Bool
	buildSessionFn = func(c sessionSource, who sessIdent, rules ClientRules, cut int) (builtSession, error) {
		bs, err := real(c, who, rules, cut)
		if who.name == "k" && once.CompareAndSwap(false, true) {
			e.call("late", 9, 0, 0, "user:fix the retry", "assistant:done") // a live update lands mid-build
		}
		return bs, err
	}
	t.Cleanup(func() { buildSessionFn = real })

	Prefill(t.Context(), e.dep)
	e.flush()
	if !once.Load() {
		t.Fatal("the build hook never ran")
	}
	if len(e.files("v")) != 2 { // k2 and k3 were written; k moved on
		t.Errorf("view files = %d, want 2 (not the moved session's)", len(e.files("v")))
	}
	if _, err := os.Stat(e.fileOf("v", "k")); err == nil {
		t.Error("a view of a session that moved while it was built was written")
	}
	if e.disk.skipped.Load() == 0 {
		t.Error("the stale write was not counted as skipped")
	}
	e.restart("v1") // a new process rebuilds the moved session; nothing older is on disk to serve
	var v map[string]any
	if err := json.Unmarshal(e.mustGet("/api/sessions/"+sid()+"/view"), &v); err != nil {
		t.Fatal(err)
	}
	if o := v["overview"].(map[string]any); o["calls"] != 2.0 {
		t.Errorf("served calls = %v, want 2 (the store's)", o["calls"])
	}
	e.flush()
	if _, err := os.Stat(e.fileOf("v", "k")); err != nil {
		t.Errorf("the rebuilt view was not written: %v", err)
	}
}

// The memo of a session whose version moved before the write is dropped, never written
// under the older key.
func TestDiskCache_WriterDropsAMemoForAMovedSession(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	c := e.dep.Reader.WithContext(t.Context())
	id := store.SessionIdent{Principal: core.PrincipalLocal, Client: "cc", Key: "k"}
	vers, _ := c.SessionVersions([]store.SessionIdent{id})
	effs, _ := c.EffectVersions([]store.SessionIdent{id})
	e.call("b", 4, 0, 0, "user:fix the retry", "assistant:done")
	e.disk.PutMemo(id, vers[id], effs[id], digestMemo{})
	e.flush()
	if len(e.files("m")) != 0 {
		t.Error("a memo of an older version was written")
	}
	if _, err := os.Stat(filepath.Join(e.disk.root, diskHighWat)); err == nil {
		t.Error("the high-water file was raised for an entry that was not written")
	}
	// at the current version it is written
	vers, _ = c.SessionVersions([]store.SessionIdent{id})
	e.disk.PutMemo(id, vers[id], effs[id], digestMemo{})
	e.flush()
	if len(e.files("m")) != 1 {
		t.Error("a memo at the current version was not written")
	}
}

// The background pass fills the cache for every session: memos for all, views for idle
// ones. A second process then answers every page from disk, before its own pass starts.
func TestPrefill_BuildsMissingEntries(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.save("hot", "k4", "cc", 3590) // 10 s before now: active, so no view is built for it
	e.now = baseTime.Add(3600 * time.Second)
	Prefill(t.Context(), e.dep)
	e.flush()
	if got := len(e.files("m")); got != 4 {
		t.Errorf("memo files = %d, want 4", got)
	}
	if got := len(e.files("v")); got != 3 {
		t.Errorf("view files = %d, want 3 (the active session has none)", got)
	}
	if !strings.Contains(scrapeMetrics(t, e.reg), "gateway_ui_cache_rebuild_seconds ") {
		t.Error("no rebuild gauge")
	}

	e.restart("v1")
	builds := countBuilds(t)
	for i := range 4 {
		e.mustGet(listPath(i))
	}
	for _, k := range []string{"k", "k2", "k3"} {
		e.mustGet("/api/sessions/" + sessionID(store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: k}) + "/view")
	}
	if e.computes() != 0 || builds.Load() != 0 {
		t.Errorf("a restart after the pass computed %d memos and built %d sessions", e.computes(), builds.Load())
	}
	// a second pass finds nothing missing
	Prefill(t.Context(), e.dep)
	if e.computes() != 0 || builds.Load() != 0 {
		t.Errorf("a pass over a full cache computed %d memos and built %d sessions", e.computes(), builds.Load())
	}
}

// The pass stops when its context ends.
func TestPrefill_StopsOnCancel(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	Prefill(ctx, e.dep)
	if len(e.files("m")) != 0 {
		t.Error("a cancelled pass built entries")
	}
}

// The view of an active session is not written.
func TestDiskCache_ActiveViewNotWritten(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.now = baseTime.Add(100 * time.Second) // the sessions are 97-99 s old: active
	e.mustGet("/api/sessions/" + sid() + "/view")
	e.mustGetReady(listPath(0))
	e.flush()
	if len(e.files("v")) != 0 {
		t.Errorf("an active session's view was written: %v", e.files("v"))
	}
	if len(e.files("m")) == 0 {
		t.Error("memos of active sessions are written")
	}
}

// A view whose timeline build is older than the version read for the request is served
// (it is what the in-memory cache does) but not written.
func TestDiskCache_ViewOfAnOlderBuildNotWritten(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.mustGet("/api/sessions/" + sid() + "/view")
	e.flush()
	e.dep.Views = NewViewCache() // memory forgets the view; the timeline cache keeps its build
	e.srv = Handler(e.dep)
	wrote, skipped := e.disk.written.Load(), e.disk.skipped.Load()

	// the session moves on; a request that takes whatever timeline is cached gets the old build
	e.call("b", 4, 0, 0, "user:fix the retry", "assistant:done")
	real := buildSessionFn
	gate := make(chan struct{})
	buildSessionFn = func(c sessionSource, who sessIdent, rules ClientRules, cut int) (builtSession, error) {
		<-gate
		return real(c, who, rules, cut)
	}
	t.Cleanup(func() { buildSessionFn = real })
	e.mustGet("/api/sessions/" + sid() + "/view")
	e.flush()
	close(gate)
	for { // the timeline build the request started runs on; let it end before the hook is restored
		tc := e.dep.Timelines
		tc.fmu.Lock()
		idle := len(tc.active) == 0
		tc.fmu.Unlock()
		if idle {
			break
		}
		time.Sleep(time.Millisecond)
	}
	e.flush()
	if got := e.disk.written.Load(); got != wrote {
		t.Errorf("entries written %d -> %d: a view of an older build was written", wrote, got)
	}
	if got := e.disk.skipped.Load(); got != skipped {
		t.Errorf("a view of an older build was handed to the writer (%d -> %d skipped)", skipped, got)
	}
}

// A content read that fails for a reason other than not-found or unavailable (here a row the
// reader cannot scan) makes a result that is served and kept nowhere: not in memory, not on disk.
func TestDiskCache_ContentErrorIsCachedNowhere(t *testing.T) {
	e := newDiskEnv(t)
	e.call("a", 1, 0, 0, "user:fix the retry")
	copyOf := bytes.Repeat([]byte("line\n"), store.InlineMax) // over InlineMax: a blob file
	id := store.SessionIdent{Principal: core.PrincipalLocal, Client: "cc", Key: "k"}
	cp := store.Checkpoint{Copies: [][]byte{copyOf}, Effects: []core.Effect{{Kind: core.EffectFileChange,
		At: baseTime.Add(2 * time.Second), Principal: id.Principal, Client: id.Client, SessionKey: id.Key, Root: "/w",
		Path: "/w/a.txt", Change: core.FileCreated, Size: int64(len(copyOf)), Mode: "git", AfterHash: store.ContentHash(copyOf)}},
		Upserts: []store.SnapshotRow{{Root: "/w"}}}
	if err := e.s.SaveCheckpoint(t.Context(), id, cp); err != nil {
		t.Fatal(err)
	}
	h := store.ContentHash(copyOf)
	db, err := sql.Open("sqlite", "file:"+filepath.Join(e.dir, store.DBName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec("UPDATE content SET size = 'not-a-number' WHERE hash = ?", h)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("corrupted %d content rows, want 1", n)
	}
	_ = db.Close()

	e.mustGet("/api/sessions/" + sid() + "/view") // served
	e.mustGet("/api/sessions/" + sid())
	e.flush()
	if len(e.files("v")) != 0 || len(e.files("m")) != 0 {
		t.Errorf("a degraded result was written: %v %v", e.files("m"), e.files("v"))
	}
	if _, ok := e.dep.Views.peek(id); ok {
		t.Error("a degraded view is in the in-memory view cache")
	}
	// 043 review P3: a degraded memo may sit in memory so the list serves its facts, but
	// it is never an answer (lookup misses it) and is never a good memo
	if pe, ok := e.dep.Digests.peek(id); ok {
		if !pe.memo.degraded {
			t.Error("a good memo is in the in-memory digest cache for a degraded session")
		}
		if _, ok := e.dep.Digests.lookup(id, pe.ver); ok {
			t.Error("a degraded memo answers a lookup")
		}
	}
}

// A blob that is gone is not a failed read (spec 045 D1): the view and the list are served
// with a placeholder for the block, and the result is not marked degraded.
func TestDiskCache_UnavailableBlobIsNotDegraded(t *testing.T) {
	e := newDiskEnv(t)
	e.call("a", 1, 0, 0, "user:fix the retry")
	copyOf := bytes.Repeat([]byte("line\n"), store.InlineMax) // over InlineMax: a blob file
	id := store.SessionIdent{Principal: core.PrincipalLocal, Client: "cc", Key: "k"}
	cp := store.Checkpoint{Copies: [][]byte{copyOf}, Effects: []core.Effect{{Kind: core.EffectFileChange,
		At: baseTime.Add(2 * time.Second), Principal: id.Principal, Client: id.Client, SessionKey: id.Key, Root: "/w",
		Path: "/w/a.txt", Change: core.FileCreated, Size: int64(len(copyOf)), Mode: "git", AfterHash: store.ContentHash(copyOf)}},
		Upserts: []store.SnapshotRow{{Root: "/w"}}}
	if err := e.s.SaveCheckpoint(t.Context(), id, cp); err != nil {
		t.Fatal(err)
	}
	h := store.ContentHash(copyOf)
	blob := filepath.Join(e.dir, "blobs", h[:2], h[2:])
	if err := os.Remove(blob); err != nil {
		t.Fatalf("the copy is not a blob file: %v", err)
	}

	e.mustGet("/api/sessions/" + sid() + "/view") // served
	e.mustGet("/api/sessions/" + sid())
	e.flush()
	if pe, ok := e.dep.Digests.peek(id); ok && pe.memo.degraded {
		t.Error("a missing blob degraded the digest memo")
	}
}

// sessionViewChecked reports a content failure other than not-found and ignores not-found.
func TestSessionView_DegradedOnlyForRealContentErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{{errors.New("i/o"), true}, {context.DeadlineExceeded, true}, {store.ErrNotFound, false}, {store.ErrContentUnavailable, false}, {nil, false}} {
		r := fakeViewReader{err: tc.err}
		_, degraded, err := viewDeps{}.sessionViewChecked(r, sessIdent{core.PrincipalLocal, "cc", "k"}, builtSession{}, nil, false, "")
		if err != nil || degraded != tc.want {
			t.Errorf("content error %v: degraded = %v, %v, want %v", tc.err, degraded, err, tc.want)
		}
	}
}

type fakeViewReader struct{ err error }

func (f fakeViewReader) SessionEffects(store.SessionIdent) ([]store.EffectRow, error) {
	return []store.EffectRow{{Kind: string(core.EffectFileChange), Change: string(core.FileCreated), Path: "/w/a",
		AfterHash: strings.Repeat("a", 64), At: 1}}, nil
}
func (f fakeViewReader) Content(string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []byte("x\n"), nil
}
func (f fakeViewReader) UnassignedConnsBetween(int64, int64, int) ([]store.EffectRow, error) {
	return nil, nil
}

// A store older than the cache (a restored copy) deletes the build folder when the cache
// opens; a store at or ahead of it keeps it.
func TestDiskCache_StoreGoingBackDeletesTheBuild(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.mustGet("/api/sessions/" + sid())
	e.mustGet("/api/sessions/" + sid() + "/view")
	e.flush()
	root := e.disk.root
	hw, ok := e.disk.readHighWater()
	if !ok || hw.ExchangeRow == 0 {
		t.Fatalf("high-water = %+v %v after writes", hw, ok)
	}

	e.restart("v1") // the store is where it was: the entries stay
	if len(e.files("m")) == 0 {
		t.Fatal("a healthy reopen deleted the entries")
	}
	for name, bump := range map[string]func(*highWater){
		"exchange": func(h *highWater) { h.ExchangeRow += 1000 },
		"hook":     func(h *highWater) { h.HookID += 1000 },
		"effect":   func(h *highWater) { h.EffectID += 1000 },
	} {
		t.Run(name, func(t *testing.T) {
			hw2 := hw
			bump(&hw2)
			b, _ := json.Marshal(hw2)
			if err := os.WriteFile(filepath.Join(root, diskHighWat), b, 0o600); err != nil {
				t.Fatal(err)
			}
			e.restart("v1")
			if n := len(e.files("m")); n != 0 {
				t.Errorf("%d memo files survived a store that went back", n)
			}
			if e.logs.FilterMessageSnippet("store is behind").Len() != 1 {
				t.Errorf("no single warning: %v", e.logs.All())
			}
			e.mustGet("/api/sessions/" + sid())
			e.flush()
			if e.computes() == 0 || len(e.files("m")) == 0 {
				t.Error("the session was not rebuilt and rewritten")
			}
		})
	}
	// no high-water file: the folder cannot be checked and goes
	if err := os.Remove(filepath.Join(root, diskHighWat)); err != nil {
		t.Fatal(err)
	}
	e.restart("v1")
	if len(e.files("m")) != 0 {
		t.Error("entries without a high-water file survived")
	}
}

// joinedCount is how many callers have joined an in-flight run.
func (c *DigestCache) joinedCount() int {
	c.fmu.Lock()
	defer c.fmu.Unlock()
	return c.joined
}

// A run that captured with the cache never opened can push the store's maxima back above
// the mark after a restore, and SQLite then reuses the rowids and ids the restore lost. The
// mark also names the row at each maximum, so a different row there drops the folder (P3).
func TestHighWater_RowIdentityMismatchDeletesTheBuild(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.effect("a")
	hookSeq++
	rec := &store.HookRecord{PrincipalID: core.PrincipalLocal, Client: "cc", Seq: hookSeq, ReceivedAt: baseTime,
		Payload: []byte(`{"n":1}`), Event: core.HarnessEvent{Phase: core.PhasePostTool, ToolCallID: "t1", ToolName: "bash", SessionKey: "k"}}
	if err := e.s.SaveHook(t.Context(), rec); err != nil {
		t.Fatal(err)
	}
	fill := func() highWater {
		t.Helper()
		e.mustGet("/api/sessions/" + sid())
		e.mustGet("/api/sessions/" + sid() + "/view")
		e.flush()
		hw, ok := e.disk.readHighWater()
		if !ok || hw.ExchangeRow == 0 || hw.HookID == 0 || hw.EffectID == 0 ||
			hw.ExchangeReqID == "" || hw.HookAt == 0 || hw.EffectAt == 0 {
			t.Fatalf("high-water = %+v %v, want all three ids and identities", hw, ok)
		}
		if len(e.files("m")) == 0 {
			t.Fatal("no memo on disk")
		}
		return hw
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(e.dir, store.DBName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// (c) an unchanged store keeps the folder and says nothing
	fill()
	e.restart("v1")
	if len(e.files("m")) == 0 {
		t.Error("an unchanged store deleted the entries")
	}
	if n := e.logs.FilterLevelExact(zap.WarnLevel).Len(); n != 0 {
		t.Errorf("%d warnings on an unchanged store: %v", n, e.logs.All())
	}

	// (a), (b) a different row at a recorded maximum, the maximum itself unchanged
	for name, stmt := range map[string]string{
		"exchange": `UPDATE exchanges SET request_id = 'other-history' WHERE rowid = ?`,
		"hook":     `UPDATE hook_events SET received_at = received_at + 1 WHERE id = ?`,
		"effect":   `UPDATE effects SET at = at + 1 WHERE id = ?`,
	} {
		t.Run(name, func(t *testing.T) {
			hw := fill()
			id := map[string]int64{"exchange": hw.ExchangeRow, "hook": hw.HookID, "effect": hw.EffectID}[name]
			if _, err := db.Exec(stmt, id); err != nil {
				t.Fatal(err)
			}
			cur, err := e.d.Reader.WithContext(t.Context()).Maxima()
			if err != nil || cur.ExchangeRow < hw.ExchangeRow || cur.HookID < hw.HookID || cur.EffectID < hw.EffectID {
				t.Fatalf("setup: maxima %+v %v are not at or above the mark %+v", cur, err, hw)
			}
			e.restart("v1")
			if n := len(e.files("m")) + len(e.files("v")); n != 0 {
				t.Errorf("%d entries survived a different row at the %s maximum", n, name)
			}
			if e.logs.FilterMessageSnippet("store is behind").Len() != 1 {
				t.Errorf("no single warning: %v", e.logs.All())
			}
		})
	}

	// (d) a file from before identities were recorded cannot be checked
	hw := fill()
	old, _ := json.Marshal(map[string]int64{"exchange_row": hw.ExchangeRow, "hook_id": hw.HookID, "effect_id": hw.EffectID})
	if err := os.WriteFile(filepath.Join(e.disk.root, diskHighWat), old, 0o600); err != nil {
		t.Fatal(err)
	}
	e.restart("v1")
	if n := len(e.files("m")) + len(e.files("v")); n != 0 {
		t.Errorf("%d entries survived an old-format high-water file", n)
	}
	if e.logs.FilterLevelExact(zap.WarnLevel).Len() != 1 {
		t.Errorf("no single warning: %v", e.logs.All())
	}
}

// A folder that was opened and never written to is no reason for a warning at the next open.
func TestDiskCache_EmptyFolderReopensQuietly(t *testing.T) {
	e := newDiskEnv(t)
	e.restart("v1")
	if n := e.logs.FilterLevelExact(zap.WarnLevel).Len(); n != 0 {
		t.Errorf("%d warnings: %v", n, e.logs.All())
	}
}

// A view over the size limit is not written; the oldest view files go when the total is over.
func TestDiskCache_SizeLimits(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	realFile, realTotal := maxViewBytes, maxViewTotal
	t.Cleanup(func() { maxViewBytes, maxViewTotal = realFile, realTotal })

	maxViewBytes = 10
	e.mustGet("/api/sessions/" + sid() + "/view")
	e.flush()
	if len(e.files("v")) != 0 {
		t.Error("a view over the per-file limit was written")
	}

	maxViewBytes = realFile
	e.restart("v1") // the view built above is still in memory, which writes nothing
	paths := map[string]time.Time{}
	for i, k := range []string{"k", "k2", "k3"} {
		id := sessionID(store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: k})
		e.mustGet("/api/sessions/" + id + "/view")
		e.flush()
		mod := time.Now().Add(time.Duration(i-10) * time.Minute)
		if err := os.Chtimes(e.fileOf("v", k), mod, mod); err != nil {
			t.Fatal(err)
		}
		paths[k] = mod
	}
	if len(e.files("v")) != 3 {
		t.Fatalf("view files = %d, want 3", len(e.files("v")))
	}
	info, _ := os.Stat(e.fileOf("v", "k3"))
	maxViewTotal = info.Size()*2 + info.Size()/2 // room for two files
	e.disk.Trim(t.Context())
	left := e.files("v")
	if len(left) != 2 {
		t.Fatalf("after the trim %d view files, want 2", len(left))
	}
	if _, err := os.Stat(e.fileOf("v", "k")); err == nil {
		t.Error("the oldest view file stayed")
	}
	for _, k := range []string{"k2", "k3"} {
		if _, err := os.Stat(e.fileOf("v", k)); err != nil {
			t.Errorf("a newer view file went: %v", err)
		}
	}
}

// The writer trims by itself every trimEvery view writes.
func TestDiskCache_TrimsEveryNViewWrites(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	realEvery, realTotal := trimEvery, maxViewTotal
	t.Cleanup(func() { trimEvery, maxViewTotal = realEvery, realTotal })
	trimEvery, maxViewTotal = 3, 1
	for _, k := range []string{"k", "k2", "k3"} {
		e.mustGet("/api/sessions/" + sessionID(store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: k}) + "/view")
	}
	e.flush()
	if n := len(e.files("v")); n != 0 {
		t.Errorf("%d view files after the third write with a 1-byte cap", n)
	}
}

// The clean-up removes temp files that sat for 10 minutes and keeps newer ones.
func TestDiskCache_CleanupTempFiles(t *testing.T) {
	e := newDiskEnv(t)
	oldTmp := filepath.Join(e.disk.root, "v", diskTmp+"old")
	newTmp := filepath.Join(e.disk.root, "v", diskTmp+"new")
	for _, p := range []string{oldTmp, newTmp} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	long := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldTmp, long, long); err != nil {
		t.Fatal(err)
	}
	e.disk.Cleanup()
	if _, err := os.Stat(oldTmp); err == nil {
		t.Error("an old temp file stayed")
	}
	if _, err := os.Stat(newTmp); err != nil {
		t.Error("a recent temp file was deleted")
	}
}

// Folders are 0700 and files 0600 (AC2).
func TestDiskCache_Permissions(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.mustGet("/api/sessions/" + sid())
	e.mustGet("/api/sessions/" + sid() + "/view")
	e.flush()
	err := filepath.Walk(filepath.Join(e.dir, diskDirName), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s is %v, want %v", p, info.Mode().Perm(), want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A full queue drops a request's write instead of blocking it.
func TestDiskCache_FullQueueDropsRequestWrites(t *testing.T) {
	c := &DiskCache{q: make(chan diskReq, 1)}
	id := store.SessionIdent{Principal: "p", Client: "c", Key: "k"}
	done := make(chan struct{})
	go func() {
		for range 5 {
			c.PutMemo(id, store.Version{}, store.EffectVersion{}, digestMemo{})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("PutMemo blocked on a full queue")
	}
	if c.skipped.Load() != 4 || len(c.q) != 1 {
		t.Errorf("skipped %d, queued %d, want 4 and 1", c.skipped.Load(), len(c.q))
	}
	// the pass waits for room, and stops waiting when asked to
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	c.ctx = t.Context()
	c.q = make(chan diskReq, 4)
	for range 3 {
		c.q <- diskReq{}
	}
	c.WaitRoom(ctx)
	if ctx.Err() == nil {
		t.Error("WaitRoom returned with the queue over half full")
	}
}

// memo DTO: decode(encode(m)) equals m, for memos of seeded sessions and for one with
// every field set, cost totals included.
// unassigned stores a connection that belongs to no session, after the session's effects.
func (e *diskEnv) unassigned(host string) {
	e.t.Helper()
	c := core.Effect{Kind: core.EffectNetConn, At: baseTime.Add(1500 * time.Millisecond), Principal: core.PrincipalLocal,
		Client: "cc", Host: host, Port: 443, Via: "connect", EndedAt: baseTime.Add(2 * time.Second), Outcome: core.NetOK}
	if err := e.s.SaveEffects(e.t.Context(), []core.Effect{c}, nil); err != nil {
		e.t.Fatal(err)
	}
}

// A view's header rests on unassigned_max, the global effect maximum, which is above the
// session's own effect maximum when unassigned connections follow it. The high-water mark
// covers it, so an older backup that still has the session's effects is caught (P2-3).
func TestHighWater_ViewCoversUnassignedMax(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.effect("a")
	e.unassigned("one.example.com")
	e.unassigned("two.example.com")
	e.mustGet("/api/sessions/" + sid() + "/view")
	e.flush()
	if len(e.files("v")) == 0 {
		t.Fatal("no view was written")
	}
	c := e.dep.Reader.WithContext(t.Context())
	un, err := c.MaxEffectID()
	if err != nil {
		t.Fatal(err)
	}
	id := store.SessionIdent{Principal: core.PrincipalLocal, Client: "cc", Key: "k"}
	effs, _ := c.EffectVersions([]store.SessionIdent{id})
	if effs[id].MaxID >= un {
		t.Fatalf("setup: session effect max %d is not below the global %d", effs[id].MaxID, un)
	}
	if hw, _ := e.disk.readHighWater(); hw.EffectID < un {
		t.Errorf("high-water effect id = %d, want at least unassigned_max %d", hw.EffectID, un)
	}

	// restore a backup that has the session's effects and not the later connections
	db, err := sql.Open("sqlite", "file:"+filepath.Join(e.dir, store.DBName)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`DELETE FROM effects WHERE id > ?`, effs[id].MaxID); err != nil {
		t.Fatal(err)
	}
	e.restart("v1")
	if n := len(e.files("v")); n != 0 {
		t.Errorf("%d view files survived a store that went back", n)
	}
	if e.logs.FilterMessageSnippet("store is behind").Len() != 1 {
		t.Errorf("no single warning: %v", e.logs.All())
	}
}

// The mark is raised before the entry is written (AC8): an entry whose write fails leaves
// the mark covering it.
func TestHighWater_RaisedBeforeTheEntryIsWritten(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.effect("a")
	e.unassigned("one.example.com")
	// a regular file where the view folder goes: every view write fails
	vdir := filepath.Join(e.disk.root, diskViewDir)
	if err := os.RemoveAll(vdir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vdir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e.mustGet("/api/sessions/" + sid() + "/view")
	e.flush()
	if e.disk.written.Load() != 0 {
		t.Fatalf("written = %d, the entry write was meant to fail", e.disk.written.Load())
	}
	un, err := e.dep.Reader.WithContext(t.Context()).MaxEffectID()
	if err != nil {
		t.Fatal(err)
	}
	hw, ok := e.disk.readHighWater()
	if !ok || hw.ExchangeRow == 0 || hw.EffectID < un {
		t.Errorf("high-water = %+v %v after a failed entry write, want it covering effect id %d", hw, ok, un)
	}
}

// An effect that lands after the memo's data was read must not let the memo be written:
// its key was made from the effect version read before the data, which is now behind (AC5).
func TestDiskCache_EffectAfterDataReadMemoNotWritten(t *testing.T) {
	// Registered before newDiskEnv, so it runs after the env's shutdown cleanups: no
	// background compute can still read computeMemoFn while it is written back.
	real := computeMemoFn
	t.Cleanup(func() { computeMemoFn = real })
	e := newDiskEnv(t)
	e.seed()
	var once atomic.Bool
	computeMemoFn = func(src memoSource, vd viewDeps, row store.SessionRow) (digestMemo, error) {
		m, err := real(src, vd, row)
		if row.SessionKey == "k" && once.CompareAndSwap(false, true) {
			e.effect("late") // lands after the data was read
		}
		return m, err
	}

	e.mustGet("/api/sessions/" + sid())
	e.flush()
	if !once.Load() {
		t.Fatal("the compute hook never ran")
	}
	if _, err := os.Stat(e.fileOf("m", "k")); err == nil {
		t.Error("a memo read before an effect landed was written under the older key")
	}
	if e.disk.skipped.Load() == 0 {
		t.Error("the stale write was not counted as skipped")
	}
	e.restart("v1")
	before := e.computes()
	e.mustGet("/api/sessions/" + sid())
	e.flush()
	if e.computes() == before {
		t.Error("the later read did not rebuild")
	}
	if _, err := os.Stat(e.fileOf("m", "k")); err != nil {
		t.Errorf("the rebuilt memo was not written: %v", err)
	}
}

// A request that joins a compute already in flight still gets the disk hit: the lookup is
// inside the compute (D24), so the joined compute finds the entry and nothing is computed.
func TestDiskCache_JoiningRequestGetsTheDiskHit(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.mustGet("/api/sessions/" + sid())
	e.flush()
	if len(e.files("m")) == 0 {
		t.Fatal("no memo on disk")
	}
	e.restart("v1")

	row := store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "k"}
	id := sessionIdent(row)
	vers, err := e.dep.Reader.WithContext(t.Context()).SessionVersions([]store.SessionIdent{id})
	if err != nil {
		t.Fatal(err)
	}
	started, gate := make(chan struct{}), make(chan struct{})
	held := func(ctx context.Context) (digestMemo, error) {
		close(started)
		<-gate // before the disk lookup
		return e.dep.digestCompute(row, vers[id])(ctx)
	}
	never := func(context.Context) (digestMemo, error) {
		t.Error("a joining request ran its own compute")
		return digestMemo{}, errors.New("unexpected")
	}
	type res struct {
		h   digestHit
		err error
	}
	out := make(chan res, 2)
	go func() { h, err := e.dep.Digests.get(t.Context(), id, vers[id], held); out <- res{h, err} }()
	<-started
	go func() { h, err := e.dep.Digests.get(t.Context(), id, vers[id], never); out <- res{h, err} }()
	deadline := time.Now().Add(5 * time.Second)
	for e.dep.Digests.joinedCount() == 0 { // the second request has joined the run
		if time.Now().After(deadline) {
			t.Fatal("the second request never joined the in-flight run")
		}
		time.Sleep(time.Millisecond)
	}
	close(gate)
	for range 2 {
		r := <-out
		if r.err != nil {
			t.Fatal(r.err)
		}
		if !r.h.memo.fromDisk {
			t.Errorf("a caller got a memo not read from disk: %+v", r.h)
		}
	}
	if n := e.computes(); n != 0 {
		t.Errorf("computes = %d, want 0 (served from disk)", n)
	}
}

func TestMemoDTO_RoundTrip(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.call("b", 4, 2, 100, "user:fix the retry", "assistant:done")
	for _, k := range []string{"k", "k2", "k3"} {
		row, err := e.dep.Reader.WithContext(t.Context()).SessionByKey(core.PrincipalLocal, "cc", k)
		if err != nil {
			t.Fatal(err)
		}
		m, err := computeMemo(e.dep.Reader.WithContext(t.Context()), e.dep.viewDeps(), row)
		if err != nil {
			t.Fatal(err)
		}
		roundTrip(t, k, m)
	}
	one := int64(5)
	var g timeline.Group
	g.Cost.Add(cost.Call{Status: cost.StatusPriced, Amount: 12, Source: "x", Estimated: true})
	g.Cost.Add(cost.Call{Status: cost.StatusPriced, Amount: 3, Source: "y"})
	g.Input = timeline.Sum{Value: &one, Unreported: 2}
	var h timeline.Group
	h.Cost.Add(cost.Call{Status: cost.StatusPriced, Amount: 1, Source: "x"})
	roundTrip(t, "full", digestMemo{
		digest: timeline.Digest{Title: "t", ToolStates: map[string]int{"a": 1, "b": 0}, MainErrors: 1, SideErrors: 2, HasCalls: true, HasHooks: true},
		stats: timeline.Stats{ModelCalls: 3, ToolCalls: 4, Errors: 3, Usage: timeline.Summary{Calls: 3, Total: g,
			Models: []timeline.NamedGroup{{Model: "m1", Group: h}, {Model: "m2"}}}},
		row: rowFacts{title: "T", folder: "f", folderFull: "/f", prompts: 1, mainCalls: 2, sideCalls: 3, commands: 4, clockMS: 5,
			look: 6, lookKinds: []timeline.LookKind{{Kind: "k", Count: 1, Words: "w"}}},
	})
}

func roundTrip(t *testing.T, name string, m digestMemo) {
	t.Helper()
	b, err := encodeMemo(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeMemo(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, m) {
		t.Errorf("%s: memo changed in the round trip:\n%+v\n%+v", name, got, m)
	}
}

// view JSON: json(decode(json(v))) equals json(v) for the views of seeded sessions, with
// effects, hooks and an unassigned connection in them.
func TestViewJSON_RoundTrip(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	e.callWith("b", 4, false, []toolSpec{{"Bash", `{"command":"npm test"}`}}, "user:fix the retry", "assistant:Fixed.")
	e.effect("a.txt")
	c := core.Effect{Kind: core.EffectNetConn, At: baseTime.Add(1500 * time.Millisecond), Principal: core.PrincipalLocal,
		Client: "cc", Host: "late.example.com", Port: 443, Via: "connect", EndedAt: baseTime.Add(2 * time.Second), Outcome: core.NetOK}
	if err := e.s.SaveEffects(t.Context(), []core.Effect{c}, nil); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"k", "k2", "k3"} {
		id := sessionID(store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: k})
		v, err := e.dep.cachedView(t.Context(), sessIdent{core.PrincipalLocal, "cc", k}, true)
		if err != nil {
			t.Fatal(err)
		}
		first, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var back timeline.View
		if err := json.Unmarshal(first, &back); err != nil {
			t.Fatal(err)
		}
		second, _ := json.Marshal(back)
		if !bytes.Equal(first, second) {
			t.Errorf("%s: the view changed in a JSON round trip:\n%s\n%s", id, first, second)
		}
	}
}

func TestZoneFingerprint(t *testing.T) {
	load := func(name string) *time.Location {
		l, err := time.LoadLocation(name)
		if err != nil {
			t.Skipf("no tz database: %v", err)
		}
		return l
	}
	kolkata, tokyo, ny := zoneFingerprint(load("Asia/Kolkata")), zoneFingerprint(load("Asia/Tokyo")), zoneFingerprint(load("America/New_York"))
	if kolkata == tokyo || kolkata == ny || tokyo == ny {
		t.Error("different zones share a fingerprint")
	}
	if zoneFingerprint(load("Asia/Kolkata")) != kolkata {
		t.Error("one zone gave two fingerprints")
	}
	// The bug case: the same name ("Local") with different rules.
	a, b := time.FixedZone("Local", 19800), time.FixedZone("Local", 32400)
	if a.String() != b.String() {
		t.Fatal("test setup: the names must match")
	}
	if zoneFingerprint(a) == zoneFingerprint(b) {
		t.Error("same-named zones with different offsets share a fingerprint")
	}
}

func TestBuildIdentity_HomeAndZone(t *testing.T) {
	exe := [sha256.Size]byte{1}
	z := time.FixedZone("Local", 19800)
	base := buildIdentity("v", exe, 0, "/home/a", z, "", "aa")
	if buildIdentity("v", exe, 0, "/home/a", time.FixedZone("Local", 19800), "", "aa") != base {
		t.Error("the same inputs gave two builds")
	}
	if buildIdentity("v", exe, 0, "/home/b", z, "", "aa") == base {
		t.Error("home does not change the build")
	}
	if buildIdentity("v", exe, 0, "/home/a", time.FixedZone("Local", 32400), "", "aa") == base {
		t.Error("zone does not change the build")
	}
	if buildIdentity("v", exe, 0, "/home/a", z, "", "bb") == base {
		t.Error("a different /etc/localtime hash does not change the build")
	}
	if buildIdentity("v", exe, 0, "/home/a", z, "Asia/Beirut", "aa") == base {
		t.Error("TZ does not change the build")
	}
}
