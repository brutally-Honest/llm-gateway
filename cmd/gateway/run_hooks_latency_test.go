//go:build !race

package main

import (
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

func hookP99(t *testing.T, url string) time.Duration {
	t.Helper()
	post := func() time.Duration {
		start := time.Now()
		resp, err := http.Post(url+hookPath, "application/json", strings.NewReader(hookBody))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		return time.Since(start)
	}
	for range 100 {
		post()
	}
	d := make([]time.Duration, 1000)
	for i := range d {
		d[i] = post()
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	return d[989]
}

// AC3: p99 under 10 ms with storage healthy, and with storage stalled behind a
// 64-slot queue (most of the run takes the full-queue path).
func TestRun_HookLatencyP99HealthyAndStalled(t *testing.T) {
	for name, open := range map[string]bool{"healthy": false, "stalled": true} {
		t.Run(name, func(t *testing.T) {
			_, env := hookEnv(t, map[string]string{"GATEWAY_HOOKS_QUEUE_SIZE": "64"})
			var g *gateway
			var url string
			if open {
				g, url = startCapturing(t, env, openStalledHooks)
			} else {
				g, url = startCapturing(t, env, nil)
			}
			if p := hookP99(t, url); p >= 10*time.Millisecond {
				t.Errorf("p99 = %v, want < 10ms", p)
			}
			g.stop()
		})
	}
}
