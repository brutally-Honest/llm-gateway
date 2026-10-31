package ui

import (
	"errors"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// csp is the one content security policy every response carries (AC33).
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'"

// securityHeaders is the outermost middleware, so 421, 403 and 500 carry them too.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// trackWriter records whether anything was written, and passes Flush through for SSE.
type trackWriter struct {
	http.ResponseWriter
	wrote bool
}

func (t *trackWriter) WriteHeader(code int) {
	t.wrote = true
	t.ResponseWriter.WriteHeader(code)
}

func (t *trackWriter) Write(b []byte) (int, error) {
	t.wrote = true
	return t.ResponseWriter.Write(b)
}

func (t *trackWriter) Flush() {
	t.wrote = true
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (t *trackWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// recoverer logs a panic with the request path and answers 500 if nothing was written.
func recoverer(log *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tw := &trackWriter{ResponseWriter: w}
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				log.Error("ui handler panic", zap.String("path", r.URL.Path), zap.Any("panic", rec))
				if !tw.wrote {
					writeErr(tw, http.StatusInternalServerError, "internal", "internal error")
				}
			}()
			next.ServeHTTP(tw, r)
		})
	}
}

// hostCheck answers 421 for any Host that is not a UI host:port (DNS rebinding, AC31).
func hostCheck(hosts map[string]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !hosts[strings.ToLower(r.Host)] {
				writeErr(w, http.StatusMisdirectedRequest, "bad_host", "unknown host")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// originCheck answers 403 for an Origin that is not a UI origin. It never sets a CORS
// header (AC32). A request without Origin passes.
func originCheck(hosts map[string]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			vals := r.Header.Values("Origin")
			for _, o := range vals {
				host, ok := strings.CutPrefix(strings.ToLower(o), "http://")
				if !ok || !hosts[host] {
					writeErr(w, http.StatusForbidden, "bad_origin", "origin not allowed")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// getOnly answers 405 with Allow: GET for any other method (HEAD included) under
// /api, before routing.
func getOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && (r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/")) {
			w.Header().Set("Allow", http.MethodGet)
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}
