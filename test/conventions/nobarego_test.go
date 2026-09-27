// Package conventions holds repo-shape tests: checks over the source tree itself that
// turn a spec's Don't into a failing test. It holds tests only.
package conventions

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goHelper is the one file allowed a go statement: the recovering helper (spec 001,
// Don't "no bare go").
const goHelper = "internal/logging/go.go"

// TestNoBareGoStatements fails on a go statement in any non-test Go file of the module
// outside the recovering helper, naming the file and line of each.
func TestNoBareGoStatements(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	found, err := bareGoStatements(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, pos := range found {
		t.Errorf("%s: bare go statement; start goroutines with logging.Go", pos)
	}
}

// TestBareGoScanner_FlagsSeededFile proves the scanner on a seeded module: a go
// statement in a non-test file is flagged with its line, while the helper's own file,
// test files and the directories the go tool ignores are not.
func TestBareGoScanner_FlagsSeededFile(t *testing.T) {
	root := t.TempDir()
	bare := "package x\n\nfunc f() {\n\tgo func() {}()\n}\n"
	for name, content := range map[string]string{
		"go.mod":                   "module example.com/x\n",
		"pkg/bad.go":               bare,
		"pkg/clean.go":             "package x\n\nfunc g() {}\n",
		"pkg/bad_test.go":          bare,
		goHelper:                   bare,
		"pkg/testdata/bad.go":      bare,
		".hidden/bad.go":           bare,
		"_ignored/bad.go":          bare,
		"nested/go.mod":            "module example.com/nested\n",
		"nested/bad.go":            bare,
		"pkg/deeper/also.go":       "package deeper\n\nfunc h() { go h() }\n",
		"pkg/deeper/also_test.go":  "package deeper\n",
		"pkg/deeper/not_go_file.c": "go f();\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	found, err := bareGoStatements(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"pkg/bad.go:4", "pkg/deeper/also.go:3"}
	if strings.Join(found, ",") != strings.Join(want, ",") {
		t.Errorf("found %q, want %q", found, want)
	}
}

// bareGoStatements walks the module at root and returns "file:line" (file relative to
// root, slash-separated, in walk order) for every go statement in a non-test Go file
// other than goHelper. It skips what `go test ./...` skips: directories starting with
// "." or "_", testdata, and nested modules.
func bareGoStatements(root string) ([]string, error) {
	var found []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" {
				return filepath.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") || rel == goHelper {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if g, ok := n.(*ast.GoStmt); ok {
				found = append(found, fmt.Sprintf("%s:%d", rel, fset.Position(g.Go).Line))
			}
			return true
		})
		return nil
	})
	return found, err
}
