package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// fakeRunner records every command; Shell runs onShell so a test can make a dependency
// appear, as a real package install would.
type fakeRunner struct {
	mu      sync.Mutex
	outputs []string // every Output call, "name arg …"
	shells  []string // every Shell call, the line as given
	answers map[string]string
	fail    map[string]bool
	onShell func(line string)
}

func (r *fakeRunner) Output(name string, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line := strings.Join(append([]string{name}, args...), " ")
	r.outputs = append(r.outputs, line)
	if r.fail[line] {
		return "", os.ErrPermission
	}
	for prefix, out := range r.answers {
		if strings.HasPrefix(line, prefix) {
			return out, nil
		}
	}
	return "", nil
}

func (r *fakeRunner) Shell(line string, out io.Writer) error {
	r.mu.Lock()
	r.shells = append(r.shells, line)
	on := r.onShell
	r.mu.Unlock()
	_, _ = out.Write([]byte("(ran " + line + ")\n"))
	if on != nil {
		on(line)
	}
	return nil
}

// fakeService is a service manager whose start and stop are recorded commands; its unit
// file is the real systemd one, so hand-made unit detection is exercised.
type fakeService struct {
	systemd
	ok      bool
	running bool
	enabled bool
}

func (f *fakeService) Check() (bool, string) {
	if f.ok {
		return true, "fake service manager"
	}
	return false, "no systemd user session"
}
func (f *fakeService) StartCommands(Paths, bool) [][]string { return [][]string{{"fake-svc", "start"}} }
func (f *fakeService) Start(p Paths, changed bool) error {
	return runCommands(f.env, f.StartCommands(p, changed))
}
func (f *fakeService) StopCommands(Paths) [][]string { return [][]string{{"fake-svc", "stop"}} }
func (f *fakeService) Notes() []string               { return nil }
func (f *fakeService) Status(p Paths) ServiceStatus {
	_, err := os.Stat(f.File(p))
	return ServiceStatus{Installed: err == nil, Loaded: err == nil, Running: f.running, AtLogin: err == nil && f.enabled}
}

type fake struct {
	t       *testing.T
	env     *Env
	run     *fakeRunner
	svc     *fakeService
	out     *bytes.Buffer
	path    map[string]bool // commands on PATH
	busy    map[int]string  // port → holder
	healthy bool
}

// newFake is a Linux (or macOS) machine with every dependency present, a zsh user and an
// empty home.
func newFake(t *testing.T, goos string) *fake {
	t.Helper()
	home := t.TempDir()
	etc := t.TempDir()
	osr := filepath.Join(etc, "os-release")
	if err := os.WriteFile(osr, []byte("ID=ubuntu\nID_LIKE=debian\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(etc, "gateway-build")
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho gateway\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := &fake{
		t:   t,
		run: &fakeRunner{answers: map[string]string{"/usr/bin/claude --version": "2.1.329 (Claude Code)\n", "/usr/bin/curl --version": "curl 8.5.0 (x86_64)\n", "/usr/bin/git --version": "git version 2.43.0\n"}, fail: map[string]bool{}},
		out: &bytes.Buffer{},
		path: map[string]bool{
			"claude": true, "curl": true, "git": true, "bwrap": true, "socat": true, "docker": true,
			"apt-get": true, "systemctl": true,
		},
		busy:    map[int]string{},
		healthy: true,
	}
	if goos == "darwin" {
		delete(f.path, "apt-get")
		delete(f.path, "bwrap")
		delete(f.path, "socat")
		f.path["brew"] = true
	}
	vars := map[string]string{"SHELL": "/bin/zsh", "PATH": "/usr/bin:/bin:" + filepath.Join(home, ".local", "bin")}
	f.env = &Env{
		GOOS: goos, GOARCH: "amd64", Home: home, User: "u", UID: 1000,
		Getenv: func(k string) string { return vars[k] },
		LookPath: func(cmd string) (string, error) {
			if f.path[cmd] {
				return "/usr/bin/" + cmd, nil
			}
			return "", os.ErrNotExist
		},
		Run:   f.run,
		Ports: func(p int) (bool, string) { h, ok := f.busy[p]; return !ok, h },
		HTTPStatus: func(url string) (int, error) {
			if f.healthy {
				return 200, nil
			}
			return 0, os.ErrDeadlineExceeded
		},
		Dial:             func(string) bool { return f.healthy },
		FreeBytes:        func(string) (uint64, bool) { return 100 << 30, true },
		OSRelease:        osr,
		AppArmorRestrict: filepath.Join(etc, "no-such-file"),
		AppArmorDir:      filepath.Join(etc, "apparmor.d"),
		In:               strings.NewReader(""),
		Out:              f.out,
		Interactive:      true,
		Exe:              src,
		Version:          "1.2.3",
		LoadConfig:       testLoadConfig(home),
		Sleep:            func(time.Duration) {},
		HealthWait:       time.Second,
	}
	f.svc = &fakeService{systemd: systemd{env: f.env}, ok: true, running: true, enabled: true}
	f.env.Service = f.svc
	f.run.onShell = func(line string) {
		// A package or the client's installer "installs" what it names.
		for _, w := range strings.Fields(line) {
			switch w {
			case "bubblewrap":
				f.path["bwrap"] = true
			case "git", "socat", "curl":
				f.path[w] = true
			}
		}
		if strings.Contains(line, "claude.ai/install.sh") {
			f.path["claude"] = true
		}
	}
	return f
}

func testLoadConfig(home string) func(string) (config.Config, error) {
	return func(path string) (config.Config, error) {
		env := func(k string) (string, bool) {
			if k == "HOME" {
				return home, true
			}
			return "", false
		}
		cfg, _, err := config.Load(config.Options{Path: path, LookupEnv: env,
			Upstreams: []config.UpstreamSpec{{Name: "anthropic", DefaultBaseURL: "https://api.anthropic.com"}}})
		return cfg, err
	}
}

func (f *fake) answer(lines ...string) {
	f.env.In = strings.NewReader(strings.Join(lines, "\n") + "\n")
	f.env.reader = nil
}

func (f *fake) setEnv(k, v string) {
	old := f.env.Getenv
	f.env.Getenv = func(key string) string {
		if key == k {
			return v
		}
		return old(key)
	}
}

func (f *fake) paths() Paths { return paths(f.env, testClient()) }

func (f *fake) write(rel, content string) string {
	f.t.Helper()
	p := filepath.Join(f.env.Home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *fake) read(rel string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.env.Home, rel))
	if err != nil {
		f.t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// snapshot is every file under home with its hash.
func (f *fake) snapshot() map[string]string {
	f.t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(f.env.Home, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		rel, _ := filepath.Rel(f.env.Home, path)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fake) install(o InstallOptions) int {
	f.out.Reset()
	return Install(f.env, testClient(), o)
}

func (f *fake) uninstall(o UninstallOptions) int {
	f.out.Reset()
	return Uninstall(f.env, testClient(), o)
}

// ran says whether any install action ran: a shell offer or a service command.
func (f *fake) actions() []string {
	var out []string
	out = append(out, f.run.shells...)
	for _, o := range f.run.outputs {
		if strings.HasPrefix(o, "fake-svc") {
			out = append(out, o)
		}
	}
	return out
}

func testClient() Client {
	return Client{
		Name:         "Claude Code",
		Binary:       "claude",
		VersionArgs:  []string{"--version"},
		InstallerCmd: "curl -fsSL https://claude.ai/install.sh | bash",
		LoginNote:    "run claude once to log in",
		SettingsFile: "claude-settings.json",
		Settings: func(cfg config.Config, sandbox bool) []byte {
			out, _ := json.MarshalIndent(map[string]any{
				"env":     map[string]string{"ANTHROPIC_BASE_URL": "http://" + cfg.ListenAddr + "/anthropic"},
				"sandbox": sandbox,
			}, "", "  ")
			return append(out, '\n')
		},
		CheckSettings: func(data []byte, cfg config.Config) string {
			if !bytes.Contains(data, []byte("http://"+cfg.ListenAddr+"/anthropic")) {
				return "does not point at " + cfg.ListenAddr
			}
			return ""
		},
		ShellFunc: func(health, settings string) string {
			return "claude() {\n  curl -fsS " + health + " && command claude --settings \"" + settings + "\" \"$@\"\n}\n"
		},
		HooksPath: "/_gateway/hooks/claude-code",
		Routed: func(getenv func(string) string, cfg config.Config) string {
			if v := getenv("ANTHROPIC_BASE_URL"); v == "http://"+cfg.ListenAddr+"/anthropic" {
				return "ANTHROPIC_BASE_URL is " + v
			}
			return ""
		},
	}
}

func contains(t *testing.T, s, want string) {
	t.Helper()
	if !strings.Contains(s, want) {
		t.Fatalf("output lacks %q:\n%s", want, s)
	}
}

func lacks(t *testing.T, s, unwanted string) {
	t.Helper()
	if strings.Contains(s, unwanted) {
		t.Fatalf("output has %q:\n%s", unwanted, s)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
