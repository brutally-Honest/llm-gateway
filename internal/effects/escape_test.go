package effects

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

const secretMark = "TOP-SECRET-OUTSIDE-CONTENT"

// outsideDir is a folder beside the watched root, holding id_key with the secret.
func outsideDir(t *testing.T) string {
	t.Helper()
	dir := resolve(t.TempDir())
	write(t, filepath.Join(dir, "id_key"), secretMark+"\n")
	return dir
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
}

// noSecretStored fails when any stored effect copy, or any byte under the store folder
// (database, journal, blobs), holds the secret.
func noSecretStored(t *testing.T, ts testStore) {
	t.Helper()
	for _, e := range ts.effects(t) {
		for _, h := range []string{e.BeforeHash, e.AfterHash} {
			if h != "" && bytes.Contains([]byte(ts.content(t, h)), []byte(secretMark)) {
				t.Errorf("effect %s %s stores outside content (%s)", e.Change, e.Path, h)
			}
		}
	}
	err := filepath.WalkDir(ts.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr == nil && bytes.Contains(b, []byte(secretMark)) {
			t.Errorf("store file %s holds outside content", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// notModifiedOrCreated fails when path was reported as anything but deleted.
func notModifiedOrCreated(t *testing.T, ts testStore, path string) {
	t.Helper()
	for _, e := range ts.effects(t) {
		if e.Path == path && e.Change != "deleted" {
			t.Errorf("%s reported %s, want deleted or nothing", path, e.Change)
		}
	}
}

// modes runs fn on a root that is a git repo, then on a plain folder.
func modes(t *testing.T, fn func(t *testing.T, dir string, commit func())) {
	t.Run("git", func(t *testing.T) {
		dir := repo(t)
		fn(t, dir, func() {
			git(t, dir, "add", "-A")
			git(t, dir, "commit", "-q", "-m", "c")
		})
	})
	t.Run("scan", func(t *testing.T) {
		fn(t, resolve(t.TempDir()), func() {})
	})
}

// A tracked symlink to an outside file is never copied, and neither is a regular file
// replaced by one.
func TestEscape_FinalSymlinkToOutsideFile(t *testing.T) {
	modes(t, func(t *testing.T, dir string, commit func()) {
		out := outsideDir(t)
		secret := filepath.Join(out, "id_key")
		symlink(t, secret, filepath.Join(dir, "link"))
		write(t, filepath.Join(dir, "plain.txt"), "plain\n")
		commit()
		ts := openStore(t)
		r := newTestRunner(t, ts.st)
		checkpointIn(t, r, dir)

		write(t, secret, secretMark+" changed\n")
		if err := os.Remove(filepath.Join(dir, "plain.txt")); err != nil {
			t.Fatal(err)
		}
		symlink(t, secret, filepath.Join(dir, "plain.txt"))
		checkpointIn(t, r, dir)
		noSecretStored(t, ts)
		notModifiedOrCreated(t, ts, filepath.Join(dir, "plain.txt"))
		notModifiedOrCreated(t, ts, filepath.Join(dir, "link"))
	})
}

// A folder replaced by a symlink to an outside folder: its file is not read through it.
func TestEscape_SymlinkedFolderToOutside(t *testing.T) {
	modes(t, func(t *testing.T, dir string, commit func()) {
		out := outsideDir(t)
		write(t, filepath.Join(dir, "d", "id_key"), "mine\n")
		commit()
		ts := openStore(t)
		r := newTestRunner(t, ts.st)
		checkpointIn(t, r, dir)

		if err := os.RemoveAll(filepath.Join(dir, "d")); err != nil {
			t.Fatal(err)
		}
		symlink(t, out, filepath.Join(dir, "d"))
		checkpointIn(t, r, dir)
		noSecretStored(t, ts)
		notModifiedOrCreated(t, ts, filepath.Join(dir, "d", "id_key"))
	})
}

// A watched root swapped for a symlink to an outside folder is gone, not followed: the
// outside file of the same name is never stored.
func TestEscape_RootSwappedForSymlink(t *testing.T) {
	modes(t, func(t *testing.T, dir string, commit func()) {
		out := outsideDir(t)
		write(t, filepath.Join(out, "keep.txt"), secretMark+" same name\n")
		write(t, filepath.Join(dir, "keep.txt"), "mine\n")
		write(t, filepath.Join(dir, "id_key"), "mine\n")
		commit()
		ts := openStore(t)
		r := newTestRunner(t, ts.st)
		checkpointIn(t, r, dir)

		moved := dir + ".moved"
		if err := os.Rename(dir, moved); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(moved) })
		symlink(t, out, dir)
		checkpointIn(t, r, dir)
		noSecretStored(t, ts)
		notModifiedOrCreated(t, ts, filepath.Join(dir, "keep.txt"))
		notModifiedOrCreated(t, ts, filepath.Join(dir, "id_key"))
	})
}

// A PARENT of the watched root swapped for a symlink (`mv base/x base/y; ln -s out
// base/x`) is gone too: the same-named child root outside, with a saved row, is never read.
func TestEscape_AncestorSwappedForSymlink(t *testing.T) {
	for _, mode := range []string{"git", "scan"} {
		t.Run(mode, func(t *testing.T) {
			setup := func(root string) {
				write(t, filepath.Join(root, "keep.txt"), "mine\n")
				write(t, filepath.Join(root, "id_key"), "mine\n")
				if mode == "git" {
					needGit(t)
					git(t, root, "init", "-q")
					git(t, root, "add", "-A")
					git(t, root, "commit", "-q", "-m", "c")
				}
			}
			base := resolve(t.TempDir())
			dir := filepath.Join(base, "x", "proj")
			setup(dir)
			out := resolve(t.TempDir())
			setup(filepath.Join(out, "proj"))
			ts := openStore(t)
			r := newTestRunner(t, ts.st)
			checkpointIn(t, r, dir)

			// Outside has the secret in a file the saved rows know, changed after the baseline.
			if err := os.Rename(filepath.Join(base, "x"), filepath.Join(base, "y")); err != nil {
				t.Fatal(err)
			}
			symlink(t, out, filepath.Join(base, "x"))
			write(t, filepath.Join(out, "proj", "keep.txt"), secretMark+" changed\n")
			write(t, filepath.Join(out, "proj", "new.txt"), secretMark+" new\n")
			checkpointIn(t, r, dir)
			noSecretStored(t, ts)
			for _, p := range []string{"keep.txt", "id_key", "new.txt"} {
				notModifiedOrCreated(t, ts, filepath.Join(dir, p))
			}
		})
	}
}

// A chain a -> b -> outside, both links inside the root, leads outside all the same.
func TestEscape_SymlinkChain(t *testing.T) {
	modes(t, func(t *testing.T, dir string, commit func()) {
		out := outsideDir(t)
		write(t, filepath.Join(dir, "a", "id_key"), "mine\n")
		commit()
		ts := openStore(t)
		r := newTestRunner(t, ts.st)
		checkpointIn(t, r, dir)

		if err := os.RemoveAll(filepath.Join(dir, "a")); err != nil {
			t.Fatal(err)
		}
		symlink(t, out, filepath.Join(dir, "b"))
		symlink(t, "b", filepath.Join(dir, "a"))
		checkpointIn(t, r, dir)
		noSecretStored(t, ts)
		notModifiedOrCreated(t, ts, filepath.Join(dir, "a", "id_key"))
	})
}

// A link that stays inside the root still reaches a regular file: d -> real in scan
// mode keeps real/f.txt's row, and in git mode d/f.txt (listed changed, research D20)
// is read through the link as before.
func TestEscape_SymlinkInsideRootStillWorks(t *testing.T) {
	modes(t, func(t *testing.T, dir string, commit func()) {
		write(t, filepath.Join(dir, "real", "f.txt"), "v1\n")
		write(t, filepath.Join(dir, "d", "f.txt"), "v1\n")
		commit()
		ts := openStore(t)
		r := newTestRunner(t, ts.st)
		checkpointIn(t, r, dir)

		if err := os.RemoveAll(filepath.Join(dir, "d")); err != nil {
			t.Fatal(err)
		}
		symlink(t, "real", filepath.Join(dir, "d"))
		write(t, filepath.Join(dir, "real", "f.txt"), "v2 longer\n")
		checkpointIn(t, r, dir)
		got := byPath(t, ts.effects(t))
		e, ok := got[filepath.Join(dir, "real", "f.txt")]
		if !ok || e.Change != "modified" || ts.content(t, e.AfterHash) != "v2 longer\n" {
			t.Fatalf("real/f.txt = %+v (found %v), want modified with the new content", e, ok)
		}
		if d, ok := got[filepath.Join(dir, "d", "f.txt")]; ok {
			t.Logf("d/f.txt = %+v", d)
			if d.Change == "modified" && ts.content(t, d.AfterHash) != "v2 longer\n" {
				t.Errorf("d/f.txt content is not the in-root target's")
			}
		}
	})
}
