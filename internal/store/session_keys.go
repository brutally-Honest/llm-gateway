package store

import (
	"context"
	"database/sql"
	"fmt"
)

// KeyAssignment is the session key to give one exchange.
type KeyAssignment struct {
	RequestID string
	Key       string
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
