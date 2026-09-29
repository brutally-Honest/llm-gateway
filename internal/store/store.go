// Package store is the capture store: a SQLite index and event database in
// <dir>/gateway.db, in WAL mode, written through one writer connection. Nothing
// outside this package knows the engine.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"go.uber.org/zap"

	// The pure-Go SQLite driver, registered as "sqlite" (ADR 0004).
	_ "modernc.org/sqlite"
)

// DBName is the database file's name inside the capture directory.
const DBName = "gateway.db"

// busyTimeoutMS is how long a write waits on a lock held by a reader.
const busyTimeoutMS = 5000

// errFileGone marks the store failed after its database file was deleted or
// replaced. The store never recreates or reopens it; a restart does.
var errFileGone = errors.New("store file deleted or replaced")

// Store is the writer side of the capture store. It is safe for concurrent use;
// writes are serialized on its single connection.
type Store struct {
	dir  string
	path string
	db   *sql.DB
	log  *zap.Logger
	id   fileID // the database file's device and inode at open

	mu     sync.Mutex
	failed bool // sticky: the file was deleted or replaced
	warned bool // the "unreadable" warning has been logged
}

// fileID is a file's identity: device and inode.
type fileID struct {
	dev, ino uint64
}

// Open creates dir (0700) and gateway.db (0600) if needed, opens the single writer
// connection and migrates the schema. Every error names the path it is about.
func Open(dir string, log *zap.Logger) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("open store %s: %w", dir, err)
	}
	// The umask can narrow MkdirAll's mode but not widen it, and an existing
	// directory keeps its mode.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("open store %s: %w", dir, err)
	}
	path := filepath.Join(dir, DBName)
	// Created before the driver opens it: SQLite gives -wal and -shm the database
	// file's mode, so all three are 0600.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}
	err = f.Chmod(0o600)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}

	db, err := sql.Open("sqlite", writerDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	// The one connection stays open, so the file identity checked before each write
	// is the one the connection writes to.
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	s := &Store{dir: dir, path: path, db: db, log: log}
	if err := s.init(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}
	return s, nil
}

// init opens the connection, records the file's identity and migrates.
func (s *Store) init(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}
	fi, err := os.Stat(s.path)
	if err != nil {
		return err
	}
	id, ok := identity(fi)
	if !ok {
		return errors.New("cannot read the file's device and inode")
	}
	s.id = id
	return migrate(ctx, s.db)
}

// writerDSN is the writer connection's DSN. The path goes through url.URL so a
// '?', '#' or '%' in it can't be read as part of the query.
func writerDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS))
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(ON)")
	u := url.URL{Scheme: "file", OmitHost: true, Path: path, RawQuery: q.Encode()}
	return u.String()
}

// Close closes the writer connection.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close store %s: %w", s.path, err)
	}
	return nil
}

// write runs fn in one transaction on the writer connection, after checking that
// the database file is still the one the store opened (research Q10, Q11).
func (s *Store) write(ctx context.Context, fn func(*sql.Tx) error) error {
	if err := s.checkFile(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store %s: begin: %w", s.path, err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store %s: %w", s.path, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store %s: commit: %w", s.path, err)
	}
	return nil
}

// checkFile compares the database file's identity with the one recorded at open.
// A missing file or a changed device or inode fails the store for the rest of the
// run, logged once. Any other stat error is warned about once and left to the
// write itself, which can still succeed through the open handles.
func (s *Store) checkFile() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return fmt.Errorf("store %s: %w", s.path, errFileGone)
	}
	fi, err := os.Stat(s.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		if !s.warned {
			s.warned = true
			s.log.Warn("store file unreadable: "+err.Error(), zap.String("path", s.path))
		}
		return nil
	}
	if err == nil {
		if id, ok := identity(fi); ok && id == s.id {
			return nil
		}
	}
	s.failed = true
	s.log.Error("store file deleted or replaced; restart the gateway to resume capture",
		zap.String("path", s.path))
	return fmt.Errorf("store %s: %w", s.path, errFileGone)
}

// identity reads a file's device and inode.
func identity(fi fs.FileInfo) (fileID, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, false
	}
	// The conversions matter off Linux, where Dev is a narrower integer.
	return fileID{dev: uint64(st.Dev), ino: uint64(st.Ino)}, true
}
