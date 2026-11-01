package hooks

import (
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"

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
	// seq numbers the deliveries this process receives, in arrival order.
	seq atomic.Int64
}

// NewHandler makes the handler. maxBody is capture.max_body_bytes; with captureEnabled
// false it answers and counts but stores nothing.
func NewHandler(sink *Sink, log *zap.Logger, maxBody int64, captureEnabled bool) *Handler {
	return &Handler{sink: sink, log: log, maxBody: maxBody, captureEnabled: captureEnabled}
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
		h.sink.Submit(client, h.seq.Add(1), body)
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
