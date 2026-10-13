package anthropic

import (
	"encoding/json"
	"net/http"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

var _ core.RateLimitAdapter = Adapter{}

// IsModelCall is POST /v1/messages and nothing else: count_tokens, the models list
// and the client's own probes are not model calls.
func (Adapter) IsModelCall(method, path string) bool {
	return method == http.MethodPost && path == "/v1/messages"
}

// LimitErrorBody is Anthropic's error envelope with permission_error or
// rate_limit_error as its type.
func (Adapter) LimitErrorBody(kind core.LimitErrorKind, message string) (string, []byte) {
	typ := "rate_limit_error"
	if kind == core.LimitPermission {
		typ = "permission_error"
	}
	body, err := json.Marshal(errorEnvelope{
		Type:  "error",
		Error: errorDetail{Type: typ, Message: message},
	})
	if err != nil {
		body = []byte(`{"type":"error","error":{"type":"` + typ + `","message":"error"}}`)
	}
	return "application/json", body
}
