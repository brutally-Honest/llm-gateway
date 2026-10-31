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

// validCursor reports whether Last-Event-ID and ?since, when present, are non-negative
// integers. They are only checked: the snapshot is never narrowed by them (AC24).
func validCursor(r *http.Request) bool {
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
	if !validCursor(r) {
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
	if !h.sendSnapshot(w, r, rc, session) {
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
			err = writeLive(w, m)
		}
		if err != nil || rc.Flush() != nil {
			return
		}
	}
}

// sendSnapshot writes the connect snapshot straight to the response, outside the
// subscriber buffer. On a read failure it sends snapshot_error and reports false.
func (h *Hub) sendSnapshot(w http.ResponseWriter, r *http.Request, rc *http.ResponseController, session string) bool {
	ctx, cancel := storeCtx(r)
	defer cancel()
	snap, err := h.snap(ctx, session)
	if err != nil {
		if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
			return false
		}
		code, msg := "store_error", "the store could not be read"
		if errors.Is(err, context.DeadlineExceeded) {
			code, msg = "timeout", "the store did not answer in time"
		}
		b, _ := json.Marshal(envelope{Error: errBody{Code: code, Message: msg}})
		_, werr := fmt.Fprintf(w, "retry: %d\nevent: snapshot_error\ndata: %s\n\n", snapshotRetryMS, b)
		_ = werr
		_ = rc.Flush()
		return false
	}
	for i := range snap.Sessions {
		b, err := json.Marshal(snap.Sessions[i])
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: session\ndata: %s\n\n", b); err != nil {
			return false
		}
	}
	for _, x := range snap.Exchanges {
		b, err := json.Marshal(x.Entries)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: exchange\ndata: %s\n\n", b); err != nil {
			return false
		}
	}
	return rc.Flush() == nil
}

// writeLive writes one live message: a session event, or one entries event per exchange
// with the exchange's store row id as the event id.
func writeLive(w http.ResponseWriter, m liveMsg) error {
	if m.Summary != nil {
		b, err := json.Marshal(m.Summary)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: session\ndata: %s\n\n", b); err != nil {
			return err
		}
	}
	for _, x := range m.Exchanges {
		b, err := json.Marshal(x.Entries)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: entries\nid: %d\ndata: %s\n\n", x.RowID, b); err != nil {
			return err
		}
	}
	return nil
}
