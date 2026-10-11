package timeline_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

func summaryOf(t *testing.T, f *fake) timeline.Summary {
	t.Helper()
	_, s, err := timeline.BuildWithUsage(f, store.SessionRow{PrincipalID: "p", Client: "c", SessionKey: "k"}, timeline.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func usageText(t *testing.T, s timeline.Summary) string {
	t.Helper()
	var b bytes.Buffer
	if err := timeline.RenderUsageText(&b, s); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func usageJSON(t *testing.T, s timeline.Summary) map[string]any {
	t.Helper()
	var b bytes.Buffer
	if err := timeline.RenderUsageJSON(&b, s); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRender_UsageTotalsQualifiers(t *testing.T) {
	f := newFake()
	f.thread("r1", t0, "u:a")
	f.request("r1", "m1")
	f.usage("r1", false, n(10), nil, n(0), n(1))
	f.thread("r2", t0+1, "u:b")
	f.request("r2", "m1") // no usage: missing
	f.thread("r3", t0+2, "u:c")
	f.request("r3", "m1")
	f.usage("r3", true, n(5), n(2), n(0), n(0))
	f.thread("r4", t0+3, "u:d")
	f.request("r4", "m1")
	f.exchanges[3].Status, f.exchanges[3].TTFBNS = 429, n(5)
	out := usageText(t, summaryOf(t, f))
	want := "== usage total: input 15, output 2, cache-write 0, cache-read 1; partial (1 calls); " +
		"incomplete: 1 of 3 model calls missing usage; output incomplete (1 calls did not report); 1 errored\n"
	if !strings.HasPrefix(out, want) {
		t.Fatalf("out = %q\nwant prefix %q", out, want)
	}
	if !strings.Contains(out, "\n   m1: input 15,") {
		t.Fatalf("model line missing: %q", out)
	}
	// no reporter: unknown
	g := newFake()
	g.thread("r1", t0, "u:a")
	g.request("r1", "")
	if out := usageText(t, summaryOf(t, g)); !strings.HasPrefix(out,
		"== usage total: input unknown, output unknown, cache-write unknown, cache-read unknown; incomplete: 1 of 1 model calls missing usage\n") ||
		!strings.Contains(out, "   unknown: ") {
		t.Fatalf("out = %q", out)
	}
}

func allErrored() *fake {
	f := newFake()
	f.thread("r1", t0, "u:a")
	f.request("r1", "m1")
	f.exchanges[0].Status, f.exchanges[0].TTFBNS = 500, n(5)
	return f
}

func TestRender_AllErroredText(t *testing.T) {
	out := usageText(t, summaryOf(t, allErrored()))
	if out != "== usage total: no usage (1 errored)\n   m1: no usage (1 errored)\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestRenderJSON_AllErroredUsageNull(t *testing.T) {
	m := usageJSON(t, summaryOf(t, allErrored()))
	if v, ok := m["usage"]; !ok || v != nil || m["errored"] != float64(1) || m["calls"] != float64(1) || m["type"] != "usage_total" {
		t.Fatalf("m = %v", m)
	}
	mu := m["models"].([]any)[0].(map[string]any)
	if mu["usage"] != nil || mu["errored"] != float64(1) || mu["model"] != "m1" {
		t.Fatalf("model = %v", mu)
	}
}

func noCalls() *fake {
	f := newFake()
	f.exchange("r1", t0, "skipped")
	return f
}

func TestRender_NoModelCalls(t *testing.T) {
	if out := usageText(t, summaryOf(t, noCalls())); out != "== usage total: no model calls\n" {
		t.Fatalf("out = %q", out)
	}
	if out := usageText(t, timeline.Summary{}); out != "== usage total: no model calls\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestRenderJSON_NoModelCallsUsageNull(t *testing.T) {
	m := usageJSON(t, summaryOf(t, noCalls()))
	if v, ok := m["usage"]; !ok || v != nil || m["calls"] != float64(0) || m["errored"] != float64(0) {
		t.Fatalf("m = %v", m)
	}
	if len(m["models"].([]any)) != 0 {
		t.Fatalf("m = %v", m)
	}
}
