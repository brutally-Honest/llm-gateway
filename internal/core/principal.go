package core

import "net/http"

// PrincipalLocal is the one principal of a gateway with no identity: every request
// is attributed to it until a later phase resolves callers.
const PrincipalLocal = "local"

// PrincipalResolver names who sent a request. Every stored exchange and event
// carries the ID it returns.
type PrincipalResolver interface {
	// Resolve returns the principal ID for r.
	Resolve(r *http.Request) string
}

// LocalPrincipal resolves every request to PrincipalLocal.
type LocalPrincipal struct{}

// Resolve always returns PrincipalLocal.
func (LocalPrincipal) Resolve(*http.Request) string { return PrincipalLocal }
