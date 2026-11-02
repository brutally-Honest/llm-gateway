package hooks_test

import (
	"bytes"
	"cmp"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/hooks"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

func newHandler(t *testing.T, st *fakeStore, q int, max int64, on bool) (http.Handler, *hooks.Sink, *bytes.Buffer) {
	t.Helper()
	log, buf, _ := logger()
	s := hooks.NewSink(cfg(q, 1, 1<<20, findMapper("c", toolMapper)), st, log)
	t.Cleanup(func() { closeSink(t, s) })
	r := chi.NewRouter()
	hooks.NewHandler(s, log, max, on).Mount(r)
	return r, s, buf
}

func post(h http.Handler, host, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/_gateway/hooks/c", strings.NewReader(body))
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func waitRecs(t *testing.T, st *fakeStore, n int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if len(st.records()) >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("records = %d, want %d", len(st.records()), n)
}

func TestHandler_LoopbackPostAnswers200Empty(t *testing.T) {
	st := &fakeStore{}
	h, _, _ := newHandler(t, st, 4, 1024, true)
	for _, host := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080", "127.0.0.1"} {
		w := post(h, host, `{"a":1}`, nil)
		if w.Code != 200 || w.Body.Len() != 0 {
			t.Fatalf("%s: %d %q", host, w.Code, w.Body.String())
		}
	}
	waitRecs(t, st, 4)
	if string(st.records()[0].Payload) != `{"a":1}` {
		t.Fatal("payload altered")
	}
}

func TestHandler_AnswersBeforeStorageFinishes(t *testing.T) {
	st := &fakeStore{gate: make(chan struct{}), started: make(chan struct{})}
	h, _, _ := newHandler(t, st, 4, 1024, true)
	done := make(chan int)
	go func() { done <- post(h, "127.0.0.1", `{}`, nil).Code }()
	select {
	case c := <-done:
		if c != 200 {
			t.Fatal(c)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler waited on storage")
	}
	close(st.gate)
}

func TestHandler_NonLoopbackHost403(t *testing.T) {
	st := &fakeStore{}
	h, s, _ := newHandler(t, st, 4, 1024, true)
	for _, host := range []string{"example.com", "10.0.0.5:80", "evil.localhost.com", ""} {
		if c := post(h, host, `{}`, nil).Code; c != 403 {
			t.Fatalf("%q: %d", host, c)
		}
	}
	closeSink(t, s)
	if len(st.records()) != 0 {
		t.Fatal("stored")
	}
}

func TestHandler_OriginOrSecFetchSite403(t *testing.T) {
	st := &fakeStore{}
	h, s, _ := newHandler(t, st, 4, 1024, true)
	for _, hdr := range []map[string]string{{"Origin": "http://evil.com"}, {"Sec-Fetch-Site": "cross-site"}} {
		w := post(h, "127.0.0.1", `{}`, hdr)
		if w.Code != 403 {
			t.Fatalf("%v: %d", hdr, w.Code)
		}
	}
	closeSink(t, s)
	if len(st.records()) != 0 {
		t.Fatal("stored")
	}
}

func TestHandler_NonPostIs405(t *testing.T) {
	st := &fakeStore{}
	h, s, _ := newHandler(t, st, 4, 1024, true)
	for _, m := range []string{"GET", "PUT", "DELETE", "HEAD"} {
		req := httptest.NewRequest(m, "/_gateway/hooks/c", nil)
		req.Host = "127.0.0.1"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 405 || w.Header().Get("Allow") != "POST" {
			t.Fatalf("%s: %d", m, w.Code)
		}
	}
	closeSink(t, s)
	if len(st.records()) != 0 {
		t.Fatal("stored")
	}
}

func TestHandler_FullQueueStillAnswers200(t *testing.T) {
	st := &fakeStore{gate: make(chan struct{}), started: make(chan struct{})}
	h, s, _ := newHandler(t, st, 1, 1024, true)
	post(h, "127.0.0.1", `{}`, nil)
	<-st.started
	post(h, "127.0.0.1", `{}`, nil)
	w := post(h, "127.0.0.1", `{}`, nil)
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("%d", w.Code)
	}
	if s.Counts().QueueFull != 1 {
		t.Fatalf("QueueFull = %d", s.Counts().QueueFull)
	}
	close(st.gate)
}

func TestHandler_OversizeBody200NotStoredCounted(t *testing.T) {
	st := &fakeStore{}
	h, s, buf := newHandler(t, st, 4, 8, true)
	big := `{"k":"` + strings.Repeat("S3CRET", 10) + `"}`
	w := post(h, "127.0.0.1", big, nil)
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("%d", w.Code)
	}
	// exactly at the limit is stored
	post(h, "127.0.0.1", `12345678`, nil)
	waitRecs(t, st, 1)
	closeSink(t, s)
	if len(st.records()) != 1 || s.Counts().Oversize != 1 {
		t.Fatalf("recs=%d oversize=%d", len(st.records()), s.Counts().Oversize)
	}
	if !strings.Contains(buf.String(), "over limit") || strings.Contains(buf.String(), "S3CRET") {
		t.Fatalf("log: %s", buf.String())
	}
}

func TestHandler_CaptureOffCounts(t *testing.T) {
	st := &fakeStore{}
	h, s, _ := newHandler(t, st, 4, 1024, false)
	w := post(h, "127.0.0.1", `{}`, nil)
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("%d", w.Code)
	}
	closeSink(t, s)
	if len(st.records()) != 0 || s.Counts().CaptureOff != 1 {
		t.Fatalf("recs=%d off=%d", len(st.records()), s.Counts().CaptureOff)
	}
}

// reorderStore holds each save until the test opens its gate, so the test decides the
// order in which the workers finish. Entry order is announced on entered; each finished
// record is announced on finished after it is stored.
type reorderStore struct {
	entered  chan string
	finished chan *store.HookRecord
	gates    map[string]chan struct{}
}

func (f *reorderStore) SaveHook(ctx context.Context, h *store.HookRecord) error {
	p := string(h.Payload)
	f.entered <- p
	select {
	case <-f.gates[p]:
	case <-ctx.Done():
		return ctx.Err()
	}
	f.finished <- h
	return nil
}

// Events posted in order keep their posting order in the timeline even when the
// writes finish out of order with several workers.
func TestHandler_SeqFollowsPostingOrderWhenWritesFinishOutOfOrder(t *testing.T) {
	const n = 4
	st := &reorderStore{entered: make(chan string, n), finished: make(chan *store.HookRecord, n),
		gates: map[string]chan struct{}{}}
	for i := range n {
		st.gates[strconv.Itoa(i)] = make(chan struct{})
	}
	log, _, _ := logger()
	m := mapperFunc(func(b []byte) core.HarnessEvent {
		return core.HarnessEvent{Phase: core.PhaseTurnEnd, SessionKey: "s", SourceName: "src-" + string(b)}
	})
	s := hooks.NewSink(hooks.Config{QueueSize: 8, Workers: n, MemoryLimit: 1 << 20, Mappers: findMapper("c", m)}, st, log)
	defer closeSink(t, s)
	r := chi.NewRouter()
	hooks.NewHandler(s, log, 1024, true).Mount(r)
	for i := range n {
		if w := post(r, "127.0.0.1", strconv.Itoa(i), nil); w.Code != 200 {
			t.Fatalf("post %d: %d", i, w.Code)
		}
	}
	for range n {
		select {
		case <-st.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("the workers did not all start a write")
		}
	}
	var done []*store.HookRecord
	for i := n - 1; i >= 0; i-- { // finish the writes one at a time, last posted first
		close(st.gates[strconv.Itoa(i)])
		select {
		case h := <-st.finished:
			done = append(done, h)
		case <-time.After(5 * time.Second):
			t.Fatal("a released write did not finish")
		}
	}
	if string(done[0].Payload) != strconv.Itoa(n-1) {
		t.Fatalf("first finished payload %q, want %d", done[0].Payload, n-1)
	}
	written := slices.Clone(done) // finish order, kept before the sort below
	slices.SortStableFunc(done, func(a, b *store.HookRecord) int { return cmp.Compare(a.Seq, b.Seq) })
	for i, h := range done {
		if string(h.Payload) != strconv.Itoa(i) {
			t.Fatalf("timeline position %d holds payload %q, want %d", i, h.Payload, i)
		}
	}

	// The timeline, built from a real store: the same events saved in the order their
	// writes finished, all with one receive time, so only seq can order them.
	dir := t.TempDir()
	ss, err := store.Open(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(100, 0)
	for _, h := range written {
		cp := *h
		cp.ReceivedAt = at
		if err := ss.SaveHook(t.Context(), &cp); err != nil {
			t.Fatal(err)
		}
	}
	if err := ss.Close(); err != nil {
		t.Fatal(err)
	}
	rd, err := store.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rd.Close() }()
	session := store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "c", SessionKey: "s"}
	entries, err := timeline.Build(rd, session, timeline.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != n {
		t.Fatalf("timeline has %d entries, want %d", len(entries), n)
	}
	for i, e := range entries {
		if e.Kind != timeline.KindHarness || e.Text != "src-"+strconv.Itoa(i) {
			t.Fatalf("timeline entry %d = %s %q, want harness src-%d", i, e.Kind, e.Text, i)
		}
	}
}

func stampHandler(t *testing.T, workers int, clock func() time.Time) (http.Handler, *fakeStore) {
	t.Helper()
	st := &fakeStore{}
	log, _, _ := logger()
	s := hooks.NewSink(hooks.Config{QueueSize: 64, Workers: workers, MemoryLimit: 1 << 20, Mappers: findMapper("c", toolMapper)}, st, log)
	t.Cleanup(func() { closeSink(t, s) })
	h := hooks.NewHandler(s, log, 1024, true)
	h.SetClock(clock)
	r := chi.NewRouter()
	h.Mount(r)
	return r, st
}

func assertAligned(t *testing.T, recs []*store.HookRecord) {
	t.Helper()
	slices.SortFunc(recs, func(a, b *store.HookRecord) int { return cmp.Compare(a.Seq, b.Seq) })
	for i := 1; i < len(recs); i++ {
		if recs[i].ReceivedAt.Before(recs[i-1].ReceivedAt) {
			t.Fatalf("seq %d has time %v before seq %d time %v", recs[i].Seq, recs[i].ReceivedAt, recs[i-1].Seq, recs[i-1].ReceivedAt)
		}
	}
}

func TestHandler_BackwardsClockKeepsReceiveTimeNonDecreasing(t *testing.T) {
	base := time.Unix(1000, 0)
	times := []time.Time{base.Add(5 * time.Second), base, base.Add(2 * time.Second), base.Add(9 * time.Second)}
	var i int
	h, st := stampHandler(t, 1, func() time.Time { i++; return times[i-1] })
	for range times {
		if w := post(h, "127.0.0.1", `{}`, nil); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	waitRecs(t, st, len(times))
	recs := st.records()
	assertAligned(t, recs)
	if !recs[1].ReceivedAt.Equal(times[0]) {
		t.Fatalf("stepped-back time stored as %v, want %v", recs[1].ReceivedAt, times[0])
	}
}

func TestHandler_BlockedClockKeepsSeqAndTimeOrderAligned(t *testing.T) {
	base := time.Unix(1000, 0)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h, st := stampHandler(t, 2, func() time.Time {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return base.Add(10 * time.Second) // later than request 2's reading
		}
		return base
	})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); post(h, "127.0.0.1", `{"n":1}`, nil) }()
	<-entered
	go func() { defer wg.Done(); post(h, "127.0.0.1", `{"n":2}`, nil) }()
	time.Sleep(200 * time.Millisecond) // request 2 has started and reaches its stamp
	close(release)
	wg.Wait()
	waitRecs(t, st, 2)
	assertAligned(t, st.records())
}

func TestHandler_ConcurrentPostsOutOfOrderWritesKeepSeqAndTimeAligned(t *testing.T) {
	var n atomic.Int64
	base := time.Unix(1000, 0)
	h, st := stampHandler(t, 4, func() time.Time {
		if v := n.Add(1); v%3 == 0 {
			return base.Add(time.Duration(-v) * time.Millisecond) // steps back now and then
		}
		return base.Add(time.Duration(n.Load()) * time.Millisecond)
	})
	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() { defer wg.Done(); post(h, "127.0.0.1", `{}`, nil) }()
	}
	wg.Wait()
	waitRecs(t, st, 40)
	assertAligned(t, st.records())
}
