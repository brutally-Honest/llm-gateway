package main

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// 046 P3-d: the title rule reaches the view through the rules the server really hands the
// UI (uiRules, claudecode.Profile). A first prompt of exactly "Tool loaded." is a synthetic
// turn titled "(system) tool loaded"; the session title skips it for the typed prompt. If
// PromptTitle's signature or its wiring drifted, the titles would fall back to the prompt.
func TestRun_PromptTitleRuleReachesTheView(t *testing.T) {
	const (
		path    = "/anthropic/v1/messages"
		session = "00000000-0000-4000-8000-000000000046"
		model   = `"model":"claude-opus-5-5","max_tokens":64,"stream":true`
	)
	bodies := []string{
		`{` + model + `,"messages":[{"role":"user","content":[{"type":"text","text":"Tool loaded."}]}]}`,
		`{` + model + `,"messages":[` +
			`{"role":"user","content":[{"type":"text","text":"Tool loaded."}]},` +
			`{"role":"assistant","content":[{"type":"text","text":"ok"}]},` +
			`{"role":"user","content":[{"type":"text","text":"fix the login button"}]}]}`,
	}
	text := upstreamAnswer{contentType: "text/event-stream; charset=utf-8", body: fixture(t, "stream.sse")}
	var n atomic.Int32
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		text.handler(w, r)
	})
	dir := t.TempDir()
	g, url := servedWith(t, captureEnv(dir, up, map[string]string{"GATEWAY_EFFECTS_ENABLED": "false"}))
	for i, body := range bodies {
		code, err := do(t, http.MethodPost, url+path+"?beta=true", map[string]string{
			"Content-Type":             "application/json",
			"User-Agent":               "claude-cli/2.1.325 (external, cli)",
			"X-App":                    "cli",
			"Anthropic-Version":        "2023-06-01",
			"Anthropic-Beta":           mainBeta,
			"X-Claude-Code-Session-Id": session,
		}, body)
		if err != nil || code != http.StatusOK {
			t.Fatalf("call %d: status %d, err %v", i+1, code, err)
		}
		g.waitQueued(path, i+1)
	}
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	h := uiTestHandler(t, "127.0.0.1:7199", r)
	var page struct {
		Items []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"items"`
	}
	getSessionsReady(t, h, &page)
	if len(page.Items) != 1 {
		t.Fatalf("sessions = %d, want 1", len(page.Items))
	}
	var view struct {
		Overview struct {
			Title string `json:"title"`
		} `json:"overview"`
		Turns []struct {
			Title string `json:"title"`
		} `json:"turns"`
	}
	getJSON(t, h, "/api/sessions/"+page.Items[0].ID+"/view", &view)
	if len(view.Turns) != 2 {
		t.Fatalf("turns = %+v, want 2", view.Turns)
	}
	if got := view.Turns[0].Title; got != "(system) tool loaded" {
		t.Errorf("turn 1 title = %q, want %q", got, "(system) tool loaded")
	}
	if got := view.Turns[1].Title; got != "fix the login button" {
		t.Errorf("turn 2 title = %q, want %q", got, "fix the login button")
	}
	if got := view.Overview.Title; got != "fix the login button" {
		t.Errorf("session title = %q, want %q", got, "fix the login button")
	}
	// the sessions list shows the same title as the view (046 round 2 P4-1)
	if got := page.Items[0].Title; got != view.Overview.Title {
		t.Errorf("list row title = %q, want the overview's %q", got, view.Overview.Title)
	}
}
