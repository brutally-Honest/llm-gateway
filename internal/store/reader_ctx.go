package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CtxReader is a Reader view whose every query carries ctx, so a cancelled request
// or a timeout abandons the query. It satisfies timeline.Source. Make one per
// request; it holds no state beyond the context.
type CtxReader struct {
	r   *Reader
	ctx context.Context
}

// WithContext returns a view of r whose queries run under ctx.
func (r *Reader) WithContext(ctx context.Context) *CtxReader {
	return &CtxReader{r: r, ctx: ctx}
}

// SessionExchanges is Reader.SessionExchanges under the view's context.
func (c *CtxReader) SessionExchanges(principal, client, key string) ([]ExchangeRow, error) {
	return c.r.sessionExchangesCtx(c.ctx, principal, client, key)
}

// Events is Reader.Events under the view's context.
func (c *CtxReader) Events(id string) ([]EventRow, error) { return c.r.eventsCtx(c.ctx, id) }

// Content is Reader.Content under the view's context.
func (c *CtxReader) Content(hash string) ([]byte, error) { return c.r.contentCtx(c.ctx, hash) }

// Exchange is Reader.Exchange under the view's context.
func (c *CtxReader) Exchange(id string) (ExchangeRow, error) { return c.r.exchangeCtx(c.ctx, id) }

// UngroupedSummary is Reader.UngroupedSummary under the view's context.
func (c *CtxReader) UngroupedSummary() (UngroupedRow, error) {
	return c.r.ungroupedSummaryCtx(c.ctx)
}

// SessionAfter is the after key of a session page: the sort tuple of the last session
// on that page, its last activity and identity. Sessions after it in SessionRows order
// come next.
type SessionAfter struct {
	Last        int64
	PrincipalID string
	Client      string
	SessionKey  string
}

// UngroupedAfter is the after key of an ungrouped page: the sort tuple of the last
// ungrouped exchange on that page.
type UngroupedAfter struct {
	StartedAt int64
	RequestID string
}

// SessionPage lists up to limit sessions in SessionRows order, strictly after the
// after key (nil: from the start). The full tuple is compared, so sessions that share
// a last activity are neither repeated nor skipped. limit must be positive.
func (c *CtxReader) SessionPage(after *SessionAfter, limit int) ([]SessionRow, error) {
	q, args := sessionSelect+` GROUP BY principal_id, client, session_key`, []any{}
	if after != nil {
		q += ` HAVING MAX(started_at) < ? OR (MAX(started_at) = ? AND (principal_id, client, session_key) > (?, ?, ?))`
		args = append(args, after.Last, after.Last, after.PrincipalID, after.Client, after.SessionKey)
	}
	q += ` ORDER BY MAX(started_at) DESC, principal_id, client, session_key LIMIT ?`
	args = append(args, limit)
	return c.r.sessions(c.ctx, q, args...)
}

// UngroupedPage lists up to limit exchanges with no session key, newest first (ties
// by request ID), strictly after the after key (nil: from the start).
func (c *CtxReader) UngroupedPage(after *UngroupedAfter, limit int) ([]ExchangeRow, error) {
	q, args := `WHERE session_key IS NULL`, []any{}
	if after != nil {
		q += ` AND (started_at < ? OR (started_at = ? AND request_id > ?))`
		args = append(args, after.StartedAt, after.StartedAt, after.RequestID)
	}
	q += ` ORDER BY started_at DESC, request_id LIMIT ?`
	args = append(args, limit)
	return c.r.exchanges(c.ctx, q, args...)
}

// SessionByKey reads the one session with this identity, or ErrNotFound.
func (c *CtxReader) SessionByKey(principal, client, key string) (SessionRow, error) {
	rows, err := c.r.sessions(c.ctx, sessionSelect+` AND principal_id = ? AND client = ? AND session_key = ?`+sessionOrder,
		principal, client, key)
	if err != nil {
		return SessionRow{}, err
	}
	if len(rows) == 0 {
		return SessionRow{}, fmt.Errorf("store %s: session %s: %w", c.r.path, key, ErrNotFound)
	}
	return rows[0], nil
}

// RowID reads the rowid of the exchange with this request ID, or ErrNotFound.
func (c *CtxReader) RowID(requestID string) (int64, error) {
	var id int64
	err := c.r.db.QueryRowContext(c.ctx, `SELECT rowid FROM exchanges WHERE request_id = ?`, requestID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("store %s: exchange %s: %w", c.r.path, requestID, ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("store %s: rowid of %s: %w", c.r.path, requestID, err)
	}
	return id, nil
}

// RowExchange is an exchange with its rowid.
type RowExchange struct {
	RowID int64
	ExchangeRow
}

// SessionSnapshot reads one session's exchanges in timeline order (start, request
// ID) with their row ids, and the highest row id among them, in one read snapshot.
// A session with no exchanges returns none and max 0.
func (c *CtxReader) SessionSnapshot(principal, client, key string) ([]RowExchange, int64, error) {
	tx, err := c.r.db.BeginTx(c.ctx, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("store %s: session snapshot: %w", c.r.path, err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(c.ctx, `SELECT rowid, `+exchangeColumns+` FROM exchanges
		WHERE principal_id = ? AND client = ? AND session_key = ? ORDER BY started_at, request_id`,
		principal, client, key)
	if err != nil {
		return nil, 0, fmt.Errorf("store %s: session snapshot: %w", c.r.path, err)
	}
	defer func() { _ = rows.Close() }()
	var (
		out []RowExchange
		max int64
	)
	for rows.Next() {
		var id int64
		ex, err := scanExchange(rowIDScanner{rows, &id})
		if err != nil {
			return nil, 0, fmt.Errorf("store %s: session snapshot: %w", c.r.path, err)
		}
		out = append(out, RowExchange{RowID: id, ExchangeRow: ex})
		if id > max {
			max = id
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store %s: session snapshot: %w", c.r.path, err)
	}
	return out, max, nil
}

// rowIDScanner scans a leading rowid column, then hands the rest to scanExchange.
type rowIDScanner struct {
	rows *sql.Rows
	id   *int64
}

func (s rowIDScanner) Scan(dest ...any) error {
	return s.rows.Scan(append([]any{s.id}, dest...)...)
}
