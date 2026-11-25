package store_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

var _ interface {
	SessionExchanges(principal, client, key string) ([]store.ExchangeRow, error)
	Events(requestID string) ([]store.EventRow, error)
	Content(hash string) ([]byte, error)
} = (*store.CtxReader)(nil)

func TestReader_WriteThroughReaderFails(t *testing.T) {
	r := seedSessions(t, sx{"a", "local", "cc", "k", 1})
	if err := r.ExecForTest(`DELETE FROM exchanges`); err == nil {
		t.Fatal("write through the reader succeeded")
	}
	rows, err := r.SessionRows(0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %v, err = %v", rows, err)
	}
}

func TestReader_CancelledContextAbandonsQuery(t *testing.T) {
	r := seedSessions(t, sx{"a", "local", "cc", "k", 1})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c := r.WithContext(ctx)
	if _, err := c.SessionPage(nil, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("SessionPage err = %v, want Canceled", err)
	}
	if _, err := c.SessionExchanges("local", "cc", "k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("SessionExchanges err = %v, want Canceled", err)
	}
	dctx, dcancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer dcancel()
	<-dctx.Done()
	if _, err := r.WithContext(dctx).UngroupedPage(nil, 10); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("UngroupedPage err = %v, want DeadlineExceeded", err)
	}
}

func walkSessions(t *testing.T, r *store.Reader, limit int) []string {
	t.Helper()
	var (
		out   []string
		after *store.SessionAfter
	)
	for range 100 {
		page, err := r.WithContext(t.Context()).SessionPage(after, limit)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, sessionNames(page)...)
		if len(page) < limit {
			return out
		}
		l := page[len(page)-1]
		after = &store.SessionAfter{Last: l.Last, PrincipalID: l.PrincipalID, Client: l.Client, SessionKey: l.SessionKey}
	}
	t.Fatal("walk did not end")
	return nil
}

func TestReader_SessionPageWalkNoDuplicates(t *testing.T) {
	var xs []sx
	for i := range 7 {
		xs = append(xs, sx{fmt.Sprintf("x%d", i), "local", "cc", fmt.Sprintf("k%d", i), int64(i)})
	}
	r := seedSessions(t, xs...)
	all, err := r.SessionRows(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 2, 3, 7, 10} {
		if got := walkSessions(t, r, limit); !reflect.DeepEqual(got, sessionNames(all)) {
			t.Fatalf("limit %d: walk = %v, want %v", limit, got, sessionNames(all))
		}
	}
}

func TestReader_SessionPageTiesBrokenByKey(t *testing.T) {
	r := seedSessions(t,
		sx{"a", "local", "cc", "kb", 5},
		sx{"b", "local", "cc", "ka", 5},
		sx{"c", "local", "cc", "kd", 5},
		sx{"d", "alice", "cc", "kc", 5},
		sx{"e", "local", "cc", "ke", 9},
	)
	all, err := r.SessionRows(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 2, 3} {
		if got := walkSessions(t, r, limit); !reflect.DeepEqual(got, sessionNames(all)) || len(got) != 5 {
			t.Fatalf("limit %d: walk = %v, want %v", limit, got, sessionNames(all))
		}
	}
}

func TestReader_UngroupedPageTiesBrokenByRequestID(t *testing.T) {
	r := seedSessions(t,
		sx{"c", "local", "cc", "", 5},
		sx{"a", "local", "cc", "", 5},
		sx{"b", "local", "cc", "", 5},
		sx{"d", "local", "cc", "", 8},
		sx{"s", "local", "cc", "k", 5},
	)
	want, err := r.UngroupedExchanges(0)
	if err != nil {
		t.Fatal(err)
	}
	if got := requestIDs(want); !reflect.DeepEqual(got, []string{"d", "a", "b", "c"}) {
		t.Fatalf("ungrouped = %v", got)
	}
	for _, limit := range []int{1, 2, 3} {
		var (
			got   []string
			after *store.UngroupedAfter
		)
		for range 20 {
			page, err := r.WithContext(t.Context()).UngroupedPage(after, limit)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, requestIDs(page)...)
			if len(page) < limit {
				break
			}
			l := page[len(page)-1]
			after = &store.UngroupedAfter{StartedAt: l.StartedAt, RequestID: l.RequestID}
		}
		if !reflect.DeepEqual(got, requestIDs(want)) {
			t.Fatalf("limit %d: walk = %v, want %v", limit, got, requestIDs(want))
		}
	}
}

func TestReader_ExistingMethodsUnchanged(t *testing.T) {
	r := seedSessions(t,
		sx{"a", "local", "cc", "k", 1},
		sx{"b", "local", "cc", "k", 2},
		sx{"u", "local", "cc", "", 3},
	)
	c := r.WithContext(t.Context())
	a, err := r.SessionExchanges("local", "cc", "k")
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.SessionExchanges("local", "cc", "k")
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("ctx view differs: %v %v", err, b)
	}
	snap, max, err := c.SessionSnapshot("local", "cc", "k")
	if err != nil || len(snap) != 2 || snap[0].RequestID != "a" || max != snap[1].RowID || max <= snap[0].RowID {
		t.Fatalf("snapshot = %+v max %d err %v", snap, max, err)
	}
	if id, err := c.RowID("b"); err != nil || id != snap[1].RowID {
		t.Fatalf("RowID = %d, %v", id, err)
	}
	if _, err := c.RowID("zz"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("RowID missing err = %v", err)
	}
	s, err := c.SessionByKey("local", "cc", "k")
	if err != nil || s.Count != 2 {
		t.Fatalf("SessionByKey = %+v, %v", s, err)
	}
	if _, err := c.SessionByKey("local", "cc", "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SessionByKey missing err = %v", err)
	}
}

// seedMixedSessions makes six exchange sessions (two tied on last) and two hooks-only sessions.
func seedMixedSessions(t *testing.T) *store.Reader {
	t.Helper()
	dir := t.TempDir()
	s, _ := open(t, dir)
	for i, e := range []sx{
		{"a", "local", "cc", "k1", 1}, {"b", "local", "cc", "k2", 5}, {"c", "local", "cc", "k3", 5},
		{"d", "local", "cc", "k4", 3}, {"e", "local", "cc", "k5", 9}, {"f", "alice", "cc", "k6", 2},
	} {
		ex := exchange(e.id, []byte("q"+e.id), []byte("r"+e.id))
		ex.PrincipalID, ex.Client, ex.SessionKey = e.principal, e.client, e.session
		ex.Start = time.Unix(1700000000+e.at, 0)
		ex.End = ex.Start.Add(time.Second)
		if err := s.SaveExchange(t.Context(), ex); err != nil {
			t.Fatal(i, err)
		}
	}
	for i, k := range []string{"h1", "h2"} {
		h := hookFor("local", "cc", k, `{"n":1}`, time.Unix(1700000004+int64(i), 0))
		h.Seq = int64(i + 1)
		if err := s.SaveHook(t.Context(), h); err != nil {
			t.Fatal(err)
		}
	}
	return openReader(t, dir)
}

// The window count agrees with SessionCount and the rows with SessionPage and
// SessionPageAt, with and without after, offset and an empty page.
func TestReader_SessionPageTotal(t *testing.T) {
	for name, r := range map[string]*store.Reader{
		"mixed": seedMixedSessions(t), "empty": seedSessions(t),
	} {
		c := r.WithContext(t.Context())
		want, err := c.SessionCount()
		if err != nil {
			t.Fatal(err)
		}
		all, err := r.SessionRows(0)
		if err != nil {
			t.Fatal(err)
		}
		afters := []*store.SessionAfter{nil}
		for _, row := range all {
			afters = append(afters, &store.SessionAfter{Last: row.Last, PrincipalID: row.PrincipalID, Client: row.Client, SessionKey: row.SessionKey})
		}
		for _, limit := range []int{1, 3, 100} {
			for ai, after := range afters {
				rows, total, err := c.SessionPageTotal(after, limit)
				if err != nil {
					t.Fatal(err)
				}
				page, err := c.SessionPage(after, limit)
				if err != nil {
					t.Fatal(err)
				}
				if total != want || !reflect.DeepEqual(rows, page) {
					t.Fatalf("%s after#%d limit %d: total %d rows %v, want %d %v", name, ai, limit, total, rows, want, page)
				}
			}
			for _, offset := range []int{0, 1, 4, len(all), len(all) + 7} {
				rows, total, err := c.SessionPageAtTotal(offset, limit)
				if err != nil {
					t.Fatal(err)
				}
				page, err := c.SessionPageAt(offset, limit)
				if err != nil {
					t.Fatal(err)
				}
				if total != want || !reflect.DeepEqual(rows, page) {
					t.Fatalf("%s offset %d limit %d: total %d rows %v, want %d %v", name, offset, limit, total, rows, want, page)
				}
			}
		}
	}
	// the mixed store really holds hooks-only sessions, and an empty page past the end still counts them
	c := seedMixedSessions(t).WithContext(t.Context())
	rows, total, err := c.SessionPageAtTotal(100, 5)
	if err != nil || len(rows) != 0 || total != 8 {
		t.Fatalf("past the end: rows %v total %d err %v, want 0 rows and 8", rows, total, err)
	}
	rows, total, err = c.SessionPageTotal(nil, 8)
	hooks := 0
	for _, row := range rows {
		if row.HooksOnly {
			hooks++
		}
	}
	if err != nil || total != 8 || hooks != 2 {
		t.Fatalf("all: total %d hooks-only %d err %v, want 8 and 2", total, hooks, err)
	}
}
