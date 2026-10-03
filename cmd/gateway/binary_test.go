package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestBinary_SignalShutdown covers what run's in-process tests can't: a real signal
// reaching the built binary (AC15), and every byte the process writes (AC13).
func TestBinary_SignalShutdown(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "gateway")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Env = buildEnv()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	for name, sig := range map[string]syscall.Signal{"SIGTERM": syscall.SIGTERM, "SIGINT": syscall.SIGINT} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(bin)
			cmd.Dir = t.TempDir() // no config.yaml
			// The store goes in a temp dir, never the default data dir (spec 002, Do).
			cmd.Env = append(gatewayFreeEnv(), "GATEWAY_LISTEN_ADDR=127.0.0.1:0", "GATEWAY_CAPTURE_DIR="+t.TempDir())
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cmd.Process.Kill() })

			var (
				mu    sync.Mutex
				lines []string
			)
			started := make(chan string, 1)
			readDone := make(chan struct{})
			go func() {
				defer close(readDone)
				sc := bufio.NewScanner(stdout)
				for sc.Scan() {
					mu.Lock()
					lines = append(lines, sc.Text())
					mu.Unlock()
					var m map[string]any
					if json.Unmarshal(sc.Bytes(), &m) == nil && m["msg"] == "gateway started" {
						addr, _ := m["addr"].(string)
						started <- addr
					}
				}
			}()

			var addr string
			select {
			case addr = <-started:
			case <-time.After(10 * time.Second):
				t.Fatalf("no startup line\nstderr: %s", stderr.String())
			}
			resp, err := http.Get("http://" + addr + "/healthz")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("/healthz status = %d, want 200", resp.StatusCode)
			}

			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			<-readDone // stdout closes when the process exits
			exited := make(chan error, 1)
			go func() { exited <- cmd.Wait() }()
			select {
			case err := <-exited:
				if err != nil {
					t.Errorf("exit: %v, want status 0", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the gateway did not exit after the signal")
			}

			if s := stderr.String(); s != "" {
				t.Errorf("stderr = %q, want empty", s)
			}
			mu.Lock()
			defer mu.Unlock()
			var stopped, requests int
			for _, line := range lines {
				var m map[string]any
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					t.Errorf("stdout line is not JSON: %q", line)
					continue
				}
				for _, k := range []string{"level", "ts", "msg"} {
					if _, ok := m[k]; !ok {
						t.Errorf("line has no %q: %q", k, line)
					}
				}
				switch m["msg"] {
				case "gateway stopped":
					stopped++
				case "request":
					requests++
					if id, _ := m["request_id"].(string); len(id) != 26 {
						t.Errorf("request line request_id = %v", m["request_id"])
					}
				}
			}
			if stopped != 1 || requests != 1 {
				t.Errorf("got %d gateway stopped and %d request lines, want 1 each\n%s", stopped, requests, strings.Join(lines, "\n"))
			}
		})
	}
}

// gatewayFreeEnv is the test's environment without GATEWAY_* variables, so the
// gateway's config comes only from what the test sets.
func gatewayFreeEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GATEWAY_") {
			env = append(env, kv)
		}
	}
	return env
}

// buildEnv is the go toolchain's environment: the test's, with HOME as the test
// process got it, since TestMain points HOME at a temp dir and the toolchain keeps its
// caches under HOME.
func buildEnv() []string {
	env := os.Environ()
	if buildHome != "" {
		env = append(env, "HOME="+buildHome)
	}
	return env
}
