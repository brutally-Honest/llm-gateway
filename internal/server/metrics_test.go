package server

import (
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/observability"
)

// Spec 021 AC3, E4, E5: what counts as an upstream error, and as which kind.
func TestUpstreamErrorKind(t *testing.T) {
	for _, c := range []struct {
		name string
		m    core.Meta
		want string
	}{
		{"ok", core.Meta{HasTTFB: true, Status: 200}, ""},
		{"timeout", core.Meta{GatewayError: core.GatewayErrorUpstreamTimeout, Status: 504}, observability.UpstreamTimeout},
		{"unreachable", core.Meta{GatewayError: core.GatewayErrorUpstreamUnreachable, Status: 502}, observability.UpstreamUnreachable},
		{"aborted", core.Meta{HasTTFB: true, Status: 200, UpstreamAborted: true}, observability.UpstreamAborted},
		{"429", core.Meta{HasTTFB: true, Status: 429}, observability.UpstreamRateLimited},
		{"503", core.Meta{HasTTFB: true, Status: 503}, observability.UpstreamStatus5xx},
		{"the gateway's own 429", core.Meta{Status: 429}, ""},
		{"client left", core.Meta{Status: 499, ClientDisconnected: true}, ""},
		{"client body", core.Meta{GatewayError: "client_body", Status: 400}, ""},
		{"shutdown cut", core.Meta{GatewayError: "gateway_shutdown", Status: 503}, ""},
		{"upstream 4xx", core.Meta{HasTTFB: true, Status: 400}, ""},
	} {
		if got := upstreamErrorKind(&c.m); got != c.want {
			t.Errorf("%s: kind = %q, want %q", c.name, got, c.want)
		}
	}
}
