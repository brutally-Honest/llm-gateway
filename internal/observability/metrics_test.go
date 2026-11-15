package observability

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusClass(t *testing.T) {
	for status, want := range map[int]string{0: "none", 99: "none", 100: "1xx", 200: "2xx", 299: "2xx",
		304: "3xx", 404: "4xx", 499: "4xx", 503: "5xx", 599: "5xx", 600: "none"} {
		if got := StatusClass(status); got != want {
			t.Errorf("StatusClass(%d) = %q, want %q", status, got, want)
		}
	}
}

// A nil *Metrics is metrics off: every method is a no-op and /metrics is a 404.
func TestNilMetricsIsOff(t *testing.T) {
	var m *Metrics
	m.Request(ListenerAPI, 200, true, time.Second)
	m.Proxied(Upstream{Protocol: "p", Answered: true, ErrorKind: UpstreamTimeout})
	m.StoreWrite(StoreOpExchange, true, time.Millisecond)
	m.ViewBuild(true, time.Millisecond)
	m.Warmup(time.Second)
	m.GaugeFunc("g", "h", func() float64 { return 1 })
	m.LastShutdown(LastShutdown{})
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("nil Handler = %d, want 404", rec.Code)
	}
	RequestMetrics(nil, ListenerUI)(http.NotFoundHandler()).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func scrapeText(t *testing.T, m *Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	b, _ := io.ReadAll(rec.Body)
	return string(b)
}

func TestMetricsRecordWithLabels(t *testing.T) {
	m := NewMetrics("v1.2.3")
	m.Request(ListenerUI, 404, false, time.Millisecond)
	m.Proxied(Upstream{Protocol: "anthropic", Stream: true, Answered: true, TTFB: time.Millisecond,
		Total: time.Second, HasOverhead: true, Overhead: time.Microsecond, ErrorKind: UpstreamAborted})
	m.StoreWrite(StoreOpParse, false, time.Millisecond)
	m.ViewBuild(false, time.Millisecond)
	m.Warmup(2 * time.Second)
	h := RequestMetrics(m, ListenerUI)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	text := scrapeText(t, m)
	for _, want := range []string{
		`gateway_http_requests_total{listener="ui",proxied="false",status_class="4xx"} 2`,
		`gateway_upstream_errors_total{kind="aborted",protocol="anthropic"} 1`,
		`gateway_upstream_ttfb_seconds_count{protocol="anthropic",stream="true"} 1`,
		`gateway_upstream_duration_seconds_count{protocol="anthropic",stream="true"} 1`,
		`gateway_proxy_overhead_seconds_count{protocol="anthropic"} 1`,
		`gateway_store_write_seconds_count{op="parse",outcome="error"} 1`,
		`gateway_ui_view_build_seconds_count{cache="cold"} 1`,
		`gateway_ui_warmup_seconds 2`,
		`gateway_build_info{go_version="`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("scrape has no %q", want)
		}
	}
}

// An unanswered call records no upstream timing.
func TestProxiedUnansweredRecordsNoTiming(t *testing.T) {
	m := NewMetrics("dev")
	m.Proxied(Upstream{Protocol: "anthropic", ErrorKind: UpstreamUnreachable})
	text := scrapeText(t, m)
	if strings.Contains(text, "gateway_upstream_ttfb_seconds_count") {
		t.Error("ttfb observed for a call upstream never answered")
	}
	if !strings.Contains(text, `gateway_upstream_errors_total{kind="unreachable",protocol="anthropic"} 1`) {
		t.Error("unreachable not counted")
	}
}

func TestLastShutdownRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := LoadLastShutdown(dir); ok || err != nil {
		t.Fatalf("empty dir: ok=%v err=%v, want none", ok, err)
	}
	at := time.Unix(1_800_000_000, 0).UTC()
	want := LastShutdown{At: at, Duration: 0.25, CallsCut: 2, TunnelsCut: 3}
	if err := SaveLastShutdown(dir, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := LoadLastShutdown(dir)
	if !ok || err != nil || !got.At.Equal(at) || got.Duration != 0.25 || got.CallsCut != 2 || got.TunnelsCut != 3 {
		t.Fatalf("load = %+v %v %v, want %+v", got, ok, err, want)
	}
	m := NewMetrics("dev")
	m.LastShutdown(got)
	text := scrapeText(t, m)
	for _, w := range []string{"gateway_last_shutdown_duration_seconds 0.25", "gateway_last_shutdown_calls_cut 2",
		"gateway_last_shutdown_tunnels_cut 3", "gateway_last_shutdown_timestamp_seconds 1.8e+09"} {
		if !strings.Contains(text, w) {
			t.Errorf("scrape has no %q", w)
		}
	}
}

// E6: a malformed record is an error, never a panic or a half-read value.
func TestLastShutdownMalformed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, lastShutdownFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := LoadLastShutdown(dir); ok || err == nil {
		t.Errorf("malformed: ok=%v err=%v, want an error", ok, err)
	}
}
