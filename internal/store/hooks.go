package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// HookRecord is one hook delivery as the hooks sink hands it over: the body exactly
// as received, the event a profile mapped from it, and who and when. It carries no
// request header, so none can be stored.
type HookRecord struct {
	PrincipalID string
	Client      string
	// Seq is the per-process sequence number, assigned when the delivery is received.
	Seq        int64
	ReceivedAt time.Time
	Payload    []byte // the body, byte-for-byte
	Event      core.HarnessEvent
	// ID is the stored row id, set by SaveHook once the row is written.
	ID int64
}

// SaveHook stores one hook event: the payload (and the tool input, when there is
// one) as content, and an index row. A repeated payload gets its own row; the
// content hash is the same. One transaction, after the blobs are on disk.
func (s *Store) SaveHook(ctx context.Context, h *HookRecord) error {
	items := []content{{hash: hashOf(h.Payload), data: h.Payload}}
	payloadHash := items[0].hash
	var input sql.NullString
	if len(h.Event.ToolInput) > 0 {
		input = sql.NullString{String: hashOf(h.Event.ToolInput), Valid: true}
		items = append(items, content{hash: input.String, data: h.Event.ToolInput})
	}
	e := h.Event
	return s.write(ctx, items, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO hook_events (
			seq, principal_id, client, session_key, tool_use_id, phase, source_name,
			tool_name, agent_id, agent_type, input_hash, payload_hash, received_at, work_dir
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			h.Seq, h.PrincipalID, h.Client, nullString(e.SessionKey), nullString(e.ToolCallID),
			string(e.Phase), e.SourceName, e.ToolName, e.AgentID, e.AgentType,
			input, payloadHash, h.ReceivedAt.UnixNano(), nullString(e.WorkDir))
		if err != nil {
			return fmt.Errorf("insert hook event: %w", err)
		}
		if h.ID, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("insert hook event: %w", err)
		}
		return nil
	})
}
