package timeline

import (
	"encoding/json"
	"fmt"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// PreviewRunes is the most runes of the first user prompt a session row shows.
const PreviewRunes = 80

// SessionSource is what ListSessions reads. *store.Reader satisfies it.
type SessionSource interface {
	Source
	SessionRows(limit int) ([]store.SessionRow, error)
}

// SessionListing is a session row with a preview of its first user prompt. Preview
// is empty when the session holds no user message (a side call only).
type SessionListing struct {
	store.SessionRow
	Preview string
}

// ListSessions lists sessions, newest last activity first; limit 0 lists all.
func ListSessions(src SessionSource, limit int) ([]SessionListing, error) {
	rows, err := src.SessionRows(limit)
	if err != nil {
		return nil, fmt.Errorf("timeline: sessions: %w", err)
	}
	out := make([]SessionListing, 0, len(rows))
	for _, s := range rows {
		p, err := preview(src, s)
		if err != nil {
			return nil, err
		}
		out = append(out, SessionListing{SessionRow: s, Preview: p})
	}
	return out, nil
}

// preview is the first text of the first user message in the session's exchanges,
// taken in order, cut to PreviewRunes runes.
func preview(src Source, s store.SessionRow) (string, error) {
	exchanges, err := src.SessionExchanges(s.PrincipalID, s.Client, s.SessionKey)
	if err != nil {
		return "", fmt.Errorf("timeline: session exchanges: %w", err)
	}
	for _, ex := range exchanges {
		evs, err := loadEvents(src, ex)
		if err != nil {
			return "", err
		}
		for _, e := range evs {
			if e.Kind != "message" {
				continue
			}
			text, ok, err := firstUserText(src, e)
			if err != nil {
				return "", err
			}
			if ok {
				return cutRunes(text, PreviewRunes), nil
			}
		}
	}
	return "", nil
}

func firstUserText(src Source, e store.EventRow) (string, bool, error) {
	var p messagePayload
	if err := json.Unmarshal(e.Payload, &p); err != nil || p.Role != "user" {
		return "", false, nil
	}
	for _, b := range p.Blocks {
		if b.Type != "text" {
			continue
		}
		raw, err := content(src, b.Hash)
		if err != nil {
			return "", false, err
		}
		if raw == nil {
			continue
		}
		if t := blockText(raw); t != "" {
			return t, true, nil
		}
	}
	return "", false, nil
}

func cutRunes(s string, n int) string {
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}
