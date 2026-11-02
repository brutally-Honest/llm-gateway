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
	// HooksOnly marks a session known only from hook events: no exchange carries
	// its key. First and Last are then hook receive times and Count is 0.
	HooksOnly bool `json:"hooks_only,omitempty"`
}

// UngroupedRow summarises the exchanges that carry no session key. Count 0 means
// there are none, and First and Last are then 0.
type UngroupedRow struct {
	Count int   `json:"count"`
	First int64 `json:"first"`
	Last  int64 `json:"last"`
}

// sessionSelect lists exchange sessions and, after them, sessions seen only in hook
// events. Callers append AND conditions and sessionOrder.
const sessionSelect = `SELECT principal_id, client, session_key, first, last, count, hooks_only FROM (
	SELECT principal_id, client, session_key, MIN(started_at) AS first, MAX(started_at) AS last,
		COUNT(*) AS count, 0 AS hooks_only
	FROM exchanges WHERE session_key IS NOT NULL GROUP BY principal_id, client, session_key
	UNION ALL
	SELECT principal_id, client, session_key, MIN(received_at), MAX(received_at), 0, 1
	FROM hook_events h WHERE session_key IS NOT NULL AND NOT EXISTS (
		SELECT 1 FROM exchanges e WHERE e.principal_id = h.principal_id
			AND e.client = h.client AND e.session_key = h.session_key)
	GROUP BY principal_id, client, session_key
) WHERE 1 = 1`

const sessionOrder = ` ORDER BY last DESC, principal_id, client, session_key`

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
		if err := rows.Scan(&s.PrincipalID, &s.Client, &s.SessionKey, &s.First, &s.Last, &s.Count, &s.HooksOnly); err != nil {
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

// HookRow is one stored hook event. Payload and Input are content hashes.
type HookRow struct {
	// ID is the stored row id: unlike Seq it never restarts with the gateway.
	ID          int64  `json:"id"`
	Seq         int64  `json:"seq"`
	PrincipalID string `json:"principal_id"`
	Client      string `json:"client"`
	SessionKey  string `json:"session_key"`
	ToolUseID   string `json:"tool_use_id"`
	Phase       string `json:"phase"`
	SourceName  string `json:"source_name"`
	ToolName    string `json:"tool_name"`
	AgentID     string `json:"agent_id"`
	AgentType   string `json:"agent_type"`
	InputHash   string `json:"input_hash"`
	PayloadHash string `json:"payload_hash"`
	ReceivedAt  int64  `json:"received_at"`
}

// HookKey is the order of hook lines: receive time, then seq, then stored row id. Seq
// restarts with the gateway, so the row id is what keeps the order total. HookEvents
// reads in this order (its ORDER BY), and the timeline and its paging compare by it.
type HookKey struct {
	ReceivedAt int64
	Seq        int64
	ID         int64
}

// Key is the row's place in the hook-line order.
func (h HookRow) Key() HookKey { return HookKey{ReceivedAt: h.ReceivedAt, Seq: h.Seq, ID: h.ID} }

// Less reports whether k sorts before o.
func (k HookKey) Less(o HookKey) bool {
	if k.ReceivedAt != o.ReceivedAt {
		return k.ReceivedAt < o.ReceivedAt
	}
	if k.Seq != o.Seq {
		return k.Seq < o.Seq
	}
	return k.ID < o.ID
}

const hookColumns = `id, seq, principal_id, client, COALESCE(session_key, ''), COALESCE(tool_use_id, ''),
	phase, source_name, tool_name, agent_id, agent_type, COALESCE(input_hash, ''), payload_hash, received_at`

// HookEvents reads one session's hook events by receive time, then seq, then row id. Principal
// and client are part of the session's identity.
func (r *Reader) HookEvents(principal, client, key string) ([]HookRow, error) {
	return r.hookEventsCtx(context.Background(), principal, client, key)
}

func (r *Reader) hookEventsCtx(ctx context.Context, principal, client, key string) ([]HookRow, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+hookColumns+` FROM hook_events
		WHERE principal_id = ? AND client = ? AND session_key = ? ORDER BY received_at, seq, id`,
		principal, client, key)
	if err != nil {
		return nil, fmt.Errorf("store %s: hook events: %w", r.path, err)
	}
	defer func() { _ = rows.Close() }()
	var out []HookRow
	for rows.Next() {
		var h HookRow
		if err := rows.Scan(&h.ID, &h.Seq, &h.PrincipalID, &h.Client, &h.SessionKey, &h.ToolUseID, &h.Phase,
			&h.SourceName, &h.ToolName, &h.AgentID, &h.AgentType, &h.InputHash, &h.PayloadHash, &h.ReceivedAt); err != nil {
			return nil, fmt.Errorf("store %s: hook events: %w", r.path, err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: hook events: %w", r.path, err)
	}
	return out, nil
}

// UngroupedHookCount counts hook events with no session key.
func (r *Reader) UngroupedHookCount() (int, error) {
	return r.ungroupedHookCountCtx(context.Background())
}

func (r *Reader) ungroupedHookCountCtx(ctx context.Context) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM hook_events WHERE session_key IS NULL`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store %s: ungrouped hook count: %w", r.path, err)
	}
	return n, nil
}
