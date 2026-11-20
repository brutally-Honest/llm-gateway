package install

import (
	"maps"
	"net"
	"regexp"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// refusedConfigs is one config per listener key with a refused address, and the
// message config.CheckListenAddr gives for it (spec 022).
func refusedConfigs() map[string]struct{ yaml, msg string } {
	msg := func(key, v string) string { return config.CheckListenAddr(key, v).Message() }
	return map[string]struct{ yaml, msg string }{
		"api":        {"listen_addr: 0.0.0.0:7197\n", msg("listen_addr", "0.0.0.0:7197")},
		"ui":         {"ui:\n  listen_addr: \"[::]:7199\"\n", msg("ui.listen_addr", "[::]:7199")},
		"http_proxy": {"effects:\n  network:\n    http_proxy_addr: 192.168.1.5:7198\n", msg("effects.network.http_proxy_addr", "192.168.1.5:7198")},
		"socks":      {"effects:\n  network:\n    socks_proxy_addr: gateway.lan:7195\n", msg("effects.network.socks_proxy_addr", "gateway.lan:7195")},
		"empty_host": {"listen_addr: \":7197\"\n", msg("listen_addr", ":7197")},
	}
}

// Spec 022 AC3: doctor reports a config with a listener off loopback as fail, with the
// message the gateway gives.
func TestDoctorRefusesNonLoopbackConfig(t *testing.T) {
	for name, c := range refusedConfigs() {
		t.Run(name, func(t *testing.T) {
			f := installed(t)
			f.write(".config/llm-gateway/config.yaml", c.yaml)
			r := results(f)["config"]
			if r.Level != Fail || !strings.Contains(r.Detail, c.msg) {
				t.Fatalf("config check = %+v, want fail with %q", r, c.msg)
			}
			if code := Doctor(f.env, testClient(), DoctorOptions{}); code == ExitOK {
				t.Fatal("doctor exit 0 with a refused listener")
			}
		})
	}
}

// Spec 022 AC3: with a loader that does not check listeners, doctor still applies the
// same rule and gives the same message.
func TestDoctorChecksListenersItself(t *testing.T) {
	f := installed(t)
	f.env.LoadConfig = func(string) (config.Config, error) {
		cfg := config.Defaults()
		cfg.ListenAddr = "0.0.0.0:7197"
		return cfg, nil
	}
	want := config.CheckListenAddr("listen_addr", "0.0.0.0:7197").Message()
	if r := results(f)["config"]; r.Level != Fail || !strings.Contains(r.Detail, want) {
		t.Fatalf("config check = %+v, want fail with %q", r, want)
	}
}

// Spec 022 AC4: install refuses an existing config with a listener off loopback, with
// the same message, before it writes or runs anything.
func TestInstallRefusesNonLoopbackConfig(t *testing.T) {
	for name, c := range refusedConfigs() {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, "linux")
			f.write(".config/llm-gateway/config.yaml", c.yaml)
			before := f.snapshot()
			if code := f.install(InstallOptions{Yes: true}); code != ExitStopped {
				t.Fatalf("exit %d, want %d\n%s", code, ExitStopped, f.out)
			}
			contains(t, f.out.String(), c.msg)
			contains(t, f.out.String(), "Nothing was written.")
			if after := f.snapshot(); !maps.Equal(before, after) {
				t.Fatalf("files changed: %v → %v", keys(before), keys(after))
			}
			if a := f.actions(); len(a) > 0 {
				t.Fatalf("ran %v", a)
			}
		})
	}
}

// Spec 022 AC4: install never writes a non-loopback address. Every host:port in every
// file a fresh install writes is on loopback, and the config it writes passes the rule.
func TestInstallWritesLoopbackOnly(t *testing.T) {
	f := newFake(t, "linux")
	before := f.snapshot()
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("install exit %d\n%s", code, f.out)
	}
	cfg, err := f.env.LoadConfig(f.paths().Config)
	if err != nil {
		t.Fatalf("written config: %v", err)
	}
	if e := config.CheckListeners(cfg); e != nil {
		t.Fatalf("written config: %s", e.Message())
	}
	hostPort := regexp.MustCompile(`(?:\[[0-9a-fA-F:.]*\]|[A-Za-z0-9.-]*):\d{2,5}\b`)
	written := 0
	for path, sum := range f.snapshot() {
		if before[path] == sum {
			continue
		}
		for _, hp := range hostPort.FindAllString(f.read(path), -1) {
			host, _, err := net.SplitHostPort(hp)
			if err != nil || strings.Contains(host, "/") {
				continue
			}
			written++
			if e := config.CheckListenAddr(path, hp); e != nil {
				t.Errorf("%s writes %q", path, hp)
			}
		}
	}
	if written == 0 {
		t.Fatal("found no address in the written files; the scan is broken")
	}
}
