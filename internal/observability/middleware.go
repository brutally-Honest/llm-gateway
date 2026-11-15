package observability

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// RequestMetrics counts every request on listener, with its status class and time.
// Nothing it serves is proxied. The wrapper keeps http.Flusher, which SSE streams need.
func RequestMetrics(m *Metrics, listener string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if m == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() { m.Request(listener, ww.Status(), false, time.Since(start)) }()
			next.ServeHTTP(ww, r)
		})
	}
}
