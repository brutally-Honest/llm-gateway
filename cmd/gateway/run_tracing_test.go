package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// collector is a fake OTLP/HTTP collector: it keeps every span posted to /v1/traces.
type collector struct {
	*httptest.Server
	mu       sync.Mutex
	posts    int
	spans    []*tracepb.Span
	resource []*commonpb.KeyValue
}

func newCollector(t *testing.T) *collector {
	t.Helper()
	c := &collector{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req coltrace.ExportTraceServiceRequest
		if r.URL.Path != "/v1/traces" || proto.Unmarshal(body, &req) != nil {
			http.Error(w, "bad export", http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		c.posts++
		for _, rs := range req.GetResourceSpans() {
			c.resource = rs.GetResource().GetAttributes()
			for _, ss := range rs.GetScopeSpans() {
				c.spans = append(c.spans, ss.GetSpans()...)
			}
		}
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		out, _ := proto.Marshal(&coltrace.ExportTraceServiceResponse{})
		_, _ = w.Write(out)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *collector) snapshot() (int, []*tracepb.Span, []*commonpb.KeyValue) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.posts, slices.Clone(c.spans), c.resource
}

// attrs is a span's attributes as strings.
func attrs(kvs []*commonpb.KeyValue) map[string]string {
	out := map[string]string{}
	for _, kv := range kvs {
		v := kv.GetValue()
		switch {
		case v.GetStringValue() != "":
			out[kv.GetKey()] = v.GetStringValue()
		default:
			out[kv.GetKey()] = strings.TrimPrefix(fmt.Sprint(v.GetValue()), "&")
		}
	}
	return out
}

// The probes: a credential header, a custom header, the prompt and the response. None
// may reach a span.
const (
	probeKey      = "sk-ant-api03-PROBEKEYVALUE"
	probeHeader   = "probe-header-value-77"
	probePrompt   = "PROMPT-PROBE-TEXT-say-hello"
	probeResponse = "RESPONSE-PROBE-TEXT-hello"
	probeSystem   = "SYSTEM-PROBE-TEXT"
)

// tracedCall starts a capturing gateway with tracing pointed at col (on or off), makes one
// model call carrying the probes, stops the gateway and returns the call's request id.
func tracedCall(t *testing.T, col *collector, on bool) string {
	t.Helper()
	const path = "/anthropic/v1/messages"
	up := newFakeUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5",`+
			`"content":[{"type":"text","text":"`+probeResponse+`"}],"stop_reason":"end_turn",`+
			`"usage":{"input_tokens":3,"output_tokens":2}}`)
	})
	g, url := startCapturing(t, captureEnv(t.TempDir(), up, map[string]string{
		"GATEWAY_TRACING_ENABLED":       fmt.Sprint(on),
		"GATEWAY_TRACING_OTLP_ENDPOINT": col.URL,
	}), nil)
	body := `{"model":"claude-opus-5-5","max_tokens":5,"system":"` + probeSystem + `",` +
		`"messages":[{"role":"user","content":"` + probePrompt + `"}]}`
	req, _ := http.NewRequest(http.MethodPost, url+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", probeKey)
	req.Header.Set("X-Probe", probeHeader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	// The capture is stored before the stop's drain ends, and the spans flush at exit.
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	ids := g.requestIDs(path)
	if len(ids) != 1 {
		t.Fatalf("request ids = %v", ids)
	}
	return ids[0]
}

// Spec 021 AC11: one trace per proxied call, with its five span kinds in their places and
// the exchange id, sizes, status and model as attributes.
func TestRun_TraceOfAProxiedCall(t *testing.T) {
	col := newCollector(t)
	id := tracedCall(t, col, true)
	_, spans, res := col.snapshot()

	byName := map[string][]*tracepb.Span{}
	for _, s := range spans {
		byName[s.GetName()] = append(byName[s.GetName()], s)
	}
	for name, n := range map[string]int{"gateway.receive": 1, "gateway.upstream_request": 1,
		"gateway.response_stream": 1, "gateway.capture": 1, "gateway.store_write": 2} {
		if len(byName[name]) != n {
			t.Fatalf("%s spans = %d, want %d (got %d spans in all)", name, len(byName[name]), n, len(spans))
		}
	}
	root := byName["gateway.receive"][0]
	capture := byName["gateway.capture"][0]
	if len(root.GetParentSpanId()) != 0 {
		t.Error("gateway.receive has a parent; want it to be the root")
	}
	for _, s := range spans {
		if !slices.Equal(s.GetTraceId(), root.GetTraceId()) {
			t.Errorf("%s is in trace %s, want %s", s.GetName(), hex.EncodeToString(s.GetTraceId()), hex.EncodeToString(root.GetTraceId()))
		}
		want := root.GetSpanId()
		switch s.GetName() {
		case "gateway.receive":
			continue
		case "gateway.store_write":
			want = capture.GetSpanId()
		}
		if !slices.Equal(s.GetParentSpanId(), want) {
			t.Errorf("%s has the wrong parent", s.GetName())
		}
	}

	ra := attrs(root.GetAttributes())
	for k, v := range map[string]string{"gateway.exchange_id": id, "gateway.protocol": "anthropic",
		"http.request.method": "POST", "http.response.status_code": "200", "gateway.capture": "queued"} {
		if !strings.Contains(ra[k], v) {
			t.Errorf("receive %s = %q, want %q", k, ra[k], v)
		}
	}
	for _, k := range []string{"http.request.body.size", "http.response.body.size", "gateway.ttfb_ms"} {
		if _, ok := ra[k]; !ok {
			t.Errorf("receive has no %s", k)
		}
	}
	ca := attrs(capture.GetAttributes())
	if ca["gen_ai.request.model"] != "claude-opus-5-5" || ca["gateway.parse_status"] != "ok" || ca["gateway.capture"] != "stored" {
		t.Errorf("capture attributes = %v, want the model, parse ok and stored", ca)
	}
	ops := []string{attrs(byName["gateway.store_write"][0].GetAttributes())["gateway.store.op"],
		attrs(byName["gateway.store_write"][1].GetAttributes())["gateway.store.op"]}
	slices.Sort(ops)
	if !slices.Equal(ops, []string{"exchange", "parse"}) {
		t.Errorf("store_write ops = %v", ops)
	}
	if attrs(res)["service.name"] != "llm-gateway" {
		t.Errorf("resource = %v, want service.name llm-gateway", attrs(res))
	}
}

// Spec 021 AC11: no span attribute carries content or a header, by key or by value.
func TestRun_TraceAttributesHoldNoContentOrHeaders(t *testing.T) {
	col := newCollector(t)
	tracedCall(t, col, true)
	_, spans, res := col.snapshot()
	if len(spans) == 0 {
		t.Fatal("no spans exported")
	}
	all := append([]*commonpb.KeyValue{}, res...)
	for _, s := range spans {
		all = append(all, s.GetAttributes()...)
		for _, e := range s.GetEvents() {
			all = append(all, e.GetAttributes()...)
		}
		if s.GetStatus().GetMessage() != "" {
			all = append(all, &commonpb.KeyValue{Key: "status", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s.GetStatus().GetMessage()}}})
		}
	}
	for _, kv := range all {
		key := strings.ToLower(kv.GetKey())
		for _, banned := range []string{"header", "body.content", "prompt", "content", "message", "authorization", "api_key", "user_agent", "user-agent"} {
			if strings.Contains(key, banned) {
				t.Errorf("attribute key %q looks like content or a header", kv.GetKey())
			}
		}
		v := fmt.Sprint(kv.GetValue())
		for _, probe := range []string{probeKey, probeHeader, probePrompt, probeResponse, probeSystem, "PROBEKEY", "claude-cli"} {
			if strings.Contains(v, probe) {
				t.Errorf("attribute %s = %q carries %q", kv.GetKey(), v, probe)
			}
		}
	}
}

// Spec 021 AC12: with tracing off no span is made, so nothing is exported, even with an
// endpoint set.
func TestRun_NoSpanWhenTracingOff(t *testing.T) {
	col := newCollector(t)
	tracedCall(t, col, false)
	if posts, spans, _ := col.snapshot(); posts != 0 || len(spans) != 0 {
		t.Errorf("tracing off: %d exports, %d spans; want none", posts, len(spans))
	}
}
