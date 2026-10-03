package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// eventAdapter is the test adapter with a parser that really produces canonical
// events, for a wire format that exists only in this file: the request body is
// {"text": ...} and the response body {"reply": ..., "tokens": ...}.
type eventAdapter struct {
	testAdapter
}

func (eventAdapter) SecretHeaders() []string     { return []string{"X-Test-Key"} }
func (eventAdapter) SecretQueryParams() []string { return []string{"key"} }
func (eventAdapter) Parser() core.Parser         { return eventParser{} }

type eventParser struct{}

func (eventParser) HashExcludedFields() []string { return []string{excludedKey} }

func (eventParser) Parse(in core.ParseInput) core.ParseResult {
	var req struct {
		Text string `json:"text"`
	}
	var res struct {
		Reply  string `json:"reply"`
		Tokens int64  `json:"tokens"`
	}
	if json.Unmarshal(in.RequestBody, &req) != nil || json.Unmarshal(in.ResponseBody, &res) != nil {
		return core.ParseResult{Status: core.ParseFailed}
	}
	block := func(text string) []core.Block {
		// The excluded key is wire-only: the pipeline must leave it out of the hash.
		raw, _ := json.Marshal(map[string]string{"text": text, excludedKey: "wire-only"})
		return []core.Block{{Type: core.BlockText, Content: raw}}
	}
	return core.ParseResult{Status: core.ParseOK, Events: []core.Event{
		{Kind: core.KindRequest, Request: &core.RequestEvent{Model: "test-model"}},
		{Kind: core.KindMessage, Message: &core.MessageEvent{
			Index: 0, Role: "user", Source: core.SourceRequestHistory, Blocks: block(req.Text),
		}},
		{Kind: core.KindMessage, Message: &core.MessageEvent{
			Index: -1, Role: "assistant", Source: core.SourceResponse, StopReason: "done", Blocks: block(res.Reply),
		}},
		{Kind: core.KindUsage, Usage: &core.UsageEvent{OutputTokens: &res.Tokens}},
	}}
}

// pipelineRig is a gateway for adapter a that captures through the real sink into a
// real store in dir.
type pipelineRig struct {
	g    *gateway
	dir  string
	st   *store.Store
	sink *capture.Sink
}

func startPipeline(t *testing.T, a core.Adapter, base *upstream) *pipelineRig {
	t.Helper()
	dir := t.TempDir()
	log := logging.New(io.Discard, "debug")
	st, err := store.Open(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sink := capture.NewSink(capture.Config{QueueSize: 8, Workers: 1, DecodeLimit: 1 << 20}, st, log)
	t.Cleanup(func() { sink.Close(context.Background()) })
	g := startCaptureGateway(t, a, base.URL, core.Capture{Sink: sink, Budget: core.NewBudget(1 << 20), MaxBodyBytes: 1 << 20})
	return &pipelineRig{g: g, dir: dir, st: st, sink: sink}
}

// send sends req through the gateway and returns the exchange's request ID, once it
// has been submitted: the access line is written after that.
func (p *pipelineRig) send(t *testing.T, req *http.Request) string {
	t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	line := p.g.accessLine(t, req.URL.Path)
	if line["capture"] != core.CaptureQueued {
		t.Fatalf("capture = %v, want %s", line["capture"], core.CaptureQueued)
	}
	id, _ := line["request_id"].(string)
	return id
}

// finish drains the sink, closes the store and opens a reader over it.
func (p *pipelineRig) finish(t *testing.T) *store.Reader {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if n := p.sink.Close(ctx); n != 0 {
		t.Fatalf("%d exchanges left undrained", n)
	}
	if err := p.st.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := store.OpenReader(p.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// AC59: an adapter and parser core has never heard of, registered from the test,
// are captured, redacted and turned into canonical events in a real store.
func TestCore_TestParserNeedsNoCoreChange(t *testing.T) {
	checkNoLeaksAtEnd(t)
	up := newUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"reply":"hello back","tokens":3}`)
	})
	p := startPipeline(t, eventAdapter{}, up)

	req, err := http.NewRequest(http.MethodPost, p.g.url+"/t/v1/chat", strings.NewReader(`{"text":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-Key", "sekrit-pipeline")
	id := p.send(t, req)
	r := p.finish(t)

	row, err := r.Exchange(id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Parse != string(core.ParseOK) || row.Protocol != "test" || row.PrincipalID != core.PrincipalLocal {
		t.Errorf("parse %q, protocol %q, principal %q; want ok, test, local", row.Parse, row.Protocol, row.PrincipalID)
	}
	if got := row.RequestHeaders.Values("X-Test-Key"); !slices.Equal(got, []string{core.Redacted}) {
		t.Errorf("stored X-Test-Key = %q, want [REDACTED]", got)
	}

	evs, err := r.Events(id)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, ev := range evs {
		kinds = append(kinds, ev.Kind)
		if ev.PrincipalID != core.PrincipalLocal || ev.SchemaVersion != core.SchemaVersion {
			t.Errorf("event %d: principal %q, schema %d", ev.Seq, ev.PrincipalID, ev.SchemaVersion)
		}
	}
	want := []string{"request", "message", "message", "usage"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("event kinds %v, want %v", kinds, want)
	}
	for _, ev := range evs[1:3] {
		var payload struct {
			Blocks []struct {
				Hash string `json:"hash"`
			} `json:"blocks"`
		}
		if err := json.Unmarshal(ev.Payload, &payload); err != nil || len(payload.Blocks) != 1 {
			t.Fatalf("message %d payload %s: %v", ev.Seq, ev.Payload, err)
		}
		if _, err := r.Content(ev.ContentHash); err != nil {
			t.Errorf("message %d content %s: %v", ev.Seq, ev.ContentHash, err)
		}
		block, err := r.Content(payload.Blocks[0].Hash)
		if err != nil {
			t.Fatalf("message %d block: %v", ev.Seq, err)
		}
		if bytes.Contains(block, []byte(excludedKey)) {
			t.Errorf("message %d block %s keeps the parser's hash-excluded key", ev.Seq, block)
		}
	}
	if got := string(evs[3].Payload); !strings.Contains(got, `"output_tokens":3`) {
		t.Errorf("usage payload %s, want output_tokens 3", got)
	}
}

// redactAdapterQueryParamsInStore is TestRedact_AdapterQueryParams/in_store: the
// adapter's secret query parameter and header go upstream unchanged, and reach
// neither the database nor any blob.
func redactAdapterQueryParamsInStore(t *testing.T) {
	checkNoLeaksAtEnd(t)
	const secretQuery, secretHeader = "sekrit-query-in-store", "sekrit-header-in-store"
	up := newUpstream(t, nil)
	p := startPipeline(t, parsingAdapter{}, up)

	target := p.g.url + "/t/v1/x?a=1&key=" + secretQuery + "&b=2"
	// Over the inline limit, so the body is a blob the scan has to open.
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(strings.Repeat("b", 8<<10)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-Key", secretHeader)
	id := p.send(t, req)
	seenUp := up.only(t)
	if seenUp.RequestURI != "/v1/x?a=1&key="+secretQuery+"&b=2" || seenUp.Header.Get("X-Test-Key") != secretHeader {
		t.Errorf("upstream got %q with X-Test-Key %q, want both unchanged", seenUp.RequestURI, seenUp.Header.Get("X-Test-Key"))
	}
	r := p.finish(t)

	row, err := r.Exchange(id)
	if err != nil {
		t.Fatal(err)
	}
	if row.Query != "a=1&key=[REDACTED]&b=2" {
		t.Errorf("stored query %q, want key redacted and the rest as sent", row.Query)
	}
	if got := row.RequestHeaders.Values("X-Test-Key"); !slices.Equal(got, []string{core.Redacted}) {
		t.Errorf("stored X-Test-Key = %q, want [REDACTED]", got)
	}

	blobs := 0
	err = filepath.WalkDir(p.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if filepath.Base(filepath.Dir(filepath.Dir(path))) == "blobs" {
			blobs++
			hash := filepath.Base(filepath.Dir(path)) + filepath.Base(path)
			if data, err = r.Content(hash); err != nil {
				return err
			}
		}
		for _, secret := range []string{secretQuery, secretHeader} {
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("%s holds %q", path, secret)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if blobs == 0 {
		t.Error("no blob was scanned; the request body should have needed one")
	}
}
