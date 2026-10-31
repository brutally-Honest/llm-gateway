package ui

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	return &streamEnv{apiEnv: &apiEnv{t: t, s: s, dir: dir}, hub: h, ts: ts}
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
