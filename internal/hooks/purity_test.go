package hooks_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPurity_HooksPackageImportsNoClient holds the package's non-test files to
// importing no client profile. Event names are checked by core's purity test.
func TestPurity_HooksPackageImportsNoClient(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		if strings.Contains(string(b), "internal/clients/") {
			t.Errorf("%s imports a client package", f)
		}
	}
	if checked == 0 {
		t.Fatal("no files checked")
	}
}
