package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Spec 030 AC2, AC7, AC10: the UI answers as soon as it is up, with no warm-up gate; the
// view cache lives under capture.dir with 0700 folders and 0600 files; the pass records
// its duration and the reads are counted.
func TestRun_UIServesAtOnceAndKeepsAViewCache(t *testing.T) {
	const sessionKey = "4f1c2a9e-77b0-4c1e-9d2a-1b2c3d4e5f60"
	dir := t.TempDir()
	g, url := metricsGateway(t, dir, jsonUpstream(t))
	ui := "http://" + uiAddr(g)

	// answered at once, whatever the background pass is doing
	resp, err := http.Get(ui + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/sessions = %d, want 200", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPost, url+"/anthropic/v1/messages", strings.NewReader(`{"model":"claude-opus-5-5","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-cli/2.1.0 (external, cli)")
	req.Header.Set("X-Claude-Code-Session-Id", sessionKey)
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, r2.Body)
	_ = r2.Body.Close()
	waitSeries(t, url+"/metrics", "gateway_store_write_seconds", map[string]string{"op": "parse", "outcome": "ok"})

	// a list request builds the session's memo on demand and the writer files it
	var memo string
	deadline := time.Now().Add(10 * time.Second)
	for memo == "" {
		if r, err := http.Get(ui + "/api/sessions"); err == nil {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}
		if m, _ := filepath.Glob(filepath.Join(dir, "view-cache", "*", "m", "*")); len(m) > 0 {
			memo = m[0]
		}
		if memo == "" && time.Now().After(deadline) {
			t.Fatal("no memo file under capture.dir/view-cache")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, p := range []string{memo, filepath.Dir(memo), filepath.Dir(filepath.Dir(memo)), filepath.Join(dir, "view-cache")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s is %v, want %v", p, info.Mode().Perm(), want)
		}
	}
	waitCount(t, url+"/metrics", "gateway_ui_view_cache_reads_total", map[string]string{"kind": "memo", "result": "miss"})
}
