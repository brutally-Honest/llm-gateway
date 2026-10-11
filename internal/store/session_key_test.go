package store_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// releasedStep1SHA256 pins migration step 1 as released by 002.
const releasedStep1SHA256 = "25fb8b4c22ed7ba9cf818fc523db17d2dfeb2fe3cbe8d4ac1cbe5f6b39498b36"

func TestMigrate_ReleasedStepUnchanged(t *testing.T) {
	sum := sha256.Sum256([]byte(store.MigrationForTest(1)))
	if got := hex.EncodeToString(sum[:]); got != releasedStep1SHA256 {
		t.Fatalf("migration 1 changed: sha256 %s, want %s", got, releasedStep1SHA256)
	}
}

func TestMigrate_SessionKeyStepAddsColumnAndIndex(t *testing.T) {
	dir := t.TempDir()
	// A store 002 left behind: step 1 only, one exchange.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(store.MigrationForTest(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO exchanges (request_id, principal_id, protocol, client, auth,
		method, path, query, request_headers, response_headers, status, started_at, ended_at,
		stream, request_truncated, response_truncated, request_incomplete, client_disconnected,
		upstream_aborted) VALUES ('old','local','p','c','api_key','POST','/','','{}','{}',200,1,2,0,0,0,0,0,0)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s, _ := open(t, dir)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db = rawDB(t, dir)
	if got := userVersion(t, db); got != store.MigrationCountForTest() || got < 2 {
		t.Fatalf("user_version = %d, want %d", got, store.MigrationCountForTest())
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM pragma_table_xinfo('exchanges') WHERE name = 'session_key'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("session_key column: count %d, err %v", n, err)
	}
	var cols []string
	rows, err := db.Query("SELECT name FROM pragma_index_info('exchanges_session') ORDER BY seqno")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	if got, want := strings.Join(cols, ","), "principal_id,client,session_key,started_at"; got != want {
		t.Fatalf("index columns = %s, want %s", got, want)
	}
	var key sql.NullString
	if err := db.QueryRow("SELECT session_key FROM exchanges WHERE request_id = 'old'").Scan(&key); err != nil || key.Valid {
		t.Fatalf("old exchange session_key = %v, err %v, want NULL", key, err)
	}
}

func savedKey(t *testing.T, key string) (sql.NullString, store.ExchangeRow) {
	t.Helper()
	dir := t.TempDir()
	s, _ := open(t, dir)
	ex := exchange("k1", []byte("req"), []byte("res"))
	ex.SessionKey = key
	if err := s.SaveExchange(t.Context(), ex); err != nil {
		t.Fatal(err)
	}
	var raw sql.NullString
	if err := rawDB(t, dir).QueryRow("SELECT session_key FROM exchanges WHERE request_id = 'k1'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	row, err := openReader(t, dir).Exchange("k1")
	if err != nil {
		t.Fatal(err)
	}
	return raw, row
}

func TestSaveExchange_StoresSessionKey(t *testing.T) {
	const key = " Opaque/Key:1 "
	raw, row := savedKey(t, key)
	if !raw.Valid || raw.String != key {
		t.Fatalf("stored session_key = %v, want %q verbatim", raw, key)
	}
	if row.SessionKey != key {
		t.Fatalf("row.SessionKey = %q, want %q", row.SessionKey, key)
	}
}

func TestSaveExchange_NoKeyStoresNull(t *testing.T) {
	raw, row := savedKey(t, "")
	if raw.Valid {
		t.Fatalf("stored session_key = %q, want NULL", raw.String)
	}
	if row.SessionKey != "" {
		t.Fatalf("row.SessionKey = %q, want empty", row.SessionKey)
	}
}

func TestDump_UngroupedRowUnchanged(t *testing.T) {
	_, row := savedKey(t, "")
	b, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "session_key") {
		t.Fatalf("ungrouped row JSON mentions session_key: %s", b)
	}
	_, keyed := savedKey(t, "k")
	b, _ = json.Marshal(keyed)
	if !strings.Contains(string(b), `"session_key":"k"`) {
		t.Fatalf("keyed row JSON lacks session_key: %s", b)
	}
}
