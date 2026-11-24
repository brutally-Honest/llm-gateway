package store_test

import (
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

func saveConn(t *testing.T, s *store.Store, session string, at int64) {
	t.Helper()
	e := core.Effect{Kind: core.EffectNetConn, At: time.Unix(at, 0), Principal: "local", Client: "cc",
		SessionKey: session, Host: "example.com", Port: 443, Via: "connect", EndedAt: time.Unix(at+1, 0),
		Outcome: core.NetOK}
	if err := s.SaveEffects(t.Context(), []core.Effect{e}, nil); err != nil {
		t.Fatal(err)
	}
}

// Maxima follow every write path and start at zero.
func TestMaxima_FollowTheTables(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	r := openReader(t, dir).WithContext(t.Context())
	if m, err := r.Maxima(); err != nil || m != (store.Maxima{}) {
		t.Fatalf("empty store: %+v %v", m, err)
	}
	saveEx(t, s, "e1", "local", "cc", "k", 1)
	saveHookFor(t, s, "local", "cc", "k", 2)
	saveConn(t, s, "k", 3)
	m, err := r.Maxima()
	if err != nil {
		t.Fatal(err)
	}
	if m.ExchangeRow < 1 || m.HookID < 1 || m.EffectID < 1 {
		t.Fatalf("Maxima = %+v, want all three set", m)
	}
	saveConn(t, s, "k", 4)
	m2, _ := r.Maxima()
	if m2.EffectID != m.EffectID+1 || m2.ExchangeRow != m.ExchangeRow || m2.HookID != m.HookID {
		t.Errorf("Maxima %+v -> %+v, want only the effect id to move by one", m, m2)
	}
	if id, err := r.MaxEffectID(); err != nil || id != m2.EffectID {
		t.Errorf("MaxEffectID = %d %v, want %d", id, err, m2.EffectID)
	}
}

// UnassignedConnAfter sees only connections with no session, newer than the id, inside the range.
func TestUnassignedConnAfter(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	r := openReader(t, dir).WithContext(t.Context())
	saveConn(t, s, "", 100) // unassigned, old
	base, _ := r.MaxEffectID()
	check := func(name string, after, from, to int64, want bool) {
		t.Helper()
		const sec = int64(time.Second)
		got, err := r.UnassignedConnAfter(after, from*sec, to*sec)
		if err != nil || got != want {
			t.Errorf("%s: got %v %v, want %v", name, got, err, want)
		}
	}
	check("nothing newer", base, 0, 1000, false)
	check("older one counts when after is before it", 0, 0, 1000, true)
	saveConn(t, s, "k", 150) // assigned: never counts
	check("assigned connection", base, 0, 1000, false)
	saveConn(t, s, "", 150)
	check("newer, inside range", base, 100, 200, true)
	check("newer, before range", base, 151, 200, false)
	check("newer, after range", base, 0, 149, false)
	check("range ends are inclusive", base, 150, 150, true)
}

// RowIdentities names the row at each id, skips id 0 and flags an id with no row.
func TestRowIdentities(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	r := openReader(t, dir).WithContext(t.Context())
	if got, err := r.RowIdentities(0, 0, 0); err != nil || got != (store.RowIdentities{}) {
		t.Fatalf("zero ids: %+v %v", got, err)
	}
	saveEx(t, s, "e1", "local", "cc", "k", 1)
	saveHookFor(t, s, "local", "cc", "k", 2)
	saveConn(t, s, "k", 3)
	m, err := r.Maxima()
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.RowIdentities(m.ExchangeRow, m.HookID, m.EffectID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExchangeRequestID != "e1" || got.HookReceivedAt == 0 || got.EffectAt == 0 ||
		got.ExchangeMissing || got.HookMissing || got.EffectMissing {
		t.Errorf("RowIdentities = %+v", got)
	}
	gone, err := r.RowIdentities(m.ExchangeRow+1, m.HookID+1, m.EffectID+1)
	if err != nil {
		t.Fatal(err)
	}
	if !gone.ExchangeMissing || !gone.HookMissing || !gone.EffectMissing {
		t.Errorf("ids past the maxima = %+v, want all missing", gone)
	}
}
