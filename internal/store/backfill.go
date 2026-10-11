package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
)

// Unkeyed is one stored exchange that has no session key yet.
type Unkeyed struct {
	Rowid          int64
	RequestID      string
	Client         string
	RequestHeaders http.Header
}

// KeyAssignment is the session key to give one exchange.
type KeyAssignment struct {
	RequestID string
	Key       string
}

// UnkeyedBatch returns up to limit exchanges with no session key whose rowid is
// above afterRowid, in rowid order. Pass the last rowid of a batch to get the next.
func (s *Store) UnkeyedBatch(ctx context.Context, afterRowid int64, limit int) ([]Unkeyed, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT rowid, request_id, client, request_headers
		FROM exchanges WHERE session_key IS NULL AND rowid > ? ORDER BY rowid LIMIT ?`,
		afterRowid, limit)
	if err != nil {
		return nil, fmt.Errorf("store %s: unkeyed exchanges: %w", s.path, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Unkeyed
	for rows.Next() {
		var (
			u Unkeyed
			h string
		)
		if err := rows.Scan(&u.Rowid, &u.RequestID, &u.Client, &h); err != nil {
			return nil, fmt.Errorf("store %s: unkeyed exchanges: %w", s.path, err)
		}
		if err := json.Unmarshal([]byte(h), &u.RequestHeaders); err != nil {
			return nil, fmt.Errorf("store %s: exchange %s: request_headers: %w", s.path, u.RequestID, err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: unkeyed exchanges: %w", s.path, err)
	}
	return out, nil
}

// SetSessionKeys gives each exchange its key in one transaction and returns how many
// it changed. An exchange that already has a key is left as it is, so a rerun or a
// run beside a capturing gateway changes nothing that was keyed.
func (s *Store) SetSessionKeys(ctx context.Context, as []KeyAssignment) (filled int, err error) {
	if len(as) == 0 {
		return 0, nil
	}
	err = s.write(ctx, nil, func(tx *sql.Tx) error {
		filled = 0
		for _, a := range as {
			res, err := tx.ExecContext(ctx,
				`UPDATE exchanges SET session_key = ? WHERE request_id = ? AND session_key IS NULL`,
				a.Key, a.RequestID)
			if err != nil {
				return fmt.Errorf("set session key of %s: %w", a.RequestID, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return fmt.Errorf("set session key of %s: %w", a.RequestID, err)
			}
			filled += int(n)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return filled, nil
}
