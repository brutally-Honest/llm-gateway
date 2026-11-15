package ui

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// todayJSON is the totals since a time (AC4): sessions with any activity (a model call or
// a hook, 017 D30), model calls with side calls included, and since spec 017 AC3 the files
// the sessions changed (distinct per session) and the sites they connected to (distinct
// hosts).
type todayJSON struct {
	Sessions   int         `json:"sessions"`
	Calls      int         `json:"calls"`
	Changed    int         `json:"changed"`
	Sites      int         `json:"sites"`
	Input      counterJSON `json:"input"`
	Output     counterJSON `json:"output"`
	CacheWrite counterJSON `json:"cache_write"`
	CacheRead  counterJSON `json:"cache_read"`
}

// sinceSource presents a fixed set of exchanges as one session, so SessionStats can
// total their usage whatever session they belong to. Side calls count like any call.
type sinceSource struct {
	*store.CtxReader
	exs []store.ExchangeRow
}

func (s sinceSource) SessionExchanges(string, string, string) ([]store.ExchangeRow, error) {
	return s.exs, nil
}

func mountToday(api chi.Router, d Deps) { api.Get("/today", d.getToday) }

func (d Deps) getToday(w http.ResponseWriter, r *http.Request) {
	since, err := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "since must be an RFC 3339 time")
		return
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	exs, err := c.ExchangesSince(since.UnixNano())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	st, err := timeline.SessionStats(sinceSource{c, exs}, store.SessionRow{})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	u := usageToJSON(timeline.Summary{Total: st.Usage.Total})
	seen := map[[3]string]bool{}
	for _, ex := range exs {
		if ex.SessionKey != "" {
			seen[[3]string{ex.PrincipalID, ex.Client, ex.SessionKey}] = true
		}
	}
	// 017 AC3 (D30): a session with only hooks since the time counts too.
	hooked, err := c.HookSessionsSince(since.UnixNano())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	for _, id := range hooked {
		seen[[3]string{id.Principal, id.Client, id.Key}] = true
	}
	changed, sites, err := c.EffectTotalsSince(since.UnixNano())
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, todayJSON{
		Sessions: len(seen), Calls: st.ModelCalls, Changed: changed, Sites: sites,
		Input: u.Input, Output: u.Output, CacheWrite: u.CacheWrite, CacheRead: u.CacheRead,
	})
}
