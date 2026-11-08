package claudecode_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/clients/claudecode"
)

// AC6, AC7: the effect rules on real hook payload shapes. Read, Grep and Glob are read
// targets with a line range or a pattern, and Read's content comes out of its payload;
// Write, Edit, MultiEdit and NotebookEdit name their paths; the working folder comes from
// cwd; extra folders come from the settings files, and a missing one adds nothing.
func TestClaudeCode_EffectRules(t *testing.T) {
	p := claudecode.Profile{}
	fixture := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	reads := []struct {
		file, path, detail string
	}{
		{"post_read.json", "/work/app/main.go", "lines 10–29"},
		{"post_grep.json", "", "func main"},
		{"post_glob.json", "/work/lib", "**/*.go"},
	}
	for _, c := range reads {
		ev := p.MapHarness(fixture(c.file))
		if ev.WorkDir != "/work/app" {
			t.Errorf("%s: WorkDir = %q", c.file, ev.WorkDir)
		}
		path, detail, ok := p.ReadTarget(ev.ToolName, ev.ToolInput)
		if !ok || path != c.path || detail != c.detail {
			t.Errorf("%s: ReadTarget = %q %q %v, want %q %q", c.file, path, detail, ok, c.path, c.detail)
		}
		if p.WritePaths(ev.ToolName, ev.ToolInput) != nil {
			t.Errorf("%s: a read tool names write paths", c.file)
		}
	}
	if got, ok := p.ReadContent(fixture("post_read.json")); !ok || string(got) != "package main\n" {
		t.Errorf("Read content = %q %v", got, ok)
	}
	if got, ok := p.ReadContent(fixture("post_glob.json")); !ok || string(got) != "/work/lib/a.go\n/work/lib/b.go" {
		t.Errorf("Glob content = %q %v", got, ok)
	}

	writes := map[string]string{
		"post_write.json":        "/work/app/new.txt",
		"post_edit.json":         "/work/app/main.go",
		"post_multiedit.json":    "/work/app/main.go",
		"post_notebookedit.json": "/work/app/nb.ipynb",
	}
	for file, want := range writes {
		ev := p.MapHarness(fixture(file))
		if got := p.WritePaths(ev.ToolName, ev.ToolInput); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("%s: WritePaths = %v, want [%s]", file, got, want)
		}
		if _, _, ok := p.ReadTarget(ev.ToolName, ev.ToolInput); ok {
			t.Errorf("%s: a write tool is a read target", file)
		}
	}

	home, work := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	if got := p.ExtraDirs(work); len(got) != 0 {
		t.Errorf("no settings files: ExtraDirs = %v", got)
	}
	if err := os.MkdirAll(filepath.Join(work, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"permissions":{"additionalDirectories":["/srv/shared","../sibling"]}}`
	if err := os.WriteFile(filepath.Join(work, ".claude", "settings.local.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	want := []string{"/srv/shared", filepath.Join(filepath.Dir(work), "sibling")}
	if got := p.ExtraDirs(work); !reflect.DeepEqual(got, want) {
		t.Errorf("ExtraDirs = %v, want %v", got, want)
	}
}
