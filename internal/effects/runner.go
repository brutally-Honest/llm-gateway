package effects

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/hooks"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

const (
	// evictAfter is how long a session's memory cache stays without a hook (D16).
	evictAfter = time.Hour
	// sweepEvery is how often the cache is evicted and old snapshots are swept (E16).
	sweepEvery = time.Hour
)

// CheckpointStore is what the runner reads and saves snapshots through.
type CheckpointStore interface {
	SaveCheckpoint(ctx context.Context, session store.SessionIdent, cp store.Checkpoint) error
	LoadSnapshot(ctx context.Context, session store.SessionIdent) ([]store.SnapshotRow, error)
	DeleteSnapshot(ctx context.Context, session store.SessionIdent) error
	SweepSnapshots(ctx context.Context, cutoff time.Time) (int64, error)
	ActiveSessions(ctx context.Context, since time.Time) ([]store.ActiveSession, error)
	LastHookBefore(ctx context.Context, session store.SessionIdent, beforeID int64) (time.Time, error)
}

// Counts are the runner's counters, for the shutdown line.
type Counts struct {
	Checkpoints, EffectsStored, GitFailures int64
}

// session is one session's checkpoint state. ident, and the fields under the runner's mu
// (workDir, learn, lastHook, ended, running, dirty), are shared; roots, snap, tops and
// seeded are touched only by the session's one running checkpoint.
type session struct {
	ident store.SessionIdent

	workDir  string
	learn    []learned
	lastHook time.Time
	ended    bool
	running  bool
	dirty    bool

	// firstHookID is the hook that created this memory entry; prevStart is the start of
	// the last checkpoint that saved, or zero before one has run in this entry. Both are
	// touched only by the running checkpoint, except firstHookID, set once under mu.
	firstHookID int64
	prevStart   time.Time

	loaded bool
	seeded bool
	roots  *rootSet
	snap   map[string]map[string]fileState
	tops   map[string]string // root -> its git top level, "" for none
}

// learned is a path one of the session's tools named, to learn a watched folder from.
type learned struct {
	path  string
	write bool
}

// top is root's git top level, looked up once per root.
func (s *session) top(ctx context.Context, g *gitRunner, root string) (string, bool) {
	if t, ok := s.tops[root]; ok {
		return t, t != ""
	}
	t, _ := g.topLevel(ctx, root)
	s.tops[root] = t
	return t, t != ""
}

// Runner runs file checkpoints on stored hook events (spec 014 AC1): each session has at
// most one checkpoint running and one queued. It also keeps the set of active sessions
// the network sensor links connections to (AC10).
type Runner struct {
	store    CheckpointStore
	rules    Rules
	onSaved  Saved
	log      *zap.Logger
	git      *gitRunner
	ignore   map[string]bool
	maxCopy  int64
	extra    []string
	window   time.Duration
	retain   time.Duration
	files    bool
	home     string
	temps    []string
	now      func() time.Time
	evictAge time.Duration

	// check runs one checkpoint; tests replace it.
	check func(ctx context.Context, s *session) error

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	sessions map[store.SessionIdent]*session
	active   map[store.SessionIdent]time.Time // last hook of each session with no end

	checkpoints, effectsStored, gitFailures atomic.Int64
}

// NewRunner builds the runner from effects config. Start seeds and sweeps it.
func NewRunner(cfg config.Effects, st CheckpointStore, rules Rules, onSaved Saved, log *zap.Logger) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	home, _ := os.UserHomeDir()
	ignore := make(map[string]bool, len(cfg.Files.Ignore))
	for _, n := range cfg.Files.Ignore {
		ignore[n] = true
	}
	r := &Runner{
		store: st, rules: rules, onSaved: onSaved, log: log,
		git: newGitRunner(cfg.Files.GitTimeout), ignore: ignore, maxCopy: cfg.Files.MaxCopyBytes,
		extra: cfg.Files.ExtraDirs, window: cfg.Network.ActiveWindow, retain: cfg.Files.SnapshotRetention,
		files: cfg.Files.Enabled, home: home, temps: defaultTemps(), now: time.Now, evictAge: evictAfter,
		ctx: ctx, cancel: cancel,
		sessions: map[store.SessionIdent]*session{}, active: map[store.SessionIdent]time.Time{},
	}
	r.check = r.runCheckpoint
	return r
}

// Start seeds the active set from the store, sweeps old snapshots, and runs the hourly
// sweep until Close.
func (r *Runner) Start() {
	ctx, cancel := context.WithTimeout(r.ctx, storeTimeout)
	if list, err := r.store.ActiveSessions(ctx, r.now().Add(-r.window)); err == nil {
		r.mu.Lock()
		for _, a := range list {
			r.active[a.SessionIdent] = a.LastHook
		}
		r.mu.Unlock()
	} else {
		r.log.Warn("effects: active sessions not read", zap.Error(err))
	}
	cancel()
	r.sweep()
	r.wg.Add(1)
	logging.Go(r.log, "effects_sweep", func() error {
		defer r.wg.Done()
		t := time.NewTicker(sweepEvery)
		defer t.Stop()
		for {
			select {
			case <-r.ctx.Done():
				return nil
			case <-t.C:
				r.sweep()
			}
		}
	})
}

// sweep drops snapshots older than the retention and evicts idle sessions from memory.
func (r *Runner) sweep() {
	ctx, cancel := context.WithTimeout(r.ctx, storeTimeout)
	defer cancel()
	if _, err := r.store.SweepSnapshots(ctx, r.now().Add(-r.retain)); err != nil && r.ctx.Err() == nil {
		r.log.Warn("effects: snapshot sweep failed", zap.Error(err))
	}
	cutoff := r.now().Add(-r.evictAge)
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, s := range r.sessions {
		if !s.running && s.lastHook.Before(cutoff) {
			delete(r.sessions, id)
		}
	}
	for id, at := range r.active {
		if at.Before(r.now().Add(-r.window)) {
			delete(r.active, id)
		}
	}
}

// OnStored is called for each stored hook event. It only updates memory and signals: it
// never blocks on git or the store.
func (r *Runner) OnStored(h hooks.HookStored) {
	if h.SessionKey == "" {
		return // E11: no session, no checkpoint
	}
	id := store.SessionIdent{Principal: h.Principal, Client: h.Client, Key: h.SessionKey}
	at := h.At
	if at.IsZero() {
		at = r.now()
	}
	ev := h.Event
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if ev.Phase == core.PhaseSessionEnd {
		delete(r.active, id)
	} else {
		r.active[id] = at
	}
	if !r.files || ev.WorkDir == "" {
		return // E11: no working folder, no checkpoint
	}
	s := r.sessions[id]
	if s == nil {
		s = &session{ident: id, tops: map[string]string{}, firstHookID: h.ID}
		r.sessions[id] = s
	}
	s.lastHook = at
	if s.workDir == "" {
		s.workDir = filepath.Clean(ev.WorkDir)
	}
	if ev.Phase == core.PhaseSessionEnd {
		s.ended = true
	}
	if ev.Phase == core.PhasePostTool && r.rules != nil {
		if rules := r.rules(h.Client); rules != nil {
			for _, p := range rules.WritePaths(ev.ToolName, ev.ToolInput) {
				s.learn = append(s.learn, learned{path: absUnder(p, ev.WorkDir), write: true})
			}
			if p, _, ok := rules.ReadTarget(ev.ToolName, ev.ToolInput); ok && p != "" {
				s.learn = append(s.learn, learned{path: absUnder(p, ev.WorkDir)})
			}
		}
	}
	r.signalLocked(s)
}

// absUnder is p made absolute against dir.
func absUnder(p, dir string) string {
	if filepath.IsAbs(p) || dir == "" {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, p)
}

// signalLocked starts a checkpoint for s, or marks one queued if one is running. r.mu is
// held.
func (r *Runner) signalLocked(s *session) {
	if s.running {
		s.dirty = true
		return
	}
	s.running = true
	r.wg.Add(1)
	logging.Go(r.log, "effects_checkpoint", func() error {
		defer r.wg.Done()
		r.loop(s)
		return nil
	})
}

// loop runs checkpoints for s while it is dirty, then ends; a session that ended has its
// snapshot deleted after its last checkpoint (E16).
func (r *Runner) loop(s *session) {
	for {
		if r.ctx.Err() != nil {
			r.mu.Lock()
			s.running, s.dirty = false, false
			r.mu.Unlock()
			return
		}
		if err := r.check(r.ctx, s); err != nil && r.ctx.Err() == nil {
			r.log.Warn("effects: checkpoint failed", zap.Error(err))
		}
		r.checkpoints.Add(1)
		r.mu.Lock()
		if s.dirty {
			s.dirty = false
			r.mu.Unlock()
			continue
		}
		s.running = false
		ended := s.ended
		if ended {
			delete(r.sessions, s.ident)
		}
		r.mu.Unlock()
		if ended {
			ctx, cancel := context.WithTimeout(r.ctx, storeTimeout)
			if err := r.store.DeleteSnapshot(ctx, s.ident); err != nil && r.ctx.Err() == nil {
				r.log.Warn("effects: snapshot not deleted", zap.Error(err))
			}
			cancel()
		}
		return
	}
}

// runCheckpoint loads the session's snapshot when it is not in memory, settles its
// watched folders, and runs one checkpoint.
func (r *Runner) runCheckpoint(ctx context.Context, s *session) error {
	if !s.loaded {
		lctx, cancel := context.WithTimeout(ctx, storeTimeout)
		rows, err := r.store.LoadSnapshot(lctx, s.ident)
		cancel()
		if err != nil {
			return err
		}
		s.roots = newRootSet(r.home, r.temps)
		s.snap = map[string]map[string]fileState{}
		for _, row := range rows {
			m := s.snap[row.Root]
			if m == nil {
				m = map[string]fileState{}
				s.snap[row.Root] = m
				s.roots.add(row.Root)
			}
			if row.Path != "" {
				m[row.Path] = fileState{size: row.Size, mtimeNS: row.MtimeNS, hash: row.Hash, copyHash: row.CopyHash}
			}
		}
		s.loaded = true
		// No checkpoint in this memory entry yet (a restart or an eviction): the window of
		// the first one starts at the session's last hook before the one that made it
		// (spec 015 E7).
		if s.firstHookID > 0 {
			lctx, cancel := context.WithTimeout(ctx, storeTimeout)
			at, err := r.store.LastHookBefore(lctx, s.ident, s.firstHookID)
			cancel()
			if err != nil {
				return err
			}
			s.prevStart = at
		}
	}
	r.mu.Lock()
	workDir, learn := s.workDir, s.learn
	s.learn = nil
	r.mu.Unlock()
	if !s.seeded && workDir != "" {
		s.roots.add(workDir)
		if r.rules != nil {
			if rules := r.rules(s.ident.Client); rules != nil {
				for _, d := range rules.ExtraDirs(workDir) {
					s.roots.add(d)
				}
			}
		}
		for _, d := range r.extra {
			s.roots.add(d)
		}
		s.seeded = true
	}
	for _, l := range learn {
		r.learnRoot(ctx, s, l)
	}
	return r.checkpoint(ctx, s)
}

// learnRoot watches the folder a tool path outside every watched folder belongs to: its
// git work tree, or for a write outside any work tree its folder (AC6). Rejected folders
// (home, /, temp) are never added (E8).
func (r *Runner) learnRoot(ctx context.Context, s *session, l learned) {
	if s.roots.covers(l.path) {
		return
	}
	dir := l.path
	if fi, err := os.Stat(l.path); err != nil || !fi.IsDir() {
		dir = filepath.Dir(l.path)
	}
	if rejectedRoot(dir, r.home, r.temps) {
		return
	}
	if top, ok := r.git.topLevel(ctx, dir); ok {
		s.roots.add(top)
		return
	}
	if l.write {
		s.roots.add(dir)
	}
}

// ActiveSession is the one session with a hook event inside the active window and no
// session end since; false with none or more than one (AC10).
func (r *Runner) ActiveSession() (store.SessionIdent, bool) {
	cutoff := r.now().Add(-r.window)
	r.mu.Lock()
	defer r.mu.Unlock()
	var found store.SessionIdent
	n := 0
	for id, at := range r.active {
		if at.Before(cutoff) {
			continue
		}
		found = id
		n++
	}
	return found, n == 1
}

// Counts is a snapshot of the runner's counters.
func (r *Runner) Counts() Counts {
	return Counts{Checkpoints: r.checkpoints.Load(), EffectsStored: r.effectsStored.Load(), GitFailures: r.gitFailures.Load()}
}

// Close stops new checkpoints, cancels the running ones and waits for them until ctx ends.
func (r *Runner) Close(ctx context.Context) {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.cancel()
	done := logging.Go(r.log, "effects_close", func() error {
		r.wg.Wait()
		return nil
	})
	select {
	case <-done:
	case <-ctx.Done():
	}
}
