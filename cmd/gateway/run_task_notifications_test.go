package main

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/ui"
)

// taskNoticeFixtures are spec 018's task-notification session: hook payloads (*-hook-*)
// and request bodies (*-call-*), redacted from captured ones, in the order Claude Code
// sent them (testdata/task_notifications/README.md there).
const taskNoticeFixtures = "../../internal/clients/claudecode/testdata/task_notifications"

// taskNoticeView is what the UI API says of the captured session.
type taskNoticeView struct {
	Overview struct {
		Turns     int `json:"turns"`
		NeedsLook []struct {
			Kind  string `json:"kind"`
			Title string `json:"title"`
		} `json:"needs_look"`
	} `json:"overview"`
	Turns []struct {
		N          int    `json:"n"`
		Prompt     string `json:"prompt"`
		HooksOnly  bool   `json:"hooks_only"`
		Background bool   `json:"background"`
		Steps      []struct {
			N int `json:"n"`
		} `json:"steps"`
	} `json:"turns"`
}

// 018 AC7–AC10: task notices that arrive while their turn is open (two at once, as Claude
// Code sends tasks that finished together) go with that turn; one
// that arrives after the turn ended gets a background turn of its own; neither counts as a
// prompt or needs a look, while a typed prompt no captured call answered is still a
// hooks-only turn that does.
func TestRun_TaskNotificationsStayWithTheirCalls(t *testing.T) {
	const (
		path    = "/anthropic/v1/messages"
		session = "00000000-0000-4000-8000-000000000018"
	)
	names, err := filepath.Glob(filepath.Join(taskNoticeFixtures, "*.json"))
	if err != nil || len(names) != 8 {
		t.Fatalf("fixtures %v, %v; want 8", names, err)
	}
	slices.Sort(names)
	bodies := make([]string, len(names))
	for i, name := range names {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		bodies[i] = strings.ReplaceAll(string(b), `"SESSION"`, `"`+session+`"`)
	}

	// The first call answers with a tool call (its turn stays open); the others with text.
	sse := "text/event-stream; charset=utf-8"
	tool := upstreamAnswer{contentType: sse, body: fixture(t, "tool_turn/response.sse")}
	text := upstreamAnswer{contentType: sse, body: fixture(t, "stream.sse")}
	var n atomic.Int32
	up := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			tool.handler(w, r)
			return
		}
		text.handler(w, r)
	})
	dir := t.TempDir()
	g, url := servedWith(t, captureEnv(dir, up, map[string]string{"GATEWAY_EFFECTS_ENABLED": "false"}))
	calls := 0
	for i, name := range names {
		base := filepath.Base(name)
		if strings.Contains(base, "-hook-") {
			if code, err := do(t, http.MethodPost, url+hookPath, nil, bodies[i]); err != nil || code != http.StatusOK {
				t.Fatalf("%s: status %d, err %v", base, code, err)
			}
			continue
		}
		code, err := do(t, http.MethodPost, url+path+"?beta=true", map[string]string{
			"Content-Type":             "application/json",
			"User-Agent":               "claude-cli/2.1.325 (external, cli)",
			"X-App":                    "cli",
			"Anthropic-Version":        "2023-06-01",
			"Anthropic-Beta":           mainBeta,
			"X-Claude-Code-Session-Id": session,
		}, bodies[i])
		if err != nil || code != http.StatusOK {
			t.Fatalf("%s: status %d, err %v", base, code, err)
		}
		calls++
		g.waitQueued(path, calls)
	}
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	h := ui.Handler(ui.Deps{Addr: "127.0.0.1:7199", Reader: r, Rules: uiRules()})
	var page struct {
		Items []struct {
			ID   string `json:"id"`
			List struct {
				Prompts   int `json:"prompts"`
				MainCalls int `json:"main_calls"`
				NeedsLook int `json:"needs_look"`
			} `json:"list"`
		} `json:"items"`
	}
	getSessionsReady(t, h, &page)
	if len(page.Items) != 1 {
		t.Fatalf("sessions = %d, want 1", len(page.Items))
	}
	var v taskNoticeView
	getJSON(t, h, "/api/sessions/"+page.Items[0].ID+"/view", &v)

	type turn struct {
		prompt                string
		steps                 int
		hooksOnly, background bool
	}
	var got []turn
	for _, x := range v.Turns {
		p := x.Prompt
		if i := strings.IndexByte(p, '\n'); i >= 0 {
			p = p[:i]
		}
		got = append(got, turn{p, len(x.Steps), x.HooksOnly, x.Background})
	}
	want := []turn{
		{"Run the slow check in the background, then read the config.", 2, false, false},
		{"<task-notification>", 1, false, true},
		{"Commit this.", 0, true, false},
	}
	if !slices.Equal(got, want) {
		t.Errorf("turns = %+v\nwant    %+v", got, want)
	}
	if v.Overview.Turns != 2 {
		t.Errorf("overview turns (prompts) = %d, want 2", v.Overview.Turns)
	}
	if l := v.Overview.NeedsLook; len(l) != 1 || l[0].Kind != "hooks_only_turn" || !strings.HasPrefix(l[0].Title, "Turn 3 ") {
		t.Errorf("needs a look = %+v, want only turn 3's hooks-only item", l)
	}
	if l := page.Items[0].List; l.Prompts != 2 || l.MainCalls != 3 || l.NeedsLook != 1 {
		t.Errorf("row list = %+v, want 2 prompts, 3 main calls, 1 needs a look", l)
	}
}
