package store_test

import (
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// AC13: storing effects for a session leaves its timeline version (SessionVersions)
// as it was, and moves its own effect version.
func TestEffects_SessionVersionUnchanged(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	id := store.SessionIdent{Principal: "local", Client: "client-a", Key: "s1"}
	ev := core.HarnessEvent{Phase: core.PhasePostTool, SessionKey: id.Key}
	if err := s.SaveHook(t.Context(), hookRec(`{"x":1}`, ev, time.Unix(100, 0))); err != nil {
		t.Fatal(err)
	}
	r := openReader(t, dir).WithContext(t.Context())
	ids := []store.SessionIdent{id}
	before, err := r.SessionVersions(ids)
	if err != nil {
		t.Fatal(err)
	}
	effBefore, err := r.EffectVersions(ids)
	if err != nil {
		t.Fatal(err)
	}

	copyOf := []byte("hello\n")
	effects := []core.Effect{
		{Kind: core.EffectFileChange, At: time.Unix(101, 0), Principal: id.Principal, Client: id.Client,
			SessionKey: id.Key, Root: "/w", Path: "/w/a.txt", Change: core.FileCreated, Size: 6, Mode: "git",
			AfterHash: store.ContentHash(copyOf)},
		{Kind: core.EffectNetConn, At: time.Unix(102, 0), Principal: id.Principal, Client: id.Client,
			SessionKey: id.Key, Host: "example.com", Port: 443, Via: "connect", BytesUp: 10, BytesDown: 20,
			EndedAt: time.Unix(103, 0), Outcome: core.NetOK},
	}
	cp := store.Checkpoint{Effects: effects, Copies: [][]byte{copyOf},
		Upserts: []store.SnapshotRow{{Root: "/w"}, {Root: "/w", Path: "a.txt", Size: 6, Hash: "h"}}}
	if err := s.SaveCheckpoint(t.Context(), id, cp); err != nil {
		t.Fatal(err)
	}

	after, err := r.SessionVersions(ids)
	if err != nil {
		t.Fatal(err)
	}
	if after[id] != before[id] {
		t.Errorf("SessionVersions moved: %+v -> %+v", before[id], after[id])
	}
	effAfter, err := r.EffectVersions(ids)
	if err != nil {
		t.Fatal(err)
	}
	if effAfter[id] == effBefore[id] || effAfter[id].Count != 2 {
		t.Errorf("EffectVersions = %+v -> %+v, want it moved to 2 effects", effBefore[id], effAfter[id])
	}
	snap, err := s.LoadSnapshot(t.Context(), id)
	if err != nil || len(snap) != 2 {
		t.Errorf("LoadSnapshot = %+v, %v; want the 2 saved rows", snap, err)
	}
}
