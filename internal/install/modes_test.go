package install

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
)

// perm is the permission bits of path, independent of the umask.
func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func expectPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if got := perm(t, path); got != want {
		t.Fatalf("%s mode %o, want %o", path, got, want)
	}
}

// The config folder is 0700 and config.yaml 0600 after a fresh install.
func TestFreshInstallConfigPrivate(t *testing.T) {
	f := newFake(t, "linux")
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	p := f.paths()
	expectPerm(t, filepath.Dir(p.Config), 0o700)
	expectPerm(t, p.Config, 0o600)
}

// A reinstall tightens a looser config folder and config.yaml, content unchanged, and
// the summary says updated.
func TestReinstallTightensLooseConfigModes(t *testing.T) {
	f := newFake(t, "linux")
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("first exit %d\n%s", code, f.out)
	}
	p := f.paths()
	before := f.read(".config/llm-gateway/config.yaml")
	if err := os.Chmod(filepath.Dir(p.Config), 0o775); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.Config, 0o664); err != nil {
		t.Fatal(err)
	}
	ino := inode(t, p.Config)
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("reinstall exit %d\n%s", code, f.out)
	}
	expectPerm(t, filepath.Dir(p.Config), 0o700)
	expectPerm(t, p.Config, 0o600)
	if f.read(".config/llm-gateway/config.yaml") != before {
		t.Fatal("config content changed")
	}
	if ino != 0 && inode(t, p.Config) != ino {
		t.Fatal("config was rewritten (inode changed); a reinstall must only chmod it")
	}
	if !regexp.MustCompile(`(?m)config\s+updated\s+\S*config\.yaml$`).MatchString(f.out.String()) {
		t.Fatalf("summary does not list config.yaml as updated:\n%s", f.out)
	}
}

// inode is the inode number of path, or 0 when the platform's Sys() is not a *syscall.Stat_t.
func inode(t *testing.T, path string) uint64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return sys.Ino
	}
	return 0
}

// A reinstall over a symlinked config keeps the link and the content, and tightens the
// target it points to instead of replacing the link with a regular file.
func TestReinstallSymlinkedConfigTightensTarget(t *testing.T) {
	f := newFake(t, "linux")
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("first exit %d\n%s", code, f.out)
	}
	p := f.paths()
	before := f.read(".config/llm-gateway/config.yaml")
	target := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(target, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p.Config); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p.Config); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(p.Config), 0o775); err != nil {
		t.Fatal(err)
	}
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("reinstall exit %d\n%s", code, f.out)
	}
	st, err := os.Lstat(p.Config)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("config.yaml is no longer a symlink")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != before {
		t.Fatal("symlink target content changed")
	}
	expectPerm(t, target, 0o600)
	expectPerm(t, filepath.Dir(p.Config), 0o700)
	if !regexp.MustCompile(`(?m)config\s+updated\s+\S*config\.yaml$`).MatchString(f.out.String()) {
		t.Fatalf("summary does not list config.yaml as updated:\n%s", f.out)
	}
}

// A reinstall over private modes and unchanged content reports unchanged.
func TestReinstallPrivateModesUnchanged(t *testing.T) {
	f := newFake(t, "linux")
	f.install(InstallOptions{Yes: true})
	p := f.paths()
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	expectPerm(t, filepath.Dir(p.Config), 0o700)
	expectPerm(t, p.Config, 0o600)
	if !regexp.MustCompile(`(?m)config\s+unchanged\s+`).MatchString(f.out.String()) {
		t.Fatalf("summary does not list config.yaml as unchanged:\n%s", f.out)
	}
}

// A chmod that fails on reinstall (here EPERM, as for a symlink to another user's file)
// prints a warning with the manual command and does not stop the install.
func TestReinstallChmodFailureWarnsAndSucceeds(t *testing.T) {
	f := newFake(t, "linux")
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("first exit %d\n%s", code, f.out)
	}
	p := f.paths()
	if err := os.Chmod(p.Config, 0o644); err != nil {
		t.Fatal(err)
	}
	orig := chmod
	chmod = func(name string, mode os.FileMode) error {
		return &os.PathError{Op: "chmod", Path: name, Err: syscall.EPERM}
	}
	t.Cleanup(func() { chmod = orig })
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("reinstall exit %d, want ExitOK when only a chmod fails\n%s", code, f.out)
	}
	if !strings.Contains(f.out.String(), "could not make") {
		t.Fatalf("no chmod warning in output:\n%s", f.out)
	}
	if !strings.Contains(f.out.String(), "run: chmod 600 ") {
		t.Fatalf("warning lacks the manual chmod command:\n%s", f.out)
	}
}

// Files install does not tighten keep their modes: a shell rc it edits and the settings
// file in the config folder.
func TestOtherFilesKeepTheirModes(t *testing.T) {
	f := newFake(t, "linux")
	f.write(".zshrc", "export A=1\n")
	if err := os.Chmod(filepath.Join(f.env.Home, ".zshrc"), 0o664); err != nil {
		t.Fatal(err)
	}
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	expectPerm(t, filepath.Join(f.env.Home, ".zshrc"), 0o664)

	p := f.paths()
	if err := os.Chmod(p.Settings, 0o664); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(p.Config), 0o775); err != nil {
		t.Fatal(err)
	}
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("reinstall exit %d\n%s", code, f.out)
	}
	expectPerm(t, p.Settings, 0o664)
	expectPerm(t, filepath.Dir(p.Config), 0o700)
}
