package effects

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// gitRunner runs git as a program, read-only. Every call has --no-optional-locks, so it
// never takes index.lock while the agent's own git runs (E6), and a timeout. It never
// runs a command that writes. The repo it runs in is not trusted (spec 034): its config
// and attributes can name programs, so every call turns off the settings that run one
// (core.fsmonitor, hooks, signature programs, transports, lazy fetch, pager) and the
// commands used never hash a work-tree file through a filter.
type gitRunner struct {
	bin     string // "" when git is not installed: every folder is scanned (E1)
	timeout time.Duration
}

func newGitRunner(timeout time.Duration) *gitRunner {
	bin, err := exec.LookPath("git")
	if err != nil {
		bin = ""
	}
	return &gitRunner{bin: bin, timeout: timeout}
}

var errNoGit = errors.New("git is not installed")

// run runs git in dir with args and returns its stdout.
func (g *gitRunner) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if g.bin == "" {
		return nil, errNoGit
	}
	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	argv := append([]string{
		"--no-optional-locks",
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-c", "log.showSignature=false",
		"-c", "protocol.allow=never",
		"-C", dir,
	}, args...)
	cmd := exec.CommandContext(ctx, g.bin, argv...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat",
		"GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=none", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
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
	mode string // as ls-tree prints it, e.g. "100644"
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
	head, err := g.headEntries(ctx, top)
	if err != nil {
		return nil, err
	}
	hashLen := 20
	sha := sha1.New
	if out, err := g.run(ctx, top, "rev-parse", "--show-object-format"); err == nil &&
		strings.TrimSpace(string(out)) == "sha256" {
		hashLen, sha = 32, sha256.New
	}
	entries, indexMTime, err := g.indexEntries(ctx, top, hashLen)
	if err != nil {
		return nil, err
	}
	fileMode := true
	if out, err := g.run(ctx, top, "config", "--type=bool", "core.filemode"); err == nil {
		fileMode = strings.TrimSpace(string(out)) != "false"
	}

	var out []gitEntry
	inIndex := make(map[string]bool, len(entries))
	listed := map[string]bool{}
	for _, e := range entries {
		if e.mode == modeGitlink {
			continue
		}
		inIndex[e.path] = true
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
		case h.oid != e.oid || h.mode != fmt.Sprintf("%06o", e.mode):
			out = append(out, gitEntry{path: e.path, tracked: true})
		case e.skipWorktree || e.assumeValid:
		default:
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if changed(top, e, indexMTime, fileMode, sha) {
				out = append(out, gitEntry{path: e.path, tracked: true})
			}
		}
	}
	for p := range head {
		if !inIndex[p] {
			out = append(out, gitEntry{path: p, tracked: true})
		}
	}

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
	for _, rec := range strings.Split(string(tree), "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta) // mode, type, oid
		if len(f) != 3 || f[0] == "160000" {
			continue
		}
		out[path] = headEntry{mode: f[0], oid: f[2]}
	}
	return out, nil
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
		if data, err := os.ReadFile(p); err == nil {
			if fi, err := os.Stat(p); err == nil {
				if entries, err := readIndex(data, hashLen); err == nil {
					return entries, fi.ModTime(), nil
				}
			}
		}
	}
	out, err := g.run(ctx, top, "ls-files", "-z", "-s", "-v")
	if err != nil {
		return nil, time.Time{}, err
	}
	var entries []indexEntry
	for _, rec := range strings.Split(string(out), "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta) // tag, mode, oid, stage
		if len(f) != 4 {
			continue
		}
		mode, err := strconv.ParseUint(f[1], 8, 32)
		stage, err2 := strconv.ParseUint(f[3], 10, 8)
		if err != nil || err2 != nil {
			continue
		}
		entries = append(entries, indexEntry{
			path: path, mode: uint32(mode), oid: f[2], stage: uint8(stage),
			skipWorktree: f[0] == "S" || f[0] == "s",
			assumeValid:  f[0] != "" && f[0][0] >= 'a' && f[0][0] <= 'z',
		})
	}
	return entries, time.Time{}, nil
}

// changed reports whether the work-tree file of index entry e differs from the index:
// missing, a different kind, a different executable bit, or different raw content. A
// file whose size, mtime and inode match the entry and is older than the index file is
// trusted; a newer one is racy (git could not tell a same-size rewrite) and is hashed.
func changed(top string, e indexEntry, indexMTime time.Time, fileMode bool, sha func() hash.Hash) bool {
	abs := filepath.Join(top, filepath.FromSlash(e.path))
	fi, err := os.Lstat(abs)
	if err != nil {
		return true
	}
	isLink := e.mode&0o170000 == 0o120000
	switch {
	case isLink:
		if fi.Mode()&os.ModeSymlink == 0 {
			return true
		}
	case !fi.Mode().IsRegular():
		return true
	case fileMode && (fi.Mode().Perm()&0o100 != 0) != (e.mode&0o100 != 0):
		return true
	}
	if e.hasStat && !indexMTime.IsZero() && fi.ModTime().Before(indexMTime) &&
		uint32(fi.Size()) == e.size && uint32(fi.ModTime().Unix()) == e.mtimeSec &&
		uint32(fi.ModTime().Nanosecond()) == e.mtimeNsec && inodeMatches(fi, e.ino) {
		return false
	}
	h := sha()
	if isLink {
		target, err := os.Readlink(abs)
		if err != nil {
			return true
		}
		_, _ = fmt.Fprintf(h, "blob %d\x00%s", len(target), target)
	} else {
		f, err := os.Open(abs)
		if err != nil {
			return true
		}
		defer func() { _ = f.Close() }()
		_, _ = fmt.Fprintf(h, "blob %d\x00", fi.Size())
		if _, err := io.Copy(h, f); err != nil {
			return true
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)) != e.oid
}

// inodeMatches compares the inode with the index's (git stores it in 32 bits). Where the
// platform's stat has no inode, it matches, as git does on such platforms.
func inodeMatches(fi os.FileInfo, ino uint32) bool {
	sys := reflect.ValueOf(fi.Sys())
	if sys.Kind() == reflect.Pointer {
		sys = sys.Elem()
	}
	if sys.Kind() != reflect.Struct {
		return true
	}
	f := sys.FieldByName("Ino")
	if !f.IsValid() || !f.CanUint() {
		return true
	}
	return uint32(f.Uint()) == ino
}

// show is the last committed version of rel in the work tree at top, if any. It never
// runs a diff driver or text conversion from the repo.
func (g *gitRunner) show(ctx context.Context, top, rel string) ([]byte, bool) {
	out, err := g.run(ctx, top, "show", "--no-ext-diff", "--no-textconv", "HEAD:"+filepath.ToSlash(rel))
	if err != nil {
		return nil, false
	}
	return out, true
}
