package timeline

import (
	"fmt"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// PreviewRunes is the most runes of the first user prompt a session row shows.
const PreviewRunes = 80

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

// Stats are a session's counts and usage, taken from exchanges and events only.
type Stats struct {
	ModelCalls, ToolCalls, Errors int
	Usage                         Summary
}

// SessionStats counts what Build would show for the session (model calls, tool
// calls, errors) and summarizes its usage, without reading any content.
func SessionStats(src Source, session store.SessionRow) (Stats, error) {
	exchanges, err := src.SessionExchanges(session.PrincipalID, session.Client, session.SessionKey)
	if err != nil {
		return Stats{}, fmt.Errorf("timeline: session exchanges: %w", err)
	}
	var (
		st      Stats
		earlier []thread
		calls   []CallUsage
	)
	for _, ex := range exchanges {
		evs, err := src.Events(ex.RequestID)
		if err != nil {
			return Stats{}, fmt.Errorf("timeline: events of %s: %w", ex.RequestID, err)
		}
		if c, ok := callUsage(ex, evs); ok {
			calls = append(calls, c)
		}
		hashes := messageHashes(evs)
		if len(hashes) == 0 || foldedParse(ex.Parse) {
			continue
		}
		shared, _ := bestMatch(earlier, hashes)
		earlier = append(earlier, thread{hashes: hashes})
		idx := -1
		for _, e := range evs {
			switch e.Kind {
			case "message":
				idx++
			case "tool_call":
				if idx < 0 || idx >= shared {
					st.ToolCalls++
				}
			case "error":
				st.Errors++
			}
		}
	}
	st.ModelCalls = len(calls)
	st.Usage = Summarize(calls)
	return st, nil
}
