package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// /dev/null is a character device but no terminal: questions read from it would only
// see end of file, so the installer must say there is no terminal instead (D14; found
// on the real-machine check, where the Bash tool's stdin is /dev/null).
func TestIsTerminalExcludesDevNull(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	if isTerminal(null) {
		t.Fatal("/dev/null taken for a terminal")
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "answers"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if isTerminal(file) || isTerminal(strings.NewReader("y\n")) {
		t.Fatal("a file or a reader taken for a terminal")
	}
}

// B13 (spec 024 AC9): with neither lsof nor ss to ask, a busy port's owner is unknown,
// never "another process", which made the running gateway's own ports read as foreign.
func TestPortHolderUnknownWithoutTools(t *testing.T) {
	r := &fakeRunner{}
	e := &Env{GOOS: "darwin", Run: r, LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
	if got := portHolder(e, 7197); !strings.Contains(got, "unknown") || !strings.Contains(got, "lsof") {
		t.Fatalf("holder = %q, want an unknown owner naming the missing lsof", got)
	}
	if len(r.outputs) != 0 {
		t.Fatalf("ran %v with no tool on PATH", r.outputs)
	}
	e.GOOS = "linux"
	if got := portHolder(e, 7197); !strings.Contains(got, "unknown") || !strings.Contains(got, "ss") {
		t.Fatalf("linux holder = %q, want an unknown owner naming the missing ss", got)
	}
}

// B13: preflight never names "another process"; a port whose owner was not found reads
// as held by an unknown process.
func TestPortsUnknownHolderWording(t *testing.T) {
	f := newFake(t, "linux")
	f.busy[7197] = ""
	f.install(InstallOptions{Check: true})
	contains(t, f.out.String(), "7197 held by an unknown process")
	lacks(t, f.out.String(), "another process")
}
