package install

import (
	"encoding/json"
	"strings"
	"testing"
)

const handUnit = "[Service]\nExecStart=/opt/gateway\n"

// installOverHandUnit installs over a hand-made unit whose service was enabled and/or
// running, as the fake service manager reports it.
func installOverHandUnit(t *testing.T, enabled, running bool) *fake {
	t.Helper()
	f := newFake(t, "linux")
	f.write(".config/systemd/user/llm-gateway.service", handUnit)
	f.svc.enabled, f.svc.running = enabled, running
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("install exit %d\n%s", code, f.out)
	}
	return f
}

func (f *fake) state() State {
	f.t.Helper()
	var s State
	if err := json.Unmarshal([]byte(f.read(".config/llm-gateway/install-state.json")), &s); err != nil {
		f.t.Fatal(err)
	}
	return s
}

// install-state.json records whether the unit install replaced was enabled and running
// (user request after the real-machine check, D25).
func TestInstallRecordsPriorServiceState(t *testing.T) {
	for _, c := range []struct{ enabled, running bool }{{true, true}, {true, false}, {false, true}, {false, false}} {
		f := installOverHandUnit(t, c.enabled, c.running)
		p := f.state().Service.Prior
		if p == nil || p.Enabled != c.enabled || p.Running != c.running {
			t.Fatalf("%+v: prior %+v", c, p)
		}
		contains(t, f.out.String(), "("+priorWords(PriorService{c.enabled, c.running})+")")
		// An upgrade keeps it: it is the state before the first install.
		f.svc.enabled, f.svc.running = true, true
		f.install(InstallOptions{Yes: true})
		if p2 := f.state().Service.Prior; p2 == nil || *p2 != *p {
			t.Fatalf("%+v: prior after upgrade %+v", c, p2)
		}
	}
	// A fresh install, with no unit to replace, records none.
	n := newFake(t, "linux")
	n.install(InstallOptions{Yes: true})
	if n.state().Service.Prior != nil {
		t.Fatal("prior recorded with no unit replaced")
	}
}

// Uninstall puts the user's own unit back and restores its state: enabled and started if
// it was, nothing run if it was neither, and prints what it did with the command to stop
// it (D25).
func TestUninstallRestoresPriorServiceState(t *testing.T) {
	cases := []struct {
		enabled, running bool
		want             string // the restore command, "" for none
	}{
		{true, true, "systemctl --user enable --now llm-gateway.service"},
		{true, false, "systemctl --user enable llm-gateway.service"},
		{false, true, "systemctl --user start llm-gateway.service"},
		{false, false, ""},
	}
	for _, c := range cases {
		f := installOverHandUnit(t, c.enabled, c.running)
		f.run.outputs = nil
		if code := f.uninstall(UninstallOptions{Yes: true}); code != ExitOK {
			t.Fatalf("%+v: exit %d\n%s", c, code, f.out)
		}
		if f.read(".config/systemd/user/llm-gateway.service") != handUnit {
			t.Fatalf("%+v: hand-made unit not put back", c)
		}
		ran := strings.Join(f.run.outputs, "|")
		// Order: stop the managed service, reload, then restore the user's.
		stop, reload := strings.Index(ran, "fake-svc stop"), strings.Index(ran, "systemctl --user daemon-reload")
		if stop < 0 || reload < stop {
			t.Fatalf("%+v: ran %q", c, ran)
		}
		out := f.out.String()
		contains(t, out, "Your own service file is back as it was before install ("+priorWords(PriorService{c.enabled, c.running})+")")
		if c.want == "" {
			for _, cmd := range []string{"enable", "start"} {
				if strings.Contains(ran, "systemctl --user "+cmd) {
					t.Fatalf("%+v: ran %q", c, ran)
				}
			}
			lacks(t, out, "To stop it:")
			continue
		}
		if at := strings.Index(ran, c.want); at < reload {
			t.Fatalf("%+v: want %q after the reload, ran %q", c, c.want, ran)
		}
		contains(t, out, "    run     "+c.want) // shown in the plan before it runs
		contains(t, out, "To stop it: systemctl --user disable --now llm-gateway.service")
	}
}

// launchd's restore loads the plist into gui/$UID and enables it.
func TestLaunchdRestoreCommands(t *testing.T) {
	e, p := goldenEnv("darwin")
	l := &launchd{env: e}
	got := l.RestoreCommands(p, true, true)
	if len(got) != 2 || strings.Join(got[0], " ") != "launchctl enable gui/501/dev.llm-gateway" ||
		strings.Join(got[1], " ") != "launchctl bootstrap gui/501 /Users/u/Library/LaunchAgents/dev.llm-gateway.plist" {
		t.Fatalf("%q", got)
	}
	if l.RestoreCommands(p, false, false) != nil || l.StopHint(p) != "launchctl bootout gui/501/dev.llm-gateway" {
		t.Fatal("launchd restore or stop hint")
	}
}
