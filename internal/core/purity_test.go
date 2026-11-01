package core_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
		{"../timeline", full},
		{"../ui", full},                       // the web UI: Go files and the embedded web/ files
		{"../../cmd/gateway/viewer.go", full}, // the viewer commands; a single file, as run.go names a profile
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
		for _, word := range forbiddenIn(path, b, forbidden) {
			t.Errorf("%s contains %q; this package must not spell it", path, word)
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

// forbiddenIn returns the forbidden words that b, the contents of path, spells.
func forbiddenIn(path string, b []byte, forbidden []string) []string {
	lower := strings.ToLower(string(b))
	if isWebCSS(path) {
		lower = cssPropertyName.ReplaceAllString(lower, "$1$2")
	}
	var found []string
	for _, word := range forbidden {
		if strings.Contains(lower, word) {
			found = append(found, word)
		}
	}
	return found
}

// cssPropertyName matches the CSS property "cursor" where a declaration starts a line;
// forbiddenIn removes only that name, so the rest of the line is still checked. It is the
// one exemption from the client names, signed off by the user (specs/009-web-ui
// research.md D17): the word stays banned everywhere else, the same CSS file included.
var cssPropertyName = regexp.MustCompile(`(?m)^(\s*)cursor(\s*:)`)

// isWebCSS reports whether path is a stylesheet of the embedded web UI.
func isWebCSS(path string) bool {
	p := filepath.ToSlash(path)
	return strings.HasPrefix(p, "../ui/web/") && strings.HasSuffix(p, ".css")
}

// D17: the CSS property exemption is that narrow. Only a declaration of the property at
// the start of a line, in a stylesheet under ui/web/, passes; the word anywhere else fails.
func TestPurity_CursorExemptionIsNarrow(t *testing.T) {
	cases := []struct {
		name, path, body string
		pass             bool
	}{
		{"css property", "../ui/web/style.css", "button {\n  cursor: pointer;\n}\n", true},
		{"css property, indented with a tab and spaced", "../ui/web/style.css", "\tcursor : default;\n", true},
		{"css property, upper case", "../ui/web/style.css", "  CURSOR: pointer;\n", true},
		{"css property after other text on its line", "../ui/web/style.css", "button { cursor: pointer; }\n", false},
		{"css comment", "../ui/web/style.css", "/* cursor */\n", false},
		{"css comment after the property", "../ui/web/style.css", "  cursor: pointer; /* cursor */\n", false},
		{"css selector", "../ui/web/style.css", ".cursor { color: red; }\n", false},
		{"css value", "../ui/web/style.css", "  content: \"cursor\";\n", false},
		{"css outside ui/web", "../ui/style.css", "  cursor: pointer;\n", false},
		{"js string", "../ui/web/app.js", "const s = \"cursor\";\n", false},
		{"js property line", "../ui/web/app.js", "  cursor: 1,\n", false},
		{"html", "../ui/web/index.html", "<p>cursor</p>\n", false},
		{"go", "../ui/ui.go", "// cursor\n", false},
	}
	for _, c := range cases {
		got := forbiddenIn(c.path, []byte(c.body), names)
		if pass := len(got) == 0; pass != c.pass {
			t.Errorf("%s: forbidden words %v, want pass=%v", c.name, got, c.pass)
		}
	}
	// The exemption covers the client name only: other words on that line still fail.
	if got := forbiddenIn("../ui/web/style.css", []byte("  cursor: claude;\n"), names); len(got) != 1 || got[0] != "claude" {
		t.Errorf("other names on the property line: %v, want [claude]", got)
	}
}
