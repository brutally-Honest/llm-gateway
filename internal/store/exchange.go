package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// ErrNotFound is returned for an exchange the store does not hold.
var ErrNotFound = errors.New("not found")

// SaveExchange stores one captured exchange: its row, and both bodies by the sha256
// of the bytes as captured (a gzip body is hashed as gzip). An empty body is stored
// as no body. One transaction, after the bodies' blobs are on disk.
func (s *Store) SaveExchange(ctx context.Context, ex *core.Exchange) error {
	reqH, err := headerJSON(ex.RequestHeader)
	if err != nil {
		return fmt.Errorf("store %s: request headers: %w", s.path, err)
	}
	resH, err := headerJSON(ex.ResponseHeader)
	if err != nil {
		return fmt.Errorf("store %s: response headers: %w", s.path, err)
	}
	var items []content
	reqHash := bodyContent(ex.Request, &items)
	resHash := bodyContent(ex.Response, &items)

	var ttfb sql.NullInt64
	if ex.HasTTFB {
		ttfb = sql.NullInt64{Int64: int64(ex.TTFB), Valid: true}
	}
	return s.write(ctx, items, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO exchanges (
			request_id, principal_id, protocol, client, auth, method, path, query,
			request_headers, response_headers, status, started_at, ttfb_ns, ended_at, stream,
			request_truncated, response_truncated, request_incomplete,
			client_disconnected, upstream_aborted, gateway_error, request_body, response_body
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ex.RequestID, ex.PrincipalID, ex.Protocol, ex.Client, string(ex.Auth),
			ex.Method, ex.Path, ex.Query, reqH, resH, ex.Status,
			ex.Start.UnixNano(), ttfb, ex.End.UnixNano(), ex.Stream,
			ex.Request.Truncated, ex.Response.Truncated, ex.RequestIncomplete,
			ex.ClientDisconnected, ex.UpstreamAborted, nullString(ex.GatewayError),
			reqHash, resHash)
		if err != nil {
			return fmt.Errorf("insert exchange %s: %w", ex.RequestID, err)
		}
		return nil
	})
}

// SaveParse stores an exchange's parse: its canonical events, the content they refer
// to, and the exchange's parse status. One transaction, after the blobs are on disk.
// An exchange the store does not hold is ErrNotFound.
func (s *Store) SaveParse(ctx context.Context, requestID, principalID string,
	status core.ParseStatus, events []core.StoredEvent, contents []core.Content) error {
	items := make([]content, 0, len(contents))
	for _, c := range contents {
		items = append(items, content{hash: c.Hash, data: c.Bytes})
	}
	return s.write(ctx, items, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE exchanges SET parse = ? WHERE request_id = ?`, string(status), requestID)
		if err != nil {
			return fmt.Errorf("set parse of %s: %w", requestID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("set parse of %s: %w", requestID, err)
		}
		if n == 0 {
			return fmt.Errorf("exchange %s: %w", requestID, ErrNotFound)
		}
		for _, e := range events {
			_, err := tx.ExecContext(ctx, `INSERT INTO events (
				request_id, seq, kind, schema_version, principal_id, source, partial,
				content_hash, tool_call_id, payload
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				requestID, e.Seq, string(e.Kind), core.SchemaVersion, principalID,
				nullString(string(e.Source)), e.Partial, nullString(e.ContentHash),
				nullString(e.ToolCallID), string(e.Payload))
			if err != nil {
				return fmt.Errorf("insert event %s/%d: %w", requestID, e.Seq, err)
			}
		}
		return nil
	})
}

// bodyContent adds b to items and returns its hash, or NULL for an empty body.
func bodyContent(b core.Body, items *[]content) sql.NullString {
	data := b.Bytes()
	if len(data) == 0 {
		return sql.NullString{}
	}
	h := hashOf(data)
	*items = append(*items, content{hash: h, data: data})
	return sql.NullString{String: h, Valid: true}
}

// headerJSON encodes h as {name: [values]}; a nil header is {}.
func headerJSON(h http.Header) (string, error) {
	if h == nil {
		h = http.Header{}
	}
	b, err := json.Marshal(h)
	return string(b), err
}

func nullString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: v != ""}
}
