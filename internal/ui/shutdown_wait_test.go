package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// Spec 047 F1: the shutdown waits for a build still running. Wait does not return true
// while the build is mid-work, and once it does return, the build has finished its write,
// so nothing more lands in the folder and the folder can be removed.
func TestShutdown_WaitsForARunningBuildBeforeTheFolderGoes(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := newDigestCacheCtx(ctx, nil, 8)
	started, release := make(chan struct{}), make(chan struct{})
	id := store.SessionIdent{Principal: core.PrincipalLocal, Client: "cc", Key: "k"}
	late := filepath.Join(dir, "late")
	c.queue(id, store.Version{}, gatePrio{class: classBackground}, func(ctx context.Context) (digestMemo, error) {
		close(started)
		<-ctx.Done() // the shutdown has begun
		<-release
		// the work in hand still finishes: it writes after the shutdown began
		return digestMemo{}, os.WriteFile(late, []byte("x"), 0o600)
	})
	<-started
	cancel()

	gone, stop := context.WithCancel(context.Background())
	stop() // a wait with no time left
	if c.Wait(gone) {
		t.Fatal("Wait returned while a build was still running")
	}
	close(release)
	if !c.Wait(context.Background()) {
		t.Fatal("Wait did not return after the build finished")
	}
	if _, err := os.Stat(late); err != nil {
		t.Fatalf("the build's write was dropped: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the folder came back after Wait returned: %v", err)
	}
}

// The same for the disk cache's writer: after Wait returns it writes nothing more, and
// what it wrote is whole.
func TestShutdown_DiskWriterStopsAfterWait(t *testing.T) {
	e := newDiskEnv(t)
	e.seed()
	c := e.dep.Reader.WithContext(t.Context())
	id := store.SessionIdent{Principal: core.PrincipalLocal, Client: "cc", Key: "k"}
	vers, _ := c.SessionVersions([]store.SessionIdent{id})
	effs, _ := c.EffectVersions([]store.SessionIdent{id})
	e.disk.PutMemo(id, vers[id], effs[id], digestMemo{})
	e.cancel()
	if !e.disk.Wait(context.Background()) {
		t.Fatal("Wait did not return after the context was cancelled")
	}
	snap := func() map[string]time.Time {
		out := map[string]time.Time{}
		_ = filepath.WalkDir(e.disk.root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				fi, _ := d.Info()
				out[p] = fi.ModTime()
			}
			return nil
		})
		return out
	}
	before := snap()
	for p := range before {
		if strings.HasPrefix(filepath.Base(p), diskTmp) {
			t.Errorf("a half-written temp file is left: %s", p)
		}
	}
	e.disk.PutMemo(id, vers[id], effs[id], digestMemo{}) // no writer is left to take it
	e.disk.Trim(context.Background())
	after := snap()
	if len(after) != len(before) {
		t.Errorf("files after Wait: %d, before: %d", len(after), len(before))
	}
	for p, m := range before {
		if !after[p].Equal(m) {
			t.Errorf("%s was written after Wait returned", p)
		}
	}
}
