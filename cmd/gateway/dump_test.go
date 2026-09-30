package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

const (
	dumpPath     = "/anthropic/v1/messages"
	fixturesPath = "../../internal/protocols/anthropic/testdata"
)

// fixture reads one of the Anthropic adapter's recorded fixtures.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixturesPath, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// upstreamAnswer is what the fake upstream sends back for every request.
type upstreamAnswer struct {
	contentType, encoding string
	body                  []byte
}

func (a upstreamAnswer) handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", a.contentType)
	if a.encoding != "" {
		w.Header().Set("Content-Encoding", a.encoding)
	}
	_, _ = w.Write(a.body)
}

// sendMessage posts reqBody through the gateway at url and reads the whole answer.
// Accept-Encoding is set by hand, so the client's transport hands the body back as
// sent.
func sendMessage(t *testing.T, url string, reqBody []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+dumpPath, bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, br, zstd")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
}

// captureOne sends one request through a gateway capturing into dir, stops it, and
// returns the stored exchange with a reader over the store.
func captureOne(t *testing.T, dir string, answer upstreamAnswer, reqBody []byte) (store.ExchangeRow, *store.Reader) {
	t.Helper()
	up := newBodyUpstream(t, answer.handler)
	g, url := servedAgainst(t, dir, up.URL, nil)
	sendMessage(t, url, reqBody)
	return storedExchange(t, g, dir, dumpPath)
}

// dump runs `gateway dump args...` against the store in dir and returns the exit
// code and everything written to stdout.
func dump(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	var out syncBuffer
	env := map[string]string{"GATEWAY_CAPTURE_DIR": dir}
	code := run(t.Context(), deps{
		args: append([]string{"dump"}, args...),
		lookupEnv: func(name string) (string, bool) {
			v, ok := env[name]
			return v, ok
		},
		stdout: &out,
		listen: nil, // dump never binds; a call would panic
	})
	return code, out.String()
}

// printed is how dump prints a body: as is, plus a newline if it has none at the end.
func printed(b []byte) string {
	if len(b) > 0 && b[len(b)-1] != '\n' {
		return string(b) + "\n"
	}
	return string(b)
}

// wantDump is dump's whole expected output for ex: the row as one JSON line, both
// bodies under their header lines, then the events as JSON lines.
func wantDump(t *testing.T, r *store.Reader, ex store.ExchangeRow, reqNote string, req []byte,
	resNote string, res []byte) string {
	t.Helper()
	row, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.Write(row)
	b.WriteString("\n== request body" + reqNote + "\n" + printed(req))
	b.WriteString("== response body" + resNote + "\n" + printed(res))
	b.WriteString("== events\n")
	events, err := r.Events(ex.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// eventLines returns the JSON lines after "== events", parsed.
func eventLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	_, after, ok := strings.Cut(out, "\n== events\n")
	if !ok {
		t.Fatalf("no events section in\n%s", out)
	}
	var events []map[string]any
	for _, l := range strings.Split(strings.TrimSuffix(after, "\n"), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("event line is not JSON: %q", l)
		}
		events = append(events, m)
	}
	return events
}

func gzipped(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func brotlied(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zstded(t *testing.T, b []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = enc.Close() }()
	return enc.EncodeAll(b, nil)
}

// AC54: dump prints the row's fields, both bodies and the canonical events as JSON
// lines, in that order. Encoded bodies are decoded unless -raw; an unsupported
// encoding prints the raw bytes with a notice and still exits zero.
func TestDump_PrintsExchange(t *testing.T) {
	req := fixture(t, "non_streaming/request.json")
	jsonRes := fixture(t, "non_streaming/response.json")
	sse := fixture(t, "stream.sse")
	cases := []struct {
		name    string
		answer  upstreamAnswer
		args    []string
		resNote string
		res     []byte // what dump prints for the response body
		parsed  bool   // the exchange parses into response events with content hashes
	}{
		{name: "streamed", answer: upstreamAnswer{"text/event-stream; charset=utf-8", "", sse}, res: sse, parsed: true},
		{name: "non_streamed", answer: upstreamAnswer{"application/json", "", jsonRes}, res: jsonRes, parsed: true},
		{name: "gzip", answer: upstreamAnswer{"application/json", "gzip", gzipped(t, jsonRes)},
			resNote: " (content-encoding gzip, decoded)", res: jsonRes, parsed: true},
		{name: "br", answer: upstreamAnswer{"application/json", "br", brotlied(t, jsonRes)},
			resNote: " (content-encoding br, decoded)", res: jsonRes, parsed: true},
		{name: "zstd", answer: upstreamAnswer{"application/json", "zstd", zstded(t, jsonRes)},
			resNote: " (content-encoding zstd, decoded)", res: jsonRes, parsed: true},
		{name: "raw_flag", answer: upstreamAnswer{"application/json", "gzip", gzipped(t, jsonRes)}, args: []string{"-raw"},
			resNote: " (content-encoding gzip, not decoded: -raw)", res: gzipped(t, jsonRes), parsed: true},
		{name: "unsupported_encoding", answer: upstreamAnswer{"application/octet-stream", "x-custom", []byte("opaque \x00\x01 bytes")},
			resNote: " (content-encoding x-custom, unsupported: raw bytes)", res: []byte("opaque \x00\x01 bytes")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			ex, r := captureOne(t, dir, tc.answer, req)
			if got := ex.ResponseHeaders.Get("Content-Encoding"); got != tc.answer.encoding {
				t.Fatalf("stored Content-Encoding = %q, want %q", got, tc.answer.encoding)
			}
			code, out := dump(t, dir, append(tc.args, ex.RequestID)...)
			if code != exitOK {
				t.Fatalf("exit code = %d, want 0\n%s", code, out)
			}
			if want := wantDump(t, r, ex, "", req, tc.resNote, tc.res); out != want {
				t.Errorf("output:\n%s\nwant:\n%s", out, want)
			}
			var row map[string]any
			first, _, _ := strings.Cut(out, "\n")
			if err := json.Unmarshal([]byte(first), &row); err != nil || row["request_id"] != ex.RequestID {
				t.Errorf("first line %q (%v), want the exchange row as JSON", first, err)
			}
			events := eventLines(t, out)
			if len(events) == 0 {
				t.Fatal("no events printed")
			}
			var responseHashes int
			for _, e := range events {
				if e["request_id"] != ex.RequestID {
					t.Errorf("event %v is not the exchange's", e)
				}
				if e["source"] == "response" && e["content_hash"] != "" {
					responseHashes++
				}
			}
			if tc.parsed && responseHashes == 0 {
				t.Errorf("events %v: want response events with content hashes", events)
			}
		})
	}
}

// AC55: -last (and --last) prints the most recent exchange.
func TestDump_Last(t *testing.T) {
	dir := t.TempDir()
	req := fixture(t, "non_streaming/request.json")
	up := newFakeUpstream(t, upstreamAnswer{"application/json", "", fixture(t, "non_streaming/response.json")}.handler)
	g, url := servedAgainst(t, dir, up.URL, nil)
	sendMessage(t, url, req)
	sendMessage(t, url, req)
	g.waitQueued(dumpPath, 2)
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	ids := g.requestIDs(dumpPath)
	if len(ids) != 2 {
		t.Fatalf("got %d request lines, want 2", len(ids))
	}
	code, byID := dump(t, dir, ids[1])
	if code != exitOK {
		t.Fatalf("dump %s: exit code %d\n%s", ids[1], code, byID)
	}
	for _, flag := range []string{"-last", "--last"} {
		code, out := dump(t, dir, flag)
		if code != exitOK {
			t.Fatalf("dump %s: exit code %d\n%s", flag, code, out)
		}
		if out != byID {
			t.Errorf("dump %s printed\n%s\nwant the second exchange, as dump %s prints it:\n%s", flag, out, ids[1], byID)
		}
	}
}

// dumpFailure asserts dump printed exactly one dump failed line with reason, and
// returns it.
func dumpFailure(t *testing.T, out, reason string) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1:\n%s", len(lines), out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("line is not JSON: %q", lines[0])
	}
	if m["level"] != "error" || m["msg"] != "dump failed" || m["reason"] != reason {
		t.Errorf("line = %v, want an error dump failed line with reason %q", m, reason)
	}
	return m
}

// AC56: an unknown ID or a missing store exits 1 with the reason.
func TestDump_UnknownIDFails(t *testing.T) {
	t.Run("unknown_id", func(t *testing.T) {
		dir := t.TempDir()
		captureOne(t, dir, upstreamAnswer{"application/json", "", []byte(`{}`)}, []byte(`{}`))
		code, out := dump(t, dir, "no-such-request")
		if code != exitRuntime {
			t.Errorf("exit code = %d, want 1", code)
		}
		dumpFailure(t, out, "exchange not found")
	})
	t.Run("missing_store", func(t *testing.T) {
		inTempDir(t, nil)
		dir := t.TempDir()
		for _, args := range [][]string{{"-last"}, {"some-request"}} {
			code, out := dump(t, dir, args...)
			if code != exitRuntime {
				t.Errorf("dump %v: exit code = %d, want 1", args, code)
			}
			if line := dumpFailure(t, out, "store not found"); line["path"] != dir {
				t.Errorf("path = %v, want %s", line["path"], dir)
			}
		}
	})
}

// fileState is what a write would change about a file.
type fileState struct {
	size  int64
	mtime time.Time
}

// storeFiles is every file under dir but SQLite's -wal and -shm, which a read-only
// open may create or touch (research Q9): gateway.db and every blob.
func storeFiles(t *testing.T, dir string) map[string]fileState {
	t.Helper()
	out := map[string]fileState{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(p, "-wal") || strings.HasSuffix(p, "-shm") {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = fileState{fi.Size(), fi.ModTime()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// assertUnchanged fails if a store file was added, removed or written.
func assertUnchanged(t *testing.T, before, after map[string]fileState) {
	t.Helper()
	if len(after) != len(before) {
		t.Errorf("store files went from %v to %v", before, after)
	}
	for name, b := range before {
		if a, ok := after[name]; !ok || !a.mtime.Equal(b.mtime) || a.size != b.size {
			t.Errorf("%s went from %+v to %+v", name, b, a)
		}
	}
}

// AC57: dump never writes gateway.db or a blob. A fresh directory stays empty, and an
// existing database is left as it was, with the gateway stopped or running.
func TestDump_NeverWritesStore(t *testing.T) {
	// Over InlineMax, so the response is read from a blob.
	large := []byte(`{"type":"message","content":[{"type":"text","text":"` +
		strings.Repeat("x", 3*store.InlineMax) + `"}]}`)
	answer := upstreamAnswer{"application/json", "", large}

	t.Run("fresh_dir", func(t *testing.T) {
		inTempDir(t, nil)
		dir := t.TempDir()
		missing := filepath.Join(t.TempDir(), "absent")
		for _, d := range []string{dir, missing} {
			if code, out := dump(t, d, "-last"); code != exitRuntime {
				t.Errorf("dump in %s: exit code = %d, want 1\n%s", d, code, out)
			}
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Errorf("fresh directory holds %v (%v), want nothing", entries, err)
		}
		if _, err := os.Stat(missing); !os.IsNotExist(err) {
			t.Errorf("missing directory was created (stat err %v)", err)
		}
	})

	t.Run("gateway_stopped", func(t *testing.T) {
		dir := t.TempDir()
		ex, _ := captureOne(t, dir, answer, []byte(`{}`))
		before := storeFiles(t, dir)
		if len(before) < 2 {
			t.Fatalf("store files = %v, want gateway.db and a blob", before)
		}
		for _, args := range [][]string{{ex.RequestID}, {"-last"}, {"-raw", ex.RequestID}} {
			if code, out := dump(t, dir, args...); code != exitOK {
				t.Fatalf("dump %v: exit code = %d\n%s", args, code, out)
			}
		}
		assertUnchanged(t, before, storeFiles(t, dir))
	})

	t.Run("gateway_running", func(t *testing.T) {
		dir := t.TempDir()
		up := newBodyUpstream(t, answer.handler)
		g, url := servedAgainst(t, dir, up.URL, nil)
		sendMessage(t, url, []byte(`{}`))
		g.waitQueued(dumpPath, 1)
		id := g.requestIDs(dumpPath)[0]
		// The exchange and its parse are stored before the snapshot, so nothing the
		// gateway still has to write can land between the two.
		r := openReader(t, dir)
		deadline := time.Now().Add(5 * time.Second)
		for {
			ex, err := r.Exchange(id)
			if err == nil && ex.Parse != "" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("exchange %s not stored and parsed: %v", id, err)
			}
			time.Sleep(5 * time.Millisecond)
		}
		before := storeFiles(t, dir)
		for _, args := range [][]string{{id}, {"-last"}} {
			if code, out := dump(t, dir, args...); code != exitOK {
				t.Fatalf("dump %v: exit code = %d\n%s", args, code, out)
			}
		}
		assertUnchanged(t, before, storeFiles(t, dir))
		if code := g.stop(); code != exitOK {
			t.Errorf("gateway exit code = %d, want 0", code)
		}
		checkNoCaptureLeaks(t)
	})
}

// Bad dump arguments fail as bad gateway flags do, before any store is opened, and a
// positional argument other than dump still fails as it did in 001.
func TestDump_BadArguments(t *testing.T) {
	inTempDir(t, nil)
	dir := t.TempDir()
	for _, args := range [][]string{
		{},
		{"-last", "an-id"},
		{"one", "two"},
		{"-unknown"},
	} {
		code, out := dump(t, dir, args...)
		if code != exitConfig || !strings.Contains(out, `"msg":"invalid flags"`) {
			t.Errorf("dump %v: exit code %d, output %q; want 2 and invalid flags", args, code, out)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("directory holds %v (%v), want nothing", entries, err)
	}
	var out syncBuffer
	code := run(t.Context(), deps{args: []string{"dumpx"}, lookupEnv: func(string) (string, bool) { return "", false }, stdout: &out})
	if code != exitConfig || !strings.Contains(out.String(), `"reason":"unexpected argument"`) {
		t.Errorf("gateway dumpx: exit code %d, output %q; want 2 and unexpected argument", code, out.String())
	}
}
