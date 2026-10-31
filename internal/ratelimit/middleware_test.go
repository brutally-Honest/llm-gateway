package ratelimit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

type fakeAdapter struct{}

func (fakeAdapter) Name() string                       { return "fake" }
func (fakeAdapter) Prefix() string                     { return "/f" }
func (fakeAdapter) DefaultBaseURL() string             { return "http://x" }
func (fakeAdapter) AuthKind(http.Header) core.AuthKind { return core.AuthNone }
func (fakeAdapter) ErrorBody(string) (string, []byte)  { return "application/json", []byte("{}") }
func (fakeAdapter) IsModelCall(method, path string) bool {
	return method == http.MethodPost && path == "/v1/messages"
}
func (fakeAdapter) LimitErrorBody(k core.LimitErrorKind, msg string) (string, []byte) {
	typ := "rate_limit_error"
	if k == core.LimitPermission {
		typ = "permission_error"
	}
	b, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": typ, "message": msg}})
	return "application/json", b
}
func (fakeAdapter) UsageReader(int, http.Header, bool) core.UsageReader { return nil }

type rig struct {
	h       http.Handler
	forward int
	logs    *observer.ObservedLogs
	now     time.Time
	meta    *core.Meta
	status  int
}

func newRig(l Limiter, status int) *rig {
	core0, logs := observer.New(zap.InfoLevel)
	rg := &rig{logs: logs, now: t0.Add(30*time.Second + 500*time.Millisecond), status: status}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rg.forward++
		rg.meta = core.MetaFrom(r.Context())
		w.WriteHeader(rg.status)
	})
	rg.h = middleware(l, fakeAdapter{}, zap.New(core0), func() time.Time { return rg.now })(next)
	return rg
}

func (rg *rig) do(method, path, key string, set bool) *httptest.ResponseRecorder {
	ctx, m := core.WithMeta(context.Background())
	m.RequestID = "rid-1"
	req := httptest.NewRequest(method, path, nil).WithContext(ctx)
	if set {
		req.Header.Set("X-Gateway-Key", key)
	}
	rec := httptest.NewRecorder()
	rg.h.ServeHTTP(rec, req)
	return rec
}

func errType(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error struct{ Type, Message string }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	return e.Error.Type
}

const msgPath = "/f/v1/messages"

func TestMiddleware_MissingOrEmptyKey_403Permission(t *testing.T) {
	rg := newRig(NewFixedWindowMemory(cfgWith(5, 0)), 200)
	for _, set := range []bool{false, true} {
		rec := rg.do("POST", msgPath, "", set)
		if rec.Code != 403 || errType(t, rec) != "permission_error" {
			t.Fatalf("set=%v: %d %s", set, rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), "x-gateway-key") || strings.Contains(rec.Body.String(), "upstream") {
			t.Fatalf("message: %s", rec.Body)
		}
	}
	if rg.forward != 0 {
		t.Fatal("forwarded")
	}
}

func TestMiddleware_UnknownKey_403Permission(t *testing.T) {
	rl := NewFixedWindowMemory(cfgWith(5, 0))
	rg := newRig(rl, 200)
	for _, k := range []string{"nope", " k", "K", "k "} {
		rec := rg.do("POST", msgPath, k, true)
		if rec.Code != 403 || errType(t, rec) != "permission_error" {
			t.Fatalf("%q: %d", k, rec.Code)
		}
		if k != "" && strings.Contains(rec.Body.String(), "nope") {
			t.Fatal("echoed key")
		}
	}
	if rg.forward != 0 {
		t.Fatal("forwarded")
	}
	if rg.do("POST", msgPath, "k", true).Code != 200 {
		t.Fatal("exact key rejected")
	}
}

func TestMiddleware_NonModelCall_NotRequiredNotCounted(t *testing.T) {
	rg := newRig(NewFixedWindowMemory(cfgWith(1, 0)), 200)
	for i := 0; i < 3; i++ {
		if rg.do("POST", "/f/v1/messages/count_tokens", "", false).Code != 200 || rg.do("GET", "/f/v1/models", "k", true).Code != 200 {
			t.Fatal("blocked")
		}
	}
	if rg.do("POST", msgPath, "k", true).Code != 200 {
		t.Fatal("non-model calls were counted")
	}
	if rg.meta.OnUsage == nil {
		t.Fatal("admitted call not observed")
	}
}

func TestMiddleware_RPMExceeded_429NotForwarded(t *testing.T) {
	rg := newRig(NewFixedWindowMemory(cfgWith(2, 0)), 200)
	rg.do("POST", msgPath, "k", true)
	rg.do("POST", msgPath, "k", true)
	rec := rg.do("POST", msgPath, "k", true)
	if rec.Code != 429 || rg.forward != 2 {
		t.Fatalf("%d forwarded %d", rec.Code, rg.forward)
	}
}

func TestMiddleware_TPMExceeded_429NotForwarded(t *testing.T) {
	rl := NewFixedWindowMemory(cfgWith(0, 100))
	rg := newRig(rl, 200)
	rg.do("POST", msgPath, "k", true)
	rg.meta.OnUsage(core.UsageResult{Tokens: 100, OK: true, Status: 200})
	rec := rg.do("POST", msgPath, "k", true)
	if rec.Code != 429 || rg.forward != 1 || rec.Header().Get("X-Gateway-Ratelimit") != "tokens" {
		t.Fatalf("%d forwarded %d", rec.Code, rg.forward)
	}
}

func TestMiddleware_429Response_HeadersAndEnvelope(t *testing.T) {
	rg := newRig(NewFixedWindowMemory(cfgWith(1, 0)), 200)
	rg.do("POST", msgPath, "k", true)
	rec := rg.do("POST", msgPath, "k", true)
	h := rec.Header()
	reset := t0.Add(time.Minute).UTC().Format(time.RFC3339)
	if rec.Code != 429 || errType(t, rec) != "rate_limit_error" ||
		h.Get("Retry-After") != "30" || h.Get("X-Gateway-Ratelimit") != "requests" ||
		h.Get("X-Gateway-Ratelimit-Limit") != "1" || h.Get("X-Gateway-Ratelimit-Remaining") != "0" ||
		h.Get("X-Gateway-Ratelimit-Reset") != reset {
		t.Fatalf("%d %v %s", rec.Code, h, rec.Body)
	}
	for _, want := range []string{`\"k\"`, "requests", "1", "30"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("message lacks %q: %s", want, rec.Body)
		}
	}
}

func TestRetryAfter_RoundsUpMinOne(t *testing.T) {
	n := t0
	for _, c := range []struct {
		reset time.Time
		want  int64
	}{
		{n.Add(1500 * time.Millisecond), 2}, {n.Add(time.Second), 1}, {n.Add(time.Millisecond), 1},
		{n, 1}, {n.Add(-time.Second), 1}, {n.Add(59*time.Second + time.Nanosecond), 60},
	} {
		if got := retryAfterSeconds(c.reset, n); got != c.want {
			t.Errorf("%v: got %d want %d", c.reset.Sub(n), got, c.want)
		}
	}
}

func TestMiddleware_AdmittedCallCountedEvenIfUpstreamFails(t *testing.T) {
	rg := newRig(NewFixedWindowMemory(cfgWith(2, 0)), 502)
	rg.do("POST", msgPath, "k", true)
	rg.meta.OnUsage(core.UsageResult{Status: 502})
	rg.do("POST", msgPath, "k", true)
	if rg.do("POST", msgPath, "k", true).Code != 429 {
		t.Fatal("failed calls not counted")
	}
}

func TestMiddleware_NoUsage2xx_WarnsNoTokens(t *testing.T) {
	f := &fakeLimiter{}
	rg := newRig(f, 200)
	rg.do("POST", msgPath, "k", true)
	rg.meta.OnUsage(core.UsageResult{Status: 200})
	if f.recorded != 0 || rg.logs.FilterMessage("usage_missing").Len() != 1 {
		t.Fatalf("recorded=%d logs=%v", f.recorded, rg.logs.All())
	}
}

func TestMiddleware_Upstream5xx_NoTokensNoWarn(t *testing.T) {
	f := &fakeLimiter{}
	rg := newRig(f, 500)
	rg.do("POST", msgPath, "k", true)
	rg.meta.OnUsage(core.UsageResult{Status: 500})
	if f.recorded != 0 || rg.logs.Len() != 0 {
		t.Fatalf("recorded=%d logs=%v", f.recorded, rg.logs.All())
	}
}

func TestMiddleware_RejectionLogsOneLine(t *testing.T) {
	rg := newRig(NewFixedWindowMemory(cfgWith(1, 0)), 200)
	rg.do("POST", msgPath, "", false)
	rg.do("POST", msgPath, "zz", true)
	rg.do("POST", msgPath, "k", true)
	rg.do("POST", msgPath, "k", true)
	if rg.logs.Len() != 3 {
		t.Fatalf("%v", rg.logs.All())
	}
	e := rg.logs.All()
	m0, m1, m2 := e[0].ContextMap(), e[1].ContextMap(), e[2].ContextMap()
	if e[0].Message != "ratelimit_rejected" || m0["reason"] != "missing_key" || m0["key"] != "absent" || m0["request_id"] != "rid-1" {
		t.Fatalf("%v", m0)
	}
	if m1["reason"] != "unknown_key" {
		t.Fatalf("%v", m1)
	}
	if m2["reason"] != "requests" || m2["key"] != "k" || m2["retry_after"] != int64(30) {
		t.Fatalf("%v", m2)
	}
}

type fakeLimiter struct {
	mu       sync.Mutex
	dec      Decision
	admitted int
	recorded int64
}

func (f *fakeLimiter) Admit(string, time.Time) Decision {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.admitted++
	return f.dec
}
func (f *fakeLimiter) Record(_ string, n int64, _ time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded += n
}

func TestMiddleware_WorksAgainstFakeLimiter(t *testing.T) {
	f := &fakeLimiter{}
	rg := newRig(f, 200)
	if rg.do("POST", msgPath, "any", true).Code != 200 || rg.forward != 1 {
		t.Fatal("not admitted")
	}
	rg.meta.OnUsage(core.UsageResult{Tokens: 7, OK: true, Status: 200})
	if f.recorded != 7 {
		t.Fatalf("recorded %d", f.recorded)
	}
	f.dec = Decision{Outcome: Limited, Dimension: DimTokens, Limit: 9, Reset: rg.now.Add(3 * time.Second)}
	rec := rg.do("POST", msgPath, "any", true)
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "3" || rg.forward != 1 {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	f.dec = Decision{Outcome: UnknownKey}
	if rg.do("POST", msgPath, "any", true).Code != 403 {
		t.Fatal("want 403")
	}
}

func TestMiddleware_Upstream429NoGatewayHeaders(t *testing.T) {
	f := &fakeLimiter{}
	rg := newRig(f, 429)
	rec := rg.do("POST", msgPath, "k", true)
	rg.meta.OnUsage(core.UsageResult{Status: 429})
	for name := range rec.Header() {
		if strings.HasPrefix(strings.ToLower(name), "x-gateway-ratelimit") {
			t.Fatalf("added %s", name)
		}
	}
	if rec.Code != 429 || f.recorded != 0 || rg.logs.Len() != 0 {
		t.Fatalf("%d %d %v", rec.Code, f.recorded, rg.logs.All())
	}
}
