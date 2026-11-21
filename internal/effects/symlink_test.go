package effects

import (
	"os"
	"path/filepath"
	"testing"
)

// linkTo is a symlink to dir in a fresh temp folder, the way a user may reach a repo
// (macOS /var → /private/var, a ~/code link to another disk).
func linkTo(t *testing.T, dir string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
	return link
}

// B1 (spec 024 AC5): a session whose folder is reached through a symlink still gets its
// git changes. git reports the resolved top level, so before the fix every candidate was
// outside the unresolved root and the changes were dropped silently.
func TestCheckpoint_SymlinkedRepoPath(t *testing.T) {
	real := repo(t)
	write(t, filepath.Join(real, "a.txt"), "one\n")
	git(t, real, "add", ".")
	git(t, real, "commit", "-q", "-m", "init")
	link := linkTo(t, real)
	ts := openStore(t)
	r := newTestRunner(t, ts.st)

	checkpointIn(t, r, link)
	write(t, filepath.Join(real, "a.txt"), "one\ntwo\n")
	write(t, filepath.Join(real, "c.txt"), "new\n")
	checkpointIn(t, r, link)
	got := byPath(t, ts.effects(t))
	if len(got) != 2 {
		t.Fatalf("effects = %+v, want a.txt modified and c.txt created", got)
	}
	if got[filepath.Join(real, "a.txt")].Change != "modified" || got[filepath.Join(real, "c.txt")].Change != "created" {
		t.Fatalf("effects = %+v, want them under the resolved folder %s", got, real)
	}
}

// B1: a folder learned under its resolved spelling while the session started under a
// symlink to it is one watched folder, not two.
func TestRoots_SymlinkedFolderWatchedOnce(t *testing.T) {
	real := repo(t)
	write(t, filepath.Join(real, "lib", "x.go"), "package x\n")
	link := linkTo(t, real)
	ts := openStore(t)
	r := newTestRunner(t, ts.st)
	r.mu.Lock()
	s := &session{ident: testSession, tops: map[string]string{}, workDir: link,
		learn: []learned{
			{path: filepath.Join(real, "lib", "x.go")}, // the resolved spelling
			{path: filepath.Join(link, "lib", "x.go")}, // the link's spelling
		}}
	r.sessions[testSession] = s
	r.mu.Unlock()
	if err := r.runCheckpoint(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	if got := s.roots.list(); len(got) != 1 || got[0] != real {
		t.Fatalf("roots = %v, want only %s", got, real)
	}
}
