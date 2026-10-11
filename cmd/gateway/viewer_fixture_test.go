package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

const fixtureDir = "../../testdata/sessions/claude-code-task"

// fixtureRows reads a JSON-lines file of the committed session fixture into maps.
func fixtureRows(t *testing.T, name string) []map[string]any {
	t.Helper()
	f, err := os.Open(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// fixtureStore copies the committed claude-code-task fixture into a fresh store with
// the real migrations and returns its directory, ready for the viewer commands.
func fixtureStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, store.DBName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	contents := fixtureRows(t, "content.jsonl")
	for _, c := range contents {
		c["size"] = len(c["data"].(string))
		c["location"] = "inline"
	}
	insertRows(t, db, "content", contents)
	insertRows(t, db, "exchanges", fixtureRows(t, "exchanges.jsonl"))
	insertRows(t, db, "events", fixtureRows(t, "events.jsonl"))
	return dir
}

// insertRows inserts fixture rows; the column names are the trusted keys of the fixture.
func insertRows(t *testing.T, db *sql.DB, table string, rows []map[string]any) {
	t.Helper()
	for _, r := range rows {
		cols := make([]string, 0, len(r))
		for k := range r {
			cols = append(cols, k)
		}
		sort.Strings(cols)
		args := make([]any, len(cols))
		for i, c := range cols {
			args[i] = r[c]
		}
		q := "INSERT INTO " + table + " (" + strings.Join(cols, ",") + ") VALUES (?" +
			strings.Repeat(",?", len(cols)-1) + ")"
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("insert into %s: %v", table, err)
		}
	}
}

func TestFixture_TimelineUsageTotals(t *testing.T) {
	dir := fixtureStore(t)
	code, out := viewerRun(t, dir, "timeline", "fixture-id-1")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	ls := lines(out)
	tail := ls[len(ls)-2:]
	want := []string{
		"== usage total: input 9123, output 1084, cache-write 129405, cache-read 422293; 1 errored",
		"   claude-opus-5-5: input 9123, output 1084, cache-write 129405, cache-read 422293; 1 errored",
	}
	for i := range want {
		if tail[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, tail[i], want[i])
		}
	}
}

func TestFixture_SessionsUsageTotals(t *testing.T) {
	dir := fixtureStore(t)
	code, out := viewerRun(t, dir, "sessions")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	var row string
	for _, l := range lines(out) {
		if strings.HasPrefix(l, "fixture-id-1\t") {
			row = l
		}
	}
	if !strings.HasSuffix(row, "\tinput 9123, output 1084, cache-write 129405, cache-read 422293; 1 errored") {
		t.Errorf("session row = %q\n%s", row, out)
	}
	// one model, so no per-model line below the row (AC6)
	if strings.Contains(out, "claude-opus-5-5:") {
		t.Errorf("single-model session printed a model line:\n%s", out)
	}
	_, js := viewerRun(t, dir, "sessions", "-json")
	if !strings.Contains(js, `"input_tokens":9123`) || !strings.Contains(js, `"errored":1`) {
		t.Errorf("sessions -json lacks the totals:\n%s", js)
	}
}

func TestFixture_HeadHelloNotAModelCall(t *testing.T) {
	dir := fixtureStore(t)
	// the hello exchange has no session key, so it sits alone in the ungrouped set with
	// no model calls: not summed, not counted missing (E6)
	_, out := viewerRun(t, dir, "sessions")
	var ungrouped string
	for _, l := range lines(out) {
		if strings.HasPrefix(l, "ungrouped\t") {
			ungrouped = l
		}
	}
	if !strings.HasSuffix(ungrouped, "\t1\t\tno model calls") {
		t.Errorf("ungrouped line = %q\n%s", ungrouped, out)
	}
	_, js := viewerRun(t, dir, "timeline", "-json", "fixture-id-1")
	jl := lines(js)
	var m map[string]any
	if err := json.Unmarshal([]byte(jl[len(jl)-1]), &m); err != nil {
		t.Fatalf("last line: %v", err)
	}
	if m["calls"] != float64(10) || m["errored"] != float64(1) {
		t.Errorf("calls %v errored %v, want 10 and 1: the hello is not a model call", m["calls"], m["errored"])
	}
}

func TestFixture_TimelineJSONCarriesSameTotals(t *testing.T) {
	dir := fixtureStore(t)
	code, out := viewerRun(t, dir, "timeline", "-json", "fixture-id-1")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	jl := lines(out)
	var m map[string]any
	if err := json.Unmarshal([]byte(jl[len(jl)-1]), &m); err != nil || m["type"] != "usage_total" {
		t.Fatalf("last line %q: %v", jl[len(jl)-1], err)
	}
	usage := m["usage"].(map[string]any)
	for k, want := range map[string]float64{
		"input_tokens": 9123, "output_tokens": 1084,
		"cache_write_tokens": 129405, "cache_read_tokens": 422293,
	} {
		if usage[k] != want {
			t.Errorf("usage.%s = %v, want %v", k, usage[k], want)
		}
	}
	if usage["complete"] != true || usage["missing"] != float64(0) || m["errored"] != float64(1) {
		t.Errorf("want complete, 0 missing, 1 errored: %v", m)
	}
	models := m["models"].([]any)
	if len(models) != 1 || models[0].(map[string]any)["model"] != "claude-opus-5-5" {
		t.Errorf("models = %v", models)
	}
}
