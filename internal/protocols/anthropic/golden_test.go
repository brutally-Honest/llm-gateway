package anthropic_test

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
)

// goldenHeader is one header line of testdata/stream.headers.
type goldenHeader struct{ name, value string }

// readGoldenHeaders parses testdata/stream.headers: a status line, then one
// `Name: value` per line.
func readGoldenHeaders(t *testing.T) (int, []goldenHeader) {
	t.Helper()
	raw, err := os.ReadFile("testdata/stream.headers")
	if err != nil {
		t.Fatalf("golden fixture: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	parts := strings.SplitN(lines[0], " ", 3)
	if len(parts) < 2 {
		t.Fatalf("golden fixture: bad status line %q", lines[0])
	}
	status, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("golden fixture: bad status line %q: %v", lines[0], err)
	}
	var headers []goldenHeader
	for _, line := range lines[1:] {
		name, value, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("golden fixture: bad header line %q", line)
		}
		headers = append(headers, goldenHeader{name: name, value: value})
	}
	return status, headers
}

// sseEvents splits body after each blank-line event boundary, so the pieces joined
// are body again.
func sseEvents(body []byte) [][]byte {
	var events [][]byte
	for len(body) > 0 {
		i := bytes.Index(body, []byte("\n\n"))
		if i < 0 {
			events = append(events, body)
			break
		}
		events = append(events, body[:i+2])
		body = body[i+2:]
	}
	return events
}

func TestProxy_GoldenAnthropicStreamReplay(t *testing.T) {
	want, err := os.ReadFile("testdata/stream.sse")
	if err != nil {
		t.Fatalf("golden fixture: %v", err)
	}
	status, headers := readGoldenHeaders(t)
	events := sseEvents(want)
	if len(events) < 2 || !bytes.Equal(bytes.Join(events, nil), want) {
		t.Fatalf("golden fixture: split into %d events that do not rebuild the file", len(events))
	}

	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		for _, h := range headers {
			w.Header()[h.name] = append(w.Header()[h.name], h.value)
		}
		w.WriteHeader(status)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream: response writer cannot flush")
			return
		}
		for _, ev := range events {
			if _, err := w.Write(ev); err != nil {
				t.Errorf("upstream: writing event: %v", err)
				return
			}
			flusher.Flush()
		}
	})
	gw := startGateway(t, upstreamConfig(up.URL))

	reqBody := `{"model":"claude-sonnet-5","max_tokens":64,"stream":true,` +
		`"messages":[{"role":"user","content":"Say hello in five words."}]}`
	req, err := http.NewRequest(http.MethodPost, "http://"+gw.addr+"/anthropic/v1/messages",
		strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Accept", "text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading the replayed stream: %v", err)
	}

	if res.StatusCode != status {
		t.Errorf("status = %d, want %d", res.StatusCode, status)
	}
	for _, h := range headers {
		if gotValue := res.Header.Get(h.name); gotValue != h.value {
			t.Errorf("header %s = %q, want %q", h.name, gotValue, h.value)
		}
	}
	if !bytes.Equal(got, want) {
		t.Errorf("client bytes differ from testdata/stream.sse:\n got %d bytes: %q\nwant %d bytes: %q",
			len(got), got, len(want), want)
	}
	if n := len(up.only(t).Body); n != len(reqBody) {
		t.Errorf("upstream got a %d-byte body, want %d", n, len(reqBody))
	}
}
