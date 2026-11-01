package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

const addr = "127.0.0.1:7199"

func do(t *testing.T, h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://"+addr+path, nil)
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newTestRouter(extra ...func(chi.Router)) http.Handler {
	return newRouter(Deps{Addr: addr}, extra...)
}

func wantEnvelope(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (%s)", rec.Code, status, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	want := `{"error":{"code":"` + code + `",`
	if !strings.HasPrefix(rec.Body.String(), want) {
		t.Errorf("body = %s, want prefix %s", rec.Body, want)
	}
}

func TestUI_BadHostIs421(t *testing.T) {
	h := newTestRouter()
	for _, p := range []string{"/", "/api/health", "/index.html"} {
		rec := do(t, h, "GET", p, map[string]string{"Host": "evil.example:7199"})
		wantEnvelope(t, rec, 421, "bad_host")
		if strings.Contains(rec.Body.String(), "Gateway") {
			t.Errorf("%s served content on a bad host", p)
		}
	}
	if rec := do(t, h, "GET", "/api/health", nil); rec.Code != 200 {
		t.Errorf("good host = %d", rec.Code)
	}
}

func TestUI_BadOriginIs403NoCORS(t *testing.T) {
	h := newTestRouter()
	rec := do(t, h, "GET", "/api/health", map[string]string{"Origin": "http://evil.example"})
	wantEnvelope(t, rec, 403, "bad_origin")
	for _, o := range []string{"http://" + addr, "http://localhost:7199"} {
		rec = do(t, h, "GET", "/api/health", map[string]string{"Origin": o})
		if rec.Code != 200 {
			t.Errorf("origin %s = %d", o, rec.Code)
		}
	}
	for _, o := range []string{"null", "https://" + addr} {
		if rec = do(t, h, "GET", "/api/health", map[string]string{"Origin": o}); rec.Code != 403 {
			t.Errorf("origin %s = %d, want 403", o, rec.Code)
		}
	}
	for _, rec := range []*httptest.ResponseRecorder{rec, do(t, h, "GET", "/api/health", map[string]string{"Origin": "http://" + addr})} {
		for k := range rec.Header() {
			if strings.HasPrefix(strings.ToLower(k), "access-control-") {
				t.Errorf("CORS header %s present", k)
			}
		}
	}
}

func TestUI_SecurityHeadersOnEveryResponse(t *testing.T) {
	h := newTestRouter(func(r chi.Router) { r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("x") }) })
	cases := []struct {
		method, path string
		hdr          map[string]string
	}{
		{"GET", "/", nil},
		{"GET", "/index.html", nil},
		{"GET", "/missing.js", nil},
		{"GET", "/api/health", nil},
		{"GET", "/api/nope", nil},
		{"POST", "/api/health", nil},
		{"GET", "/api/boom", nil},
		{"GET", "/", map[string]string{"Host": "evil.example"}},
		{"GET", "/", map[string]string{"Origin": "http://evil.example"}},
	}
	for _, c := range cases {
		rec := do(t, h, c.method, c.path, c.hdr)
		if got := rec.Header().Get("Content-Security-Policy"); got != csp {
			t.Errorf("%s %s CSP = %q", c.method, c.path, got)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s %s missing nosniff/referrer headers", c.method, c.path)
		}
	}
}

func TestUI_PanicRecoveredKeepsServing(t *testing.T) {
	h := newTestRouter(func(r chi.Router) {
		r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("x") })
		r.Get("/half", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("partial"))
			panic("y")
		})
	})
	wantEnvelope(t, do(t, h, "GET", "/api/boom", nil), 500, "internal")
	if rec := do(t, h, "GET", "/api/half", nil); rec.Code != 200 || rec.Body.String() != "partial" {
		t.Errorf("half-written = %d %q", rec.Code, rec.Body)
	}
	if rec := do(t, h, "GET", "/api/health", nil); rec.Code != 200 {
		t.Errorf("after panic health = %d", rec.Code)
	}
}

func TestAPI_NonGETIs405WithAllow(t *testing.T) {
	h := newTestRouter()
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"} {
		for _, p := range []string{"/api/health", "/api/unknown"} {
			rec := do(t, h, m, p, nil)
			if rec.Code != 405 || rec.Header().Get("Allow") != "GET" {
				t.Errorf("%s %s = %d allow=%q", m, p, rec.Code, rec.Header().Get("Allow"))
			}
			if m != "HEAD" {
				wantEnvelope(t, rec, 405, "method_not_allowed")
			}
		}
	}
}

func TestUI_StaticNonGETIs405(t *testing.T) {
	h := newTestRouter()
	for _, m := range []string{"POST", "PUT", "DELETE", "HEAD"} {
		for _, p := range []string{"/", "/index.html"} {
			rec := do(t, h, m, p, nil)
			if rec.Code != 405 || rec.Header().Get("Allow") != "GET" {
				t.Errorf("%s %s = %d allow=%q", m, p, rec.Code, rec.Header().Get("Allow"))
			}
		}
	}
	if rec := do(t, h, "GET", "/", nil); rec.Code != 200 {
		t.Errorf("GET / = %d", rec.Code)
	}
}

func TestAPI_Health(t *testing.T) {
	rec := do(t, newTestRouter(), "GET", "/api/health", nil)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Errorf("health = %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("content-type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestUI_StoreTimeoutIs504(t *testing.T) {
	h := newTestRouter(func(r chi.Router) {
		r.Get("/slow", func(w http.ResponseWriter, _ *http.Request) { writeStoreErr(w, context.DeadlineExceeded) })
		r.Get("/bad", func(w http.ResponseWriter, _ *http.Request) { writeStoreErr(w, http.ErrBodyNotAllowed) })
		r.Get("/gone", func(w http.ResponseWriter, _ *http.Request) { writeStoreErr(w, context.Canceled) })
	})
	wantEnvelope(t, do(t, h, "GET", "/api/slow", nil), 504, "timeout")
	rec := do(t, h, "GET", "/api/bad", nil)
	wantEnvelope(t, rec, 500, "store_error")
	if strings.Contains(rec.Body.String(), "body") {
		t.Errorf("store error text leaked: %s", rec.Body)
	}
	if rec := do(t, h, "GET", "/api/gone", nil); rec.Body.Len() != 0 {
		t.Errorf("cancelled request got a body: %s", rec.Body)
	}
}

func TestUI_AllLoopbackHostFormsAccepted(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:7199", "127.0.0.2:7199"} {
		h := newRouter(Deps{Addr: listen})
		for _, host := range []string{"127.0.0.1:7199", "localhost:7199", "[::1]:7199", listen} {
			if rec := do(t, h, "GET", "/api/health", map[string]string{"Host": host}); rec.Code != 200 {
				t.Errorf("listen %s: host %s = %d, want 200", listen, host, rec.Code)
			}
		}
		wantEnvelope(t, do(t, h, "GET", "/api/health", map[string]string{"Host": "evil.example:7199"}), 421, "bad_host")
	}
}

func TestUI_LoopbackHostWrongPortIs421(t *testing.T) {
	h := newRouter(Deps{Addr: "127.0.0.2:7199"})
	for _, host := range []string{"127.0.0.1", "localhost", "[::1]", "127.0.0.2"} {
		for _, bad := range []string{host + ":7200", host} {
			wantEnvelope(t, do(t, h, "GET", "/api/health", map[string]string{"Host": bad}), 421, "bad_host")
		}
	}
}

func TestUI_NonBodyResponsesSendStandardCSP(t *testing.T) {
	e := newAPIEnv(t)
	e.save("a", "k", "cc", 1)
	r, err := store.OpenReader(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	h := newRouter(Deps{Addr: addr, Reader: r, Now: func() time.Time { return baseTime }},
		func(r chi.Router) { r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("x") }) })
	cases := []struct {
		name, method, path string
		hdr                map[string]string
		code               int
	}{
		{"page", "GET", "/", nil, 200},
		{"asset redirect", "GET", "/index.html", nil, 301},
		{"missing asset", "GET", "/missing.js", nil, 404},
		{"api", "GET", "/api/health", nil, 200},
		{"api sessions", "GET", "/api/sessions", nil, 200},
		{"api exchange", "GET", "/api/exchanges/a", nil, 200},
		{"api 404", "GET", "/api/nope", nil, 404},
		{"api 405", "POST", "/api/health", nil, 405},
		{"static 405", "POST", "/", nil, 405},
		{"421", "GET", "/", map[string]string{"Host": "evil.example"}, 421},
		{"403", "GET", "/api/health", map[string]string{"Origin": "http://evil.example"}, 403},
		{"500", "GET", "/api/boom", nil, 500},
	}
	for _, c := range cases {
		rec := do(t, h, c.method, c.path, c.hdr)
		if rec.Code != c.code {
			t.Errorf("%s: status = %d, want %d", c.name, rec.Code, c.code)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != csp {
			t.Errorf("%s: CSP = %q, want standard", c.name, got)
		}
	}
}
