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
