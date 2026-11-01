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
	sent, ok := h.sendSnapshot(w, r, rc, session)
	if !ok {
		return
	}
	// ": open" follows the snapshot, so a client that sees the stream open knows the
	// snapshot is complete.
	if _, err := fmt.Fprint(w, ": open\n\n"); err != nil || rc.Flush() != nil {
		return
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
			err = writeLive(w, m, sent)
		}
		if err != nil || rc.Flush() != nil {
			return
		}
	}
}

// sendSnapshot writes the connect snapshot straight to the response, outside the
// subscriber buffer. On a read failure it sends snapshot_error and reports false. It returns
// this stream's sent-record: per exchange, the hash of the entry list just sent (AC54).
func (h *Hub) sendSnapshot(w http.ResponseWriter, r *http.Request, rc *http.ResponseController, session string) (map[string]string, bool) {
	ctx, cancel := storeCtx(r)
	defer cancel()
	snap, err := h.snap(ctx, session)
	if err != nil {
		if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
			return nil, false
		}
		code, msg := "store_error", "the store could not be read"
		if errors.Is(err, context.DeadlineExceeded) {
			code, msg = "timeout", "the store did not answer in time"
		}
		b, _ := json.Marshal(envelope{Error: errBody{Code: code, Message: msg}})
		_, werr := fmt.Fprintf(w, "retry: %d\nevent: snapshot_error\ndata: %s\n\n", snapshotRetryMS, b)
		_ = werr
		_ = rc.Flush()
		return nil, false
	}
	for i := range snap.Sessions {
		b, err := json.Marshal(snap.Sessions[i])
		if err != nil {
			return nil, false
		}
		if _, err := fmt.Fprintf(w, "event: session\ndata: %s\n\n", b); err != nil {
			return nil, false
		}
	}
	sent := make(map[string]string, len(snap.Exchanges))
	for _, x := range snap.Exchanges {
		sent[x.RequestID] = x.Hash
		b, err := json.Marshal(x.Entries)
		if err != nil {
			return nil, false
		}
		if _, err := fmt.Fprintf(w, "event: exchange\ndata: %s\n\n", b); err != nil {
			return nil, false
		}
	}
	return sent, rc.Flush() == nil
}

// writeLive writes one live message: a session event, or one batch for the committed
// exchanges. The batch is an entries event per committed exchange, then an exchange event
// with the whole list for every exchange that started after the earliest committed one
// and differs from what this stream last sent (AC54). Only the batch's last event has an
// id, the highest row id in it (D14). sent is this stream's record; an inactive session
// drops it (E21).
func writeLive(w http.ResponseWriter, m liveMsg, sent map[string]string) error {
	if m.Summary != nil {
		b, err := json.Marshal(m.Summary)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: session\ndata: %s\n\n", b); err != nil {
			return err
		}
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
	first := -1
	committed := make(map[string]bool, len(m.Exchanges))
	for _, x := range m.Exchanges {
		batch = append(batch, ev{"entries", x})
		sent[x.RequestID] = x.Hash
		committed[x.RequestID] = true
		maxRow = max(maxRow, x.RowID)
		for i, a := range m.All {
			if a.RequestID == x.RequestID && (first < 0 || i < first) {
				first = i
			}
		}
	}
	if first >= 0 {
		for _, a := range m.All[first+1:] {
			if committed[a.RequestID] || sent[a.RequestID] == a.Hash {
				continue
			}
			batch = append(batch, ev{"exchange", a})
			sent[a.RequestID] = a.Hash
		}
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
