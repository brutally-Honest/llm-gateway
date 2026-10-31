package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

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

// serve runs one SSE stream until the client leaves, the hub drops it, or the hub closes.
func (h *Hub) serve(w http.ResponseWriter, r *http.Request, session string) {
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
