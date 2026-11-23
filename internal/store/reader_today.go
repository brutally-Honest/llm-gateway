package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// ExchangeStub is the few columns of an exchange that the day's totals need: who it
// belongs to, when it started, and what is needed to price it. It leaves out the
// headers, bodies and the rest, which dominate the cost of reading an ExchangeRow.
type ExchangeStub struct {
	RequestID   string
	PrincipalID string
	Protocol    string
	Client      string
	SessionKey  string // "": ungrouped
	Status      int
	StartedAt   int64  // unix nanoseconds
	TTFBNS      *int64 // nil: no response headers
	Parsed      bool   // a parse landed, so its events are stored (one transaction, SaveParse)
}

// ExchangeStubsSince lists the exchanges that started at or after since (unix ns), in
// the order ExchangesSince gives them. It uses the started_at index.
func (c *CtxReader) ExchangeStubsSince(since int64) ([]ExchangeStub, error) {
	rows, err := c.r.db.QueryContext(c.ctx, exchangeStubsSinceSQL, since)
	if err != nil {
		return nil, fmt.Errorf("store %s: exchange stubs: %w", c.r.path, err)
	}
	defer func() { _ = rows.Close() }()
	var out []ExchangeStub
	for rows.Next() {
		var (
			s    ExchangeStub
			ttfb sql.NullInt64
			key  sql.NullString
		)
		if err := rows.Scan(&s.RequestID, &s.PrincipalID, &s.Protocol, &s.Client, &key,
			&s.Status, &s.StartedAt, &ttfb, &s.Parsed); err != nil {
			return nil, fmt.Errorf("store %s: exchange stubs: %w", c.r.path, err)
		}
		s.SessionKey = key.String
		if ttfb.Valid {
			v := ttfb.Int64
			s.TTFBNS = &v
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: exchange stubs: %w", c.r.path, err)
	}
	return out, nil
}

const exchangeStubsSinceSQL = `SELECT request_id, principal_id, protocol, client, session_key, status,
	started_at, ttfb_ns, parse IS NOT NULL
	FROM exchanges WHERE started_at >= ? ORDER BY started_at, request_id`

// CallEvents reads an exchange's request and usage events, in order: all that a call's
// model and token counts come from. The other events (messages, tool calls) are the bulk
// of a parse and are not read. An exchange with none gives none.
func (c *CtxReader) CallEvents(id string) ([]EventRow, error) {
	return c.r.callEvents(c.ctx, id)
}

func (r *Reader) callEvents(ctx context.Context, id string) ([]EventRow, error) {
	rows, err := r.db.QueryContext(ctx, callEventsSQL, id)
	if err != nil {
		return nil, fmt.Errorf("store %s: call events of %s: %w", r.path, id, err)
	}
	defer func() { _ = rows.Close() }()
	var out []EventRow
	for rows.Next() {
		e := EventRow{RequestID: id}
		var payload string
		if err := rows.Scan(&e.Seq, &e.Kind, &e.Partial, &payload); err != nil {
			return nil, fmt.Errorf("store %s: call events of %s: %w", r.path, id, err)
		}
		e.Payload = json.RawMessage(payload)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: call events of %s: %w", r.path, id, err)
	}
	return out, nil
}

const callEventsSQL = `SELECT seq, kind, partial, payload FROM events
	WHERE request_id = ? AND kind IN ('request', 'usage') ORDER BY seq`
