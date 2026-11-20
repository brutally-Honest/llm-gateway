package install

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// managedMark is in every service file install writes, so a lost state file still tells
// it apart from a hand-made one (research D9).
const managedMark = "Managed by gateway install"

// ServiceStatus is the live state of the login service.
type ServiceStatus struct {
	Installed bool // the unit or plist exists
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
	// StartCommands are run after the file is written: reload, enable and (re)start.
	StartCommands(p Paths) [][]string
	// StopCommands are run before the file is removed.
	StopCommands(p Paths) [][]string
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

func (s *systemd) StartCommands(Paths) [][]string {
	return [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", systemdUnit},
		{"systemctl", "--user", "restart", systemdUnit},
	}
}

func (s *systemd) StopCommands(Paths) [][]string {
	return [][]string{{"systemctl", "--user", "disable", "--now", systemdUnit}}
}

func (s *systemd) Status(p Paths) ServiceStatus {
	st := ServiceStatus{}
	if _, err := os.Stat(s.File(p)); err == nil {
		st.Installed = true
	}
	out, _ := s.env.Run.Output("systemctl", "--user", "is-active", systemdUnit)
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

func (l *launchd) StartCommands(p Paths) [][]string {
	target := l.domain() + "/" + launchdLabel
	return [][]string{
		{"mkdir", "-p", logDir(p)},
		// bootout fails harmlessly when the agent is not loaded; bootstrap then loads the
		// new plist, enable keeps it on at login and kickstart -k restarts it.
		{"launchctl", "bootout", target},
		{"launchctl", "bootstrap", l.domain(), l.File(p)},
		{"launchctl", "enable", target},
		{"launchctl", "kickstart", "-k", target},
	}
}

func (l *launchd) StopCommands(Paths) [][]string {
	return [][]string{{"launchctl", "bootout", l.domain() + "/" + launchdLabel}}
}

func (l *launchd) Status(p Paths) ServiceStatus {
	st := ServiceStatus{}
	if _, err := os.Stat(l.File(p)); err == nil {
		st.Installed = true
	}
	out, err := l.env.Run.Output("launchctl", "print", l.domain()+"/"+launchdLabel)
	if err == nil {
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

func (noService) Kind() string                   { return "none" }
func (noService) Check() (bool, string)          { return false, "unsupported OS" }
func (noService) File(Paths) string              { return "" }
func (noService) Render(Paths) []byte            { return nil }
func (noService) StartCommands(Paths) [][]string { return nil }
func (noService) StopCommands(Paths) [][]string  { return nil }
func (noService) Status(Paths) ServiceStatus     { return ServiceStatus{} }
func (noService) Notes() []string                { return nil }
func (noService) LogHint(Paths) string           { return "" }

// manualStart is how to run the gateway without a service (spec AC8).
func manualStart(p Paths) string {
	return p.tilde(p.Binary) + " -config " + p.tilde(p.Config)
}
