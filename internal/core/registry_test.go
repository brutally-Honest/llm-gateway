package core_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/server"
)

// headerProfile is a test-only client that matches when its header is present.
type headerProfile struct {
	name   string
	header string
}

func (p headerProfile) Name() string               { return p.name }
func (p headerProfile) Match(r *http.Request) bool { return r.Header.Get(p.header) != "" }

// startRegistryGateway is server.New with a registry mounted exactly as production
// mounts it: the test adapter in front of base, then the given profiles.
func startRegistryGateway(t *testing.T, base *url.URL, profiles ...core.Profile) *gateway {
	t.Helper()
	logs := &syncBuffer{}
	log := logging.New(logs, "debug")
	reg := core.NewRegistry(log)
	for _, p := range profiles {
		reg.AddProfile(p)
	}
	reg.AddAdapter(testAdapter{}, upstreamConfig(base))
	srv := server.New(log, reg.Mount)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	addr := ln.Addr().String()
	return &gateway{addr: addr, url: "http://" + addr, logs: logs}
}

// send makes one request through the gateway and returns the response with its body.
func (g *gateway) send(t *testing.T, method, path string, header http.Header) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, g.url+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, vs := range header {
		req.Header[name] = vs
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res, body
}

func (u *upstream) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.reqs)
}

func TestRouter_PathOutsidePrefixIs404(t *testing.T) {
	up := newUpstream(t, nil)
	gw := startRegistryGateway(t, up.URL)
	// What chi's own 404 sends, so the test pins the gateway's answer, not upstream's.
	want := httptest.NewRecorder()
	http.NotFound(want, httptest.NewRequest(http.MethodGet, "/", nil))

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/"},
		{http.MethodPost, "/v1/messages"},
		{http.MethodGet, "/other/v1/models"},
		{http.MethodGet, "/tx/v1/models"}, // shares the prefix's letters, not the prefix
		{http.MethodGet, "/t"},            // the bare prefix is not under it
		{http.MethodHead, "/api/hello"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			res, body := gw.send(t, tc.method, tc.path, nil)
			if res.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", res.StatusCode)
			}
			if tc.method != http.MethodHead && string(body) != want.Body.String() {
				t.Errorf("body = %q, want the gateway's %q", body, want.Body.String())
			}
			line := gw.accessLine(t, tc.path)
			if _, ok := line["protocol"]; ok {
				t.Errorf("404 line has protocol %v; nothing was proxied", line["protocol"])
			}
		})
	}
	if n := up.count(); n != 0 {
		t.Errorf("upstream saw %d requests, want 0", n)
	}

	res, body := gw.send(t, http.MethodGet, "/healthz", nil)
	if res.StatusCode != http.StatusOK || string(body) != `{"status":"ok"}` {
		t.Errorf("/healthz = %d %q, want 200 {\"status\":\"ok\"}", res.StatusCode, body)
	}
}

func TestProfile_UnknownClientProxied(t *testing.T) {
	up := newUpstream(t, nil)
	gw := startRegistryGateway(t, up.URL, testProfile{})

	res, _ := gw.send(t, http.MethodPost, "/t/v1/unknown-client", http.Header{"User-Agent": {"some-other-tool/1.0"}})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want upstream's 200", res.StatusCode)
	}
	if got := up.only(t).RequestURI; got != "/v1/unknown-client" {
		t.Errorf("upstream saw %q, want /v1/unknown-client", got)
	}
	line := gw.accessLine(t, "/t/v1/unknown-client")
	if line["client"] != core.ClientUnknown {
		t.Errorf("client = %v, want %q", line["client"], core.ClientUnknown)
	}
	if line["protocol"] != "test" {
		t.Errorf("protocol = %v, want test", line["protocol"])
	}
}

// TestCore_TestAdapterAndProfileNeedNoCoreChange registers an adapter and a profile
// that exist only in this test, through the registry's public API, and proxies every
// standard method through them. Non-standard methods get chi's 405 (research Q20).
func TestCore_TestAdapterAndProfileNeedNoCoreChange(t *testing.T) {
	up := newUpstream(t, nil)
	gw := startRegistryGateway(t, up.URL, testProfile{})

	methods := []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
	}
	for _, method := range methods {
		path := "/t/v1/" + strings.ToLower(method)
		res, _ := gw.send(t, method, path+"?beta=true", http.Header{"X-Test-Client": {"1"}})
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d, want upstream's 200", method, res.StatusCode)
			continue
		}
		line := gw.accessLine(t, path)
		if line["protocol"] != "test" || line["client"] != "test-client" {
			t.Errorf("%s: protocol, client = %v, %v; want test, test-client", method, line["protocol"], line["client"])
		}
	}

	up.mu.Lock()
	defer up.mu.Unlock()
	if len(up.reqs) != len(methods) {
		t.Fatalf("upstream saw %d requests, want %d", len(up.reqs), len(methods))
	}
	for i, method := range methods {
		want := "/v1/" + strings.ToLower(method) + "?beta=true"
		if got := up.reqs[i]; got.Method != method || got.RequestURI != want {
			t.Errorf("upstream request %d = %s %s, want %s %s", i, got.Method, got.RequestURI, method, want)
		}
	}
}

func TestRegistry_IdentifyFirstMatchWins(t *testing.T) {
	reg := core.NewRegistry(logging.New(io.Discard, "debug"))
	reg.AddProfile(headerProfile{name: "first", header: "X-A"})
	reg.AddProfile(headerProfile{name: "second", header: "X-B"})
	reg.AddProfile(headerProfile{name: "third", header: "X-A"})

	for _, tc := range []struct {
		header http.Header
		want   string
	}{
		{http.Header{"X-A": {"1"}, "X-B": {"1"}}, "first"},
		{http.Header{"X-A": {"1"}}, "first"},
		{http.Header{"X-B": {"1"}}, "second"},
		{http.Header{}, core.ClientUnknown},
	} {
		r := httptest.NewRequest(http.MethodGet, "/t/v1/x", nil)
		r.Header = tc.header
		if got := reg.Identify(r); got != tc.want {
			t.Errorf("Identify(%v) = %q, want %q", tc.header, got, tc.want)
		}
	}

	empty := core.NewRegistry(logging.New(io.Discard, "debug"))
	if got := empty.Identify(httptest.NewRequest(http.MethodGet, "/", nil)); got != core.ClientUnknown {
		t.Errorf("Identify with no profiles = %q, want %q", got, core.ClientUnknown)
	}
}
