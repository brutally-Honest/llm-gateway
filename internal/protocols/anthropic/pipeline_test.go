package anthropic_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/protocols/anthropic"
	"github.com/brutally-honest/llm-gateway/internal/server"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// AC48: a malformed body gives parse failed and a capture_failed line with stage
// parse, and the raw bodies are stored as they went over the wire. It runs through
// the real proxy, sink, pipeline and store.
func TestParse_FailureKeepsRaw(t *testing.T) {
	const (
		goodRequest  = `{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`
		goodResponse = `{"type":"message","role":"assistant","content":[{"type":"text","text":"hello"}]}`
		malformed    = `{"type":"message","content":[not json`
	)
	cases := []struct {
		name, request, response string
	}{
		{"response", goodRequest, malformed},
		{"request", malformed, goodResponse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkNoLeaksAtEnd(t)
			up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			})

			g := startCapturingGateway(t, upstreamConfig(up.URL))
			res, err := http.Post("http://"+g.addr+"/anthropic/v1/messages", "application/json",
				strings.NewReader(tc.request))
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if err != nil || res.StatusCode != http.StatusOK || string(body) != tc.response {
				t.Fatalf("client got %d %q (%v), want 200 with upstream's body", res.StatusCode, body, err)
			}

			id := g.queuedID(t)
			r := g.drain(t)
			row, err := r.Exchange(id)
			if err != nil {
				t.Fatalf("exchange %s: %v", id, err)
			}
			if row.Parse != string(core.ParseFailed) {
				t.Errorf("parse = %q, want failed", row.Parse)
			}
			for _, b := range []struct{ name, hash, want string }{
				{"request", row.RequestBody, tc.request},
				{"response", row.ResponseBody, tc.response},
			} {
				got, err := r.Content(b.hash)
				if err != nil || string(got) != b.want {
					t.Errorf("stored %s body %q (%v), want %q", b.name, got, err, b.want)
				}
			}

			var failed []map[string]any
			for _, l := range logLines(t, g.logs) {
				if l["msg"] == "capture_failed" {
					failed = append(failed, l)
				}
			}
			if len(failed) != 1 || failed[0]["stage"] != "parse" || failed[0]["request_id"] != id {
				t.Errorf("capture_failed lines = %v, want one with stage parse for %s", failed, id)
			}
		})
	}
}

// capturingGateway is a gateway whose registry captures into a real sink, pipeline
// and store in a temporary directory.
type capturingGateway struct {
	gateway
	dir  string
	srv  *server.Server
	sink *capture.Sink
	st   *store.Store
}

func startCapturingGateway(t *testing.T, up config.Upstream) *capturingGateway {
	t.Helper()
	dir := t.TempDir()
	logs := &syncBuffer{}
	log := logging.New(logs, "debug")
	st, err := store.Open(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	sink := capture.NewSink(capture.Config{QueueSize: 8, Workers: 1, DecodeLimit: decodeLimit}, st, log)
	reg := core.NewRegistry(log, core.WithCapture(core.Capture{
		Sink: sink, Budget: core.NewBudget(1 << 20), MaxBodyBytes: decodeLimit, Principal: core.LocalPrincipal{},
	}))
	reg.AddAdapter(anthropic.Adapter{}, up)
	srv := server.New(log, reg.Mount)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return &capturingGateway{
		gateway: gateway{addr: ln.Addr().String(), logs: logs},
		dir:     dir, srv: srv, sink: sink, st: st,
	}
}

// queuedID is the request ID of the messages exchange, whose capture must have been
// queued.
func (g *capturingGateway) queuedID(t *testing.T) string {
	t.Helper()
	line := g.accessLine(t, "/anthropic/v1/messages")
	if line["capture"] != core.CaptureQueued {
		t.Fatalf("capture = %v, want %s", line["capture"], core.CaptureQueued)
	}
	id, _ := line["request_id"].(string)
	return id
}

// drain stops the gateway, drains the sink into the store and opens it for reading.
func (g *capturingGateway) drain(t *testing.T) *store.Reader {
	t.Helper()
	_ = g.srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if n := g.sink.Close(ctx); n != 0 {
		t.Fatalf("%d exchanges left undrained", n)
	}
	if err := g.st.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := store.OpenReader(g.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// A response the gateway wrote itself is stored with its flag and no error event:
// the parser never reads it as the provider's. Through the real proxy, sink,
// pipeline and store: an unreachable upstream (502, gateway_error) and a client that
// leaves before upstream answers (499, client_disconnected). A refused dial leaves
// the request copy empty (T17), so only the 499 case has request events to check.
func TestParse_GatewayMadeResponseStoredWithoutError(t *testing.T) {
	const request = `{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`
	for _, tc := range []struct {
		name    string
		up      func(t *testing.T) config.Upstream
		wait    time.Duration // client timeout; 0 waits for the answer
		status  int
		request bool // the request copy is whole, so parse is ok with a request event
	}{
		{"upstream_unreachable", func(t *testing.T) config.Upstream { return upstreamConfig(refusedURL(t)) }, 0,
			http.StatusBadGateway, false},
		{"client_left", func(t *testing.T) config.Upstream { return upstreamConfig(hangingUpstream(t)) },
			200 * time.Millisecond, 499, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkNoLeaksAtEnd(t)
			g := startCapturingGateway(t, tc.up(t))
			client := &http.Client{Timeout: tc.wait}
			res, err := client.Post("http://"+g.addr+"/anthropic/v1/messages", "application/json",
				strings.NewReader(request))
			if err == nil {
				_, _ = io.Copy(io.Discard, res.Body)
				_ = res.Body.Close()
			}
			id := g.queuedID(t)
			r := g.drain(t)
			row, err := r.Exchange(id)
			if err != nil {
				t.Fatalf("exchange %s: %v", id, err)
			}
			if row.Status != tc.status || (row.GatewayError == "" && !row.ClientDisconnected) {
				t.Errorf("exchange status %d, gateway_error %q, client_disconnected %v; want %d with a flag",
					row.Status, row.GatewayError, row.ClientDisconnected, tc.status)
			}
			if tc.request && row.Parse != string(core.ParseOK) {
				t.Errorf("parse = %q, want ok", row.Parse)
			}
			events, err := r.Events(id)
			if err != nil {
				t.Fatal(err)
			}
			var sawRequest bool
			for _, ev := range events {
				switch ev.Kind {
				case string(core.KindRequest):
					sawRequest = true
				case string(core.KindError):
					t.Errorf("stored an error event for a gateway-made response: %s", ev.Payload)
				}
			}
			if tc.request && !sawRequest {
				t.Error("the request event is missing")
			}
		})
	}
}

// logLines decodes every JSON log line written so far.
func logLines(t *testing.T, logs *syncBuffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	sc := bufio.NewScanner(strings.NewReader(logs.String()))
	for sc.Scan() {
		var l map[string]any
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("log line %q: %v", sc.Text(), err)
		}
		lines = append(lines, l)
	}
	return lines
}
