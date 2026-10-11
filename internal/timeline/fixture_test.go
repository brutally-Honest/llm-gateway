package timeline_test

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"go.uber.org/zap"
	_ "modernc.org/sqlite"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// jsonLines reads a JSON-lines file into maps.
func jsonLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
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
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, m)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// insertAll inserts rows into table; the column names are the (trusted, fixture) keys.
func insertAll(t *testing.T, db *sql.DB, table string, rows []map[string]any) {
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

// openFixture builds a temp store from the committed session fixture, with the real
// migrations, and opens it through store.OpenReader.
func openFixture(t *testing.T) *store.Reader {
	t.Helper()
	dirs, err := filepath.Glob("../../testdata/sessions/*")
	if err != nil || len(dirs) != 1 {
		t.Fatalf("want one session fixture, got %v (%v)", dirs, err)
	}
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
	contents := jsonLines(t, filepath.Join(dirs[0], "content.jsonl"))
	for _, c := range contents {
		c["size"] = len(c["data"].(string))
		c["location"] = "inline"
	}
	insertAll(t, db, "content", contents)
	insertAll(t, db, "exchanges", jsonLines(t, filepath.Join(dirs[0], "exchanges.jsonl")))
	insertAll(t, db, "events", jsonLines(t, filepath.Join(dirs[0], "events.jsonl")))
	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestBuild_RealSessionFixture(t *testing.T) {
	r := openFixture(t)
	sessions, err := r.SessionRows(0)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %v, %v", sessions, err)
	}
	session := sessions[0]
	exs, err := r.SessionExchanges(session.PrincipalID, session.Client, session.SessionKey)
	if err != nil || len(exs) != session.Count {
		t.Fatalf("exchanges = %d, %v", len(exs), err)
	}

	// D12: the request events point at system content that the fixture leaves out.
	missingSystem := 0
	wantCalls := map[string]bool{}
	wantErrors := 0
	for _, ex := range exs {
		evs, err := r.Events(ex.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range evs {
			switch e.Kind {
			case "request":
				var p struct {
					SystemHash string `json:"system_hash"`
				}
				if err := json.Unmarshal(e.Payload, &p); err != nil {
					t.Fatal(err)
				}
				if p.SystemHash == "" {
					continue
				}
				if _, err := r.Content(p.SystemHash); errors.Is(err, store.ErrNotFound) {
					missingSystem++
				}
			case "tool_call":
				wantCalls[e.ToolCallID] = true
			case "error":
				wantErrors++
			}
		}
	}
	if missingSystem == 0 {
		t.Fatalf("no request event has a system_hash without stored content")
	}

	for _, opts := range []timeline.Options{{}, {Full: true}} {
		entries, err := timeline.Build(r, session, opts)
		if err != nil {
			t.Fatalf("Build(%+v): %v", opts, err)
		}
		seen := map[string]bool{}
		calls := map[string]bool{}
		errs := 0
		var last int64
		for _, e := range entries {
			if e.Start < last {
				t.Fatalf("entry out of order: %d < %d", e.Start, last)
			}
			last = e.Start
			switch e.Kind {
			case timeline.KindExchange, timeline.KindFolded:
				seen[e.RequestID] = true
			case timeline.KindToolCall:
				calls[e.ToolCallID] = true
			case timeline.KindError:
				errs++
			}
			if opts.Full && e.Omitted != 0 {
				t.Fatalf("Full cut an entry")
			}
		}
		if len(seen) != len(exs) {
			t.Fatalf("%d of %d exchanges in the timeline", len(seen), len(exs))
		}
		if len(calls) != len(wantCalls) {
			t.Fatalf("tool calls %d, want %d", len(calls), len(wantCalls))
		}
		for id := range wantCalls {
			if !calls[id] {
				t.Fatalf("tool call %s missing", id)
			}
		}
		if errs != wantErrors {
			t.Fatalf("errors %d, want %d", errs, wantErrors)
		}
	}
}
