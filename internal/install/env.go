// Package install is `gateway install`, `gateway uninstall` and `gateway doctor`: the
// preflight of what the gateway needs, the offers to install what is missing, the files
// and service it manages, and the checks of a live install (spec 023).
//
// It knows no client. What belongs to a client (its binary, its installer, its settings
// file and shell function) comes in as a Client, built by cmd/gateway from the client's
// own package (research D3).
package install

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/startup"
)

// Runner runs outside commands. Tests swap it for a fake that records every call.
type Runner interface {
	// Output runs name with args and returns its combined output.
	Output(name string, args ...string) (string, error)
	// Shell runs a command line shown to the user with `sh -c`, streaming its output.
	Shell(line string, out io.Writer) error
}

// PortProbe says whether a loopback port is free, and if not, which process holds it.
type PortProbe func(port int) (free bool, holder string)

// Env is everything the installer takes from the machine, so tests can fake each part.
type Env struct {
	GOOS, GOARCH string
	Home         string
	User         string
	UID          int
	Root         bool // running as root: package commands get no sudo
	Getenv       func(string) string
	LookPath     func(string) (string, error)
	Run          Runner
	Ports        PortProbe
	// HTTPStatus GETs url with a short timeout and returns the status code.
	HTTPStatus func(url string) (int, error)
	// Dial says whether something accepts TCP connections on addr.
	Dial func(addr string) bool
	// Fetch GETs url with a short timeout and returns its body (doctor reads /metrics).
	Fetch     func(url string) (string, error)
	Now       func() time.Time
	FreeBytes func(path string) (uint64, bool)
	OSRelease string // path of os-release; "" means /etc/os-release
	// AppArmorRestrict is the path of kernel.apparmor_restrict_unprivileged_userns and
	// AppArmorDir where profiles live; "" means the real ones.
	AppArmorRestrict, AppArmorDir string
	In                            io.Reader
	Out                           io.Writer
	Interactive                   bool // In is a terminal a person answers on
	Exe                           string
	Version                       string
	Service                       ServiceManager // nil: chosen from GOOS with Run
	LoadConfig                    func(path string) (config.Config, error)
	Sleep                         func(time.Duration)
	HealthWait                    time.Duration // how long install waits for /healthz

	reader *bufio.Reader
}

// Client is what the installer needs from the one client it wires (research D3).
type Client struct {
	Name         string   // "Claude Code"
	Binary       string   // the command on PATH, "claude"
	VersionArgs  []string // prints the version on its first line
	InstallerCmd string   // the official installer, run with sh -c after asking
	LoginNote    string   // what the user still does after installing it
	SettingsFile string   // file name in the config directory
	// Settings is the settings document for cfg; sandbox says whether its prerequisites
	// passed preflight.
	Settings func(cfg config.Config, sandbox bool) []byte
	// CheckSettings says why data does not point at the gateway configured in cfg, or "".
	CheckSettings func(data []byte, cfg config.Config) string
	// ShellFunc is the shell function, given the health URL and the settings file as
	// written in the shell (with $HOME).
	ShellFunc func(healthURL, settingsPath string) string
	// HooksPath is the hooks ingest path on the API listener.
	HooksPath string
	// NetworkNote is a line for the summary when network effects are on, such as which
	// runtimes honour the proxy settings (spec 024 AC3); "" for none.
	NetworkNote string
	// Routed says why the process running the installer is itself a client session
	// routed through the gateway in cfg (so stopping the gateway cuts it off), or "".
	Routed func(getenv func(string) string, cfg config.Config) string
}

// Paths are where the installer reads and writes, the same on both OSes but for the
// service file and logs (research D8).
type Paths struct {
	Home      string
	BinDir    string
	Binary    string
	ConfigDir string
	Config    string
	Settings  string
	State     string
	DataDir   string
	// StartupError is the record a gateway under its service writes when its start
	// fails (spec 025 AC5); "" when no HOME places it.
	StartupError string
}

func paths(e *Env, c Client) Paths {
	cfgDir := filepath.Join(e.Home, ".config", "llm-gateway")
	lookup := func(k string) (string, bool) {
		if k == "HOME" {
			return e.Home, true
		}
		v := e.Getenv(k)
		return v, v != ""
	}
	data, _ := config.DefaultCaptureDir(lookup)
	startErr, _ := startup.Path(lookup)
	return Paths{
		StartupError: startErr,
		Home:         e.Home,
		BinDir:       filepath.Join(e.Home, ".local", "bin"),
		Binary:       filepath.Join(e.Home, ".local", "bin", "gateway"),
		ConfigDir:    cfgDir,
		Config:       filepath.Join(cfgDir, "config.yaml"),
		Settings:     filepath.Join(cfgDir, c.SettingsFile),
		State:        filepath.Join(cfgDir, "install-state.json"),
		DataDir:      data,
	}
}

// tilde writes p with ~ for the home folder, for display.
func (p Paths) tilde(path string) string {
	if path == p.Home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(path, p.Home+string(filepath.Separator)); ok {
		return "~/" + rel
	}
	return path
}

// shellPath writes path with $HOME for the home folder, for shell files.
func (p Paths) shellPath(path string) string {
	if rel, ok := strings.CutPrefix(path, p.Home+string(filepath.Separator)); ok {
		return "$HOME/" + rel
	}
	return path
}

func (e *Env) printf(format string, a ...any) {
	_, _ = fmt.Fprintf(e.Out, format, a...)
}

// ask asks a yes/no question. With no terminal it answers def and says so; noTTYStop
// questions answer no instead (research D14).
func (e *Env) ask(question string, def bool) bool {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	e.printf("%s %s ", question, hint)
	if !e.Interactive {
		e.printf("(no terminal: no)\n")
		return false
	}
	if e.reader == nil {
		e.reader = bufio.NewReader(e.In)
	}
	line, err := e.reader.ReadString('\n')
	if err != nil && line == "" {
		e.printf("\n")
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes":
		return true
	default:
		return false
	}
}

func (e *Env) service() ServiceManager {
	if e.Service != nil {
		return e.Service
	}
	switch e.GOOS {
	case "linux":
		e.Service = &systemd{env: e}
	case "darwin":
		e.Service = &launchd{env: e}
	default:
		e.Service = noService{}
	}
	return e.Service
}
