package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/startup"
)

func installed(t *testing.T) *fake {
	t.Helper()
	f := newFake(t, "linux")
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("install exit %d\n%s", code, f.out)
	}
	f.write(".local/share/llm-gateway/store.db", "data")
	f.path["gateway"] = true
	lp := f.env.LookPath
	f.env.LookPath = func(cmd string) (string, error) {
		if cmd == "gateway" && f.path["gateway"] {
			return filepath.Join(f.env.Home, ".local", "bin", "gateway"), nil
		}
		return lp(cmd)
	}
	return f
}

func results(f *fake) map[string]Result {
	out := map[string]Result{}
	for _, r := range Diagnose(f.env, testClient()) {
		out[r.ID] = r
	}
	return out
}

// A fresh install passes every check that matters, and doctor exits 0.
func TestDoctorAllPass(t *testing.T) {
	f := installed(t)
	for id, r := range results(f) {
		if r.Level != Pass {
			t.Errorf("%s: %s %s", id, r.Level, r.Detail)
		}
		if !strings.HasPrefix(r.Link, DocsURL+"#check-") {
			t.Errorf("%s: link %s", id, r.Link)
		}
	}
	f.out.Reset()
	if code := Doctor(f.env, testClient(), DoctorOptions{}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
}

// healthzFake serves body for the API's /healthz through Env.Fetch.
func healthzFake(f *fake, body string) {
	f.env.Fetch = func(url string) (string, error) {
		if url == "http://127.0.0.1:7197/healthz" {
			return body, nil
		}
		return "", os.ErrDeadlineExceeded
	}
}

// Spec 045 D11: doctor reports what capture has lost, from the /healthz body.
func TestDoctorCaptureHealth(t *testing.T) {
	const okBody = `{"status":"ok","since":"2026-11-26T09:00:00Z","lost":0,"counts":{"hook_events_dropped":0,"captures_dropped":0,"store_write_failures":0,"blob_write_failures":0}}`
	const badBody = `{"status":"degraded","since":"2026-11-26T09:00:00Z","lost":7,"counts":{"hook_events_dropped":0,"captures_dropped":5,"store_write_failures":2,"blob_write_failures":0},"reason":"captures dropped, store writes failed"}`

	f := installed(t)
	healthzFake(f, okBody)
	r := results(f)["capture-health"]
	if r.Level != Pass || r.Detail != "no capture data lost since 2026-11-26T09:00:00Z" || r.Name != "Capture health" {
		t.Fatalf("ok: %+v", r)
	}
	f.out.Reset()
	if code := Doctor(f.env, testClient(), DoctorOptions{}); code != ExitOK || !strings.Contains(f.out.String(), "Capture health") || !strings.Contains(f.out.String(), "no capture data lost since 2026-11-26T09:00:00Z") {
		t.Fatalf("plain ok: exit %d\n%s", code, f.out)
	}

	healthzFake(f, badBody)
	r = results(f)["capture-health"]
	wantDetail := "7 lost since 2026-11-26T09:00:00Z: captures dropped, store writes failed (hook events dropped 0, captures dropped 5, store writes failed 2, blob writes failed 0)"
	if r.Level != Warn || r.Detail != wantDetail || !strings.Contains(r.Fix, "docs/observability.md, Capture health") {
		t.Fatalf("degraded: %+v", r)
	}
	f.out.Reset()
	if code := Doctor(f.env, testClient(), DoctorOptions{}); code != ExitOK { // a warn is not a fail
		t.Fatalf("degraded exit %d", code)
	}
	if !strings.Contains(f.out.String(), wantDetail) {
		t.Fatalf("plain output lacks the degraded line:\n%s", f.out)
	}
	f.out.Reset()
	Doctor(f.env, testClient(), DoctorOptions{JSON: true})
	var js struct{ Checks []Result }
	if err := json.Unmarshal(f.out.Bytes(), &js); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range js.Checks {
		found = found || (c.ID == "capture-health" && c.Level == Warn)
	}
	if !found {
		t.Fatalf("json output lacks capture-health warn:\n%s", f.out)
	}

	// An older gateway's body, or garbage, or no answer: no result, no failure.
	for _, body := range []string{`{"status":"ok"}`, `not json`, ``} {
		healthzFake(f, body)
		if r, ok := results(f)["capture-health"]; ok {
			t.Errorf("body %q gave %+v", body, r)
		}
	}
	f.env.Fetch = nil
	if _, ok := results(f)["capture-health"]; ok {
		t.Error("nil Fetch gave a capture-health result")
	}
}

// Each check fails (or warns) when what it checks is broken, with a fix and a link; a
// fail makes doctor exit non-zero (spec AC21, AC22).
func TestDoctorEachCheckFails(t *testing.T) {
	cases := []struct {
		id     string
		level  Level
		break_ func(f *fake)
	}{
		{"platform", Fail, func(f *fake) { f.env.GOARCH = "386" }},
		{"client", Fail, func(f *fake) { delete(f.path, "claude") }},
		{"curl", Fail, func(f *fake) { delete(f.path, "curl") }},
		{"service-manager", Fail, func(f *fake) { f.svc.ok = false }},
		{"service", Fail, func(f *fake) { f.svc.running = false }},
		{"shell", Warn, func(f *fake) { f.setEnv("SHELL", "/usr/bin/fish") }},
		{"git", Warn, func(f *fake) { delete(f.path, "git") }},
		{"bwrap", Warn, func(f *fake) { delete(f.path, "bwrap") }},
		{"socat", Warn, func(f *fake) { delete(f.path, "socat") }},
		{"apparmor", Warn, func(f *fake) { f.restrictUserns() }},
		{"docker", Warn, func(f *fake) { delete(f.path, "docker") }},
		{"binary", Fail, func(f *fake) {
			f.path["gateway"] = false
			_ = os.Remove(filepath.Join(f.env.Home, ".local/bin/gateway"))
		}},
		{"config", Fail, func(f *fake) { f.write(".config/llm-gateway/config.yaml", "listen_addr: 0.0.0.0:7197\n") }},
		{"listener-api", Fail, func(f *fake) { f.healthy = false }},
		{"listener-http-proxy", Fail, func(f *fake) { f.env.Dial = func(string) bool { return false } }},
		{"settings", Fail, func(f *fake) { f.write(".config/llm-gateway/claude-settings.json", "{}") }},
		{"function", Fail, func(f *fake) { f.write(".zshrc", f.read(".zshrc")+"claude() {\n  x\n}\n") }},
		{"hooks", Fail, func(f *fake) {
			f.env.HTTPStatus = func(url string) (int, error) {
				if strings.Contains(url, "hooks") {
					return 404, nil
				}
				return 200, nil
			}
		}},
		{"store", Warn, func(f *fake) { f.env.FreeBytes = func(string) (uint64, bool) { return 1 << 20, true } }},
		{"leftover", Warn, func(f *fake) { f.write(".bashrc", "claude() {\n  x\n}\n") }},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			f := installed(t)
			c.break_(f)
			r := results(f)[c.id]
			if r.Level != c.level || r.Fix == "" || r.Link == "" {
				t.Fatalf("%s: got %+v, want %s with a fix", c.id, r, c.level)
			}
			f.out.Reset()
			code := Doctor(f.env, testClient(), DoctorOptions{})
			if (c.level == Fail) != (code != ExitOK) {
				t.Fatalf("exit %d for a %s\n%s", code, c.level, f.out)
			}
		})
	}
}

func TestDoctorServiceStates(t *testing.T) {
	f := installed(t)
	f.svc.running = false
	if r := results(f)["service"]; r.Level != Fail || r.Detail != "loaded, not running" {
		t.Fatalf("%+v", r)
	}
	_ = os.Remove(f.svc.File(f.paths()))
	if r := results(f)["service"]; r.Level != Fail || !strings.Contains(r.Detail, "not found") {
		t.Fatalf("%+v", r)
	}
	// Installed with --no-service: a warning, not a fail.
	n := newFake(t, "linux")
	n.install(InstallOptions{Yes: true, NoService: true})
	if r := results(n)["service"]; r.Level != Warn {
		t.Fatalf("%+v", r)
	}
	// A hand-made unit is an older install left over.
	h := newFake(t, "linux")
	h.write(".config/systemd/user/llm-gateway.service", "[Service]\n")
	if r := results(h)["leftover"]; r.Level != Warn || !strings.Contains(r.Detail, "not made by install") {
		t.Fatalf("%+v", r)
	}
}

func TestDoctorJSON(t *testing.T) {
	f := installed(t)
	f.healthy = false
	f.out.Reset()
	if code := Doctor(f.env, testClient(), DoctorOptions{JSON: true}); code != ExitStopped {
		t.Fatalf("exit %d", code)
	}
	var doc struct {
		OK     bool     `json:"ok"`
		Checks []Result `json:"checks"`
	}
	if err := json.Unmarshal(f.out.Bytes(), &doc); err != nil {
		t.Fatalf("%v\n%s", err, f.out)
	}
	if doc.OK || len(doc.Checks) < 15 {
		t.Fatalf("ok %v, %d checks", doc.OK, len(doc.Checks))
	}
}

// After uninstall, doctor shows the install gone and the data kept.
func TestDoctorAfterUninstall(t *testing.T) {
	f := installed(t)
	f.uninstall(UninstallOptions{Yes: true})
	r := results(f)
	if r["service"].Level != Fail || r["function"].Level != Fail || r["settings"].Level != Fail {
		t.Fatalf("service %s, function %s, settings %s", r["service"].Level, r["function"].Level, r["settings"].Level)
	}
	if r["store"].Level != Pass || !strings.Contains(r["store"].Detail, "4 B used") {
		t.Fatalf("store %+v", r["store"])
	}
}

// Every link doctor prints lands on a "### Check: <id>" section of docs/install.md, and
// no two checks share an id, on either OS (spec AC21, AC26).
func TestDoctorLinksMatchDocs(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "install.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, goos := range []string{"linux", "darwin"} {
		f := installed(t)
		f.env.GOOS = goos
		f.env.Service = &fakeService{systemd: systemd{env: f.env}, ok: true, running: true, enabled: true}
		seen := map[string]bool{}
		for _, r := range Diagnose(f.env, testClient()) {
			if seen[r.ID] {
				t.Errorf("%s: id %s twice", goos, r.ID)
			}
			seen[r.ID] = true
			id := strings.TrimPrefix(r.Link, DocsURL+"#check-")
			if !strings.Contains(string(doc), "\n### Check: "+id+"\n") {
				t.Errorf("%s: %s links to #check-%s, which docs/install.md lacks", goos, r.ID, id)
			}
		}
	}
}

// The UI listener is checked on its own health route, /api/health, which the UI serves;
// it has no /healthz (found on the real-machine check).
func TestDoctorUIUsesItsHealthRoute(t *testing.T) {
	f := installed(t)
	f.env.HTTPStatus = func(url string) (int, error) {
		if strings.Contains(url, ":7199/") && !strings.HasSuffix(url, "/api/health") {
			return 404, nil
		}
		return 200, nil
	}
	if r := results(f)["listener-ui"]; r.Level != Pass {
		t.Fatalf("%+v", r)
	}
	f.env.HTTPStatus = func(url string) (int, error) {
		if strings.Contains(url, ":7199/") {
			return 0, os.ErrDeadlineExceeded
		}
		return 200, nil
	}
	// API up, UI silent, no start time to read: most likely the gateway is still starting, so a warn.
	r := results(f)["listener-ui"]
	if r.Level != Warn || !strings.Contains(r.Detail, "most likely warming up") || !strings.Contains(r.Fix, "may still be starting") {
		t.Fatalf("%+v", r)
	}
	lacksUIWarmupClaim(t, r)
}

// lacksUIWarmupClaim fails when a UI-listener Fix claims a start-up warm-up or a 30 s wait:
// the UI serves as soon as it binds (spec 030).
func lacksUIWarmupClaim(t *testing.T, r Result) {
	t.Helper()
	for _, bad := range []string{"30 s", "warm-up"} {
		if strings.Contains(r.Fix, bad) {
			t.Fatalf("Fix claims %q: %+v", bad, r)
		}
	}
}

// A UI that does not answer while the API does is warming up for the first 120 s after
// the gateway's start (a warn, "about N s"), and broken after (a fail); with the API down
// too it is a plain fail (user request after the real-machine check, D26).
func TestDoctorUIWarmingUp(t *testing.T) {
	now := time.Unix(1_795_185_000, 0)
	setup := func(sinceStart time.Duration, apiUp bool) Result {
		f := installed(t)
		f.env.Now = func() time.Time { return now }
		f.env.HTTPStatus = func(url string) (int, error) {
			if strings.Contains(url, ":7199/") || (!apiUp && strings.Contains(url, ":7197/")) {
				return 0, os.ErrDeadlineExceeded
			}
			return 200, nil
		}
		f.env.Fetch = func(url string) (string, error) {
			if url == "http://127.0.0.1:7197/healthz" { // spec 045: capture health; no answer here
				return "", os.ErrDeadlineExceeded
			}
			if url != "http://127.0.0.1:7197/metrics" {
				t.Fatalf("fetched %s", url)
			}
			// As the Prometheus text format writes it: shortest exact form, e.g. 1.79518514687e+09.
			start := strconv.FormatFloat(float64(now.Add(-sinceStart).Unix())+0.25, 'e', -1, 64)
			return "# HELP x\ngo_goroutines 9\nprocess_start_time_seconds " + start + "\n", nil
		}
		return results(f)["listener-ui"]
	}
	if r := setup(12*time.Second, true); r.Level != Warn || !strings.Contains(r.Detail, "warming up, about 12 s since the gateway started") || !strings.Contains(r.Fix, "may still be starting") {
		t.Fatalf("12 s: %+v", r)
	}
	lacksUIWarmupClaim(t, setup(12*time.Second, true))
	if r := setup(5*time.Minute, true); r.Level != Fail || !strings.Contains(r.Detail, "300 s after the gateway started") {
		t.Fatalf("5 min: %+v", r)
	}
	lacksUIWarmupClaim(t, setup(5*time.Minute, true))
	if r := setup(12*time.Second, false); r.Level != Fail || r.Detail != "127.0.0.1:7199 does not answer" {
		t.Fatalf("API down: %+v", r)
	}
	lacksUIWarmupClaim(t, setup(12*time.Second, false))
}

// Spec 025 AC5: a gateway failing its start under the service records the error;
// doctor names it while the record is fresh and the API is down, and not otherwise.
func TestDoctorServiceRestartingAfterStartupError(t *testing.T) {
	now := time.Unix(1_795_185_000, 0)
	setup := func(age time.Duration, apiUp bool) Result {
		f := installed(t)
		f.env.Now = func() time.Time { return now }
		f.env.HTTPStatus = func(url string) (int, error) {
			if !apiUp {
				return 0, os.ErrDeadlineExceeded
			}
			return 200, nil
		}
		rec, _ := json.Marshal(startup.Record{Error: "invalid config (key=listen_addr, reason=invalid address)",
			Key: "k", LoggedAt: now.Add(-age), FailedAt: now.Add(-age)})
		f.write(".local/state/llm-gateway/startup-error.json", string(rec))
		return results(f)["service"]
	}
	r := setup(4*time.Second, false)
	if r.Level != Fail || r.Detail != "service is restarting after a startup error: invalid config (key=listen_addr, reason=invalid address)" ||
		!strings.Contains(r.Fix, "journalctl --user -u llm-gateway.service -f") || !strings.Contains(r.Fix, "every 10 s") {
		t.Fatalf("fresh record, API down: %+v", r)
	}
	if r := setup(4*time.Second, true); r.Level != Pass {
		t.Fatalf("fresh record, API up (started since): %+v", r)
	}
	if r := setup(5*time.Minute, false); strings.Contains(r.Detail, "startup error") {
		t.Fatalf("old record: %+v", r)
	}
}
