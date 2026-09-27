package main

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
)

// upstreamHit is what the fake upstream saw of one request.
type upstreamHit struct {
	method, path, query string
}

// fakeUpstream is a loopback upstream that records what reaches it and answers with
// handler.
type fakeUpstream struct {
	*httptest.Server
	mu   sync.Mutex
	hits []upstreamHit
}

func newFakeUpstream(t *testing.T, handler http.HandlerFunc) *fakeUpstream {
	t.Helper()
	u := &fakeUpstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		u.mu.Lock()
		u.hits = append(u.hits, upstreamHit{r.Method, r.URL.Path, r.URL.RawQuery})
		u.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *fakeUpstream) seen() []upstreamHit {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.hits)
}

// servedWith starts run on a real 127.0.0.1:0 listener at log_level debug, with env
// added on top, and returns it with its base URL.
func servedWith(t *testing.T, env map[string]string, mount ...func(chi.Router)) (*gateway, string) {
	t.Helper()
	inTempDir(t, nil)
	all := map[string]string{"GATEWAY_LISTEN_ADDR": "127.0.0.1:0", "GATEWAY_LOG_LEVEL": "debug"}
	for k, v := range env {
		all[k] = v
	}
	g := startGateway(t, options{env: all, realListen: true, mount: mount})
	addr, _ := g.waitLine("gateway started")["addr"].(string)
	return g, "http://" + addr
}

// do sends a request with header and body, reads the whole response, and returns
// the status (or 0 and the read error when the connection broke).
func do(t *testing.T, method, url string, header map[string]string, body string) (int, error) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	_, err = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, err
}

// requestLines returns the access lines whose path is path.
func (g *gateway) requestLines(path string) []map[string]any {
	var out []map[string]any
	for _, l := range g.lines() {
		if l["msg"] == "request" && l["path"] == path {
			out = append(out, l)
		}
	}
	return out
}

// The one test that proves the wiring: the real adapter and profile, through run,
// against a fake upstream on loopback.
func TestRun_ProxiesAnthropicEndToEnd(t *testing.T) {
	up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"type":"message"}`)
	})
	g, url := servedWith(t, map[string]string{"GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL": up.URL})

	claude := map[string]string{"User-Agent": "claude-cli/2.1.283 (external, cli)", "x-api-key": "k"}
	if code, err := do(t, http.MethodPost, url+"/anthropic/v1/messages?beta=true", claude, `{}`); err != nil || code != http.StatusOK {
		t.Fatalf("claude-code request: status %d, err %v", code, err)
	}
	// Go's own User-Agent matches no profile, and no auth header is sent.
	if code, err := do(t, http.MethodGet, url+"/anthropic/v1/models", nil, ""); err != nil || code != http.StatusOK {
		t.Fatalf("unknown-client request: status %d, err %v", code, err)
	}
	g.waitCount("request", 2)

	want := []upstreamHit{{http.MethodPost, "/v1/messages", "beta=true"}, {http.MethodGet, "/v1/models", ""}}
	if got := up.seen(); !slices.Equal(got, want) {
		t.Errorf("upstream saw %v, want %v", got, want)
	}

	cases := []struct{ path, client, auth string }{
		{"/anthropic/v1/messages", "claude-code", "api_key"},
		{"/anthropic/v1/models", "unknown", "none"},
	}
	for _, tc := range cases {
		lines := g.requestLines(tc.path)
		if len(lines) != 1 {
			t.Fatalf("%s: %d request lines, want 1", tc.path, len(lines))
		}
		l := lines[0]
		if l["protocol"] != "anthropic" || l["client"] != tc.client || l["auth"] != tc.auth || l["stream"] != false {
			t.Errorf("%s: line = %v, want protocol anthropic, client %s, auth %s, stream false", tc.path, l, tc.client, tc.auth)
		}
		if _, ok := l["ttfb_ms"]; !ok {
			t.Errorf("%s: no ttfb_ms in %v", tc.path, l)
		}
	}
	if code := g.stop(); code != exitOK {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// overrideValue is the base_url override in TestRun_UpstreamEnvOverrideLogged.
var overrideValue = regexp.MustCompile(`127\.0\.0\.1:1\b`)

// AC2, the startup-line half: an upstream env override is named in env_overrides.
func TestRun_UpstreamEnvOverrideLogged(t *testing.T) {
	g, _ := servedWith(t, map[string]string{
		"GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL":                "http://127.0.0.1:1",
		"GATEWAY_UPSTREAMS_ANTHROPIC_RESPONSE_HEADER_TIMEOUT": "5m",
	})
	line := g.waitLine("gateway started")
	var got []string
	raw, _ := line["env_overrides"].([]any)
	for _, v := range raw {
		s, _ := v.(string)
		got = append(got, s)
	}
	for _, key := range []string{"upstreams.anthropic.base_url", "upstreams.anthropic.response_header_timeout"} {
		if !slices.Contains(got, key) {
			t.Errorf("env_overrides = %v, want it to name %s", got, key)
		}
	}
	// The word boundary after ":1" keeps the gateway's own 127.0.0.1:<port> from matching.
	if overrideValue.MatchString(g.stdout.String()) {
		t.Errorf("an override value appears in the output\n%s", g.stdout.String())
	}
}

// AC39: a sentinel in x-api-key, Authorization, the query and the body reaches neither
// stdout nor stderr, on a success, a 502, a mid-stream abort and a panicking route.
// Not parallel: os.Stderr is global, and it is swapped before run builds its logger.
func TestProxy_SecretsNotLogged(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	stderrOut := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		stderrOut <- string(b)
	}()
	// readStderr restores os.Stderr, closes the pipe so the reader returns, and waits
	// for it. The cleanup runs it on every exit path, a t.Fatal included.
	var once sync.Once
	var stderrText string
	readStderr := func() string {
		once.Do(func() {
			os.Stderr = stderr
			_ = w.Close()
			stderrText = <-stderrOut
			_ = r.Close()
		})
		return stderrText
	}
	t.Cleanup(func() { readStderr() })

	header := map[string]string{
		"User-Agent":    "claude-cli/2.1.283 (external, cli)",
		"x-api-key":     sentinel,
		"Authorization": "Bearer " + sentinel,
	}
	query := "?beta=true&key=" + sentinel
	body := `{"model":"m","messages":[{"role":"user","content":"` + sentinel + `"}]}`

	// A live upstream: /v1/messages succeeds, /v1/abort dies after its headers.
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/abort" {
			_, _ = io.WriteString(w, `{"type":"message"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: ping\ndata: {}\n\n")
		w.(http.Flusher).Flush()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})
	live, liveURL := servedWith(t, map[string]string{"GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL": up.URL},
		func(r chi.Router) {
			r.Post("/panic", func(_ http.ResponseWriter, r *http.Request) {
				panic(errors.New(r.Header.Get("x-api-key")))
			})
		})

	if code, err := do(t, http.MethodPost, liveURL+"/anthropic/v1/messages"+query, header, body); err != nil || code != http.StatusOK {
		t.Errorf("success: status %d, err %v", code, err)
	}
	if _, err := do(t, http.MethodPost, liveURL+"/anthropic/v1/abort"+query, header, body); err == nil {
		t.Errorf("mid-stream abort: the client read the whole body, want a broken connection")
	}
	if code, _ := do(t, http.MethodPost, liveURL+"/panic"+query, header, body); code != http.StatusInternalServerError {
		t.Errorf("panic: status %d, want 500", code)
	}
	live.waitCount("request", 3)
	live.waitCount("panic recovered", 1)

	// A dead upstream: a loopback port nothing listens on.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := ln.Addr().String()
	_ = ln.Close()
	dead, deadURL := servedWith(t, map[string]string{"GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL": "http://" + deadAddr})
	if code, err := do(t, http.MethodPost, deadURL+"/anthropic/v1/messages"+query, header, body); err != nil || code != http.StatusBadGateway {
		t.Errorf("502: status %d, err %v", code, err)
	}
	dead.waitCount("request", 1)

	if l := live.requestLines("/anthropic/v1/abort"); len(l) != 1 || l[0]["upstream_aborted"] != true {
		t.Errorf("abort line = %v, want upstream_aborted true", l)
	}
	if l := dead.requestLines("/anthropic/v1/messages"); len(l) != 1 || l[0]["gateway_error"] != "upstream_unreachable" {
		t.Errorf("502 line = %v, want gateway_error upstream_unreachable", l)
	}

	for _, g := range []*gateway{live, dead} {
		if code := g.stop(); code != exitOK {
			t.Errorf("exit code = %d, want 0", code)
		}
		noValue(t, g, sentinel)
	}
	if out := readStderr(); strings.Contains(out, sentinel) {
		t.Errorf("stderr contains the sentinel\n%s", out)
	}
}
