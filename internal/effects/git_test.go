package effects

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// parseStatusForTest is the status parser candidates used before spec 034: ordinary (1),
// renamed or copied (2, whose original path follows as its own field), unmerged (u) and
// untracked (?) entries of `git status --porcelain=v2 -z`.
func parseStatusForTest(b []byte) []gitEntry {
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

// plainGit runs git in a safe test repo and returns its stdout.
func plainGit(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

// statusSet is what the old candidates answered, from git status and the ignored list.
func statusSet(t *testing.T, dir string) []string {
	t.Helper()
	out := parseStatusForTest(plainGit(t, dir, "status", "--porcelain=v2", "-z", "--untracked-files=all"))
	for _, p := range strings.Split(string(plainGit(t, dir, "ls-files", "-z", "-o", "-i", "--exclude-standard", "--directory")), "\x00") {
		if p != "" && !strings.HasSuffix(p, "/") {
			out = append(out, gitEntry{path: p})
		}
	}
	return entrySet(out)
}

func entrySet(es []gitEntry) []string {
	seen := map[string]bool{}
	for _, e := range es {
		seen[fmt.Sprintf("%s tracked=%v", e.path, e.tracked)] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameSet(t *testing.T, dir string) {
	t.Helper()
	got, err := newGitRunner(30*time.Second).candidates(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	g, w := entrySet(got), statusSet(t, dir)
	if strings.Join(g, "\n") != strings.Join(w, "\n") {
		t.Errorf("candidates differ from git status\n got: %q\nwant: %q", g, w)
	}
}

func commitAll(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "c")
}

// slowly lets the test change a file without the index shortcut hiding it: a file's mtime
// is moved to the future, so it is racy and stat-different.
func touchFuture(t *testing.T, path string) {
	t.Helper()
	when := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

// AC4: for each kind of change, the Go comparison lists the paths git status listed,
// with the same tracked answer.
func TestCandidates_MatchesGitStatus(t *testing.T) {
	needGit(t)
	// Both sides (candidates and the reference git status) read the same config.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	base := func(t *testing.T, extra ...string) string {
		t.Helper()
		dir := resolve(t.TempDir())
		git(t, dir, append([]string{"init", "-q"}, extra...)...)
		write(t, filepath.Join(dir, "mod.txt"), "one\n")
		write(t, filepath.Join(dir, "staged.txt"), "one\n")
		write(t, filepath.Join(dir, "del.txt"), "x\n")
		write(t, filepath.Join(dir, "rmcached.txt"), "x\n")
		write(t, filepath.Join(dir, "old-name.txt"), "rename me, with enough content to match\n")
		write(t, filepath.Join(dir, "sub", "nested", "keep.txt"), "k\n")
		write(t, filepath.Join(dir, "exe.sh"), "#!/bin/sh\n")
		write(t, filepath.Join(dir, "same.txt"), "same\n")
		write(t, filepath.Join(dir, ".gitignore"), "*.log\nbuild/\n")
		if err := os.Symlink("mod.txt", filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		commitAll(t, dir)
		return dir
	}
	mutate := func(t *testing.T, dir string) {
		t.Helper()
		write(t, filepath.Join(dir, "mod.txt"), "two!\n") // same size
		touchFuture(t, filepath.Join(dir, "mod.txt"))
		write(t, filepath.Join(dir, "staged.txt"), "staged change\n")
		git(t, dir, "add", "staged.txt")
		write(t, filepath.Join(dir, "added.txt"), "new\n")
		git(t, dir, "add", "added.txt")
		write(t, filepath.Join(dir, "gone-added.txt"), "new\n")
		git(t, dir, "add", "gone-added.txt")
		if err := os.Remove(filepath.Join(dir, "gone-added.txt")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(dir, "del.txt")); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "rm", "-q", "--cached", "rmcached.txt")
		git(t, dir, "mv", "old-name.txt", "new-name.txt")
		write(t, filepath.Join(dir, "sub", "nested", "deep", "u.txt"), "u\n")
		write(t, filepath.Join(dir, "top-untracked.txt"), "u\n")
		write(t, filepath.Join(dir, "debug.log"), "ignored\n")
		write(t, filepath.Join(dir, "build", "out.bin"), "ignored\n")
		if err := os.Chmod(filepath.Join(dir, "exe.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("elsewhere", filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", "-N", "intent.txt")
	}
	prep := func(t *testing.T, dir string) {
		write(t, filepath.Join(dir, "intent.txt"), "i\n")
		mutate(t, dir)
	}

	t.Run("mixed changes", func(t *testing.T) {
		dir := base(t)
		prep(t, dir)
		sameSet(t, dir)
	})
	t.Run("clean tree", func(t *testing.T) {
		dir := base(t)
		sameSet(t, dir)
	})
	t.Run("index version 4", func(t *testing.T) {
		dir := base(t)
		git(t, dir, "update-index", "--index-version", "4")
		prep(t, dir)
		sameSet(t, dir)
	})
	t.Run("split index falls back", func(t *testing.T) {
		dir := base(t)
		git(t, dir, "update-index", "--split-index")
		prep(t, dir)
		sameSet(t, dir)
	})
	t.Run("unborn head", func(t *testing.T) {
		needGit(t)
		dir := resolve(t.TempDir())
		git(t, dir, "init", "-q")
		write(t, filepath.Join(dir, "a.txt"), "a\n")
		write(t, filepath.Join(dir, "b.txt"), "b\n")
		git(t, dir, "add", "a.txt")
		sameSet(t, dir)
	})
	t.Run("unmerged", func(t *testing.T) {
		dir := base(t)
		git(t, dir, "checkout", "-q", "-b", "side")
		write(t, filepath.Join(dir, "same.txt"), "side\n")
		git(t, dir, "commit", "-qam", "side")
		git(t, dir, "checkout", "-q", "-")
		write(t, filepath.Join(dir, "same.txt"), "main\n")
		git(t, dir, "commit", "-qam", "main")
		cmd := exec.Command("git", "-C", dir, "merge", "side")
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x")
		_ = cmd.Run() // conflicts: exit 1
		if !strings.Contains(string(plainGit(t, dir, "status", "--porcelain=v2")), "u UU") {
			t.Fatal("no merge conflict was made")
		}
		sameSet(t, dir)
	})
	t.Run("skip-worktree and assume-unchanged", func(t *testing.T) {
		dir := base(t)
		git(t, dir, "update-index", "--skip-worktree", "del.txt")
		git(t, dir, "update-index", "--assume-unchanged", "mod.txt")
		if err := os.Remove(filepath.Join(dir, "del.txt")); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "mod.txt"), "changed but assumed\n")
		sameSet(t, dir)
		got, err := newGitRunner(30*time.Second).candidates(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range got {
			if e.path == "del.txt" || e.path == "mod.txt" {
				t.Errorf("%s listed although it is skip-worktree or assume-unchanged", e.path)
			}
		}
	})
	t.Run("skip-worktree in the fallback", func(t *testing.T) {
		dir := base(t)
		git(t, dir, "update-index", "--split-index")
		git(t, dir, "update-index", "--skip-worktree", "del.txt")
		git(t, dir, "update-index", "--assume-unchanged", "mod.txt")
		if err := os.Remove(filepath.Join(dir, "del.txt")); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "mod.txt"), "changed but assumed\n")
		sameSet(t, dir)
	})
	t.Run("linked worktree", func(t *testing.T) {
		dir := base(t)
		wt := filepath.Join(resolve(t.TempDir()), "wt")
		git(t, dir, "worktree", "add", "-q", wt)
		write(t, filepath.Join(wt, "mod.txt"), "two!\n")
		touchFuture(t, filepath.Join(wt, "mod.txt"))
		write(t, filepath.Join(wt, "wt-new.txt"), "n\n")
		if err := os.Remove(filepath.Join(wt, "del.txt")); err != nil {
			t.Fatal(err)
		}
		sameSet(t, wt)
	})
	t.Run("nested untracked repo", func(t *testing.T) {
		dir := base(t)
		nested := filepath.Join(dir, "vendor", "inner")
		write(t, filepath.Join(nested, "x.txt"), "x\n")
		git(t, nested, "init", "-q")
		git(t, nested, "add", "-A")
		git(t, nested, "commit", "-q", "-m", "inner")
		prep(t, dir)
		sameSet(t, dir)
	})
	t.Run("core.worktree", func(t *testing.T) {
		wtree, gdir := resolve(t.TempDir()), filepath.Join(resolve(t.TempDir()), "g.git")
		git(t, wtree, "init", "-q", "--separate-git-dir", gdir)
		git(t, wtree, "config", "core.worktree", wtree)
		write(t, filepath.Join(wtree, "a.txt"), "one\n")
		write(t, filepath.Join(wtree, "b.txt"), "two\n")
		commitAll(t, wtree)
		write(t, filepath.Join(wtree, "a.txt"), "uno\n")
		touchFuture(t, filepath.Join(wtree, "a.txt"))
		write(t, filepath.Join(wtree, "c.txt"), "new\n")
		sameSet(t, wtree)
		got, err := newGitRunner(30*time.Second).candidates(context.Background(), wtree)
		if err != nil {
			t.Fatal(err)
		}
		if s := strings.Join(entrySet(got), ";"); !strings.Contains(s, "a.txt tracked=true") {
			t.Errorf("a.txt not listed as changed: %s", s)
		}
	})
	t.Run("core.symlinks=false", func(t *testing.T) {
		dir := base(t)
		git(t, dir, "config", "core.symlinks", "false")
		// git keeps a link as a regular file holding the link text on such a file system.
		if err := os.Remove(filepath.Join(dir, "link")); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, "link"), "mod.txt")
		sameSet(t, dir)
		got, err := newGitRunner(30*time.Second).candidates(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range got {
			if e.path == "link" {
				t.Errorf("link listed although its file holds the link text")
			}
		}
		write(t, filepath.Join(dir, "link"), "other")
		touchFuture(t, filepath.Join(dir, "link"))
		sameSet(t, dir)
	})
	t.Run("sha256", func(t *testing.T) {
		needGit(t)
		dir := resolve(t.TempDir())
		cmd := exec.Command("git", "init", "-q", "--object-format=sha256", dir)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git cannot make a sha256 repo: %v %s", err, out)
		}
		write(t, filepath.Join(dir, "a.txt"), "one\n")
		write(t, filepath.Join(dir, "b.txt"), "two\n")
		commitAll(t, dir)
		write(t, filepath.Join(dir, "a.txt"), "uno\n")
		touchFuture(t, filepath.Join(dir, "a.txt"))
		write(t, filepath.Join(dir, "c.txt"), "new\n")
		sameSet(t, dir)
		got, err := newGitRunner(30*time.Second).candidates(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if s := strings.Join(entrySet(got), ";"); !strings.Contains(s, "a.txt tracked=true") {
			t.Errorf("sha256 repo: a.txt not listed as changed: %s", s)
		}
	})
	t.Run("unchanged files are not hashed twice or listed", func(t *testing.T) {
		dir := base(t)
		time.Sleep(20 * time.Millisecond)
		git(t, dir, "status", "-s") // refresh the index so nothing is racy
		got, err := newGitRunner(30*time.Second).candidates(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("clean repo listed %v", got)
		}
	})
}

// A canceled context stops the comparison.
func TestCandidates_Canceled(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	commitAll(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newGitRunner(30*time.Second).candidates(ctx, dir); err == nil {
		t.Fatal("candidates ran with a canceled context")
	}
}

// A git call that times out must fail candidates, not report HEAD as unborn, which would
// list every committed file as added.
func TestCandidates_TimeoutIsNotUnbornHead(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	commitAll(t, dir)
	g := newGitRunner(time.Nanosecond)
	// Called alone: HEAD must be an error, not an empty map (the unborn answer).
	if head, err := g.headEntries(context.Background(), dir); err == nil {
		t.Fatalf("headEntries with a 1ns timeout returned no error: %d entries", len(head))
	}
	got, err := g.candidates(context.Background(), dir)
	if err == nil {
		t.Fatalf("candidates with a 1ns timeout returned no error: %v", entrySet(got))
	}
	if len(got) != 0 {
		t.Errorf("candidates returned entries with an error: %v", entrySet(got))
	}
}

// hostile is a repo whose own config and attributes name programs. Each program touches a
// marker file in a folder outside the repo, then passes stdin through.
type hostile struct {
	dir     string
	markers string
}

func (h hostile) script(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(h.markers, name+".sh")
	marker := filepath.Join(h.markers, name)
	text := "#!/bin/sh\ntouch " + marker + "\n" + body + "\n"
	if err := os.WriteFile(p, []byte(text), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func (h hostile) found(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(h.markers)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".sh") {
			out = append(out, e.Name())
		}
	}
	return out
}

// newHostile makes a committed repo (a.txt, b.txt, filter attributes on a.txt) and then
// plants the settings, so setting up never runs them. driver is the filter setting under
// test.
func newHostile(t *testing.T, filterKey string, viaInfo bool) hostile {
	t.Helper()
	needGit(t)
	h := hostile{dir: resolve(t.TempDir()), markers: resolve(t.TempDir())}
	git(t, h.dir, "init", "-q")
	write(t, filepath.Join(h.dir, "a.txt"), "aaaa\n")
	write(t, filepath.Join(h.dir, "b.txt"), "bbbb\n")
	if viaInfo {
		write(t, filepath.Join(h.dir, ".git", "info", "attributes"), "a.txt filter=evil diff=evil\n")
	} else {
		write(t, filepath.Join(h.dir, ".gitattributes"), "a.txt filter=evil diff=evil\n")
	}
	write(t, filepath.Join(h.dir, ".gitignore"), "*.ign\n")
	git(t, h.dir, "add", "-A")
	git(t, h.dir, "commit", "-q", "-m", "init")
	cat := "cat"
	git(t, h.dir, "config", "core.fsmonitor", h.script(t, "fsmonitor", "exit 0"))
	git(t, h.dir, "config", "filter.evil."+filterKey, h.script(t, filterKey, cat))
	git(t, h.dir, "config", "filter.evil.smudge", h.script(t, "smudge", cat))
	git(t, h.dir, "config", "diff.external", h.script(t, "diffexternal", "exit 0"))
	git(t, h.dir, "config", "diff.evil.textconv", h.script(t, "textconv", cat))
	git(t, h.dir, "config", "diff.evil.command", h.script(t, "diffcommand", "exit 0"))
	git(t, h.dir, "config", "core.pager", h.script(t, "pager", cat))
	git(t, h.dir, "config", "core.hooksPath", filepath.Join(h.markers, "hooks"))
	// a.txt: same size, different content, mtime in the future: stat-dirty and racy.
	write(t, filepath.Join(h.dir, "a.txt"), "AAAA\n")
	touchFuture(t, filepath.Join(h.dir, "a.txt"))
	write(t, filepath.Join(h.dir, "untracked.txt"), "u\n")
	write(t, filepath.Join(h.dir, "secret.ign"), "i\n")
	return h
}

// AC1, AC2, AC3: every git path the runner uses, in a repo whose config names programs for
// core.fsmonitor, the clean filter or the process filter, diff.external, a textconv
// driver and the pager, runs none of them, and still reports the right candidates.
func TestGit_HostileRepoRunsNothing(t *testing.T) {
	for _, tc := range []struct {
		key, name string
		viaInfo   bool
	}{
		{"clean", "filter clean", false},
		{"process", "filter process", false},
		{"clean", "filter clean via info/attributes", true},
		{"process", "filter process via info/attributes", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHostile(t, tc.key, tc.viaInfo)
			g := newGitRunner(30 * time.Second)
			ctx := context.Background()

			top, ok := g.topLevel(ctx, h.dir)
			if !ok {
				t.Fatal("no top level")
			}
			got, err := g.candidates(ctx, top)
			if err != nil {
				t.Fatal(err)
			}
			set := strings.Join(entrySet(got), ";")
			for _, want := range []string{"a.txt tracked=true", "untracked.txt tracked=false", "secret.ign tracked=false"} {
				if !strings.Contains(set, want) {
					t.Errorf("candidates lack %q: %s", want, set)
				}
			}
			if strings.Contains(set, "b.txt") {
				t.Errorf("unchanged b.txt is listed: %s", set)
			}
			data, ok := g.show(ctx, top, "a.txt", 1<<20)
			if !ok || string(data) != "aaaa\n" {
				t.Errorf("show a.txt = %q, %v; want the committed bytes", data, ok)
			}
			if m := h.found(t); len(m) > 0 {
				t.Errorf("git ran repo-named programs: %v", m)
			}
		})
	}
	t.Run("full checkpoint", func(t *testing.T) {
		h := newHostile(t, "clean", false)
		// Baseline from the committed state, then the hostile edit again.
		ts := openStore(t)
		r := newTestRunner(t, ts.st)
		checkpointIn(t, r, h.dir)
		write(t, filepath.Join(h.dir, "a.txt"), "ZZZZ\n")
		touchFuture(t, filepath.Join(h.dir, "a.txt"))
		write(t, filepath.Join(h.dir, "new.txt"), "n\n")
		checkpointIn(t, r, h.dir)
		rows := byPath(t, ts.effects(t))
		if _, ok := rows[filepath.Join(h.dir, "a.txt")]; !ok {
			t.Errorf("a.txt change not recorded: %v", rows)
		}
		if _, ok := rows[filepath.Join(h.dir, "new.txt")]; !ok {
			t.Errorf("new.txt not recorded: %v", rows)
		}
		if m := h.found(t); len(m) > 0 {
			t.Errorf("a checkpoint ran repo-named programs: %v", m)
		}
	})
}

// A partial clone whose remote is an ext:: transport would run a program when show needs a
// blob it lacks (a lazy fetch). The runner turns lazy fetch and transports off.
func TestGit_LazyFetchRunsNothing(t *testing.T) {
	needGit(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	src, dst, markers := resolve(t.TempDir()), filepath.Join(resolve(t.TempDir()), "clone"), resolve(t.TempDir())
	git(t, src, "init", "-q")
	git(t, src, "config", "uploadpack.allowFilter", "true")
	git(t, src, "config", "uploadpack.allowAnySHA1InWant", "true")
	write(t, filepath.Join(src, "a.txt"), "aaaa\n")
	commitAll(t, src)
	if out, err := exec.Command("git", "clone", "-q", "--filter=blob:none", "--no-checkout", "--no-local",
		"file://"+src, dst).CombinedOutput(); err != nil {
		t.Skipf("git cannot make a partial clone: %v %s", err, out)
	}
	marker := filepath.Join(markers, "ext-ran")
	script := filepath.Join(markers, "ext.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, dst, "config", "remote.origin.url", "ext::"+script)
	git(t, dst, "config", "protocol.allow", "always")
	git(t, dst, "config", "protocol.ext.allow", "always")

	// Control: plain git does run the program, so the setup is a real lazy fetch.
	ctl, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctl, "git", "-C", dst, "show", "HEAD:a.txt").Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("plain git did not lazy-fetch through ext::; cannot probe on this git")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	g := newGitRunner(30 * time.Second)
	if _, ok := g.show(context.Background(), dst, "a.txt", 1<<20); ok {
		t.Error("show returned a blob the clone does not have")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("show ran the remote's ext:: program")
	}
}

// The gateway's own environment may carry repo-location variables; none reaches git.
func TestGitEnv_StripsRepoLocationVars(t *testing.T) {
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_CONFIG_PARAMETERS",
		"GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_EXTERNAL_DIFF", "GIT_EXEC_PATH"} {
		t.Setenv(k, "/x")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("HOME", "/home/x")
	env := strings.Join(gitEnv(), "\n")
	for _, bad := range []string{"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_OBJECT_DIRECTORY=",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=", "GIT_COMMON_DIR=", "GIT_NAMESPACE=", "GIT_CONFIG_PARAMETERS=",
		"GIT_CONFIG_COUNT=", "GIT_CONFIG_KEY_0=", "GIT_CONFIG_VALUE_0=", "GIT_EXTERNAL_DIFF=", "GIT_EXEC_PATH="} {
		if strings.Contains("\n"+env, "\n"+bad) {
			t.Errorf("%s was passed to git", bad)
		}
	}
	for _, keep := range []string{"GIT_CONFIG_GLOBAL=/dev/null", "HOME=/home/x"} {
		if !strings.Contains(env, keep) {
			t.Errorf("%s was dropped", keep)
		}
	}
}

// A same-size rewrite with the old mtime put back changes the ctime; the entry's stat no
// longer matches, so the file is hashed and listed.
func TestCandidates_CtimeChangeIsSeen(t *testing.T) {
	dir := repo(t)
	p := filepath.Join(dir, "a.txt")
	write(t, p, "one\n")
	commitAll(t, dir)
	time.Sleep(20 * time.Millisecond)
	git(t, dir, "update-index", "--refresh") // index newer than the file: not racy
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	g := newGitRunner(30 * time.Second)
	if got, err := g.candidates(context.Background(), dir); err != nil || len(got) != 0 {
		t.Fatalf("clean tree: %v, %v", got, err)
	}
	write(t, p, "two\n")
	if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	got, err := g.candidates(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.Join(entrySet(got), ";"); s != "a.txt tracked=true" {
		t.Errorf("same-size rewrite with restored mtime: %s", s)
	}
}

// A folder replaced by a symlink to a folder outside the work tree is not followed: the
// files below it are listed as changed without being opened, as git refuses such paths.
func TestCandidates_SymlinkedLeadingFolderIsNotFollowed(t *testing.T) {
	setup := func(t *testing.T) (dir, outside string) {
		dir = repo(t)
		outside = resolve(t.TempDir())
		write(t, filepath.Join(dir, "d", "id_key"), "secret\n")
		write(t, filepath.Join(dir, "d", "e", "f.txt"), "deep\n")
		write(t, filepath.Join(dir, "keep.txt"), "k\n")
		commitAll(t, dir)
		time.Sleep(20 * time.Millisecond)
		git(t, dir, "update-index", "--refresh")
		if err := os.RemoveAll(filepath.Join(dir, "d")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, "d")); err != nil {
			t.Fatal(err)
		}
		return dir, outside
	}
	run := func(t *testing.T, dir string) []gitEntry {
		t.Helper()
		type res struct {
			got []gitEntry
			err error
		}
		ch := make(chan res, 1)
		go func() {
			got, err := newGitRunner(30*time.Second).candidates(context.Background(), dir)
			ch <- res{got, err}
		}()
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatal(r.err)
			}
			return r.got
		case <-time.After(5 * time.Second):
			t.Fatal("candidates blocked: it opened a file below a symlinked folder")
			return nil
		}
	}
	t.Run("same content outside", func(t *testing.T) {
		dir, outside := setup(t)
		write(t, filepath.Join(outside, "id_key"), "secret\n")
		write(t, filepath.Join(outside, "e", "f.txt"), "deep\n")
		s := strings.Join(entrySet(run(t, dir)), ";")
		for _, want := range []string{"d/id_key tracked=true", "d/e/f.txt tracked=true"} {
			if !strings.Contains(s, want) {
				t.Errorf("%q not listed: %s", want, s)
			}
		}
		if strings.Contains(s, "keep.txt") {
			t.Errorf("unchanged keep.txt listed: %s", s)
		}
	})
	t.Run("fifo outside", func(t *testing.T) {
		dir, outside := setup(t)
		if err := exec.Command("mkfifo", filepath.Join(outside, "id_key")).Run(); err != nil {
			t.Skipf("no mkfifo: %v", err)
		}
		s := strings.Join(entrySet(run(t, dir)), ";")
		if !strings.Contains(s, "d/id_key tracked=true") {
			t.Errorf("d/id_key not listed: %s", s)
		}
	})
}

// A tracked file swapped for a FIFO is a changed file, and listing it never blocks.
func TestCandidates_FifoInPlaceOfFile(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	commitAll(t, dir)
	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("mkfifo", filepath.Join(dir, "a.txt")).Run(); err != nil {
		t.Skipf("no mkfifo: %v", err)
	}
	done := make(chan []gitEntry, 1)
	go func() {
		got, _ := newGitRunner(30*time.Second).candidates(context.Background(), dir)
		done <- got
	}()
	select {
	case got := <-done:
		if s := strings.Join(entrySet(got), ";"); !strings.Contains(s, "a.txt tracked=true") {
			t.Errorf("a.txt not listed: %s", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("candidates blocked on a FIFO")
	}
}

// Files the fallback (split index) hashed equal to the index are not hashed again while
// their stat is unchanged, and are dropped from the cache once they are not seen.
func TestCandidates_FallbackCache(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	write(t, filepath.Join(dir, "b.txt"), "two\n")
	commitAll(t, dir)
	git(t, dir, "update-index", "--split-index")
	old := time.Now().Add(-time.Hour)
	for _, n := range []string{"a.txt", "b.txt"} {
		if err := os.Chtimes(filepath.Join(dir, n), old, old); err != nil {
			t.Fatal(err)
		}
	}
	g := newGitRunner(30 * time.Second)
	if _, err := g.candidates(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if len(g.cache[dir]) != 2 {
		t.Fatalf("cache has %d entries, want 2", len(g.cache[dir]))
	}
	// Same-size rewrite, old mtime restored: ctime changes, so it is hashed and listed.
	write(t, filepath.Join(dir, "a.txt"), "uno\n")
	if err := os.Chtimes(filepath.Join(dir, "a.txt"), old, old); err != nil {
		t.Fatal(err)
	}
	got, err := g.candidates(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.Join(entrySet(got), ";"); !strings.Contains(s, "a.txt tracked=true") || strings.Contains(s, "b.txt") {
		t.Errorf("candidates = %s", s)
	}
	if len(g.cache[dir]) != 1 {
		t.Errorf("cache has %d entries after a.txt changed, want only b.txt", len(g.cache[dir]))
	}
}

// candidatesWithin runs candidates and fails the test when it does not return in d.
func candidatesWithin(t *testing.T, d time.Duration, g *gitRunner, ctx context.Context, dir string) ([]gitEntry, error) {
	t.Helper()
	type res struct {
		got []gitEntry
		err error
	}
	ch := make(chan res, 1)
	go func() {
		got, err := g.candidates(ctx, dir)
		ch <- res{got, err}
	}()
	select {
	case r := <-ch:
		return r.got, r.err
	case <-time.After(d):
		t.Fatalf("candidates did not return within %v", d)
		return nil, nil
	}
}

func plantIndex(t *testing.T, dir string, plant func(path string)) {
	t.Helper()
	p := filepath.Join(dir, ".git", "index")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	plant(p)
}

// A .git/index that is a symlink to /dev/zero is not read into memory: candidates
// returns (git itself rejects the index) instead of filling memory.
func TestCandidates_IndexSymlinkedToDevZero(t *testing.T) {
	if _, err := os.Stat("/dev/zero"); err != nil {
		t.Skip("no /dev/zero")
	}
	dir := repo(t)
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	commitAll(t, dir)
	plantIndex(t, dir, func(p string) {
		if err := os.Symlink("/dev/zero", p); err != nil {
			t.Fatal(err)
		}
	})
	if _, _, err := readIndexFile(filepath.Join(dir, ".git", "index")); err == nil {
		t.Fatal("readIndexFile accepted /dev/zero")
	}
	_, _ = candidatesWithin(t, 20*time.Second, newGitRunner(10*time.Second), context.Background(), dir)
}

// A FIFO at .git/index never hangs the open or the read.
func TestCandidates_IndexIsFifo(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	commitAll(t, dir)
	plantIndex(t, dir, func(p string) {
		if err := exec.Command("mkfifo", p).Run(); err != nil {
			t.Skipf("no mkfifo: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, _ = candidatesWithin(t, 5*time.Second, newGitRunner(30*time.Second), ctx, dir)
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestReadIndexFile_TooLarge(t *testing.T) {
	p := filepath.Join(t.TempDir(), "index")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxIndexBytes + 1); err != nil {
		t.Skipf("no sparse files: %v", err)
	}
	_ = f.Close()
	if _, _, err := readIndexFile(p); err == nil {
		t.Fatal("an index over maxIndexBytes was read")
	}
}

// The read takes exactly the fstat size: a file that shrank or grew is an error.
func TestReadExactly(t *testing.T) {
	got, err := readExactly(strings.NewReader("abcd"), 4)
	if err != nil || string(got) != "abcd" {
		t.Fatalf("exact: %q, %v", got, err)
	}
	if got, err := readExactly(strings.NewReader(""), 0); err != nil || len(got) != 0 {
		t.Fatalf("empty: %q, %v", got, err)
	}
	if _, err := readExactly(strings.NewReader("abc"), 4); err == nil {
		t.Error("a file shorter than its fstat size was accepted")
	}
	if _, err := readExactly(strings.NewReader(""), 4); err == nil {
		t.Error("an empty read of a 4-byte file was accepted")
	}
	if _, err := readExactly(strings.NewReader("abcde"), 4); err == nil {
		t.Error("a file that grew was accepted")
	}
}

// Since git 2.37 a skip-worktree file that exists in the work tree is a modification;
// an absent one is not. sparse.expectFilesOutsideOfPatterns turns the first off.
func TestCandidates_SkipWorktree(t *testing.T) {
	needGit(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	setup := func(t *testing.T) string {
		dir := repo(t)
		write(t, filepath.Join(dir, "in", "a.txt"), "a\n")
		write(t, filepath.Join(dir, "out", "b.txt"), "b\n")
		write(t, filepath.Join(dir, "out", "c.txt"), "c\n")
		commitAll(t, dir)
		git(t, dir, "sparse-checkout", "set", "--cone", "in")
		if _, err := os.Lstat(filepath.Join(dir, "out", "b.txt")); err == nil {
			t.Fatal("out/b.txt is still there after sparse-checkout")
		}
		return dir
	}
	t.Run("absent is unchanged", func(t *testing.T) {
		dir := setup(t)
		got, err := newGitRunner(30*time.Second).candidates(context.Background(), dir)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", entrySet(got), err)
		}
		sameSet(t, dir)
	})
	t.Run("present is modified", func(t *testing.T) {
		dir := setup(t)
		write(t, filepath.Join(dir, "out", "b.txt"), "different\n")
		got, err := newGitRunner(30*time.Second).candidates(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if s := strings.Join(entrySet(got), ";"); s != "out/b.txt tracked=true" {
			t.Errorf("got %s", s)
		}
		sameSet(t, dir)
	})
	t.Run("expectFilesOutsideOfPatterns", func(t *testing.T) {
		dir := setup(t)
		git(t, dir, "config", "sparse.expectFilesOutsideOfPatterns", "true")
		write(t, filepath.Join(dir, "out", "b.txt"), "different\n")
		got, err := newGitRunner(30*time.Second).candidates(context.Background(), dir)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", entrySet(got), err)
		}
		sameSet(t, dir)
	})
	t.Run("skip-worktree without sparse checkout is unchanged", func(t *testing.T) {
		dir := repo(t)
		write(t, filepath.Join(dir, "cfg"), "one\n")
		commitAll(t, dir)
		git(t, dir, "update-index", "--skip-worktree", "cfg")
		write(t, filepath.Join(dir, "cfg"), "two\n")
		got, err := newGitRunner(30*time.Second).candidates(context.Background(), dir)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", entrySet(got), err)
		}
		sameSet(t, dir)
	})
	t.Run("fallback ls-files path", func(t *testing.T) {
		dir := setup(t)
		git(t, dir, "update-index", "--split-index")
		write(t, filepath.Join(dir, "out", "b.txt"), "different\n")
		got, err := newGitRunner(30*time.Second).candidates(context.Background(), dir)
		if err != nil {
			t.Fatal(err)
		}
		if s := strings.Join(entrySet(got), ";"); s != "out/b.txt tracked=true" {
			t.Errorf("got %s", s)
		}
	})
}

// Hashing stops with the context: a huge sparse file whose size matches the index modulo
// 2^32 does not run for minutes.
func TestCandidates_HashingStopsWithContext(t *testing.T) {
	dir := repo(t)
	p := filepath.Join(dir, "big.bin")
	write(t, p, "x")
	commitAll(t, dir)
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Truncate(1<<33 + 1); err != nil {
		t.Skipf("no sparse files: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = candidatesWithin(t, 10*time.Second, newGitRunner(30*time.Second), ctx, dir)
	if err == nil {
		t.Error("candidates finished an 8 GiB hash")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
}

// A file rewritten in place with the same size and the same (future) mtime as its index
// entry is racy: the entry is not older than the index file, so it is hashed.
func TestCandidates_RacyEntryIsHashed(t *testing.T) {
	dir := repo(t)
	git(t, dir, "config", "core.trustctime", "false")
	p := filepath.Join(dir, "a.txt")
	write(t, p, "one\n")
	commitAll(t, dir)
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "update-index", "--refresh") // the index stores the future mtime
	g := newGitRunner(30 * time.Second)
	if got, err := g.candidates(context.Background(), dir); err != nil || len(got) != 0 {
		t.Fatalf("clean tree: %v, %v", entrySet(got), err)
	}
	write(t, p, "two\n")
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	got, err := g.candidates(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.Join(entrySet(got), ";"); s != "a.txt tracked=true" {
		t.Errorf("racy rewrite not seen: %q", s)
	}
}

// The fallback does not cache a file touched within the last second, and a same-size
// rewrite of it is seen.
func TestCandidates_FallbackCacheSkipsFreshFiles(t *testing.T) {
	dir := repo(t)
	p := filepath.Join(dir, "a.txt")
	write(t, p, "one\n")
	commitAll(t, dir)
	git(t, dir, "update-index", "--split-index")
	now := time.Now()
	if err := os.Chtimes(p, now, now); err != nil {
		t.Fatal(err)
	}
	g := newGitRunner(30 * time.Second)
	if got, err := g.candidates(context.Background(), dir); err != nil || len(got) != 0 {
		t.Fatalf("clean tree: %v, %v", entrySet(got), err)
	}
	if n := len(g.cache[dir]); n != 0 {
		t.Fatalf("a file modified just now was cached (%d entries)", n)
	}
	write(t, p, "two\n")
	if err := os.Chtimes(p, now, now); err != nil {
		t.Fatal(err)
	}
	got, err := g.candidates(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.Join(entrySet(got), ";"); s != "a.txt tracked=true" {
		t.Errorf("same-size rewrite not seen: %q", s)
	}
}

func TestSafeRelPath(t *testing.T) {
	for p, want := range map[string]bool{
		"a.txt": true, "d/e/f": true, ".gitignore": true, "x/.github/y": true,
		"/etc/passwd": false, "../x": false, "a/../b": false, "./a": false, "a/./b": false,
		"a//b": false, "a/": false, ".git/config": false, "d/.GIT/x": false, ".Git": false, "": false,
	} {
		if got := safeRelPath(p); got != want {
			t.Errorf("safeRelPath(%q) = %v, want %v", p, got, want)
		}
	}
}

// A work tree not used for an hour loses its cache.
func TestGitRunner_CacheDropsIdleTops(t *testing.T) {
	g := newGitRunner(time.Second)
	g.storeCache("/a", map[string]hashedFile{"x": {}})
	g.storeCache("/b", map[string]hashedFile{"y": {}})
	g.used = map[string]time.Time{"/a": time.Now().Add(-2 * time.Hour), "/b": time.Now()}
	g.cachedFor("/b")
	if _, ok := g.cache["/a"]; ok {
		t.Error("idle top /a kept its cache")
	}
	if _, ok := g.cache["/b"]; !ok {
		t.Error("recent top /b lost its cache")
	}
}

// P1-b: run holds at most its cap of stdout; more kills git and fails with
// errGitOutputTooLarge, while output under the cap comes back whole. show holds at most
// limit+1 bytes of a blob.
func TestGit_OutputCap(t *testing.T) {
	needGit(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	dir := resolve(t.TempDir())
	git(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "a.txt"), strings.Repeat("x", 5000))
	commitAll(t, dir)
	ctx := context.Background()

	g := newGitRunner(30 * time.Second)
	g.maxOut = 1000
	if _, err := g.run(ctx, dir, "show", "HEAD:a.txt"); !errors.Is(err, errGitOutputTooLarge) {
		t.Errorf("run over the cap: err = %v, want errGitOutputTooLarge", err)
	}
	g.maxOut = 6000
	if out, err := g.run(ctx, dir, "show", "HEAD:a.txt"); err != nil || len(out) != 5000 {
		t.Errorf("run under the cap: %d bytes, %v", len(out), err)
	}

	g = newGitRunner(30 * time.Second)
	if data, ok := g.show(ctx, dir, "a.txt", 5000); !ok || len(data) != 5000 {
		t.Errorf("show at the limit: %d bytes, ok=%v", len(data), ok)
	}
	if _, ok := g.show(ctx, dir, "a.txt", 100); ok {
		t.Error("show over the limit returned the blob")
	}
}

// Past the cap git must be killed, not left blocked on a pipe nobody reads until the
// timeout. The blob is well over the pipe buffer.
func TestGit_OutputCapKillsGit(t *testing.T) {
	needGit(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	dir := resolve(t.TempDir())
	git(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "big.txt"), strings.Repeat("x", 5<<20))
	commitAll(t, dir)

	g := newGitRunner(30 * time.Second)
	g.maxOut = 1000
	start := time.Now()
	_, err := g.run(context.Background(), dir, "show", "HEAD:big.txt")
	if !errors.Is(err, errGitOutputTooLarge) {
		t.Errorf("err = %v, want errGitOutputTooLarge", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("run took %v; git was not killed at the cap", d)
	}
}

// P2-b: when one pass cannot hash every stat-mismatched file inside the hashing budget,
// each failed pass still keeps what it proved, so repeated checkpoints finish and the
// answer is git status's.
func TestCandidates_HashProgressSurvivesTimeout(t *testing.T) {
	needGit(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := resolve(t.TempDir())
	git(t, dir, "init", "-q")
	const files = 400
	body := strings.Repeat("x", 32<<10)
	for i := range files {
		write(t, filepath.Join(dir, fmt.Sprintf("f%03d.txt", i)), body)
	}
	commitAll(t, dir)
	// Stat-different from the index but old enough to be cached: every file must be hashed.
	past := time.Now().Add(-time.Hour)
	for i := range files {
		if err := os.Chtimes(filepath.Join(dir, fmt.Sprintf("f%03d.txt", i)), past, past); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(dir, "f007.txt"), body+"changed")
	write(t, filepath.Join(dir, "new.txt"), "new")

	g := newGitRunner(30 * time.Second)
	g.hashTimeout = 15 * time.Millisecond // long enough to walk the cache, too short for every file
	failed, last := 0, -1
	var got []gitEntry
	var err error
	for call := 0; call < 1000; call++ {
		got, err = g.candidates(context.Background(), dir)
		g.mu.Lock()
		n := len(g.cache[dir])
		g.mu.Unlock()
		if n < last {
			t.Fatalf("call %d: cache shrank from %d to %d", call, last, n)
		}
		last = n
		if err == nil {
			break
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("call %d: %v", call, err)
		}
		failed++
	}
	if err != nil {
		t.Fatalf("never finished; cache holds %d of %d files", last, files)
	}
	if failed == 0 {
		t.Skip("one pass finished inside the budget; nothing to prove on this machine")
	}
	g2, w := entrySet(got), statusSet(t, dir)
	if strings.Join(g2, "\n") != strings.Join(w, "\n") {
		t.Errorf("candidates differ from git status\n got: %q\nwant: %q", g2, w)
	}
}
