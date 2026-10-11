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
	Usage   Summary
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
		u, err := sessionUsage(src, s)
		if err != nil {
			return nil, err
		}
		out = append(out, SessionListing{SessionRow: s, Preview: p, Usage: u})
	}
	return out, nil
}

// UngroupedSource is what ListUngrouped reads. *store.Reader satisfies it.
type UngroupedSource interface {
	Events(requestID string) ([]store.EventRow, error)
	UngroupedExchanges(limit int) ([]store.ExchangeRow, error)
}

// ListUngrouped lists the newest exchanges with no session key (limit 0 lists all)
// and the usage Summary of all of them, whatever the limit. exchanges is how many
// ungrouped exchanges the Summary covers.
func ListUngrouped(src UngroupedSource, limit int) (rows []UngroupedExchange, sum Summary, exchanges int, err error) {
	all, err := src.UngroupedExchanges(0)
	if err != nil {
		return nil, Summary{}, 0, fmt.Errorf("timeline: ungrouped: %w", err)
	}
	var calls []CallUsage
	rows = []UngroupedExchange{}
	for i, ex := range all {
		evs, err := src.Events(ex.RequestID)
		if err != nil {
			return nil, Summary{}, 0, fmt.Errorf("timeline: events of %s: %w", ex.RequestID, err)
		}
		c, ok := callUsage(ex, evs)
		var call *CallUsage
		if ok {
			calls = append(calls, c)
			call = &c
		}
		if limit == 0 || i < limit {
			rows = append(rows, UngroupedExchange{RequestID: ex.RequestID, Client: ex.Client,
				Method: ex.Method, Path: ex.Path, Start: ex.StartedAt, Status: ex.Status, Call: call})
		}
	}
	return rows, Summarize(calls), len(all), nil
}

// sessionUsage summarises the model calls of a session. Every exchange's events are
// read, folded ones too, since a failed parse may still hold a request event.
func sessionUsage(src Source, s store.SessionRow) (Summary, error) {
	exchanges, err := src.SessionExchanges(s.PrincipalID, s.Client, s.SessionKey)
	if err != nil {
		return Summary{}, fmt.Errorf("timeline: session exchanges: %w", err)
	}
	var calls []CallUsage
	for _, ex := range exchanges {
		evs, err := src.Events(ex.RequestID)
		if err != nil {
			return Summary{}, fmt.Errorf("timeline: events of %s: %w", ex.RequestID, err)
		}
		if c, ok := callUsage(ex, evs); ok {
			calls = append(calls, c)
		}
	}
	return Summarize(calls), nil
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
