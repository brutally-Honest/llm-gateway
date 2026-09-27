package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// served starts run on a real 127.0.0.1:0 listener at log_level debug, so every line
// the gateway can write is written, and returns it with its base URL.
func served(t *testing.T, mount ...func(chi.Router)) (*gateway, string) {
	t.Helper()
	inTempDir(t, nil)
	g := startGateway(t, options{
		env:        map[string]string{"GATEWAY_LISTEN_ADDR": "127.0.0.1:0", "GATEWAY_LOG_LEVEL": "debug"},
		realListen: true,
		mount:      mount,
	})
	addr, _ := g.waitLine("gateway started")["addr"].(string)
	return g, "http://" + addr
}

// send makes a request and returns its status, failing the test on a transport error.
func send(t *testing.T, method, url string, header map[string]string) int {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// waitCount waits until n lines have msg.
func (g *gateway) waitCount(msg string, n int) {
	g.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c := 0
		for _, l := range g.lines() {
			if l["msg"] == msg {
				c++
			}
		}
		if c == n {
			return
		}
		if c > n || time.Now().After(deadline) {
			g.t.Fatalf("got %d %q lines, want %d\n%s", c, msg, n, g.stdout.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// AC13, in-process: across startup, a 200, a 404, a recovered panic and shutdown,
// every line is JSON with level, ts and msg, and request lines carry request_id.
func TestRun_AllLinesJSON(t *testing.T) {
	g, url := served(t, func(r chi.Router) {
		r.Get("/panic", func(http.ResponseWriter, *http.Request) { panic("boom") })
	})
	send(t, http.MethodGet, url+"/healthz", nil)
	send(t, http.MethodGet, url+"/nope", nil)
	send(t, http.MethodGet, url+"/panic", nil)
	g.waitCount("request", 3)
	if code := g.stop(); code != exitOK {
		t.Errorf("exit code = %d, want 0", code)
	}

	lines := g.lines() // fails the test on any line that isn't JSON
	for _, l := range lines {
		for _, k := range []string{"level", "ts", "msg"} {
			if _, ok := l[k]; !ok {
				t.Errorf("line has no %q: %v", k, l)
			}
		}
		if l["msg"] == "request" {
			if id, _ := l["request_id"].(string); len(id) != 26 {
				t.Errorf("request line request_id = %v, want a 26-character ID", l["request_id"])
			}
		}
	}
	for _, msg := range []string{"gateway started", "panic recovered", "gateway stopped"} {
		found := false
		for _, l := range lines {
			found = found || l["msg"] == msg
		}
		if !found {
			t.Errorf("no %q line\n%s", msg, g.stdout.String())
		}
	}
}

// AC14: a sentinel in Authorization, x-api-key and the query string reaches no log
// line, on /healthz and on a 404, at debug level.
func TestRun_SecretsNotLogged(t *testing.T) {
	g, url := served(t)
	header := map[string]string{"Authorization": "Bearer " + sentinel, "x-api-key": sentinel}
	for _, path := range []string{"/healthz", "/not-a-route"} {
		send(t, http.MethodGet, url+path+"?key="+sentinel, header)
	}
	g.waitCount("request", 2)
	g.stop()
	noValue(t, g, sentinel)
}

// AC17: a handler panic gives a 500 and one panic recovered line, the server keeps
// serving, and nothing reaches stderr.
func TestRun_PanicRecovered(t *testing.T) {
	// Swapped before run builds its logger, which binds zap's error output to
	// os.Stderr at that moment (research Q6). Not parallel: os.Stderr is global.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = stderr })
	stderrOut := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		stderrOut <- string(b)
	}()

	g, url := served(t, func(r chi.Router) {
		r.Get("/panic", func(http.ResponseWriter, *http.Request) { panic("boom") })
	})
	if code := send(t, http.MethodGet, url+"/panic", nil); code != http.StatusInternalServerError {
		t.Errorf("/panic status = %d, want 500", code)
	}
	if code := send(t, http.MethodGet, url+"/healthz", nil); code != http.StatusOK {
		t.Errorf("/healthz after the panic: status = %d, want 200", code)
	}
	g.waitCount("request", 2)
	g.waitCount("panic recovered", 1)
	if code := g.stop(); code != exitOK {
		t.Errorf("exit code = %d, want 0", code)
	}

	os.Stderr = stderr
	_ = w.Close()
	if out := <-stderrOut; out != "" {
		t.Errorf("stderr = %q, want empty", out)
	}
	for _, l := range g.lines() {
		if l["msg"] == "panic recovered" && (l["level"] != "error" || l["panic_type"] != "string") {
			t.Errorf("panic line = %v", l)
		}
	}
	if strings.Contains(fmt.Sprint(g.lines()), "boom") {
		t.Errorf("panic value appears in the output")
	}
}
