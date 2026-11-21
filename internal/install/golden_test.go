package install

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s differs:\n%s", name, unifiedDiff(name, string(want), string(got)))
	}
}

func goldenEnv(goos string) (*Env, Paths) {
	vars := map[string]string{"PATH": "/usr/bin:/bin"}
	home := "/home/u"
	if goos == "darwin" {
		home = "/Users/u"
	}
	e := &Env{GOOS: goos, Home: home, UID: 501, Getenv: func(k string) string { return vars[k] }}
	return e, paths(e, testClientReal())
}

// testClientReal is the client with the real Claude Code function, so the goldens show
// the block a user gets.
func testClientReal() Client {
	c := testClient()
	c.ShellFunc = func(health, settings string) string {
		return "claude() {\n" +
			"  if curl -fsS --max-time 1 " + health + " >/dev/null 2>&1; then\n" +
			"    command claude --settings \"" + settings + "\" \"$@\"\n" +
			"  else\n" +
			"    echo \"llm-gateway is not running: starting plain Claude Code\" >&2\n" +
			"    command claude \"$@\"\n" +
			"  fi\n" +
			"}\n"
	}
	return c
}

// The generated unit, plist and marker block per OS (spec 023 tests).
func TestGoldenServiceFiles(t *testing.T) {
	e, p := goldenEnv("linux")
	golden(t, "systemd.service", (&systemd{env: e}).Render(p))
	golden(t, "block-linux.sh", []byte(shellBlock(e, testClientReal(), p, "http://127.0.0.1:7197/healthz")))

	m, mp := goldenEnv("darwin")
	golden(t, "launchd.plist", (&launchd{env: m}).Render(mp))
	// macOS with ~/.local/bin off PATH gets the PATH line (AC15).
	golden(t, "block-darwin.sh", []byte(shellBlock(m, testClientReal(), mp, "http://127.0.0.1:7197/healthz")))
}

func TestSystemdCommands(t *testing.T) {
	e, p := goldenEnv("linux")
	s := &systemd{env: e}
	if got := s.StartCommands(p, true); len(got) != 3 || got[2][2] != "restart" {
		t.Fatalf("start %v", got)
	}
	if got := s.StopCommands(p); got[0][3] != "--now" {
		t.Fatalf("stop %v", got)
	}
	e.Run = &fakeRunner{} // launchctl print succeeds: the job is loaded
	l := &launchd{env: e}
	got := l.StartCommands(p, true)
	if len(got) != 5 || got[1][1] != "bootout" || got[2][1] != "enable" || got[3][1] != "bootstrap" ||
		got[3][2] != "gui/501" || strings.Join(got[4], " ") != "launchctl kickstart gui/501/dev.llm-gateway" {
		t.Fatalf("launchd start, plist changed: %v", got)
	}
	if got := l.StartCommands(p, false); len(got) != 3 || strings.Join(got[2], " ") != "launchctl kickstart -k gui/501/dev.llm-gateway" {
		t.Fatalf("launchd start, plist unchanged: %v", got)
	}
}

func TestLingerNote(t *testing.T) {
	r := &fakeRunner{answers: map[string]string{"loginctl show-user u -p Linger": "Linger=no\n"}}
	s := &systemd{env: &Env{User: "u", Run: r}}
	notes := s.Notes()
	if len(notes) != 2 || notes[1] != "To keep it running without a login session (for example over ssh), run: loginctl enable-linger u" {
		t.Fatalf("notes %q", notes)
	}
	for _, o := range r.outputs {
		if o == "loginctl enable-linger u" {
			t.Fatal("enable-linger was run")
		}
	}
	r.answers["loginctl show-user u -p Linger"] = "Linger=yes\n"
	if s.Notes() != nil {
		t.Fatal("note with lingering on")
	}
}
