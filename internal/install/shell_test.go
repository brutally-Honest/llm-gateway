package install

import (
	"strings"
	"testing"
)

func TestParseRCFindsDefinitions(t *testing.T) {
	for _, src := range []string{
		"claude() {\n  x\n}\n",
		"claude () {\n  x\n}\n",
		"function claude {\n  x\n}\n",
		"claude()\n{\n  x\n}\n",
		"  claude() { command claude \"$@\"; }\n",
	} {
		f := parseRC("a=1\n"+src+"b=2\n", "claude")
		if len(f.defs) != 1 || f.text(f.defs[0]) != src {
			t.Fatalf("%q: defs %v", src, f.defs)
		}
	}
	if f := parseRC("claudette() {\n}\nalias claude=x\n", "claude"); len(f.defs) != 0 {
		t.Fatalf("false match %v", f.defs)
	}
}

func TestWithBlockReplacesAllDefinitionsOnce(t *testing.T) {
	block := markBegin + "\nNEW\n" + markEnd + "\n"
	// A block and a hand-written function both present: one definition after (AC16).
	src := "a=1\n" + block + "claude() {\n  old\n}\n"
	f := parseRC(src, "claude")
	out, replaced := f.withBlock(block, true)
	if out != "a=1\n"+block || replaced != "claude() {\n  old\n}\n" {
		t.Fatalf("out %q replaced %q", out, replaced)
	}
	// Appending to a file without a trailing blank line adds one, and uninstall drops it.
	f = parseRC("a=1\n", "claude")
	out, _ = f.withBlock(block, false)
	if out != "a=1\n\n"+block {
		t.Fatalf("append %q", out)
	}
	if back := parseRC(out, "claude").withoutBlock(""); back != "a=1\n" {
		t.Fatalf("remove %q", back)
	}
}

func TestUnifiedDiff(t *testing.T) {
	d := unifiedDiff("f", "a\nb\nc\n", "a\nB\nc\n")
	want := "--- f\n+++ f (new)\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"
	if d != want {
		t.Fatalf("diff:\n%s\nwant:\n%s", d, want)
	}
	if unifiedDiff("f", "x\n", "x\n") != "" {
		t.Fatal("diff of equal files")
	}
	long := strings.Repeat("l\n", 20)
	d = unifiedDiff("f", "a\n"+long+"z\n", "A\n"+long+"Z\n")
	if strings.Count(d, "@@ -") != 2 {
		t.Fatalf("want two hunks:\n%s", d)
	}
}
