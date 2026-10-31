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
