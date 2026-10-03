package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
)

// Reader is a read-only view of a capture store, for `gateway dump` and tests. It
// never writes gateway.db or a blob; beside a stopped gateway, SQLite may create the
// -wal and -shm files (research Q9). It is safe for concurrent use.
type Reader struct {
	dir  string
	path string
	db   *sql.DB
	dec  *zstd.Decoder // DecodeAll is safe for concurrent use
}

// ExchangeRow is one stored exchange. A NULL column reads as "" (or nil for
// TTFBNS). Body fields are content hashes, readable through Content.
type ExchangeRow struct {
	RequestID          string      `json:"request_id"`
	PrincipalID        string      `json:"principal_id"`
	Protocol           string      `json:"protocol"`
	Client             string      `json:"client"`
	Auth               string      `json:"auth"`
	Method             string      `json:"method"`
	Path               string      `json:"path"`
	Query              string      `json:"query"`
	RequestHeaders     http.Header `json:"request_headers"`
	ResponseHeaders    http.Header `json:"response_headers"`
	Status             int         `json:"status"`
	StartedAt          int64       `json:"started_at"` // unix nanoseconds
	TTFBNS             *int64      `json:"ttfb_ns"`    // nil: no response headers
	EndedAt            int64       `json:"ended_at"`
	Stream             bool        `json:"stream"`
	RequestTruncated   bool        `json:"request_truncated"`
	ResponseTruncated  bool        `json:"response_truncated"`
	RequestIncomplete  bool        `json:"request_incomplete"`
	Truncated          bool        `json:"truncated"`
	ClientDisconnected bool        `json:"client_disconnected"`
	UpstreamAborted    bool        `json:"upstream_aborted"`
	GatewayError       string      `json:"gateway_error"`
	RequestBody        string      `json:"request_body"`  // "": no body
	ResponseBody       string      `json:"response_body"` // "": no body
	Parse              string      `json:"parse"`         // "": not parsed yet
}

// EventRow is one stored canonical event. A NULL column reads as "".
type EventRow struct {
	RequestID     string          `json:"request_id"`
	Seq           int             `json:"seq"`
	Kind          string          `json:"kind"`
	SchemaVersion int             `json:"schema_version"`
	PrincipalID   string          `json:"principal_id"`
	Source        string          `json:"source"`
	Partial       bool            `json:"partial"`
	ContentHash   string          `json:"content_hash"`
	ToolCallID    string          `json:"tool_call_id"`
	Payload       json.RawMessage `json:"payload"`
}

// OpenReader opens the store in dir read-only. It checks that gateway.db exists
// first and never creates it or dir. It opens with mode=ro and never immutable=1,
// which gives torn reads while the gateway writes (research Q9).
func OpenReader(dir string) (*Reader, error) {
	path := filepath.Join(dir, DBName)
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("open store %s: not a regular file", path)
	}
	db, err := sql.Open("sqlite", readerDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}
	r := &Reader{dir: dir, path: path, db: db, dec: dec}
	if err := r.checkSchema(); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("open store %s: %w", path, err)
	}
	return r, nil
}

// readerDSN is the read-only DSN, built through url.URL like the writer's.
func readerDSN(path string) string {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS))
	u := url.URL{Scheme: "file", OmitHost: true, Path: path, RawQuery: q.Encode()}
	return u.String()
}

// checkSchema refuses a database this binary can't read: one never migrated, or
// one migrated by a newer gateway. A reader never migrates.
func (r *Reader) checkSchema() error {
	var version int
	if err := r.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	switch {
	case version > len(migrations):
		return fmt.Errorf("schema newer than gateway (database version %d, gateway knows %d)",
			version, len(migrations))
	case version < len(migrations):
		return fmt.Errorf("schema older than gateway (database version %d, gateway knows %d); "+
			"start the gateway once to migrate it", version, len(migrations))
	}
	return nil
}

// Close closes the reader's connections.
func (r *Reader) Close() error {
	r.dec.Close()
	if err := r.db.Close(); err != nil {
		return fmt.Errorf("close store %s: %w", r.path, err)
	}
	return nil
}

const exchangeColumns = `request_id, principal_id, protocol, client, auth, method, path, query,
	request_headers, response_headers, status, started_at, ttfb_ns, ended_at, stream,
	request_truncated, response_truncated, request_incomplete, truncated,
	client_disconnected, upstream_aborted, gateway_error, request_body, response_body, parse`

// Exchange reads the exchange with request ID id, or ErrNotFound.
func (r *Reader) Exchange(id string) (ExchangeRow, error) {
	row := r.db.QueryRow(`SELECT `+exchangeColumns+` FROM exchanges WHERE request_id = ?`, id)
	ex, err := scanExchange(row)
	if err != nil {
		return ExchangeRow{}, fmt.Errorf("store %s: exchange %s: %w", r.path, id, err)
	}
	return ex, nil
}

// Last reads the exchange that started last, or ErrNotFound on an empty store. Ties
// go to the one stored last.
func (r *Reader) Last() (ExchangeRow, error) {
	row := r.db.QueryRow(`SELECT ` + exchangeColumns +
		` FROM exchanges ORDER BY started_at DESC, rowid DESC LIMIT 1`)
	ex, err := scanExchange(row)
	if err != nil {
		return ExchangeRow{}, fmt.Errorf("store %s: last exchange: %w", r.path, err)
	}
	return ex, nil
}

func scanExchange(row *sql.Row) (ExchangeRow, error) {
	var (
		ex                         ExchangeRow
		reqH, resH                 string
		ttfb                       sql.NullInt64
		gwErr, reqB, resB, parseSt sql.NullString
	)
	err := row.Scan(&ex.RequestID, &ex.PrincipalID, &ex.Protocol, &ex.Client, &ex.Auth,
		&ex.Method, &ex.Path, &ex.Query, &reqH, &resH, &ex.Status, &ex.StartedAt, &ttfb,
		&ex.EndedAt, &ex.Stream, &ex.RequestTruncated, &ex.ResponseTruncated,
		&ex.RequestIncomplete, &ex.Truncated, &ex.ClientDisconnected, &ex.UpstreamAborted,
		&gwErr, &reqB, &resB, &parseSt)
	if errors.Is(err, sql.ErrNoRows) {
		return ExchangeRow{}, ErrNotFound
	}
	if err != nil {
		return ExchangeRow{}, err
	}
	if err := json.Unmarshal([]byte(reqH), &ex.RequestHeaders); err != nil {
		return ExchangeRow{}, fmt.Errorf("request_headers: %w", err)
	}
	if err := json.Unmarshal([]byte(resH), &ex.ResponseHeaders); err != nil {
		return ExchangeRow{}, fmt.Errorf("response_headers: %w", err)
	}
	if ttfb.Valid {
		v := ttfb.Int64
		ex.TTFBNS = &v
	}
	ex.GatewayError, ex.RequestBody, ex.ResponseBody, ex.Parse =
		gwErr.String, reqB.String, resB.String, parseSt.String
	return ex, nil
}

// Content reads the content stored under hash, inline or from its blob,
// decompressed. A hash the store doesn't hold is ErrNotFound.
func (r *Reader) Content(hash string) ([]byte, error) {
	if !validHash(hash) {
		return nil, fmt.Errorf("store %s: content %q: %w", r.path, hash, ErrNotFound)
	}
	var (
		size     int64
		location string
		data     []byte
	)
	err := r.db.QueryRow(`SELECT size, location, data FROM content WHERE hash = ?`, hash).
		Scan(&size, &location, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store %s: content %s: %w", r.path, hash, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store %s: content %s: %w", r.path, hash, err)
	}
	if location == "inline" {
		if data == nil {
			data = []byte{}
		}
		return data, nil
	}
	path := filepath.Join(r.dir, blobDirName, hash[:2], hash[2:])
	compressed, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("blob %s: %w", path, err)
	}
	out, err := r.dec.DecodeAll(compressed, make([]byte, 0, size))
	if err != nil {
		return nil, fmt.Errorf("blob %s: %w", path, err)
	}
	if int64(len(out)) != size {
		return nil, fmt.Errorf("blob %s: %d bytes, the row says %d", path, len(out), size)
	}
	return out, nil
}

// validHash reports whether h has the shape of a content key: sha256 in lowercase
// hex. Anything else can't be in the store, and never reaches a file path.
func validHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for _, c := range h {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Events reads the exchange's canonical events in seq order; an exchange with no
// parse stored has none. An exchange the store doesn't hold is ErrNotFound.
func (r *Reader) Events(id string) ([]EventRow, error) {
	tx, err := r.db.Begin() // one snapshot for the existence check and the events
	if err != nil {
		return nil, fmt.Errorf("store %s: events of %s: %w", r.path, id, err)
	}
	defer func() { _ = tx.Rollback() }()
	var one int
	err = tx.QueryRow(`SELECT 1 FROM exchanges WHERE request_id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store %s: exchange %s: %w", r.path, id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store %s: events of %s: %w", r.path, id, err)
	}
	rows, err := tx.Query(`SELECT request_id, seq, kind, schema_version, principal_id, source,
		partial, content_hash, tool_call_id, payload FROM events WHERE request_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("store %s: events of %s: %w", r.path, id, err)
	}
	defer func() { _ = rows.Close() }()
	var out []EventRow
	for rows.Next() {
		var (
			e                  EventRow
			source, hash, call sql.NullString
			payload            string
		)
		if err := rows.Scan(&e.RequestID, &e.Seq, &e.Kind, &e.SchemaVersion, &e.PrincipalID,
			&source, &e.Partial, &hash, &call, &payload); err != nil {
			return nil, fmt.Errorf("store %s: events of %s: %w", r.path, id, err)
		}
		e.Source, e.ContentHash, e.ToolCallID = source.String, hash.String, call.String
		e.Payload = json.RawMessage(payload)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: events of %s: %w", r.path, id, err)
	}
	return out, nil
}
