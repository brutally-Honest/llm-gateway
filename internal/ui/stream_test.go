package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

type streamEnv struct {
	*apiEnv
	hub *Hub
	ts  *httptest.Server
}

// newStreamEnv serves the real handler, store and hub on a real listener.
func newStreamEnv(t *testing.T, tune func(*Hub)) *streamEnv {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	ts := httptest.NewUnstartedServer(nil)
	d := Deps{Addr: ts.Listener.Addr().String(), Reader: r, Now: func() time.Time { return baseTime.Add(100 * time.Second) }}
	h := newHub(d, hubQueueSize)
	if tune != nil {
		tune(h)
	}
	h.start()
	d.Hub = h
	ts.Config.Handler = newRouter(d)
	ts.Start()
	t.Cleanup(func() { h.Close(); ts.Close() })
	return &streamEnv{apiEnv: &apiEnv{t: t, s: s, dir: dir, h: ts.Config.Handler}, hub: h, ts: ts}
}

func (e *streamEnv) open(path string) *sse {
	e.t.Helper()
	resp, err := http.Get(e.ts.URL + path)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = resp.Body.Close() })
	return &sse{t: e.t, resp: resp, sc: bufio.NewScanner(resp.Body)}
}

type sse struct {
	t    *testing.T
	resp *http.Response
	sc   *bufio.Scanner
}

type sseEvent struct{ event, id, data, comment string }

// next reads one event or comment block; it fails the test after 5 s.
func (s *sse) next() sseEvent {
	s.t.Helper()
	timer := time.AfterFunc(5*time.Second, func() { _ = s.resp.Body.Close() })
	defer timer.Stop()
	var ev sseEvent
	for s.sc.Scan() {
		line := s.sc.Text()
		switch {
		case line == "":
			return ev
		case strings.HasPrefix(line, ":"):
			ev.comment = strings.TrimSpace(line[1:])
		case strings.HasPrefix(line, "event: "):
			ev.event = line[7:]
		case strings.HasPrefix(line, "id: "):
			ev.id = line[4:]
		case strings.HasPrefix(line, "data: "):
			ev.data = line[6:]
		}
	}
	s.t.Fatalf("stream ended: %v", s.sc.Err())
	return ev
}

// nextData skips comments and returns the next real event.
func (s *sse) nextData() sseEvent {
	s.t.Helper()
	for {
		if ev := s.next(); ev.event != "" {
			return ev
		}
	}
}

func (e *streamEnv) commitA() {
	e.save("a", "k", "cc", 10)
	e.parseMsgs("a", "ok", []any{"user", text("hi")})
	e.hub.Publish(capture.Committed{RequestID: "a", PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "k"})
}

func TestStream_GlobalSendsLiveSessionEvent(t *testing.T) {
	e := newStreamEnv(t, nil)
	s := e.open("/api/stream")
	if ct := s.resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	e.commitA()
	ev := s.nextData()
	if ev.event != "session" {
		t.Fatalf("event = %q", ev.event)
	}
	var sum sessionSummary
	if err := json.Unmarshal([]byte(ev.data), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.ID != sid() || sum.ExchangeCount != 1 || sum.Client != "cc" {
		t.Fatalf("summary = %+v", sum)
	}
}

func TestStream_SessionSendsEntriesWithRowID(t *testing.T) {
	e := newStreamEnv(t, nil)
	s := e.open("/api/sessions/" + sid() + "/stream")
	e.commitA()
	ev := s.nextData()
	if ev.event != "entries" {
		t.Fatalf("event = %q", ev.event)
	}
	if ev.id != "1" { // the only stored exchange has row id 1
		t.Fatalf("id = %q, want 1", ev.id)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(ev.data), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0]["id"] != "a:0" || entries[1]["kind"] != "user_prompt" {
		t.Fatalf("entries = %v", entries)
	}
}

func TestStream_HeartbeatEveryInterval(t *testing.T) {
	e := newStreamEnv(t, func(h *Hub) { h.heartbeat = 20 * time.Millisecond })
	s := e.open("/api/stream")
	pings := 0
	for pings < 3 {
		if ev := s.next(); ev.comment == "ping" {
			pings++
		}
	}
	if heartbeatEvery > 15*time.Second {
		t.Fatalf("default heartbeat %s exceeds 15s", heartbeatEvery)
	}
}

func TestStream_33rdIs503(t *testing.T) {
	e := newStreamEnv(t, nil)
	for i := 0; i < maxStreams; i++ {
		e.open("/api/stream")
	}
	resp, err := http.Get(e.ts.URL + "/api/sessions/" + sid() + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 503 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil || env.Error.Code != "too_many_streams" {
		t.Fatalf("body = %+v %v", env, err)
	}
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type = %q", resp.Header.Get("Content-Type"))
	}
}

func TestStream_HubCloseEndsStreams(t *testing.T) {
	e := newStreamEnv(t, nil)
	s := e.open("/api/stream")
	s.next()
	e.hub.Close()
	for s.sc.Scan() {
	}
	if err := s.sc.Err(); err != nil {
		t.Fatalf("stream did not end cleanly: %v", err)
	}
}

// openWith opens path with extra request headers.
func (e *streamEnv) openWith(path string, hdr map[string]string) *sse {
	e.t.Helper()
	req, err := http.NewRequest("GET", e.ts.URL+path, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = resp.Body.Close() })
	return &sse{t: e.t, resp: resp, sc: bufio.NewScanner(resp.Body)}
}

func decodeEntries(t *testing.T, data string) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal([]byte(data), &out); err != nil {
		t.Fatalf("data %q: %v", data, err)
	}
	return out
}

// seedThree stores three exchanges of session k with parsed messages.
func (e *streamEnv) seedThree() {
	e.save("b", "k", "cc", 20)
	e.parseMsgs("b", "ok", []any{"user", text("q2")})
	e.save("a", "k", "cc", 10)
	e.parseMsgs("a", "ok", []any{"user", text("q1")}, []any{"assistant", text("r1")})
	e.save("c", "k", "cc", 30)
	e.parseMsgs("c", "ok", []any{"user", text("q3")})
}

// snapshotEntries reads n exchange events and returns their entries joined in order.
func snapshotEntries(t *testing.T, s *sse, n int) []map[string]any {
	t.Helper()
	var all []map[string]any
	for i := 0; i < n; i++ {
		ev := s.nextData()
		if ev.event != "exchange" || ev.id != "" {
			t.Fatalf("event %d = %q id %q, want exchange without id", i, ev.event, ev.id)
		}
		all = append(all, decodeEntries(t, ev.data)...)
	}
	return all
}

func TestStream_SnapshotEqualsFreshTimelineWithAndWithoutResumeInputs(t *testing.T) {
	e := newStreamEnv(t, nil)
	e.seedThree()
	want := e.freshTimeline()
	path := "/api/sessions/" + sid() + "/stream"
	for name, s := range map[string]*sse{
		"none":          e.open(path),
		"last-event-id": e.openWith(path, map[string]string{"Last-Event-ID": "2"}),
		"since":         e.open(path + "?since=2"),
	} {
		got := snapshotEntries(t, s, 3)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: snapshot = %v, want %v", name, got, want)
		}
	}
}

func TestStream_LastEventIDAndSinceValidatedNotNarrowing(t *testing.T) {
	e := newStreamEnv(t, nil)
	e.seedThree()
	path := "/api/sessions/" + sid() + "/stream"
	for name, c := range map[string]struct {
		path string
		hdr  map[string]string
	}{
		"header text":     {path, map[string]string{"Last-Event-ID": "x"}},
		"header negative": {path, map[string]string{"Last-Event-ID": "-1"}},
		"since text":      {path + "?since=abc", nil},
		"since empty":     {path + "?since=", nil},
		"global since":    {"/api/stream?since=1.5", nil},
	} {
		s := e.openWith(c.path, c.hdr)
		if s.resp.StatusCode != 400 {
			t.Fatalf("%s: status = %d", name, s.resp.StatusCode)
		}
		var env envelope
		if err := json.NewDecoder(s.resp.Body).Decode(&env); err != nil || env.Error.Code != "bad_request" {
			t.Fatalf("%s: body = %+v %v", name, env, err)
		}
	}
	// A cursor past every row id still gets the whole session.
	s := e.openWith(path+"?since=9999", map[string]string{"Last-Event-ID": "9999"})
	if got := snapshotEntries(t, s, 3); !reflect.DeepEqual(got, e.freshTimeline()) {
		t.Fatalf("narrowed snapshot: %v", got)
	}
}

func TestStream_GlobalSnapshotThenSessionEvent(t *testing.T) {
	e := newStreamEnv(t, nil)
	e.save("o", "old", "cc", 1)
	e.save("n", "new", "cc", 5)
	s := e.open("/api/stream")
	var keys []string
	for i := 0; i < 2; i++ {
		ev := s.nextData()
		if ev.event != "session" {
			t.Fatalf("event = %q", ev.event)
		}
		var sum sessionSummary
		if err := json.Unmarshal([]byte(ev.data), &sum); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, sum.SessionKey)
	}
	if !reflect.DeepEqual(keys, []string{"new", "old"}) {
		t.Fatalf("snapshot order = %v", keys)
	}
	e.commitA()
	ev := s.nextData()
	var sum sessionSummary
	if ev.event != "session" || json.Unmarshal([]byte(ev.data), &sum) != nil || sum.ID != sid() {
		t.Fatalf("live event = %+v", ev)
	}
}

func TestStream_CommitOutOfRowIDOrderStillReachesPage(t *testing.T) {
	e := newStreamEnv(t, nil)
	e.save("a", "k", "cc", 10) // row 1, commits last
	e.save("b", "k", "cc", 20) // row 2, commits first
	s := e.open("/api/sessions/" + sid() + "/stream")
	snapshotEntries(t, s, 2)
	pub := func(id string) {
		e.hub.Publish(capture.Committed{RequestID: id, PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "k"})
	}
	e.parseMsgs("b", "ok", []any{"user", text("second")})
	pub("b")
	if ev := s.nextData(); ev.event != "entries" || ev.id != "2" {
		t.Fatalf("first live = %+v", ev)
	}
	e.parseMsgs("a", "ok", []any{"user", text("first")})
	pub("a")
	ev := s.nextData()
	if ev.event != "entries" || ev.id != "1" {
		t.Fatalf("late commit with lower row id = %+v", ev)
	}
	if es := decodeEntries(t, ev.data); es[0]["exchange_id"] != "a" {
		t.Fatalf("entries = %v", es)
	}
}

func TestStream_CommitDuringSnapshotNotMissed(t *testing.T) {
	var e *streamEnv
	e = newStreamEnv(t, func(h *Hub) {
		orig := h.snap
		h.snap = func(ctx context.Context, session string) (snapshot, error) {
			// The stream has subscribed: this commit lands between subscribe and read.
			e.commitA()
			return orig(ctx, session)
		}
	})
	s := e.open("/api/sessions/" + sid() + "/stream")
	if ev := s.nextData(); ev.event != "exchange" {
		t.Fatalf("first = %+v", ev)
	}
	if ev := s.nextData(); ev.event != "entries" {
		t.Fatalf("live copy = %+v", ev)
	}
}

func TestStream_BackfilledExchangesAppearOnReconnectSnapshot(t *testing.T) {
	e := newStreamEnv(t, func(h *Hub) { h.heartbeat = 20 * time.Millisecond })
	e.save("u", "", "cc", 5) // ungrouped until backfill
	path := "/api/sessions/" + sid() + "/stream"
	s := e.open(path)
	e.save("u2", "k", "cc", 6) // backfill assigns it to the session; nothing is published
	e.parseMsgs("u2", "ok", []any{"user", text("old")})
	for i := 0; i < 3; i++ {
		if ev := s.next(); ev.event != "" {
			t.Fatalf("event at backfill: %+v", ev)
		}
	}
	got := e.open(path).nextData()
	if got.event != "exchange" || decodeEntries(t, got.data)[0]["exchange_id"] != "u2" {
		t.Fatalf("reconnect snapshot = %+v", got)
	}
}

func TestStream_SnapshotLargerThan64DoesNotDropSubscriber(t *testing.T) {
	e := newStreamEnv(t, nil)
	const n = 3 * subscriberBuffer
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("x%03d", i)
		e.save(id, "k", "cc", i)
		e.parseMsgs(id, "ok", []any{"user", text("q")})
	}
	s := e.open("/api/sessions/" + sid() + "/stream")
	snapshotEntries(t, s, n)
	e.commitA()
	if ev := s.nextData(); ev.event != "entries" {
		t.Fatalf("live after big snapshot = %+v", ev)
	}
}

func TestStream_SnapshotFailureSendsErrorAndLongRetry(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		code string
	}{
		"store error": {errors.New("disk /secret/path: select prompt-text-xyz"), "store_error"},
		"timeout":     {fmt.Errorf("query: %w", context.DeadlineExceeded), "timeout"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newStreamEnv(t, func(h *Hub) {
				h.snap = func(context.Context, string) (snapshot, error) { return snapshot{}, c.err }
			})
			e.seedThree()
			s := e.open("/api/sessions/" + sid() + "/stream")
			raw, err := io.ReadAll(s.resp.Body) // returns only because the stream is closed
			if err != nil {
				t.Fatal(err)
			}
			body := string(raw)
			if !strings.Contains(body, "retry: 30000\n") || !strings.Contains(body, "event: snapshot_error\n") {
				t.Fatalf("body = %q", body)
			}
			if strings.Contains(body, "event: exchange") {
				t.Fatalf("exchange sent after failure: %q", body)
			}
			if strings.Contains(body, "secret") || strings.Contains(body, "prompt-text") {
				t.Fatalf("message leaks detail: %q", body)
			}
			var env envelope
			for _, l := range strings.Split(body, "\n") {
				if d, ok := strings.CutPrefix(l, "data: "); ok {
					if err := json.Unmarshal([]byte(d), &env); err != nil {
						t.Fatal(err)
					}
				}
			}
			if env.Error.Code != c.code || env.Error.Message == "" {
				t.Fatalf("envelope = %+v", env)
			}
		})
	}
}

// freshTimeline is the session's timeline API entries, fetched over the test listener.
func (e *streamEnv) freshTimeline() []map[string]any {
	e.t.Helper()
	resp, err := http.Get(e.ts.URL + "/api/sessions/" + sid() + "/timeline")
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var p tlPage
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		e.t.Fatal(err)
	}
	return p.Entries
}
