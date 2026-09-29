package store_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

const (
	dbName      = "gateway.db"
	deletedMsg  = "store file deleted or replaced; restart the gateway to resume capture"
	unreadable  = "store file unreadable: "
	schemaNewer = "schema newer than gateway"
)

// syncBuffer is a goroutine-safe log sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// lines decodes every JSON log line written so far.
func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	b.mu.Lock()
	raw := b.buf.String()
	b.mu.Unlock()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

// withMsg returns the lines whose msg starts with prefix.
func withMsg(lines []map[string]any, prefix string) []map[string]any {
	var out []map[string]any
	for _, l := range lines {
		if msg, _ := l["msg"].(string); strings.HasPrefix(msg, prefix) {
			out = append(out, l)
		}
	}
	return out
}

func newLogger() (*zap.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return logging.New(buf, "debug"), buf
}

func open(t *testing.T, dir string) (*store.Store, *syncBuffer) {
	t.Helper()
	log, buf := newLogger()
	s, err := store.Open(dir, log)
	if err != nil {
		t.Fatalf("Open(%s): %v", dir, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, buf
}

// rawDB opens the database with no pragmas, for inspecting or tampering with it.
func rawDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, dbName))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("PRAGMA user_version: %v", err)
	}
	return v
}

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
}

func TestStore_MigratesFromEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	s, _ := open(t, dir)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db := rawDB(t, dir)
	if got := userVersion(t, db); got != 1 {
		t.Fatalf("user_version = %d, want 1", got)
	}
	want := map[string]string{
		"content":              "table",
		"exchanges":            "table",
		"events":               "table",
		"exchanges_started_at": "index",
		"exchanges_principal":  "index",
		"events_content":       "index",
		"events_tool_call":     "index",
		"events_principal":     "index",
	}
	for name, typ := range want {
		var got string
		err := db.QueryRow("SELECT type FROM sqlite_schema WHERE name = ?", name).Scan(&got)
		if err != nil {
			t.Errorf("%s %s missing: %v", typ, name, err)
			continue
		}
		if got != typ {
			t.Errorf("%s is a %s, want %s", name, got, typ)
		}
	}
	for _, col := range []string{"request_incomplete", "truncated", "parse"} {
		var n int
		err := db.QueryRow("SELECT count(*) FROM pragma_table_xinfo('exchanges') WHERE name = ?", col).Scan(&n)
		if err != nil || n != 1 {
			t.Errorf("exchanges.%s: count %d, err %v", col, n, err)
		}
	}
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q (err %v), want wal", mode, err)
	}
	_ = db.Close()

	// Opening again applies nothing new and keeps the version.
	s2, _ := open(t, dir)
	if err := s2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := userVersion(t, rawDB(t, dir)); got != 1 {
		t.Fatalf("user_version after reopen = %d, want 1", got)
	}
}

func TestStore_Pragmas(t *testing.T) {
	s, _ := open(t, t.TempDir())
	got := s.PragmasForTest(t.Context())
	want := map[string]string{
		"journal_mode": "wal",
		"busy_timeout": "5000",
		"synchronous":  "1", // NORMAL
		"foreign_keys": "1",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("PRAGMA %s = %q, want %q", k, got[k], v)
		}
	}
	if n := s.MaxOpenConnsForTest(); n != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", n)
	}
}

// TestStore_Permissions is not parallel: the umask is process-wide.
func TestStore_Permissions(t *testing.T) {
	cases := []struct {
		name     string
		umask    int
		existing os.FileMode // 0: the directory does not exist yet
	}{
		{"umask_0022", 0o022, 0},
		{"umask_0000", 0o000, 0},
		{"existing_dir_0755", 0o000, 0o755},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := syscall.Umask(tc.umask)
			defer syscall.Umask(old)

			dir := filepath.Join(t.TempDir(), "store")
			if tc.existing != 0 {
				if err := os.Mkdir(dir, tc.existing); err != nil {
					t.Fatal(err)
				}
			}
			s, _ := open(t, dir)
			// A write makes SQLite create -wal and -shm.
			if err := s.WriteInlineForTest(t.Context(), "aa", []byte("x")); err != nil {
				t.Fatalf("write: %v", err)
			}

			fi, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := fi.Mode().Perm(); got != 0o700 {
				t.Errorf("dir mode = %o, want 700", got)
			}
			seen := map[string]bool{}
			err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
				if err != nil || path == dir {
					return err
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				seen[d.Name()] = true
				want := os.FileMode(0o600)
				if d.IsDir() {
					want = 0o700
				}
				if got := info.Mode().Perm(); got != want {
					t.Errorf("%s mode = %o, want %o", path, got, want)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{dbName, dbName + "-wal", dbName + "-shm"} {
				if !seen[name] {
					t.Errorf("%s not found; files: %v", name, seen)
				}
			}
		})
	}
}

func TestStore_OpenFailsFast(t *testing.T) {
	t.Run("unwritable_dir", func(t *testing.T) {
		skipIfRoot(t)
		parent := filepath.Join(t.TempDir(), "locked")
		if err := os.Mkdir(parent, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
		dir := filepath.Join(parent, "store")
		assertOpenFails(t, dir, "permission denied")
	})
	t.Run("file_in_place_of_dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "store")
		if err := os.WriteFile(dir, []byte("not a dir"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertOpenFails(t, dir, "not a directory")
	})
	t.Run("newer_schema", func(t *testing.T) {
		dir := t.TempDir()
		s, _ := open(t, dir)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		db := rawDB(t, dir)
		if _, err := db.Exec("PRAGMA user_version = 99"); err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
		assertOpenFails(t, dir, schemaNewer)
		if got := userVersion(t, rawDB(t, dir)); got != 99 {
			t.Errorf("user_version = %d after the failed open, want 99 untouched", got)
		}
	})
}

func assertOpenFails(t *testing.T, dir, reason string) {
	t.Helper()
	log, _ := newLogger()
	s, err := store.Open(dir, log)
	if err == nil {
		_ = s.Close()
		t.Fatalf("Open(%s) succeeded, want an error", dir)
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("error %q does not name the path %s", err, dir)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Errorf("error %q does not give the reason %q", err, reason)
	}
}

func TestStore_DeletedDatabase(t *testing.T) {
	t.Run("unlinked", testUnlinked)
	t.Run("replaced", testReplaced)
	t.Run("stat_permission_denied_not_deleted", testStatPermissionDenied)
}

func testUnlinked(t *testing.T) {
	dir := t.TempDir()
	s, buf := open(t, dir)
	ctx := t.Context()
	if err := s.WriteInlineForTest(ctx, "aa", []byte("before")); err != nil {
		t.Fatalf("write before delete: %v", err)
	}
	dbPath := filepath.Join(dir, dbName)
	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}
	for i, h := range []string{"bb", "cc", "dd"} {
		err := s.WriteInlineForTest(ctx, h, []byte("after"))
		if err == nil {
			t.Fatalf("write %d after delete succeeded, want an error", i)
		}
		if !strings.Contains(err.Error(), dbPath) {
			t.Errorf("write %d error %q does not name %s", i, err, dbPath)
		}
	}
	deleted := withMsg(buf.lines(t), deletedMsg)
	if len(deleted) != 1 {
		t.Fatalf("%d %q lines, want exactly 1", len(deleted), deletedMsg)
	}
	if deleted[0]["path"] != dbPath || deleted[0]["level"] != "error" {
		t.Errorf("line = %v, want level error and path %s", deleted[0], dbPath)
	}
	if _, err := os.Stat(dbPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat %s: %v; the store must not recreate it", dbPath, err)
	}
	_ = s.Close()
	if _, err := os.Stat(dbPath); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat %s after Close: %v; the store must not recreate it", dbPath, err)
	}
}

func testReplaced(t *testing.T) {
	dir := t.TempDir()
	s, buf := open(t, dir)
	ctx := t.Context()
	if err := s.WriteInlineForTest(ctx, "aa", []byte("before")); err != nil {
		t.Fatalf("write before replace: %v", err)
	}
	dbPath := filepath.Join(dir, dbName)
	tmp := dbPath + ".new"
	if err := os.WriteFile(tmp, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, dbPath); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteInlineForTest(ctx, "bb", []byte("after")); err == nil {
		t.Fatal("write after replace succeeded, want an error")
	}
	if n := len(withMsg(buf.lines(t), deletedMsg)); n != 1 {
		t.Fatalf("%d %q lines, want 1", n, deletedMsg)
	}
}

func testStatPermissionDenied(t *testing.T) {
	skipIfRoot(t)
	dir := t.TempDir()
	log, buf := newLogger()
	s, err := store.Open(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := s.WriteInlineForTest(ctx, "aa", []byte("before")); err != nil {
		t.Fatalf("write before chmod: %v", err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	for i, h := range []string{"bb", "cc", "dd"} {
		if err := s.WriteInlineForTest(ctx, h, []byte("inline")); err != nil {
			_ = os.Chmod(dir, 0o700)
			t.Fatalf("inline write %d after chmod 000: %v", i, err)
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	lines := buf.lines(t)
	if n := len(withMsg(lines, deletedMsg)); n != 0 {
		t.Errorf("%d %q lines, want none", n, deletedMsg)
	}
	warn := withMsg(lines, unreadable)
	if len(warn) != 1 {
		t.Fatalf("%d %q lines, want exactly 1", len(warn), unreadable)
	}
	if warn[0]["level"] != "warn" || warn[0]["path"] != filepath.Join(dir, dbName) {
		t.Errorf("line = %v, want level warn and path %s", warn[0], filepath.Join(dir, dbName))
	}
	var n int
	if err := rawDB(t, dir).QueryRow("SELECT count(*) FROM content").Scan(&n); err != nil || n != 4 {
		t.Errorf("content rows = %d (err %v), want 4", n, err)
	}
}

// A cancelled context fails the write without marking the store failed.
func TestStore_WriteHonoursContext(t *testing.T) {
	s, buf := open(t, t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.WriteInlineForTest(ctx, "aa", []byte("x")); err == nil {
		t.Fatal("write with a cancelled context succeeded")
	}
	if err := s.WriteInlineForTest(t.Context(), "aa", []byte("x")); err != nil {
		t.Fatalf("write after a cancelled one: %v", err)
	}
	if n := len(withMsg(buf.lines(t), deletedMsg)); n != 0 {
		t.Errorf("%d %q lines, want none", n, deletedMsg)
	}
}
