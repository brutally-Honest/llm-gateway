package hooks_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/hooks"
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
