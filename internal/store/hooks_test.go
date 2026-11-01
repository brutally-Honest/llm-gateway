package store_test

import (
	"bytes"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

func hookRec(payload string, ev core.HarnessEvent, at time.Time) *store.HookRecord {
	return &store.HookRecord{PrincipalID: "local", Client: "client-a", ReceivedAt: at,
		Payload: []byte(payload), Event: ev}
}

func TestSaveHook_PayloadByteForByte(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	// Not canonical JSON, and over InlineMax so it goes through a blob.
	payload := "{ \"a\" :1,\n \"pad\":\"" + strings.Repeat("x", store.InlineMax+10) + "\" }\r\n"
	at := time.Unix(100, 5)
	ev := core.HarnessEvent{Phase: core.PhasePreTool, SessionKey: "s1", ToolCallID: "tu1",
		ToolName: "Bash", ToolInput: []byte(`{"command":"ls"}`), SourceName: "evt-a"}
	if err := s.SaveHook(t.Context(), hookRec(payload, ev, at)); err != nil {
		t.Fatal(err)
	}
	var ph, ih, sess, tu, client string
	var recv int64
	if err := rawDB(t, dir).QueryRow(`SELECT payload_hash, input_hash, session_key, tool_use_id, client, received_at
		FROM hook_events`).Scan(&ph, &ih, &sess, &tu, &client, &recv); err != nil {
		t.Fatal(err)
	}
	r := openReader(t, dir)
	got, err := r.Content(ph)
	if err != nil || !bytes.Equal(got, []byte(payload)) {
		t.Fatalf("payload = %q, err %v", got, err)
	}
	in, err := r.Content(ih)
	if err != nil || string(in) != `{"command":"ls"}` {
		t.Fatalf("input = %q, err %v", in, err)
	}
	if sess != "s1" || tu != "tu1" || client != "client-a" || recv != at.UnixNano() {
		t.Fatalf("row = %q %q %q %d", sess, tu, client, recv)
	}
}

func TestSaveHook_StoresNoRequestHeaders(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	if err := s.SaveHook(t.Context(), hookRec(`{"x":1}`, core.HarnessEvent{Phase: core.PhaseTurnEnd}, time.Now())); err != nil {
		t.Fatal(err)
	}
	rows, err := rawDB(t, dir).Query(`SELECT name FROM pragma_table_xinfo('hook_events')`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		l := strings.ToLower(n)
		if strings.Contains(l, "header") || strings.Contains(l, "auth") || strings.Contains(l, "token") {
			t.Fatalf("hook_events has a credential-bearing column %q", n)
		}
	}
}

func TestSaveHook_SessionIDVerbatim(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	const key = " Sess/ABC:1 "
	if err := s.SaveHook(t.Context(), hookRec(`{}`, core.HarnessEvent{SessionKey: key}, time.Now())); err != nil {
		t.Fatal(err)
	}
	ex := exchange("e1", []byte("a"), []byte("b"))
	ex.SessionKey = key
	if err := s.SaveExchange(t.Context(), ex); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := rawDB(t, dir).QueryRow(`SELECT count(*) FROM hook_events h JOIN exchanges e
		ON e.session_key = h.session_key`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("exact join count = %d, err %v", n, err)
	}
	var got string
	if err := rawDB(t, dir).QueryRow(`SELECT session_key FROM hook_events`).Scan(&got); err != nil || got != key {
		t.Fatalf("session_key = %q, err %v", got, err)
	}
}

func TestSaveHook_IdenticalPayloadsBothStored(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	at := time.Unix(10, 0)
	for range 2 {
		if err := s.SaveHook(t.Context(), hookRec(`{"same":true}`, core.HarnessEvent{SessionKey: "s"}, at)); err != nil {
			t.Fatal(err)
		}
	}
	db := rawDB(t, dir)
	var rows, hashes, seqs int
	if err := db.QueryRow(`SELECT count(*), count(DISTINCT payload_hash), count(DISTINCT seq) FROM hook_events`).
		Scan(&rows, &hashes, &seqs); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || hashes != 1 || seqs != 2 {
		t.Fatalf("rows %d hashes %d seqs %d, want 2 1 2", rows, hashes, seqs)
	}
}

func TestSaveHook_NoSessionStoredUngrouped(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	if err := s.SaveHook(t.Context(), hookRec(`not json`, core.HarnessEvent{Phase: core.PhaseUnknown}, time.Now())); err != nil {
		t.Fatal(err)
	}
	var sess, tu sql.NullString
	if err := rawDB(t, dir).QueryRow(`SELECT session_key, tool_use_id FROM hook_events`).Scan(&sess, &tu); err != nil {
		t.Fatal(err)
	}
	if sess.Valid || tu.Valid {
		t.Fatalf("session %v tool_use %v, want NULL", sess, tu)
	}
}

func TestMigrate_V2StoreUpgradesKeepsData(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if _, err := db.Exec(store.MigrationForTest(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO exchanges (request_id, principal_id, protocol, client, auth,
		method, path, query, request_headers, response_headers, status, started_at, ended_at,
		stream, request_truncated, response_truncated, request_incomplete, client_disconnected,
		upstream_aborted, session_key) VALUES ('old','local','p','c','api_key','POST','/','','{}','{}',200,1,2,0,0,0,0,0,0,'k')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s, _ := open(t, dir)
	if err := s.SaveHook(t.Context(), hookRec(`{}`, core.HarnessEvent{}, time.Now())); err != nil {
		t.Fatal(err)
	}
	raw := rawDB(t, dir)
	if got := userVersion(t, raw); got != store.MigrationCountForTest() || got < 3 {
		t.Fatalf("user_version = %d", got)
	}
	var k string
	if err := raw.QueryRow(`SELECT session_key FROM exchanges WHERE request_id='old'`).Scan(&k); err != nil || k != "k" {
		t.Fatalf("old exchange lost: %q %v", k, err)
	}
}
