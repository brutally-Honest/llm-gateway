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
