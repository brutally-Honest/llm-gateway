package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// /dev/null is a character device but no terminal: questions read from it would only
// see end of file, so the installer must say there is no terminal instead (D14; found
// on the real-machine check, where the Bash tool's stdin is /dev/null).
func TestIsTerminalExcludesDevNull(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	if isTerminal(null) {
		t.Fatal("/dev/null taken for a terminal")
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "answers"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if isTerminal(file) || isTerminal(strings.NewReader("y\n")) {
		t.Fatal("a file or a reader taken for a terminal")
	}
}

// B13 (spec 024 AC9): with neither lsof nor ss to ask, a busy port's owner is unknown,
// never "another process", which made the running gateway's own ports read as foreign.
func TestPortHolderUnknownWithoutTools(t *testing.T) {
	r := &fakeRunner{}
	e := &Env{GOOS: "darwin", Run: r, LookPath: func(string) (string, error) { return "", os.ErrNotExist }}
	if got := portHolder(e, 7197); !strings.Contains(got, "unknown") || !strings.Contains(got, "lsof") {
		t.Fatalf("holder = %q, want an unknown owner naming the missing lsof", got)
	}
	if len(r.outputs) != 0 {
		t.Fatalf("ran %v with no tool on PATH", r.outputs)
	}
	e.GOOS = "linux"
	if got := portHolder(e, 7197); !strings.Contains(got, "unknown") || !strings.Contains(got, "ss") {
		t.Fatalf("linux holder = %q, want an unknown owner naming the missing ss", got)
	}
}

// B13: preflight never names "another process"; a port whose owner was not found reads
// as held by an unknown process.
func TestPortsUnknownHolderWording(t *testing.T) {
	f := newFake(t, "linux")
	f.busy[7197] = ""
	f.install(InstallOptions{Check: true})
	contains(t, f.out.String(), "7197 held by an unknown process")
	lacks(t, f.out.String(), "another process")
}

// ownGatewayFake is a linux install fake with no lsof or ss on PATH and ports 7195,
// 7197, 7198 and 7199 busy, their owner asked of the real portHolder. healthz and uiHealth
// are the bodies the API and UI health routes give, "" for no answer.
func ownGatewayFake(t *testing.T, healthz, uiHealth string) *fake {
	t.Helper()
	f := newFake(t, "linux")
	delete(f.path, "lsof")
	delete(f.path, "ss")
	for _, port := range []int{7195, 7197, 7198, 7199} {
		f.busy[port] = portHolder(f.env, port)
	}
	f.env.Fetch = func(url string) (string, error) {
		switch url {
		case "http://127.0.0.1:7197/healthz":
			if healthz != "" {
				return healthz, nil
			}
		case "http://127.0.0.1:7199/api/health":
			if uiHealth != "" {
				return uiHealth, nil
			}
		}
		return "", os.ErrDeadlineExceeded
	}
	return f
}

// Spec 025 AC3: with no tool to name a port's owner, ports held by a process that
// answers this gateway's health routes are the running gateway's, so a reinstall goes on
// and says how the gateway was found.
func TestPortsOwnGatewayFoundByHealth(t *testing.T) {
	f := ownGatewayFake(t, `{"status":"ok"}`, `{"ok":true}`)
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	contains(t, f.out.String(), "7195, 7197, 7198, 7199 held by the running gateway")
	contains(t, f.out.String(), "it answers its health routes")
	lacks(t, f.out.String(), "unknown process")
}

// Spec 025 AC3: a process that does not answer as this gateway (nothing, or another
// service's health body) keeps the ports busy with an unknown owner, and preflight fails.
func TestPortsUnknownProcessNotTakenForGateway(t *testing.T) {
	for name, bodies := range map[string][2]string{
		"no answer":     {"", ""},
		"other service": {`{"status":"healthy"}`, "ok"},
	} {
		t.Run(name, func(t *testing.T) {
			f := ownGatewayFake(t, bodies[0], bodies[1])
			if code := f.install(InstallOptions{Yes: true}); code == ExitOK {
				t.Fatalf("exit %d, want a preflight failure\n%s", code, f.out)
			}
			contains(t, f.out.String(), "7197 held by an unknown process")
			lacks(t, f.out.String(), "held by the running gateway")
		})
	}
}

// Spec 025 AC3: only the API's health makes 7197 the gateway's, so with the UI alone
// answering, 7199 and the proxy ports are the gateway's and 7197 stays unknown.
func TestPortsOwnGatewayPerListener(t *testing.T) {
	f := ownGatewayFake(t, "", `{"ok":true}`)
	f.install(InstallOptions{Check: true})
	contains(t, f.out.String(), "7197 held by an unknown process")
	lacks(t, f.out.String(), "7199 held by an unknown process")
}
