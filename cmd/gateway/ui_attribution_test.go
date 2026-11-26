package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/clients/claudecode"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/ui"
)

// uiTestHandler is the UI handler on r with its caches made here, so cleanup can cancel
// and wait for their background work before the reader (registered earlier) closes (spec
// 047 F1).
func uiTestHandler(t *testing.T, addr string, r *store.Reader) http.Handler {
	t.Helper()
	d := ui.Deps{Addr: addr, Reader: r, Rules: uiRules(), Ctx: t.Context(),
		Digests: ui.NewDigestCache(t.Context(), nil), Timelines: ui.NewTimelineCache(t.Context(), nil), Views: ui.NewViewCache()}
	t.Cleanup(func() {
		// t.Context() is already cancelled here, so the work is winding down.
		bg := context.Background()
		if !d.Views.Wait(bg) || !d.Digests.Wait(bg) || !d.Timelines.Wait(bg) {
			t.Error("background work did not finish")
		}
	})
	return ui.Handler(d)
}

// spec 015 AC9, research.md Q5: the proxy stores an unassigned connection under client
// "unknown", which has no rules; with the profiles the gateway registers, a connection to
// one of Claude Code's own hosts is still harness, naming claude-code, and any other host
// carries no label.
func TestUI_UnassignedHarnessUsesRegisteredProfiles(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	conn := func(host string) core.Effect {
		return core.Effect{Kind: core.EffectNetConn, At: at, EndedAt: at.Add(time.Second), Principal: core.PrincipalLocal,
			Client: "unknown", Host: host, Port: 443, Via: "connect", Outcome: core.NetOK}
	}
	if err := s.SaveEffects(t.Context(), []core.Effect{conn("downloads.claude.ai"), conn("example.com")}, nil); err != nil {
		t.Fatal(err)
	}
	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	const addr = "127.0.0.1:7199"
	h := uiTestHandler(t, addr, r)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+addr+"/api/effects/unassigned", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET unassigned = %d %s", rec.Code, rec.Body)
	}
	var page struct {
		Effects []struct {
			Host        string `json:"host"`
			Attribution *struct {
				Label  string `json:"label"`
				Client string `json:"client"`
			} `json:"attribution"`
		} `json:"effects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Effects) != 2 {
		t.Fatalf("unassigned = %s, want 2", rec.Body)
	}
	if a := page.Effects[0].Attribution; a == nil || a.Label != "harness" || a.Client != claudecode.Name {
		t.Errorf("%s = %+v, want harness %s", page.Effects[0].Host, a, claudecode.Name)
	}
	if a := page.Effects[1].Attribution; a != nil {
		t.Errorf("%s = %+v, want none", page.Effects[1].Host, a)
	}
}
