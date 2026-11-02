package timeline

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// DigestRules is the client-specific knowledge a Digest needs. The timeline names no
// client, so the caller supplies it; a nil DigestRules means no side calls, prompts
// shown whole and no generated titles.
type DigestRules interface {
	// IsSideCall reports whether a request's user message texts mark a side call.
	IsSideCall(userTexts []string) bool
	// StripReminders removes injected reminder blocks from a prompt.
	StripReminders(text string) string
	// TitleOf extracts a generated title from a response; false when unusable.
	TitleOf(responseText string) (string, bool)
}

// Digest is what a session list row shows beyond SessionStats.
type Digest struct {
	// Title is the latest generated title, else the first prompt outside side calls
	// with reminders removed, cut to PreviewRunes; "" when there is neither.
	Title string
	// ToolStates counts tool calls (and hook-seen calls) per chip; every chip in Chips
	// is present.
	ToolStates map[string]int
	// MainErrors counts error events outside side calls, SideErrors those inside them.
	// Together they equal SessionStats' Errors.
	MainErrors, SideErrors int
	// HasCalls is true when the session holds a stored exchange, HasHooks when it
	// holds a hook event.
	HasCalls, HasHooks bool
}

// SessionDigest summarises a session in one pass over its exchanges. Content is read
// only for user prompts and for the response of side calls.
func SessionDigest(src Source, session store.SessionRow, rules DigestRules) (Digest, error) {
	exchanges, err := src.SessionExchanges(session.PrincipalID, session.Client, session.SessionKey)
	if err != nil {
		return Digest{}, fmt.Errorf("timeline: session exchanges: %w", err)
	}
	d := Digest{ToolStates: map[string]int{}, HasCalls: len(exchanges) > 0}
	for _, s := range Chips {
		d.ToolStates[s] = 0
	}
	var generated, firstPrompt string
	for _, ex := range exchanges {
		evs, err := loadEvents(src, ex)
		if err != nil {
			return Digest{}, err
		}
		if len(messageHashes(evs)) == 0 || foldedParse(ex.Parse) {
			continue // SessionStats counts nothing from these either
		}
		var userTexts []string
		var errs int
		var failed bool
		var last *store.EventRow
		for i, e := range evs {
			switch e.Kind {
			case "error":
				errs++
				failed = true
			case "message":
				last = &evs[i]
				if t, ok, err := userText(src, e); err != nil {
					return Digest{}, err
				} else if ok {
					userTexts = append(userTexts, t)
				}
			}
		}
		side := rules != nil && rules.IsSideCall(userTexts)
		if side {
			d.SideErrors += errs
			if title, ok, err := generatedTitle(src, rules, last, failed || ex.Status >= 400); err != nil {
				return Digest{}, err
			} else if ok {
				generated = title
			}
			continue
		}
		d.MainErrors += errs
		if firstPrompt == "" {
			for _, t := range userTexts {
				if rules != nil {
					t = rules.StripReminders(t)
				}
				if t = strings.TrimSpace(t); t != "" {
					firstPrompt = t
					break
				}
			}
		}
	}
	d.Title = cutRunes(firstPrompt, PreviewRunes)
	if generated != "" {
		d.Title = cutRunes(generated, PreviewRunes)
	}

	entries, err := Build(src, session, Options{Cut: 1})
	if err != nil {
		return Digest{}, err
	}
	hookCalls := map[string]bool{}
	for _, e := range entries {
		switch {
		case e.Kind == KindToolCall && e.Chip != "":
			d.ToolStates[e.Chip]++
		case e.Kind == KindHarness && e.Chip != "" && !hookCalls[e.ToolCallID]:
			hookCalls[e.ToolCallID] = true // one chip per tool call, not per hook event
			d.ToolStates[e.Chip]++
		}
	}
	if session.SessionKey != "" {
		hooks, err := src.HookEvents(session.PrincipalID, session.Client, session.SessionKey)
		if err != nil {
			return Digest{}, fmt.Errorf("timeline: hook events: %w", err)
		}
		d.HasHooks = len(hooks) > 0
	}
	return d, nil
}

// textOf joins the text blocks of a message; ok is false when it has none stored.
func textOf(src Source, e store.EventRow, role string) (string, bool, error) {
	var p messagePayload
	if err := json.Unmarshal(e.Payload, &p); err != nil || p.Role != role {
		return "", false, nil
	}
	var parts []string
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
		parts = append(parts, blockText(raw))
	}
	return strings.Join(parts, "\n"), len(parts) > 0, nil
}

func userText(src Source, e store.EventRow) (string, bool, error) { return textOf(src, e, "user") }

// generatedTitle reads the reply of a side call: its last message when that is the
// assistant's. A failed call has no usable title.
func generatedTitle(src Source, rules DigestRules, last *store.EventRow, failed bool) (string, bool, error) {
	if failed || last == nil {
		return "", false, nil
	}
	text, ok, err := textOf(src, *last, "assistant")
	if err != nil || !ok {
		return "", false, err
	}
	title, ok := rules.TitleOf(text)
	return strings.TrimSpace(title), ok && strings.TrimSpace(title) != "", nil
}

// UserTexts lists the text of every user message of a request, in order, whole (not
// cut). A request with no stored events, or a folded one, has none.
func UserTexts(src Source, ex store.ExchangeRow) ([]string, error) {
	evs, err := loadEvents(src, ex)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range evs {
		if e.Kind != "message" {
			continue
		}
		t, ok, err := userText(src, e)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, t)
		}
	}
	return out, nil
}
