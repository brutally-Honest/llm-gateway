package effects

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gitRunner runs git as a program, read-only: every call has --no-optional-locks, so it
// never takes index.lock while the agent's own git runs (E6), and a timeout. It never
// runs a command that writes.
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
	cmd := exec.CommandContext(ctx, g.bin, append([]string{"--no-optional-locks", "-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
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

// candidates are the paths git reports for the work tree at top: tracked changes and
// untracked files (status), and git-ignored files but not git-ignored folders (AC4).
func (g *gitRunner) candidates(ctx context.Context, top string) ([]gitEntry, error) {
	status, err := g.run(ctx, top, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	out := parseStatus(status)
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

// parseStatus reads `git status --porcelain=v2 -z`: ordinary (1), renamed or copied (2,
// whose original path follows as its own field), unmerged (u) and untracked (?) entries.
func parseStatus(b []byte) []gitEntry {
	fields := strings.Split(string(b), "\x00")
	var out []gitEntry
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case '1':
			if parts := strings.SplitN(f, " ", 9); len(parts) == 9 {
				out = append(out, gitEntry{path: parts[8], tracked: !strings.Contains(parts[1], "A")})
			}
		case '2':
			if parts := strings.SplitN(f, " ", 10); len(parts) == 10 {
				out = append(out, gitEntry{path: parts[9]})
			}
			if i+1 < len(fields) && fields[i+1] != "" {
				i++
				out = append(out, gitEntry{path: fields[i], tracked: true})
			}
		case 'u':
			if parts := strings.SplitN(f, " ", 11); len(parts) == 11 {
				out = append(out, gitEntry{path: parts[10], tracked: true})
			}
		case '?':
			out = append(out, gitEntry{path: f[2:]})
		}
	}
	return out
}

// show is the last committed version of rel in the work tree at top, if any.
func (g *gitRunner) show(ctx context.Context, top, rel string) ([]byte, bool) {
	out, err := g.run(ctx, top, "show", "HEAD:"+filepath.ToSlash(rel))
	if err != nil {
		return nil, false
	}
	return out, true
}
