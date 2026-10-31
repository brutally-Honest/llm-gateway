package store

import (
	"context"
	"fmt"
)

// SessionRow is one session: the exchanges that share a principal, client and
// session key. First and Last are unix nanoseconds of the exchanges' start times.
type SessionRow struct {
	PrincipalID string `json:"principal_id"`
	Client      string `json:"client"`
	SessionKey  string `json:"session_key"`
	First       int64  `json:"first"`
	Last        int64  `json:"last"`
	Count       int    `json:"count"`
}

// UngroupedRow summarises the exchanges that carry no session key. Count 0 means
// there are none, and First and Last are then 0.
type UngroupedRow struct {
	Count int   `json:"count"`
	First int64 `json:"first"`
	Last  int64 `json:"last"`
}

const sessionSelect = `SELECT principal_id, client, session_key, MIN(started_at), MAX(started_at), COUNT(*)
	FROM exchanges WHERE session_key IS NOT NULL`

const sessionOrder = ` GROUP BY principal_id, client, session_key
	ORDER BY MAX(started_at) DESC, principal_id, client, session_key`

// SessionRows lists sessions, newest last activity first; limit 0 lists all.
func (r *Reader) SessionRows(limit int) ([]SessionRow, error) {
	q, args := sessionSelect+sessionOrder, []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	return r.sessions(context.Background(), q, args...)
}

// SessionsByKey lists every session with the key, whatever its client or principal,
// in SessionRows order.
func (r *Reader) SessionsByKey(key string) ([]SessionRow, error) {
	return r.sessions(context.Background(), sessionSelect+` AND session_key = ?`+sessionOrder, key)
}

func (r *Reader) sessions(ctx context.Context, q string, args ...any) ([]SessionRow, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store %s: sessions: %w", r.path, err)
	}
	defer func() { _ = rows.Close() }()
	var out []SessionRow
	for rows.Next() {
		var s SessionRow
		if err := rows.Scan(&s.PrincipalID, &s.Client, &s.SessionKey, &s.First, &s.Last, &s.Count); err != nil {
			return nil, fmt.Errorf("store %s: sessions: %w", r.path, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: sessions: %w", r.path, err)
	}
	return out, nil
}

// SessionExchanges reads one session's exchanges by start time, then request ID.
// Principal and client are part of the session's identity, so another tenant's
// exchanges with the same key are never returned.
func (r *Reader) SessionExchanges(principal, client, key string) ([]ExchangeRow, error) {
	return r.sessionExchangesCtx(context.Background(), principal, client, key)
}

func (r *Reader) sessionExchangesCtx(ctx context.Context, principal, client, key string) ([]ExchangeRow, error) {
	return r.exchanges(ctx, `WHERE principal_id = ? AND client = ? AND session_key = ?
		ORDER BY started_at, request_id`, principal, client, key)
}

// UngroupedSummary counts the exchanges with no session key and gives their first
// and last start times.
func (r *Reader) UngroupedSummary() (UngroupedRow, error) {
	return r.ungroupedSummaryCtx(context.Background())
}

func (r *Reader) ungroupedSummaryCtx(ctx context.Context) (UngroupedRow, error) {
	var u UngroupedRow
	var first, last *int64
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(started_at), MAX(started_at)
		FROM exchanges WHERE session_key IS NULL`).Scan(&u.Count, &first, &last)
	if err != nil {
		return UngroupedRow{}, fmt.Errorf("store %s: ungrouped summary: %w", r.path, err)
	}
	if first != nil {
		u.First = *first
	}
	if last != nil {
		u.Last = *last
	}
	return u, nil
}

// UngroupedExchanges lists exchanges with no session key, newest first (ties by
// request ID); limit 0 lists all.
func (r *Reader) UngroupedExchanges(limit int) ([]ExchangeRow, error) {
	q, args := `WHERE session_key IS NULL ORDER BY started_at DESC, request_id`, []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	return r.exchanges(context.Background(), q, args...)
}

func (r *Reader) exchanges(ctx context.Context, tail string, args ...any) ([]ExchangeRow, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+exchangeColumns+` FROM exchanges `+tail, args...)
	if err != nil {
		return nil, fmt.Errorf("store %s: exchanges: %w", r.path, err)
	}
	defer func() { _ = rows.Close() }()
	var out []ExchangeRow
	for rows.Next() {
		ex, err := scanExchange(rows)
		if err != nil {
			return nil, fmt.Errorf("store %s: exchanges: %w", r.path, err)
		}
		out = append(out, ex)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: exchanges: %w", r.path, err)
	}
	return out, nil
}
