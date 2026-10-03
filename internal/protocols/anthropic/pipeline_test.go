package anthropic_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
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
	return startCapturingGatewayWith(t, up, 1<<20, decodeLimit)
}

// startCapturingGatewayWith is startCapturingGateway with the capture's memory budget
// and max_body_bytes set; the parse's decode limit stays decodeLimit.
func startCapturingGatewayWith(t *testing.T, up config.Upstream, budget, maxBody int64) *capturingGateway {
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
		Sink: sink, Budget: core.NewBudget(budget), MaxBodyBytes: maxBody, Principal: core.LocalPrincipal{},
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
// the request copy empty (T17), so only the 499 case has request events to check; the
// empty copy is a request cut short, so its parse is partial, not failed (Q22).
func TestParse_GatewayMadeResponseStoredWithoutError(t *testing.T) {
	const request = `{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`
	for _, tc := range []struct {
		name    string
		up      func(t *testing.T) config.Upstream
		wait    time.Duration // client timeout; 0 waits for the answer
		status  int
		parse   core.ParseStatus
		request bool // the request copy is whole, so there is a request event
	}{
		{"upstream_unreachable", func(t *testing.T) config.Upstream { return upstreamConfig(refusedURL(t)) }, 0,
			http.StatusBadGateway, core.ParsePartial, false},
		{"client_left", func(t *testing.T) config.Upstream { return upstreamConfig(hangingUpstream(t)) },
			200 * time.Millisecond, 499, core.ParseOK, true},
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
			if row.Parse != string(tc.parse) {
				t.Errorf("parse = %q, want %s", row.Parse, tc.parse)
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

// A request copy sealed before its body's end is parsed as a request cut short:
// partial, not failed, with the events of every message complete before the seal.
// Upstream reads a known prefix of the body, answers and closes, while the client is
// still sending far more than loopback's socket buffers hold, so the exchange ends
// with request_incomplete set and truncated not.
func TestParse_RequestSealedEarlyPartial(t *testing.T) {
	const prefix = `{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"},` +
		`{"role":"user","content":"`
	const size = 24 << 20 // under decodeLimit, so only the seal can cut the copy
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadFull(r.Body, make([]byte, len(prefix))); err != nil {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okResponse)
	}))
	t.Cleanup(up.Close)
	base, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	checkNoLeaksAtEnd(t)
	g := startCapturingGatewayWith(t, upstreamConfig(base), 2*size, 2*size)

	conn, err := net.Dial("tcp", g.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(conn, "POST /anthropic/v1/messages HTTP/1.1\r\nHost: x\r\n"+
		"Content-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", size, prefix); err != nil {
		t.Fatal(err)
	}
	written := make(chan struct{})
	go func() {
		defer close(written)
		chunk := bytes.Repeat([]byte("b"), 64<<10)
		for sent := len(prefix); sent < size; sent += len(chunk) {
			if _, err := conn.Write(chunk[:min(len(chunk), size-sent)]); err != nil {
				return
			}
		}
	}()
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	id := g.queuedID(t)
	_ = conn.Close()
	<-written

	r := g.drain(t)
	row, err := r.Exchange(id)
	if err != nil {
		t.Fatalf("exchange %s: %v", id, err)
	}
	if row.Status != http.StatusOK || !row.RequestIncomplete || row.Truncated {
		t.Fatalf("status %d, request_incomplete %v, truncated %v; want 200, true, false",
			row.Status, row.RequestIncomplete, row.Truncated)
	}
	if row.Parse != string(core.ParsePartial) {
		t.Errorf("parse = %q, want partial", row.Parse)
	}
	events, err := r.Events(id)
	if err != nil {
		t.Fatal(err)
	}
	var requests, history int
	for _, ev := range events {
		switch {
		case ev.Kind == string(core.KindRequest):
			requests++
		case ev.Kind == string(core.KindMessage) && ev.Source == string(core.SourceRequestHistory):
			history++
		}
	}
	if requests != 1 || history != 1 {
		t.Errorf("%d request and %d request_history message events, want 1 and 1 (the message before the seal)",
			requests, history)
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

// okResponse is a small JSON answer for turns whose response the test doesn't read.
const okResponse = `{"id":"msg_01Ok","type":"message","role":"assistant","model":"m",` +
	`"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`

// AC49: a message resent in a later request is stored once. Its message event in
// both exchanges has the same content hash and block hashes, and the store holds one
// copy of each. The resent message has a block over 4096 bytes, so the blob path is
// covered too. Subtest cache_control_added resends the message pretty-printed, keys
// reordered and with a cache_control on its block, as Claude Code moves its cache
// breakpoint forward: the block's top-level cache_control is excluded from the hash.
func TestStore_BlockContentDedup(t *testing.T) {
	big := strings.Repeat("line of notes.txt\\n", 300) // over 4096 bytes once decoded
	first := `{"model":"m","max_tokens":8,"messages":[{"role":"user","content":[` +
		`{"type":"text","text":"Count the lines."},{"type":"text","text":"` + big + `"}]}]}`
	for _, tc := range []struct{ name, resent string }{
		{"as_sent", `{"role":"user","content":[{"type":"text","text":"Count the lines."},` +
			`{"type":"text","text":"` + big + `"}]}`},
		{"cache_control_added", `{
  "content": [
    {"text": "Count the lines.", "type": "text"},
    {"cache_control": {"type": "ephemeral"}, "text": "` + big + `", "type": "text"}
  ],
  "role": "user"
}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkNoLeaksAtEnd(t)
			up := newUpstream(t, jsonResponder(okResponse))
			g := startCapturingGateway(t, upstreamConfig(up.URL))
			second := `{"model":"m","max_tokens":8,"messages":[` + tc.resent + `,` +
				`{"role":"assistant","content":"300 lines."},{"role":"user","content":"Thanks."}]}`
			ids := []string{g.post(t, first, 1), g.post(t, second, 2)}
			r := g.drain(t)

			var msgs []messagePayload
			for _, id := range ids {
				m := storedMessages(t, r, id, core.SourceRequestHistory)
				if len(m) == 0 {
					t.Fatalf("exchange %s stored no request_history message", id)
				}
				msgs = append(msgs, m[0])
			}
			a, b := msgs[0], msgs[1]
			if a.ContentHash == "" || a.ContentHash != b.ContentHash {
				t.Errorf("resent message content_hash %q, first sent %q: want one non-empty hash",
					b.ContentHash, a.ContentHash)
			}
			if len(a.Blocks) != 2 || len(b.Blocks) != 2 {
				t.Fatalf("blocks %+v and %+v, want two each", a.Blocks, b.Blocks)
			}
			for i := range a.Blocks {
				if a.Blocks[i].Hash == "" || a.Blocks[i].Hash != b.Blocks[i].Hash {
					t.Errorf("block %d hash %q then %q, want one non-empty hash", i, a.Blocks[i].Hash, b.Blocks[i].Hash)
				}
			}
			for _, h := range []string{a.ContentHash, a.Blocks[0].Hash, a.Blocks[1].Hash} {
				checkStored(t, r, h)
			}
			got, err := r.Content(a.Blocks[1].Hash)
			if err != nil || !strings.Contains(string(got), "line of notes.txt") ||
				strings.Contains(string(got), "cache_control") {
				t.Errorf("stored large block %.80q… (%v), want the text without cache_control", got, err)
			}
		})
	}
}

// AC50: on the recorded tool turn, turn N's response message (streamed) and its copy
// in turn N+1's request (resent with a cache_control and without the stream's
// "caller") share one content hash and one stored copy, and so does the tool input.
func TestStore_ResponseMessageDedupsWithNextRequest(t *testing.T) {
	checkNoLeaksAtEnd(t)
	g, ids := runToolTurn(t)
	r := g.drain(t)

	resp := storedMessages(t, r, ids[0], core.SourceResponse)
	if len(resp) != 1 {
		t.Fatalf("turn 1 stored %d response messages, want 1", len(resp))
	}
	var resent *messagePayload
	for _, m := range storedMessages(t, r, ids[1], core.SourceRequestHistory) {
		if m.Role == "assistant" {
			resent = &m
			break
		}
	}
	if resent == nil {
		t.Fatal("turn 2 resent no assistant message")
	}
	if resp[0].ContentHash == "" || resp[0].ContentHash != resent.ContentHash {
		t.Errorf("response message content_hash %q, resent copy %q: want one non-empty hash",
			resp[0].ContentHash, resent.ContentHash)
	}
	checkStored(t, r, resp[0].ContentHash)

	inputs := map[string]string{}
	for i, id := range ids[:2] {
		for _, tc := range storedToolCalls(t, r, id) {
			if tc.ID == turnToolID {
				inputs[[]string{"response", "resent"}[i]] = tc.InputHash
			}
		}
	}
	if inputs["response"] == "" || inputs["response"] != inputs["resent"] {
		t.Errorf("tool input_hash %q in the response, %q resent: want one non-empty hash",
			inputs["response"], inputs["resent"])
	}
	checkStored(t, r, inputs["response"])
}

// AC51: two turns with the same system and tools (the recorded tool turn; turn 1's
// copy is compacted, turn 2's pretty-printed as recorded) give one system_hash, one
// tools_hash, and one stored copy of each.
func TestStore_SystemAndToolsStoredOnce(t *testing.T) {
	checkNoLeaksAtEnd(t)
	g, ids := runToolTurn(t)
	r := g.drain(t)

	var reqs []storedRequestPayload
	for _, id := range ids {
		reqs = append(reqs, storedRequest(t, r, id))
	}
	a, b := reqs[0], reqs[1]
	if a.SystemHash == "" || a.SystemHash != b.SystemHash {
		t.Errorf("system_hash %q then %q, want one non-empty hash", a.SystemHash, b.SystemHash)
	}
	if a.ToolsHash == "" || a.ToolsHash != b.ToolsHash {
		t.Errorf("tools_hash %q then %q, want one non-empty hash", a.ToolsHash, b.ToolsHash)
	}
	checkStored(t, r, a.SystemHash)
	checkStored(t, r, a.ToolsHash)
}

// AC52: tool inputs are hashed whole. Two inputs that differ only in a cache_control
// key, nested or at the input's top level, get different input hashes, and both are
// stored with the key kept.
func TestStore_ExclusionNotAppliedInsideToolInput(t *testing.T) {
	for _, tc := range []struct{ name, plain, hinted string }{
		{"nested", `{"file_path":"/w/notes.txt","opts":{"limit":3}}`,
			`{"file_path":"/w/notes.txt","opts":{"limit":3,"cache_control":{"type":"ephemeral"}}}`},
		{"input_top_level", `{"file_path":"/w/notes.txt"}`,
			`{"file_path":"/w/notes.txt","cache_control":{"type":"ephemeral"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkNoLeaksAtEnd(t)
			up := newUpstream(t, jsonResponder(okResponse))
			g := startCapturingGateway(t, upstreamConfig(up.URL))
			body := `{"model":"m","max_tokens":8,"messages":[` +
				`{"role":"user","content":"Read it twice."},` +
				`{"role":"assistant","content":[` +
				`{"type":"tool_use","id":"toolu_plain","name":"Read","input":` + tc.plain + `},` +
				`{"type":"tool_use","id":"toolu_hinted","name":"Read","input":` + tc.hinted + `}]},` +
				`{"role":"user","content":[` +
				`{"type":"tool_result","tool_use_id":"toolu_plain","content":"3"},` +
				`{"type":"tool_result","tool_use_id":"toolu_hinted","content":"3"}]}]}`
			id := g.post(t, body, 1)
			r := g.drain(t)

			hashes := map[string]string{}
			for _, c := range storedToolCalls(t, r, id) {
				hashes[c.ID] = c.InputHash
			}
			plain, hinted := hashes["toolu_plain"], hashes["toolu_hinted"]
			if plain == "" || hinted == "" || plain == hinted {
				t.Fatalf("input_hash %q (plain) and %q (with cache_control), want two different hashes", plain, hinted)
			}
			for h, want := range map[string]string{plain: tc.plain, hinted: tc.hinted} {
				checkStored(t, r, h)
				got, err := r.Content(h)
				if err != nil || !jsonEqual(t, got, []byte(want)) {
					t.Errorf("stored input %s (%v), want %s", got, err, want)
				}
			}
		})
	}
}

// runToolTurn sends the recorded tool turn through a capturing gateway: turn 1 is
// next_request.json cut before the assistant message, answered with the recorded
// stream; turn 2 is next_request.json as recorded. It returns both request IDs.
func runToolTurn(t *testing.T) (*capturingGateway, []string) {
	t.Helper()
	next := readFixture(t, "tool_turn/next_request.json")
	sse := readFixture(t, "tool_turn/response.sse")
	var calls atomic.Int32
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			// response.headers' content type: the stream is parsed as SSE.
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			_, _ = w.Write(sse)
			return
		}
		jsonResponder(okResponse)(w, r)
	})
	g := startCapturingGateway(t, upstreamConfig(up.URL))
	ids := []string{g.post(t, firstTurn(t, next), 1), g.post(t, string(next), 2)}
	return g, ids
}

// firstTurn is the request before next: the same body with its messages cut before
// the first assistant message, re-encoded compact.
func firstTurn(t *testing.T, next []byte) string {
	t.Helper()
	var req map[string]json.RawMessage
	if err := json.Unmarshal(next, &req); err != nil {
		t.Fatal(err)
	}
	var msgs []json.RawMessage
	if err := json.Unmarshal(req["messages"], &msgs); err != nil {
		t.Fatal(err)
	}
	for i, m := range msgs {
		var role struct{ Role string }
		if err := json.Unmarshal(m, &role); err != nil {
			t.Fatal(err)
		}
		if role.Role == "assistant" {
			msgs = msgs[:i]
			break
		}
	}
	cut, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	req["messages"] = cut
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func jsonResponder(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}
}

// post sends body to the messages endpoint, reads the whole answer, and returns the
// request ID of the nth messages exchange the gateway logged, whose capture must have
// been queued. Requests are sent one at a time, so the nth line is this one.
func (g *capturingGateway) post(t *testing.T, body string, n int) string {
	t.Helper()
	res, err := http.Post("http://"+g.addr+"/anthropic/v1/messages", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("request %d: status %d (%v), want 200", n, res.StatusCode, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var lines []map[string]any
		for _, l := range logLines(t, g.logs) {
			if l["msg"] == "request" && l["path"] == "/anthropic/v1/messages" {
				lines = append(lines, l)
			}
		}
		if len(lines) >= n {
			line := lines[n-1]
			if line["capture"] != core.CaptureQueued {
				t.Fatalf("request %d: capture = %v, want %s", n, line["capture"], core.CaptureQueued)
			}
			id, _ := line["request_id"].(string)
			return id
		}
		if time.Now().After(deadline) {
			t.Fatalf("no request line %d in:\n%s", n, g.logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// messagePayload is the part of a stored message payload the dedup tests read.
type messagePayload struct {
	Role        string `json:"role"`
	ContentHash string `json:"content_hash"`
	Blocks      []struct {
		Type string `json:"type"`
		Hash string `json:"hash"`
	} `json:"blocks"`
}

type storedRequestPayload struct {
	SystemHash string `json:"system_hash"`
	ToolsHash  string `json:"tools_hash"`
}

type toolCallPayload struct {
	ID        string `json:"id"`
	InputHash string `json:"input_hash"`
}

// storedPayloads decodes the payload of each stored event of kind (and source, when
// set) of exchange id, in order.
func storedPayloads[P any](t *testing.T, r *store.Reader, id string, kind core.EventKind, source core.Source) []P {
	t.Helper()
	events, err := r.Events(id)
	if err != nil {
		t.Fatalf("events of %s: %v", id, err)
	}
	var out []P
	for _, ev := range events {
		if ev.Kind != string(kind) || (source != "" && ev.Source != string(source)) {
			continue
		}
		var p P
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("payload %s: %v", ev.Payload, err)
		}
		out = append(out, p)
	}
	return out
}

func storedMessages(t *testing.T, r *store.Reader, id string, source core.Source) []messagePayload {
	t.Helper()
	return storedPayloads[messagePayload](t, r, id, core.KindMessage, source)
}

func storedToolCalls(t *testing.T, r *store.Reader, id string) []toolCallPayload {
	t.Helper()
	return storedPayloads[toolCallPayload](t, r, id, core.KindToolCall, "")
}

func storedRequest(t *testing.T, r *store.Reader, id string) storedRequestPayload {
	t.Helper()
	reqs := storedPayloads[storedRequestPayload](t, r, id, core.KindRequest, "")
	if len(reqs) != 1 {
		t.Fatalf("exchange %s stored %d request events, want 1", id, len(reqs))
	}
	return reqs[0]
}

// checkStored checks that content-addressed hash reads back from the store. Equal
// hashes plus this read are what "one stored copy" rests on: the store keeps one
// entry per hash.
func checkStored(t *testing.T, r *store.Reader, hash string) {
	t.Helper()
	if _, err := r.Content(hash); err != nil {
		t.Errorf("content %q: %v", hash, err)
	}
}
