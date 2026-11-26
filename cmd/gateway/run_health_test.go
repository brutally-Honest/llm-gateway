package main

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/observability"
)

// counterValue is the value of family name's series carrying want, or -1 if absent.
func counterValue(t *testing.T, url, name string, want map[string]string) float64 {
	t.Helper()
	for _, m := range scrape(t, url)[name].GetMetric() {
		got := labelsOf(m)
		match := true
		for k, v := range want {
			match = match && got[k] == v
		}
		if match {
			return m.GetCounter().GetValue()
		}
	}
	return -1
}

// Spec 045 AC4-AC6: a forced hook drop shows in the counters, /healthz and /api/health,
// and /healthz stays 200.
func TestRun_LossShowsInMetricsAndHealth(t *testing.T) {
	dir, env := hookEnv(t, map[string]string{
		"GATEWAY_HOOKS_QUEUE_SIZE": "1", "GATEWAY_HOOKS_WORKERS": "1",
		"GATEWAY_UI_LISTEN_ADDR":                   freeAddr(t),
		"GATEWAY_EFFECTS_NETWORK_HTTP_PROXY_ADDR":  freeAddr(t),
		"GATEWAY_EFFECTS_NETWORK_SOCKS_PROXY_ADDR": freeAddr(t),
	})
	for k, v := range captureEnv(dir, jsonUpstream(t), env) {
		env[k] = v
	}
	g, url := startCapturing(t, env, openStalledHooks)
	ui := "http://" + uiAddr(g)

	// Nothing lost yet: ok, and every new series exists at 0.
	var h observability.Health
	fetchJSON(t, url+"/healthz", &h)
	if h.Status != "ok" || h.Lost != 0 {
		t.Fatalf("before: %+v", h)
	}
	for _, s := range []struct {
		name   string
		labels map[string]string
	}{
		{"gateway_hook_events_dropped_total", map[string]string{"reason": "queue_full"}},
		{"gateway_hook_events_dropped_total", map[string]string{"reason": "memory"}},
		{"gateway_hook_events_dropped_total", map[string]string{"reason": "oversize"}},
		{"gateway_hook_events_dropped_total", map[string]string{"reason": "unreadable"}},
		{"gateway_store_write_failures_total", nil},
		{"gateway_blob_write_failures_total", nil},
		{"gateway_capture_dropped_total", map[string]string{"reason": "effects"}},
	} {
		if v := counterValue(t, url+"/metrics", s.name, s.labels); v != 0 {
			t.Errorf("%s%v = %v before any loss, want 0", s.name, s.labels, v)
		}
	}

	// One in the stalled worker, one queued, the rest refused.
	for range 6 {
		if code, err := do(t, http.MethodPost, url+hookPath, nil, hookBody); err != nil || code != http.StatusOK {
			t.Fatalf("hook status %d, err %v", code, err)
		}
	}
	dropped := counterValue(t, url+"/metrics", "gateway_hook_events_dropped_total", map[string]string{"reason": "queue_full"})
	if dropped < 4 {
		t.Fatalf("queue_full = %v, want >= 4", dropped)
	}
	for _, other := range []string{"memory", "oversize", "unreadable"} {
		if v := counterValue(t, url+"/metrics", "gateway_hook_events_dropped_total", map[string]string{"reason": other}); v != 0 {
			t.Errorf("reason %s = %v, want 0", other, v)
		}
	}

	resp, err := http.Get(url + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200 while degraded", resp.StatusCode)
	}
	if err := json.Unmarshal(b, &h); err != nil {
		t.Fatal(err)
	}
	if h.Status != "degraded" || h.Lost != int64(dropped) || h.Counts.HookEventsDropped != int64(dropped) ||
		h.Counts.CapturesDropped != 0 || h.Reason != "hook events dropped" {
		t.Errorf("/healthz = %s", b)
	}

	var api struct {
		OK bool `json:"ok"`
		observability.Health
	}
	fetchJSON(t, ui+"/api/health", &api)
	if !api.OK || api.Status != "degraded" || api.Counts.HookEventsDropped < 4 {
		t.Errorf("/api/health = %+v", api)
	}
}

func fetchJSON(t *testing.T, url string, into any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
}
