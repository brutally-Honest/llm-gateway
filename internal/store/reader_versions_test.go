package store_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

func versionOf(t *testing.T, r *store.Reader, ids ...store.SessionIdent) map[store.SessionIdent]store.Version {
	t.Helper()
	m, err := r.WithContext(t.Context()).SessionVersions(ids)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func saveEx(t *testing.T, s *store.Store, id, principal, client, key string, at int64) {
	t.Helper()
	ex := exchange(id, []byte("q"+id), []byte("r"+id))
	ex.PrincipalID, ex.Client, ex.SessionKey = principal, client, key
	ex.Start = time.Unix(1700000000+at, 0)
	ex.End = ex.Start.Add(time.Second)
	if err := s.SaveExchange(t.Context(), ex); err != nil {
		t.Fatal(err)
	}
}

func saveHookFor(t *testing.T, s *store.Store, principal, client, key string, seq int64) {
	t.Helper()
	h := &store.HookRecord{PrincipalID: principal, Client: client, Seq: seq, ReceivedAt: time.Unix(1700000000+seq, 0),
		Payload: []byte("{}"), Event: core.HarnessEvent{Phase: core.PhasePreTool, SessionKey: key, ToolCallID: "t", ToolName: "Bash"}}
	if err := s.SaveHook(t.Context(), h); err != nil {
		t.Fatal(err)
	}
}

func TestSessionVersions_ChangesOnEveryWritePath(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	r := openReader(t, dir)
	id := store.SessionIdent{Principal: "local", Client: "cc", Key: "k"}
	prev := versionOf(t, r, id)[id]
	step := func(name string, write func()) {
		t.Helper()
		write()
		cur := versionOf(t, r, id)[id]
		if cur == prev {
			t.Fatalf("%s did not change the version: %+v", name, cur)
		}
		prev = cur
	}
	step("insert", func() { saveEx(t, s, "a", "local", "cc", "k", 1) })
	step("parse landing", func() {
		if err := s.SaveParse(t.Context(), "a", "local", core.ParseOK,
			[]core.StoredEvent{{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{}`)}}, nil); err != nil {
			t.Fatal(err)
		}
	})
	step("backfill", func() {
		saveEx(t, s, "b", "local", "cc", "", 2)
		if _, err := s.SetSessionKeys(t.Context(), []store.KeyAssignment{{RequestID: "b", Key: "k"}}); err != nil {
			t.Fatal(err)
		}
	})
	step("hook", func() { saveHookFor(t, s, "local", "cc", "k", 1) })
}

func TestSessionVersions_BatchEqualsSingle(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	saveEx(t, s, "a", "local", "cc", "k1", 1)
	saveEx(t, s, "b", "local", "cc", "k1", 2)
	saveEx(t, s, "c", "local", "cc", "k2", 3)
	saveHookFor(t, s, "local", "cc", "k2", 1)
	saveHookFor(t, s, "local", "cc", "k3", 2)
	r := openReader(t, dir)
	ids := []store.SessionIdent{{"local", "cc", "k1"}, {"local", "cc", "k2"}, {"local", "cc", "k3"}, {"local", "cc", "none"}}
	batch := versionOf(t, r, ids...)
	for _, id := range ids {
		if one := versionOf(t, r, id); !reflect.DeepEqual(one[id], batch[id]) || len(one) != 1 {
			t.Fatalf("%v: single %+v, batch %+v", id, one, batch[id])
		}
	}
}

func TestSessionVersions_SameKeyOtherPrincipalOrClientIsSeparate(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	saveEx(t, s, "a", "local", "cc", "k", 1)
	saveEx(t, s, "b", "alice", "cc", "k", 2)
	saveEx(t, s, "c", "local", "other", "k", 3)
	saveEx(t, s, "d", "local", "other", "k", 4)
	saveHookFor(t, s, "alice", "cc", "k", 1)
	r := openReader(t, dir)
	a, b, c := store.SessionIdent{"local", "cc", "k"}, store.SessionIdent{"alice", "cc", "k"}, store.SessionIdent{"local", "other", "k"}
	m := versionOf(t, r, a, b, c)
	if m[a].ExCount != 1 || m[a].HookCount != 0 {
		t.Fatalf("a = %+v", m[a])
	}
	if m[b].ExCount != 1 || m[b].HookCount != 1 {
		t.Fatalf("b = %+v", m[b])
	}
	if m[c].ExCount != 2 || m[c].HookCount != 0 {
		t.Fatalf("c = %+v", m[c])
	}
}

func TestSessionVersions_HooksOnlyAndNoHooksSessionsHaveKeys(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	saveEx(t, s, "a", "local", "cc", "calls", 1)
	saveHookFor(t, s, "local", "cc", "hooks", 1)
	r := openReader(t, dir)
	calls, hooks, none := store.SessionIdent{"local", "cc", "calls"}, store.SessionIdent{"local", "cc", "hooks"}, store.SessionIdent{"local", "cc", "none"}
	m := versionOf(t, r, calls, hooks, none)
	if len(m) != 3 {
		t.Fatalf("entries = %d", len(m))
	}
	if v := m[calls]; v.ExCount != 1 || v.HookCount != 0 || v.HookMaxID != 0 {
		t.Fatalf("calls = %+v", v)
	}
	if v := m[hooks]; v.ExCount != 0 || v.ExMaxRow != 0 || v.HookCount != 1 || v.HookMaxID == 0 {
		t.Fatalf("hooks = %+v", v)
	}
	if m[none] != (store.Version{}) {
		t.Fatalf("none = %+v", m[none])
	}
}
