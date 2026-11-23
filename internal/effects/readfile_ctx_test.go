package effects

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A huge file must not be read to the end once the context is done: readFile returns the
// context error quickly, so the checkpoint can stop and retry the file next time.
func TestReadFileStopsOnContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	const size = 8 << 30 // sparse: takes no disk, but reading it would take seconds
	if err := os.Truncate(path, size); err != nil {
		t.Skipf("sparse file not supported: %v", err)
	}
	rt, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	r := &Runner{maxCopy: 1 << 20}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, _, err = r.readFile(ctx, rt, "big.bin", size)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("readFile took %v after cancel", d)
	}
}
