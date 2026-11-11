package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// snapshotRetryMS is the reconnect delay sent with snapshot_error (E22).
const snapshotRetryMS = 30000

func mountStreams(api chi.Router, d Deps) {
	api.Get("/stream", d.Hub.serveGlobal)
	api.Get("/sessions/{id}/stream", d.Hub.serveSession)
}

func (h *Hub) serveGlobal(w http.ResponseWriter, r *http.Request) { h.serve(w, r, "") }

func (h *Hub) serveSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, _, _, ok := parseSessionID(id); !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	h.serve(w, r, id)
}

// validResumeInputs reports whether Last-Event-ID and ?since, when present, are non-negative
// integers. They are only checked: the snapshot is never narrowed by them (AC24).
func validResumeInputs(r *http.Request) bool {
	ok := func(v string) bool {
		n, err := strconv.ParseInt(v, 10, 64)
		return err == nil && n >= 0
	}
	if v := r.Header.Get("Last-Event-ID"); v != "" && !ok(v) {
		return false
	}
	return !r.URL.Query().Has("since") || ok(r.URL.Query().Get("since"))
}

// serve runs one SSE stream until the client leaves, the hub drops it, or the hub closes.
func (h *Hub) serve(w http.ResponseWriter, r *http.Request, session string) {
	if !validResumeInputs(r) {
		writeErr(w, http.StatusBadRequest, "bad_request", "Last-Event-ID and since must be non-negative integers")
		return
	}
	if session != "" && r.URL.Query().Get("view") == "1" {
		h.serveView(w, r, session)
		return
	}
	sub, err := h.subscribe(session)
	switch {
	case errors.Is(err, errTooManyStreams):
		writeErr(w, http.StatusServiceUnavailable, "too_many_streams", "too many open streams")
		return
	case err != nil:
		writeErr(w, http.StatusServiceUnavailable, "shutting_down", "the gateway is shutting down")
		return
	}
	defer h.unsubscribe(sub)

	rc := http.NewResponseController(w)
	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	sent, from, stale, ok := h.sendSnapshot(w, r, rc, session)
	if !ok {
		return
	}
	// ": open" follows the snapshot, so a client that sees the stream open knows the
	// snapshot is complete.
	if _, err := fmt.Fprint(w, ": open\n\n"); err != nil || rc.Flush() != nil {
		return
	}
	// A snapshot served from an older cache is caught up once the rebuild lands: this
	// stream is subscribed, so it gets everything after the snapshot's position.
	for _, st := range stale {
		h.catchUp(st.who, st.entries, nil, nil)
	}
	tick := time.NewTicker(h.heartbeat)
	defer tick.Stop()
	for {
		var err error
		select {
		case <-r.Context().Done():
			return
		case <-h.done:
			return
		case <-sub.gone:
			return
		case <-tick.C:
			_, err = fmt.Fprint(w, ": ping\n\n")
		case m := <-sub.ch:
			err = writeLive(w, m, sent, from)
		}
		if err != nil || rc.Flush() != nil {
			return
		}
	}
}

// sendSnapshot writes the connect snapshot straight to the response, outside the
// subscriber buffer. On a read failure it sends snapshot_error and reports false. It returns
// this stream's sent-record: per exchange, the hash of the entry list just sent (AC54),
// and the sessions the snapshot served from an older cache.
func (h *Hub) sendSnapshot(w http.ResponseWriter, r *http.Request, rc *http.ResponseController, session string) (map[string]string, eventPositionJSON, []staleRead, bool) {
	ctx, cancel := storeCtx(r)
	defer cancel()
	snap, err := h.snap(ctx, session)
	if err != nil {
		if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
			return nil, eventPositionJSON{}, nil, false
		}
		code, msg := "store_error", "the store could not be read"
		if errors.Is(err, context.DeadlineExceeded) {
			code, msg = "timeout", "the store did not answer in time"
		}
		b, _ := json.Marshal(envelope{Error: errBody{Code: code, Message: msg}})
		_, werr := fmt.Fprintf(w, "retry: %d\nevent: snapshot_error\ndata: %s\n\n", snapshotRetryMS, b)
		_ = werr
		_ = rc.Flush()
		return nil, eventPositionJSON{}, nil, false
	}
	for i := range snap.Sessions {
		b, err := json.Marshal(snap.Sessions[i])
		if err != nil {
			return nil, eventPositionJSON{}, nil, false
		}
		if _, err := fmt.Fprintf(w, "event: session\ndata: %s\n\n", b); err != nil {
			return nil, eventPositionJSON{}, nil, false
		}
	}
	sent := make(map[string]string, len(snap.Exchanges))
	for _, x := range snap.Exchanges {
		sent[x.RequestID] = x.Hash
		b, err := json.Marshal(x.Entries)
		if err != nil {
			return nil, eventPositionJSON{}, nil, false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", exchangeEvent(x), b); err != nil {
			return nil, eventPositionJSON{}, nil, false
		}
	}
	return sent, snap.Position, snap.Stale, rc.Flush() == nil
}

// exchangeEvent is the SSE event that carries x's whole entry list: "hooks" for the hooks
// item, whose group the client replaces as a whole, else "exchange".
func exchangeEvent(x liveExchange) string {
	if x.RequestID == hooksExchange {
		return "hooks"
	}
	return "exchange"
}

// writeLive writes one live message: a session event, or one batch for the committed
// exchanges. The batch is an entries event per committed exchange, then an exchange event
// with the whole list for every exchange that started after the earliest committed one
// and differs from what this stream last sent (AC54). Only the batch's last event has an
// id, the highest row id in it (D14). sent is this stream's record; an inactive session
// drops it (E21). A message built from less of the store than from, the snapshot's
// position, is dropped whole.
func writeLive(w http.ResponseWriter, m liveMsg, sent map[string]string, from eventPositionJSON) error {
	if m.Effects != nil {
		b, err := json.Marshal(m.Effects)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "event: effects\ndata: %s\n\n", b)
		return err
	}
	if m.Summary != nil {
		b, err := json.Marshal(m.Summary)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: session\ndata: %s\n\n", b); err != nil {
			return err
		}
	}
	if m.behind(from) {
		return nil // read from less of the store than the snapshot saw: it would undo it
	}
	if m.Refresh {
		return writeRefresh(w, m, sent)
	}
	if len(m.Exchanges) == 0 {
		return nil
	}
	if m.Inactive {
		clear(sent)
	}
	type ev struct {
		name string
		x    liveExchange
	}
	var batch []ev
	var maxRow int64
	committed := make(map[string]bool, len(m.Exchanges))
	for _, x := range m.Exchanges {
		batch = append(batch, ev{"entries", x})
		sent[x.RequestID] = x.Hash
		committed[x.RequestID] = true
		maxRow = max(maxRow, x.RowID)
	}
	for _, a := range m.All {
		if committed[a.RequestID] || sent[a.RequestID] == a.Hash {
			continue
		}
		batch = append(batch, ev{exchangeEvent(a), a})
		sent[a.RequestID] = a.Hash
	}
	if m.Inactive {
		clear(sent)
	}
	for i, e := range batch {
		b, err := json.Marshal(e.x.Entries)
		if err != nil {
			return err
		}
		id := ""
		if i == len(batch)-1 {
			id = fmt.Sprintf("id: %d\n", maxRow)
		}
		if _, err := fmt.Fprintf(w, "event: %s\n%sdata: %s\n\n", e.name, id, b); err != nil {
			return err
		}
	}
	return nil
}

// behind reports whether m's entries were built from less of the store than from, the
// position of the snapshot the stream sent. A message with no entries, or from an
// unknown position, is never behind.
func (m liveMsg) behind(from eventPositionJSON) bool {
	if len(m.All) == 0 {
		return false
	}
	return m.Position.Row < from.Row || m.Position.Hook < from.Hook
}

// writeRefresh writes a hook refresh: an exchange event with the whole list for every
// exchange in All, the hooks pseudo-exchange included, that differs from what this stream
// last sent. There is no id: row ids belong to exchanges only.
func writeRefresh(w http.ResponseWriter, m liveMsg, sent map[string]string) error {
	for _, a := range m.All {
		if h, ok := sent[a.RequestID]; ok && h == a.Hash {
			continue
		}
		b, err := json.Marshal(a.Entries)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", exchangeEvent(a), b); err != nil {
			return err
		}
		sent[a.RequestID] = a.Hash
	}
	if m.Inactive {
		clear(sent)
	}
	return nil
}

// viewDebounce is how long a view stream gathers pokes before it rebuilds the view once.
const viewDebounce = 150 * time.Millisecond

// viewEventJSON is the live view event (spec 016 AC12): the overview, the side calls, the
// between blocks and the lists whole, and only the turns whose hash changed since this
// stream last sent them. Order is the number of every turn the view has now, in order, so a
// client drops any other; Full marks the first event, which carries every turn.
type viewEventJSON struct {
	Version   string                `json:"version"`
	Full      bool                  `json:"full"`
	Order     []int                 `json:"order"`
	Turns     []timeline.Turn       `json:"turns"`
	Overview  timeline.Overview     `json:"overview"`
	SideCalls []timeline.SideCall   `json:"side_calls"`
	Between   []timeline.Between    `json:"between"`
	Files     timeline.FilesList    `json:"files"`
	Commands  timeline.CommandsList `json:"commands"`
	Sites     timeline.SitesList    `json:"sites"`
	Context   timeline.ContextView  `json:"context"`
}

// serveView runs one session view stream: the whole view on open, then, a debounce after
// each burst of changes, a view event with the turns that changed. It never reads entries.
func (h *Hub) serveView(w http.ResponseWriter, r *http.Request, session string) {
	principal, client, key, _ := parseSessionID(session)
	who := sessIdent{principal, client, key}
	sub, err := h.subscribeView(session, true)
	switch {
	case errors.Is(err, errTooManyStreams):
		writeErr(w, http.StatusServiceUnavailable, "too_many_streams", "too many open streams")
		return
	case err != nil:
		writeErr(w, http.StatusServiceUnavailable, "shutting_down", "the gateway is shutting down")
		return
	}
	defer h.unsubscribe(sub)
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	sent := map[int]string{} // turn number -> hash last sent
	send := func(full bool) error {
		ctx, cancel := context.WithTimeout(r.Context(), buildTimeout)
		v, err := h.viewOf(ctx, who)
		cancel()
		if err != nil {
			if r.Context().Err() != nil {
				return err
			}
			code, msg := "store_error", "the store could not be read"
			if errors.Is(err, context.DeadlineExceeded) {
				code, msg = "timeout", "the store did not answer in time"
			}
			b, _ := json.Marshal(envelope{Error: errBody{Code: code, Message: msg}})
			_, werr := fmt.Fprintf(w, "retry: %d\nevent: snapshot_error\ndata: %s\n\n", snapshotRetryMS, b)
			_ = rc.Flush()
			if werr != nil {
				return werr
			}
			return errViewFailed
		}
		b, err := json.Marshal(viewEvent(v, sent, full))
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: view\ndata: %s\n\n", b); err != nil {
			return err
		}
		return rc.Flush()
	}
	if err := send(true); err != nil {
		return
	}
	if _, err := fmt.Fprint(w, ": open\n\n"); err != nil || rc.Flush() != nil {
		return
	}
	tick := time.NewTicker(h.heartbeat)
	defer tick.Stop()
	var fire <-chan time.Time
	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.done:
			return
		case <-sub.gone:
			return
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		case <-sub.ch:
			if fire == nil {
				fire = time.After(viewDebounce)
			}
		case <-fire:
			fire = nil
			if err := send(false); err != nil {
				return
			}
		}
	}
}

// errViewFailed ends a view stream whose build failed; the browser reopens it.
var errViewFailed = errors.New("ui: view build failed")

// viewEvent is v as a live event: every turn when full, else only those whose hash
// differs from what sent holds. sent is updated to what this event carries.
func viewEvent(v timeline.View, sent map[int]string, full bool) viewEventJSON {
	ev := viewEventJSON{Version: v.Version, Full: full, Order: make([]int, 0, len(v.Turns)), Turns: []timeline.Turn{},
		Overview: v.Overview, SideCalls: v.SideCalls, Between: v.Between, Files: v.Files, Commands: v.Commands,
		Sites: v.Sites, Context: v.Context}
	have := make(map[int]bool, len(v.Turns))
	for _, t := range v.Turns {
		ev.Order = append(ev.Order, t.N)
		have[t.N] = true
		if full || sent[t.N] != t.Hash {
			ev.Turns = append(ev.Turns, t)
			sent[t.N] = t.Hash
		}
	}
	for n := range sent {
		if !have[n] {
			delete(sent, n)
		}
	}
	return ev
}
