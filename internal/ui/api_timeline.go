package ui

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

const (
	defaultTimelineLimit = 500
	maxTimelineLimit     = 2000
)

type timelinePage struct {
	Entries     []entryJSON `json:"entries"`
	NextAfter   *string     `json:"next_after"`
	StreamSince int64       `json:"stream_since"`
}

// snapshotSource serves the exchanges of one snapshot read, so the timeline and
// stream_since describe the same rows.
type snapshotSource struct {
	*store.CtxReader
	exs []store.ExchangeRow
}

func (s snapshotSource) SessionExchanges(string, string, string) ([]store.ExchangeRow, error) {
	return s.exs, nil
}

func mountExchanges(api chi.Router, d Deps) {
	api.Get("/sessions/{id}/timeline", d.getTimeline)
	api.Get("/exchanges/{id}", d.getExchange)
	api.Get("/exchanges/{id}/body", d.getBody)
}

// position is an entry's sort tuple: exchange start, exchange id, index.
type position struct {
	start int64
	req   string
	idx   int
}

// after reports whether p sorts strictly after the token's position.
func (p position) after(t afterToken) bool {
	switch {
	case p.start != t.At:
		return p.start > t.At
	case p.req != t.RequestID:
		return p.req > t.RequestID
	}
	return p.idx > t.Index
}

func (d Deps) getTimeline(w http.ResponseWriter, r *http.Request) {
	principal, client, key, ok := parseSessionID(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	limit, ok := parseLimit(r, defaultTimelineLimit, maxTimelineLimit)
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad_request", "limit must be a positive integer")
		return
	}
	var after *afterToken
	if r.URL.Query().Has("after") {
		t, ok := decodeAfter(r.URL.Query().Get("after"), listTimeline)
		if !ok {
			writeErr(w, http.StatusBadRequest, "bad_request", "after is not a timeline position")
			return
		}
		after = &t
	}
	if r.URL.Query().Has("since") {
		if n, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64); err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "bad_request", "since must be a non-negative integer")
			return
		}
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	rows, _, err := c.SessionSnapshot(principal, client, key)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if len(rows) == 0 {
		writeErr(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	exs := make([]store.ExchangeRow, len(rows))
	byID := make(map[string]store.ExchangeRow, len(rows))
	rowID := make(map[string]int64, len(rows))
	for i, x := range rows {
		exs[i] = x.ExchangeRow
		byID[x.RequestID] = x.ExchangeRow
		rowID[x.RequestID] = x.RowID
	}
	session := store.SessionRow{PrincipalID: principal, Client: client, SessionKey: key}
	entries, _, err := timeline.BuildWithUsage(snapshotSource{c, exs}, session, timeline.Options{Cut: entryCut})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	all := mapEntries(entries, byID)
	pos := make([]position, len(all))
	for i, e := range all {
		pos[i] = position{start: byID[e.ExchangeID].StartedAt, req: e.ExchangeID, idx: e.Index}
	}
	from := 0
	if after != nil {
		for from < len(all) && !pos[from].after(*after) {
			from++
		}
	}
	end := min(from+limit, len(all))
	page := timelinePage{Entries: all[from:end]}
	for _, e := range page.Entries {
		page.StreamSince = max(page.StreamSince, rowID[e.ExchangeID])
	}
	if end < len(all) {
		last := pos[end-1]
		n := encodeAfter(afterToken{List: listTimeline, At: last.start, RequestID: last.req, Index: last.idx})
		page.NextAfter = &n
	}
	writeJSON(w, page)
}
