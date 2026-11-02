package store_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

type sx struct {
	id, principal, client, session string
	at                             int64 // seconds after the base time
}

func seedSessions(t *testing.T, xs ...sx) *store.Reader {
	t.Helper()
	dir := t.TempDir()
	s, _ := open(t, dir)
	for _, e := range xs {
		ex := exchange(e.id, []byte("q"+e.id), []byte("r"+e.id))
		ex.PrincipalID, ex.Client, ex.SessionKey = e.principal, e.client, e.session
		ex.Start = time.Unix(1700000000+e.at, 0)
		ex.End = ex.Start.Add(time.Second)
		if err := s.SaveExchange(t.Context(), ex); err != nil {
			t.Fatal(err)
		}
	}
	return openReader(t, dir)
}

func sessionNames(rows []store.SessionRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.PrincipalID+"/"+r.Client+"/"+r.SessionKey)
	}
	return out
}

func requestIDs(rows []store.ExchangeRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.RequestID)
	}
	return out
}

func TestReader_SessionsKeyedByPrincipalClientAndKey(t *testing.T) {
	r := seedSessions(t,
		sx{"a", "local", "cc", "k", 1},
		sx{"b", "local", "other", "k", 2},
		sx{"c", "alice", "cc", "k", 3},
		sx{"d", "local", "cc", "k", 4},
	)
	rows, err := r.SessionRows(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("sessions = %v, want 3", sessionNames(rows))
	}
	for _, row := range rows {
		if row.PrincipalID == "local" && row.Client == "cc" {
			if row.Count != 2 {
				t.Fatalf("count = %d, want 2", row.Count)
			}
			if row.First != time.Unix(1700000001, 0).UnixNano() || row.Last != time.Unix(1700000004, 0).UnixNano() {
				t.Fatalf("first/last = %d/%d", row.First, row.Last)
			}
		}
	}
	got, err := r.SessionExchanges("local", "cc", "k")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "d"}; !reflect.DeepEqual(requestIDs(got), want) {
		t.Fatalf("exchanges = %v, want %v", requestIDs(got), want)
	}
}

func TestReader_SessionsNewestActivityFirst(t *testing.T) {
	r := seedSessions(t,
		sx{"a", "local", "cc", "old", 1},
		sx{"b", "local", "cc", "new", 5},
		sx{"c", "local", "cc", "old", 9},
		sx{"d", "local", "cc", "mid", 7},
		sx{"e", "local", "cc", "", 20},
	)
	rows, err := r.SessionRows(0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"local/cc/old", "local/cc/mid", "local/cc/new"}
	if !reflect.DeepEqual(sessionNames(rows), want) {
		t.Fatalf("order = %v, want %v", sessionNames(rows), want)
	}
}

func TestReader_SessionsLimit(t *testing.T) {
	r := seedSessions(t,
		sx{"a", "local", "cc", "k1", 1},
		sx{"b", "local", "cc", "k2", 2},
		sx{"c", "local", "cc", "k3", 3},
	)
	for _, tc := range []struct{ n, want int }{{2, 2}, {0, 3}, {10, 3}} {
		rows, err := r.SessionRows(tc.n)
		if err != nil || len(rows) != tc.want {
			t.Fatalf("SessionRows(%d) = %d rows, err %v, want %d", tc.n, len(rows), err, tc.want)
		}
	}
	rows, _ := r.SessionRows(2)
	if want := []string{"local/cc/k3", "local/cc/k2"}; !reflect.DeepEqual(sessionNames(rows), want) {
		t.Fatalf("top 2 = %v, want %v", sessionNames(rows), want)
	}
}

func TestReader_UngroupedSummary(t *testing.T) {
	r := seedSessions(t,
		sx{"a", "local", "cc", "", 3},
		sx{"b", "local", "cc", "k", 4},
		sx{"c", "local", "cc", "", 9},
	)
	sum, err := r.UngroupedSummary()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Count != 2 || sum.First != time.Unix(1700000003, 0).UnixNano() || sum.Last != time.Unix(1700000009, 0).UnixNano() {
		t.Fatalf("summary = %+v", sum)
	}
	empty, err := seedSessions(t, sx{"a", "local", "cc", "k", 1}).UngroupedSummary()
	if err != nil || empty.Count != 0 {
		t.Fatalf("summary = %+v, err %v, want count 0", empty, err)
	}
}

func TestReader_UngroupedExchangesNewestFirst(t *testing.T) {
	r := seedSessions(t,
		sx{"a", "local", "cc", "", 1},
		sx{"b", "local", "cc", "k", 2},
		sx{"c", "local", "cc", "", 3},
		sx{"d", "local", "cc", "", 2},
	)
	got, err := r.UngroupedExchanges(0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"c", "d", "a"}; !reflect.DeepEqual(requestIDs(got), want) {
		t.Fatalf("ungrouped = %v, want %v", requestIDs(got), want)
	}
	got, _ = r.UngroupedExchanges(2)
	if want := []string{"c", "d"}; !reflect.DeepEqual(requestIDs(got), want) {
		t.Fatalf("limit 2 = %v, want %v", requestIDs(got), want)
	}
}

func TestReader_SessionExchangesTieOrdersByRequestID(t *testing.T) {
	r := seedSessions(t,
		sx{"z", "local", "cc", "k", 1},
		sx{"a", "local", "cc", "k", 1},
		sx{"m", "local", "cc", "k", 1},
	)
	got, err := r.SessionExchanges("local", "cc", "k")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "m", "z"}; !reflect.DeepEqual(requestIDs(got), want) {
		t.Fatalf("order = %v, want %v", requestIDs(got), want)
	}
}

func TestReader_SessionsByKeyAcrossClients(t *testing.T) {
	r := seedSessions(t,
		sx{"a", "local", "cc", "k", 1},
		sx{"b", "local", "other", "k", 2},
		sx{"c", "alice", "cc", "k", 3},
		sx{"d", "local", "cc", "x", 4},
	)
	rows, err := r.SessionsByKey("k")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alice/cc/k", "local/other/k", "local/cc/k"}
	if !reflect.DeepEqual(sessionNames(rows), want) {
		t.Fatalf("matches = %v, want %v", sessionNames(rows), want)
	}
	none, err := r.SessionsByKey("nope")
	if err != nil || len(none) != 0 {
		t.Fatalf("no match = %v, err %v", none, err)
	}
}

func TestReader_SessionsEmptyStore(t *testing.T) {
	r := seedSessions(t)
	rows, err := r.SessionRows(0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("sessions = %v, err %v", rows, err)
	}
	sum, err := r.UngroupedSummary()
	if err != nil || sum.Count != 0 {
		t.Fatalf("summary = %+v, err %v", sum, err)
	}
	ex, err := r.UngroupedExchanges(0)
	if err != nil || len(ex) != 0 {
		t.Fatalf("ungrouped = %v, err %v", ex, err)
	}
	se, err := r.SessionExchanges("local", "cc", "k")
	if err != nil || len(se) != 0 {
		t.Fatalf("exchanges = %v, err %v", se, err)
	}
}

// saveHooks stores recs in order, numbering them 1, 2, ... as the sink would.
func saveHooks(t *testing.T, dir string, recs ...*store.HookRecord) {
	t.Helper()
	s, _ := open(t, dir)
	for i, h := range recs {
		h.Seq = int64(i + 1)
		if err := s.SaveHook(t.Context(), h); err != nil {
			t.Fatal(err)
		}
	}
}

func hookFor(principal, client, session, payload string, at time.Time) *store.HookRecord {
	h := hookRec(payload, core.HarnessEvent{Phase: core.PhaseTurnEnd, SessionKey: session}, at)
	h.PrincipalID, h.Client = principal, client
	return h
}

func TestHookEvents_OrderedByReceiveThenSeq(t *testing.T) {
	dir := t.TempDir()
	t1, t2 := time.Unix(100, 0), time.Unix(200, 0)
	saveHooks(t, dir,
		hookFor("local", "cc", "s", `{"n":1}`, t2),
		hookFor("local", "cc", "s", `{"n":2}`, t1),
		hookFor("local", "cc", "s", `{"n":3}`, t1))
	r := openReader(t, dir)
	for name, get := range map[string]func() ([]store.HookRow, error){
		"reader": func() ([]store.HookRow, error) { return r.HookEvents("local", "cc", "s") },
		"ctx":    func() ([]store.HookRow, error) { return r.WithContext(t.Context()).HookEvents("local", "cc", "s") },
	} {
		rows, err := get()
		if err != nil {
			t.Fatal(err)
		}
		var seqs []int64
		for _, h := range rows {
			seqs = append(seqs, h.Seq)
		}
		if !reflect.DeepEqual(seqs, []int64{2, 3, 1}) {
			t.Fatalf("%s: seqs = %v", name, seqs)
		}
	}
}

func TestHookEvents_PrincipalScoped(t *testing.T) {
	dir := t.TempDir()
	at := time.Unix(100, 0)
	saveHooks(t, dir,
		hookFor("local", "cc", "s", `{"n":1}`, at),
		hookFor("other", "cc", "s", `{"n":2}`, at),
		hookFor("local", "zz", "s", `{"n":3}`, at))
	rows, err := openReader(t, dir).HookEvents("local", "cc", "s")
	if err != nil || len(rows) != 1 || rows[0].PrincipalID != "local" || rows[0].Client != "cc" {
		t.Fatalf("rows = %+v, err %v", rows, err)
	}
}

func TestSessionRows_HooksOnlySessionListed(t *testing.T) {
	dir := t.TempDir()
	saveHooks(t, dir, hookFor("local", "cc", "h", `{"n":1}`, time.Unix(100, 0)),
		hookFor("local", "cc", "h", `{"n":2}`, time.Unix(300, 0)))
	r := openReader(t, dir)
	rows, err := r.SessionRows(0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v, err %v", rows, err)
	}
	want := store.SessionRow{PrincipalID: "local", Client: "cc", SessionKey: "h",
		First: time.Unix(100, 0).UnixNano(), Last: time.Unix(300, 0).UnixNano(), HooksOnly: true}
	if rows[0] != want {
		t.Fatalf("row = %+v, want %+v", rows[0], want)
	}
	by, err := r.SessionsByKey("h")
	if err != nil || len(by) != 1 || !by[0].HooksOnly {
		t.Fatalf("by key = %+v, err %v", by, err)
	}
	one, err := r.WithContext(t.Context()).SessionByKey("local", "cc", "h")
	if err != nil || !one.HooksOnly {
		t.Fatalf("session by key = %+v, err %v", one, err)
	}
}

func TestSessionRows_ExchangeSessionNotHooksOnly(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	ex := exchange("a", []byte("q"), []byte("r"))
	ex.PrincipalID, ex.Client, ex.SessionKey = "local", "cc", "k"
	if err := s.SaveExchange(t.Context(), ex); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveHook(t.Context(), hookFor("local", "cc", "k", `{"n":1}`, time.Unix(100, 0))); err != nil {
		t.Fatal(err)
	}
	rows, err := openReader(t, dir).SessionRows(0)
	if err != nil || len(rows) != 1 || rows[0].HooksOnly || rows[0].Count != 1 {
		t.Fatalf("rows = %+v, err %v", rows, err)
	}
}

func TestUngroupedHookCount(t *testing.T) {
	dir := t.TempDir()
	at := time.Unix(100, 0)
	saveHooks(t, dir, hookFor("local", "cc", "", `{"n":1}`, at), hookFor("local", "cc", "", `{"n":2}`, at),
		hookFor("local", "cc", "s", `{"n":3}`, at))
	r := openReader(t, dir)
	n, err := r.UngroupedHookCount()
	if err != nil || n != 2 {
		t.Fatalf("count = %d, err %v", n, err)
	}
	if n, err = r.WithContext(t.Context()).UngroupedHookCount(); err != nil || n != 2 {
		t.Fatalf("ctx count = %d, err %v", n, err)
	}
	rows, _ := r.SessionRows(0)
	if len(rows) != 1 || rows[0].SessionKey != "s" {
		t.Fatalf("sessions = %+v", rows)
	}
}
