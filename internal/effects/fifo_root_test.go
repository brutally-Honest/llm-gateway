package effects

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// fifoAt makes a FIFO at a path that was a folder: `mv proj x; mkfifo proj`.
func fifoAt(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "proj")
	if err := exec.Command("mkfifo", p).Run(); err != nil {
		t.Skipf("no mkfifo: %v", err)
	}
	return p
}

// noHang fails the test when f does not return in 5 seconds.
func noHang(t *testing.T, what string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s blocked on a FIFO", what)
	}
}

// A watched folder swapped for a FIFO is a folder that is gone: the saved rows stay and
// the checkpoint does not block opening it.
func TestCheckRoot_FifoInPlaceOfRoot(t *testing.T) {
	ts := openStore(t)
	r := newTestRunner(t, ts.st)
	root := fifoAt(t)
	s := &session{ident: testSession, tops: map[string]string{}, workDir: root}
	saved := map[string]fileState{"a.txt": {}}
	noHang(t, "checkRoot", func() {
		got, err := r.checkRoot(context.Background(), s, root, saved, false, time.Now(), &store.Checkpoint{})
		if err != nil {
			t.Errorf("checkRoot: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("saved rows not kept: %v", got)
		}
	})
}

// A git top level that is a FIFO is never opened as a work tree.
func TestCandidates_FifoTop(t *testing.T) {
	needGit(t)
	top := fifoAt(t)
	noHang(t, "candidates", func() {
		if _, err := newGitRunner(30*time.Second).candidates(context.Background(), top); err == nil {
			t.Error("a FIFO top returned no error")
		}
	})
	noHang(t, "openDirRoot", func() {
		if rt, err := openDirRoot(top); err == nil {
			_ = rt.Close()
			t.Error("openDirRoot opened a FIFO")
		} else if !os.IsNotExist(err) {
			t.Errorf("want not-exist, got %v", err)
		}
	})
}
