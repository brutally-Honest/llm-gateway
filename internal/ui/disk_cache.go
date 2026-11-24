package ui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/observability"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// The on-disk view cache (spec 030): per session, its memo and its view, one file each
// under <capture.dir>/view-cache/<build>/{m,v}/. It is disposable: every entry can be
// rebuilt from the store, and an entry is used only when its whole key equals the current
// one, so nothing here is ever served stale.

const (
	// diskFormat is the entry format; a change makes every old entry a miss.
	diskFormat = 1
	// diskDirName is the cache's folder under capture.dir.
	diskDirName = "view-cache"
	// diskQueueSize bounds the writer's queue.
	diskQueueSize = 256

	diskMemoDir = "m"
	diskViewDir = "v"
	diskTmp     = ".tmp-"
	diskHighWat = "high-water"

	diskFileMode = 0o600
	diskDirMode  = 0o700
)

// The limits are vars so tests can lower them (spec 030 AC9, D22).
var (
	// maxViewBytes is the largest view body written.
	maxViewBytes = 32 << 20
	// maxViewTotal is the most the view files of a build may hold.
	maxViewTotal int64 = 1 << 30
	// trimEvery is how many view writes pass between size checks.
	trimEvery = 100
	// idleAfter is how long a build folder or a temp file must sit untouched before
	// the clean-up deletes it.
	idleAfter = 10 * time.Minute
	// damagedWarnEvery is the least time between two warnings about damaged entries.
	damagedWarnEvery = time.Minute
)

// hashExe returns the sha256 of the running executable; tests replace it. The result is
// kept for the process: the file /proc/self/exe names does not change while it runs.
var hashExe = sync.OnceValues(hashExecutable)

func hashExecutable() ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	f, err := os.Open("/proc/self/exe")
	if err != nil {
		path, perr := os.Executable()
		if perr != nil {
			return sum, fmt.Errorf("ui view cache: executable: %w", perr)
		}
		if f, err = os.Open(path); err != nil {
			return sum, fmt.Errorf("ui view cache: executable: %w", err)
		}
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, fmt.Errorf("ui view cache: hash executable: %w", err)
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// diskHeader is line 1 of an entry. Sum is the sha256 of the body (every byte after line
// 1). The view-only fields are zero for a memo.
type diskHeader struct {
	Format int       `json:"format"`
	Build  string    `json:"build"`
	Ident  [3]string `json:"ident"`
	Key    string    `json:"key"`
	Sum    string    `json:"sum"`

	Last          int64 `json:"last,omitempty"`
	EndOnly       bool  `json:"end_only,omitempty"`
	Active        bool  `json:"active,omitempty"`
	From          int64 `json:"from,omitempty"`
	To            int64 `json:"to,omitempty"`
	UnassignedMax int64 `json:"unassigned_max,omitempty"`
}

// highWater is the high-water file: the largest ids any written entry was built on.
type highWater struct {
	ExchangeRow int64 `json:"exchange_row"`
	HookID      int64 `json:"hook_id"`
	EffectID    int64 `json:"effect_id"`
}

type diskOp byte

const (
	opMemo diskOp = iota
	opView
	opDelete
	opTrim
	opBarrier
)

// diskReq is one request to the writer.
type diskReq struct {
	op   diskOp
	id   store.SessionIdent
	key  string
	ver  store.Version
	eff  store.EffectVersion
	memo digestMemo
	view *viewEntry
	// path and sum name a damaged file to delete, if its bytes still hash to sum; an
	// empty sum is a file that could not be read.
	path, sum string
	done      chan struct{}
}

// DiskCache is the on-disk view cache. A nil *DiskCache is no disk cache: every method
// is a miss or a no-op, so callers need no check.
type DiskCache struct {
	build string
	root  string
	ctx   context.Context
	r     *store.Reader
	log   *zap.Logger
	m     *observability.Metrics
	q     chan diskReq
	now   func() time.Time

	// hw and viewWrites belong to the writer goroutine.
	hw         highWater
	viewWrites int

	warnMu       sync.Mutex
	lastWarn     time.Time
	damagedSince int

	damaged atomic.Int64
	skipped atomic.Int64
	written atomic.Int64
}

// OpenDiskCache opens the cache of this build under dir (capture.dir) and starts its
// writer, which stops with ctx. The build identity is the cache format, version, a hash of
// the running executable, the attribution skew, the home folder and the local time zone's
// name and rules. Before anything is read, the store's largest ids are compared with the build
// folder's high-water file: if the store is behind it, the store was replaced by an older
// copy and the folder is deleted (AC8). An error means the caller runs without the cache.
func OpenDiskCache(ctx context.Context, dir, version string, skew time.Duration, r *store.Reader, log *zap.Logger, m *observability.Metrics) (*DiskCache, error) {
	if log == nil {
		log = zap.NewNop()
	}
	exe, err := hashExe()
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	build := buildIdentity(version, exe, skew, home, time.Local)
	base := filepath.Join(dir, diskDirName)
	c := &DiskCache{
		build: build, root: filepath.Join(base, build), ctx: ctx, r: r, log: log, m: m,
		q: make(chan diskReq, diskQueueSize), now: time.Now,
	}
	if r != nil {
		qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := c.checkHighWater(qctx); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Join(c.root, diskMemoDir), diskDirMode); err != nil {
		return nil, fmt.Errorf("ui view cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(c.root, diskViewDir), diskDirMode); err != nil {
		return nil, fmt.Errorf("ui view cache: %w", err)
	}
	logging.Go(log, "ui_cache_writer", func() error { c.writer(); return nil })
	return c, nil
}

// buildIdentity hashes the inputs a cached view depends on into the 16-character build id.
func buildIdentity(version string, exe [sha256.Size]byte, skew time.Duration, home string, zone *time.Location) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d\x00%s\x00%x\x00%d\x00%s\x00%s\x00%s", diskFormat, version, exe, int64(skew), home, zone.String(), zoneFingerprint(zone))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// zoneFingerprint describes a zone by its rules, not its name: the abbreviation and offset
// at midnight UTC on Jan 1 and Jul 1 of 1990-2040. With TZ unset Go names the system zone
// "Local" whatever /etc/localtime holds, so the name alone cannot tell two zones apart.
func zoneFingerprint(loc *time.Location) string {
	var b strings.Builder
	for y := 1990; y <= 2040; y++ {
		for _, m := range []time.Month{time.January, time.July} {
			b.WriteString(time.Date(y, m, 1, 0, 0, 0, 0, time.UTC).In(loc).Format("MST -0700"))
			b.WriteByte(';')
		}
	}
	return b.String()
}

// checkHighWater deletes the build folder when the store's largest ids are below the ones
// any entry in it was built on. A folder with no readable high-water file cannot be
// checked and is deleted too.
func (c *DiskCache) checkHighWater(ctx context.Context) error {
	if _, err := os.Stat(c.root); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	cur, err := c.r.WithContext(ctx).Maxima()
	if err != nil {
		return err
	}
	hw, ok := c.readHighWater()
	switch {
	case !ok && !c.hasEntries():
		return nil // opened and closed without a write: nothing to check
	case !ok:
		c.log.Warn("ui view cache: high-water file missing or unreadable; the cache of this build was deleted")
	case cur.ExchangeRow < hw.ExchangeRow || cur.HookID < hw.HookID || cur.EffectID < hw.EffectID:
		c.log.Warn("ui view cache: the store is behind the cache (an older copy was restored?); the cache of this build was deleted")
	default:
		c.hw = hw
		return nil
	}
	if err := os.RemoveAll(c.root); err != nil {
		return fmt.Errorf("ui view cache: %w", err)
	}
	return nil
}

// hasEntries reports whether the build folder holds any entry file.
func (c *DiskCache) hasEntries() bool {
	for _, kind := range []string{diskMemoDir, diskViewDir} {
		ents, _ := os.ReadDir(filepath.Join(c.root, kind))
		for _, e := range ents {
			if !strings.HasPrefix(e.Name(), diskTmp) {
				return true
			}
		}
	}
	return false
}

func (c *DiskCache) readHighWater() (highWater, bool) {
	b, err := os.ReadFile(filepath.Join(c.root, diskHighWat))
	var hw highWater
	if err != nil || json.Unmarshal(b, &hw) != nil {
		return highWater{}, false
	}
	return hw, true
}

// Build is the build identity: the folder's name under view-cache/.
func (c *DiskCache) Build() string {
	if c == nil {
		return ""
	}
	return c.build
}

func effKey(e store.EffectVersion) string { return fmt.Sprintf("%d.%d", e.Count, e.MaxID) }

// viewKeyOf is the key of a view and a memo: the timeline build version and the effect
// version, the string buildView labels a view with.
func viewKeyOf(v store.Version, e store.EffectVersion) string { return buildKey(v) + effKey(e) }

func fileName(id store.SessionIdent) string {
	sum := sha256.Sum256([]byte(id.Principal + "\x00" + id.Client + "\x00" + id.Key))
	return hex.EncodeToString(sum[:])
}

func (c *DiskCache) path(kind string, id store.SessionIdent) string {
	return filepath.Join(c.root, kind, fileName(id))
}

func (c *DiskCache) read(kind, result string) {
	c.m.ViewCacheRead(kind, result)
}

type loadResult int

const (
	loadHit loadResult = iota
	loadMiss
	loadDamaged
)

// load reads and checks one entry. A missing file or a different key is a plain miss;
// anything unreadable, unparsable, mis-summed or naming another build, format or session
// is damaged: counted, warned about at most once a minute, and queued for deletion.
func (c *DiskCache) load(dir string, id store.SessionIdent, key string) (h diskHeader, body []byte, fileSum string, res loadResult) {
	path := c.path(dir, id)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return diskHeader{}, nil, "", loadMiss
	}
	if err != nil {
		c.damage(dir, path, "", err)
		return diskHeader{}, nil, "", loadDamaged
	}
	whole := sha256.Sum256(raw)
	fileSum = hex.EncodeToString(whole[:])
	bad := func(why string) (diskHeader, []byte, string, loadResult) {
		c.damage(dir, path, fileSum, errors.New(why))
		return diskHeader{}, nil, "", loadDamaged
	}
	nl := bytes.IndexByte(raw, '\n')
	if nl < 0 {
		return bad("no header line")
	}
	if err := json.Unmarshal(raw[:nl], &h); err != nil {
		return bad("header is not JSON")
	}
	body = raw[nl+1:]
	if h.Format != diskFormat {
		return bad("unknown format")
	}
	if h.Build != c.build || h.Ident != [3]string{id.Principal, id.Client, id.Key} {
		return bad("names another build or session")
	}
	if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != h.Sum {
		return bad("checksum differs")
	}
	if h.Key != key {
		return diskHeader{}, nil, "", loadMiss
	}
	return h, body, fileSum, loadHit
}

// damage counts a damaged entry, warns at most once a minute with the count since the
// last warning, and asks the writer to delete the file if it is still the bad one.
func (c *DiskCache) damage(dir, path, sum string, why error) {
	c.damaged.Add(1)
	kind := observability.CacheKindMemo
	if dir == diskViewDir {
		kind = observability.CacheKindView
	}
	c.read(kind, observability.CacheDamaged)
	c.warnMu.Lock()
	c.damagedSince++
	var n int
	if now := c.now(); c.lastWarn.IsZero() || now.Sub(c.lastWarn) >= damagedWarnEvery {
		n, c.damagedSince, c.lastWarn = c.damagedSince, 0, now
	}
	c.warnMu.Unlock()
	if n > 0 {
		c.log.Warn("ui view cache: damaged entries ignored and removed",
			zap.Int("count", n), zap.String("file", filepath.Base(path)), zap.NamedError("reason", why))
	}
	select {
	case c.q <- diskReq{op: opDelete, path: path, sum: sum}:
	default:
	}
}

// GetMemo returns the memo stored under key, which must equal the entry's key exactly.
func (c *DiskCache) GetMemo(id store.SessionIdent, key string) (digestMemo, bool) {
	if c == nil {
		return digestMemo{}, false
	}
	_, body, fileSum, res := c.load(diskMemoDir, id, key)
	switch res {
	case loadMiss:
		c.read(observability.CacheKindMemo, observability.CacheMiss)
		return digestMemo{}, false
	case loadDamaged:
		return digestMemo{}, false
	}
	m, err := decodeMemo(body)
	if err != nil {
		c.damage(diskMemoDir, c.path(diskMemoDir, id), fileSum, err)
		return digestMemo{}, false
	}
	c.read(observability.CacheKindMemo, observability.CacheHit)
	return m, true
}

// viewUsable reports whether a loaded view entry may be served now: its active flag at
// now is the one it was built with, and no unassigned connection stored since it was built
// falls in its range (D15).
func (c *DiskCache) viewUsable(ctx context.Context, h diskHeader, now time.Time) bool {
	if isActive(h.Last, h.EndOnly, now) != h.Active {
		return false
	}
	if c.r == nil {
		return h.From == 0
	}
	ok, err := unassignedQuiet(c.r.WithContext(ctx), h.From, h.To, h.UnassignedMax)
	return err == nil && ok
}

// unassignedQuiet reports that no connection with no session was stored after id max with
// a start in [from, to]; with from == 0 the view reads none and it is true.
func unassignedQuiet(c *store.CtxReader, from, to, max int64) (bool, error) {
	if from == 0 {
		return true, nil
	}
	later, err := c.UnassignedConnAfter(max, from, to)
	return !later, err
}

// GetView returns the view stored under key when its active flag at now is the one it
// was built with and no unassigned connection stored since falls in its range.
func (c *DiskCache) GetView(ctx context.Context, id store.SessionIdent, key string, now time.Time) (*viewEntry, bool) {
	if c == nil {
		return nil, false
	}
	h, body, fileSum, res := c.load(diskViewDir, id, key)
	switch res {
	case loadMiss:
		c.read(observability.CacheKindView, observability.CacheMiss)
		return nil, false
	case loadDamaged:
		return nil, false
	}
	if !c.viewUsable(ctx, h, now) {
		c.read(observability.CacheKindView, observability.CacheMiss)
		return nil, false
	}
	var v timeline.View
	if err := json.Unmarshal(body, &v); err != nil {
		c.damage(diskViewDir, c.path(diskViewDir, id), fileSum, err)
		return nil, false
	}
	c.read(observability.CacheKindView, observability.CacheHit)
	return &viewEntry{id: id, key: key, last: h.Last, active: h.Active, endOnly: h.EndOnly, view: v,
		from: h.From, to: h.To, unMax: h.UnassignedMax}, true
}

// HasView is GetView without decoding or counting a hit or miss: the background pass
// asks it whether a view needs building.
func (c *DiskCache) HasView(ctx context.Context, id store.SessionIdent, key string, now time.Time) bool {
	if c == nil {
		return false
	}
	h, _, _, res := c.load(diskViewDir, id, key)
	return res == loadHit && c.viewUsable(ctx, h, now)
}

// PutMemo hands a memo computed at store version ver and effect version eff to the
// writer. It never blocks: a full queue drops it.
func (c *DiskCache) PutMemo(id store.SessionIdent, ver store.Version, eff store.EffectVersion, m digestMemo) {
	if c == nil {
		return
	}
	c.enqueue(diskReq{op: opMemo, id: id, key: viewKeyOf(ver, eff), ver: ver, eff: eff, memo: m})
}

// PutView hands a built view, whose key was made from ver and eff, to the writer. It never
// blocks.
func (c *DiskCache) PutView(e *viewEntry, ver store.Version, eff store.EffectVersion) {
	if c == nil {
		return
	}
	c.enqueue(diskReq{op: opView, id: e.id, key: e.key, ver: ver, eff: eff, view: e})
}

func (c *DiskCache) enqueue(r diskReq) {
	select {
	case c.q <- r:
	default:
		c.skipped.Add(1)
	}
}

// WaitRoom returns when the queue is at most half full, or ctx ends: the background pass
// calls it between sessions, so it waits for room where a request drops its write.
func (c *DiskCache) WaitRoom(ctx context.Context) {
	if c == nil {
		return
	}
	for len(c.q) > cap(c.q)/2 {
		select {
		case <-ctx.Done():
			return
		case <-c.ctx.Done():
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// Trim deletes the oldest view files while the view files of the build total more than the
// cap, and returns when done.
func (c *DiskCache) Trim(ctx context.Context) { c.request(ctx, diskReq{op: opTrim}) }

// flush returns when every request queued before it is handled.
func (c *DiskCache) flush(ctx context.Context) { c.request(ctx, diskReq{op: opBarrier}) }

func (c *DiskCache) request(ctx context.Context, r diskReq) {
	if c == nil {
		return
	}
	r.done = make(chan struct{})
	select {
	case c.q <- r:
	case <-ctx.Done():
		return
	case <-c.ctx.Done():
		return
	}
	select {
	case <-r.done:
	case <-ctx.Done():
	case <-c.ctx.Done():
	}
}

// writer is the one goroutine that writes and deletes entries.
func (c *DiskCache) writer() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case r := <-c.q:
			c.handle(r)
			if r.done != nil {
				close(r.done)
			}
		}
	}
}

func (c *DiskCache) handle(r diskReq) {
	defer func() {
		if p := recover(); p != nil {
			logging.LogPanic(c.log, "ui view cache write panicked", p)
		}
	}()
	switch r.op {
	case opMemo, opView:
		if err := c.put(r); err != nil && c.ctx.Err() == nil {
			c.log.Debug("ui view cache: entry not written", zap.Error(err))
		}
	case opDelete:
		c.remove(r.path, r.sum)
	case opTrim:
		c.trim()
	}
}

// put writes one entry if the session's versions, read now, still equal its key (AC5).
func (c *DiskCache) put(r diskReq) error {
	if c.r != nil {
		ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
		defer cancel()
		rd := c.r.WithContext(ctx)
		ids := []store.SessionIdent{r.id}
		vers, err := rd.SessionVersions(ids)
		if err != nil {
			return err
		}
		effs, err := rd.EffectVersions(ids)
		if err != nil {
			return err
		}
		if viewKeyOf(vers[r.id], effs[r.id]) != r.key {
			c.skipped.Add(1)
			return nil // the session moved on; the next read rebuilds
		}
	}
	var (
		dir  = diskMemoDir
		hdr  = diskHeader{Format: diskFormat, Build: c.build, Ident: [3]string{r.id.Principal, r.id.Client, r.id.Key}, Key: r.key}
		body []byte
		err  error
	)
	if r.op == opView {
		dir = diskViewDir
		e := r.view
		hdr.Last, hdr.EndOnly, hdr.Active, hdr.From, hdr.To, hdr.UnassignedMax = e.last, e.endOnly, e.active, e.from, e.to, e.unMax
		body, err = json.Marshal(e.view)
		if err == nil && len(body) > maxViewBytes {
			return nil // too large to keep (AC9)
		}
	} else {
		body, err = encodeMemo(r.memo)
	}
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	hdr.Sum = hex.EncodeToString(sum[:])
	head, err := json.Marshal(hdr)
	if err != nil {
		return err
	}
	if err := c.raiseHighWater(r); err != nil {
		return err
	}
	if err := c.writeFile(filepath.Join(c.root, dir), fileName(r.id), head, body); err != nil {
		return err
	}
	c.written.Add(1)
	if r.op == opView {
		if c.viewWrites++; c.viewWrites%trimEvery == 0 {
			c.trim()
		}
	}
	return nil
}

// raiseHighWater makes the high-water file cover the ids r was built on, before the entry
// is written. It is rewritten also when the file is gone, for the next start's check.
func (c *DiskCache) raiseHighWater(r diskReq) error {
	next := c.hw
	next.ExchangeRow = max(next.ExchangeRow, r.ver.ExMaxRow)
	next.HookID = max(next.HookID, r.ver.HookMaxID)
	next.EffectID = max(next.EffectID, r.eff.MaxID)
	path := filepath.Join(c.root, diskHighWat)
	if _, err := os.Stat(path); err == nil && next == c.hw {
		return nil
	}
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := c.writeFile(c.root, diskHighWat, b, nil); err != nil {
		return err
	}
	c.hw = next
	return nil
}

// writeFile writes head, a newline and body to a temp file in dir, then renames it to
// name, so a reader sees the old file or the new one whole. The folder is created if it
// is gone. With a nil body the file is head alone.
func (c *DiskCache) writeFile(dir, name string, head, body []byte) error {
	if err := os.MkdirAll(dir, diskDirMode); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, diskTmp)
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(diskFileMode); err != nil {
		_ = f.Close()
		return err
	}
	w := append([]byte(nil), head...)
	if body != nil {
		w = append(append(w, '\n'), body...)
	}
	if _, err := f.Write(w); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		return err
	}
	ok = true
	return nil
}

// remove deletes a damaged file if it still is the one found bad: its bytes hash to sum
// (an empty sum is one that could not be read, deleted if it still cannot be). A fresh
// entry renamed in meanwhile has other bytes and stays.
func (c *DiskCache) remove(path, sum string) {
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return
	case err != nil:
		if sum != "" {
			return
		}
	default:
		got := sha256.Sum256(raw)
		if sum == "" || hex.EncodeToString(got[:]) != sum {
			return
		}
	}
	_ = os.Remove(path)
}

// trim deletes the oldest-written view files until the view files total at most the cap.
func (c *DiskCache) trim() {
	dir := filepath.Join(c.root, diskViewDir)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		size int64
		mod  time.Time
	}
	var files []file
	var total int64
	for _, e := range ents {
		if e.IsDir() || strings.HasPrefix(e.Name(), diskTmp) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{filepath.Join(dir, e.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= maxViewTotal {
		return
	}
	slices.SortFunc(files, func(a, b file) int { return a.mod.Compare(b.mod) })
	for _, f := range files {
		if total <= maxViewTotal {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}

// Cleanup deletes the other build folders, and temp files of this one, that have sat
// untouched for 10 minutes (D22). A folder another gateway is writing to stays.
func (c *DiskCache) Cleanup() {
	if c == nil {
		return
	}
	cut := c.now().Add(-idleAfter)
	base := filepath.Dir(c.root)
	if ents, err := os.ReadDir(base); err == nil {
		for _, e := range ents {
			p := filepath.Join(base, e.Name())
			if !e.IsDir() || e.Name() == c.build || newest(p).After(cut) {
				continue
			}
			if err := os.RemoveAll(p); err == nil {
				c.log.Info("ui view cache: removed an old build's entries", zap.String("build", e.Name()))
			}
		}
	}
	for _, kind := range []string{diskMemoDir, diskViewDir} {
		dir := filepath.Join(c.root, kind)
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !strings.HasPrefix(e.Name(), diskTmp) {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().Before(cut) {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
}

// newest is the latest modification time of a build folder, its two entry folders and its
// high-water file: any write into it moves one of them.
func newest(dir string) time.Time {
	var t time.Time
	for _, p := range []string{dir, filepath.Join(dir, diskMemoDir), filepath.Join(dir, diskViewDir), filepath.Join(dir, diskHighWat)} {
		if info, err := os.Stat(p); err == nil && info.ModTime().After(t) {
			t = info.ModTime()
		}
	}
	return t
}

// memoDTO is a memo as it is stored: the memo's own fields are unexported, so the file
// holds these exported twins. Seen is each cost total's Seen flag, the one thing JSON
// would lose: the stats' total first, then one per model.
type memoDTO struct {
	Digest timeline.Digest `json:"digest"`
	Stats  timeline.Stats  `json:"stats"`
	Seen   []bool          `json:"seen"`
	Row    rowFactsDTO     `json:"row"`
}

type rowFactsDTO struct {
	Title      string              `json:"title"`
	Folder     string              `json:"folder"`
	FolderFull string              `json:"folder_full"`
	Prompts    int                 `json:"prompts"`
	MainCalls  int                 `json:"main_calls"`
	SideCalls  int                 `json:"side_calls"`
	Commands   int                 `json:"commands"`
	ClockMS    int64               `json:"clock_ms"`
	Look       int                 `json:"look"`
	LookKinds  []timeline.LookKind `json:"look_kinds"`
}

func encodeMemo(m digestMemo) ([]byte, error) {
	seen := []bool{m.stats.Usage.Total.Cost.Seen()}
	for _, g := range m.stats.Usage.Models {
		seen = append(seen, g.Cost.Seen())
	}
	r := m.row
	return json.Marshal(memoDTO{Digest: m.digest, Stats: m.stats, Seen: seen, Row: rowFactsDTO{
		Title: r.title, Folder: r.folder, FolderFull: r.folderFull, Prompts: r.prompts, MainCalls: r.mainCalls,
		SideCalls: r.sideCalls, Commands: r.commands, ClockMS: r.clockMS, Look: r.look, LookKinds: r.lookKinds}})
}

func decodeMemo(b []byte) (digestMemo, error) {
	var d memoDTO
	if err := json.Unmarshal(b, &d); err != nil {
		return digestMemo{}, err
	}
	if len(d.Seen) != 1+len(d.Stats.Usage.Models) {
		return digestMemo{}, errors.New("memo: seen flags do not match the models")
	}
	d.Stats.Usage.Total.Cost.SetSeen(d.Seen[0])
	for i := range d.Stats.Usage.Models {
		d.Stats.Usage.Models[i].Cost.SetSeen(d.Seen[1+i])
	}
	r := d.Row
	return digestMemo{digest: d.Digest, stats: d.Stats, row: rowFacts{
		title: r.Title, folder: r.Folder, folderFull: r.FolderFull, prompts: r.Prompts, mainCalls: r.MainCalls,
		sideCalls: r.SideCalls, commands: r.Commands, clockMS: r.ClockMS, look: r.Look, lookKinds: r.LookKinds}}, nil
}
