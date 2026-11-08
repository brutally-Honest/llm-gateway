package store

import (
	"fmt"
	"strings"
)

// SessionIdent is the identity of a session: principal, client and key.
type SessionIdent struct {
	Principal, Client, Key string
}

// Version summarises what a session holds in aggregates that move on every write
// that can change its digest: an exchange insert (ExCount, ExMaxRow), a parse landing
// (Parsed), a backfilled session key (ExCount, maybe ExMaxRow) and a hook insert
// (HookCount, HookMaxID). A half the session lacks reads as zero. Comparable.
type Version struct {
	ExCount   int
	ExMaxRow  int64
	Parsed    int
	HookCount int
	HookMaxID int64
}

// The version queries, shared with the query-plan test. Each is versionPrefix,
// one "(?,?,?)" per requested session, then its tail. They join on the full identity
// and group on it, never on the key alone.
const (
	versionPrefix = `WITH l(p,c,k) AS (VALUES `

	exchangeVersionTail = `) SELECT e.principal_id, e.client, e.session_key, COUNT(*), MAX(e.rowid), COUNT(e.parse)
FROM l JOIN exchanges e ON e.principal_id = l.p AND e.client = l.c AND e.session_key = l.k
GROUP BY e.principal_id, e.client, e.session_key`

	hookVersionTail = `) SELECT h.principal_id, h.client, h.session_key, COUNT(*), MAX(h.id)
FROM l JOIN hook_events h ON h.principal_id = l.p AND h.client = l.c AND h.session_key = l.k
GROUP BY h.principal_id, h.client, h.session_key`
)

func versionSQL(tail string, n int) string {
	return versionPrefix + strings.TrimSuffix(strings.Repeat("(?,?,?),", n), ",") + tail
}

// SessionVersions reads the version of each requested session in one transaction
// and two grouped queries. Every requested session has an entry.
func (c *CtxReader) SessionVersions(ids []SessionIdent) (map[SessionIdent]Version, error) {
	out := make(map[SessionIdent]Version, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, 0, 3*len(ids))
	for _, id := range ids {
		args = append(args, id.Principal, id.Client, id.Key)
		out[id] = Version{}
	}
	tx, err := c.r.db.BeginTx(c.ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store %s: session versions: %w", c.r.path, err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(c.ctx, versionSQL(exchangeVersionTail, len(ids)), args...)
	if err != nil {
		return nil, fmt.Errorf("store %s: session versions: %w", c.r.path, err)
	}
	for rows.Next() {
		var id SessionIdent
		var v Version
		if err := rows.Scan(&id.Principal, &id.Client, &id.Key, &v.ExCount, &v.ExMaxRow, &v.Parsed); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store %s: session versions: %w", c.r.path, err)
		}
		out[id] = v
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("store %s: session versions: %w", c.r.path, err)
	}
	_ = rows.Close()

	rows, err = tx.QueryContext(c.ctx, versionSQL(hookVersionTail, len(ids)), args...)
	if err != nil {
		return nil, fmt.Errorf("store %s: session versions: %w", c.r.path, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id SessionIdent
		var n int
		var max int64
		if err := rows.Scan(&id.Principal, &id.Client, &id.Key, &n, &max); err != nil {
			return nil, fmt.Errorf("store %s: session versions: %w", c.r.path, err)
		}
		v := out[id]
		v.HookCount, v.HookMaxID = n, max
		out[id] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: session versions: %w", c.r.path, err)
	}
	return out, nil
}
