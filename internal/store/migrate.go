package store

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations are applied in order; migration i brings user_version to i+1. A step
// is never edited once released: a change is a new step.
var migrations = []string{
	// 1: content, exchanges, events (plan, SQLite store).
	`
CREATE TABLE content (
  hash     TEXT PRIMARY KEY,
  size     INTEGER NOT NULL,
  location TEXT NOT NULL CHECK (location IN ('inline', 'blob')),
  data     BLOB,
  CHECK ((location = 'inline') = (data IS NOT NULL))
) WITHOUT ROWID;

CREATE TABLE exchanges (
  request_id          TEXT PRIMARY KEY,
  principal_id        TEXT NOT NULL,
  protocol            TEXT NOT NULL,
  client              TEXT NOT NULL,
  auth                TEXT NOT NULL,
  method              TEXT NOT NULL,
  path                TEXT NOT NULL,
  query               TEXT NOT NULL,
  request_headers     TEXT NOT NULL,
  response_headers    TEXT NOT NULL,
  status              INTEGER NOT NULL,
  started_at          INTEGER NOT NULL,
  ttfb_ns             INTEGER,
  ended_at            INTEGER NOT NULL,
  stream              INTEGER NOT NULL,
  request_truncated   INTEGER NOT NULL,
  response_truncated  INTEGER NOT NULL,
  request_incomplete  INTEGER NOT NULL,
  truncated           INTEGER GENERATED ALWAYS AS (request_truncated OR response_truncated) VIRTUAL,
  client_disconnected INTEGER NOT NULL,
  upstream_aborted    INTEGER NOT NULL,
  gateway_error       TEXT,
  request_body        TEXT REFERENCES content(hash),
  response_body       TEXT REFERENCES content(hash),
  parse               TEXT CHECK (parse IN ('ok','partial','skipped','unsupported_encoding','failed'))
);
CREATE INDEX exchanges_started_at ON exchanges(started_at);
CREATE INDEX exchanges_principal ON exchanges(principal_id, started_at);

CREATE TABLE events (
  request_id     TEXT NOT NULL REFERENCES exchanges(request_id),
  seq            INTEGER NOT NULL,
  kind           TEXT NOT NULL,
  schema_version INTEGER NOT NULL,
  principal_id   TEXT NOT NULL,
  source         TEXT,
  partial        INTEGER NOT NULL,
  content_hash   TEXT REFERENCES content(hash),
  tool_call_id   TEXT,
  payload        TEXT NOT NULL,
  PRIMARY KEY (request_id, seq)
) WITHOUT ROWID;
CREATE INDEX events_content ON events(content_hash);
CREATE INDEX events_tool_call ON events(tool_call_id);
CREATE INDEX events_principal ON events(principal_id, kind);
`,
}

// migrate brings the database to len(migrations) inside one BEGIN IMMEDIATE, so
// two processes opening the same store can't both apply a step. A database newer
// than this binary is refused.
func migrate(ctx context.Context, db *sql.DB) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("migrate: begin: %w", err)
	}
	defer func() {
		if err != nil {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("migrate: read user_version: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("schema newer than gateway (database version %d, gateway knows %d)",
			version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		if _, err := conn.ExecContext(ctx, migrations[i]); err != nil {
			return fmt.Errorf("migrate: step %d: %w", i+1, err)
		}
	}
	if version < len(migrations) {
		// PRAGMA takes no bound parameters; the value is an int this package owns.
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(migrations))); err != nil {
			return fmt.Errorf("migrate: set user_version: %w", err)
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("migrate: commit: %w", err)
	}
	return nil
}
