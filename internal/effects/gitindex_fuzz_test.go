package effects

import (
	"encoding/binary"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fuzzGit runs git in dir with a hermetic environment; ok is false when git fails or is
// missing, and the caller skips that seed.
func fuzzGit(dir string, args ...string) bool {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	return cmd.Run() == nil
}

// fuzzRepoIndex builds a repository in a fresh temp dir, runs the setup steps and
// returns its .git/index; nil when any step fails (old git, no git).
func fuzzRepoIndex(f *testing.F, objectFormat string, steps ...[]string) []byte {
	dir := f.TempDir()
	init := []string{"init", "-q"}
	if objectFormat != "" {
		init = append(init, "--object-format="+objectFormat)
	}
	if !fuzzGit(dir, init...) {
		return nil
	}
	for _, p := range []string{"a.txt", "dir/b.txt", "dir/sub/c.txt", "dir/sub/d.txt", "z.txt"} {
		full := filepath.Join(dir, p)
		if os.MkdirAll(filepath.Dir(full), 0o755) != nil || os.WriteFile(full, []byte(p+"\n"), 0o644) != nil {
			return nil
		}
	}
	if !fuzzGit(dir, "add", "a.txt", "dir", "z.txt") {
		return nil
	}
	for _, s := range steps {
		if !fuzzGit(dir, s...) {
			return nil
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		return nil
	}
	return data
}

func fuzzHeader(version, count uint32) []byte {
	h := []byte("DIRC\x00\x00\x00\x00\x00\x00\x00\x00")
	binary.BigEndian.PutUint32(h[4:], version)
	binary.BigEndian.PutUint32(h[8:], count)
	return h
}

// fuzzV4Entry is one version 4 entry with the given prefix-strip bytes and path suffix.
func fuzzV4Entry(hashLen int, strip []byte, suffix string) []byte {
	e := make([]byte, 40+hashLen+2)
	e[24+2] = 0x81 // mode 0100644
	e[24+3] = 0xa4
	e = append(e, strip...)
	e = append(e, suffix...)
	return append(e, 0)
}

func FuzzReadIndex(f *testing.F) {
	var real [][]byte
	add := func(d []byte) {
		if d != nil {
			real = append(real, d)
		}
	}
	add(fuzzRepoIndex(f, ""))
	add(fuzzRepoIndex(f, "", []string{"update-index", "--skip-worktree", "dir/b.txt"}))
	add(fuzzRepoIndex(f, "", []string{"update-index", "--index-version", "3"}, []string{"update-index", "--assume-unchanged", "a.txt"}))
	add(fuzzRepoIndex(f, "", []string{"update-index", "--index-version", "4"}))
	add(fuzzRepoIndex(f, "sha256"))
	add(fuzzRepoIndex(f, "sha256", []string{"update-index", "--index-version", "4"}))
	add(fuzzRepoIndex(f, "", []string{"update-index", "--split-index"}))
	add(fuzzRepoIndex(f, "", []string{"sparse-checkout", "init", "--cone", "--sparse-index"}, []string{"sparse-checkout", "set", "dir/sub"}))
	for _, d := range real {
		f.Add(d)
		for _, cut := range []int{12, 13, 40, len(d) / 4, len(d) / 2, len(d) - 21, len(d) - 20, len(d) - 1} {
			if cut >= 0 && cut < len(d) {
				f.Add(d[:cut])
			}
		}
		big := append([]byte{}, d...)
		if len(big) >= 12 {
			binary.BigEndian.PutUint32(big[8:12], 0xffffffff)
			f.Add(big)
		}
	}
	f.Add([]byte{})
	f.Add(fuzzHeader(2, 0))
	f.Add(fuzzHeader(4, 0))
	f.Add(fuzzHeader(2, 0xffffffff))
	f.Add(fuzzHeader(4, 0xffffffff))
	rnd := rand.New(rand.NewSource(1))
	for _, n := range []int{16, 64, 300} {
		b := make([]byte, n)
		rnd.Read(b)
		f.Add(b)
		copy(b, fuzzHeader(4, 3))
		f.Add(b)
	}
	// v4 entries whose prefix-strip varint is absurd, and one that is valid.
	for _, strip := range [][]byte{
		{0x7f},
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		{0x80},
	} {
		d := append(fuzzHeader(4, 2), fuzzV4Entry(20, []byte{0}, "abc")...)
		d = append(d, fuzzV4Entry(20, strip, "x")...)
		f.Add(d)
	}
	// One long path followed by many entries that keep all of it (strip 0): the paths add
	// up to far more than the file, which the reader must refuse.
	long := fuzzHeader(4, 401)
	long = append(long, fuzzV4Entry(20, []byte{0}, strings.Repeat("p", 8000))...)
	for i := 0; i < 400; i++ {
		long = append(long, fuzzV4Entry(20, []byte{0}, "a")...)
	}
	f.Add(long)

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, hashLen := range []int{20, 32} {
			es, err := readIndex(data, hashLen)
			if err != nil {
				if es != nil {
					t.Fatalf("hashLen %d: entries returned with error %v", hashLen, err)
				}
				continue
			}
			// Every entry takes at least 40+hashLen+3 bytes of input, so a count above
			// that bound means the reader trusted the header.
			if max := len(data) / (40 + hashLen + 3); len(es) > max {
				t.Fatalf("hashLen %d: %d entries from %d bytes", hashLen, len(es), len(data))
			}
			total := 0
			for _, e := range es {
				if e.path == "" || len(e.oid) != 2*hashLen {
					t.Fatalf("hashLen %d: bad entry %+v", hashLen, e)
				}
				if len(e.path) > maxIndexPath {
					t.Fatalf("hashLen %d: path of %d bytes", hashLen, len(e.path))
				}
				total += len(e.path)
			}
			// Version 4 paths are rebuilt from the previous one; their total size must
			// stay within the budget, or a small file expands quadratically. Version 2
			// and 3 paths are slices of the input, so len(data) bounds them.
			if limit := max(indexPathBudget(len(data)), uint64(len(data))); uint64(total) > limit {
				t.Fatalf("hashLen %d: %d path bytes from %d input bytes", hashLen, total, len(data))
			}
		}
	})
}
