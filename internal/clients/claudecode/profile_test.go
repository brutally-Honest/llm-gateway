package claudecode_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/clients/claudecode"
	"github.com/brutally-honest/llm-gateway/internal/core"
)

// The profile satisfies core.Profile without importing core; the check lives here.
var _ core.Profile = claudecode.Profile{}

func TestProfile_ClaudeCodeDetected(t *testing.T) {
	p := claudecode.Profile{}
	if got := p.Name(); got != "claude-code" {
		t.Fatalf("Name() = %q, want %q", got, "claude-code")
	}

	cases := []struct {
		name    string
		method  string
		path    string
		headers map[string]string
		want    bool
	}{
		{
			name:    "claude-cli user agent",
			method:  http.MethodPost,
			path:    "/anthropic/v1/messages?beta=true",
			headers: map[string]string{"User-Agent": "claude-cli/2.1.283 (external, cli)"},
			want:    true,
		},
		{
			name:    "x-app cli alone",
			method:  http.MethodPost,
			path:    "/anthropic/v1/messages?beta=true",
			headers: map[string]string{"User-Agent": "some-sdk/1.0", "X-App": "cli"},
			want:    true,
		},
		{
			name:    "x-app cli with no user agent",
			method:  http.MethodPost,
			path:    "/anthropic/v1/messages",
			headers: map[string]string{"x-app": "cli"},
			want:    true,
		},
		{
			// The HEAD /api/hello shape from research Q1: a Bun user agent and no X-App.
			name:   "bun user agent from head api hello",
			method: http.MethodHead,
			path:   "/anthropic/api/hello",
			headers: map[string]string{
				"User-Agent":      "Bun/1.4.3",
				"Accept":          "*/*",
				"Accept-Encoding": "gzip, deflate, br",
				"Connection":      "keep-alive",
			},
			want: false,
		},
		{
			name:    "claude-cli not at the start",
			method:  http.MethodPost,
			path:    "/anthropic/v1/messages",
			headers: map[string]string{"User-Agent": "wrapper claude-cli/2.1.283"},
			want:    false,
		},
		{
			name:    "x-app with another value",
			method:  http.MethodPost,
			path:    "/anthropic/v1/messages",
			headers: map[string]string{"X-App": "web"},
			want:    false,
		},
		{
			name:    "no headers",
			method:  http.MethodGet,
			path:    "/anthropic/v1/models",
			headers: nil,
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			r.Header.Del("User-Agent")
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if got := p.Match(r); got != tc.want {
				t.Errorf("Match() = %v, want %v (headers %v)", got, tc.want, r.Header)
			}
		})
	}
}

// AC2: the Claude Code session header's value is the session key, unparsed.
func TestProfile_SessionKeyFromHeader(t *testing.T) {
	var p core.SessionKeyer = claudecode.Profile{}
	h := http.Header{}
	h.Set("X-Claude-Code-Session-Id", "3f2a9c1e-7b4d-4e8a-9c2f-1d5b6a7e8f90")
	if got, ok := p.SessionKey(h); !ok || got != "3f2a9c1e-7b4d-4e8a-9c2f-1d5b6a7e8f90" {
		t.Fatalf("SessionKey() = %q, %v, want the header value", got, ok)
	}
}

// AC2: a request without the header has no session key.
func TestProfile_NoHeaderNoKey(t *testing.T) {
	var p core.SessionKeyer = claudecode.Profile{}
	if got, ok := p.SessionKey(http.Header{"User-Agent": {"claude-cli/1.0.0"}}); got != "" || ok {
		t.Fatalf("SessionKey() = %q, %v, want none and absent", got, ok)
	}
}

// A header that is present but empty is reported as present, so core can reject it.
func TestProfile_EmptyHeaderIsPresent(t *testing.T) {
	var p core.SessionKeyer = claudecode.Profile{}
	if got, ok := p.SessionKey(http.Header{"X-Claude-Code-Session-Id": {""}}); got != "" || !ok {
		t.Fatalf("SessionKey() = %q, %v, want empty and present", got, ok)
	}
}
