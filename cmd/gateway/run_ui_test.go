package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// uiEnv is captureEnv with the UI on an OS-chosen port.
func uiEnv(t *testing.T, extra map[string]string) map[string]string {
	t.Helper()
	up := jsonUpstream(t)
	all := map[string]string{"GATEWAY_UI_LISTEN_ADDR": "127.0.0.1:0", "GATEWAY_LISTEN_ADDR": "127.0.0.2:0"}
	for k, v := range extra {
		all[k] = v
	}
	return captureEnv(t.TempDir(), up, all)
}

// uiAddr waits for the ui started line and returns its address.
func uiAddr(g *gateway) string {
	g.t.Helper()
	addr, _ := g.waitLine("ui started")["addr"].(string)
	return addr
}

// uiLines are the lines whose msg mentions the UI.
func uiLines(g *gateway) []map[string]any {
	var out []map[string]any
	for _, l := range g.lines() {
		if m, _ := l["msg"].(string); strings.Contains(strings.ToLower(m), "ui") {
			out = append(out, l)
		}
	}
	return out
}

// AC1, E2: the UI answers on its own listener, apart from the proxy, over a store that
// capture created at startup.
func TestRun_UIServesOnOwnListener(t *testing.T) {
	g, url := startCapturing(t, uiEnv(t, nil), nil)
	addr := uiAddr(g)
	if addr == strings.TrimPrefix(url, "http://") {
		t.Fatalf("ui and proxy share %s", addr)
	}
	resp, err := http.Get("http://" + addr + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/api/health = %d, want 200", resp.StatusCode)
	}
	resp, err = http.Get("http://" + addr + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/api/sessions = %d, want 200 over the fresh store", resp.StatusCode)
	}
	// The proxy listener does not serve the UI.
	resp, err = http.Get(url + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("proxy served /api/health")
	}
}

// AC2: an open SSE stream does not hold shutdown up; it ends and run exits 0.
func TestRun_ShutdownClosesUIStreams(t *testing.T) {
	g, _ := startCapturing(t, uiEnv(t, nil), nil)
	addr := uiAddr(g)
	resp, err := http.Get("http://" + addr + "/api/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatalf("stream gave nothing: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, resp.Body); done <- err }()
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream is still open after run returned")
	}
	if n := len(linesWith(g, "shutdown timed out")) + len(linesWith(g, "ui shutdown failed")); n != 0 {
		t.Errorf("got %d shutdown failure lines, want none", n)
	}
}

// AC2, AC28: workers still draining after the hub closed publish into it without a
// panic or a stall; every queued exchange is stored and shutdown ends in time.
func TestRun_CommitsDuringShutdownAfterHubClose(t *testing.T) {
	const path = "/anthropic/v1/messages"
	env := uiEnv(t, map[string]string{"GATEWAY_CAPTURE_WORKERS": "1"})
	g, url := startCapturing(t, env, openSlow(50*time.Millisecond, make(chan struct{}, 1)))
	uiAddr(g)
	const n = 5
	for range n {
		if code, err := do(t, http.MethodPost, url+path, nil, `{"hello":"world"}`); err != nil || code != http.StatusOK {
			t.Fatalf("status %d, err %v", code, err)
		}
	}
	g.waitQueued(path, n)
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	line := stoppedLine(t, g)
	if line["undrained"] != float64(0) || line["store_failed"] != float64(0) {
		t.Errorf("capture stopped = %v, want all drained", line)
	}
	for _, l := range g.lines() {
		if l["level"] == "error" {
			t.Errorf("error line during shutdown: %v", l)
		}
	}
}

// AC5: ui.enabled false opens no second listener and logs nothing about the UI.
func TestRun_UIDisabledOpensNothingLogsNothing(t *testing.T) {
	g, _ := startCapturing(t, uiEnv(t, map[string]string{"GATEWAY_UI_ENABLED": "false"}), nil)
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if n := len(g.listened()); n != 1 {
		t.Errorf("listen called for %v, want only the proxy", g.listened())
	}
	if ls := uiLines(g); len(ls) != 0 {
		t.Errorf("ui lines with the UI off: %v", ls)
	}
}

// AC6: ui on with capture off starts no UI and warns once.
func TestRun_UIWithCaptureOffWarnsOnce(t *testing.T) {
	g, _ := startCapturing(t, uiEnv(t, map[string]string{"GATEWAY_CAPTURE_ENABLED": "false"}), nil)
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if n := len(g.listened()); n != 1 {
		t.Errorf("listen called for %v, want only the proxy", g.listened())
	}
	ls := uiLines(g)
	if len(ls) != 1 || ls[0]["level"] != "warn" {
		t.Errorf("ui lines = %v, want exactly one warn", ls)
	}
}

// AC7, E17: a taken UI port is a startup error and the proxy never starts.
func TestRun_UIBindFailureExitsRuntime(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	inTempDir(t, nil)
	g := startGateway(t, options{
		env:        uiEnv(t, map[string]string{"GATEWAY_UI_LISTEN_ADDR": taken.Addr().String()}),
		realListen: true,
	})
	if code := g.wait(); code != exitRuntime {
		t.Fatalf("exit code = %d, want %d", code, exitRuntime)
	}
	if n := len(linesWith(g, "gateway started")); n != 0 {
		t.Errorf("proxy started despite the UI bind failure")
	}
	ls := linesWith(g, "cannot bind")
	if len(ls) != 1 || ls[0]["key"] != "ui.listen_addr" {
		t.Errorf("cannot bind lines = %v, want one naming ui.listen_addr", ls)
	}
}

// AC28: a stream whose client never reads does not slow the proxy.
func TestRun_ProxyUnaffectedByStuckUISubscriber(t *testing.T) {
	const path = "/anthropic/v1/messages"
	g, url := startCapturing(t, uiEnv(t, nil), nil)
	addr := uiAddr(g)
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, _ = io.WriteString(c, "GET /api/stream HTTP/1.1\r\nHost: "+addr+"\r\n\r\n")
	start := time.Now()
	for range 20 {
		if code, err := do(t, http.MethodPost, url+path, nil, `{"hello":"world"}`); err != nil || code != http.StatusOK {
			t.Fatalf("status %d, err %v", code, err)
		}
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("20 requests took %v with a stuck subscriber", d)
	}
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

// The spec's Goal: a UI server that fails while the gateway runs stops the UI only. The
// proxy keeps serving, the failure is logged once, and a later signal exits cleanly.
func TestRun_UIServeFailureKeepsProxy(t *testing.T) {
	const path = "/anthropic/v1/messages"
	inTempDir(t, nil)
	env := uiEnv(t, map[string]string{"GATEWAY_LOG_LEVEL": "debug"})
	uiLn := make(chan net.Listener, 1)
	g := startGateway(t, options{env: env, realListen: true, bound: func(addr string, ln net.Listener) {
		if addr == env["GATEWAY_UI_LISTEN_ADDR"] {
			uiLn <- ln
		}
	}})
	addr, _ := g.waitLine("gateway started")["addr"].(string)
	url := "http://" + addr
	ui := uiAddr(g)

	// Closing the UI's listener under it makes its Serve return an error.
	_ = (<-uiLn).Close()
	failed := func() int {
		n := 0
		for _, l := range g.lines() {
			if m, _ := l["msg"].(string); strings.HasPrefix(m, "ui serve failed") {
				n++
			}
		}
		return n
	}
	for deadline := time.Now().Add(5 * time.Second); failed() == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the ui failure was never logged")
		}
	}

	select {
	case code := <-g.code:
		g.code <- code
		t.Fatalf("run exited with %d after the ui failed, want it still serving", code)
	default:
	}
	for range 3 {
		if code, err := do(t, http.MethodPost, url+path, nil, `{"hello":"world"}`); err != nil || code != http.StatusOK {
			t.Fatalf("proxy after the ui failed: status %d, err %v", code, err)
		}
	}
	if c, err := net.Dial("tcp", ui); err == nil {
		_ = c.Close()
		t.Errorf("ui address %s still accepts connections", ui)
	}
	if n := failed(); n != 1 {
		t.Errorf("ui failure logged %d times, want 1", n)
	}
	if code := g.stop(); code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
}
