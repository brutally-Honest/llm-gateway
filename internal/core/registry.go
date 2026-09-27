package core

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// Registry holds the adapters, each with the proxy built for its upstream, and the
// client profiles, both in registration order. Everything is registered before the
// server starts; after that it is only read.
type Registry struct {
	log      *zap.Logger
	adapters []mounted
	profiles []Profile
}

// mounted is one adapter and the proxy in front of its upstream.
type mounted struct {
	adapter Adapter
	proxy   *Proxy
}

// NewRegistry returns an empty registry. log is where each proxy's own error line goes.
func NewRegistry(log *zap.Logger) *Registry {
	return &Registry{log: log}
}

// AddProfile registers a client profile. Profiles are tried in the order added.
func (r *Registry) AddProfile(p Profile) {
	r.profiles = append(r.profiles, p)
}

// AddAdapter registers a protocol adapter and builds its proxy in front of up.
func (r *Registry) AddAdapter(a Adapter, up config.Upstream) {
	r.adapters = append(r.adapters, mounted{adapter: a, proxy: NewProxy(a, up, r.Identify, r.log)})
}

// Identify names the client that sent req: the first profile that matches, else
// ClientUnknown.
func (r *Registry) Identify(req *http.Request) string {
	for _, p := range r.profiles {
		if p.Match(req) {
			return p.Name()
		}
	}
	return ClientUnknown
}

// Mount registers each adapter's proxy under its prefix, for every method chi knows,
// HEAD included. The bare prefix is not under it and, like every other path, gets
// chi's 404. chi checks the method before the path, so a non-standard method gets
// chi's 405 inside or outside a prefix (research Q20).
func (r *Registry) Mount(rt chi.Router) {
	for _, m := range r.adapters {
		rt.Handle(m.adapter.Prefix()+"/*", m.proxy)
	}
}
