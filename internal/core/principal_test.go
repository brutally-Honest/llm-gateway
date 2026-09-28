package core_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// This phase has no identity: every request, whatever it carries, is the local
// principal (AC22).
func TestLocalPrincipal_AlwaysLocal(t *testing.T) {
	var resolver core.PrincipalResolver = core.LocalPrincipal{}

	plain := httptest.NewRequest(http.MethodPost, "/t/v1/x", nil)
	withCreds := httptest.NewRequest(http.MethodGet, "/t/other?key=abc", nil)
	withCreds.Header.Set("Authorization", "Bearer secret")
	withCreds.Header.Set("X-Test-Key", "secret")
	withCreds.Header.Set("X-Test-Client", "1")

	for name, r := range map[string]*http.Request{"plain": plain, "with credentials": withCreds} {
		if got := resolver.Resolve(r); got != core.PrincipalLocal {
			t.Errorf("%s: Resolve = %q, want %q", name, got, core.PrincipalLocal)
		}
	}
	if core.PrincipalLocal != "local" {
		t.Errorf("PrincipalLocal = %q, want %q", core.PrincipalLocal, "local")
	}
}
