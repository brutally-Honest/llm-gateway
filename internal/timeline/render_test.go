package timeline_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

type listFake struct {
	*fake
	rows []store.SessionRow
}

func (l listFake) SessionRows(int) ([]store.SessionRow, error) { return l.rows, nil }

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC).UnixNano()

func TestListSessions_RowsWithPreview(t *testing.T) {
	f := newFake()
	f.thread("r1", t0, "a:hello", "u:"+strings.Repeat("é", 100))
	l := listFake{f, []store.SessionRow{{PrincipalID: "p", Client: "c", SessionKey: "k", First: t0, Last: t0, Count: 1}}}
	got, err := timeline.ListSessions(l, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if want := strings.Repeat("é", 80); got[0].Preview != want {
		t.Fatalf("preview = %q", got[0].Preview)
	}
	if got[0].Count != 1 || got[0].SessionKey != "k" {
		t.Fatalf("row = %+v", got[0])
	}
}

func TestListSessions_EmptyPreviewForSideCallOnly(t *testing.T) {
	f := newFake()
	f.exchange("r1", t0, "skipped")
	l := listFake{f, []store.SessionRow{{PrincipalID: "p", Client: "c", SessionKey: "k", First: t0, Last: t0, Count: 1}}}
	got, err := timeline.ListSessions(l, 0)
	if err != nil || len(got) != 1 || got[0].Preview != "" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func rows() []timeline.SessionListing {
	return []timeline.SessionListing{{SessionRow: store.SessionRow{PrincipalID: "p", Client: "c",
		SessionKey: "k", First: t0, Last: t0, Count: 2}, Preview: "hi"}}
}

func TestRenderText_LocalZoneRFC3339(t *testing.T) {
	loc := time.FixedZone("X", 5*3600+1800)
	var b bytes.Buffer
	if err := timeline.RenderSessionsText(&b, rows(), loc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "2026-10-11T17:30:00+05:30") {
		t.Fatalf("out = %q", b.String())
	}
	f := newFake()
	f.thread("r1", t0, "u:hi")
	b.Reset()
	if err := timeline.RenderText(&b, build(t, f, timeline.Options{}), loc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "2026-10-11T17:30:00+05:30") {
		t.Fatalf("out = %q", b.String())
	}
}

func TestRenderJSON_UTC(t *testing.T) {
	var b bytes.Buffer
	if err := timeline.RenderSessionsJSON(&b, rows(), time.UTC); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	f.thread("r1", t0, "u:hi")
	if err := timeline.RenderJSON(&b, build(t, f, timeline.Options{}), time.UTC); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err)
		}
		if m["type"] == nil {
			t.Fatalf("no type: %s", l)
		}
	}
	if !strings.Contains(lines[0], `"first":"2026-10-11T12:00:00Z"`) || !strings.Contains(lines[1], `"time":"2026-10-11T12:00:00Z"`) {
		t.Fatalf("lines = %q", lines)
	}
}

func TestRenderText_EscapesTerminalControls(t *testing.T) {
	f := newFake()
	f.thread("r1", t0, "u:ok\n\tline\x1b[31mred\u009b\x07")
	var b bytes.Buffer
	entries := build(t, f, timeline.Options{})
	if err := timeline.RenderText(&b, entries, time.UTC); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.ContainsAny(out, "\x1b\x07\u009b") {
		t.Fatalf("raw control in %q", out)
	}
	if !strings.Contains(out, "ok\n\tline") || !strings.Contains(out, `\x1b[31mred\u009b\x07`) {
		t.Fatalf("out = %q", out)
	}
	r := rows()
	r[0].SessionKey, r[0].Preview = "k\x1b]0;x\x07", "a\nb"
	b.Reset()
	if err := timeline.RenderSessionsText(&b, r, time.UTC); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(b.String(), "\x1b\x07") || strings.Count(b.String(), "\n") != 1 {
		t.Fatalf("out = %q", b.String())
	}
}

func TestRenderText_FoldedAndMarkerLines(t *testing.T) {
	f := newFake()
	f.thread("r1", t0, "u:a", "a:b")
	f.thread("r2", t0+1, "u:a", "a:c")
	f.exchange("r3", t0+2, "failed")
	f.exchange("r4", t0+3, "ok")
	var b bytes.Buffer
	if err := timeline.RenderText(&b, build(t, f, timeline.Options{}), time.UTC); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"~ history diverged", "-- ", " r3", " r4", "parse=failed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	if strings.Count(out, "-- ") != 2 {
		t.Fatalf("out = %q", out)
	}
}

func TestRender_RealSessionFixtureWithoutSystemContent(t *testing.T) {
	r := openFixture(t)
	listing, err := timeline.ListSessions(r, 0)
	if err != nil || len(listing) != 1 {
		t.Fatalf("listing = %v, %v", listing, err)
	}
	entries, err := timeline.Build(r, listing[0].SessionRow, timeline.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var text, js bytes.Buffer
	for _, f := range []func() error{
		func() error { return timeline.RenderSessionsText(&text, listing, time.Local) },
		func() error { return timeline.RenderText(&text, entries, time.Local) },
		func() error { return timeline.RenderSessionsJSON(&js, listing, time.UTC) },
		func() error { return timeline.RenderJSON(&js, entries, time.UTC) },
	} {
		if err := f(); err != nil {
			t.Fatal(err)
		}
	}
	if text.Len() == 0 || js.Len() == 0 {
		t.Fatal("empty output")
	}
	for _, l := range strings.Split(strings.TrimSpace(js.String()), "\n") {
		if !json.Valid([]byte(l)) {
			t.Fatalf("invalid json line %q", l)
		}
	}
	if strings.Contains(text.String()+js.String(), "system_hash") {
		t.Fatal("system content leaked into output")
	}
}

func renderText(t *testing.T, f *fake) string {
	t.Helper()
	var b bytes.Buffer
	if err := timeline.RenderText(&b, build(t, f, timeline.Options{}), time.UTC); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func entryJSONs(t *testing.T, f *fake) []map[string]any {
	t.Helper()
	var b bytes.Buffer
	if err := timeline.RenderJSON(&b, build(t, f, timeline.Options{}), time.UTC); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

func lineWith(out, id string) string {
	for _, l := range strings.Split(out, "\n") {
		if (strings.HasPrefix(l, "== ") || strings.HasPrefix(l, "-- ")) && strings.Contains(l, " "+id) {
			return l
		}
	}
	return ""
}

func TestRenderText_CallCountersOnExchangeAndFolded(t *testing.T) {
	f := newFake()
	f.thread("r1", t0, "u:hi")
	f.request("r1", "m1")
	f.usage("r1", false, n(10), n(20), n(0), n(5))
	f.exchange("r2", t0+1, "skipped")
	f.request("r2", "m1")
	f.usage("r2", false, n(1), n(2), n(3), n(4))
	out := renderText(t, f)
	for id, want := range map[string]string{
		"r1": " tokens in=10 out=20 cache_write=0 cache_read=5",
		"r2": " tokens in=1 out=2 cache_write=3 cache_read=4",
	} {
		if l := lineWith(out, id); !strings.HasSuffix(l, want) {
			t.Fatalf("%s line = %q, want suffix %q", id, l, want)
		}
	}
}

func TestRenderJSON_NilCounterIsNull(t *testing.T) {
	f := newFake()
	f.thread("r1", t0, "u:hi")
	f.request("r1", "m1")
	f.usage("r1", false, n(10), nil, n(0), nil)
	out := renderText(t, f)
	if l := lineWith(out, "r1"); !strings.HasSuffix(l, " tokens in=10 out=unknown cache_write=0 cache_read=unknown") {
		t.Fatalf("line = %q", l)
	}
	u, ok := entryJSONs(t, f)[0]["usage"].(map[string]any)
	if !ok {
		t.Fatalf("usage = %v", entryJSONs(t, f)[0])
	}
	if v, present := u["output_tokens"]; !present || v != nil {
		t.Fatalf("output_tokens = %v present=%v, want null", v, present)
	}
	if u["cache_write_tokens"] != float64(0) || u["input_tokens"] != float64(10) || u["partial"] != false {
		t.Fatalf("usage = %v", u)
	}
}

func TestRenderText_NoUsageErroredAndMissing(t *testing.T) {
	f := newFake()
	f.thread("r1", t0, "u:hi")
	f.request("r1", "m1")
	f.exchanges[0].Status, f.exchanges[0].TTFBNS = 429, n(5)
	f.exchange("r2", t0+1, "ok")
	f.request("r2", "m1")
	out := renderText(t, f)
	if l := lineWith(out, "r1"); !strings.HasSuffix(l, " no usage (errored)") {
		t.Fatalf("r1 = %q", l)
	}
	if l := lineWith(out, "r2"); !strings.HasSuffix(l, " no usage (missing)") {
		t.Fatalf("r2 = %q", l)
	}
}

func TestRenderJSON_NoUsageKey(t *testing.T) {
	f := newFake()
	f.thread("r1", t0, "u:hi")
	f.request("r1", "m1")
	f.exchanges[0].Status, f.exchanges[0].TTFBNS = 500, n(5)
	f.exchange("r2", t0+1, "ok")
	f.request("r2", "m1")
	ls := entryJSONs(t, f)
	for _, l := range ls {
		want := map[string]string{"r1": "errored", "r2": "missing"}[l["request_id"].(string)]
		if l["type"] == "message" {
			continue
		}
		v, present := l["usage"]
		if !present || v != nil || l["no_usage"] != want {
			t.Fatalf("line = %v, want usage null no_usage %s", l, want)
		}
	}
}

func TestRender_PartialUsageMarked(t *testing.T) {
	f := newFake()
	f.thread("r1", t0, "u:hi")
	f.request("r1", "m1")
	f.usage("r1", true, n(3), nil, nil, nil)
	if l := lineWith(renderText(t, f), "r1"); !strings.HasSuffix(l, " tokens in=3 out=unknown cache_write=unknown cache_read=unknown [usage partial]") {
		t.Fatalf("line = %q", l)
	}
	if u := entryJSONs(t, f)[0]["usage"].(map[string]any); u["partial"] != true {
		t.Fatalf("usage = %v", u)
	}
}

func TestRender_NonModelExchangeUnchanged(t *testing.T) {
	f := newFake()
	f.exchange("r1", t0, "skipped")
	f.exchanges[0].Method, f.exchanges[0].Path = "HEAD", "/api/hello"
	out := renderText(t, f)
	if strings.Contains(out, "tokens") || strings.Contains(out, "no usage") {
		t.Fatalf("out = %q", out)
	}
	l := entryJSONs(t, f)[0]
	if _, ok := l["usage"]; ok {
		t.Fatalf("line = %v", l)
	}
	if _, ok := l["no_usage"]; ok {
		t.Fatalf("line = %v", l)
	}
}
