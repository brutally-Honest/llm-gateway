package conventions

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// identifierRules are the patterns no fixture may contain (spec 001, AC25): each one
// names an API key, a bearer token, an org, account or workspace ID, or an email.
var identifierRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"anthropic api key", regexp.MustCompile(`sk-ant-`)},
	{"bearer token", regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)},
	{"uuid", regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)},
	{"email address", regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`)},
	{"workspace id", regexp.MustCompile(`wrkspc_`)},
	{"organization", regexp.MustCompile(`(?i)organization`)},
	{"account_uuid", regexp.MustCompile(`(?i)account_uuid`)},
	{"org_id", regexp.MustCompile(`(?i)org_id`)},
}

// TestFixtures_NoIdentifiers fails on any identifier in a file under a testdata
// directory of the module, naming the file, line and rule of each (spec 001, AC25).
func TestFixtures_NoIdentifiers(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	found, err := fixtureIdentifiers(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Errorf("%s: identifier in a test fixture; scrub it", f)
	}
}

// TestFixtureScanner_FlagsEachIdentifier proves the scanner on a seeded module: each
// identifier, in its own testdata file, is flagged by its rule; a clean fixture and
// files outside testdata are not.
func TestFixtureScanner_FlagsEachIdentifier(t *testing.T) {
	root := t.TempDir()
	seeded := map[string]string{
		"a/testdata/key.txt":           "x-api-key: sk-ant-api03-abcdef\n",
		"a/testdata/bearer.txt":        "Authorization: bEaReR abc.def-123\n",
		"a/testdata/uuid.json":         `{"id":"123E4567-e89b-12d3-a456-426614174000"}` + "\n",
		"a/testdata/email.txt":         "clean line\nfrom: someone@example.co.uk\n",
		"a/testdata/workspace.txt":     "anthropic-workspace: wrkspc_01abc\n",
		"a/testdata/org.headers":       "Anthropic-Organization-Id: scrubbed\n",
		"a/testdata/nested/acct.json":  `{"account_uuid":"x"}` + "\n",
		"b/testdata/orgid.json":        `{"ORG_ID":"x"}` + "\n",
		"a/testdata/clean.sse":         "event: ping\ndata: {\"type\":\"ping\"}\n\nBearer\n",
		"a/not_testdata/leak.txt":      "sk-ant-api03-abcdef\n",
		"a/leak_test.go":               "// someone@example.com\n",
		".hidden/testdata/leak.txt":    "sk-ant-api03-abcdef\n",
		"nested/go.mod":                "module example.com/nested\n",
		"nested/testdata/leak.txt":     "sk-ant-api03-abcdef\n",
		"go.mod":                       "module example.com/x\n",
		"a/testdata/second_line.txt":   "ok\nok\nwrkspc_x\n",
		"c/testdata/deeper/clean2.txt": "org_level_disabled\n",
	}
	for name, content := range seeded {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	found, err := fixtureIdentifiers(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"a/testdata/bearer.txt:1: bearer token",
		"a/testdata/email.txt:2: email address",
		"a/testdata/key.txt:1: anthropic api key",
		"a/testdata/nested/acct.json:1: account_uuid",
		"a/testdata/org.headers:1: organization",
		"a/testdata/second_line.txt:3: workspace id",
		"a/testdata/uuid.json:1: uuid",
		"a/testdata/workspace.txt:1: workspace id",
		"b/testdata/orgid.json:1: org_id",
	}
	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Errorf("found:\n%s\nwant:\n%s", strings.Join(found, "\n"), strings.Join(want, "\n"))
	}
}

// fixtureIdentifiers walks the module at root and returns "file:line: rule" (file
// relative to root, slash-separated, in walk order) for every identifierRules match in
// a file under a testdata directory. Like `go test ./...`, it skips directories
// starting with "." or "_" and nested modules.
func fixtureIdentifiers(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !underTestdata(rel) {
			return nil
		}
		hits, err := scanFile(path)
		if err != nil {
			return fmt.Errorf("scan %s: %w", rel, err)
		}
		for _, h := range hits {
			found = append(found, rel+":"+h)
		}
		return nil
	})
	return found, err
}

// underTestdata reports whether the slash-separated relative path has a testdata
// directory among its parents.
func underTestdata(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, p := range parts[:len(parts)-1] {
		if p == "testdata" {
			return true
		}
	}
	return false
}

// scanFile returns "line: rule" for each rule that matches each line of the file.
func scanFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var hits []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for line := 1; sc.Scan(); line++ {
		for _, r := range identifierRules {
			if r.re.Match(sc.Bytes()) {
				hits = append(hits, fmt.Sprintf("%d: %s", line, r.name))
			}
		}
	}
	return hits, sc.Err()
}
