// Package ui serves the read-only web UI: a JSON API and the embedded page, on its own
// loopback listener, behind the Host, Origin and header checks.
package ui

import (
	"embed"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

//go:embed web
var webFS embed.FS

// Deps is what Handler needs.
type Deps struct {
	// Addr is the address the UI listener is bound to (host:port). Its host:port, and
	// localhost on the same port, are the only Hosts and Origins served.
	Addr string
	Log  *zap.Logger
	// Reader is the read-only store view; nil leaves the store routes unmounted.
	Reader *store.Reader
	// Hub fans commits out to SSE streams; nil leaves the stream routes unmounted.
	Hub *Hub
	// Now is the clock for the active flag; nil is time.Now.
	Now func() time.Time
}

// Handler returns the UI router.
func Handler(d Deps) http.Handler { return newRouter(d) }

// allowedHosts is the host:port set the UI answers to (D8): the three loopback forms on
// the bound port, plus the listen host itself when it is a loopback literal.
func allowedHosts(addr string) map[string]bool {
	set := map[string]bool{}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return set
	}
	for _, h := range []string{"127.0.0.1", "localhost", "::1"} {
		set[strings.ToLower(net.JoinHostPort(h, port))] = true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		set[strings.ToLower(net.JoinHostPort(host, port))] = true
	}
	return set
}

// newRouter builds the router; extra mounts are for tests and later routes.
func newRouter(d Deps, extra ...func(chi.Router)) chi.Router {
	log := d.Log
	if log == nil {
		log = zap.NewNop()
	}
	hosts := allowedHosts(d.Addr)
	r := chi.NewRouter()
	r.Use(securityHeaders, recoverer(log), hostCheck(hosts), originCheck(hosts), getOnly)
	r.Route("/api", func(api chi.Router) {
		api.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, map[string]bool{"ok": true})
		})
		if d.Reader != nil {
			mountSessions(api, d)
			mountExchanges(api, d)
		}
		if d.Hub != nil {
			mountStreams(api, d)
		}
		for _, m := range extra {
			m(api)
		}
		api.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeErr(w, http.StatusNotFound, "not_found", "no such route")
		})
	})
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err) // the embed directive guarantees web/
	}
	files := http.FileServerFS(sub)
	r.Get("/*", files.ServeHTTP)
	return r
}
