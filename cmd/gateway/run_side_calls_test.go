package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/ui"
)

// sideCallFixtures are spec 018's request bodies, redacted copies of captured ones, in the
// order Claude Code sent them (testdata/side_calls/README.md there).
const sideCallFixtures = "../../internal/clients/claudecode/testdata/side_calls"

// The anthropic-beta lists as captured: a side call adds fallback-credit and drops
// afk-mode (018 research, Notes).
const (
	mainBeta = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,per-turn-control-2026-07-01,mid-conversation-tool-changes-2026-07-01,inline-tools-2026-09-15,advisor-tool-2026-03-01,thinking-token-count-2026-05-13,advanced-tool-use-2025-11-20,effort-2025-11-24,dangerous-tool-use-2026-09-03,thinking-display-updates-2026-08-18,afk-mode-2026-01-31,extended-cache-ttl-2025-04-11"
	sideBeta = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,per-turn-control-2026-07-01,mid-conversation-tool-changes-2026-07-01,inline-tools-2026-09-15,advisor-tool-2026-03-01,thinking-token-count-2026-05-13,advanced-tool-use-2025-11-20,effort-2025-11-24,fallback-credit-2026-06-01,dangerous-tool-use-2026-09-03,thinking-display-updates-2026-08-18,extended-cache-ttl-2025-04-11"
)

// sideCallSession is what the UI API says of the captured session: its view's turns and
// side calls, and its sessions-list row.
type sideCallSession struct {
	view struct {
		Overview struct {
			Turns     int               `json:"turns"`
			Calls     int               `json:"calls"`
			NeedsLook []json.RawMessage `json:"needs_look"`
		} `json:"overview"`
		Turns []struct {
			N      int    `json:"n"`
			Prompt string `json:"prompt"`
		} `json:"turns"`
		SideCalls []struct {
			N          int    `json:"n"`
			Name       string `json:"name"`
			ExchangeID string `json:"exchange_id"`
		} `json:"side_calls"`
	}
	row struct {
		ID   string `json:"id"`
		List struct {
			Prompts   int `json:"prompts"`
			MainCalls int `json:"main_calls"`
			SideCalls int `json:"side_calls"`
			NeedsLook int `json:"needs_look"`
		} `json:"list"`
	}
	handler http.Handler
}

func getJSON(t *testing.T, h http.Handler, path string, into any) {
	t.Helper()
	const addr = "127.0.0.1:7199"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+addr+path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
}

// getSessionsReady is getJSON for /api/sessions, asked again until no row is pending: the
// list answers at once with the cheap fields and the facts follow (spec 043).
func getSessionsReady(t *testing.T, h http.Handler, into any) {
	t.Helper()
	const addr = "127.0.0.1:7199"
	deadline := time.Now().Add(10 * time.Second)
	for {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+addr+"/api/sessions", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/sessions = %d %s", rec.Code, rec.Body)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"pending":true`)) {
			if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
				t.Fatalf("GET /api/sessions: %v", err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET /api/sessions: rows still pending after 10 s: %s", rec.Body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// captureSideCallSession sends the five fixtures through run as Claude Code sends them,
// stops the gateway, and reads the session back through the UI API with the registered
// profiles.
func captureSideCallSession(t *testing.T) sideCallSession {
	t.Helper()
	const (
		path    = "/anthropic/v1/messages"
		session = "00000000-0000-4000-8000-000000000018"
	)
	dir := t.TempDir()
	// Every call streams the recorded answer, except the away summary (the second), which
	// ends in the recorded mid-stream error: a failed side call must raise nothing either.
	ok := upstreamAnswer{contentType: "text/event-stream; charset=utf-8", body: fixture(t, "stream.sse")}
	failed := upstreamAnswer{contentType: ok.contentType, body: fixture(t, "error/stream_error.sse")}
	var n atomic.Int32
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 2 {
			failed.handler(w, r)
			return
		}
		ok.handler(w, r)
	})
	names, err := filepath.Glob(filepath.Join(sideCallFixtures, "*.json"))
	if err != nil || len(names) != 5 {
		t.Fatalf("fixtures %v, %v; want 5", names, err)
	}
	slices.Sort(names)
	bodies := make([][]byte, len(names))
	for i, name := range names {
		if bodies[i], err = os.ReadFile(name); err != nil {
			t.Fatal(err)
		}
	}
	g, url := servedWith(t, captureEnv(dir, up, nil)) // runs in a temp dir: read fixtures first
	for i, body := range bodies {
		name := names[i]
		beta := mainBeta
		if i == 1 || i == 3 {
			beta = sideBeta
		}
		code, err := do(t, http.MethodPost, url+path+"?beta=true", map[string]string{
			"Content-Type":             "application/json",
			"User-Agent":               "claude-cli/2.1.325 (external, cli)",
			"X-App":                    "cli",
			"Anthropic-Version":        "2023-06-01",
			"Anthropic-Beta":           beta,
			"X-Claude-Code-Session-Id": session,
		}, string(body))
		if err != nil || code != http.StatusOK {
			t.Fatalf("%s: status %d, err %v", filepath.Base(name), code, err)
		}
		g.waitQueued(path, i+1) // one at a time, so the calls are stored in order
	}
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	var s sideCallSession
	s.handler = ui.Handler(ui.Deps{Addr: "127.0.0.1:7199", Reader: r, Rules: uiRules()})
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	getSessionsReady(t, s.handler, &page)
	if len(page.Items) != 1 {
		t.Fatalf("sessions = %d, want 1", len(page.Items))
	}
	if err := json.Unmarshal(page.Items[0], &s.row); err != nil {
		t.Fatal(err)
	}
	getJSON(t, s.handler, "/api/sessions/"+s.row.ID+"/view", &s.view)
	return s
}

// 018 AC1–AC4, AC6: of the five captured calls, the away summary and the prompt suggestion
// are side calls; the three real prompts, the one quoting both markers included, are the
// session's only turns, prompts and main calls, and nothing needs a look, though the away
// summary failed.
func TestRun_AwaySummaryAndPromptSuggestionAreSideCalls(t *testing.T) {
	s := captureSideCallSession(t)
	v := s.view
	if v.Overview.Calls != 5 {
		t.Errorf("calls = %d, want 5", v.Overview.Calls)
	}
	var prompts []string
	for _, turn := range v.Turns {
		prompts = append(prompts, turn.Prompt)
	}
	want := []string{
		"Fix the retry in the upstream client.",
		`Why does a recap request start with "The user stepped away and is coming back." and a guess with "[SUGGESTION MODE:"?`,
		"Commit this.",
	}
	if v.Overview.Turns != 3 || !slices.Equal(prompts, want) {
		t.Errorf("turns = %d %q, want 3 %q", v.Overview.Turns, prompts, want)
	}
	var side []string
	for _, c := range v.SideCalls {
		side = append(side, c.Name)
	}
	if !slices.Equal(side, []string{"away summary", "prompt suggestion"}) {
		t.Errorf("side calls = %q, want away summary, prompt suggestion", side)
	}
	if len(v.Overview.NeedsLook) != 0 {
		t.Errorf("needs a look = %d items, want none", len(v.Overview.NeedsLook))
	}
	l := s.row.List
	if l.Prompts != 3 || l.MainCalls != 3 || l.SideCalls != 2 || l.NeedsLook != 0 {
		t.Errorf("row list = %+v, want 3 prompts, 3 main calls, 2 side calls, 0 needs a look", l)
	}

	// Each side call still opens in the call details dialog, as a side call by its name.
	for _, c := range v.SideCalls {
		var call struct {
			Facts struct {
				Thread struct{ Kind, Label string } `json:"thread"`
			} `json:"facts"`
		}
		getJSON(t, s.handler, "/api/sessions/"+s.row.ID+"/calls/"+c.ExchangeID, &call)
		if th := call.Facts.Thread; th.Kind != "side" || th.Label != c.Name {
			t.Errorf("call #%d thread = %+v, want side %q", c.N, th, c.Name)
		}
	}
}
