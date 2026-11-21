package install

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The loopback ports the gateway uses: 7195 SOCKS, 7197 API, 7198 HTTP proxy, 7199 UI,
// and 7196 kept free as the range's reserved slot (research, Dependencies).
var gatewayPorts = []int{7195, 7196, 7197, 7198, 7199}

// Dep is one row of the preflight table.
type Dep struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Found    bool   `json:"found"`
	Detail   string `json:"detail"`            // version or path when found; the reason when not
	Meaning  string `json:"meaning,omitempty"` // what missing means for the user
	Pkg      string `json:"-"`                 // the package that provides it, "" if none
	Manual   string `json:"-"`                 // manual steps when nothing can install it
	Offer    string `json:"-"`                 // an install command other than a package (the client's installer)
	Skipped  bool   `json:"skipped,omitempty"` // not checked because a flag turned it off
}

// Mark is ✓ found, ✗ missing and required, – optional and missing (spec AC1).
func (d Dep) Mark() string {
	switch {
	case d.Found:
		return "✓"
	case d.Required:
		return "✗"
	default:
		return "–"
	}
}

// Preflight is the result of the checks, re-runnable per dependency after an offer.
type Preflight struct {
	Deps      []Dep
	Paths     Paths
	FreeBytes uint64
	FreeKnown bool
	PkgMgr    string // apt, dnf, pacman, brew or ""

	env    *Env
	client Client
	opts   checkOpts
}

type checkOpts struct {
	noService bool
}

// runPreflight runs every check.
func runPreflight(e *Env, c Client, o checkOpts) *Preflight {
	p := &Preflight{Paths: paths(e, c), env: e, client: c, opts: o}
	p.PkgMgr = detectPkgMgr(e)
	for _, id := range p.ids() {
		p.Deps = append(p.Deps, p.check(id))
	}
	p.FreeBytes, p.FreeKnown = freeAt(e, p.Paths.DataDir)
	return p
}

func (p *Preflight) ids() []string {
	ids := []string{"platform", "client", "curl", "service-manager", "ports", "shell", "git"}
	if p.env.GOOS == "linux" {
		ids = append(ids, "bwrap", "socat", "apparmor")
	} else {
		ids = append(ids, "sandbox")
	}
	return append(ids, "docker")
}

// recheck runs one dependency's check again, after an offer ran its command.
func (p *Preflight) recheck(id string) Dep {
	d := p.check(id)
	for i := range p.Deps {
		if p.Deps[i].ID == id {
			p.Deps[i] = d
		}
	}
	return d
}

func (p *Preflight) dep(id string) (Dep, bool) {
	for _, d := range p.Deps {
		if d.ID == id {
			return d, true
		}
	}
	return Dep{}, false
}

func (p *Preflight) found(id string) bool {
	d, ok := p.dep(id)
	return ok && d.Found
}

// SandboxOK says whether Claude Code's sandbox prerequisites all passed (research D5).
func (p *Preflight) SandboxOK() bool {
	if p.env.GOOS == "linux" {
		return p.found("bwrap") && p.found("socat") && p.found("apparmor")
	}
	return p.found("sandbox")
}

// MissingRequired lists the required dependencies still missing.
func (p *Preflight) MissingRequired() []Dep {
	var out []Dep
	for _, d := range p.Deps {
		if d.Required && !d.Found && !d.Skipped {
			out = append(out, d)
		}
	}
	return out
}

func (p *Preflight) check(id string) Dep {
	e := p.env
	switch id {
	case "platform":
		d := Dep{ID: id, Name: "OS and CPU", Required: true, Detail: e.GOOS + "/" + e.GOARCH}
		d.Found = (e.GOOS == "linux" || e.GOOS == "darwin") && (e.GOARCH == "amd64" || e.GOARCH == "arm64")
		if !d.Found {
			d.Meaning = "only Linux and macOS on amd64 or arm64 are supported"
			d.Manual = "run the gateway on Linux or macOS (amd64 or arm64)"
		}
		return d
	case "client":
		return p.checkClient()
	case "curl":
		d := p.checkCmd(id, "curl", true, "curl", "the claude function uses it to find the gateway")
		if d.Found {
			if out, err := e.Run.Output(d.Detail, "--version"); err == nil {
				if f := strings.Fields(firstLine(out)); len(f) >= 2 {
					d.Detail = f[1] + "  " + p.Paths.tilde(d.Detail)
				}
			}
		}
		return d
	case "service-manager":
		d := Dep{ID: id, Name: "Service manager", Required: true}
		if p.opts.noService {
			d.Skipped = true
			d.Detail = "skipped (--no-service): the gateway won't start at login"
			return d
		}
		d.Found, d.Detail = e.service().Check()
		if !d.Found {
			d.Meaning = "no login service; re-run with --no-service to start the gateway by hand"
			d.Manual = noServiceHelp(e)
		}
		return d
	case "ports":
		return p.checkPorts()
	case "shell":
		d := Dep{ID: id, Name: "Shell (zsh or bash)"}
		sh, rc := shellRC(e)
		if rc != "" {
			d.Found, d.Detail = true, sh+"  "+p.Paths.tilde(rc)
		} else {
			name := sh
			if name == "" {
				name = "unknown ($SHELL unset)"
			}
			d.Detail = name
			d.Meaning = "the claude function is printed for you to add by hand"
			d.Manual = "add the lines gateway install prints to " + manualRC(sh)
		}
		return d
	case "git":
		d := p.checkCmd(id, "git", false, "git", "file diffs only from stored copies of small text files")
		if d.Found {
			if out, err := e.Run.Output(d.Detail, "--version"); err == nil {
				d.Detail = strings.TrimPrefix(firstLine(out), "git version ")
			}
		}
		return d
	case "bwrap":
		return p.checkCmd(id, "bwrap", false, "bubblewrap", "no sandbox: Bash traffic that ignores HTTPS_PROXY is not seen")
	case "socat":
		return p.checkCmd(id, "socat", false, "socat", "no sandbox: Bash traffic that ignores HTTPS_PROXY is not seen")
	case "apparmor":
		return p.checkAppArmor()
	case "sandbox":
		// macOS: Seatbelt is built in (research, Dependencies).
		return Dep{ID: id, Name: "Sandbox (Seatbelt)", Found: true, Detail: "built in"}
	case "docker":
		d := p.checkCmd(id, "docker", false, "", "no observability stack (make observability-up)")
		d.Manual = "install Docker from https://docs.docker.com/get-docker/ (only for the observability stack)"
		return d
	}
	return Dep{ID: id, Name: id}
}

// checkCmd finds a command on PATH.
func (p *Preflight) checkCmd(id, cmd string, required bool, pkg, meaning string) Dep {
	d := Dep{ID: id, Name: cmd, Required: required, Pkg: pkg}
	if path, err := p.env.LookPath(cmd); err == nil {
		d.Found, d.Detail = true, path
		return d
	}
	d.Detail = "not found"
	d.Meaning = meaning
	return d
}

func (p *Preflight) checkClient() Dep {
	e, c := p.env, p.client
	d := Dep{ID: "client", Name: c.Name, Required: true, Offer: c.InstallerCmd}
	path, err := e.LookPath(c.Binary)
	if err != nil {
		// Just installed by the offer, but ~/.local/bin is not on this PATH yet (spec E5).
		local := filepath.Join(p.Paths.BinDir, c.Binary)
		if st, serr := os.Stat(local); serr == nil && !st.IsDir() {
			path, err = local, nil
		}
	}
	if err != nil {
		d.Detail = "not found"
		d.Meaning = "required: the gateway wires " + c.Name + "; " + c.LoginNote
		d.Manual = c.InstallerCmd
		return d
	}
	d.Found, d.Detail = true, p.Paths.tilde(path)
	if out, err := e.Run.Output(path, c.VersionArgs...); err == nil && firstLine(out) != "" {
		d.Detail = firstLine(out) + "  " + p.Paths.tilde(path)
	}
	return d
}

func (p *Preflight) checkPorts() Dep {
	d := Dep{ID: "ports", Name: "Ports 7195–7199", Required: true}
	var busy, gw []string
	byHealth := false
	answers := p.gatewayAnswers()
	for _, port := range gatewayPorts {
		free, holder := p.env.Ports(port)
		switch {
		case free:
		case isGatewayHolder(holder):
			gw = append(gw, strconv.Itoa(port)) // the running gateway: an upgrade (spec E2)
		case isUnknownHolder(holder) && answers(port):
			// No tool named the owner, but this gateway answers there (spec 025 AC3).
			gw = append(gw, strconv.Itoa(port))
			byHealth = true
		default:
			if holder == "" {
				holder = unknownHolder
			}
			busy = append(busy, fmt.Sprintf("%d held by %s", port, holder))
		}
	}
	if len(busy) > 0 {
		d.Detail = strings.Join(busy, "; ")
		d.Meaning = "the gateway listens on 127.0.0.1:7195–7199"
		d.Manual = "stop the process holding the port, then run the installer again"
		return d
	}
	d.Found = true
	d.Detail = "free on 127.0.0.1"
	if len(gw) > 0 {
		d.Detail = "free on 127.0.0.1 (" + strings.Join(gw, ", ") + " held by the running gateway)"
		if byHealth {
			d.Detail = "free on 127.0.0.1 (" + strings.Join(gw, ", ") + " held by the running gateway: " +
				"no lsof or ss to name the owner, but it answers its health routes)"
		}
	}
	return d
}

func isGatewayHolder(holder string) bool {
	return holder == "gateway" || strings.HasPrefix(holder, "gateway ")
}

func isUnknownHolder(holder string) bool {
	return holder == "" || strings.HasPrefix(holder, unknownHolder)
}

// The health routes and bodies of this gateway's API and UI listeners on their default
// ports (internal/server healthz, internal/ui /api/health).
const (
	apiHealthURL  = "http://127.0.0.1:7197/healthz"
	apiHealthBody = `{"status":"ok"}`
	uiHealthURL   = "http://127.0.0.1:7199/api/health"
	uiHealthBody  = `{"ok":true}`
)

// gatewayAnswers says, per port, whether this gateway holds it. The health routes are
// asked once, on the first listener port: if either answers as this gateway, all four of
// its listener ports (7195, 7197, 7198, 7199) are its own. The proxies have no health
// route, and the UI holds its port without answering while it warms up after a restart
// (research D6). 7196 is never the gateway's: nothing binds it.
func (p *Preflight) gatewayAnswers() func(port int) bool {
	var asked, ours bool
	return func(port int) bool {
		switch port {
		case 7195, 7197, 7198, 7199:
		default:
			return false
		}
		if !asked {
			asked = true
			ours = p.answersWith(apiHealthURL, apiHealthBody) || p.answersWith(uiHealthURL, uiHealthBody)
		}
		return ours
	}
}

func (p *Preflight) answersWith(url, want string) bool {
	if p.env.Fetch == nil {
		return false
	}
	body, err := p.env.Fetch(url)
	return err == nil && strings.TrimSpace(body) == want
}

// checkAppArmor passes unless Ubuntu's unprivileged-userns restriction is on with no
// bwrap profile, which makes Claude Code run commands unsandboxed (spec E6).
func (p *Preflight) checkAppArmor() Dep {
	e := p.env
	d := Dep{ID: "apparmor", Name: "AppArmor and bwrap", Found: true, Detail: "no restriction"}
	restrict := e.AppArmorRestrict
	if restrict == "" {
		restrict = "/proc/sys/kernel/apparmor_restrict_unprivileged_userns"
	}
	data, err := os.ReadFile(restrict)
	if err != nil || strings.TrimSpace(string(data)) != "1" {
		return d
	}
	dir := e.AppArmorDir
	if dir == "" {
		dir = "/etc/apparmor.d"
	}
	if _, err := os.Stat(filepath.Join(dir, "bwrap")); err == nil {
		d.Detail = "restricted, bwrap profile present"
		return d
	}
	d.Found = false
	d.Detail = "unprivileged user namespaces restricted, no bwrap profile"
	d.Meaning = "no sandbox: Claude Code runs Bash unsandboxed until bwrap has a profile"
	d.Manual = "add the bwrap AppArmor profile from https://code.claude.com/docs/en/sandboxing (needs sudo; the installer never does it)"
	return d
}

// shellRC is the shell named by $SHELL and the startup file the function goes in, or ""
// for a shell other than zsh or bash (research D11).
func shellRC(e *Env) (string, string) {
	sh := filepath.Base(e.Getenv("SHELL"))
	if e.Getenv("SHELL") == "" {
		sh = ""
	}
	switch sh {
	case "zsh":
		dir := e.Getenv("ZDOTDIR")
		if dir == "" {
			dir = e.Home
		}
		return sh, filepath.Join(dir, ".zshrc")
	case "bash":
		if e.GOOS == "darwin" {
			return sh, filepath.Join(e.Home, ".bash_profile")
		}
		return sh, filepath.Join(e.Home, ".bashrc")
	}
	return sh, ""
}

// manualRC is where the lines go for a shell the installer does not edit.
func manualRC(sh string) string {
	switch sh {
	case "fish":
		return "~/.config/fish/config.fish (fish needs its own syntax: a `function claude … end` with the same body)"
	case "ksh", "mksh":
		return "~/.kshrc"
	case "sh", "dash":
		return "~/.profile"
	}
	return "your shell's startup file"
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

// freeAt is the free space of the nearest folder of path that exists.
func freeAt(e *Env, path string) (uint64, bool) {
	for dir := path; dir != ""; dir = filepath.Dir(dir) {
		if _, err := os.Stat(dir); err == nil {
			return e.FreeBytes(dir)
		}
		if dir == filepath.Dir(dir) {
			break
		}
	}
	return 0, false
}

// render prints the table and the paths under it (spec AC1).
func (p *Preflight) render() {
	e := p.env
	e.printf("Preflight\n")
	width := 0
	for _, d := range p.Deps {
		width = max(width, len([]rune(d.Name)))
	}
	for _, d := range p.Deps {
		label := "required"
		if !d.Required {
			label = "optional"
		}
		mark := d.Mark()
		if d.Skipped {
			mark = "–"
		}
		pad := strings.Repeat(" ", width-len([]rune(d.Name)))
		e.printf("  %s %s%s  %-8s  %s\n", mark, d.Name, pad, label, d.Detail)
		if !d.Found && !d.Skipped && d.Meaning != "" {
			e.printf("      → %s\n", d.Meaning)
		}
	}
	e.printf("\n  Install location  %s\n", p.Paths.tilde(p.Paths.BinDir))
	e.printf("  Config            %s\n", p.Paths.tilde(p.Paths.ConfigDir))
	e.printf("  Data              %s\n", p.Paths.tilde(p.Paths.DataDir))
	if p.FreeKnown {
		e.printf("  Free disk         %s\n", humanBytes(p.FreeBytes))
	}
	pm := p.PkgMgr
	if pm == "" {
		pm = "none found"
	}
	e.printf("  Package manager   %s\n\n", pm)
}

// offers asks to install each missing dependency something can install, in table order
// (spec AC4–AC7). It returns false when a required dependency is still missing.
func (p *Preflight) offers(installDeps bool) bool {
	e := p.env
	var manual []Dep
	for _, d := range append([]Dep(nil), p.Deps...) {
		if d.Found || d.Skipped {
			continue
		}
		cmd := d.Offer
		if cmd == "" && d.Pkg != "" {
			cmd = pkgCommand(p.PkgMgr, d.Pkg, e.Root)
		}
		if cmd == "" {
			manual = append(manual, d)
			continue
		}
		e.printf("%s is missing (%s).\n  To install it, the installer would run:\n    %s\n", d.Name, reqWord(d), cmd)
		if d.ID == "client" {
			e.printf("  This is %s's official installer. Logging in stays with you: %s\n", p.client.Name, p.client.LoginNote)
		}
		yes := installDeps
		if installDeps {
			e.printf("Run it now? [y/N] yes (--install-deps)\n")
		} else {
			yes = e.ask("Run it now?", false)
		}
		if !yes {
			e.printf("  Skipped.\n\n")
			continue
		}
		if err := e.Run.Shell(cmd, e.Out); err != nil {
			e.printf("  The command failed: %v\n", err)
		}
		if nd := p.recheck(d.ID); nd.Found {
			e.printf("  %s now found: %s\n\n", nd.Name, nd.Detail)
		} else {
			e.printf("  %s is still missing.\n\n", nd.Name)
		}
	}
	if len(manual) > 0 {
		p.printManual(manual)
	}
	return len(p.MissingRequired()) == 0
}

func reqWord(d Dep) string {
	if d.Required {
		return "required"
	}
	return "optional: " + d.Meaning
}

func (p *Preflight) printManual(deps []Dep) {
	e := p.env
	sort.SliceStable(deps, func(i, j int) bool { return deps[i].Required && !deps[j].Required })
	e.printf("To install by hand:\n")
	for _, d := range deps {
		steps := d.Manual
		if steps == "" && d.Pkg != "" {
			steps = manualPkgSteps(e.GOOS, d.Pkg)
		}
		if steps == "" {
			continue
		}
		e.printf("  %s %s (%s): %s\n", d.Mark(), d.Name, reqWord(d), steps)
	}
	e.printf("\n")
}

func manualPkgSteps(goos, pkg string) string {
	if goos == "darwin" {
		return "install Homebrew (https://brew.sh), then `brew install " + pkg + "`; the installer never installs a package manager"
	}
	return "install the `" + pkg + "` package with your distribution's package manager"
}

func noServiceHelp(e *Env) string {
	if e.GOOS == "linux" {
		return "no systemd user session (common in containers, WSL and minimal distros): run `gateway install --no-service` and start the gateway by hand"
	}
	return "launchd is not reachable for this user: run `gateway install --no-service` and start the gateway by hand"
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// installedDeps names the dependencies preflight checks on this OS that a user installs
// as packages, in preflight order, for uninstall's keep list (spec 024 AC8). The OS, the
// service manager, ports, the shell and built-in parts are left out.
func installedDeps(e *Env, c Client) []string {
	names := map[string]string{"client": c.Name, "curl": "curl", "git": "git", "bwrap": "bubblewrap", "socat": "socat", "docker": "Docker"}
	var out []string
	for _, id := range (&Preflight{env: e}).ids() {
		if n, ok := names[id]; ok {
			out = append(out, n)
		}
	}
	return out
}
