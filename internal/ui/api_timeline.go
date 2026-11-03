package ui

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/store"
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

func mountExchanges(api chi.Router, d Deps) {
	api.Get("/sessions/{id}/timeline", d.getTimeline)
	api.Get("/exchanges/{id}", d.getExchange)
	api.Get("/exchanges/{id}/body", d.getBody)
}

// position is an entry's sort tuple: start, request id, index within the exchange,
// then for a hook line its sequence number and stored row id. A hook line's start is
// its receive time; its index is 0.
type position struct {
	start int64
	req   string
	idx   int
	seq   int64
	row   int64
}

// after reports whether p sorts strictly after the token's position. Two hook lines
// that share a receive time compare by their hook key, the order the store reads in.
func (p position) after(t afterToken) bool {
	switch {
	case p.start != t.At:
		return p.start > t.At
	case p.req != t.RequestID:
		return p.req > t.RequestID
	case p.idx != t.Index:
		return p.idx > t.Index
	}
	return store.HookKey{ReceivedAt: t.At, Seq: t.Seq, ID: t.Row}.Less(store.HookKey{ReceivedAt: p.start, Seq: p.seq, ID: p.row})
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
	bs, err := buildSession(c, sessIdent{principal, client, key}, d.Rules[client], entryCut)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if bs.rows == 0 {
		// A session known only from hook events has a timeline of hook lines (AC25).
		hooks, err := c.HookEvents(principal, client, key)
		if err != nil {
			writeStoreErr(w, err)
			return
		}
		if len(hooks) == 0 {
			writeErr(w, http.StatusNotFound, "not_found", "no such session")
			return
		}
	}
	entries, byID, facts, rowID := bs.entries, bs.byID, bs.facts, bs.rowID
	all := mapEntries(entries, byID, facts, d.Rules[client])
	pos := make([]position, len(all))
	for i, e := range all {
		pos[i] = position{start: e.startNS, req: e.ExchangeID, idx: e.Index}
		if e.Kind == kindHookEvent {
			pos[i] = position{start: e.startNS, req: hookRequest, seq: e.hookSeq, row: e.hookRow}
		}
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
		n := encodeAfter(afterToken{List: listTimeline, At: last.start, RequestID: last.req, Index: last.idx,
			Seq: last.seq, Row: last.row})
		page.NextAfter = &n
	}
	writeJSON(w, page)
}
