package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/clients/claudecode"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

var hookPath = "/_gateway/hooks/" + claudecode.Name

// hookBody is a Claude Code hook delivery; the fixture is the one place its event
// name is spelled, so the expected values below are parsed from it.
var hookBody, hookFixture = loadHookFixture()

type hookFields struct {
	Event     string `json:"hook_event_name"`
	SessionID string `json:"session_id"`
	ToolUseID string `json:"tool_use_id"`
	ToolName  string `json:"tool_name"`
}

func loadHookFixture() (string, hookFields) {
	b, err := os.ReadFile("../../internal/clients/claudecode/testdata/hook_tool_start.json")
	if err != nil {
		panic(err)
	}
	var f hookFields
	if err := json.Unmarshal(b, &f); err != nil {
		panic(err)
	}
	return string(b), f
}

// stallHooks is the real store with SaveHook held until its context is cancelled.
type stallHooks struct{ *store.Store }

func (s stallHooks) SaveHook(ctx context.Context, _ *store.HookRecord) error {
	<-ctx.Done()
	return ctx.Err()
}

func openStalledHooks(dir string, log *zap.Logger) (capture.Store, error) {
	st, err := store.Open(dir, log)
	if err != nil {
		return nil, err
	}
	return stallHooks{st}, nil
}

func hookEnv(t *testing.T, extra map[string]string) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	env := map[string]string{"GATEWAY_CAPTURE_DIR": dir, "GATEWAY_SHUTDOWN_TIMEOUT": "500ms"}
	for k, v := range extra {
		env[k] = v
	}
	return dir, env
}

// AC2, AC10: a delivery to the ingest route is stored mapped, and shutdown drains it.
func TestRun_HooksIngestStoresEvent(t *testing.T) {
	dir, env := hookEnv(t, nil)
	g, url := startCapturing(t, env, nil)
	if code, err := do(t, http.MethodPost, url+hookPath, nil, hookBody); err != nil || code != http.StatusOK {
		t.Fatalf("status %d, err %v", code, err)
	}
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
	rows, err := openReader(t, dir).HookEvents(core.PrincipalLocal, claudecode.Name, hookFixture.SessionID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %v, err %v; want one", rows, err)
	}
	if rows[0].ToolName != hookFixture.ToolName || rows[0].ToolUseID != hookFixture.ToolUseID || rows[0].SourceName != hookFixture.Event {
		t.Errorf("row = %+v", rows[0])
	}
}

// AC8: with capture off the route still answers 200 and counts it.
func TestRun_CaptureOffHooksAnswers200(t *testing.T) {
	g, url := servedWith(t, map[string]string{"GATEWAY_CAPTURE_ENABLED": "false"})
	if code, err := do(t, http.MethodPost, url+hookPath, nil, hookBody); err != nil || code != http.StatusOK {
		t.Fatalf("status %d, err %v", code, err)
	}
	if code := g.stop(); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}
}

// AC9, AC8: with capture off, shutdown still logs the hook counters on a line of
// their own, with every capture-off delivery counted.
func TestRun_CaptureOffLogsHookCounters(t *testing.T) {
	g, url := servedWith(t, map[string]string{"GATEWAY_CAPTURE_ENABLED": "false"})
	for range 3 {
		if code, err := do(t, http.MethodPost, url+hookPath, nil, hookBody); err != nil || code != http.StatusOK {
			t.Fatalf("status %d, err %v", code, err)
		}
	}
	g.stop()
	lines := linesWith(g, "hook ingest stopped")
	if len(lines) != 1 {
		t.Fatalf("got %d hook ingest stopped lines, want 1\n%s", len(lines), g.stdout.String())
	}
	if n, _ := lines[0]["hooks_capture_off"].(float64); n != 3 {
		t.Errorf("hooks_capture_off = %v, want 3", lines[0]["hooks_capture_off"])
	}
	if n := len(linesWith(g, "capture stopped")); n != 0 {
		t.Errorf("got %d capture stopped lines with capture off", n)
	}
}

// AC9: shutdown logs the hook counters on the capture stopped line.
func TestRun_ShutdownLogsHookCounters(t *testing.T) {
	_, env := hookEnv(t, map[string]string{
		"GATEWAY_HOOKS_QUEUE_SIZE": "1", "GATEWAY_HOOKS_WORKERS": "1",
	})
	g, url := startCapturing(t, env, openStalledHooks)
	for range 5 {
		if code, err := do(t, http.MethodPost, url+hookPath, nil, hookBody); err != nil || code != http.StatusOK {
			t.Fatalf("status %d, err %v", code, err)
		}
	}
	g.stop()
	line := stoppedLine(t, g)
	for _, k := range []string{"hooks_queue_full", "hooks_oversize", "hooks_capture_off", "hooks_not_json", "hooks_store_failed"} {
		if _, ok := line[k]; !ok {
			t.Errorf("capture stopped has no %s: %v", k, line)
		}
	}
	// One in the worker, one queued, the other three refused.
	if n, _ := line["hooks_queue_full"].(float64); n < 3 {
		t.Errorf("hooks_queue_full = %v, want >= 3", line["hooks_queue_full"])
	}
	if n, _ := line["hooks_store_failed"].(float64); n < 1 {
		t.Errorf("hooks_store_failed = %v, want >= 1 (stalled save cancelled at shutdown)", line["hooks_store_failed"])
	}
}

// E13: a burst of hooks into a full queue never costs an LLM exchange its capture.
func TestRun_HookBurstDoesNotDropLLMCapture(t *testing.T) {
	const path = "/anthropic/v1/messages"
	dir, env := hookEnv(t, map[string]string{"GATEWAY_HOOKS_QUEUE_SIZE": "1"})
	up := jsonUpstream(t)
	env["GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL"] = up.URL
	g, url := startCapturing(t, env, openStalledHooks)
	for range 200 {
		if code, err := do(t, http.MethodPost, url+hookPath, nil, hookBody); err != nil || code != http.StatusOK {
			t.Fatalf("hook status %d, err %v", code, err)
		}
	}
	if code, err := do(t, http.MethodPost, url+path, nil, `{"hello":"world"}`); err != nil || code != http.StatusOK {
		t.Fatalf("llm status %d, err %v", code, err)
	}
	g.waitQueued(path, 1)
	g.stop()
	r := openReader(t, dir)
	for _, id := range g.requestIDs(path) {
		if _, err := r.Exchange(id); err != nil {
			t.Errorf("exchange %s not stored: %v", id, err)
		}
	}
	if n, _ := stoppedLine(t, g)["hooks_queue_full"].(float64); n < 100 {
		t.Errorf("hooks_queue_full = %v, want a burst of drops", n)
	}
}
