// Package timeline turns one session's stored exchanges and events into a list of
// entries a viewer can print. It reads only canonical events and content, so it
// names no provider and no client.
package timeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// DefaultEntryLimit is the most bytes of one text, tool input or tool result that an
// entry keeps unless Options.Full is set.
const DefaultEntryLimit = 2000

// NotStored is the text of a block whose content the store does not hold.
const NotStored = "[content not stored]"

// Source is what Build reads. *store.Reader satisfies it.
type Source interface {
	SessionExchanges(principal, client, key string) ([]store.ExchangeRow, error)
	Events(requestID string) ([]store.EventRow, error)
	Content(hash string) ([]byte, error)
}

// Options changes how Build cuts entries.
type Options struct {
	// Full keeps every entry whole instead of cutting at DefaultEntryLimit.
	Full bool
}

// Kind names an entry.
type Kind string

// The entry kinds.
const (
	KindExchange   Kind = "exchange"    // header of an exchange that is shown in full
	KindFolded     Kind = "folded"      // one line for an exchange with nothing to show
	KindMarker     Kind = "marker"      // "history diverged"
	KindMessage    Kind = "message"     // Role and Text
	KindToolCall   Kind = "tool_call"   // Name, ToolCallID and Text (the input)
	KindToolResult Kind = "tool_result" // ToolCallID, IsError and Text
	KindError      Kind = "error"       // Status and Text
)

// MarkerDiverged is the text of the marker that precedes a rewritten history.
const MarkerDiverged = "history diverged"

// Entry is one line of a timeline. Which fields are set depends on Kind; RequestID
// and Start are always set.
type Entry struct {
	Kind      Kind
	RequestID string
	Start     int64 // unix nanoseconds of the exchange's start

	Method, Path string
	Status       int
	Parse        string

	Role       string
	Name       string
	ToolCallID string
	IsError    bool
	Text       string
	// Omitted is how many bytes of Text were cut off; 0 when nothing was.
	Omitted int
	// Partial marks an entry built from an exchange or event that is incomplete.
	Partial bool
	// Call is the usage of the model call this exchange or folded entry stands for;
	// nil for any other entry.
	Call *CallUsage
}

type thread struct {
	hashes []string
}

// Build lists a session's entries: exchanges by start time then request id, each
// reduced to what it adds to the best-matching earlier exchange. A source error is
// returned; missing content is shown as NotStored.
func Build(src Source, session store.SessionRow, opts Options) ([]Entry, error) {
	exchanges, err := src.SessionExchanges(session.PrincipalID, session.Client, session.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("timeline: session exchanges: %w", err)
	}
	var (
		out     []Entry
		earlier []thread
	)
	for _, ex := range exchanges {
		evs, err := loadEvents(src, ex)
		if err != nil {
			return nil, err
		}
		hashes := messageHashes(evs)
		if len(hashes) == 0 || foldedParse(ex.Parse) {
			fe := folded(ex)
			// A folded-parse exchange may still be a model call; its events are
			// read only to find the request event.
			callEvs := evs
			if foldedParse(ex.Parse) {
				if callEvs, err = src.Events(ex.RequestID); err != nil {
					return nil, fmt.Errorf("timeline: events of %s: %w", ex.RequestID, err)
				}
			}
			fe.Call = callOf(ex, callEvs)
			out = append(out, fe)
			continue
		}
		lcp, best := bestMatch(earlier, hashes)
		earlier = append(earlier, thread{hashes: hashes})
		entries, err := exchangeEntries(src, ex, evs, lcp, best, opts)
		if err != nil {
			return nil, err
		}
		entries[0].Call = callOf(ex, evs)
		out = append(out, entries...)
	}
	return out, nil
}

func callOf(ex store.ExchangeRow, evs []store.EventRow) *CallUsage {
	c, ok := callUsage(ex, evs)
	if !ok {
		return nil
	}
	return &c
}

func foldedParse(p string) bool {
	return p == "failed" || p == "skipped" || p == "unsupported_encoding"
}

func loadEvents(src Source, ex store.ExchangeRow) ([]store.EventRow, error) {
	if foldedParse(ex.Parse) {
		return nil, nil
	}
	evs, err := src.Events(ex.RequestID)
	if err != nil {
		return nil, fmt.Errorf("timeline: events of %s: %w", ex.RequestID, err)
	}
	return evs, nil
}

// messageHashes are the content hashes of the message events in seq order. A message
// without a hash gets a value no other message can equal.
func messageHashes(evs []store.EventRow) []string {
	var out []string
	for _, e := range evs {
		if e.Kind != "message" {
			continue
		}
		h := e.ContentHash
		if h == "" {
			h = fmt.Sprintf("\x00%s/%d", e.RequestID, e.Seq)
		}
		out = append(out, h)
	}
	return out
}

func folded(ex store.ExchangeRow) Entry {
	return Entry{Kind: KindFolded, RequestID: ex.RequestID, Start: ex.StartedAt,
		Method: ex.Method, Path: ex.Path, Status: ex.Status, Parse: ex.Parse}
}

func lcp(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// bestMatch picks the earlier thread sharing the longest leading run with hashes.
// Ties go to a thread whose own list equals the shared run, then to the latest. A
// run of 0 means a new thread, and best is then nil.
func bestMatch(earlier []thread, hashes []string) (int, *thread) {
	var (
		best      *thread
		bestLCP   int
		bestExact bool
	)
	for i := range earlier {
		n := lcp(earlier[i].hashes, hashes)
		if n == 0 {
			continue
		}
		exact := len(earlier[i].hashes) == n
		if n > bestLCP || (n == bestLCP && (exact || !bestExact)) {
			best, bestLCP, bestExact = &earlier[i], n, exact
		}
	}
	return bestLCP, best
}

type messagePayload struct {
	Role   string `json:"role"`
	Blocks []struct {
		Hash string `json:"hash"`
		Type string `json:"type"`
	} `json:"blocks"`
}

type toolCallPayload struct {
	Name string `json:"name"`
}

type toolResultPayload struct {
	IsError bool `json:"is_error"`
}

type errorPayload struct {
	Status  int    `json:"status"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

func exchangeEntries(src Source, ex store.ExchangeRow, evs []store.EventRow, shared int, best *thread, opts Options) ([]Entry, error) {
	partialEx := ex.Parse == "partial"
	out := []Entry{{Kind: KindExchange, RequestID: ex.RequestID, Start: ex.StartedAt,
		Method: ex.Method, Path: ex.Path, Status: ex.Status, Parse: ex.Parse, Partial: partialEx}}
	if best != nil && len(best.hashes) > shared {
		out = append(out, Entry{Kind: KindMarker, RequestID: ex.RequestID, Start: ex.StartedAt,
			Text: MarkerDiverged, Partial: partialEx})
	}
	base := Entry{RequestID: ex.RequestID, Start: ex.StartedAt}
	idx := -1 // index of the latest message event
	for _, e := range evs {
		en := base
		en.Partial = partialEx || e.Partial
		var err error
		switch e.Kind {
		case "message":
			idx++
			if idx < shared {
				continue
			}
			en.Kind = KindMessage
			err = fillMessage(src, e, &en)
		case "tool_call":
			if idx >= 0 && idx < shared {
				continue
			}
			en.Kind, en.ToolCallID = KindToolCall, e.ToolCallID
			var p toolCallPayload
			_ = json.Unmarshal(e.Payload, &p)
			en.Name = p.Name
			en.Text, err = rawContent(src, e.ContentHash)
		case "tool_result":
			if idx >= 0 && idx < shared {
				continue
			}
			en.Kind, en.ToolCallID = KindToolResult, e.ToolCallID
			var p toolResultPayload
			_ = json.Unmarshal(e.Payload, &p)
			en.IsError = p.IsError
			en.Text, err = rawContent(src, e.ContentHash)
		case "error":
			en.Kind = KindError
			var p errorPayload
			_ = json.Unmarshal(e.Payload, &p)
			en.Status = p.Status
			en.Name = p.Type
			en.Text = p.Message
		default: // request and usage are not part of the timeline
			continue
		}
		if err != nil {
			return nil, err
		}
		if !opts.Full {
			en.Text, en.Omitted = cut(en.Text, DefaultEntryLimit)
		}
		out = append(out, en)
	}
	return out, nil
}

func fillMessage(src Source, e store.EventRow, en *Entry) error {
	var p messagePayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("timeline: message %s/%d: %w", e.RequestID, e.Seq, err)
	}
	en.Role = p.Role
	var text string
	for _, b := range p.Blocks {
		var part string
		switch b.Type {
		case "text":
			raw, err := content(src, b.Hash)
			if err != nil {
				return err
			}
			part = blockText(raw)
		case "reasoning", "media", "unknown":
			part = "[" + b.Type + "]"
		default: // tool blocks show as their own tool events
			continue
		}
		if text != "" {
			text += "\n"
		}
		text += part
	}
	en.Text = text
	return nil
}

// content reads stored content; absent content is nil, not an error.
func content(src Source, hash string) ([]byte, error) {
	if hash == "" {
		return nil, nil
	}
	b, err := src.Content(hash)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("timeline: content %s: %w", hash, err)
	}
	return b, nil
}

func rawContent(src Source, hash string) (string, error) {
	b, err := content(src, hash)
	if err != nil {
		return "", err
	}
	if b == nil {
		return NotStored, nil
	}
	return string(b), nil
}

// blockText is the block's "text" string, else the block as stored.
func blockText(raw []byte) string {
	if raw == nil {
		return NotStored
	}
	var t struct {
		Text *string `json:"text"`
	}
	if json.Unmarshal(raw, &t) == nil && t.Text != nil {
		return *t.Text
	}
	return string(raw)
}

// cut shortens s to at most limit bytes at a rune boundary and says how many bytes
// it dropped.
func cut(s string, limit int) (string, int) {
	if len(s) <= limit {
		return s, 0
	}
	i := limit
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i], len(s) - i
}
