package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		{"service", Fail, func(f *fake) { f.svc.ok = false; f.svc.running = false }},
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
	if r := results(f)["service"]; r.Level != Fail || r.Detail != "installed, not running" {
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
