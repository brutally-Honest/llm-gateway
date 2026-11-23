package store_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// A store written by the binary before migration 6 gets the two unused indexes dropped on
// Open, and keeps its rows (spec 041 S3).
func TestOpen_MigratesStoreFromVersion5DroppingUnusedIndexes(t *testing.T) {
	dir := t.TempDir()

	// Build the database exactly as the version-5 binary left it: steps 1..5 only.
	old, err := sql.Open("sqlite", "file:"+filepath.Join(dir, dbName))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	for n := 1; n <= 5; n++ {
		if _, err := old.Exec(store.MigrationForTest(n)); err != nil {
			t.Fatalf("migration %d: %v", n, err)
		}
	}
	if _, err := old.Exec("PRAGMA user_version = 5"); err != nil {
		t.Fatalf("set user_version: %v", err)
	}
	if _, err := old.Exec(`
INSERT INTO exchanges (request_id, principal_id, protocol, client, auth, method, path, query,
  request_headers, response_headers, status, started_at, ended_at, stream,
  request_truncated, response_truncated, request_incomplete, client_disconnected, upstream_aborted)
VALUES ('r1', 'local', 'p', 'cc', 'none', 'POST', '/v1/x', '', '{}', '{}', 200, 1, 2, 0, 0, 0, 0, 0, 0)`); err != nil {
		t.Fatalf("insert exchange: %v", err)
	}
	if _, err := old.Exec(`
INSERT INTO events (request_id, seq, kind, schema_version, principal_id, partial, tool_call_id, payload)
VALUES ('r1', 1, 'tool_call', 1, 'local', 0, 'tc1', '{"name":"x"}')`); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if err := old.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s, _ := open(t, dir)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db := rawDB(t, dir)
	if got := userVersion(t, db); got != store.MigrationCountForTest() {
		t.Fatalf("user_version = %d, want %d", got, store.MigrationCountForTest())
	}
	for _, name := range []string{"events_tool_call", "events_principal"} {
		var n int
		if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name = ?", name).Scan(&n); err != nil {
			t.Fatalf("sqlite_schema %s: %v", name, err)
		}
		if n != 0 {
			t.Errorf("index %s still present after migration 6", name)
		}
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name = 'events_content' AND type = 'index'").Scan(&n); err != nil {
		t.Fatalf("sqlite_schema events_content: %v", err)
	}
	if n != 1 {
		t.Errorf("events_content index missing after migration 6")
	}
	var payload, toolCallID string
	err = db.QueryRow("SELECT payload, tool_call_id FROM events WHERE request_id = 'r1' AND seq = 1").Scan(&payload, &toolCallID)
	if err != nil {
		t.Fatalf("read inserted event: %v", err)
	}
	if payload != `{"name":"x"}` || toolCallID != "tc1" {
		t.Errorf("event = (%q, %q), want the inserted row", payload, toolCallID)
	}
}
