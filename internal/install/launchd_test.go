package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// errEIO stands for `launchctl bootstrap` exiting 5 (Input/output error).
var errEIO = errors.New("exit status 5")

// fakeLaunchctl models the launchd job behind `launchctl`: bootout returns before the job
// is gone (it stays visible to `launchctl print` for goneAfter more prints), and bootstrap
// can fail with EIO a number of times first, as on the Mac (macos-v1.md B2).
type fakeLaunchctl struct {
	*fakeRunner
	loaded    bool
	running   bool
	goneAfter int  // prints that still show the job after a bootout
	neverGone bool // the job never goes away
	eio       int  // bootstraps that fail with EIO before one succeeds
	bootouts  int
}

func (l *fakeLaunchctl) Output(name string, args ...string) (string, error) {
	out, _ := l.fakeRunner.Output(name, args...) // recorded
	if name != "launchctl" || len(args) == 0 {
		return out, nil
	}
	switch args[0] {
	case "print":
		if len(args) > 1 && strings.Contains(args[1], "/"+launchdLabel) {
			if !l.loaded {
				return `Could not find service "dev.llm-gateway" in domain for user gui: 1000`, errors.New("exit status 113")
			}
			if l.bootouts > 0 && !l.neverGone {
				if l.goneAfter == 0 {
					l.loaded, l.running = false, false
					return `Could not find service "dev.llm-gateway" in domain for user gui: 1000`, errors.New("exit status 113")
				}
				l.goneAfter--
			}
			if l.running {
				return "state = running\n", nil
			}
			return "state = not running\n", nil
		}
		return "domain = gui/1000\n", nil
	case "bootout":
		if !l.loaded {
			return "Boot-out failed: 3: No such process", errors.New("exit status 3")
		}
		l.bootouts++
		l.running = false
	case "bootstrap":
		if l.loaded {
			return "Bootstrap failed: 5: Input/output error", errEIO
		}
		if l.eio > 0 {
			l.eio--
			return "Bootstrap failed: 5: Input/output error", errEIO
		}
		l.loaded, l.running, l.bootouts = true, true, 0
	case "kickstart":
		if !l.loaded {
			return `Could not find service "dev.llm-gateway"`, errors.New("exit status 113")
		}
		l.running = true
	}
	return out, nil
}

// launchdFake is a Mac whose service manager is the real launchd type over fakeLaunchctl.
func launchdFake(t *testing.T) (*fake, *fakeLaunchctl) {
	t.Helper()
	f := newFake(t, "darwin")
	lc := &fakeLaunchctl{fakeRunner: f.run}
	f.env.Run = lc
	f.env.Service = &launchd{env: f.env}
	return f, lc
}

// launchctlRan is every launchctl command run, in order, without the domain probe.
func launchctlRan(f *fake) []string {
	var out []string
	for _, o := range f.run.outputs {
		if strings.HasPrefix(o, "launchctl ") && o != "launchctl print gui/1000" && !strings.HasPrefix(o, "launchctl print-disabled") {
			out = append(out, o)
		}
	}
	return out
}

func (f *fake) plistPath() string {
	return filepath.Join(f.env.Home, "Library", "LaunchAgents", launchdLabel+".plist")
}

// B2: a reinstall with the plist unchanged restarts the job with kickstart -k only.
func TestLaunchd_ReinstallUnchangedPlistKickstartsOnly(t *testing.T) {
	f, lc := launchdFake(t)
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("first install exit %d\n%s", code, f.out)
	}
	f.run.outputs = nil
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("reinstall exit %d\n%s", code, f.out)
	}
	ran := strings.Join(launchctlRan(f), "\n")
	lacks(t, ran, "bootout")
	lacks(t, ran, "bootstrap")
	contains(t, ran, "launchctl kickstart -k gui/1000/dev.llm-gateway")
	if !lc.loaded || !lc.running {
		t.Fatalf("job loaded=%v running=%v after reinstall", lc.loaded, lc.running)
	}
}

// B2: a changed plist is booted out, the job is polled until launchd has removed it (the
// bootout returns before that), and only then bootstrapped.
func TestLaunchd_ChangedPlistWaitsForBootoutBeforeBootstrap(t *testing.T) {
	f, lc := launchdFake(t)
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("first install exit %d\n%s", code, f.out)
	}
	if err := os.WriteFile(f.plistPath(), []byte("<!-- "+managedMark+" -->\nold\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lc.goneAfter = 3
	f.run.outputs = nil
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("reinstall exit %d\n%s", code, f.out)
	}
	ran := launchctlRan(f)
	boot, gone, strap := -1, -1, -1
	for i, c := range ran {
		switch {
		case strings.HasPrefix(c, "launchctl bootout"):
			boot = i
		case strings.HasPrefix(c, "launchctl bootstrap") && strap < 0:
			strap = i
		}
	}
	// The print that found the job gone is the last print before the bootstrap.
	for i := strap - 1; i > boot; i-- {
		if strings.HasPrefix(ran[i], "launchctl print gui/1000/") {
			gone = i
			break
		}
	}
	prints := 0
	for _, c := range ran[boot+1 : strap] {
		if strings.HasPrefix(c, "launchctl print gui/1000/") {
			prints++
		}
	}
	if boot < 0 || strap < 0 || gone < 0 || prints != 4 {
		t.Fatalf("want bootout, 4 prints (3 still there, 1 gone), then bootstrap; ran:\n%s", strings.Join(ran, "\n"))
	}
	if strings.Count(strings.Join(ran, "\n"), "launchctl bootstrap") != 1 {
		t.Fatalf("bootstrap retried although the job was gone first:\n%s", strings.Join(ran, "\n"))
	}
	if !lc.loaded || !lc.running {
		t.Fatalf("job loaded=%v running=%v", lc.loaded, lc.running)
	}
}

// B2: bootstrap is retried on EIO with a backoff and then succeeds.
func TestLaunchd_BootstrapRetriesOnEIO(t *testing.T) {
	f, lc := launchdFake(t)
	lc.eio = 2
	var slept []time.Duration
	f.env.Sleep = func(d time.Duration) { slept = append(slept, d) }
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("install exit %d\n%s", code, f.out)
	}
	if len(slept) < 2 || slept[1] <= slept[0] {
		t.Fatalf("backoff sleeps %v, want growing waits between the retries", slept)
	}
	if n := strings.Count(strings.Join(launchctlRan(f), "\n"), "launchctl bootstrap"); n != 3 {
		t.Fatalf("bootstraps = %d, want 3 (2 EIO, then ok)", n)
	}
	if !lc.loaded || !lc.running {
		t.Fatalf("job loaded=%v running=%v", lc.loaded, lc.running)
	}
}

// B2: when bootstrap keeps failing, install says plainly that the gateway is not running
// and gives the exact command to run.
func TestLaunchd_BootstrapFailureSaysSoWithCommand(t *testing.T) {
	f, lc := launchdFake(t)
	lc.eio = 1000
	if code := f.install(InstallOptions{Yes: true}); code != ExitStopped {
		t.Fatalf("install exit %d, want %d\n%s", code, ExitStopped, f.out)
	}
	out := f.out.String()
	contains(t, out, "The gateway is not running")
	contains(t, out, "launchctl bootstrap gui/1000 "+f.plistPath())
}

// B2: a job that never leaves after bootout is waited for only so long, then reported
// with the command to run, and bootstrap is not tried over it.
func TestLaunchd_BootoutTimeoutIsBounded(t *testing.T) {
	f, lc := launchdFake(t)
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("first install exit %d\n%s", code, f.out)
	}
	if err := os.WriteFile(f.plistPath(), []byte("<!-- "+managedMark+" -->\nold\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lc.neverGone = true
	f.run.outputs = nil
	if code := f.install(InstallOptions{Yes: true}); code != ExitStopped {
		t.Fatalf("reinstall exit %d\n%s", code, f.out)
	}
	ran := strings.Join(launchctlRan(f), "\n")
	lacks(t, ran, "launchctl bootstrap")
	out := f.out.String()
	contains(t, out, "The gateway is not running")
	contains(t, out, "launchctl bootstrap gui/1000 "+f.plistPath())
}
