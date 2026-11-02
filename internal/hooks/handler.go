package hooks

import (
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// Route is the ingest path; {client} names the profile the delivery belongs to.
const Route = "/_gateway/hooks/{client}"

// Handler answers hook deliveries. Whatever the sink does with a body, the answer is
// 200 with an empty body, which a harness reads as "no decision".
type Handler struct {
	sink           *Sink
	log            *zap.Logger
	maxBody        int64
	captureEnabled bool
	clock          func() time.Time

	// mu makes seq and the receive time one step: a delivery that takes a higher seq
	// never gets an earlier time.
	mu   sync.Mutex
	seq  int64
	last time.Time
}

// NewHandler makes the handler. maxBody is capture.max_body_bytes; with captureEnabled
// false it answers and counts but stores nothing.
func NewHandler(sink *Sink, log *zap.Logger, maxBody int64, captureEnabled bool) *Handler {
	return &Handler{sink: sink, log: log, maxBody: maxBody, captureEnabled: captureEnabled, clock: time.Now}
}

// SetClock replaces the time source, for tests. Call it before serving.
func (h *Handler) SetClock(clock func() time.Time) { h.clock = clock }

// stamp takes the next seq and the receive time together: the clock reading, or the
// last time handed out if the clock went backwards.
func (h *Handler) stamp() (int64, time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	now := h.clock()
	if now.Before(h.last) {
		now = h.last
	}
	h.last = now
	return h.seq, now
}

// Mount registers the ingest route on r.
func (h *Handler) Mount(r chi.Router) { r.HandleFunc(Route, h.ServeHTTP) }

// ServeHTTP never logs a body or a header: only the client segment, a size, a reason.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !loopbackHost(r.Host) || len(r.Header.Values("Origin")) > 0 || len(r.Header.Values("Sec-Fetch-Site")) > 0 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	client := chi.URLParam(r, "client")
	if !h.captureEnabled {
		h.sink.RecordCaptureOff()
		w.WriteHeader(http.StatusOK)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, h.maxBody+1))
	switch {
	case err != nil:
		h.log.Warn("hook body unreadable", zap.String("client", client))
	case int64(len(body)) > h.maxBody:
		h.sink.RecordOversize(client, h.maxBody)
	default:
		seq, at := h.stamp()
		h.sink.Submit(client, seq, at, body)
	}
	w.WriteHeader(http.StatusOK)
}

// loopbackHost reports whether a Host header names localhost or a loopback address.
func loopbackHost(hostport string) bool {
	host := hostport
	if hp, _, err := net.SplitHostPort(hostport); err == nil {
		host = hp
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
