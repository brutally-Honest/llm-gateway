package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/protocols/anthropic"
)

const (
	rlPath = "/anthropic/v1/messages"
	rlKey  = "alice-key-sentinel"
)

// rlConfig is a config file enabling the limiter with one key.
func rlConfig(rpm, tpm int) string {
	return fmt.Sprintf("ratelimit:\n  enabled: true\n  keys:\n    %s:\n      rpm: %d\n      tpm: %d\n", rlKey, rpm, tpm)
}

// rlGateway starts run on a real listener with config as the config file.
func rlGateway(t *testing.T, config string, up *httptest.Server, env map[string]string) (*gateway, string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "rl.yaml")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	all := map[string]string{
		"GATEWAY_LISTEN_ADDR":                  "127.0.0.1:0",
		"GATEWAY_LOG_LEVEL":                    "debug",
		"GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL": up.URL,
	}
	for k, v := range env {
		all[k] = v
	}
	g := startGateway(t, options{args: []string{"-config", path}, env: all, realListen: true})
	addr, _ := g.waitLine("gateway started")["addr"].(string)
	return g, "http://" + addr
}

// rlPost posts a model call with the key and returns the response with its body read.
func rlPost(t *testing.T, url, key string, extra map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+rlPath, strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("X-Gateway-Key", key)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return res, b
}

// waitLimited posts until the gateway answers 429; tokens are added after the
// response is delivered, so the first calls may still be admitted.
func waitLimited(t *testing.T, url string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, _ := rlPost(t, url, rlKey, nil)
		if res.StatusCode == http.StatusTooManyRequests {
			return res
		}
		if time.Now().After(deadline) {
			t.Fatalf("never limited, last status %d", res.StatusCode)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	// Tests chdir into temp dirs, so the fixture dir is found from this file.
	_, self, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(self), "../../internal/protocols/anthropic/testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fixtureTokens is the usage total of testdata/stream.sse as the adapter reads it.
func fixtureTokens(t *testing.T) int64 {
	t.Helper()
	r := anthropic.Adapter{}.UsageReader(200, http.Header{}, true)
	r.Write(readFile(t, "stream.sse"))
	n, ok := r.Tokens()
	if !ok || n == 0 {
		t.Fatalf("fixture tokens = %d, %v", n, ok)
	}
	return n
}

func sseUpstream(t *testing.T, file, enc string) *fakeUpstream {
	t.Helper()
	body := readFile(t, file)
	return newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		if enc != "" {
			w.Header().Set("Content-Encoding", enc)
		}
		_, _ = w.Write(body)
	})
}

// AC1: disabled (the default) needs no key and limits nothing.
func TestRun_RateLimitDisabled_ProxiesWithoutKey(t *testing.T) {
	up := jsonUpstream(t)
	g, url := servedWith(t, map[string]string{"GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL": up.URL})
	for i := 0; i < 5; i++ {
		if res, _ := rlPost(t, url, "", nil); res.StatusCode != http.StatusOK {
			t.Fatalf("call %d: status %d, want 200", i, res.StatusCode)
		}
	}
	if n := len(up.seen()); n != 5 {
		t.Errorf("upstream saw %d calls, want 5", n)
	}
	if len(linesWith(g, "ratelimit_rejected")) != 0 {
		t.Error("a rejection was logged with the limiter off")
	}
}

// AC16: enabled with no keys refuses to start and names the field.
func TestRun_InvalidRateLimitConfig_RefusesToStart(t *testing.T) {
	for name, cfg := range map[string]string{
		"no_keys":  "ratelimit:\n  enabled: true\n",
		"strategy": "ratelimit:\n  strategy: nope\n",
		"negative": "ratelimit:\n  enabled: true\n  keys:\n    a:\n      rpm: -1\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			path := filepath.Join(dir, "rl.yaml")
			if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
				t.Fatal(err)
			}
			g := startGateway(t, options{args: []string{"-config", path}, env: map[string]string{"GATEWAY_LISTEN_ADDR": "127.0.0.1:0"}})
			line := failsBeforeBind(t, g, exitConfig)
			if line["msg"] != "invalid config" || !strings.HasPrefix(fmt.Sprint(line["key"]), "ratelimit.") {
				t.Errorf("line = %v, want invalid config naming a ratelimit field", line)
			}
		})
	}
}

// AC1, AC18: with the limiter off, x-gateway-key is still stripped and redacted on
// every route.
func TestRun_RateLimitDisabled_GatewayKeyStillStrippedAndRedacted(t *testing.T) {
	for _, path := range []string{"/anthropic/v1/messages", "/anthropic/v1/models"} {
		t.Run(path, func(t *testing.T) {
			dir := t.TempDir()
			up := newSecretUpstream(t)
			g, url := servedAgainst(t, dir, up.URL, nil)
			req, _ := http.NewRequest(http.MethodPost, url+path, strings.NewReader(`{}`))
			req.Header.Set("X-Gateway-Key", sentinel)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			if got := up.received(t).Get("X-Gateway-Key"); got != "" {
				t.Errorf("upstream got X-Gateway-Key %q", got)
			}
			ex, r := storedExchange(t, g, dir, path)
			if got := ex.RequestHeaders.Values("X-Gateway-Key"); len(got) != 1 || got[0] != core.Redacted {
				t.Errorf("stored X-Gateway-Key = %q, want [%s]", got, core.Redacted)
			}
			scanStore(t, r, dir, sentinel)
			noValue(t, g, sentinel)
		})
	}
}

// AC9: tokens from a stream's usage count against tpm.
func TestRun_TokensRecordedFromStream(t *testing.T) {
	total := fixtureTokens(t)
	up := sseUpstream(t, "stream.sse", "")
	_, url := rlGateway(t, rlConfig(0, int(total)), up.Server, nil)
	if res, _ := rlPost(t, url, rlKey, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("first call status %d, want 200", res.StatusCode)
	}
	res := waitLimited(t, url)
	if res.Header.Get("X-Gateway-Ratelimit") != "tokens" {
		t.Errorf("X-Gateway-Ratelimit = %q, want tokens", res.Header.Get("X-Gateway-Ratelimit"))
	}
	if n := len(up.seen()); n != 1 {
		t.Errorf("upstream saw %d calls, want 1", n)
	}
}

// AC9: a gzip stream is forwarded as is and its usage still counted.
func TestRun_GzipStreamTokensRecorded(t *testing.T) {
	total := fixtureTokens(t)
	up := sseUpstream(t, "stream.sse.gz", "gzip")
	_, url := rlGateway(t, rlConfig(0, int(total)), up.Server, nil)
	res, body := rlPost(t, url, rlKey, map[string]string{"Accept-Encoding": "gzip"})
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("status %d, encoding %q", res.StatusCode, res.Header.Get("Content-Encoding"))
	}
	if string(body) != string(readFile(t, "stream.sse.gz")) {
		t.Error("forwarded bytes differ from the upstream's gzip stream")
	}
	if got := waitLimited(t, url).Header.Get("X-Gateway-Ratelimit"); got != "tokens" {
		t.Errorf("X-Gateway-Ratelimit = %q, want tokens", got)
	}
}

// AC10: tokens count with capture off.
func TestRun_TokensCountedWithCaptureOff(t *testing.T) {
	total := fixtureTokens(t)
	up := sseUpstream(t, "stream.sse", "")
	_, url := rlGateway(t, rlConfig(0, int(total)), up.Server, map[string]string{"GATEWAY_CAPTURE_ENABLED": "false"})
	rlPost(t, url, rlKey, nil)
	waitLimited(t, url)
}

// AC10: tokens count when the exchange is dropped from capture.
func TestRun_TokensCountedWhenExchangeDropped(t *testing.T) {
	total := fixtureTokens(t)
	up := sseUpstream(t, "stream.sse", "")
	g, url := rlGateway(t, rlConfig(0, int(total)), up.Server, map[string]string{
		"GATEWAY_CAPTURE_MEMORY_LIMIT": "1024",
		"GATEWAY_CAPTURE_DIR":          t.TempDir(),
	})
	req, _ := http.NewRequest(http.MethodPost, url+rlPath, strings.NewReader(`{"pad":"`+strings.Repeat("b", 64<<10)+`"}`))
	req.Header.Set("X-Gateway-Key", rlKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for len(g.requestLines(rlPath)) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if l := g.requestLines(rlPath); len(l) != 1 || l[0]["capture"] != core.CaptureDroppedMemory {
		t.Fatalf("lines = %v, want capture %s", l, core.CaptureDroppedMemory)
	}
	waitLimited(t, url)
}

// AC18, E9: with the limiter on, the key is stripped and redacted on a model call
// and on a route the limiter ignores.
func TestRun_GatewayKeyNotForwardedAndRedacted(t *testing.T) {
	for _, path := range []string{"/anthropic/v1/messages", "/anthropic/v1/models"} {
		t.Run(path, func(t *testing.T) {
			dir := t.TempDir()
			up := newSecretUpstream(t)
			g, url := rlGateway(t, rlConfig(0, 0), up.Server, map[string]string{"GATEWAY_CAPTURE_DIR": dir})
			method := http.MethodPost
			if path != rlPath {
				method = http.MethodGet
			}
			req, _ := http.NewRequest(method, url+path, strings.NewReader(`{}`))
			req.Header.Set("X-Gateway-Key", rlKey)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status %d, want 200", res.StatusCode)
			}
			if got := up.received(t).Get("X-Gateway-Key"); got != "" {
				t.Errorf("upstream got X-Gateway-Key %q", got)
			}
			ex, r := storedExchange(t, g, dir, path)
			if got := ex.RequestHeaders.Values("X-Gateway-Key"); len(got) != 1 || got[0] != core.Redacted {
				t.Errorf("stored X-Gateway-Key = %q, want [%s]", got, core.Redacted)
			}
			scanStore(t, r, dir, rlKey)
		})
	}
}

// AC19: an upstream 429 reaches the client verbatim, without gateway headers, and
// counts no tokens.
func TestRun_Upstream429PassedThroughVerbatim(t *testing.T) {
	const body = `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`
	up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "17")
		w.Header().Set("Anthropic-Ratelimit-Requests-Remaining", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, body)
	})
	_, url := rlGateway(t, rlConfig(0, 1), up.Server, nil)
	for i := 0; i < 3; i++ {
		res, b := rlPost(t, url, rlKey, nil)
		if res.StatusCode != http.StatusTooManyRequests || string(b) != body ||
			res.Header.Get("Retry-After") != "17" || res.Header.Get("Anthropic-Ratelimit-Requests-Remaining") != "0" {
			t.Fatalf("call %d: status %d, body %q, headers %v", i, res.StatusCode, b, res.Header)
		}
		for name := range res.Header {
			if strings.HasPrefix(strings.ToLower(name), "x-gateway-ratelimit") {
				t.Errorf("call %d: gateway header %s added", i, name)
			}
		}
	}
	// tpm is 1: had tokens been recorded, the later calls would never reach upstream.
	if n := len(up.seen()); n != 3 {
		t.Errorf("upstream saw %d calls, want 3", n)
	}
}

// E1: a client that leaves mid-stream still has the usage seen so far counted.
func TestRun_ClientDisconnectMidStreamRecordsSeenTokens(t *testing.T) {
	done := make(chan struct{})
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-done:
		}
	})
	t.Cleanup(func() { close(done) })
	_, url := rlGateway(t, rlConfig(0, 50), up.Server, nil)

	req, _ := http.NewRequest(http.MethodPost, url+rlPath, strings.NewReader(`{}`))
	req.Header.Set("X-Gateway-Key", rlKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(res.Body)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended before the usage event: %v", err)
		}
		if strings.HasPrefix(line, "data:") {
			break
		}
	}
	_ = res.Body.Close()
	waitLimited(t, url)
}
