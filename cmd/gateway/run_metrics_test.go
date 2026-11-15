package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// scrape GETs url and parses the Prometheus text it answers.
func scrape(t *testing.T, url string) map[string]*dto.MetricFamily {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", url, resp.StatusCode)
	}
	p := expfmt.NewTextParser(model.UTF8Validation)
	fams, err := p.TextToMetricFamilies(resp.Body)
	if err != nil {
		t.Fatalf("parse %s: %v", url, err)
	}
	return fams
}

// labelsOf is a metric's labels as a map.
func labelsOf(m *dto.Metric) map[string]string {
	out := map[string]string{}
	for _, l := range m.GetLabel() {
		out[l.GetName()] = l.GetValue()
	}
	return out
}

// hasSeries reports whether family name has a series whose labels include want.
func hasSeries(fams map[string]*dto.MetricFamily, name string, want map[string]string) bool {
	f, ok := fams[name]
	if !ok {
		return false
	}
	for _, m := range f.GetMetric() {
		got := labelsOf(m)
		match := true
		for k, v := range want {
			if got[k] != v {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// bannedLabelNames are labels that would carry one value per session, call or path.
var bannedLabelNames = []string{"session", "session_id", "session_key", "exchange", "exchange_id",
	"request", "request_id", "id", "path", "route", "url", "uri", "host", "addr", "remote_addr", "model"}

var (
	// requestIDShape is the gateway's request id: 26 base32 characters.
	requestIDShape = regexp.MustCompile(`^[A-Z2-7]{26}$`)
	// hexIDShape is a long hex run, uuid or not: a hash, a provider id, a trace id.
	hexIDShape = regexp.MustCompile(`[0-9a-fA-F]{12,}`)
	// safeValue is the shape every label value has: short, no path or address.
	safeValue = regexp.MustCompile(`^[A-Za-z0-9_.+-]{0,40}$`)
)

// badLabels lists every label that looks like an id, a path or a host, or that carries
// one of seen, values the test sent (session keys, request ids). It is the cardinality
// guard of spec 021 AC9.
func badLabels(fams map[string]*dto.MetricFamily, seen []string) []string {
	var bad []string
	for name, f := range fams {
		for _, m := range f.GetMetric() {
			for _, l := range m.GetLabel() {
				k, v := l.GetName(), l.GetValue()
				switch {
				case slices.Contains(bannedLabelNames, k):
					bad = append(bad, fmt.Sprintf("%s: label name %q", name, k))
				case !safeValue.MatchString(v), requestIDShape.MatchString(v), hexIDShape.MatchString(v):
					bad = append(bad, fmt.Sprintf("%s: %s=%q looks like an id, path or host", name, k, v))
				default:
					for _, s := range seen {
						if s != "" && strings.Contains(v, s) {
							bad = append(bad, fmt.Sprintf("%s: %s=%q carries %q", name, k, v, s))
						}
					}
				}
			}
		}
	}
	slices.Sort(bad)
	return bad
}

// The guard itself: a planted path, request id, host, session key and model label are
// each caught, and the gateway's own labels pass.
func TestBadLabels_CatchesIDsPathsAndHosts(t *testing.T) {
	series := func(name string, pairs ...string) *dto.MetricFamily {
		m := &dto.Metric{}
		for i := 0; i < len(pairs); i += 2 {
			m.Label = append(m.Label, &dto.LabelPair{Name: &pairs[i], Value: &pairs[i+1]})
		}
		return &dto.MetricFamily{Name: &name, Metric: []*dto.Metric{m}}
	}
	for _, c := range []struct {
		name string
		fam  *dto.MetricFamily
	}{
		{"path", series("x", "endpoint", "/anthropic/v1/messages")},
		{"request id", series("x", "call", "SXIKGUNEFMYSCKZMN5UEOMD2PX")},
		{"host", series("x", "upstream", "api.anthropic.com:443")},
		{"uuid", series("x", "who", "4f1c2a9e-77b0-4c1e-9d2a-1b2c3d4e5f60")},
		{"session key sent", series("x", "kind", "sess-probe")},
		{"model label name", series("x", "model", "claude-opus-5-5")},
	} {
		if got := badLabels(map[string]*dto.MetricFamily{"x": c.fam}, []string{"sess-probe"}); len(got) == 0 {
			t.Errorf("%s: not caught", c.name)
		}
	}
	ok := series("gateway_http_requests_total", "listener", "api", "status_class", "2xx", "proxied", "true")
	info := series("gateway_build_info", "version", "v0.21.0-3-gf18bc4c-dirty", "go_version", "go1.25.4")
	if got := badLabels(map[string]*dto.MetricFamily{"a": ok, "b": info}, nil); len(got) > 0 {
		t.Errorf("the gateway's own labels flagged: %v", got)
	}
}

// metricsGateway starts a capturing gateway with the UI and both proxy listeners on.
func metricsGateway(t *testing.T, dir string, up *fakeUpstream) (*gateway, string) {
	t.Helper()
	return startCapturing(t, captureEnv(dir, up, map[string]string{
		"GATEWAY_UI_LISTEN_ADDR":                   "127.0.0.2:0",
		"GATEWAY_EFFECTS_NETWORK_HTTP_PROXY_ADDR":  "127.0.0.3:0",
		"GATEWAY_EFFECTS_NETWORK_SOCKS_PROXY_ADDR": "127.0.0.4:0",
	}), nil)
}

// waitSeries scrapes until family name has a series with want, or fails.
func waitSeries(t *testing.T, url, name string, want map[string]string) map[string]*dto.MetricFamily {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		fams := scrape(t, url)
		if hasSeries(fams, name, want) {
			return fams
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %s%v in the scrape", name, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Spec 021 AC1–AC9, AC15: after a proxied call and a UI request, both listeners serve
// /metrics with every metric group, labelled from fixed sets only.
func TestRun_MetricsAfterRequest(t *testing.T) {
	const path = "/anthropic/v1/messages"
	const sessionKey = "4f1c2a9e-77b0-4c1e-9d2a-1b2c3d4e5f60"
	dir := t.TempDir()
	g, url := metricsGateway(t, dir, jsonUpstream(t))
	ui := "http://" + uiAddr(g)

	req, _ := http.NewRequest(http.MethodPost, url+path, strings.NewReader(`{"model":"claude-opus-5-5","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-cli/2.1.0 (external, cli)")
	req.Header.Set("X-Claude-Code-Session-Id", sessionKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if r, err := http.Get(ui + "/api/health"); err == nil {
		_ = r.Body.Close()
	}

	fams := waitSeries(t, url+"/metrics", "gateway_store_write_seconds", map[string]string{"op": "parse", "outcome": "ok"})
	for _, w := range []struct {
		name   string
		labels map[string]string
	}{
		{"gateway_http_requests_total", map[string]string{"listener": "api", "status_class": "2xx", "proxied": "true"}},
		{"gateway_http_request_duration_seconds", map[string]string{"listener": "api", "proxied": "true"}},
		{"gateway_http_requests_total", map[string]string{"listener": "ui", "status_class": "2xx", "proxied": "false"}},
		{"gateway_proxy_overhead_seconds", map[string]string{"protocol": "anthropic"}},
		{"gateway_upstream_ttfb_seconds", map[string]string{"protocol": "anthropic", "stream": "false"}},
		{"gateway_upstream_duration_seconds", map[string]string{"protocol": "anthropic", "stream": "false"}},
		{"gateway_capture_queue_depth", nil},
		{"gateway_capture_queue_capacity", nil},
		{"gateway_capture_dropped_total", map[string]string{"reason": "queue_full"}},
		{"gateway_capture_dropped_total", map[string]string{"reason": "memory"}},
		{"gateway_store_write_seconds", map[string]string{"op": "exchange", "outcome": "ok"}},
		{"gateway_proxy_tunnels_open", nil},
		{"gateway_ui_streams_open", nil},
		{"gateway_ui_warmup_seconds", nil},
		{"gateway_build_info", map[string]string{"version": version}},
		{"go_goroutines", nil},
		{"process_resident_memory_bytes", nil},
	} {
		if !hasSeries(fams, w.name, w.labels) {
			t.Errorf("no %s%v", w.name, w.labels)
		}
	}
	if _, ok := fams["gateway_last_shutdown_duration_seconds"]; ok {
		t.Error("last shutdown metrics before any recorded stop")
	}

	ids := g.requestIDs(path)
	if bad := badLabels(fams, append(ids, sessionKey, path, "claude-opus-5-5")); len(bad) > 0 {
		t.Errorf("high-cardinality labels:\n%s", strings.Join(bad, "\n"))
	}
	// The UI listener serves the same registry.
	if uiFams := scrape(t, ui+"/metrics"); !hasSeries(uiFams, "gateway_build_info", nil) {
		t.Error("the UI listener's /metrics has no gateway_build_info")
	}
}

// Spec 021 AC3: an upstream 5xx and an unreachable upstream are counted by kind.
func TestRun_MetricsUpstreamErrors(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, url := startCapturing(t, captureEnv(t.TempDir(), up, nil), nil)
	resp, err := http.Post(url+"/anthropic/v1/messages", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	waitSeries(t, url+"/metrics", "gateway_upstream_errors_total", map[string]string{"protocol": "anthropic", "kind": "status_5xx"})

	// Nothing listens at the closed upstream's address.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + ln.Addr().String()
	_ = ln.Close()
	_, url2 := startCapturing(t, map[string]string{"GATEWAY_CAPTURE_DIR": t.TempDir(), "GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL": dead}, nil)
	resp, err = http.Post(url2+"/anthropic/v1/messages", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	waitSeries(t, url2+"/metrics", "gateway_upstream_errors_total", map[string]string{"protocol": "anthropic", "kind": "unreachable"})
}

// Spec 021 AC1: the proxy listener never answers /metrics, as a plain request or as a
// forwarded one aimed at itself.
func TestRun_MetricsNotOnProxyListener(t *testing.T) {
	g, _ := metricsGateway(t, t.TempDir(), jsonUpstream(t))
	addr := proxyAddr(g)
	for _, target := range []string{"/metrics", "http://" + addr + "/metrics"} {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target, addr)
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		resp, err := http.ReadResponse(bufio.NewReader(c), nil)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if strings.Contains(string(body), "gateway_build_info") {
				t.Errorf("GET %s on the proxy listener = %d with metrics", target, resp.StatusCode)
			}
		}
		_ = c.Close()
	}
}

// Spec 021 AC7, E1: a stop leaves its record in the capture directory and the next start
// exposes it; before any stop there is none.
func TestRun_LastShutdownMetrics(t *testing.T) {
	dir := t.TempDir()
	up := jsonUpstream(t)
	g, _ := metricsGateway(t, dir, up)
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	_, url := metricsGateway(t, dir, up)
	fams := scrape(t, url+"/metrics")
	for _, name := range []string{"gateway_last_shutdown_duration_seconds", "gateway_last_shutdown_calls_cut",
		"gateway_last_shutdown_tunnels_cut", "gateway_last_shutdown_timestamp_seconds"} {
		if !hasSeries(fams, name, nil) {
			t.Errorf("no %s after a recorded stop", name)
		}
	}
	at := fams["gateway_last_shutdown_timestamp_seconds"].GetMetric()[0].GetGauge().GetValue()
	if d := time.Since(time.Unix(int64(at), 0)); d < 0 || d > time.Minute {
		t.Errorf("last shutdown at %v ago, want just now", d)
	}
}

// Spec 021 E1: with capture off the request metrics still count; no capture metric is
// registered.
func TestRun_MetricsCaptureOff(t *testing.T) {
	up := jsonUpstream(t)
	inTempDir(t, nil)
	g := startGateway(t, options{env: map[string]string{
		"GATEWAY_LISTEN_ADDR": "127.0.0.1:0", "GATEWAY_CAPTURE_ENABLED": "false",
		"GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL": up.URL,
	}})
	url := "http://" + g.waitLine("gateway started")["addr"].(string)
	resp, err := http.Post(url+"/anthropic/v1/messages", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	fams := waitSeries(t, url+"/metrics", "gateway_http_requests_total", map[string]string{"listener": "api", "proxied": "true"})
	for _, name := range []string{"gateway_capture_queue_depth", "gateway_ui_streams_open", "gateway_proxy_tunnels_open"} {
		if _, ok := fams[name]; ok {
			t.Errorf("%s with capture off", name)
		}
	}
}
