package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// sentinel stands in for a secret. It must never appear in the output (AC11).
const sentinel = "s3ntinel-v4lue"

// inTempDir makes an empty temp dir the working directory and writes config.yaml
// there if content is not nil.
func inTempDir(t *testing.T, content *string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	if content != nil {
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(*content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func ptr(s string) *string { return &s }

// failsBeforeBind asserts run exits with want, never calls listen, writes exactly
// one line, and returns it.
func failsBeforeBind(t *testing.T, g *gateway, want int) map[string]any {
	t.Helper()
	if code := g.wait(); code != want {
		t.Errorf("exit code = %d, want %d", code, want)
	}
	if got := g.listened(); len(got) != 0 {
		t.Errorf("listen was called with %v, want never", got)
	}
	lines := g.lines()
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1\n%s", len(lines), g.stdout.String())
	}
	return lines[0]
}

// noValue asserts value appears nowhere in the output.
func noValue(t *testing.T, g *gateway, value string) {
	t.Helper()
	if out := g.stdout.String(); strings.Contains(out, value) {
		t.Errorf("output contains %q\n%s", value, out)
	}
}

// -h and -help print usage as one JSON line and exit 0 without loading config or
// binding. An invalid config.yaml and env prove nothing was loaded (research Q7).
func TestRun_Help(t *testing.T) {
	for _, arg := range []string{"-h", "-help", "--help"} {
		t.Run(arg, func(t *testing.T) {
			inTempDir(t, ptr("nope: x\n"))
			g := startGateway(t, options{args: []string{arg}, env: map[string]string{"GATEWAY_LOG_LEVEL": "bogus"}})
			line := failsBeforeBind(t, g, exitOK)
			flags, _ := line["flags"].([]any)
			if line["level"] != "info" || line["msg"] != "usage" || len(flags) != 1 || flags[0] != "-config <path>" {
				t.Errorf("usage line = %v", line)
			}
		})
	}
}

// Unknown flags and positional arguments exit 2 with one line, before config loads
// or listen is called, and without the value.
func TestRun_InvalidFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want map[string]any // fields beyond level and msg
	}{
		{name: "unknown_flag", args: []string{"-token=" + sentinel}},
		{
			name: "positional", args: []string{"config.yaml"},
			want: map[string]any{"reason": "unexpected argument", "count": float64(1)},
		},
		{
			name: "positional_after_flag", args: []string{"-config", "a.yaml", sentinel, sentinel},
			want: map[string]any{"reason": "unexpected argument", "count": float64(2)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A config.yaml that exists shows the argument is not read as one.
			inTempDir(t, ptr("log_level: info\n"))
			g := startGateway(t, options{args: tc.args})
			line := failsBeforeBind(t, g, exitConfig)
			if line["level"] != "error" || line["msg"] != "invalid flags" {
				t.Errorf("line = %v", line)
			}
			for k, v := range tc.want {
				if line[k] != v {
					t.Errorf("%s = %v, want %v", k, line[k], v)
				}
			}
			noValue(t, g, sentinel)
			if tc.name == "positional" {
				noValue(t, g, "config.yaml")
			}
		})
	}
}

// AC6: each env var overrides the file's value, and the startup line names the key.
func TestRun_EnvOverridesFile(t *testing.T) {
	file := "listen_addr: 127.0.0.1:1111\nlog_level: error\nshutdown_timeout: 1h\n"

	t.Run("listen_addr", func(t *testing.T) {
		inTempDir(t, &file)
		// log_level from the file is error, so the startup line needs env too; the
		// key under test is still the only one checked in the listen record.
		g := startGateway(t, options{env: map[string]string{"GATEWAY_LISTEN_ADDR": "127.0.0.1:2222", "GATEWAY_LOG_LEVEL": "info"}})
		line := g.waitLine("gateway started")
		if got := g.listened(); !slices.Equal(got, []string{"127.0.0.1:2222"}) {
			t.Errorf("listen = %v, want [127.0.0.1:2222]", got)
		}
		wantOverrides(t, line, "listen_addr", "log_level")
	})

	t.Run("log_level", func(t *testing.T) {
		inTempDir(t, &file)
		// The file says error, so the info startup line is only there if env won.
		g := startGateway(t, options{env: map[string]string{"GATEWAY_LOG_LEVEL": "info"}})
		line := g.waitLine("gateway started")
		wantOverrides(t, line, "log_level")
	})

	t.Run("shutdown_timeout", func(t *testing.T) {
		inTempDir(t, &file)
		accepted := make(chan struct{}, 1)
		g := startGateway(t, options{
			env:      map[string]string{"GATEWAY_LOG_LEVEL": "info", "GATEWAY_SHUTDOWN_TIMEOUT": "100ms"},
			accepted: accepted,
		})
		line := g.waitLine("gateway started")
		wantOverrides(t, line, "log_level", "shutdown_timeout")

		// A connection that has sent part of a request is active, so Shutdown waits
		// for it. It gives up after env's 100ms, not the file's hour.
		addr, _ := line["addr"].(string)
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		if _, err := conn.Write([]byte("GET /healthz HTTP/1.1\r\n")); err != nil {
			t.Fatal(err)
		}
		// Cancel only once the server has the connection, so Shutdown sees it.
		select {
		case <-accepted:
		case <-time.After(2 * time.Second):
			t.Fatal("the server never accepted the connection")
		}

		start := time.Now()
		if code := g.stop(); code != exitRuntime {
			t.Errorf("exit code = %d, want 1 (shutdown gave up)", code)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("shutdown took %v, want about 100ms", took)
		}
		// in_flight is 0: the half-sent request holds the connection active, which
		// is what Shutdown waits for, but no handler is running, because net/http
		// hasn't finished reading the headers. The count is handlers, not connections
		// (plan.md, In-flight count); TestRun_ShutdownTimeout is where it is 1.
		timedOut := linesWith(g, "shutdown timed out")
		if len(timedOut) != 1 || timedOut[0]["in_flight"] != float64(0) {
			t.Errorf("shutdown timed out lines = %v, want one with in_flight 0", timedOut)
		}
		if n := len(linesWith(g, "gateway stopped")); n != 0 {
			t.Errorf("got %d gateway stopped lines, want none", n)
		}
	})
}

// linesWith returns the lines whose msg is msg.
func linesWith(g *gateway, msg string) []map[string]any {
	g.t.Helper()
	var found []map[string]any
	for _, l := range g.lines() {
		if l["msg"] == msg {
			found = append(found, l)
		}
	}
	return found
}

func wantOverrides(t *testing.T, line map[string]any, keys ...string) {
	t.Helper()
	var got []string
	for _, k := range line["env_overrides"].([]any) {
		got = append(got, k.(string))
	}
	if !slices.Equal(got, keys) {
		t.Errorf("env_overrides = %v, want %v", got, keys)
	}
}

// AC7: log_level from the file or from env decides whether the info startup line
// is written.
func TestRun_LogLevel(t *testing.T) {
	cases := []struct {
		name    string
		file    *string
		env     map[string]string
		startup bool
	}{
		{name: "file_error", file: ptr("log_level: error\n"), startup: false},
		{name: "env_error", env: map[string]string{"GATEWAY_LOG_LEVEL": "error"}, startup: false},
		{name: "file_info_env_error", file: ptr("log_level: info\n"), env: map[string]string{"GATEWAY_LOG_LEVEL": "error"}, startup: false},
		{name: "file_info", file: ptr("log_level: info\n"), startup: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inTempDir(t, tc.file)
			g := startGateway(t, options{env: tc.env})
			// No wait for the startup line is needed: run writes it after the bind
			// and before it reads ctx, so it is in the output by the time stop returns.
			waitListened(t, g)
			if code := g.stop(); code != exitOK {
				t.Errorf("exit code = %d, want 0", code)
			}
			got := false
			for _, l := range g.lines() {
				if l["msg"] == "gateway started" {
					got = true
				}
			}
			if got != tc.startup {
				t.Errorf("startup line present = %v, want %v\n%s", got, tc.startup, g.stdout.String())
			}
		})
	}
}

func waitListened(t *testing.T, g *gateway) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(g.listened()) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("listen was never called\n%s", g.stdout.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// AC8, AC11: an unknown key exits 2 before binding, naming the key and the file,
// not the value.
func TestRun_UnknownKey(t *testing.T) {
	inTempDir(t, ptr("log_level: info\nlisten_adr: "+sentinel+"\n"))
	g := startGateway(t, options{})
	line := failsBeforeBind(t, g, exitConfig)
	want := map[string]any{"level": "error", "msg": "invalid config", "key": "listen_adr", "source": "config.yaml", "reason": "unknown key", "line": float64(2)}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("%s = %v, want %v", k, line[k], v)
		}
	}
	noValue(t, g, sentinel)
}

// AC9, AC11.
func TestRun_InvalidEnvLogLevel(t *testing.T) {
	inTempDir(t, nil)
	g := startGateway(t, options{env: map[string]string{"GATEWAY_LOG_LEVEL": sentinel}})
	line := failsBeforeBind(t, g, exitConfig)
	if line["source"] != "GATEWAY_LOG_LEVEL" || line["key"] != "log_level" || line["reason"] != "invalid level" {
		t.Errorf("line = %v", line)
	}
	noValue(t, g, sentinel)
}

// AC10, AC11: "abc" appears nowhere.
func TestRun_InvalidEnvShutdownTimeout(t *testing.T) {
	inTempDir(t, nil)
	g := startGateway(t, options{env: map[string]string{"GATEWAY_SHUTDOWN_TIMEOUT": "abc"}})
	line := failsBeforeBind(t, g, exitConfig)
	if line["source"] != "GATEWAY_SHUTDOWN_TIMEOUT" || line["key"] != "shutdown_timeout" || line["reason"] != "invalid duration" {
		t.Errorf("line = %v", line)
	}
	noValue(t, g, "abc")
}

// AC12: an empty env var is unset: the file's value wins if there is one, else the
// default.
func TestRun_EmptyEnvIsUnset(t *testing.T) {
	t.Run("file_value", func(t *testing.T) {
		inTempDir(t, ptr("log_level: error\n"))
		g := startGateway(t, options{env: map[string]string{"GATEWAY_LOG_LEVEL": ""}})
		waitListened(t, g) // as in TestRun_LogLevel, stop returns after any startup line
		if code := g.stop(); code != exitOK {
			t.Errorf("exit code = %d, want 0", code)
		}
		if out := g.stdout.String(); out != "" {
			t.Errorf("got output at the file's level error, want none\n%s", out)
		}
	})
	t.Run("default", func(t *testing.T) {
		inTempDir(t, nil)
		g := startGateway(t, options{env: map[string]string{"GATEWAY_LOG_LEVEL": ""}})
		line := g.waitLine("gateway started")
		wantOverrides(t, line)
		if line["config_source"] != "defaults" {
			t.Errorf("config_source = %v, want defaults", line["config_source"])
		}
	})
}

// An empty config.yaml and a verbatim copy of config.example.yaml each start on the
// defaults, with the file as the config source.
func TestRun_EmptyConfigFile(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"empty": "", "example": string(example)} {
		t.Run(name, func(t *testing.T) {
			inTempDir(t, &content)
			g := startGateway(t, options{})
			line := g.waitLine("gateway started")
			if line["config_source"] != "config.yaml" {
				t.Errorf("config_source = %v, want config.yaml", line["config_source"])
			}
			if got := g.listened(); !slices.Equal(got, []string{"127.0.0.1:7197"}) {
				t.Errorf("listen = %v, want the default [127.0.0.1:7197]", got)
			}
		})
	}
}

// An invalid address fails with the other config errors, before listen.
func TestRun_InvalidAddressFailsBeforeBind(t *testing.T) {
	inTempDir(t, nil)
	g := startGateway(t, options{env: map[string]string{"GATEWAY_LISTEN_ADDR": sentinel}})
	line := failsBeforeBind(t, g, exitConfig)
	if line["key"] != "listen_addr" || line["reason"] != "invalid address" {
		t.Errorf("line = %v", line)
	}
	noValue(t, g, sentinel)
}

// Spec 022 AC2: a listener off loopback stops the gateway before anything is bound,
// with exit 2 and one line naming the key, the value and the reason.
func TestRun_NonLoopbackListenerFailsBeforeBind(t *testing.T) {
	cases := map[string]struct{ env, key string }{
		"api":        {"GATEWAY_LISTEN_ADDR", "listen_addr"},
		"ui":         {"GATEWAY_UI_LISTEN_ADDR", "ui.listen_addr"},
		"http_proxy": {"GATEWAY_EFFECTS_NETWORK_HTTP_PROXY_ADDR", "effects.network.http_proxy_addr"},
		"socks":      {"GATEWAY_EFFECTS_NETWORK_SOCKS_PROXY_ADDR", "effects.network.socks_proxy_addr"},
	}
	for name, c := range cases {
		for _, v := range []string{"0.0.0.0:7301", "[::]:7301", ":7301", "192.168.1.5:7301", "gateway.lan:7301"} {
			t.Run(name+"_"+v, func(t *testing.T) {
				inTempDir(t, nil)
				g := startGateway(t, options{env: map[string]string{c.env: v}})
				line := failsBeforeBind(t, g, exitConfig)
				if line["key"] != c.key || line["value"] != v || line["reason"] != config.ReasonNotLoopback || line["source"] != c.env {
					t.Errorf("line = %v", line)
				}
			})
		}
	}
}

// A bind the OS refuses exits 1 with a fixed reason and no address.
func TestRun_BindErrors(t *testing.T) {
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 7197}
	cases := map[string]struct {
		err    error
		reason string
	}{
		"in_use": {&net.OpError{Op: "listen", Net: "tcp", Addr: addr, Err: os.NewSyscallError("bind", syscall.EADDRINUSE)}, "address in use"},
		"other":  {&net.OpError{Op: "listen", Net: "tcp", Addr: addr, Err: errors.New("permission denied")}, "bind failed"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			inTempDir(t, nil)
			g := startGateway(t, options{listenErr: tc.err})
			if code := g.wait(); code != exitRuntime {
				t.Errorf("exit code = %d, want 1", code)
			}
			lines := g.lines()
			if len(lines) != 1 || lines[0]["msg"] != "cannot bind" || lines[0]["key"] != "listen_addr" || lines[0]["reason"] != tc.reason {
				t.Errorf("lines = %v, want one cannot bind line with reason %q", lines, tc.reason)
			}
			// The whole address, not the bare port: "7197" can turn up in a
			// timestamp's nanoseconds.
			noValue(t, g, "127.0.0.1:7197")
			noValue(t, g, "127.0.0.1")
		})
	}
}
