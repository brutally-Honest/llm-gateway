// Package server is the gateway's HTTP surface: the http.Server, the middleware every
// route runs under, and GET /healthz. Later features mount their routes on it.
package server

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/logging"
)

// Server is an http.Server with the gateway's router and an in-flight count.
type Server struct {
	srv      *http.Server
	inflight atomic.Int64
}

// New builds the router with the middleware and /healthz, then calls each mount on
// it. main passes no mounts; tests pass their own routes, which then run under the
// same middleware as production ones.
func New(log *zap.Logger, mount ...func(chi.Router)) *Server {
	s := &Server{}
	r := chi.NewRouter()
	// Outermost first. inflight wraps everything, so a request that panics is still
	// counted and decremented.
	r.Use(s.countInFlight, requestID, accessLog(log), recoverer(log))
	r.Get("/healthz", healthz)
	for _, m := range mount {
		m(r)
	}
	// /_gateway/ is the gateway's own prefix: anything under it that no route above
	// claimed is answered here, never forwarded to a provider. chi prefers the more
	// specific routes mounted above.
	r.Handle("/_gateway/*", http.NotFoundHandler())
	s.srv = &http.Server{
		Handler: r,
		// Slowloris guard. No ReadTimeout or WriteTimeout: a write timeout would cut
		// 001's streams.
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          logging.StdLog(log),
	}
	return s
}

// Serve serves on ln until Shutdown or Close. It returns http.ErrServerClosed then.
func (s *Server) Serve(ln net.Listener) error {
	return s.srv.Serve(ln)
}

// Shutdown stops accepting connections and waits for in-flight requests, up to ctx.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// Close closes the listener and every connection at once.
func (s *Server) Close() error {
	return s.srv.Close()
}

// InFlight is the number of handlers still running.
func (s *Server) InFlight() int64 {
	return s.inflight.Load()
}

func (s *Server) countInFlight(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.inflight.Add(1)
		defer s.inflight.Add(-1)
		next.ServeHTTP(w, r)
	})
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
