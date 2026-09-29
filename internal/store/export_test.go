package store

import (
	"context"
	"database/sql"
)

// WriteInlineForTest stores one inline content row through the store's write path:
// the file check, then one transaction on the writer connection.
func (s *Store) WriteInlineForTest(ctx context.Context, hash string, data []byte) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO content (hash, size, location, data) VALUES (?, ?, 'inline', ?)`,
			hash, len(data), data)
		return err
	})
}

// PragmasForTest reads the writer connection's pragmas.
func (s *Store) PragmasForTest(ctx context.Context) map[string]string {
	out := map[string]string{}
	for _, p := range []string{"journal_mode", "busy_timeout", "synchronous", "foreign_keys"} {
		var v string
		if err := s.db.QueryRowContext(ctx, "PRAGMA "+p).Scan(&v); err == nil {
			out[p] = v
		}
	}
	return out
}

// MaxOpenConnsForTest reports the writer pool's connection limit.
func (s *Store) MaxOpenConnsForTest() int {
	return s.db.Stats().MaxOpenConnections
}
