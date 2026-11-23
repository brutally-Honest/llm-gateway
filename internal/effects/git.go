package effects

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// gitRunner runs git as a program, read-only. Every call has --no-optional-locks, so it
// never takes index.lock while the agent's own git runs (E6), and a timeout. It never
// runs a command that writes. The repo it runs in is not trusted (spec 034): its config
// and attributes can name programs, so every call turns off the settings that run one
// (core.fsmonitor, hooks, signature programs, transports, lazy fetch, pager) and the
// commands used never hash a work-tree file through a filter.
type gitRunner struct {
	bin     string        // "" when git is not installed: every folder is scanned (E1)
	timeout time.Duration // each git call
	// hashTimeout is the budget for the Go comparison (hashing) in one candidates call;
	// 0 means timeout. Separate so slow hashing and slow git cannot starve each other.
	hashTimeout time.Duration
	maxOut      int64 // stdout cap per call; 0 means maxGitOutput (tests lower it)
	maxRecords  int   // record cap per listing parse; 0 means maxGitRecords (tests lower it)

	mu    sync.Mutex
	cache map[string]map[string]hashedFile // by top, then path: files hashed equal to the index
	used  map[string]time.Time             // by top: when candidates last ran there
}

// cacheIdle is how long a work tree can go unused before its cache is dropped, so the
// cache does not grow with every work tree the gateway has ever seen.
const cacheIdle = time.Hour

// maxIndexBytes is the largest index file the Go reader takes in; a bigger one (or one
// that is not a regular file) goes to `git ls-files` instead of being read into memory.
// 128 MiB: real non-split indexes are tens of MB, and the parsed entries, the oid
// strings and the lookup map cost several times the file size in memory.
const maxIndexBytes = 128 << 20

// hashedFile is a work-tree file the last candidates call hashed and found equal to its
// index entry. Its stat identifies the content the next call may skip hashing.
type hashedFile struct {
	size    int64
	mtimeNs int64
	ctimeNs int64
	ino     uint64
	oid     string
}

func newGitRunner(timeout time.Duration) *gitRunner {
	bin, err := exec.LookPath("git")
	if err != nil {
		bin = ""
	}
	return &gitRunner{bin: bin, timeout: timeout}
}

var errNoGit = errors.New("git is not installed")

// maxGitOutput caps the stdout one git call may produce. A hostile repo can make
// `ls-tree -r` or `ls-files` print gigabytes from a tiny object store, and run holds the
// output in memory, and the parsers multiply it (buffer growth, map and struct overhead
// per record: measured about 9x the output as peak heap). 64 MiB is far above a real
// listing (about 100 bytes a file, so roughly 600k files); a bigger repo is skipped like
// any other over-cap call, which is the safe side of the trade.
const maxGitOutput = 64 << 20

// maxGitRecords caps the records one listing parser accepts, whatever the byte size
// (short records make many entries per byte). Real repos stay far below it.
const maxGitRecords = 2_000_000

var errGitOutputTooLarge = errors.New("git output over the size cap")

// capWriter collects up to limit bytes; the first write past it cancels git and fails.
type capWriter struct {
	buf    bytes.Buffer
	limit  int64
	over   bool
	cancel context.CancelFunc
}

func (w *capWriter) Write(p []byte) (int, error) {
	if int64(w.buf.Len())+int64(len(p)) > w.limit {
		w.over = true
		w.cancel()
		return 0, errGitOutputTooLarge
	}
	return w.buf.Write(p)
}

// run runs git in dir with args and returns its stdout, at most maxOut (or maxGitOutput)
// bytes; more fails with errGitOutputTooLarge.
func (g *gitRunner) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	limit := g.maxOut
	if limit <= 0 {
		limit = maxGitOutput
	}
	return g.runLimit(ctx, dir, limit, args...)
}

func (g *gitRunner) runLimit(ctx context.Context, dir string, limit int64, args ...string) ([]byte, error) {
	if g.bin == "" {
		return nil, errNoGit
	}
	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	// cancelOut is derived before the command is built, so the capWriter can kill git.
	ctx, cancelOut := context.WithCancel(ctx)
	defer cancelOut()
	argv := append([]string{
		"--no-optional-locks",
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "log.showSignature=false",
		"-c", "protocol.allow=never",
		"-C", dir,
	}, args...)
	cmd := exec.CommandContext(ctx, g.bin, argv...)
	cmd.Env = append(gitEnv(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat",
		"GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=none", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	out := &capWriter{limit: limit, cancel: cancelOut}
	var errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &errOut
	err := cmd.Run()
	if out.over {
		return nil, errGitOutputTooLarge
	}
	if err != nil {
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			if len(msg) > 300 {
				msg = msg[:300]
			}
			return nil, fmt.Errorf("%w: %s", err, msg) // still an *exec.ExitError to errors.As
		}
		return nil, err
	}
	return out.buf.Bytes(), nil
}

// inheritedGitVars are repo-location and config variables the gateway may have inherited
// (it can be started from a git hook or alias). They would point git at another repo,
// object store or config, or name a program, so none reaches git.
var inheritedGitVars = map[string]bool{
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
	"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_COMMON_DIR": true, "GIT_NAMESPACE": true, "GIT_CONFIG_PARAMETERS": true,
	"GIT_CONFIG_COUNT": true, "GIT_EXTERNAL_DIFF": true, "GIT_EXEC_PATH": true,
}

// gitEnv is the gateway's environment without the repo-location variables. HOME and
// GIT_CONFIG_GLOBAL stay: they are the user's own config, not the repo's.
func gitEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if inheritedGitVars[k] || strings.HasPrefix(k, "GIT_CONFIG_KEY_") ||
			strings.HasPrefix(k, "GIT_CONFIG_VALUE_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// topLevel is the work tree dir is in; false when it is in none or git is missing.
func (g *gitRunner) topLevel(ctx context.Context, dir string) (string, bool) {
	out, err := g.run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return "", false
	}
	return filepath.Clean(top), true
}

// gitEntry is one candidate from git: its path from the top level, and whether git
// tracks it (a tracked path that is new to the snapshot is a modification).
type gitEntry struct {
	path    string
	tracked bool
}

// headEntry is one file of the HEAD commit.
type headEntry struct {
	mode uint32 // as ls-tree prints it in octal, e.g. 0o100644
	oid  string
}

// candidates are the paths git reports for the work tree at top: tracked changes and
// untracked files (what `git status` listed), and git-ignored files but not git-ignored
// folders (AC4). It does not run `git status`: that hashes stat-dirty files through the
// repo's clean filters, which can run a program the repo names (spec 034). The same
// comparison is done here: HEAD from ls-tree, the index file read in Go, and a
// work-tree file hashed raw when its stat no longer matches the index. A file whose
// checked-in form differs from its raw form can be listed although git calls it clean
// (research D2). Submodules are skipped (D4).
func (g *gitRunner) candidates(ctx context.Context, top string) ([]gitEntry, error) {
	// Each git call has its own g.timeout (runLimit). The Go comparison has a separate
	// hashing budget, so slow hashing cannot starve git and git cannot starve hashing: a
	// huge file cannot outlast it (S4), and what it proved before it ran out is kept (D44).
	hashBudget := g.hashTimeout
	if hashBudget <= 0 {
		hashBudget = g.timeout
	}
	head, err := g.headEntries(ctx, top)
	if err != nil {
		return nil, err
	}
	hashLen := 20
	sha := sha1.New
	format, err := g.run(ctx, top, "rev-parse", "--show-object-format")
	switch {
	case err == nil:
		if strings.TrimSpace(string(format)) == "sha256" {
			hashLen, sha = 32, sha256.New
		}
	case ctx.Err() == nil && oldGitWithoutOption(err):
		// git before 2.38 has no --show-object-format and only knows sha1.
	default:
		return nil, err
	}
	entries, indexMTime, err := g.indexEntries(ctx, top, hashLen)
	if err != nil {
		return nil, err
	}
	// Every work-tree file is opened through this root, which refuses a path that leaves
	// top through a symlink or "..". Without it no comparison is trustworthy, so the
	// folder is skipped for this checkpoint.
	root, err := openDirRoot(top)
	if err != nil {
		return nil, fmt.Errorf("open work tree %s: %w", top, err)
	}
	defer func() { _ = root.Close() }()
	cfg, err := g.configBools(ctx, top)
	if err != nil {
		return nil, err
	}
	// The budget does not run yet: hashes run under ctx alone until one proves a file equal
	// to the index, and the budget starts then (D51). So the free hashes cannot spend it,
	// and every call proves at least one file unless the caller's own context ends it (D50).
	chk := &checker{
		ctx: ctx, parent: ctx, budget: hashBudget, top: top, root: root, sha: sha, indexMTime: indexMTime,
		fileMode: true, trustCtime: true, symlinks: true,
		old: g.cachedFor(top), seen: map[string]hashedFile{},
		dirOK: map[string]bool{}, deferHuge: true,
	}
	defer chk.stopBudget()
	chk.readConfig(cfg)

	var out []gitEntry
	inIndex := make(map[string]bool, len(entries))
	listed := map[string]bool{}
	for _, e := range entries {
		if e.mode == modeGitlink {
			continue
		}
		inIndex[e.path] = true
		if !safeRelPath(e.path) {
			continue // never opened, never listed
		}
		if e.stage > 0 {
			if !listed[e.path] {
				listed[e.path] = true
				out = append(out, gitEntry{path: e.path, tracked: true})
			}
			continue
		}
		h, inHead := head[e.path]
		switch {
		case !inHead:
			out = append(out, gitEntry{path: e.path})
		case h.oid != e.oid || h.mode != e.mode:
			out = append(out, gitEntry{path: e.path, tracked: true})
		case e.assumeValid:
		case e.skipWorktree && !chk.presentOutsideSparse(e.path):
			// skip-worktree and absent (or expected to be present: sparse.expectFilesOutsideOfPatterns)
		default:
			// The budget is checked inside changed, only before a hash starts.
			if chk.changed(e) {
				out = append(out, gitEntry{path: e.path, tracked: true})
			}
		}
	}
	for p := range head {
		if !inIndex[p] {
			out = append(out, gitEntry{path: p, tracked: true})
		}
	}

	// A file whose hashing was cut off is listed unproven: the answer is incomplete. A
	// budget that ran out with nothing left to hash is not a failure.
	if chk.cut {
		g.keepProgress(top, chk)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, context.DeadlineExceeded
	}
	// Files of 4 GiB or more were set aside: one can outlast any budget, and would fail
	// every call and skip the folder for good. They get one budget of their own, after
	// every other file is judged. A file not proven equal to the index when that budget
	// ends is listed as changed (a false "maybe changed" is safe: the checkpoint reads the
	// file itself) and not cached. Only the caller's own context ends the call.
	if len(chk.deferred) > 0 {
		chk.stopBudget()
		hugeCtx, cancelHuge := context.WithTimeout(ctx, hashBudget)
		defer cancelHuge()
		// A huge file never gets a free hash: its budget runs from the start.
		chk.ctx, chk.deferHuge, chk.budget, chk.timed = hugeCtx, false, 0, true
		for _, e := range chk.deferred {
			if chk.changed(e) {
				out = append(out, gitEntry{path: e.path, tracked: true})
			}
		}
		if err := ctx.Err(); err != nil {
			g.keepProgress(top, chk)
			return nil, err
		}
	}
	g.storeCache(top, chk.seen)

	untracked, err := g.run(ctx, top, "ls-files", "-z", "-o", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Split(string(untracked), "\x00") {
		if p != "" {
			out = append(out, gitEntry{path: p})
		}
	}
	ignored, err := g.run(ctx, top, "ls-files", "-z", "-o", "-i", "--exclude-standard", "--directory")
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Split(string(ignored), "\x00") {
		if p == "" || strings.HasSuffix(p, "/") {
			continue // a git-ignored folder: its contents are not checked
		}
		out = append(out, gitEntry{path: p})
	}
	return out, nil
}

// headEntries are the files of HEAD by path, without submodules; empty when HEAD is
// unborn. Only exit code 1 from rev-parse means unborn. Any other failure is an error,
// so a timeout or a missing git never makes every file look added.
func (g *gitRunner) headEntries(ctx context.Context, top string) (map[string]headEntry, error) {
	out := map[string]headEntry{}
	if _, err := g.run(ctx, top, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return nil, err
		}
		return out, nil // unborn: every index entry is added
	}
	tree, err := g.run(ctx, top, "ls-tree", "-r", "-z", "--full-tree", "HEAD")
	if err != nil {
		return nil, err
	}
	if err := eachRecord(tree, g.recordCap(), func(rec []byte) {
		meta, pathB, ok := bytes.Cut(rec, []byte{'\t'})
		if !ok {
			return
		}
		f := bytes.Fields(meta) // mode, type, oid
		if len(f) != 3 || string(f[0]) == "160000" {
			return
		}
		path := string(pathB)
		if !safeRelPath(path) {
			return
		}
		mode, err := strconv.ParseUint(string(f[0]), 8, 32)
		if err != nil {
			return
		}
		out[path] = headEntry{mode: uint32(mode), oid: string(f[2])}
	}); err != nil {
		return nil, err
	}
	return out, nil
}

func (g *gitRunner) recordCap() int {
	if g.maxRecords > 0 {
		return g.maxRecords
	}
	return maxGitRecords
}

// eachRecord calls fn for every NUL-separated record of b, without copying b. More than
// limit records fails with errGitOutputTooLarge. fn must copy what it keeps (string(...)),
// so b can be freed once parsing ends.
func eachRecord(b []byte, limit int, fn func(rec []byte)) error {
	n := 0
	for len(b) > 0 {
		i := bytes.IndexByte(b, 0)
		rec := b
		if i < 0 {
			b = nil
		} else {
			rec, b = b[:i], b[i+1:]
		}
		if n++; n > limit {
			return errGitOutputTooLarge
		}
		fn(rec)
	}
	return nil
}

// indexEntries are the index's entries and the index file's modification time (zero
// when the entries came from git). The index file is read in Go; when it cannot be
// (split or sparse index, unknown version, corrupt) the entries come from
// `git ls-files -s -v`, with no stat, so every one is hashed (research D5).
func (g *gitRunner) indexEntries(ctx context.Context, top string, hashLen int) ([]indexEntry, time.Time, error) {
	if path, err := g.run(ctx, top, "rev-parse", "--git-path", "index"); err == nil {
		p := strings.TrimRight(string(path), "\r\n")
		if !filepath.IsAbs(p) {
			p = filepath.Join(top, p)
		}
		if data, mtime, err := readIndexFile(p); err == nil {
			if entries, err := readIndex(data, hashLen); err == nil {
				return entries, mtime, nil
			}
		}
	}
	out, err := g.run(ctx, top, "ls-files", "-z", "-s", "-v")
	if err != nil {
		return nil, time.Time{}, err
	}
	var entries []indexEntry
	if err := eachRecord(out, g.recordCap(), func(rec []byte) {
		meta, path, ok := bytes.Cut(rec, []byte{'\t'})
		if !ok {
			return
		}
		f := bytes.Fields(meta) // tag, mode, oid, stage
		if len(f) != 4 {
			return
		}
		mode, err := strconv.ParseUint(string(f[1]), 8, 32)
		stage, err2 := strconv.ParseUint(string(f[3]), 10, 8)
		if err != nil || err2 != nil {
			return
		}
		entries = append(entries, indexEntry{
			path: string(path), mode: uint32(mode), oid: string(f[2]), stage: uint8(stage),
			skipWorktree: string(f[0]) == "S" || string(f[0]) == "s",
			assumeValid:  len(f[0]) > 0 && f[0][0] >= 'a' && f[0][0] <= 'z',
		})
	}); err != nil {
		return nil, time.Time{}, err
	}
	return entries, time.Time{}, nil
}

// readIndexFile reads the index file at p and returns it with its mtime, both from one
// fstat. The file is opened without blocking (a FIFO planted there must not hang the
// open), must be a regular file, and must not be larger than maxIndexBytes (a symlink to
// /dev/zero or a huge sparse file must not fill memory); anything else is an error and
// the caller falls back to git.
func readIndexFile(p string) ([]byte, time.Time, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|oNonblock, 0)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, time.Time{}, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxIndexBytes {
		return nil, time.Time{}, errors.New("index: not a regular file or too large")
	}
	data, err := readExactly(f, st.Size())
	if err != nil {
		return nil, time.Time{}, err
	}
	return data, st.ModTime(), nil
}

// readExactly reads exactly size bytes from r into one allocation of that size (no
// doubling growth) and fails if r is shorter or has more left, i.e. the file changed
// size after the fstat.
func readExactly(r io.Reader, size int64) ([]byte, error) {
	errChanged := errors.New("index: size changed while reading")
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, errChanged
		}
		return nil, err
	}
	var extra [1]byte
	if n, err := r.Read(extra[:]); n > 0 || (err != nil && err != io.EOF) {
		if n > 0 {
			return nil, errChanged
		}
		return nil, err
	}
	return data, nil
}

// oldGitWithoutOption reports a git too old to know `rev-parse --show-object-format`:
// exit 128 with the option named in its message. Any other failure is a real one.
func oldGitWithoutOption(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 128 &&
		strings.Contains(err.Error(), "show-object-format")
}

// safeRelPath is false for a path git itself would refuse to check out (the spirit of
// verify_path): absolute, or with an empty, "." or ".." component, or a ".git" component
// in any case. Such a path is never opened.
func safeRelPath(p string) bool {
	if p == "" || p[0] == '/' {
		return false
	}
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." || c == ".." || strings.EqualFold(c, ".git") {
			return false
		}
	}
	return true
}

// configBools are core.filemode, core.trustctime, core.symlinks, core.sparseCheckout and
// sparse.expectFilesOutsideOfPatterns as `git config` prints them with --type=bool, one
// "name value" per line. Exit code 1 means none is set: empty, every default. Any other
// failure (a timeout, a bad boolean) is an error, so a failed call never silently
// changes how files are compared.
func (g *gitRunner) configBools(ctx context.Context, top string) (string, error) {
	out, err := g.run(ctx, top, "config", "--type=bool", "--get-regexp",
		`^(core\.(filemode|trustctime|symlinks|sparsecheckout)|sparse\.expectfilesoutsideofpatterns)$`)
	if err != nil {
		var exitErr *exec.ExitError
		if ctx.Err() == nil && errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", err
	}
	return string(out), nil
}

// keepProgress stores the matches a call that ran out of hashing budget had proven (same
// rules as a full pass, D21), plus the old entries it never reached, so the next
// checkpoint starts where this one stopped. An old entry is stat-keyed, so a stale one is
// never trusted.
func (g *gitRunner) keepProgress(top string, chk *checker) {
	for p, v := range chk.old {
		if _, ok := chk.seen[p]; !ok {
			chk.seen[p] = v
		}
	}
	g.storeCache(top, chk.seen)
}

func (g *gitRunner) cachedFor(top string) map[string]hashedFile {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	for t, at := range g.used {
		if t != top && now.Sub(at) > cacheIdle {
			delete(g.used, t)
			delete(g.cache, t)
		}
	}
	if g.used == nil {
		g.used = map[string]time.Time{}
	}
	g.used[top] = now
	return g.cache[top] // never mutated after it is stored: safe to read unlocked
}

// storeCache keeps the files this call hashed equal to the index; files not seen in this
// call are dropped with the old map.
func (g *gitRunner) storeCache(top string, seen map[string]hashedFile) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cache == nil {
		g.cache = map[string]map[string]hashedFile{}
	}
	if len(seen) == 0 {
		delete(g.cache, top)
		return
	}
	g.cache[top] = seen
}

// checker compares work-tree files with index entries during one candidates call.
type checker struct {
	ctx        context.Context
	top        string
	root       *os.Root // top, opened once: the race-free backstop for every file read
	sha        func() hash.Hash
	indexMTime time.Time
	fileMode   bool // core.filemode
	trustCtime bool // core.trustctime
	symlinks   bool // core.symlinks
	sparseOut  bool // sparse.expectFilesOutsideOfPatterns
	sparseOn   bool // core.sparseCheckout
	old        map[string]hashedFile
	seen       map[string]hashedFile
	dirOK      map[string]bool // work-tree folder (slash path) -> a real folder all the way down
	parent     context.Context // the caller's context
	budget     time.Duration   // hashing budget, started by the first proven match
	timed      bool            // ctx carries the budget deadline
	cancel     context.CancelFunc
	afterLstat func(abs string) // tests only: runs between the Lstat and the open
	cut        bool             // a hash was stopped by its context
	deferHuge  bool             // set aside stat-dirty files of maxHashSize or more
	deferred   []indexEntry     // the files set aside
}

func (c *checker) readConfig(out string) {
	for _, line := range strings.Split(out, "\n") {
		name, val, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		on := strings.TrimSpace(val) != "false"
		switch name {
		case "core.filemode":
			c.fileMode = on
		case "core.trustctime":
			c.trustCtime = on
		case "core.symlinks":
			c.symlinks = on
		case "sparse.expectfilesoutsideofpatterns":
			c.sparseOut = on
		case "core.sparsecheckout":
			c.sparseOn = on
		}
	}
}

// presentOutsideSparse is whether a skip-worktree path is to be compared: since git 2.37
// git status reports a skip-worktree file that exists in the work tree (written outside
// the sparse patterns) as modified, but only when core.sparseCheckout is on and
// sparse.expectFilesOutsideOfPatterns is not set; otherwise a skip-worktree entry is
// unchanged whatever the work tree holds. An absent file, or one below a symlinked
// folder, is unchanged.
func (c *checker) presentOutsideSparse(path string) bool {
	if !c.sparseOn || c.sparseOut {
		return false
	}
	if i := strings.LastIndexByte(path, '/'); i >= 0 && !c.realDir(path[:i]) {
		return false
	}
	_, err := os.Lstat(filepath.Join(c.top, filepath.FromSlash(path)))
	return err == nil
}

// realDir reports whether rel (a slash path below top, "" for top) is a folder and so
// is every folder above it: none is a symlink. Git refuses a path with a symlinked
// leading folder, and following one could read a file outside the work tree.
func (c *checker) realDir(rel string) bool {
	if rel == "" || rel == "." {
		return true
	}
	if ok, seen := c.dirOK[rel]; seen {
		return ok
	}
	parent := ""
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		parent = rel[:i]
	}
	ok := c.realDir(parent)
	if ok {
		fi, err := os.Lstat(filepath.Join(c.top, filepath.FromSlash(rel)))
		ok = err == nil && fi.IsDir()
	}
	c.dirOK[rel] = ok
	return ok
}

// maxHashSize is the size from which a stat-dirty tracked file is listed without hashing:
// the index stores sizes as 32 bits.
const maxHashSize = 1 << 32

// changed reports whether the work-tree file of index entry e differs from the index:
// missing (or below a symlinked folder), a different kind, a different executable bit, a
// different size, or different raw content. A file whose size, mtime, ctime and inode
// match the entry and is older than the index file is trusted; a newer one is racy (git
// could not tell a same-size rewrite) and is hashed.
func (c *checker) changed(e indexEntry) bool {
	if i := strings.LastIndexByte(e.path, '/'); i >= 0 && !c.realDir(e.path[:i]) {
		return true
	}
	abs := filepath.Join(c.top, filepath.FromSlash(e.path))
	fi, err := os.Lstat(abs)
	if err != nil {
		return true
	}
	if c.afterLstat != nil {
		c.afterLstat(abs) // test hook: swap the path before it is opened
	}
	isLink := e.mode&0o170000 == 0o120000
	asFile := false // a link entry kept as a regular file (core.symlinks=false)
	switch {
	case isLink:
		if fi.Mode()&os.ModeSymlink == 0 {
			if c.symlinks || !fi.Mode().IsRegular() {
				return true
			}
			asFile = true
		}
	case !fi.Mode().IsRegular():
		return true
	case c.fileMode && (fi.Mode().Perm()&0o100 != 0) != (e.mode&0o100 != 0):
		return true
	}
	if e.size != 0 && uint32(fi.Size()) != e.size {
		return true
	}
	ino, csec, cnsec, haveIDs := statIDs(fi)
	if e.hasStat && !c.indexMTime.IsZero() && fi.ModTime().Before(c.indexMTime) &&
		uint32(fi.Size()) == e.size && uint32(fi.ModTime().Unix()) == e.mtimeSec &&
		uint32(fi.ModTime().Nanosecond()) == e.mtimeNsec &&
		(!haveIDs || (uint32(ino) == e.ino &&
			(!c.trustCtime || (uint32(csec) == e.ctimeSec && uint32(cnsec) == e.ctimeNsec)))) {
		return false
	}
	if fi.Size() >= maxHashSize && !isLink && c.deferHuge {
		// The index keeps a size mod 2^32, so a size match proves nothing here, and hashing
		// a file this big can outlast the budget at every call. It is hashed after every
		// other file, on a budget of its own (see candidates).
		c.deferred = append(c.deferred, e)
		return false
	}
	id := hashedFile{size: fi.Size(), mtimeNs: fi.ModTime().UnixNano(), ctimeNs: csec*1e9 + cnsec, ino: ino, oid: e.oid}
	if prev, ok := c.old[e.path]; ok && prev == id {
		c.seen[e.path] = prev
		return false
	}
	// The budget is only checked before a hash starts: a file that needs none is never
	// failed by it. Until the budget starts, c.ctx is the caller's context.
	if c.timed && c.ctx.Err() != nil {
		c.cut = true
		return true
	}
	h := c.sha()
	hctx := c.ctx
	if isLink && !asFile {
		target, err := os.Readlink(abs)
		if err != nil {
			return true
		}
		writeBlobHeader(h, int64(len(target)))
		_, _ = io.WriteString(h, target)
	} else {
		// O_NONBLOCK: the path can be swapped for a FIFO after the Lstat; the open must
		// not wait for a writer, and the fstat below must still say regular file.
		f, err := c.root.OpenFile(filepath.FromSlash(e.path), os.O_RDONLY|oNonblock, 0)
		if err != nil {
			return true // missing, or leaves top through a symlink
		}
		defer func() { _ = f.Close() }()
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() {
			return true
		}
		// The path was swapped between the Lstat and the open, or the file changed under
		// us: it is changed, and the size the deferral judged is not the size read (D52).
		if !os.SameFile(fi, st) || st.Size() != fi.Size() || !st.ModTime().Equal(fi.ModTime()) {
			return true
		}
		// A hash outside the budget never reads a huge file; the huge phase has its own.
		if !c.timed && st.Size() >= maxHashSize {
			return true
		}
		writeBlobHeader(h, st.Size())
		if !hashChunks(hctx, h, f, st.Size()) {
			if hctx.Err() != nil {
				c.cut = true // stopped by the budget, not proven: the answer is incomplete
			}
			return true
		}
	}
	if hex.EncodeToString(h.Sum(nil)) != e.oid {
		return true
	}
	// Remember a match for the next call, but not a file touched within the last second:
	// a same-size rewrite in that window could keep the same mtime.
	if time.Since(fi.ModTime()) > time.Second {
		c.seen[e.path] = id
	}
	c.startBudget()
	return false
}

// startBudget starts the hashing budget once, when the first file is proven equal to the
// index, so hashes that proved a difference (or nothing) have not spent it.
func (c *checker) startBudget() {
	if c.timed || c.budget <= 0 {
		return
	}
	c.ctx, c.cancel = context.WithTimeout(c.parent, c.budget)
	c.timed = true
}

// stopBudget releases the budget's timer, if it started.
func (c *checker) stopBudget() {
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}

// hashChunks feeds exactly size bytes of f to h, a megabyte at a time, and stops when the
// context ends, so a huge file cannot outlast the deadline. False when f is shorter or
// longer than size, a read fails, or the context ended.
func hashChunks(ctx context.Context, h hash.Hash, f io.Reader, size int64) bool {
	buf := make([]byte, 1<<20)
	var total int64
	for total < size {
		if ctx.Err() != nil {
			return false
		}
		n, err := f.Read(buf[:min(int64(len(buf)), size-total)])
		_, _ = h.Write(buf[:n])
		total += int64(n)
		if err != nil && (err != io.EOF || total < size) {
			return false
		}
	}
	n, err := f.Read(buf[:1]) // the file must end where its size says
	return n == 0 && err == io.EOF
}

func writeBlobHeader(h hash.Hash, size int64) {
	_, _ = h.Write(strconv.AppendInt([]byte("blob "), size, 10))
	_, _ = h.Write([]byte{0})
}

// show is the last committed version of rel in the work tree at top, if any. It never
// runs a diff driver or text conversion from the repo.
func (g *gitRunner) show(ctx context.Context, top, rel string, limit int64) ([]byte, bool) {
	// limit+1 bytes at most are held, so a huge blob is never fully buffered; a blob over
	// limit fails here, which the caller treats like one it found too big.
	// min with maxGitOutput so a huge limit cannot lift the cap or overflow at MaxInt64.
	hold := int64(maxGitOutput)
	if limit < hold { // limit+1 cannot overflow here
		hold = limit + 1
	}
	out, err := g.runLimit(ctx, top, hold, "show", "--no-ext-diff", "--no-textconv", "HEAD:"+filepath.ToSlash(rel))
	if err != nil {
		return nil, false
	}
	return out, true
}
