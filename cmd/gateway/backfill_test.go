package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/clients/claudecode"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

const ccHeader = "X-Claude-Code-Session-Id"

func saveBare(t *testing.T, s *store.Store, id, client string, h http.Header) {
	t.Helper()
	ex := &core.Exchange{RequestID: id, PrincipalID: core.PrincipalLocal, Protocol: "test", Client: client,
		Auth: core.AuthAPIKey, Method: http.MethodPost, Path: "/v1/m", RequestHeader: h, Status: 200,
		Start: viewerBase, End: viewerBase.Add(time.Second)}
	if err := s.SaveExchange(t.Context(), ex); err != nil {
		t.Fatal(err)
	}
}

func seedBare(t *testing.T, fill func(*store.Store)) string {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	fill(s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func keyOf(t *testing.T, dir, id string) string {
	t.Helper()
	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	row, err := r.Exchange(id)
	if err != nil {
		t.Fatal(err)
	}
	return row.SessionKey
}

func TestBackfill_FillsFromStoredHeaders(t *testing.T) {
	dir := seedBare(t, func(s *store.Store) {
		saveBare(t, s, "a", claudecode.Name, http.Header{ccHeader: {"sess-1"}})
		saveBare(t, s, "b", claudecode.Name, http.Header{ccHeader: {"sess-2"}})
	})
	code, out := viewerRun(t, dir, "backfill-sessions")
	if code != exitOK || !strings.HasPrefix(out, "filled 2, left without key 0, skipped 0") {
		t.Fatalf("code %d out %q", code, out)
	}
	if keyOf(t, dir, "a") != "sess-1" || keyOf(t, dir, "b") != "sess-2" {
		t.Fatal("keys not filled from stored headers")
	}
}

func TestBackfill_SkipsUnknownClientAndMissingProfile(t *testing.T) {
	dir := seedBare(t, func(s *store.Store) {
		saveBare(t, s, "u", core.ClientUnknown, http.Header{ccHeader: {"x"}})
		saveBare(t, s, "gone", "retired-client", http.Header{ccHeader: {"x"}})
	})
	code, out := viewerRun(t, dir, "backfill-sessions")
	if code != exitOK || !strings.HasPrefix(out, "filled 0, left without key 0, skipped 2") {
		t.Fatalf("code %d out %q", code, out)
	}
	if keyOf(t, dir, "u") != "" || keyOf(t, dir, "gone") != "" {
		t.Fatal("skipped exchanges got a key")
	}
}

func TestBackfill_InvalidKeyLeftWithoutKey(t *testing.T) {
	dir := seedBare(t, func(s *store.Store) {
		saveBare(t, s, "none", claudecode.Name, http.Header{})
		saveBare(t, s, "ctl", claudecode.Name, http.Header{ccHeader: {"a\x01b"}})
		saveBare(t, s, "long", claudecode.Name, http.Header{ccHeader: {strings.Repeat("k", 257)}})
		saveBare(t, s, "ok", claudecode.Name, http.Header{ccHeader: {"fine"}})
	})
	code, out := viewerRun(t, dir, "backfill-sessions")
	if code != exitOK || !strings.HasPrefix(out, "filled 1, left without key 3, skipped 0") {
		t.Fatalf("code %d out %q", code, out)
	}
	for _, id := range []string{"none", "ctl", "long"} {
		if keyOf(t, dir, id) != "" {
			t.Fatalf("%s got a key", id)
		}
	}
}

func TestBackfill_SecondRunChangesNothing(t *testing.T) {
	dir := seedBare(t, func(s *store.Store) {
		saveBare(t, s, "a", claudecode.Name, http.Header{ccHeader: {"sess-1"}})
		saveBare(t, s, "n", claudecode.Name, http.Header{})
	})
	viewerRun(t, dir, "backfill-sessions")
	code, out := viewerRun(t, dir, "backfill-sessions")
	if code != exitOK || !strings.HasPrefix(out, "filled 0, left without key 1, skipped 0") {
		t.Fatalf("code %d out %q", code, out)
	}
	if keyOf(t, dir, "a") != "sess-1" {
		t.Fatal("key changed")
	}
}

func TestBackfill_ConcurrentWithCapture(t *testing.T) {
	const old, live = 1200, 100
	dir := seedBare(t, func(s *store.Store) {
		for i := range old {
			saveBare(t, s, fmt.Sprintf("old%d", i), claudecode.Name, http.Header{ccHeader: {"s-old"}})
		}
	})
	live1, err := store.Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live1.Close() }()
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range live {
			saveBare(t, live1, fmt.Sprintf("live%d", i), claudecode.Name, http.Header{ccHeader: {"s-live"}})
		}
	})
	code, _ := viewerRun(t, dir, "backfill-sessions")
	wg.Wait()
	if code != exitOK {
		t.Fatalf("code %d", code)
	}
	// Whatever the interleaving, a second run leaves every exchange keyed.
	viewerRun(t, dir, "backfill-sessions")
	for i := range old {
		if got := keyOf(t, dir, fmt.Sprintf("old%d", i)); got != "s-old" {
			t.Fatalf("old%d key = %q", i, got)
		}
	}
	for i := range live {
		if got := keyOf(t, dir, fmt.Sprintf("live%d", i)); got != "s-live" {
			t.Fatalf("live%d key = %q", i, got)
		}
	}
}

func TestBackfill_StoreMissingFailsWithoutCreatingIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nostore")
	code, out := viewerRun(t, dir, "backfill-sessions")
	if code != exitRuntime || !strings.Contains(out, "store not found") {
		t.Fatalf("code %d out %q", code, out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("store dir was created: %v", err)
	}
}
