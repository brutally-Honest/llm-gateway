package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// ContentHash is the key a piece of content is stored under. The effect sensors use it
// to name a copy before it is saved.
func ContentHash(b []byte) string { return hashOf(b) }

// SnapshotRow is one file in a session's last checkpoint. Path "" marks the root
// itself, so a root with no files is kept.
type SnapshotRow struct {
	Root, Path string
	Size       int64
	MtimeNS    int64
	Hash       string // the file's content hash; "" for a root marker
	CopyHash   string // the stored copy's hash; "" when there is none
}

// Checkpoint is what one checkpoint saves: its effects, the copies they point at, and
// the snapshot rows that changed.
type Checkpoint struct {
	Effects []core.Effect
	Copies  [][]byte // stored as content; an effect's AfterHash or BeforeHash names one
	Upserts []SnapshotRow
	Deletes []SnapshotRow // only Root and Path are read
}

// SaveEffects stores effects, and copies as content, in one transaction.
func (s *Store) SaveEffects(ctx context.Context, effects []core.Effect, copies [][]byte) error {
	return s.SaveCheckpoint(ctx, SessionIdent{}, Checkpoint{Effects: effects, Copies: copies})
}

// SaveCheckpoint stores a checkpoint's effects, its copies and its snapshot rows for
// session in one transaction, so a crash saves either all of it or none (spec 014 AC2).
func (s *Store) SaveCheckpoint(ctx context.Context, session SessionIdent, cp Checkpoint) error {
	items := make([]content, 0, len(cp.Copies))
	for _, c := range cp.Copies {
		items = append(items, content{hash: hashOf(c), data: c})
	}
	return s.write(ctx, items, func(tx *sql.Tx) error {
		if err := insertEffects(ctx, tx, cp.Effects); err != nil {
			return err
		}
		for _, r := range cp.Upserts {
			_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO effect_snapshots
				(principal_id, client, session_key, root, path, size, mtime_ns, hash, copy_hash)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				session.Principal, session.Client, session.Key, r.Root, r.Path, r.Size, r.MtimeNS,
				nullString(r.Hash), nullString(r.CopyHash))
			if err != nil {
				return fmt.Errorf("upsert snapshot row: %w", err)
			}
		}
		for _, r := range cp.Deletes {
			_, err := tx.ExecContext(ctx, `DELETE FROM effect_snapshots
				WHERE principal_id = ? AND client = ? AND session_key = ? AND root = ? AND path = ?`,
				session.Principal, session.Client, session.Key, r.Root, r.Path)
			if err != nil {
				return fmt.Errorf("delete snapshot row: %w", err)
			}
		}
		return nil
	})
}

func insertEffects(ctx context.Context, tx *sql.Tx, effects []core.Effect) error {
	for _, e := range effects {
		var ended sql.NullInt64
		if !e.EndedAt.IsZero() {
			ended = sql.NullInt64{Int64: e.EndedAt.UnixNano(), Valid: true}
		}
		var hookID, port sql.NullInt64
		if e.HookID != 0 {
			hookID = sql.NullInt64{Int64: e.HookID, Valid: true}
		}
		if e.Port != 0 {
			port = sql.NullInt64{Int64: int64(e.Port), Valid: true}
		}
		var size, up, down sql.NullInt64
		switch e.Kind {
		case core.EffectFileChange:
			size = sql.NullInt64{Int64: e.Size, Valid: true}
		case core.EffectNetConn:
			up = sql.NullInt64{Int64: e.BytesUp, Valid: true}
			down = sql.NullInt64{Int64: e.BytesDown, Valid: true}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO effects (
			principal_id, client, session_key, kind, at, root, path, change, size, mode,
			after_hash, before_hash, hook_id, detail, host, port, via, bytes_up, bytes_down,
			ended_at, outcome
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.Principal, e.Client, nullString(e.SessionKey), string(e.Kind), e.At.UnixNano(),
			nullString(e.Root), nullString(e.Path), nullString(string(e.Change)), size, nullString(e.Mode),
			nullString(e.AfterHash), nullString(e.BeforeHash), hookID, nullString(e.Detail),
			nullString(e.Host), port, nullString(e.Via), up, down, ended, nullString(string(e.Outcome)))
		if err != nil {
			return fmt.Errorf("insert effect: %w", err)
		}
	}
	return nil
}

// LoadSnapshot returns a session's saved checkpoint rows; none when it has no snapshot.
func (s *Store) LoadSnapshot(ctx context.Context, session SessionIdent) ([]SnapshotRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT root, path, COALESCE(size, 0), COALESCE(mtime_ns, 0),
		COALESCE(hash, ''), COALESCE(copy_hash, '') FROM effect_snapshots
		WHERE principal_id = ? AND client = ? AND session_key = ?`,
		session.Principal, session.Client, session.Key)
	if err != nil {
		return nil, fmt.Errorf("store %s: load snapshot: %w", s.path, err)
	}
	defer func() { _ = rows.Close() }()
	var out []SnapshotRow
	for rows.Next() {
		var r SnapshotRow
		if err := rows.Scan(&r.Root, &r.Path, &r.Size, &r.MtimeNS, &r.Hash, &r.CopyHash); err != nil {
			return nil, fmt.Errorf("store %s: load snapshot: %w", s.path, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: load snapshot: %w", s.path, err)
	}
	return out, nil
}

// DeleteSnapshot drops a session's saved checkpoint.
func (s *Store) DeleteSnapshot(ctx context.Context, session SessionIdent) error {
	return s.write(ctx, nil, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM effect_snapshots
			WHERE principal_id = ? AND client = ? AND session_key = ?`,
			session.Principal, session.Client, session.Key)
		return err
	})
}

// SweepSnapshots drops the saved checkpoint of every session with no hook event at or
// after cutoff, and returns how many rows went.
func (s *Store) SweepSnapshots(ctx context.Context, cutoff time.Time) (int64, error) {
	var n int64
	err := s.write(ctx, nil, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM effect_snapshots WHERE NOT EXISTS (
			SELECT 1 FROM hook_events h WHERE h.principal_id = effect_snapshots.principal_id
			AND h.client = effect_snapshots.client AND h.session_key = effect_snapshots.session_key
			AND h.received_at >= ?)`, cutoff.UnixNano())
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return n, err
}

// ActiveSession is a session with a recent hook event and no session end since.
type ActiveSession struct {
	SessionIdent
	LastHook time.Time
}

// ActiveSessions returns the sessions with a hook event at or after since and no
// session-end event at or after it.
func (s *Store) ActiveSessions(ctx context.Context, since time.Time) ([]ActiveSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT principal_id, client, session_key, MAX(received_at)
		FROM hook_events WHERE session_key IS NOT NULL AND received_at >= ?
		GROUP BY principal_id, client, session_key
		HAVING SUM(phase = ?) = 0`, since.UnixNano(), string(core.PhaseSessionEnd))
	if err != nil {
		return nil, fmt.Errorf("store %s: active sessions: %w", s.path, err)
	}
	defer func() { _ = rows.Close() }()
	var out []ActiveSession
	for rows.Next() {
		var a ActiveSession
		var last int64
		if err := rows.Scan(&a.Principal, &a.Client, &a.Key, &last); err != nil {
			return nil, fmt.Errorf("store %s: active sessions: %w", s.path, err)
		}
		a.LastHook = time.Unix(0, last)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: active sessions: %w", s.path, err)
	}
	return out, nil
}

// EffectRow is one stored effect. A NULL column reads as zero. HookPayload is the
// content hash of the hook event a read effect points at.
type EffectRow struct {
	ID          int64
	Principal   string
	Client      string
	SessionKey  string
	Kind        string
	At          int64 // unix nanoseconds, like hook_events.received_at
	Root        string
	Path        string
	Change      string
	Size        int64
	Mode        string
	AfterHash   string
	BeforeHash  string
	HookID      int64
	HookPayload string
	Detail      string
	Host        string
	Port        int
	Via         string
	BytesUp     int64
	BytesDown   int64
	EndedAt     int64
	Outcome     string
}

const effectSelect = `SELECT e.id, e.principal_id, e.client, COALESCE(e.session_key, ''), e.kind, e.at,
	COALESCE(e.root, ''), COALESCE(e.path, ''), COALESCE(e.change, ''), COALESCE(e.size, 0),
	COALESCE(e.mode, ''), COALESCE(e.after_hash, ''), COALESCE(e.before_hash, ''),
	COALESCE(e.hook_id, 0), COALESCE(h.payload_hash, ''), COALESCE(e.detail, ''),
	COALESCE(e.host, ''), COALESCE(e.port, 0), COALESCE(e.via, ''), COALESCE(e.bytes_up, 0),
	COALESCE(e.bytes_down, 0), COALESCE(e.ended_at, 0), COALESCE(e.outcome, '')
FROM effects e LEFT JOIN hook_events h ON h.id = e.hook_id`

func (r *Reader) effects(ctx context.Context, q string, args ...any) ([]EffectRow, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store %s: effects: %w", r.path, err)
	}
	defer func() { _ = rows.Close() }()
	out := []EffectRow{}
	for rows.Next() {
		var e EffectRow
		if err := rows.Scan(&e.ID, &e.Principal, &e.Client, &e.SessionKey, &e.Kind, &e.At, &e.Root, &e.Path,
			&e.Change, &e.Size, &e.Mode, &e.AfterHash, &e.BeforeHash, &e.HookID, &e.HookPayload, &e.Detail,
			&e.Host, &e.Port, &e.Via, &e.BytesUp, &e.BytesDown, &e.EndedAt, &e.Outcome); err != nil {
			return nil, fmt.Errorf("store %s: effects: %w", r.path, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: effects: %w", r.path, err)
	}
	return out, nil
}

// ListEffects lists up to limit of a session's effects with an id above afterID, in id
// order.
func (c *CtxReader) ListEffects(session SessionIdent, afterID int64, limit int) ([]EffectRow, error) {
	return c.r.effects(c.ctx, effectSelect+` WHERE e.principal_id = ? AND e.client = ? AND e.session_key = ?
		AND e.id > ? ORDER BY e.id LIMIT ?`, session.Principal, session.Client, session.Key, afterID, limit)
}

// ListUnassigned lists up to limit effects with no session, id above afterID, in id
// order.
func (c *CtxReader) ListUnassigned(afterID int64, limit int) ([]EffectRow, error) {
	return c.r.effects(c.ctx, effectSelect+` WHERE e.session_key IS NULL AND e.id > ? ORDER BY e.id LIMIT ?`,
		afterID, limit)
}

// Effect reads one effect by id; a missing one is ErrNotFound.
func (c *CtxReader) Effect(id int64) (EffectRow, error) {
	rows, err := c.r.effects(c.ctx, effectSelect+` WHERE e.id = ?`, id)
	if err != nil {
		return EffectRow{}, err
	}
	if len(rows) == 0 {
		return EffectRow{}, fmt.Errorf("store %s: effect %d: %w", c.r.path, id, ErrNotFound)
	}
	return rows[0], nil
}

// UnassignedConnections counts network effects with no session.
func (c *CtxReader) UnassignedConnections() (int, error) {
	var n int
	err := c.r.db.QueryRowContext(c.ctx, `SELECT COUNT(*) FROM effects
		WHERE session_key IS NULL AND kind = 'net_conn'`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store %s: unassigned connections: %w", c.r.path, err)
	}
	return n, nil
}

// EffectVersion moves on every effect stored for a session. Comparable.
type EffectVersion struct {
	Count int
	MaxID int64
}

const effectVersionTail = `) SELECT e.principal_id, e.client, e.session_key, COUNT(*), MAX(e.id)
FROM l JOIN effects e ON e.principal_id = l.p AND e.client = l.c AND e.session_key = l.k
GROUP BY e.principal_id, e.client, e.session_key`

// EffectCount is a session's effect counts for a list page: distinct changed files,
// distinct read paths and distinct sites.
type EffectCount struct {
	Changed, Read, Sites int
}

const effectCountsTail = `) SELECT e.principal_id, e.client, e.session_key,
	COUNT(DISTINCT CASE WHEN e.kind = 'file_change' THEN e.path END),
	COUNT(DISTINCT CASE WHEN e.kind = 'file_read' THEN e.path END),
	COUNT(DISTINCT CASE WHEN e.kind = 'net_conn' THEN e.host END)
FROM l JOIN effects e ON e.principal_id = l.p AND e.client = l.c AND e.session_key = l.k
GROUP BY e.principal_id, e.client, e.session_key`

// sessionArgs is the bound values of a version query's session list.
func sessionArgs(ids []SessionIdent) []any {
	args := make([]any, 0, 3*len(ids))
	for _, id := range ids {
		args = append(args, id.Principal, id.Client, id.Key)
	}
	return args
}

// EffectVersions reads the effect version of each requested session in one grouped
// query; every requested session has an entry. SessionVersions does not read effects.
func (c *CtxReader) EffectVersions(ids []SessionIdent) (map[SessionIdent]EffectVersion, error) {
	out := make(map[SessionIdent]EffectVersion, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	for _, id := range ids {
		out[id] = EffectVersion{}
	}
	rows, err := c.r.db.QueryContext(c.ctx, versionSQL(effectVersionTail, len(ids)), sessionArgs(ids)...)
	if err != nil {
		return nil, fmt.Errorf("store %s: effect versions: %w", c.r.path, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id SessionIdent
		var v EffectVersion
		if err := rows.Scan(&id.Principal, &id.Client, &id.Key, &v.Count, &v.MaxID); err != nil {
			return nil, fmt.Errorf("store %s: effect versions: %w", c.r.path, err)
		}
		out[id] = v
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: effect versions: %w", c.r.path, err)
	}
	return out, nil
}

// EffectCounts reads the effect counts of each requested session in one grouped
// query; a session with no effects has no entry.
func (c *CtxReader) EffectCounts(ids []SessionIdent) (map[SessionIdent]EffectCount, error) {
	out := make(map[SessionIdent]EffectCount, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := c.r.db.QueryContext(c.ctx, versionSQL(effectCountsTail, len(ids)), sessionArgs(ids)...)
	if err != nil {
		return nil, fmt.Errorf("store %s: effect counts: %w", c.r.path, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id SessionIdent
		var n EffectCount
		if err := rows.Scan(&id.Principal, &id.Client, &id.Key, &n.Changed, &n.Read, &n.Sites); err != nil {
			return nil, fmt.Errorf("store %s: effect counts: %w", c.r.path, err)
		}
		out[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store %s: effect counts: %w", c.r.path, err)
	}
	return out, nil
}
