package main

import (
	"net/http"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// AC2: through run, a Claude Code request carrying x-claude-code-session-id is stored
// with the header's value as its session key.
func TestRun_CapturesSessionKeyFromClaudeCodeHeader(t *testing.T) {
	const (
		path    = "/anthropic/v1/messages"
		session = "3f2a9c1e-7b4d-4e8a-9c2f-1d5b6a7e8f90"
	)
	dir := t.TempDir()
	up := jsonUpstream(t)
	g, url := servedWith(t, captureEnv(dir, up, nil))
	code, err := do(t, http.MethodPost, url+path, map[string]string{
		"User-Agent":               "claude-cli/1.0.0 (external, cli)",
		"X-Claude-Code-Session-Id": session,
	}, `{"model":"m","messages":[]}`)
	if err != nil || code != http.StatusOK {
		t.Fatalf("status %d, err %v", code, err)
	}
	ex, _ := storedExchange(t, g, dir, path)
	if ex.Client != "claude-code" || ex.SessionKey != session {
		t.Errorf("client %q, session key %q; want claude-code and %q", ex.Client, ex.SessionKey, session)
	}
}

// AC3: a request matching no profile is stored with client unknown and no session
// key, even when it carries the session header.
func TestRun_UnknownClientCapturedUngrouped(t *testing.T) {
	const path = "/anthropic/v1/messages"
	dir := t.TempDir()
	up := jsonUpstream(t)
	g, url := servedWith(t, captureEnv(dir, up, nil))
	code, err := do(t, http.MethodPost, url+path, map[string]string{
		"User-Agent":               "some-sdk/1.0",
		"X-Claude-Code-Session-Id": "should-not-group",
	}, `{"model":"m","messages":[]}`)
	if err != nil || code != http.StatusOK {
		t.Fatalf("status %d, err %v", code, err)
	}
	ex, _ := storedExchange(t, g, dir, path)
	if ex.Client != core.ClientUnknown || ex.SessionKey != "" {
		t.Errorf("client %q, session key %q; want unknown and none", ex.Client, ex.SessionKey)
	}
}
