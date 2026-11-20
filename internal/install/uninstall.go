package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// UninstallOptions are `gateway uninstall`'s flags.
type UninstallOptions struct {
	Yes, Purge bool
	// Force, with Yes, also skips the question about cutting off the calling session.
	Force bool
}

type removal struct {
	label   string
	path    string
	restore string // a .bak to move back into place after the removal
	rewrite []byte // instead of removing: the file's new content (the shell file)
	remove  bool
}

// Uninstall runs `gateway uninstall` and returns its exit code (spec AC19, AC20).
func Uninstall(e *Env, c Client, o UninstallOptions) int {
	p := paths(e, c)
	st, err := readState(p.State)
	if err != nil {
		e.printf("Stopped: cannot read %s: %v\n", p.tilde(p.State), err)
		return ExitStopped
	}
	svc := e.service()
	var steps []removal
	var stop, restart [][]string
	var keep []string
	var prior *PriorService

	// The service: only one install made (the state names it or it carries the mark).
	if file := svc.File(p); file != "" {
		if data, ok, _ := readMaybe(file); ok {
			if st.owns(file) || bytes.Contains(data, []byte(managedMark)) {
				stop = svc.StopCommands(p)
				r := removal{label: "service", path: file, remove: true, restore: existingBackup(st, file)}
				// The user's own service goes back to how install found it (D25).
				if r.restore != "" && st != nil && st.Service != nil && st.Service.Prior != nil {
					prior = st.Service.Prior
					restart = svc.RestoreCommands(p, prior.Enabled, prior.Running)
				}
				steps = append(steps, r)
			} else {
				keep = append(keep, p.tilde(file)+" (a service file not made by install)")
			}
		}
	}

	// The marker block, putting back what it replaced (research D10).
	rc := ""
	replaced := ""
	if st != nil && st.Shell != nil {
		rc, replaced = st.Shell.File, st.Shell.Replaced
	} else if _, f := shellRC(e); f != "" {
		rc = f
	}
	if rc != "" {
		if data, ok, _ := readMaybe(rc); ok {
			f := parseRC(string(data), c.Binary)
			if f.block != nil {
				out := f.withoutBlock(replaced)
				r := removal{label: "shell", path: rc, rewrite: []byte(out)}
				if st != nil && st.Shell != nil && st.Shell.Created && strings.TrimSpace(out) == "" {
					r = removal{label: "shell", path: rc, remove: true}
				}
				steps = append(steps, r)
			}
		}
	}

	// The settings file, restoring the one it replaced.
	if _, ok, _ := readMaybe(p.Settings); ok {
		if st.owns(p.Settings) {
			steps = append(steps, removal{label: "settings", path: p.Settings, remove: true, restore: existingBackup(st, p.Settings)})
		} else {
			keep = append(keep, p.tilde(p.Settings)+" (not made by install)")
		}
	}
	if st != nil {
		steps = append(steps, removal{label: "state", path: p.State, remove: true})
	}

	keep = append(keep, p.tilde(p.Binary)+" (the gateway binary; remove it last with rm "+p.tilde(p.Binary)+")")
	if !o.Purge {
		keep = append(keep, p.tilde(p.Config)+" (your config)", p.tilde(p.DataDir)+" (captured data, "+humanBytes(dirSize(p.DataDir))+")")
	}
	keep = append(keep, "every dependency (Claude Code, git, bubblewrap, socat, …)")

	if len(steps) == 0 && !o.Purge {
		e.printf("Nothing to uninstall: no service, shell block or settings file made by install was found.\n")
		printKeep(e, keep)
		return ExitOK
	}

	e.printf("Uninstall\n  Will remove\n")
	for _, cmd := range stop {
		e.printf("    run     %s\n", strings.Join(cmd, " "))
	}
	for _, s := range steps {
		switch {
		case s.rewrite != nil:
			old, _, _ := readMaybe(s.path)
			e.printf("    %-7s the marker block from %s\n", s.label, p.tilde(s.path))
			if d := unifiedDiff(p.tilde(s.path), string(old), string(s.rewrite)); d != "" {
				e.printf("%s", indent(d, "        "))
			}
		default:
			e.printf("    %-7s %s\n", s.label, p.tilde(s.path))
		}
		if s.restore != "" {
			e.printf("            and put back the original from %s\n", p.tilde(s.restore))
			if s.label == "service" && prior != nil {
				e.printf("            as it was before install: %s\n", priorWords(*prior))
			}
		}
	}
	for _, cmd := range restart {
		e.printf("    run     %s\n", strings.Join(cmd, " "))
	}
	if o.Purge {
		e.printf("    purge   %s and %s\n", p.tilde(p.ConfigDir), p.tilde(p.DataDir))
	}
	e.printf("  Will keep\n")
	for _, k := range keep {
		e.printf("    %s\n", k)
	}
	e.printf("\n")
	if !o.Yes && !e.ask("Uninstall?", false) {
		e.printf("Stopped. Nothing was changed.\n")
		return ExitStopped
	}
	// Stopping the gateway cuts off a client session routed through it, such as the
	// Claude Code session running this command; --yes still asks, --force does not (D27).
	if len(stop) > 0 && c.Routed != nil && !o.Force {
		if why := c.Routed(e.Getenv, uninstallConfig(e, p)); why != "" {
			e.printf("\nWarning: this command runs in a session routed through this gateway (%s).\n", why)
			e.printf("Stopping the service cuts that session off: its model calls fail until the gateway runs again.\n")
			if !e.ask("Stop the service anyway?", false) {
				e.printf("Stopped. Nothing was changed. Run gateway uninstall from a terminal outside that session, or pass --yes --force.\n")
				return ExitStopped
			}
		}
	}
	purge := false
	if o.Purge {
		size := humanBytes(dirSize(p.DataDir))
		q := fmt.Sprintf("Delete %s of captured data in %s and the config in %s? This cannot be undone.", size, p.tilde(p.DataDir), p.tilde(p.ConfigDir))
		if o.Yes {
			e.printf("%s yes (--yes)\n", q)
			purge = true
		} else {
			purge = e.ask(q, false)
		}
		if !purge {
			e.printf("Keeping the config and the data.\n")
		}
	}

	for _, cmd := range stop {
		if out, err := e.Run.Output(cmd[0], cmd[1:]...); err != nil {
			e.printf("  %s: %v %s (continuing)\n", strings.Join(cmd, " "), err, strings.TrimSpace(out))
		}
	}
	var failed []string
	for _, s := range steps {
		if err := doRemoval(s); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", p.tilde(s.path), err))
		}
	}
	if svc.Kind() == "systemd" && len(stop) > 0 {
		_, _ = e.Run.Output("systemctl", "--user", "daemon-reload")
	}
	var restored string
	if len(restart) > 0 {
		ok := true
		for _, cmd := range restart {
			if out, err := e.Run.Output(cmd[0], cmd[1:]...); err != nil {
				ok = false
				failed = append(failed, fmt.Sprintf("%s: %v %s", strings.Join(cmd, " "), err, strings.TrimSpace(out)))
			}
		}
		if ok {
			restored = fmt.Sprintf("Your own service file is back as it was before install (%s).\n  To stop it: %s\n",
				priorWords(*prior), svc.StopHint(p))
		}
	} else if prior != nil {
		restored = fmt.Sprintf("Your own service file is back as it was before install (%s).\n", priorWords(*prior))
	}
	// The rc file's .bak is dropped once its lines are back, if it holds nothing more.
	if st != nil && st.Shell != nil {
		if b := st.backup(rc); b != "" {
			now, _, _ := readMaybe(rc)
			orig, _, _ := readMaybe(b)
			if bytes.Equal(now, orig) {
				_ = os.Remove(b)
			} else if orig != nil {
				keep = append(keep, p.tilde(b)+" (the copy of "+p.tilde(rc)+" from before install; it differs from today's file, so it was kept)")
			}
		}
	}
	if purge {
		for _, dir := range []string{p.ConfigDir, p.DataDir} {
			if err := os.RemoveAll(dir); err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", p.tilde(dir), err))
			}
		}
	}
	if len(failed) > 0 {
		e.printf("Uninstall finished with errors:\n")
		for _, f := range failed {
			e.printf("  %s\n", f)
		}
		return ExitStopped
	}
	e.printf("Uninstalled.\n")
	if purge {
		e.printf("  Config and data deleted.\n")
	}
	if restored != "" {
		e.printf("%s", restored)
	}
	printKeep(e, keep)
	e.printf("Open a new terminal: %s then runs plain %s.\n", c.Binary, c.Name)
	return ExitOK
}

// uninstallConfig is the config the gateway runs with: the file, else the defaults.
func uninstallConfig(e *Env, p Paths) config.Config {
	if _, ok, _ := readMaybe(p.Config); ok && e.LoadConfig != nil {
		if cfg, err := e.LoadConfig(p.Config); err == nil {
			return cfg
		}
	}
	return config.Defaults()
}

func printKeep(e *Env, keep []string) {
	e.printf("  Kept\n")
	for _, k := range keep {
		e.printf("    %s\n", k)
	}
}

func existingBackup(st *State, path string) string {
	b := st.backup(path)
	if b == "" {
		return ""
	}
	if _, err := os.Stat(b); err != nil {
		return ""
	}
	return b
}

func doRemoval(s removal) error {
	if s.rewrite != nil {
		return writeAtomic(s.path, s.rewrite, 0o644)
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if s.restore != "" {
		return os.Rename(s.restore, s.path)
	}
	return nil
}
