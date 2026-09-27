package core_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// forbidden names a provider, a client or a provider's auth header. Core holds no `if`
// on any of them: everything provider- or client-shaped is a value an adapter or a
// profile hands to core. This file is the one place in the package allowed to spell
// them.
var forbidden = []string{"anthropic", "claude", "openai", "cursor", "x-api-key"}

const purityFile = "purity_test.go"

// TestCore_NoProviderOrClientIdentifiers walks every file under internal/core (the
// test's working directory), not only .go files, so testdata cannot name a provider
// either.
func TestCore_NoProviderOrClientIdentifiers(t *testing.T) {
	checked := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path == purityFile {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		lower := strings.ToLower(string(b))
		for _, word := range forbidden {
			if strings.Contains(lower, word) {
				t.Errorf("%s contains %q; core names no provider or client", path, word)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/core: %v", err)
	}
	if checked == 0 {
		t.Fatal("no files checked; the walk is not looking at internal/core")
	}
}
