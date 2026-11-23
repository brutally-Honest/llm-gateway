package effects

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// v4Entry is one version 4 entry (sha1) with the given prefix-strip byte and path suffix.
func v4Entry(strip byte, suffix string) []byte {
	e := make([]byte, 40+20+2)
	e[26], e[27] = 0x81, 0xa4 // mode 0100644
	e = append(e, strip)
	e = append(e, suffix...)
	return append(e, 0)
}

func v4Header(version, count uint32) []byte {
	h := make([]byte, 12)
	copy(h, "DIRC")
	binary.BigEndian.PutUint32(h[4:], version)
	binary.BigEndian.PutUint32(h[8:], count)
	return h
}

// A path over 4096 bytes, and a crafted version 4 index that rebuilds one long path for
// many entries, are refused as unsupported (the ls-files fallback answers) and do not
// allocate the expanded paths.
func TestReadIndex_OversizedPathsRefused(t *testing.T) {
	// One 4000-byte path, then many strip-0 entries that each keep all of it: 1000
	// entries would rebuild 4 MB of paths from a file of about 70 KB.
	crafted := append(v4Header(4, 1001), v4Entry(0, strings.Repeat("p", 4000))...)
	for i := 0; i < 1000; i++ {
		crafted = append(crafted, v4Entry(0, "a")...)
	}
	if _, err := readIndex(crafted, 20); !errors.Is(err, errIndexUnsupported) {
		t.Fatalf("crafted v4 index: %v, want unsupported", err)
	}
	// Measure what the refused read allocates: a bounded buffer plus the first path,
	// far below the 4 MB it would take to rebuild every path.
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, _ = readIndex(crafted, 20)
	runtime.ReadMemStats(&after)
	if got := after.TotalAlloc - before.TotalAlloc; got > 1<<20 {
		t.Errorf("refused read allocated %d bytes", got)
	}

	long := strings.Repeat("q", maxIndexPath+1)
	v4 := append(v4Header(4, 1), v4Entry(0, long)...)
	if _, err := readIndex(v4, 20); !errors.Is(err, errIndexUnsupported) {
		t.Fatalf("v4 path of %d bytes: %v, want unsupported", len(long), err)
	}
	v2 := append(v4Header(2, 1), make([]byte, 40+20+2)...)
	v2 = append(v2, long...)
	v2 = append(v2, 0)
	for len(v2)%8 != 0 {
		v2 = append(v2, 0)
	}
	if _, err := readIndex(v2, 20); !errors.Is(err, errIndexUnsupported) {
		t.Fatalf("v2 path of %d bytes: %v, want unsupported", len(long), err)
	}
	// The limit itself is accepted.
	ok := append(v4Header(2, 1), make([]byte, 40+20+2)...)
	ok = append(ok, long[1:]...)
	ok = append(ok, 0)
	for (len(ok)-12)%8 != 0 { // entries are padded relative to their start
		ok = append(ok, 0)
	}
	if es, err := readIndex(ok, 20); err != nil || len(es) != 1 || len(es[0].path) != maxIndexPath {
		t.Fatalf("path of %d bytes: %v, %v", maxIndexPath, es, err)
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
