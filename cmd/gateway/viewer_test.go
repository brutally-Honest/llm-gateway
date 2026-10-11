package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

var viewerBase = time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)

type vx struct {
	id, principal, client, key string
	at                         int // seconds after viewerBase
	prompt                     string
}

// seedViewer writes exchanges (and, with a prompt, one user message each) into a
// fresh store and returns its directory.
func seedViewer(t *testing.T, xs ...vx) string {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range xs {
		start := viewerBase.Add(time.Duration(x.at) * time.Second)
		ex := &core.Exchange{RequestID: x.id, PrincipalID: orDefault(x.principal, core.PrincipalLocal),
			Protocol: "test", Client: orDefault(x.client, "cc"), Auth: core.AuthAPIKey, Method: http.MethodPost,
			Path: "/v1/m", Status: 200, Start: start, End: start.Add(time.Second), SessionKey: x.key}
		if err := s.SaveExchange(t.Context(), ex); err != nil {
			t.Fatal(err)
		}
		if x.prompt == "" {
			continue
		}
		block, _ := json.Marshal(map[string]string{"type": "text", "text": x.prompt})
		bh := sum(block)
		payload, _ := json.Marshal(map[string]any{"role": "user", "blocks": []map[string]string{{"hash": bh, "type": "text"}}})
		msg := []byte("user|" + x.prompt)
		ev := core.StoredEvent{Seq: 0, Kind: core.EventKind("message"), ContentHash: sum(msg), Payload: payload}
		err := s.SaveParse(t.Context(), x.id, ex.PrincipalID, core.ParseStatus("ok"),
			[]core.StoredEvent{ev}, []core.Content{{Hash: bh, Bytes: block}, {Hash: ev.ContentHash, Bytes: msg}})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// viewer runs `gateway <args...>` against the store in dir.
func viewerRun(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	var out syncBuffer
	env := map[string]string{"GATEWAY_CAPTURE_DIR": dir}
	code := run(t.Context(), deps{
		args: args,
		lookupEnv: func(name string) (string, bool) {
			v, ok := env[name]
			return v, ok
		},
		stdout: &out,
	})
	return code, out.String()
}

func lines(out string) []string {
	if out == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n")
}

func ts(sec int, loc *time.Location) string {
	return viewerBase.Add(time.Duration(sec) * time.Second).In(loc).Format(time.RFC3339)
}

func TestSessions_PrintsRows(t *testing.T) {
	dir := seedViewer(t,
		vx{id: "a", key: "old", at: 1, prompt: "first old"},
		vx{id: "b", key: "new", at: 5, prompt: "hello there"},
		vx{id: "c", key: "new", at: 9},
	)
	code, out := viewerRun(t, dir, "sessions")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	want := []string{
		fmt.Sprintf("new\tcc\tlocal\t%s\t%s\t2\thello there\tno model calls", ts(5, time.Local), ts(9, time.Local)),
		fmt.Sprintf("old\tcc\tlocal\t%s\t%s\t1\tfirst old\tno model calls", ts(1, time.Local), ts(1, time.Local)),
	}
	if got := lines(out); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", out, strings.Join(want, "\n"))
	}
}

func TestSessions_DefaultLimitTwentyAndN(t *testing.T) {
	var xs []vx
	for i := 0; i < 25; i++ {
		xs = append(xs, vx{id: fmt.Sprintf("r%02d", i), key: fmt.Sprintf("k%02d", i), at: i})
	}
	dir := seedViewer(t, xs...)
	for _, c := range []struct {
		args []string
		want int
	}{
		{[]string{"sessions"}, 20},
		{[]string{"sessions", "-n", "3"}, 3},
		{[]string{"sessions", "-n", "0"}, 25},
	} {
		code, out := viewerRun(t, dir, c.args...)
		if code != exitOK || len(lines(out)) != c.want {
			t.Errorf("%v: exit %d, %d lines, want %d", c.args, code, len(lines(out)), c.want)
		}
	}
	if _, out := viewerRun(t, dir, "sessions", "-n", "3"); !strings.HasPrefix(out, "k24\t") {
		t.Errorf("newest first expected, got %q", out)
	}
}

func TestSessions_EndsWithUngroupedRow(t *testing.T) {
	dir := seedViewer(t,
		vx{id: "a", key: "k", at: 5},
		vx{id: "u1", at: 1},
		vx{id: "u2", at: 3},
	)
	_, out := viewerRun(t, dir, "sessions")
	got := lines(out)
	if len(got) != 2 {
		t.Fatalf("got %q", out)
	}
	want := fmt.Sprintf("ungrouped\t\t\t%s\t%s\t2\t", ts(1, time.Local), ts(3, time.Local))
	if got[1] != want {
		t.Errorf("last row = %q, want %q", got[1], want)
	}
	// With no ungrouped exchanges there is no such row.
	_, out = viewerRun(t, seedViewer(t, vx{id: "a", key: "k"}), "sessions")
	if strings.Contains(out, "ungrouped") {
		t.Errorf("unexpected ungrouped row: %q", out)
	}
}

func TestSessions_UngroupedFlagListsExchanges(t *testing.T) {
	dir := seedViewer(t,
		vx{id: "g", key: "k", at: 9},
		vx{id: "u1", at: 1},
		vx{id: "u2", at: 3},
	)
	code, out := viewerRun(t, dir, "sessions", "-ungrouped")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	want := []string{
		fmt.Sprintf("u2\t%s\tcc\tPOST\t/v1/m\t200", ts(3, time.Local)),
		fmt.Sprintf("u1\t%s\tcc\tPOST\t/v1/m\t200", ts(1, time.Local)),
	}
	if strings.Join(lines(out), "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", out)
	}
	_, out = viewerRun(t, dir, "sessions", "-ungrouped", "-n", "1")
	if len(lines(out)) != 1 {
		t.Errorf("-n 1 gave %q", out)
	}
}

func TestSessions_EmptyStoreExitsZero(t *testing.T) {
	dir := seedViewer(t)
	code, out := viewerRun(t, dir, "sessions")
	if code != exitOK || out != "" {
		t.Errorf("exit %d output %q, want 0 and nothing", code, out)
	}
}

func TestTimeline_PrintsSession(t *testing.T) {
	dir := seedViewer(t,
		vx{id: "b", key: "k", at: 5, prompt: "second"},
		vx{id: "a", key: "k", at: 1, prompt: "first"},
	)
	code, out := viewerRun(t, dir, "timeline", "k")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	ia, ib := strings.Index(out, "first"), strings.Index(out, "second")
	if ia < 0 || ib < 0 || ia > ib {
		t.Fatalf("want first before second:\n%s", out)
	}
	if !strings.Contains(out, ts(1, time.Local)) {
		t.Errorf("local RFC 3339 time missing:\n%s", out)
	}
	if strings.Contains(out, "### session") {
		t.Errorf("a single match has no heading:\n%s", out)
	}
}

func TestTimeline_LastPrintsTopSession(t *testing.T) {
	dir := seedViewer(t,
		vx{id: "a", key: "old", at: 1, prompt: "old prompt"},
		vx{id: "b", key: "new", at: 5, prompt: "new prompt"},
	)
	code, out := viewerRun(t, dir, "timeline", "-last")
	if code != exitOK || !strings.Contains(out, "new prompt") || strings.Contains(out, "old prompt") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func notFound(t *testing.T, code int, out string) {
	t.Helper()
	if code != exitRuntime {
		t.Errorf("exit %d, want 1", code)
	}
	got := lines(out)
	if len(got) != 1 {
		t.Fatalf("stdout must hold only the failure line:\n%s", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(got[0]), &m); err != nil || m["reason"] != "session not found" {
		t.Errorf("line = %q", got[0])
	}
}

func TestTimeline_LastOnEmptyStoreFails(t *testing.T) {
	code, out := viewerRun(t, seedViewer(t), "timeline", "-last")
	notFound(t, code, out)
}

func TestTimeline_UnknownKeyFailsWithNoOutput(t *testing.T) {
	code, out := viewerRun(t, seedViewer(t, vx{id: "a", key: "k", prompt: "hi"}), "timeline", "nope")
	notFound(t, code, out)
}

func TestTimeline_SameKeyTwoClientsPrintsBoth(t *testing.T) {
	dir := seedViewer(t,
		vx{id: "a", key: "k", client: "cc", at: 1, prompt: "from cc"},
		vx{id: "b", key: "k", client: "other", principal: "alice", at: 2, prompt: "from other"},
	)
	code, out := viewerRun(t, dir, "timeline", "k")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"from cc", "from other", "client=cc principal=local", "client=other principal=alice"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "### session") != 2 {
		t.Errorf("want two headings:\n%s", out)
	}
}

func TestTimeline_JSONFlag(t *testing.T) {
	dir := seedViewer(t, vx{id: "a", key: "k", at: 1, prompt: "hi"})
	code, out := viewerRun(t, dir, "timeline", "-json", "k")
	if code != exitOK {
		t.Fatalf("exit %d", code)
	}
	for _, l := range lines(out) {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil || m["type"] == nil {
			t.Fatalf("line %q: %v", l, err)
		}
	}
	if !strings.Contains(out, `"time":"`+ts(1, time.UTC)+`"`) {
		t.Errorf("want UTC time %s:\n%s", ts(1, time.UTC), out)
	}
	_, out = viewerRun(t, dir, "sessions", "-json")
	if !strings.Contains(out, `"first":"`+ts(1, time.UTC)+`"`) {
		t.Errorf("sessions -json:\n%s", out)
	}
}

func TestViewer_NeverWritesStore(t *testing.T) {
	dir := seedViewer(t, vx{id: "a", key: "k", prompt: "hi"}, vx{id: "u"})
	before := storeFiles(t, dir)
	for _, args := range [][]string{{"sessions"}, {"sessions", "-ungrouped"}, {"timeline", "k"}, {"timeline", "-last"}} {
		if code, out := viewerRun(t, dir, args...); code != exitOK {
			t.Fatalf("%v: exit %d\n%s", args, code, out)
		}
	}
	assertUnchanged(t, before, storeFiles(t, dir))
	// A missing store is not created.
	missing := filepath.Join(t.TempDir(), "nope")
	if code, _ := viewerRun(t, missing, "sessions"); code != exitRuntime {
		t.Errorf("missing store: exit %d, want 1", code)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Error("the viewer created the store directory")
	}
}

func TestViewer_RefusesOlderAndNewerSchema(t *testing.T) {
	for _, c := range []struct {
		version int
		want    string
	}{{1, "schema older than gateway"}, {99, "schema newer than gateway"}} {
		dir := seedViewer(t, vx{id: "a", key: "k"})
		db, err := sql.Open("sqlite", filepath.Join(dir, store.DBName))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", c.version)); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
		for _, args := range [][]string{{"sessions"}, {"timeline", "k"}, {"dump", "-last"}} {
			code, out := viewerRun(t, dir, args...)
			if code != exitRuntime || !strings.Contains(out, c.want) {
				t.Errorf("%v at version %d: exit %d\n%s", args, c.version, code, out)
			}
		}
	}
}

func TestViewer_BadArguments(t *testing.T) {
	dir := seedViewer(t)
	for _, args := range [][]string{
		{"sessions", "extra"},
		{"sessions", "-n", "-1"},
		{"sessions", "-bogus"},
		{"timeline"},
		{"timeline", "a", "b"},
		{"timeline", "-last", "k"},
		{"timeline", "-bogus"},
	} {
		code, out := viewerRun(t, dir, args...)
		if code != exitConfig {
			t.Errorf("%v: exit %d, want 2\n%s", args, code, out)
		}
		if len(lines(out)) != 1 {
			t.Errorf("%v: want one log line, got %q", args, out)
		}
	}
}
