package core_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// names are the providers and clients. Nothing outside their own adapter or profile
// package holds an `if` on any of them: everything provider- or client-shaped is a
// value an adapter or a profile hands over.
var names = []string{"anthropic", "claude", "openai", "cursor"}

// coreOnly are words core must not spell but the capture packages may: a provider's
// auth header, and the Anthropic wire names (block, delta and event types, and the
// adapter's hash-excluded field). Canonical names are never on this list. "tool_use"
// also covers "tool_use_id".
var coreOnly = []string{
	"x-api-key",
	"cache_control",
	"tool_use",
	"tool_use_id",
	"content_block",
	"message_delta",
	"input_json_delta",
	"thinking_delta",
	"text_delta",
	"signature_delta",
	"citations_delta",
	"message_start",
	"message_stop",
	"redacted_thinking",
}

const purityFile = "purity_test.go"

// TestCore_NoProviderOrClientIdentifiers walks every file under internal/core (the
// test's working directory), not only .go files, so testdata cannot name a provider
// either. It holds core to the full list, and the capture packages to the provider and
// client names only. This file is the one place allowed to spell them.
func TestCore_NoProviderOrClientIdentifiers(t *testing.T) {
	full := append(append([]string{}, names...), coreOnly...)
	dirs := []struct {
		dir       string
		forbidden []string
	}{
		{".", full},
		{"../capture", names},
		{"../store", names},
		{"../contentcoding", names},
	}
	for _, d := range dirs {
		checkDir(t, d.dir, d.forbidden)
	}
}

// checkDir fails the test for every file under dir that contains a forbidden word, and
// when dir holds no checked file at all.
func checkDir(t *testing.T, dir string, forbidden []string) {
	t.Helper()
	self := filepath.Join(".", purityFile)
	checked := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path == self {
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
				t.Errorf("%s contains %q; this package must not spell it", path, word)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
	if checked == 0 {
		t.Fatalf("no files checked under %s; the walk is not looking at it", dir)
	}
}
