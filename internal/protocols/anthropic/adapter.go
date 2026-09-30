// Package anthropic is the Anthropic Messages protocol adapter. It hands core the
// values that make a request Anthropic-shaped: a prefix, an upstream default, an auth
// kind, an error envelope and the headers that carry its secrets. It parses no body.
package anthropic

import (
	"encoding/json"
	"net/http"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// Name is the protocol label logged for an Anthropic request, and the adapter's key
// under upstreams in config.
const Name = "anthropic"

// Adapter is the Anthropic Messages protocol. It satisfies core.Adapter.
type Adapter struct{}

// Name is logged as the request's protocol.
func (Adapter) Name() string { return Name }

// Prefix is where the adapter is mounted.
func (Adapter) Prefix() string { return "/anthropic" }

// DefaultBaseURL is Anthropic's API.
func (Adapter) DefaultBaseURL() string { return "https://api.anthropic.com" }

// AuthKind reports which credential header is present. It never reads a value, so
// any Authorization scheme counts as bearer (research Q15).
func (Adapter) AuthKind(h http.Header) core.AuthKind {
	_, apiKey := h["X-Api-Key"]
	_, bearer := h["Authorization"]
	switch {
	case apiKey && bearer:
		return core.AuthBoth
	case apiKey:
		return core.AuthAPIKey
	case bearer:
		return core.AuthBearer
	}
	return core.AuthNone
}

// SecretHeaders adds x-api-key, Anthropic's key header, to the auth headers core
// redacts in a captured exchange.
func (Adapter) SecretHeaders() []string { return []string{"x-api-key"} }

// SecretQueryParams is empty: Anthropic never puts a secret in the query.
func (Adapter) SecretQueryParams() []string { return nil }

// errorEnvelope is Anthropic's error body. Field order is the wire order.
type errorEnvelope struct {
	Type  string      `json:"type"`
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ErrorBody is Anthropic's error envelope for an error the gateway created:
// {"type":"error","error":{"type":"api_error","message":"gateway: <reason>"}}.
func (Adapter) ErrorBody(reason string) (string, []byte) {
	body, err := json.Marshal(errorEnvelope{
		Type:  "error",
		Error: errorDetail{Type: "api_error", Message: "gateway: " + reason},
	})
	if err != nil {
		// Two strings cannot fail to marshal; keep the envelope's shape regardless.
		body = []byte(`{"type":"error","error":{"type":"api_error","message":"gateway: error"}}`)
	}
	return "application/json", body
}
