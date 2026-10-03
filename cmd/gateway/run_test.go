package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/capture"
)

// syncBuffer is a goroutine-safe stdout.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// gateway is a run call in a goroutine.
type gateway struct {
	t      *testing.T
	stdout *syncBuffer
	cancel context.CancelFunc
	code   chan int

	mu     sync.Mutex
	listen []string // addresses run asked listen for
}

// options for startGateway.
type options struct {
	args []string
	env  map[string]string
	// realListen binds the address run asks for. Otherwise the address is recorded
	// and 127.0.0.1:0 is bound instead, so no test binds 7197 (plan.md, Listener).
	realListen bool
	// listenErr, if set, is what listen returns instead of binding.
	listenErr error
	// mount adds test-only routes, under the same middleware as /healthz.
	mount []func(chi.Router)
	// accepted, if set, gets a signal each time the listener accepts a connection.
	accepted chan struct{}
	// openStore, if set, replaces store.Open.
	openStore func(dir string, log *zap.Logger) (capture.Store, error)
}

// signalListener signals accepted after each connection Accept returns.
type signalListener struct {
	net.Listener
	accepted chan<- struct{}
}

func (l signalListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.accepted <- struct{}{}
	}
	return c, err
}

func startGateway(t *testing.T, o options) *gateway {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	g := &gateway{t: t, stdout: &syncBuffer{}, cancel: cancel, code: make(chan int, 1)}
	env := withTempDataDir(t, o.env)
	d := deps{
		args: o.args,
		lookupEnv: func(name string) (string, bool) {
			v, ok := env[name]
			return v, ok
		},
		stdout:    g.stdout,
		mount:     o.mount,
		openStore: o.openStore,
		listen: func(network, addr string) (net.Listener, error) {
			g.mu.Lock()
			g.listen = append(g.listen, addr)
			g.mu.Unlock()
			if o.listenErr != nil {
				return nil, o.listenErr
			}
			if !o.realListen {
				addr = "127.0.0.1:0"
			}
			ln, err := net.Listen(network, addr)
			if err != nil || o.accepted == nil {
				return ln, err
			}
			return signalListener{Listener: ln, accepted: o.accepted}, nil
		},
	}
	go func() { g.code <- run(ctx, d) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-g.code:
		case <-time.After(5 * time.Second):
			t.Error("run did not return after cancel")
		}
	})
	return g
}

// withTempDataDir returns env with XDG_DATA_HOME set to a temp dir, so the default
// capture.dir resolves there and never to the real data dir (spec 002, Do). A test
// that sets GATEWAY_CAPTURE_DIR, XDG_DATA_HOME or HOME chooses for itself. It is
// XDG_DATA_HOME and not GATEWAY_CAPTURE_DIR so the startup line's env_overrides,
// which 001's tests assert, stays as they expect.
func withTempDataDir(t *testing.T, env map[string]string) map[string]string {
	t.Helper()
	out := maps.Clone(env)
	if out == nil {
		out = map[string]string{}
	}
	for _, k := range []string{"GATEWAY_CAPTURE_DIR", "XDG_DATA_HOME", "HOME"} {
		if _, ok := out[k]; ok {
			return out
		}
	}
	out["XDG_DATA_HOME"] = t.TempDir()
	return out
}

// lines parses every stdout line as JSON, failing the test on any that isn't.
func (g *gateway) lines() []map[string]any {
	g.t.Helper()
	var parsed []map[string]any
	for _, line := range strings.Split(strings.TrimSuffix(g.stdout.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			g.t.Fatalf("stdout line is not JSON: %q", line)
		}
		parsed = append(parsed, m)
	}
	return parsed
}

// waitLine waits for the first line with msg.
func (g *gateway) waitLine(msg string) map[string]any {
	g.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, l := range g.lines() {
			if l["msg"] == msg {
				return l
			}
		}
		if time.Now().After(deadline) {
			g.t.Fatalf("no %q line\n%s", msg, g.stdout.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stop cancels ctx and returns run's exit code.
func (g *gateway) stop() int {
	g.t.Helper()
	g.cancel()
	return g.wait()
}

// wait returns run's exit code without cancelling, for runs that exit on their own.
func (g *gateway) wait() int {
	g.t.Helper()
	select {
	case code := <-g.code:
		g.code <- code // for Cleanup
		return code
	case <-time.After(5 * time.Second):
		g.t.Fatal("run did not return")
		return -1
	}
}

// listened returns the addresses run asked listen for.
func (g *gateway) listened() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.listen)
}

// AC3: one startup line with version, the bound address and the config source.
func TestRun_StartupLine(t *testing.T) {
	t.Chdir(t.TempDir())
	g := startGateway(t, options{
		env:        map[string]string{"GATEWAY_LISTEN_ADDR": "127.0.0.1:0"},
		realListen: true,
	})
	line := g.waitLine("gateway started")

	if line["level"] != "info" || line["version"] != "dev" || line["config_source"] != "defaults" {
		t.Errorf("startup line = %v", line)
	}
	if got, _ := line["env_overrides"].([]any); len(got) != 1 || got[0] != "listen_addr" {
		t.Errorf("env_overrides = %v, want [listen_addr]", line["env_overrides"])
	}
	// addr is the real bound address, so it serves.
	addr, _ := line["addr"].(string)
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz at the logged addr %q: %v", addr, err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != `{"status":"ok"}` {
		t.Errorf("healthz = %d %q", resp.StatusCode, body)
	}

	if code := g.stop(); code != exitOK {
		t.Errorf("exit code = %d, want 0", code)
	}
	g.waitLine("gateway stopped")
	n := 0
	for _, l := range g.lines() {
		if l["msg"] == "gateway started" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("got %d startup lines, want 1", n)
	}
}

// AC4: no config.yaml, no -config, no env: built-in defaults, source "defaults".
func TestRun_DefaultsWhenNoConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	g := startGateway(t, options{})
	line := g.waitLine("gateway started")

	if line["config_source"] != "defaults" {
		t.Errorf("config_source = %v, want defaults", line["config_source"])
	}
	// Always an array, so an empty one is [] and not null.
	if got, ok := line["env_overrides"].([]any); !ok || len(got) != 0 {
		t.Errorf("env_overrides = %#v, want []", line["env_overrides"])
	}
	if code := g.stop(); code != exitOK {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// AC5: with no config change, run asks to listen on 127.0.0.1:7197. The listen is
// recorded, not performed, so the test never binds 7197.
func TestRun_DefaultListenAddr(t *testing.T) {
	t.Chdir(t.TempDir())
	g := startGateway(t, options{})
	g.waitLine("gateway started")

	if got := g.listened(); !slices.Equal(got, []string{"127.0.0.1:7197"}) {
		t.Errorf("listen addresses = %v, want [127.0.0.1:7197]", got)
	}
}
