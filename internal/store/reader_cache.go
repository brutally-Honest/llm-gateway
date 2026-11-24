package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Maxima are the largest row ids the store holds: the exchange rowid, the hook event id
// and the effect id. Ids only grow (v1 deletes nothing), so a lower value than one seen
// before means the store was replaced by an older copy.
type Maxima struct {
	ExchangeRow int64
	HookID      int64
	EffectID    int64
}

// Maxima reads the three largest ids. Each is a primary-key lookup; 0 for an empty table.
func (c *CtxReader) Maxima() (Maxima, error) {
	var m Maxima
	for _, q := range []struct {
		sql string
		to  *int64
	}{
		{`SELECT COALESCE(MAX(rowid), 0) FROM exchanges`, &m.ExchangeRow},
		{`SELECT COALESCE(MAX(id), 0) FROM hook_events`, &m.HookID},
		{`SELECT COALESCE(MAX(id), 0) FROM effects`, &m.EffectID},
	} {
		if err := c.r.db.QueryRowContext(c.ctx, q.sql).Scan(q.to); err != nil {
			return Maxima{}, fmt.Errorf("store %s: maxima: %w", c.r.path, err)
		}
	}
	return m, nil
}

// MaxEffectID is the largest effect id, 0 for none: every effect stored later has a
// larger one.
func (c *CtxReader) MaxEffectID() (int64, error) {
	var n int64
	if err := c.r.db.QueryRowContext(c.ctx, `SELECT COALESCE(MAX(id), 0) FROM effects`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store %s: max effect id: %w", c.r.path, err)
	}
	return n, nil
}

// UnassignedConnAfter reports whether a connection with no session, stored after effect
// id afterID, started in [from, to]. It walks the primary key from afterID on, so it
// reads only what is newer.
func (c *CtxReader) UnassignedConnAfter(afterID, from, to int64) (bool, error) {
	var one int
	err := c.r.db.QueryRowContext(c.ctx, `SELECT EXISTS (SELECT 1 FROM effects
		WHERE id > ? AND session_key IS NULL AND kind = 'net_conn' AND at >= ? AND at <= ?)`,
		afterID, from, to).Scan(&one)
	if err != nil {
		return false, fmt.Errorf("store %s: unassigned connections after: %w", c.r.path, err)
	}
	return one == 1, nil
}

// RowIdentities are the values that name the row at one recorded maximum: the exchange's
// request id at a rowid, the hook event's received_at at an id and the effect's at at an
// id. A restored older store can reuse a rowid or id for a different row, so the number
// alone does not say it is the same history. A field is zero when its id was 0 or when
// the row is missing; the Missing flags tell the two apart.
type RowIdentities struct {
	ExchangeRequestID string
	HookReceivedAt    int64
	EffectAt          int64

	ExchangeMissing bool
	HookMissing     bool
	EffectMissing   bool
}

// RowIdentities reads the identity of the row at each nonzero id: one primary-key lookup
// each. An id of 0 is skipped and reported as present.
func (c *CtxReader) RowIdentities(exRow, hookID, effectID int64) (RowIdentities, error) {
	var out RowIdentities
	for _, q := range []struct {
		id      int64
		sql     string
		scan    func() any
		missing *bool
	}{
		{exRow, `SELECT request_id FROM exchanges WHERE rowid = ?`, func() any { return &out.ExchangeRequestID }, &out.ExchangeMissing},
		{hookID, `SELECT received_at FROM hook_events WHERE id = ?`, func() any { return &out.HookReceivedAt }, &out.HookMissing},
		{effectID, `SELECT at FROM effects WHERE id = ?`, func() any { return &out.EffectAt }, &out.EffectMissing},
	} {
		if q.id == 0 {
			continue
		}
		err := c.r.db.QueryRowContext(c.ctx, q.sql, q.id).Scan(q.scan())
		if errors.Is(err, sql.ErrNoRows) {
			*q.missing = true
			continue
		}
		if err != nil {
			return RowIdentities{}, fmt.Errorf("store %s: row identities: %w", c.r.path, err)
		}
	}
	return out, nil
}
