package anthropic_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/protocols/anthropic"
)

// The adapter satisfies core.Adapter; the check lives here so adapter.go never has
// to name core's interface.
var _ core.Adapter = anthropic.Adapter{}

// envelope is the Anthropic error body the gateway sends for reason, spelled out
// literally so the test pins the bytes rather than trusting the adapter.
func envelope(reason string) string {
	return `{"type":"error","error":{"type":"api_error","message":"gateway: ` + reason + `"}}`
}

// checkEnvelope asserts a gateway-made error: the status, the Anthropic envelope as
// application/json, and x-gateway-error naming the reason.
func checkEnvelope(t *testing.T, res *http.Response, body []byte, status int, reason string) {
	t.Helper()
	if res.StatusCode != status {
		t.Errorf("status = %d, want %d", res.StatusCode, status)
	}
	if got := res.Header.Values("X-Gateway-Error"); !reflect.DeepEqual(got, []string{reason}) {
		t.Errorf("X-Gateway-Error = %q, want %q", got, reason)
	}
	if got := res.Header.Values("Content-Type"); !reflect.DeepEqual(got, []string{"application/json"}) {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if string(body) != envelope(reason) {
		t.Errorf("body = %q, want %q", body, envelope(reason))
	}
	var v struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Errorf("body is not JSON: %v", err)
	}
}

func TestAdapter_Values(t *testing.T) {
	a := anthropic.Adapter{}
	if a.Name() != "anthropic" || anthropic.Name != "anthropic" {
		t.Errorf("Name() = %q, Name = %q, want anthropic", a.Name(), anthropic.Name)
	}
	if a.Prefix() != "/anthropic" {
		t.Errorf("Prefix() = %q, want /anthropic", a.Prefix())
	}
	if a.DefaultBaseURL() != "https://api.anthropic.com" {
		t.Errorf("DefaultBaseURL() = %q, want https://api.anthropic.com", a.DefaultBaseURL())
	}
	for _, reason := range []string{"upstream_unreachable", "upstream_timeout", "client_body"} {
		contentType, body := a.ErrorBody(reason)
		if contentType != "application/json" || string(body) != envelope(reason) {
			t.Errorf("ErrorBody(%q) = %q %q, want application/json %q", reason, contentType, body, envelope(reason))
		}
	}
}

// AC9: the paths PLAN §7 names reach upstream without the prefix, query kept. They are
// test cases, not a route list.
func TestProxy_AnthropicRoutes(t *testing.T) {
	cases := []struct {
		method, path string
		body         string
	}{
		{http.MethodPost, "/v1/messages", `{"model":"m","messages":[]}`},
		{http.MethodPost, "/v1/messages/count_tokens", `{"model":"m","messages":[]}`},
		{http.MethodGet, "/v1/models", ""},
		{http.MethodGet, "/v1/models/claude-sonnet-5", ""},
		{http.MethodHead, "/api/hello", ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			up := newUpstream(t, nil)
			gw := startGateway(t, upstreamConfig(up.URL))
			var headers []string
			if tc.body != "" {
				headers = []string{"Content-Length: " + strconv.Itoa(len(tc.body))}
			}
			req := rawRequest(tc.method, "/anthropic"+tc.path+"?beta=true", headers, tc.body)
			res, _ := rawAt(t, gw.addr, req, tc.method == http.MethodHead)
			if res.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want upstream's 200", res.StatusCode)
			}
			got := up.only(t)
			if got.Method != tc.method {
				t.Errorf("upstream method = %q, want %q", got.Method, tc.method)
			}
			if want := tc.path + "?beta=true"; got.RequestURI != want {
				t.Errorf("upstream saw %q, want %q", got.RequestURI, want)
			}
			if string(got.Body) != tc.body {
				t.Errorf("upstream body = %q, want %q", got.Body, tc.body)
			}
			if line := gw.accessLine(t, "/anthropic"+tc.path); line["protocol"] != "anthropic" {
				t.Errorf("protocol = %v, want anthropic", line["protocol"])
			}
		})
	}
}

// AC10: a path under the prefix that no one has heard of is forwarded all the same.
func TestProxy_UnknownPathUnderPrefixForwarded(t *testing.T) {
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"from":"upstream"}`)
	})
	gw := startGateway(t, upstreamConfig(up.URL))
	body := `{"x":1}`
	res, got := gw.raw(t, rawRequest(http.MethodPut, "/anthropic/v2/not/yet/invented?beta=true&x=1",
		[]string{"Content-Length: " + strconv.Itoa(len(body))}, body))
	if res.StatusCode != http.StatusAccepted || string(got) != `{"from":"upstream"}` {
		t.Errorf("response = %d %q, want upstream's 202 {\"from\":\"upstream\"}", res.StatusCode, got)
	}
	seen := up.only(t)
	if seen.Method != http.MethodPut || seen.RequestURI != "/v2/not/yet/invented?beta=true&x=1" {
		t.Errorf("upstream saw %s %s, want PUT /v2/not/yet/invented?beta=true&x=1", seen.Method, seen.RequestURI)
	}
	if string(seen.Body) != body {
		t.Errorf("upstream body = %q, want %q", seen.Body, body)
	}
}

// AC13: HEAD /api/hello gets upstream's status and headers with an empty body. An
// unusual status proves the gateway never answers it itself.
func TestProxy_HeadAPIHelloReturnsUpstreamStatus(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusTeapot, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Upstream-Marker", "hello-"+strconv.Itoa(status))
				w.Header().Set("Content-Length", "17")
				w.WriteHeader(status)
			})
			gw := startGateway(t, upstreamConfig(up.URL))
			res, body := rawAt(t, gw.addr, rawRequest(http.MethodHead, "/anthropic/api/hello",
				[]string{"User-Agent: Bun/1.4.3", "Accept: */*"}, ""), true)
			if res.StatusCode != status {
				t.Errorf("status = %d, want upstream's %d", res.StatusCode, status)
			}
			for name, want := range map[string]string{
				"X-Upstream-Marker": "hello-" + strconv.Itoa(status),
				"Content-Type":      "application/json",
				"Content-Length":    "17",
			} {
				if got := res.Header.Values(name); !reflect.DeepEqual(got, []string{want}) {
					t.Errorf("%s = %q, want upstream's %q", name, got, want)
				}
			}
			if len(body) != 0 {
				t.Errorf("body = %q, want empty", body)
			}
			if seen := up.only(t); seen.Method != http.MethodHead || seen.RequestURI != "/api/hello" {
				t.Errorf("upstream saw %s %s, want HEAD /api/hello", seen.Method, seen.RequestURI)
			}
		})
	}
}

// AC17: both of Claude Code's auth shapes arrive upstream byte for byte.
func TestProxy_AuthHeadersForwardedUnchanged(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  map[string]string
	}{
		{
			name:  "api key",
			lines: []string{"x-api-key: sk-ant-api03-Sentinel_Key-0123456789abcdef=="},
			want:  map[string]string{"X-Api-Key": "sk-ant-api03-Sentinel_Key-0123456789abcdef=="},
		},
		{
			name: "bearer with beta",
			lines: []string{
				"Authorization: Bearer sk-ant-oat01-Sentinel.Token_0123456789",
				"anthropic-beta: oauth-2025-04-20,claude-code-20250219,  interleaved-thinking-2025-05-14",
			},
			want: map[string]string{
				"Authorization":  "Bearer sk-ant-oat01-Sentinel.Token_0123456789",
				"Anthropic-Beta": "oauth-2025-04-20,claude-code-20250219,  interleaved-thinking-2025-05-14",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newUpstream(t, nil)
			gw := startGateway(t, upstreamConfig(up.URL))
			body := `{}`
			lines := append([]string{"Content-Length: 2"}, tc.lines...)
			res, _ := gw.raw(t, rawRequest(http.MethodPost, "/anthropic/v1/messages?beta=true", lines, body))
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", res.StatusCode)
			}
			got := up.only(t).Header
			for name, want := range tc.want {
				if v := got.Values(name); !reflect.DeepEqual(v, []string{want}) {
					t.Errorf("%s upstream = %q, want %q", name, v, want)
				}
			}
		})
	}
}

// AC27: an unreachable upstream is a 502 in Anthropic's envelope.
func TestProxy_UpstreamUnreachable502(t *testing.T) {
	checkNoLeaksAtEnd(t)
	gw := startGateway(t, upstreamConfig(refusedURL(t)))
	res, body := gw.raw(t, rawRequest(http.MethodPost, "/anthropic/v1/messages?beta=true",
		[]string{"Content-Length: 2"}, "{}"))
	checkEnvelope(t, res, body, http.StatusBadGateway, "upstream_unreachable")
	if line := gw.accessLine(t, "/anthropic/v1/messages"); line["gateway_error"] != "upstream_unreachable" {
		t.Errorf("gateway_error = %v, want upstream_unreachable", line["gateway_error"])
	}
}

// AC28: no response headers within response_header_timeout is a 504 in Anthropic's
// envelope.
func TestProxy_UpstreamTimeout504(t *testing.T) {
	checkNoLeaksAtEnd(t)
	up := upstreamConfig(hangingUpstream(t))
	up.ResponseHeaderTimeout = 200 * time.Millisecond
	gw := startGateway(t, up)
	res, body := gw.raw(t, rawRequest(http.MethodPost, "/anthropic/v1/messages?beta=true",
		[]string{"Content-Length: 2"}, "{}"))
	checkEnvelope(t, res, body, http.StatusGatewayTimeout, "upstream_timeout")
	if line := gw.accessLine(t, "/anthropic/v1/messages"); line["gateway_error"] != "upstream_timeout" {
		t.Errorf("gateway_error = %v, want upstream_timeout", line["gateway_error"])
	}
}

// AC47 (Anthropic half): broken chunked framing on a live connection is a 400 in
// Anthropic's envelope.
func TestProxy_ClientBodyEnvelope(t *testing.T) {
	checkNoLeaksAtEnd(t)
	// Upstream reads whatever arrives, tolerating a body that breaks off.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	gw := startGateway(t, upstreamConfig(base))
	res, body := gw.raw(t, rawRequest(http.MethodPost, "/anthropic/v1/messages?beta=true",
		[]string{"Transfer-Encoding: chunked"}, "5\r\nhello\r\nzz\r\n"))
	checkEnvelope(t, res, body, http.StatusBadRequest, "client_body")
	if line := gw.accessLine(t, "/anthropic/v1/messages"); line["gateway_error"] != "client_body" {
		t.Errorf("gateway_error = %v, want client_body", line["gateway_error"])
	}
}

// AC26 (research Q18): Anthropic's own error responses, with the literal
// anthropic-ratelimit-* family, reach the client byte-identical to what upstream
// sends directly. Only X-Request-Id, the gateway's own, may differ.
func TestProxy_UpstreamErrorsVerbatim(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, 529} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			body := `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"},"request_id":"req_x"}`
			up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
				h := w.Header()
				h.Set("Date", fixedDate)
				h.Set("Content-Type", "application/json")
				h.Set("Retry-After", "17")
				h.Set("X-Should-Retry", "true")
				h.Set("Request-Id", "req_x")
				h.Set("Anthropic-Ratelimit-Requests-Limit", "50")
				h.Set("Anthropic-Ratelimit-Requests-Remaining", "0")
				h.Set("Anthropic-Ratelimit-Requests-Reset", "2026-09-26T10:00:17Z")
				h.Set("Anthropic-Ratelimit-Input-Tokens-Remaining", "0")
				h.Set("Anthropic-Ratelimit-Unified-Status", "rejected")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, body)
			})
			gw := startGateway(t, upstreamConfig(up.URL))
			direct, directBody := rawAt(t, up.URL.Host, rawRequest(http.MethodPost, "/v1/messages?beta=true",
				[]string{"Content-Length: 2"}, "{}"), false)
			via, viaBody := gw.raw(t, rawRequest(http.MethodPost, "/anthropic/v1/messages?beta=true",
				[]string{"Content-Length: 2"}, "{}"))
			if via.StatusCode != status || direct.StatusCode != status {
				t.Errorf("status = %d (direct %d), want %d", via.StatusCode, direct.StatusCode, status)
			}
			if !bytes.Equal(viaBody, directBody) || string(viaBody) != body {
				t.Errorf("body = %q, want upstream's %q", viaBody, directBody)
			}
			gotHeader := via.Header.Clone()
			gotHeader.Del("X-Request-Id")
			wantHeader := direct.Header.Clone()
			wantHeader.Del("X-Request-Id")
			if !reflect.DeepEqual(gotHeader, wantHeader) {
				t.Errorf("headers differ from upstream's\n got: %v\nwant: %v", gotHeader, wantHeader)
			}
			for name := range wantHeader {
				if strings.HasPrefix(name, "Anthropic-Ratelimit-") && len(via.Header.Values(name)) != 1 {
					t.Errorf("%s missing on the client side", name)
				}
			}
			if line := gw.accessLine(t, "/anthropic/v1/messages"); line["gateway_error"] != nil {
				t.Errorf("gateway_error = %v on an upstream error, want absent", line["gateway_error"])
			}
		})
	}
}

// AC37 (research Q15): auth is which header is present, never its value.
func TestAccessLog_AuthKind(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  core.AuthKind
	}{
		{"api key", []string{"x-api-key: sk-sentinel"}, core.AuthAPIKey},
		{"bearer", []string{"Authorization: Bearer sk-sentinel"}, core.AuthBearer},
		{"other scheme counts as bearer", []string{"Authorization: Basic c2VudGluZWw="}, core.AuthBearer},
		{"both", []string{"x-api-key: sk-sentinel", "Authorization: Bearer sk-sentinel"}, core.AuthBoth},
		{"empty api key is still present", []string{"x-api-key:"}, core.AuthAPIKey},
		{"none", nil, core.AuthNone},
	}
	up := newUpstream(t, nil)
	gw := startGateway(t, upstreamConfig(up.URL))
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := "/anthropic/v1/auth-" + strconv.Itoa(i)
			res, _ := gw.raw(t, rawRequest(http.MethodGet, path, tc.lines, ""))
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", res.StatusCode)
			}
			line := gw.accessLine(t, path)
			if line["auth"] != string(tc.want) {
				t.Errorf("auth = %v, want %q", line["auth"], tc.want)
			}
			if strings.Contains(gw.logs.String(), "sentinel") || strings.Contains(gw.logs.String(), "c2VudGluZWw=") {
				t.Errorf("a credential value reached the log:\n%s", gw.logs.String())
			}
		})
	}
}
