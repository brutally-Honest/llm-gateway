package install

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// managedMark is in every service file install writes, so a lost state file still tells
// it apart from a hand-made one (research D9).
const managedMark = "Managed by gateway install"

// ServiceStatus is the live state of the login service.
type ServiceStatus struct {
	Installed bool // the unit or plist exists
	Loaded    bool // the manager knows the job (launchd: bootstrapped; systemd: LoadState=loaded)
	Running   bool
	AtLogin   bool
}

// ServiceManager is the OS's login service manager. Tests use a fake.
type ServiceManager interface {
	Kind() string // "systemd" or "launchd"
	// Check says whether the manager is usable for this user, with a detail line.
	Check() (bool, string)
	// File is the unit or plist path; Render is its content for the given paths.
	File(p Paths) string
	Render(p Paths) []byte
	// StartCommands are what Start runs after the file is written (reload, enable and
	// (re)start), for the plan; fileChanged says the service file is new or changed.
	StartCommands(p Paths, fileChanged bool) [][]string
	// Start runs StartCommands, with the waits and retries the manager needs. On error
	// the service may be down; a *startError says so and how to start it.
	Start(p Paths, fileChanged bool) error
	// StopCommands are run before the file is removed.
	StopCommands(p Paths) [][]string
	// RestoreCommands put a user's own service file, restored by uninstall, back in the
	// state it had before install: enabled at login and/or running (023 research D25).
	RestoreCommands(p Paths, enabled, running bool) [][]string
	// StopHint is the command a user runs to stop and disable the service.
	StopHint(p Paths) string
	// LoadHint is the command that loads an installed service that is not loaded, and
	// StartHint the one that starts a loaded one that is not running (spec 024 AC2).
	LoadHint(p Paths) string
	StartHint(p Paths) string
	Status(p Paths) ServiceStatus
	// Notes are extra lines for the summary, such as lingering being off.
	Notes() []string
	// LogHint says where the gateway's log is.
	LogHint(p Paths) string
}

const (
	systemdUnit   = "llm-gateway.service"
	launchdLabel  = "dev.llm-gateway"
	stopTimeoutS  = 30
	unitDirSuffix = ".config/systemd/user"
)

// systemd is a user unit (spec AC14).
type systemd struct{ env *Env }

func (s *systemd) Kind() string { return "systemd" }

func (s *systemd) Check() (bool, string) {
	if _, err := s.env.LookPath("systemctl"); err != nil {
		return false, "systemctl not found"
	}
	if _, err := s.env.Run.Output("systemctl", "--user", "show-environment"); err != nil {
		return false, "no systemd user session"
	}
	return true, "systemd user session"
}

func (s *systemd) File(p Paths) string {
	return filepath.Join(p.Home, unitDirSuffix, systemdUnit)
}

func (s *systemd) Render(p Paths) []byte {
	return fmt.Appendf(nil, `# %s; `+"`gateway uninstall`"+` removes it. Edits here are overwritten.
[Unit]
Description=LLM gateway
Documentation=https://github.com/brutally-Honest/llm-gateway/blob/main/docs/install.md

[Service]
ExecStart=%s -config %s
Restart=on-failure
# Room for shutdown_timeout (10s) and the capture drain; systemd's default is 90s.
TimeoutStopSec=%d

[Install]
WantedBy=default.target
`, managedMark, p.Binary, p.Config, stopTimeoutS)
}

func (s *systemd) StartCommands(Paths, bool) [][]string {
	return [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", systemdUnit},
		{"systemctl", "--user", "restart", systemdUnit},
	}
}

func (s *systemd) Start(p Paths, changed bool) error {
	return runCommands(s.env, s.StartCommands(p, changed))
}

func (s *systemd) StopCommands(Paths) [][]string {
	return [][]string{{"systemctl", "--user", "disable", "--now", systemdUnit}}
}

func (s *systemd) RestoreCommands(_ Paths, enabled, running bool) [][]string {
	switch {
	case enabled && running:
		return [][]string{{"systemctl", "--user", "enable", "--now", systemdUnit}}
	case enabled:
		return [][]string{{"systemctl", "--user", "enable", systemdUnit}}
	case running:
		return [][]string{{"systemctl", "--user", "start", systemdUnit}}
	}
	return nil
}

func (s *systemd) StopHint(Paths) string {
	return "systemctl --user disable --now " + systemdUnit
}

func (s *systemd) LoadHint(Paths) string {
	return "systemctl --user daemon-reload && systemctl --user enable --now " + systemdUnit
}

func (s *systemd) StartHint(Paths) string { return "systemctl --user restart " + systemdUnit }

func (s *systemd) Status(p Paths) ServiceStatus {
	st := ServiceStatus{}
	if _, err := os.Stat(s.File(p)); err == nil {
		st.Installed = true
	}
	out, _ := s.env.Run.Output("systemctl", "--user", "show", "-p", "LoadState", "--value", systemdUnit)
	st.Loaded = strings.TrimSpace(out) == "loaded"
	out, _ = s.env.Run.Output("systemctl", "--user", "is-active", systemdUnit)
	st.Running = strings.TrimSpace(out) == "active"
	out, _ = s.env.Run.Output("systemctl", "--user", "is-enabled", systemdUnit)
	st.AtLogin = strings.TrimSpace(out) == "enabled"
	return st
}

// Notes explains lingering when it is off; the command is printed, never run (AC14).
func (s *systemd) Notes() []string {
	out, err := s.env.Run.Output("loginctl", "show-user", s.env.User, "-p", "Linger")
	if err != nil || strings.TrimSpace(out) != "Linger=no" {
		return nil
	}
	return []string{
		"Lingering is off: the gateway starts when you log in and stops when your last session ends.",
		"To keep it running without a login session (for example over ssh), run: loginctl enable-linger " + s.env.User,
	}
}

func (s *systemd) LogHint(Paths) string { return "journalctl --user -u " + systemdUnit + " -f" }

// launchd is a LaunchAgent in gui/$UID (spec AC14).
type launchd struct{ env *Env }

func (l *launchd) Kind() string { return "launchd" }

func (l *launchd) Check() (bool, string) {
	if _, err := l.env.Run.Output("launchctl", "print", l.domain()); err != nil {
		return false, "launchctl cannot reach " + l.domain()
	}
	return true, "launchd (" + l.domain() + ")"
}

func (l *launchd) domain() string { return "gui/" + strconv.Itoa(l.env.UID) }

func (l *launchd) File(p Paths) string {
	return filepath.Join(p.Home, "Library", "LaunchAgents", launchdLabel+".plist")
}

func logDir(p Paths) string { return filepath.Join(p.Home, "Library", "Logs", "llm-gateway") }

func (l *launchd) Render(p Paths) []byte {
	logFile := filepath.Join(logDir(p), "gateway.log")
	var b bytes.Buffer
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- %s; `+"`gateway uninstall`"+` removes it. Edits here are overwritten. -->
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>-config</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key><false/>
  </dict>
  <key>ExitTimeOut</key><integer>%d</integer>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, managedMark, launchdLabel, xmlEscape(p.Binary), xmlEscape(p.Config), stopTimeoutS, xmlEscape(logFile), xmlEscape(logFile))
	return b.Bytes()
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (l *launchd) target() string { return l.domain() + "/" + launchdLabel }

// loaded says whether launchd knows the job: `launchctl print` fails once it is gone.
func (l *launchd) loaded() bool {
	_, err := l.env.Run.Output("launchctl", "print", l.target())
	return err == nil
}

// StartCommands: a loaded job whose plist is unchanged is only restarted with kickstart
// -k. Otherwise the old job is booted out, enable keeps it on at login (and lets a job a
// user disabled load at all), bootstrap loads the new plist and kickstart makes sure it
// runs (spec 024 AC1, macos-v1.md B2).
func (l *launchd) StartCommands(p Paths, changed bool) [][]string {
	mkdir := []string{"mkdir", "-p", logDir(p)}
	enable := []string{"launchctl", "enable", l.target()}
	loaded := l.loaded()
	if !changed && loaded {
		return [][]string{mkdir, enable, {"launchctl", "kickstart", "-k", l.target()}}
	}
	out := [][]string{mkdir}
	if loaded {
		out = append(out, []string{"launchctl", "bootout", l.target()})
	}
	return append(out, enable, l.bootstrapCmd(p), []string{"launchctl", "kickstart", l.target()})
}

func (l *launchd) bootstrapCmd(p Paths) []string {
	return []string{"launchctl", "bootstrap", l.domain(), l.File(p)}
}

// Waits of a launchd start: bootout returns before launchd has removed the job, so it is
// polled for up to the job's ExitTimeOut and a little more; bootstrap can still fail with
// EIO just after, so it is retried with a backoff.
const (
	launchdPoll      = 200 * time.Millisecond
	launchdGoneLimit = (stopTimeoutS + 5) * time.Second
	launchdTries     = 5
	launchdBackoff   = 250 * time.Millisecond
)

func (l *launchd) Start(p Paths, changed bool) error {
	cmds := l.StartCommands(p, changed)
	start := func() *startError {
		return &startError{Fix: strings.Join(l.bootstrapCmd(p), " ")}
	}
	for _, cmd := range cmds {
		line := strings.Join(cmd, " ")
		switch {
		case cmd[0] == "launchctl" && cmd[1] == "bootout":
			// Fails harmlessly if the job left in the meantime; the poll decides.
			_, _ = l.env.Run.Output(cmd[0], cmd[1:]...)
			if !l.waitGone() {
				e := start()
				e.Err = fmt.Errorf("%s: launchd had not removed the old job after %s", line, launchdGoneLimit)
				return e
			}
		case cmd[0] == "launchctl" && cmd[1] == "bootstrap":
			if err := l.bootstrap(cmd); err != nil {
				e := start()
				e.Err = err
				return e
			}
		default:
			if err := runCommands(l.env, [][]string{cmd}); err != nil {
				e := start()
				if cmd[0] == "launchctl" && cmd[1] == "kickstart" {
					e.Fix = "launchctl kickstart -k " + l.target() // loaded, not running
				}
				e.Err = err
				return e
			}
		}
	}
	return nil
}

func (l *launchd) waitGone() bool {
	for waited := time.Duration(0); waited <= launchdGoneLimit; waited += launchdPoll {
		if !l.loaded() {
			return true
		}
		l.env.Sleep(launchdPoll)
	}
	return false
}

func (l *launchd) bootstrap(cmd []string) error {
	wait := launchdBackoff
	var err error
	var out string
	try := 1
	for ; try <= launchdTries; try++ {
		if out, err = l.env.Run.Output(cmd[0], cmd[1:]...); err == nil {
			return nil
		}
		if !isEIO(err, out) || try == launchdTries {
			break
		}
		l.env.Sleep(wait)
		wait *= 2
	}
	return fmt.Errorf("%s: %w %s (%d of %d tries)", strings.Join(cmd, " "), err, strings.TrimSpace(out), try, launchdTries)
}

// isEIO says launchctl failed with error 5, Input/output error: the job is still being
// removed, or launchd is busy with it.
func isEIO(err error, out string) bool {
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) && ec.ExitCode() == 5 {
		return true
	}
	return strings.Contains(out, "Input/output error") || strings.Contains(out, "failed: 5:")
}

// startError is a service start that failed and may have left the gateway down.
type startError struct {
	Err error
	Fix string // the exact command that starts it
}

func (e *startError) Error() string { return e.Err.Error() }
func (e *startError) Unwrap() error { return e.Err }

// runCommands runs cmds in order; mkdir is done in-process.
func runCommands(e *Env, cmds [][]string) error {
	for _, cmd := range cmds {
		if cmd[0] == "mkdir" {
			if err := os.MkdirAll(cmd[len(cmd)-1], 0o755); err != nil {
				return err
			}
			continue
		}
		if out, err := e.Run.Output(cmd[0], cmd[1:]...); err != nil {
			return fmt.Errorf("%s: %w %s", strings.Join(cmd, " "), err, strings.TrimSpace(out))
		}
	}
	return nil
}

func (l *launchd) StopCommands(Paths) [][]string {
	return [][]string{{"launchctl", "bootout", l.domain() + "/" + launchdLabel}}
}

// RestoreCommands: a plist loaded into gui/$UID runs (RunAtLoad, as a user's own usually
// has); enable keeps it on at login.
func (l *launchd) RestoreCommands(p Paths, enabled, running bool) [][]string {
	target := l.domain() + "/" + launchdLabel
	var out [][]string
	if enabled {
		out = append(out, []string{"launchctl", "enable", target})
	}
	if running {
		out = append(out, []string{"launchctl", "bootstrap", l.domain(), l.File(p)})
	}
	return out
}

func (l *launchd) StopHint(Paths) string {
	return "launchctl bootout " + l.domain() + "/" + launchdLabel
}

func (l *launchd) LoadHint(p Paths) string { return strings.Join(l.bootstrapCmd(p), " ") }
func (l *launchd) StartHint(Paths) string  { return "launchctl kickstart -k " + l.target() }

func (l *launchd) Status(p Paths) ServiceStatus {
	st := ServiceStatus{}
	if _, err := os.Stat(l.File(p)); err == nil {
		st.Installed = true
	}
	out, err := l.env.Run.Output("launchctl", "print", l.domain()+"/"+launchdLabel)
	if err == nil {
		st.Loaded = true
		st.Running = strings.Contains(out, "state = running")
		st.AtLogin = st.Installed // loaded from a LaunchAgent with RunAtLoad
	}
	if dis, err := l.env.Run.Output("launchctl", "print-disabled", l.domain()); err == nil &&
		strings.Contains(dis, `"`+launchdLabel+`" => disabled`) {
		st.AtLogin = false
	}
	return st
}

func (l *launchd) Notes() []string { return nil }

func (l *launchd) LogHint(p Paths) string {
	return "tail -f " + p.tilde(filepath.Join(logDir(p), "gateway.log"))
}

// noService stands in on an unsupported OS; preflight has already stopped there.
type noService struct{}

func (noService) Kind() string                         { return "none" }
func (noService) Check() (bool, string)                { return false, "unsupported OS" }
func (noService) File(Paths) string                    { return "" }
func (noService) Render(Paths) []byte                  { return nil }
func (noService) StartCommands(Paths, bool) [][]string { return nil }
func (noService) Start(Paths, bool) error              { return nil }
func (noService) StopCommands(Paths) [][]string        { return nil }
func (noService) RestoreCommands(Paths, bool, bool) [][]string {
	return nil
}
func (noService) StopHint(Paths) string      { return "" }
func (noService) LoadHint(Paths) string      { return "" }
func (noService) StartHint(Paths) string     { return "" }
func (noService) Status(Paths) ServiceStatus { return ServiceStatus{} }
func (noService) Notes() []string            { return nil }
func (noService) LogHint(Paths) string       { return "" }

// manualStart is how to run the gateway without a service (spec AC8).
func manualStart(p Paths) string {
	return p.tilde(p.Binary) + " -config " + p.tilde(p.Config)
}
