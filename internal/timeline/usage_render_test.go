package timeline_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
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

func listingOf(t *testing.T, f *fake) []timeline.SessionListing {
	t.Helper()
	l := listFake{f, []store.SessionRow{{PrincipalID: "p", Client: "c", SessionKey: "k", First: t0, Last: t0, Count: 1}}}
	got, err := timeline.ListSessions(l, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	return got
}

func twoModels() *fake {
	f := newFake()
	f.thread("r1", t0, "u:a")
	f.request("r1", "m1")
	f.usage("r1", false, n(10), n(20), n(0), n(0))
	f.thread("r2", t0+1, "u:b")
	f.request("r2", "m2")
	f.usage("r2", false, n(5), n(1), n(0), n(0))
	return f
}

func TestRenderSessionsJSON_UsageAndModels(t *testing.T) {
	var b bytes.Buffer
	if err := timeline.RenderSessionsJSON(&b, listingOf(t, twoModels()), time.UTC); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	u := m["usage"].(map[string]any)
	if m["type"] != "session" || m["count"] != float64(1) || u["input_tokens"] != float64(15) || len(m["models"].([]any)) != 2 {
		t.Fatalf("m = %v", m)
	}
	b.Reset()
	if err := timeline.RenderSessionsJSON(&b, listingOf(t, allErrored()), time.UTC); err != nil {
		t.Fatal(err)
	}
	m = map[string]any{}
	_ = json.Unmarshal(b.Bytes(), &m)
	if v, ok := m["usage"]; !ok || v != nil || m["errored"] != float64(1) || m["calls"] != float64(1) {
		t.Fatalf("m = %v", m)
	}
}

func TestRenderSessions_NoModelCallsSession(t *testing.T) {
	var b bytes.Buffer
	if err := timeline.RenderSessionsText(&b, listingOf(t, noCalls()), time.UTC); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(b.String(), "\tno model calls\n") {
		t.Fatalf("out = %q", b.String())
	}
	b.Reset()
	_ = timeline.RenderSessionsJSON(&b, listingOf(t, noCalls()), time.UTC)
	var m map[string]any
	_ = json.Unmarshal(b.Bytes(), &m)
	if v, ok := m["usage"]; !ok || v != nil || m["calls"] != float64(0) {
		t.Fatalf("m = %v", m)
	}
}

func TestRenderSessions_ExistingColumnsKeepPosition(t *testing.T) {
	var b bytes.Buffer
	if err := timeline.RenderSessionsText(&b, listingOf(t, twoModels()), time.UTC); err != nil {
		t.Fatal(err)
	}
	ls := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	cols := strings.Split(ls[0], "\t")
	if len(cols) != 8 || cols[0] != "k" || cols[1] != "c" || cols[2] != "p" || cols[5] != "1" || cols[6] != "a" ||
		cols[7] != "input 15, output 21, cache-write 0, cache-read 0" {
		t.Fatalf("cols = %q", cols)
	}
	if len(ls) != 3 || ls[1] != "   m1: input 10, output 20, cache-write 0, cache-read 0" ||
		ls[2] != "   m2: input 5, output 1, cache-write 0, cache-read 0" {
		t.Fatalf("lines = %q", ls)
	}
}
