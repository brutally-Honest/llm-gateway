package install

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// Exit codes of the three commands.
const (
	ExitOK      = 0
	ExitStopped = 1 // a check failed, a dependency is missing or the user said no
	ExitUsage   = 2
)

// InstallOptions are `gateway install`'s flags.
type InstallOptions struct {
	Yes, InstallDeps, NoShell, NoService, Check, DryRun bool
}

// change is one file the plan writes.
type change struct {
	label    string
	path     string
	old      []byte
	exists   bool
	data     []byte
	copyFrom string // the binary: copied from this path instead of data
	backup   bool   // keep a .bak of old first
}

func (c change) changed() bool {
	if c.copyFrom != "" {
		return true
	}
	return !c.exists || !bytes.Equal(c.old, c.data)
}

type plan struct {
	changes  []change
	commands [][]string
	notes    []string // what was found and decided, printed with the plan
	state    State
	off      []string // optional features off, with the reason
	shell    string   // the startup file edited, "" if none
	manual   string   // lines to add by hand for an unsupported shell
	service  bool
	// serviceChanged says the service file is new or changed, so the job is reloaded, not
	// only restarted (spec 024 AC1).
	serviceChanged bool
}

// Install runs `gateway install` and returns its exit code.
func Install(e *Env, c Client, o InstallOptions) int {
	pre := runPreflight(e, c, checkOpts{noService: o.NoService})
	pre.render()
	if o.Check {
		return checkVerdict(e, pre)
	}
	if !o.DryRun {
		if !pre.offers(o.InstallDeps) && !o.NoService && onlyServiceMissing(pre) {
			e.printf("%s\n", noServiceHelp(e))
			if o.Yes || e.ask("Continue without the service (--no-service)? The gateway won't start at login.", false) {
				o.NoService = true
				pre.opts.noService = true
				pre.recheck("service-manager")
			}
		}
	}
	if missing := pre.MissingRequired(); len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for _, d := range missing {
			names = append(names, d.Name)
		}
		if o.DryRun {
			pre.printManual(missing)
		}
		e.printf("Stopped: %s missing. Nothing was written.\n", strings.Join(names, ", "))
		return ExitStopped
	}

	p := pre.Paths
	prev, err := readState(p.State)
	if err != nil {
		e.printf("Stopped: cannot read %s: %v\n", p.tilde(p.State), err)
		return ExitStopped
	}
	if o.DryRun {
		e.printf("Dry run: nothing is written or run.\n")
	}
	pl, cfg, err := buildPlan(e, c, pre, prev, o)
	if err != nil {
		e.printf("Stopped: %v. Nothing was written.\n", err)
		return ExitStopped
	}
	if o.DryRun {
		printDryRun(e, p, pl)
		return ExitOK
	}
	printPlan(e, p, pl)
	if !o.Yes && !e.ask("Proceed?", true) {
		if !e.Interactive {
			e.printf("No terminal to ask on: run again with --yes to install without asking.\n")
		}
		e.printf("Stopped. Nothing was written.\n")
		return ExitStopped
	}
	if err := apply(e, p, pl); err != nil {
		e.printf("Install failed: %v\n", err)
		var down *startError
		if errors.As(err, &down) {
			e.printf("The gateway is not running. Start it with:\n    %s\nthen run `gateway doctor`.\n", down.Fix)
		}
		return ExitStopped
	}
	healthy := true
	if pl.service {
		healthy = waitHealthy(e, cfg)
	}
	summary(e, c, p, pl, prev, cfg, healthy)
	if !healthy {
		return ExitStopped
	}
	return ExitOK
}

func onlyServiceMissing(pre *Preflight) bool {
	m := pre.MissingRequired()
	return len(m) == 1 && m[0].ID == "service-manager"
}

func checkVerdict(e *Env, pre *Preflight) int {
	if m := pre.MissingRequired(); len(m) > 0 {
		pre.printManual(m)
		e.printf("Not ready: a required dependency is missing. `gateway install` offers to install what it can.\n")
		return ExitStopped
	}
	e.printf("Ready to install. Nothing was written or run.\n")
	return ExitOK
}

// defaultConfig is the config install writes when there is none: loopback only, with
// the sandbox on only when its prerequisites passed (research D6).
func defaultConfig(sandbox bool) []byte {
	return fmt.Appendf(nil, `# llm-gateway config, written by `+"`gateway install`"+` because there was none. It is
# yours now: install never overwrites it, and uninstall keeps it (unless --purge).
# Every other key and its default is in config.example.yaml:
# https://github.com/brutally-Honest/llm-gateway/blob/main/config.example.yaml
# Loopback only: the gateway has no auth.
listen_addr: 127.0.0.1:7197

ui:
  listen_addr: 127.0.0.1:7199

effects:
  network:
    http_proxy_addr: 127.0.0.1:7198
    socks_proxy_addr: 127.0.0.1:7195
    # Claude Code's Bash sandbox, so every command's traffic reaches the proxy. true only
    # when its prerequisites were found (docs/install.md, Requirements).
    sandbox: %t
`, sandbox)
}

func buildPlan(e *Env, c Client, pre *Preflight, prev *State, o InstallOptions) (plan, config.Config, error) {
	p := pre.Paths
	pl := plan{state: State{Version: e.Version, Backups: map[string]string{}}}
	if prev != nil {
		pl.state.ConfigCreated = prev.ConfigCreated
	}

	// The binary (research D18).
	if exe, err := filepath.EvalSymlinks(e.Exe); err == nil && exe != "" {
		dst, _ := filepath.EvalSymlinks(p.Binary)
		if exe != dst && !sameFile(exe, p.Binary) {
			pl.changes = append(pl.changes, change{label: "binary", path: p.Binary, copyFrom: exe})
		}
	}

	// The config: written only if none exists (AC12).
	sandboxOK := pre.SandboxOK()
	_, cfgExists, err := readMaybe(p.Config)
	if err != nil {
		return pl, config.Config{}, err
	}
	var cfg config.Config
	if cfgExists {
		if cfg, err = e.LoadConfig(p.Config); err != nil {
			return pl, cfg, fmt.Errorf("the config %s is not valid (%w); fix it or move it away", p.tilde(p.Config), err)
		}
		pl.notes = append(pl.notes, "Config "+p.tilde(p.Config)+" exists: kept as it is.")
	} else {
		cfg = config.Defaults()
		cfg.Effects.Network.Sandbox = sandboxOK
		pl.changes = append(pl.changes, change{label: "config", path: p.Config, data: defaultConfig(sandboxOK)})
		pl.state.ConfigCreated = true
	}
	// The same rule the gateway starts with (spec 022), before anything is written.
	if e := config.CheckListeners(cfg); e != nil {
		return pl, cfg, fmt.Errorf("the config %s is not valid (%s); fix it or move it away", p.tilde(p.Config), e.Message())
	}

	// The client's settings file (AC13, research D5).
	sandbox := cfg.Effects.Network.Sandbox && sandboxOK
	if !sandboxOK {
		pl.off = append(pl.off, "network effects, sandbox part: "+sandboxReason(pre)+" (only programs that honour HTTPS_PROXY are seen)")
	} else if !cfg.Effects.Network.Sandbox {
		pl.off = append(pl.off, "network effects, sandbox part: effects.network.sandbox is false in your config")
	}
	settings := c.Settings(cfg, sandbox)
	pl.changes = append(pl.changes, fileChange(prev, "settings", p.Settings, settings, &pl))
	pl.state.Files = append(pl.state.Files, p.Settings)

	// The service (AC14, AC16).
	if !o.NoService {
		svc := e.service()
		file := svc.File(p)
		ch := fileChange(prev, "service", file, svc.Render(p), &pl)
		var prior *PriorService
		if prev != nil && prev.Service != nil {
			prior = prev.Service.Prior // kept across upgrades: it is the state before the first install
		}
		if ch.exists && ch.changed() && !managedFile(prev, file, ch.old) {
			// What the user's own service was doing, so uninstall can put it back (D25).
			st := svc.Status(p)
			prior = &PriorService{Enabled: st.AtLogin, Running: st.Running}
			pl.notes = append(pl.notes, fmt.Sprintf("Found a %s service file not made by install: %s (%s)",
				svc.Kind(), p.tilde(file), priorWords(*prior)))
			if !confirmReplace(e, o, p, file, string(ch.old)) {
				pl.notes = append(pl.notes, "Kept your service file; no service was set up by install.")
				delete(pl.state.Backups, file)
				o.NoService = true
			}
		}
		if !o.NoService {
			pl.changes = append(pl.changes, ch)
			pl.commands = append(pl.commands, svc.StartCommands(p, ch.changed())...)
			pl.serviceChanged = ch.changed()
			pl.state.Files = append(pl.state.Files, file)
			pl.state.Service = &ServiceState{Kind: svc.Kind(), File: file, Prior: prior}
			pl.service = true
		}
	}
	if o.NoService {
		pl.off = append(pl.off, "start at login: --no-service; start it by hand with "+manualStart(p))
	}

	// The shell (AC15, AC16).
	health := "http://" + cfg.ListenAddr + "/healthz"
	block := shellBlock(e, c, p, health)
	sh, rc := shellRC(e)
	switch {
	case o.NoShell:
		pl.off = append(pl.off, "claude function: --no-shell; add it by hand (docs/install.md)")
	case rc == "":
		pl.manual = block
		pl.off = append(pl.off, "claude function: "+orUnknown(sh)+" is not zsh or bash; add the lines printed below to "+manualRC(sh))
	default:
		if err := shellChange(e, c, o, p, prev, rc, block, &pl); err != nil {
			return pl, cfg, err
		}
	}
	for _, other := range otherRCs(e, rc) {
		if f := readRC(other, c.Binary); len(f.defs) > 0 {
			pl.notes = append(pl.notes, "Also found a "+c.Binary+" function in "+p.tilde(other)+" (another shell's file): left as it is.")
		}
	}

	if !pre.found("git") {
		pl.off = append(pl.off, "file diffs from git: git not found (diffs only from stored copies of small text files)")
	}
	if !pre.found("docker") {
		pl.off = append(pl.off, "observability stack: Docker not found")
	}

	if prev != nil {
		for k, v := range prev.Backups {
			if _, ok := pl.state.Backups[k]; !ok {
				pl.state.Backups[k] = v
			}
		}
	}
	if len(pl.state.Backups) == 0 {
		pl.state.Backups = nil
	}
	pl.changes = append(pl.changes, change{label: "state", path: p.State, data: pl.state.encode()})
	last := &pl.changes[len(pl.changes)-1]
	last.old, last.exists, _ = readMaybe(p.State)
	return pl, cfg, nil
}

// priorWords says what a user's own service was doing.
func priorWords(s PriorService) string {
	on := "not enabled at login"
	if s.Enabled {
		on = "enabled at login"
	}
	if s.Running {
		return on + ", running"
	}
	return on + ", not running"
}

func orUnknown(s string) string {
	if s == "" {
		return "your shell"
	}
	return s
}

func sandboxReason(pre *Preflight) string {
	var why []string
	for _, id := range []string{"bwrap", "socat", "apparmor"} {
		if d, ok := pre.dep(id); ok && !d.Found {
			why = append(why, d.Name+": "+d.Detail)
		}
	}
	if len(why) == 0 {
		return "prerequisites missing"
	}
	return strings.Join(why, "; ")
}

func fileChange(prev *State, label, path string, data []byte, pl *plan) change {
	old, exists, _ := readMaybe(path)
	ch := change{label: label, path: path, old: old, exists: exists, data: data}
	// A file install did not write is kept as a .bak before install takes it over (AC16),
	// even when its content is already the same, so uninstall can put the user's own file
	// back instead of deleting it (AC19).
	if exists && !managedFile(prev, path, old) {
		ch.backup = true
	}
	if ch.backup {
		pl.state.Backups[path] = backupPath(path)
	} else if b := prev.backup(path); b != "" {
		pl.state.Backups[path] = b
	}
	return ch
}

// managedFile says install wrote path: the state lists it or it carries the mark.
func managedFile(prev *State, path string, data []byte) bool {
	return prev.owns(path) || bytes.Contains(data, []byte(managedMark))
}

func confirmReplace(e *Env, o InstallOptions, p Paths, path, old string) bool {
	e.printf("\nFound in %s, not made by install:\n", p.tilde(path))
	for line := range strings.SplitSeq(strings.TrimSuffix(old, "\n"), "\n") {
		e.printf("  │ %s\n", line)
	}
	if o.DryRun {
		e.printf("A real run asks before replacing it, keeping a .bak.\n")
		return true
	}
	if o.Yes {
		e.printf("Replacing it (--yes); a .bak copy is kept.\n")
		return true
	}
	return e.ask("Replace it with the managed version (a .bak copy is kept)?", false)
}

func readRC(path, name string) rcFile {
	data, _, _ := readMaybe(path)
	return parseRC(string(data), name)
}

// otherRCs are the zsh and bash startup files other than the one install edits.
func otherRCs(e *Env, rc string) []string {
	zdot := e.Getenv("ZDOTDIR")
	if zdot == "" {
		zdot = e.Home
	}
	var out []string
	for _, f := range []string{filepath.Join(zdot, ".zshrc"), filepath.Join(e.Home, ".bashrc"), filepath.Join(e.Home, ".bash_profile")} {
		if f != rc && !slices.Contains(out, f) {
			if _, err := os.Stat(f); err == nil {
				out = append(out, f)
			}
		}
	}
	return out
}

func shellChange(e *Env, c Client, o InstallOptions, p Paths, prev *State, rc, block string, pl *plan) error {
	data, exists, err := readMaybe(rc)
	if err != nil {
		return err
	}
	f := parseRC(string(data), c.Binary)
	st := &ShellState{File: rc, Created: !exists}
	if prev != nil && prev.Shell != nil && prev.Shell.File == rc {
		st.Replaced, st.Created = prev.Shell.Replaced, prev.Shell.Created
	}
	replace := false
	if len(f.defs) > 0 {
		var found strings.Builder
		for _, d := range f.defs {
			found.WriteString(f.text(d))
		}
		pl.notes = append(pl.notes, fmt.Sprintf("Found a hand-written %s function in %s (line %d).", c.Binary, p.tilde(rc), f.defs[0].Start+1))
		if confirmReplace(e, o, p, rc, found.String()) {
			replace = true
		} else {
			pl.notes = append(pl.notes, "Kept your function; the managed one was not added, so there are never two.")
			pl.off = append(pl.off, "managed claude function: you kept your own in "+p.tilde(rc))
			return nil
		}
	}
	out, replaced := f.withBlock(block, replace)
	if replaced != "" {
		st.Replaced = replaced
	}
	ch := change{label: "shell", path: rc, old: data, exists: exists, data: []byte(out), backup: replace}
	if replace {
		pl.state.Backups[rc] = backupPath(rc)
	} else if b := prev.backup(rc); b != "" {
		pl.state.Backups[rc] = b
	}
	pl.changes = append(pl.changes, ch)
	pl.state.Shell = st
	pl.shell = rc
	return nil
}

func printPlan(e *Env, p Paths, pl plan) {
	e.printf("Plan\n")
	for _, n := range pl.notes {
		e.printf("  • %s\n", n)
	}
	for _, ch := range pl.changes {
		e.printf("  %s\n", describe(p, ch))
		if ch.label == "shell" && ch.changed() {
			if d := unifiedDiff(p.tilde(ch.path), string(ch.old), string(ch.data)); d != "" {
				for line := range strings.SplitSeq(strings.TrimSuffix(d, "\n"), "\n") {
					e.printf("      %s\n", line)
				}
			}
		}
	}
	for _, cmd := range pl.commands {
		e.printf("  run   %s\n", strings.Join(cmd, " "))
	}
	if pl.manual != "" {
		e.printf("  Lines to add by hand:\n%s", indent(pl.manual, "      "))
	}
	e.printf("\n")
}

func describe(p Paths, ch change) string {
	verb := "write"
	switch {
	case ch.copyFrom != "":
		return "copy  " + p.tilde(ch.copyFrom) + " → " + p.tilde(ch.path)
	case !ch.changed():
		verb = "keep "
	case ch.exists:
		verb = "update"
	}
	s := fmt.Sprintf("%-5s %s  (%s)", verb, p.tilde(ch.path), ch.label)
	if ch.backup {
		s += ", keeping a .bak of the current one"
	}
	return s
}

func printDryRun(e *Env, p Paths, pl plan) {
	for _, n := range pl.notes {
		e.printf("• %s\n", n)
	}
	for _, ch := range pl.changes {
		switch {
		case ch.copyFrom != "":
			e.printf("\n== copy %s → %s\n", p.tilde(ch.copyFrom), p.tilde(ch.path))
		case !ch.changed() && ch.backup:
			e.printf("\n== %s (%s): unchanged, not made by install; a copy is kept as %s for uninstall\n",
				p.tilde(ch.path), ch.label, p.tilde(backupPath(ch.path)))
		case !ch.changed():
			e.printf("\n== %s (%s): unchanged\n", p.tilde(ch.path), ch.label)
		case !ch.exists:
			e.printf("\n== %s (%s): new file\n%s", p.tilde(ch.path), ch.label, indent(string(ch.data), "   "))
		default:
			if ch.backup {
				e.printf("\n== %s (%s): changed; the current one is kept as %s\n", p.tilde(ch.path), ch.label, p.tilde(backupPath(ch.path)))
			} else {
				e.printf("\n== %s (%s): changed\n", p.tilde(ch.path), ch.label)
			}
			e.printf("%s", unifiedDiff(p.tilde(ch.path), string(ch.old), string(ch.data)))
		}
	}
	if len(pl.commands) > 0 {
		e.printf("\n== commands\n")
		for _, cmd := range pl.commands {
			e.printf("   %s\n", strings.Join(cmd, " "))
		}
	}
	if pl.manual != "" {
		e.printf("\n== lines to add by hand\n%s", indent(pl.manual, "   "))
	}
	e.printf("\n")
}

func indent(s, pre string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(strings.TrimSuffix(s, "\n"), "\n") {
		b.WriteString(pre + line + "\n")
	}
	return b.String()
}

func apply(e *Env, p Paths, pl plan) error {
	for _, ch := range pl.changes {
		if !ch.changed() && !ch.backup {
			continue
		}
		if ch.backup && ch.exists {
			b := pl.state.Backups[ch.path]
			if b == "" {
				b = backupPath(ch.path)
			}
			if err := writeAtomic(b, ch.old, 0o600); err != nil {
				return fmt.Errorf("backing up %s: %w", ch.path, err)
			}
		}
		if !ch.changed() {
			continue // taken over as it is: only its copy was written
		}
		var err error
		if ch.copyFrom != "" {
			err = copyAtomic(ch.copyFrom, ch.path)
		} else {
			err = writeAtomic(ch.path, ch.data, 0o644)
		}
		if err != nil {
			return fmt.Errorf("writing %s: %w", ch.path, err)
		}
	}
	if pl.service {
		return e.service().Start(p, pl.serviceChanged)
	}
	return nil
}

func waitHealthy(e *Env, cfg config.Config) bool {
	url := "http://" + cfg.ListenAddr + "/healthz"
	e.printf("Waiting for %s ", url)
	deadline := e.HealthWait
	if deadline == 0 {
		deadline = 30 * time.Second
	}
	const step = 500 * time.Millisecond
	for waited := time.Duration(0); waited <= deadline; waited += step {
		if code, err := e.HTTPStatus(url); err == nil && code == 200 {
			e.printf("ok\n\n")
			return true
		}
		e.Sleep(step)
	}
	e.printf("no answer\n\n")
	return false
}

func summary(e *Env, c Client, p Paths, pl plan, prev *State, cfg config.Config, healthy bool) {
	svc := e.service()
	if healthy {
		e.printf("Done: llm-gateway %s is installed.\n", e.Version)
	} else {
		e.printf("Installed, but the gateway did not answer /healthz. See its log: %s\n", svc.LogHint(p))
		e.printf("Then run `gateway doctor`.\n")
	}
	if cfg.UI.Enabled {
		e.printf("  UI         http://%s (it opens once its start-up warm-up ends, up to about 30 s on a large store)\n", cfg.UI.ListenAddr)
	}
	if prev != nil {
		from := prev.Version
		if from == "" {
			from = "unknown"
		}
		e.printf("  Upgrade    %s → %s\n", from, e.Version)
	}
	e.printf("  Installed\n")
	for _, ch := range pl.changes {
		what := "unchanged"
		switch {
		case ch.copyFrom != "":
			what = "copied"
		case !ch.exists:
			what = "new"
		case ch.changed():
			what = "updated"
		}
		line := fmt.Sprintf("    %-8s %-9s %s", ch.label, what, p.tilde(ch.path))
		if ch.backup {
			line += "  (original kept as " + p.tilde(pl.state.Backups[ch.path]) + ")"
		}
		e.printf("%s\n", line)
	}
	if pl.service {
		e.printf("    service  %s, starts at login; log: %s\n", svc.Kind(), svc.LogHint(p))
		for _, n := range svc.Notes() {
			e.printf("    note     %s\n", n)
		}
	}
	if c.NetworkNote != "" && cfg.Effects.Enabled && cfg.Effects.Network.Enabled {
		e.printf("    note     %s\n", c.NetworkNote)
	}
	if len(pl.off) > 0 {
		e.printf("  Off\n")
		for _, o := range pl.off {
			e.printf("    – %s\n", o)
		}
	}
	if !pl.service {
		e.printf("\nStart the gateway by hand (it won't start at login):\n    %s\n", manualStart(p))
	}
	if pl.manual != "" {
		e.printf("\nAdd these lines by hand:\n%s", indent(pl.manual, "    "))
	}
	e.printf("\nOpen a new terminal, then run %s.\n", c.Binary)
}
