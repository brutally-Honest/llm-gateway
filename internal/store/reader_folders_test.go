package store_test

import (
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

func saveHookDir(t *testing.T, s *store.Store, principal, client, key, dir string, seq, at int64) {
	t.Helper()
	h := &store.HookRecord{PrincipalID: principal, Client: client, Seq: seq, ReceivedAt: time.Unix(1700000000+at, 0),
		Payload: []byte("{}"), Event: core.HarnessEvent{Phase: core.PhasePreTool, SessionKey: key, ToolCallID: "t", ToolName: "Bash", WorkDir: dir}}
	if err := s.SaveHook(t.Context(), h); err != nil {
		t.Fatal(err)
	}
}

func foldersOf(t *testing.T, r *store.Reader, ids ...store.SessionIdent) map[store.SessionIdent]string {
	t.Helper()
	m, err := r.WithContext(t.Context()).SessionFolders(ids)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// The folder is the work_dir of the first hook by received_at, seq, id that names one.
func TestSessionFolders_FirstNamedFolderByHookOrder(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	r := openReader(t, dir)
	byTime := store.SessionIdent{Principal: "local", Client: "cc", Key: "by-time"}
	tie := store.SessionIdent{Principal: "local", Client: "cc", Key: "tie"}
	skip := store.SessionIdent{Principal: "local", Client: "cc", Key: "skip"}
	none := store.SessionIdent{Principal: "local", Client: "cc", Key: "none"}
	exOnly := store.SessionIdent{Principal: "local", Client: "cc", Key: "ex-only"}
	missing := store.SessionIdent{Principal: "local", Client: "cc", Key: "missing"}

	// inserted out of order: the earlier received_at wins, not the earlier row id
	saveHookDir(t, s, "local", "cc", "by-time", "/late", 1, 50)
	saveHookDir(t, s, "local", "cc", "by-time", "/early", 2, 10)
	// same received_at: the lower seq wins
	saveHookDir(t, s, "local", "cc", "tie", "/seq2", 2, 5)
	saveHookDir(t, s, "local", "cc", "tie", "/seq1", 1, 5)
	// empty work_dir (stored NULL) is skipped even when it is the first hook
	saveHookDir(t, s, "local", "cc", "skip", "", 1, 1)
	saveHookDir(t, s, "local", "cc", "skip", "/named", 2, 2)
	saveHookDir(t, s, "local", "cc", "none", "", 1, 1)
	saveEx(t, s, "e1", "local", "cc", "ex-only", 1)

	got := foldersOf(t, r, byTime, tie, skip, none, exOnly, missing)
	want := map[store.SessionIdent]string{byTime: "/early", tie: "/seq1", skip: "/named", none: "", exOnly: "", missing: ""}
	for id, w := range want {
		if g, ok := got[id]; !ok || g != w {
			t.Errorf("%s = %q (present %v), want %q", id.Key, g, ok, w)
		}
	}
}

// A session holding exchanges and hooks reads its hook folder; the identity is the full
// (principal, client, key), so the same key elsewhere does not leak its folder.
func TestSessionFolders_IdentityIsPrincipalClientKey(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	r := openReader(t, dir)
	a := store.SessionIdent{Principal: "local", Client: "cc", Key: "k"}
	b := store.SessionIdent{Principal: "other", Client: "cc", Key: "k"}
	c := store.SessionIdent{Principal: "local", Client: "codex", Key: "k"}
	saveEx(t, s, "e1", "local", "cc", "k", 1)
	saveHookDir(t, s, "local", "cc", "k", "/a", 1, 1)
	saveHookDir(t, s, "other", "cc", "k", "/b", 1, 1)

	got := foldersOf(t, r, a, b, c)
	if got[a] != "/a" || got[b] != "/b" || got[c] != "" {
		t.Fatalf("folders = %v", got)
	}
	if got := foldersOf(t, r); len(got) != 0 {
		t.Fatalf("no ids = %v", got)
	}
}
