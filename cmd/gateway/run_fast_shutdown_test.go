package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// proxyAddr is the address of the HTTP forward proxy, from its listener started line.
func proxyAddr(g *gateway) string {
	g.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, l := range g.lines() {
			if l["msg"] == "proxy listener started" && l["key"] == "effects.network.http_proxy_addr" {
				addr, _ := l["addr"].(string)
				return addr
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	g.t.Fatalf("no http proxy listener line\n%s", g.stdout.String())
	return ""
}

// holdingTarget is a TCP listener that accepts connections and holds them open, the far
// end of a tunnel that never ends by itself.
func holdingTarget(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	return ln.Addr().String()
}

// endsSoon reports whether r reaches EOF or an error within d.
func endsSoon(r io.Reader, d time.Duration) bool {
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, r)
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// Spec 019 AC1, AC2: with an open CONNECT tunnel and an open UI live stream, and the
// default 10 s grace period, shutdown returns in under 2 s: neither is waited for. The
// tunnel is cut and stored as closed early, the stream ends, and run exits 0.
func TestRun_ShutdownCutsTunnelsAndUIStreamsAtOnce(t *testing.T) {
	dir := t.TempDir()
	up := jsonUpstream(t)
	g, _ := startCapturing(t, captureEnv(dir, up, map[string]string{
		"GATEWAY_UI_LISTEN_ADDR":                   "127.0.0.2:0",
		"GATEWAY_EFFECTS_NETWORK_HTTP_PROXY_ADDR":  "127.0.0.3:0",
		"GATEWAY_EFFECTS_NETWORK_SOCKS_PROXY_ADDR": "127.0.0.4:0",
	}), nil)
	target := holdingTarget(t)

	tunnel, err := net.Dial("tcp", proxyAddr(g))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tunnel.Close() }()
	if _, err := io.WriteString(tunnel, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	tr := bufio.NewReader(tunnel)
	if status, err := tr.ReadString('\n'); err != nil || !strings.Contains(status, " 200 ") {
		t.Fatalf("CONNECT = %q, %v", status, err)
	}

	addr := uiAddr(g)
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/stream", nil)
	req.Header.Set("Accept", "text/event-stream")
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Body.Close() }()
	sr := bufio.NewReader(stream.Body)
	if line, err := sr.ReadString('\n'); err != nil || stream.StatusCode != http.StatusOK {
		t.Fatalf("stream = %d %q, %v", stream.StatusCode, line, err)
	}

	start := time.Now()
	code := g.stop()
	if took := time.Since(start); took >= 2*time.Second {
		t.Errorf("shutdown took %v, want under 2s", took)
	}
	if code != exitOK {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !endsSoon(tr, time.Second) {
		t.Error("the tunnel is still open after shutdown")
	}
	if !endsSoon(sr, time.Second) {
		t.Error("the UI stream is still open after shutdown")
	}
	effs, err := openReader(t, dir).WithContext(context.Background()).ListUnassigned(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(effs) != 1 || effs[0].Outcome != string(core.NetClosedEarly) {
		t.Errorf("stored connections = %+v, want one closed early", effs)
	}
}

// Spec 019 AC3: a streamed model call still running at shutdown gets the grace period,
// no more: shutdown waits for it, then cuts it, logs shutdown timed out, and the call is
// stored, cut by the gateway (gateway_error gateway_shutdown), not as a client that left.
func TestRun_ShutdownCutsInFlightCallAfterGrace(t *testing.T) {
	const (
		path  = "/anthropic/v1/messages"
		grace = 400 * time.Millisecond
	)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) }) // before up.Close, which waits for the handler
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
		w.(http.Flusher).Flush()
		select { // a stream that would run far past the grace period
		case <-release:
		case <-r.Context().Done():
		}
	})
	dir := t.TempDir()
	g, url := startCapturing(t, captureEnv(dir, up, map[string]string{"GATEWAY_SHUTDOWN_TIMEOUT": grace.String()}), nil)

	resp, err := http.Post(url+path, "application/json", strings.NewReader(`{"model":"m","stream":true,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := bufio.NewReader(resp.Body)
	if line, err := body.ReadString('\n'); err != nil || !strings.HasPrefix(line, "event: message_start") {
		t.Fatalf("first line = %q, %v", line, err)
	}

	start := time.Now()
	code := g.stop()
	took := time.Since(start)
	if took < grace {
		t.Errorf("shutdown took %v, want at least the %v grace period", took, grace)
	}
	if took >= grace+2*time.Second {
		t.Errorf("shutdown took %v, want the grace period plus well under 2s", took)
	}
	if code != exitRuntime {
		t.Errorf("exit code = %d, want 1 (a call was cut)", code)
	}
	if l := linesWith(g, "shutdown timed out"); len(l) != 1 || l[0]["in_flight"] != float64(1) {
		t.Errorf("shutdown timed out lines = %v, want one with in_flight 1", l)
	}
	if !endsSoon(body, time.Second) {
		t.Error("the client's stream is still open after shutdown")
	}

	ids := g.requestIDs(path)
	if len(ids) != 1 {
		t.Fatalf("request lines = %d, want 1", len(ids))
	}
	ex, err := openReader(t, dir).Exchange(ids[0])
	if err != nil {
		t.Fatalf("the cut call was not stored: %v", err)
	}
	if ex.GatewayError != "gateway_shutdown" || ex.ClientDisconnected || ex.UpstreamAborted || !ex.Stream {
		t.Errorf("stored call: gateway_error %v, client_disconnected %v, upstream_aborted %v, stream %v; want a stream cut by gateway_shutdown",
			ex.GatewayError, ex.ClientDisconnected, ex.UpstreamAborted, ex.Stream)
	}
	if line := stoppedLine(t, g); line["undrained"] != float64(0) || line["store_failed"] != float64(0) {
		t.Errorf("capture stopped = %v, want nothing undrained or failed", line)
	}
}
