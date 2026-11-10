package main

import (
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
	h := ui.Handler(ui.Deps{Addr: addr, Reader: r, Rules: uiRules()})
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
