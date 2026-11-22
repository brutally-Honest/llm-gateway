package effects

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The reader returns an error, never a panic, for every truncation and for flipped bytes
// of a real index.
func TestReadIndex_CorruptNeverPanics(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	write(t, filepath.Join(dir, "sub", "deeper", "b.txt"), "two\n")
	git(t, dir, "add", ".")
	data, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if es, err := readIndex(data, 20); err != nil || len(es) != 2 {
		t.Fatalf("good index: %v, %v", es, err)
	}
	for n := 0; n < len(data); n++ {
		_, _ = readIndex(data[:n], 20) // must not panic
	}
	if _, err := readIndex(data[:len(data)-30], 20); err == nil {
		t.Fatal("truncated index read without error")
	}
	for i := 0; i < len(data); i++ {
		c := append([]byte{}, data...)
		c[i] ^= 0xff
		_, _ = readIndex(c, 20)
	}
	big := append([]byte{}, data...)
	binary.BigEndian.PutUint32(big[8:12], 0xffffffff)
	if _, err := readIndex(big, 20); err == nil {
		t.Fatal("absurd entry count read without error")
	}
	if _, err := readIndex([]byte("nope"), 20); err == nil {
		t.Fatal("bad header read without error")
	}
	bad := append([]byte{}, data...)
	binary.BigEndian.PutUint32(bad[4:8], 9)
	if _, err := readIndex(bad, 20); !errors.Is(err, errIndexUnsupported) {
		t.Fatalf("unknown version: %v, want unsupported", err)
	}
}

// Paths from a version 4 index (prefix compression) match git's own listing.
func TestReadIndex_Version4Paths(t *testing.T) {
	dir := repo(t)
	for _, p := range []string{"a/b/c.txt", "a/b/d.txt", "a/e.txt", "f.txt", "a/b/c/long-name-here.txt"} {
		write(t, filepath.Join(dir, p), p)
	}
	git(t, dir, "add", ".")
	git(t, dir, "update-index", "--index-version", "4")
	data, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	es, err := readIndex(data, 20)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", dir, "ls-files").Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields(string(out))
	if len(es) != len(want) {
		t.Fatalf("%d entries, want %d", len(es), len(want))
	}
	for i, e := range es {
		if e.path != want[i] {
			t.Errorf("entry %d is %q, want %q", i, e.path, want[i])
		}
	}
}

// A split index is reported as unsupported, so candidates falls back.
func TestReadIndex_SplitIndexUnsupported(t *testing.T) {
	dir := repo(t)
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	git(t, dir, "add", ".")
	git(t, dir, "update-index", "--split-index")
	data, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readIndex(data, 20); !errors.Is(err, errIndexUnsupported) {
		t.Fatalf("split index: %v, want unsupported", err)
	}
}
