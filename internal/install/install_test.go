package install

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// Each required dependency missing stops the install before any gateway file is
// written, once its offer (if any) is declined (spec AC2).
func TestRequiredMissingStopsNothingWritten(t *testing.T) {
	cases := map[string]func(f *fake){
		"platform":        func(f *fake) { f.env.GOARCH = "386" },
		"client":          func(f *fake) { delete(f.path, "claude") },
		"curl":            func(f *fake) { delete(f.path, "curl") },
		"service-manager": func(f *fake) { f.svc.ok = false },
		"ports":           func(f *fake) { f.busy[7197] = "nginx (pid 42)" },
	}
	for id, breakIt := range cases {
		t.Run(id, func(t *testing.T) {
			f := newFake(t, "linux")
			breakIt(f)
			f.answer("n", "n", "n")
			before := f.snapshot()
			if code := f.install(InstallOptions{}); code != ExitStopped {
				t.Fatalf("exit %d, want %d\n%s", code, ExitStopped, f.out)
			}
			contains(t, f.out.String(), "Stopped:")
			contains(t, f.out.String(), "Nothing was written.")
			if after := f.snapshot(); !maps.Equal(before, after) {
				t.Fatalf("files changed: %v → %v", keys(before), keys(after))
			}
			if a := f.actions(); len(a) > 0 {
				t.Fatalf("ran %v", a)
			}
			if id == "ports" {
				contains(t, f.out.String(), "7197 held by nginx (pid 42)")
			}
		})
	}
}

// Each optional dependency missing lets the install go on, with the feature it serves
// reported off (spec AC3).
func TestOptionalMissingContinuesFeatureOff(t *testing.T) {
	cases := map[string]struct {
		breakIt func(f *fake)
		off     string
	}{
		"git":      {func(f *fake) { delete(f.path, "git") }, "file diffs from git: git not found"},
		"bwrap":    {func(f *fake) { delete(f.path, "bwrap") }, "network effects, sandbox part: bwrap: not found"},
		"socat":    {func(f *fake) { delete(f.path, "socat") }, "network effects, sandbox part: socat: not found"},
		"docker":   {func(f *fake) { delete(f.path, "docker") }, "observability stack: Docker not found"},
		"shell":    {func(f *fake) { f.setEnv("SHELL", "/usr/bin/fish") }, "claude function: fish is not zsh or bash"},
		"apparmor": {func(f *fake) { f.restrictUserns() }, "AppArmor and bwrap: unprivileged user namespaces restricted"},
	}
	for id, c := range cases {
		t.Run(id, func(t *testing.T) {
			f := newFake(t, "linux")
			c.breakIt(f)
			f.answer("n", "n", "n")
			if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
				t.Fatalf("exit %d\n%s", code, f.out)
			}
			contains(t, f.out.String(), c.off)
			contains(t, f.out.String(), "Open a new terminal, then run claude.")
		})
	}
}

func (f *fake) restrictUserns() {
	f.t.Helper()
	p := filepath.Join(f.t.TempDir(), "restrict")
	if err := os.WriteFile(p, []byte("1\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	f.env.AppArmorRestrict = p
}

var shownCmd = regexp.MustCompile(`would run:\n    (.+)\n`)

// "y" runs exactly the command shown, then checks the dependency again (spec AC4).
func TestOfferYesRunsShownCommandAndRechecks(t *testing.T) {
	f := newFake(t, "linux")
	delete(f.path, "git")
	f.answer("y", "y")
	if code := f.install(InstallOptions{}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	m := shownCmd.FindStringSubmatch(f.out.String())
	if m == nil {
		t.Fatalf("no command shown:\n%s", f.out)
	}
	if m[1] != "sudo apt-get install -y git" || len(f.run.shells) != 1 || f.run.shells[0] != m[1] {
		t.Fatalf("shown %q, ran %v", m[1], f.run.shells)
	}
	contains(t, f.out.String(), "git now found")
	lacks(t, f.out.String(), "file diffs from git")
}

// "N" goes on for an optional dependency and stops for a required one (spec AC4).
func TestOfferNoOptionalContinuesRequiredStops(t *testing.T) {
	f := newFake(t, "linux")
	delete(f.path, "socat")
	f.answer("n", "y")
	if code := f.install(InstallOptions{}); code != ExitOK {
		t.Fatalf("optional: exit %d\n%s", code, f.out)
	}
	f = newFake(t, "linux")
	delete(f.path, "curl")
	f.answer("n")
	if code := f.install(InstallOptions{}); code != ExitStopped {
		t.Fatalf("required: exit %d\n%s", code, f.out)
	}
	if len(f.run.shells) != 0 {
		t.Fatalf("ran %v", f.run.shells)
	}
}

// --install-deps runs every offer without asking; --yes alone runs none (spec AC5).
func TestInstallDepsRunsAllYesRunsNone(t *testing.T) {
	missing := func(f *fake) {
		for _, c := range []string{"git", "bwrap", "socat", "claude"} {
			delete(f.path, c)
		}
	}
	f := newFake(t, "linux")
	missing(f)
	f.env.In = strings.NewReader("") // nothing to answer with: it must not ask
	if code := f.install(InstallOptions{InstallDeps: true, Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	want := []string{"curl -fsSL https://claude.ai/install.sh | bash", "sudo apt-get install -y git",
		"sudo apt-get install -y bubblewrap", "sudo apt-get install -y socat"}
	if strings.Join(f.run.shells, "|") != strings.Join(want, "|") {
		t.Fatalf("ran %q, want %q", f.run.shells, want)
	}
	contains(t, f.out.String(), "Logging in stays with you")

	f = newFake(t, "linux")
	delete(f.path, "git")
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	if len(f.run.shells) != 0 {
		t.Fatalf("--yes ran %v", f.run.shells)
	}
}

// sudo appears only inside a shown package-manager command: never in another command,
// a file install writes, or a command shown as root (spec AC5).
func TestSudoOnlyInShownPackageCommand(t *testing.T) {
	f := newFake(t, "linux")
	delete(f.path, "git")
	if code := f.install(InstallOptions{InstallDeps: true, Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	for _, o := range f.run.outputs {
		if strings.Contains(o, "sudo") {
			t.Fatalf("sudo in %q", o)
		}
	}
	for _, s := range f.run.shells {
		if !strings.HasPrefix(s, "sudo apt-get install -y ") {
			t.Fatalf("sudo outside a package command: %q", s)
		}
	}
	for rel := range f.snapshot() {
		if strings.Contains(f.read(rel), "sudo") && !strings.HasSuffix(rel, "gateway") {
			t.Fatalf("sudo in %s", rel)
		}
	}
	for line := range strings.SplitSeq(f.out.String(), "\n") {
		if strings.Contains(line, "sudo") && !strings.Contains(line, "sudo apt-get install -y git") {
			t.Fatalf("sudo in output line %q", line)
		}
	}

	r := newFake(t, "linux")
	r.env.Root = true
	delete(r.path, "git")
	r.install(InstallOptions{InstallDeps: true, Yes: true})
	if len(r.run.shells) != 1 || r.run.shells[0] != "apt-get install -y git" {
		t.Fatalf("as root ran %v", r.run.shells)
	}
}

func TestPackageManagerPerOS(t *testing.T) {
	cases := []struct {
		osRelease, cmd, want string
	}{
		{"ID=fedora\n", "dnf", "sudo dnf install -y git"},
		{"ID=arch\n", "pacman", "sudo pacman -S --needed --noconfirm git"},
		{"ID=linuxmint\nID_LIKE=\"ubuntu debian\"\n", "apt-get", "sudo apt-get install -y git"},
	}
	for _, c := range cases {
		f := newFake(t, "linux")
		delete(f.path, "apt-get")
		f.path[c.cmd] = true
		if err := os.WriteFile(f.env.OSRelease, []byte(c.osRelease), 0o600); err != nil {
			t.Fatal(err)
		}
		delete(f.path, "git")
		f.install(InstallOptions{InstallDeps: true, Yes: true})
		if len(f.run.shells) != 1 || f.run.shells[0] != c.want {
			t.Fatalf("%s: ran %v, want %q", c.cmd, f.run.shells, c.want)
		}
	}
	m := newFake(t, "darwin")
	delete(m.path, "git")
	m.install(InstallOptions{InstallDeps: true, Yes: true})
	if len(m.run.shells) != 1 || m.run.shells[0] != "brew install git" {
		t.Fatalf("darwin ran %v", m.run.shells)
	}
}

// No package manager: manual steps for each missing item, nothing run, no package
// manager installed (spec AC7).
func TestNoPackageManagerPrintsManualSteps(t *testing.T) {
	f := newFake(t, "darwin")
	delete(f.path, "brew")
	delete(f.path, "git")
	if code := f.install(InstallOptions{InstallDeps: true, Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	if len(f.run.shells) != 0 {
		t.Fatalf("ran %v", f.run.shells)
	}
	contains(t, f.out.String(), "To install by hand:")
	contains(t, f.out.String(), "install Homebrew (https://brew.sh), then `brew install git`; the installer never installs a package manager")

	l := newFake(t, "linux")
	delete(l.path, "apt-get")
	delete(l.path, "curl")
	if code := l.install(InstallOptions{InstallDeps: true, Yes: true}); code != ExitStopped {
		t.Fatalf("linux exit %d\n%s", code, l.out)
	}
	contains(t, l.out.String(), "install the `curl` package with your distribution's package manager")
}

// --check and --dry-run write nothing and run nothing (spec AC9, AC10).
func TestCheckAndDryRunWriteAndRunNothing(t *testing.T) {
	for _, o := range []InstallOptions{{Check: true}, {DryRun: true}, {DryRun: true, InstallDeps: true, Yes: true}} {
		f := newFake(t, "linux")
		delete(f.path, "git")
		f.write(".zshrc", "claude() {\n  echo mine\n}\n")
		before := f.snapshot()
		if code := f.install(o); code != ExitOK {
			t.Fatalf("%+v: exit %d\n%s", o, code, f.out)
		}
		if after := f.snapshot(); !maps.Equal(before, after) {
			t.Fatalf("%+v: files changed", o)
		}
		if a := f.actions(); len(a) > 0 {
			t.Fatalf("%+v: ran %v", o, a)
		}
		lacks(t, f.out.String(), "Run it now?")
		if o.Check {
			// B10 (spec 024 AC7): install.sh adds where the archive went.
			contains(t, f.out.String(), "Ready to install. Nothing was installed.\n")
		}
		if o.DryRun {
			out := f.out.String()
			contains(t, out, "Dry run: nothing is written or run.")
			contains(t, out, "config.yaml (config): new file")
			contains(t, out, "   listen_addr: 127.0.0.1:7197")
			contains(t, out, "+# >>> llm-gateway >>>")
			contains(t, out, "-  echo mine")
			contains(t, out, "   fake-svc start")
		}
	}
}

// Installing twice gives byte-identical files; the second run says nothing changed and
// still restarts the service (spec AC17, research D20).
func TestInstallTwiceIdentical(t *testing.T) {
	f := newFake(t, "linux")
	f.write(".zshrc", "export A=1\n")
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	first := f.snapshot()
	f.run.outputs = nil
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("second exit %d\n%s", code, f.out)
	}
	if second := f.snapshot(); !maps.Equal(first, second) {
		t.Fatalf("second install changed files:\n%v\n%v", first, second)
	}
	out := f.out.String()
	contains(t, out, "Upgrade    1.2.3 → 1.2.3")
	contains(t, out, "settings unchanged")
	contains(t, out, "shell    unchanged")
	if !strings.Contains(strings.Join(f.run.outputs, "|"), "fake-svc start") {
		t.Fatalf("service not restarted: %v", f.run.outputs)
	}
	if n := strings.Count(f.read(".zshrc"), markBegin); n != 1 {
		t.Fatalf("%d marker blocks", n)
	}
}

// An upgrade keeps an edited config and the data, and says what changed.
func TestUpgradeKeepsConfigAndData(t *testing.T) {
	f := newFake(t, "linux")
	f.install(InstallOptions{Yes: true})
	cfg := f.read(".config/llm-gateway/config.yaml") + "log_level: debug\n"
	f.write(".config/llm-gateway/config.yaml", cfg)
	f.write(".local/share/llm-gateway/store.db", "data")
	f.env.Version = "1.3.0"
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	if f.read(".config/llm-gateway/config.yaml") != cfg || f.read(".local/share/llm-gateway/store.db") != "data" {
		t.Fatal("config or data changed")
	}
	contains(t, f.out.String(), "Upgrade    1.2.3 → 1.3.0")
	contains(t, f.out.String(), "state    updated")
}

// Uninstall removes exactly what install added: everything but the kept config and
// binary is as it was (spec AC19).
func TestUninstallRemovesExactlyWhatInstallAdded(t *testing.T) {
	for _, rc := range []string{"export A=1\n", "export A=1\n# llm-gateway: mine\nclaude() {\n  echo mine\n}\nalias x=y\n", ""} {
		f := newFake(t, "linux")
		if rc != "" {
			f.write(".zshrc", rc)
		}
		before := f.snapshot()
		if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
			t.Fatalf("exit %d\n%s", code, f.out)
		}
		if code := f.uninstall(UninstallOptions{Yes: true}); code != ExitOK {
			t.Fatalf("uninstall exit %d\n%s", code, f.out)
		}
		after := f.snapshot()
		delete(after, ".config/llm-gateway/config.yaml")
		delete(after, ".local/bin/gateway")
		if !maps.Equal(before, after) {
			t.Fatalf("rc %q: before %v, after %v", rc, keys(before), keys(after))
		}
		if !strings.Contains(strings.Join(f.run.outputs, "|"), "fake-svc stop") {
			t.Fatal("service not stopped")
		}
		contains(t, f.out.String(), "captured data")
	}
}

// Uninstall puts the replaced function back where the block was, keeping edits made to
// the file since (spec E7).
func TestUninstallKeepsLaterEdits(t *testing.T) {
	f := newFake(t, "linux")
	f.write(".zshrc", "export A=1\nclaude() {\n  echo mine\n}\n")
	f.install(InstallOptions{Yes: true})
	f.write(".zshrc", f.read(".zshrc")+"export B=2\n")
	f.uninstall(UninstallOptions{Yes: true})
	if got, want := f.read(".zshrc"), "export A=1\nclaude() {\n  echo mine\n}\nexport B=2\n"; got != want {
		t.Fatalf("zshrc:\n%s\nwant:\n%s", got, want)
	}
	if _, err := os.Stat(filepath.Join(f.env.Home, ".zshrc.bak")); err == nil {
		contains(t, f.out.String(), "it differs from today's file, so it was kept")
	}
}

// --purge asks once more, naming the data size, and deletes config and data.
func TestUninstallPurge(t *testing.T) {
	f := newFake(t, "linux")
	f.install(InstallOptions{Yes: true})
	f.write(".local/share/llm-gateway/store.db", strings.Repeat("x", 2048))
	f.answer("y", "n")
	f.uninstall(UninstallOptions{Purge: true})
	contains(t, f.out.String(), "Delete 2.0 KiB of captured data")
	if _, err := os.Stat(filepath.Join(f.env.Home, ".local/share/llm-gateway/store.db")); err != nil {
		t.Fatal("data deleted after no")
	}
	f.install(InstallOptions{Yes: true})
	f.answer("y", "y")
	f.uninstall(UninstallOptions{Purge: true})
	for _, rel := range []string{".local/share/llm-gateway", ".config/llm-gateway"} {
		if _, err := os.Stat(filepath.Join(f.env.Home, rel)); err == nil {
			t.Fatalf("%s kept after purge", rel)
		}
	}
}

// --purge deletes only the config folder and the default data folder: a capture.dir of
// the user's own and the startup-error record are listed as kept and stay on disk.
func TestUninstallPurgeListsWhatItKeeps(t *testing.T) {
	f := newFake(t, "linux")
	f.install(InstallOptions{Yes: true})
	custom := filepath.Join(f.env.Home, "mydata")
	f.write("mydata/store.db", "keep me")
	f.write(".local/share/llm-gateway/store.db", "gone")
	rec := f.write(".local/state/llm-gateway/startup-error.json", "{}")
	f.env.LoadConfig = func(string) (config.Config, error) {
		cfg := config.Defaults()
		cfg.Capture.Dir = custom
		return cfg, nil
	}
	f.uninstall(UninstallOptions{Purge: true, Yes: true})
	out := f.out.String()
	contains(t, out, "~/mydata (capture.dir from your config; --purge deletes only the default data folder)")
	contains(t, out, "~/.local/state/llm-gateway/startup-error.json (the last startup error record)")
	contains(t, out, "Deleted ~/.config/llm-gateway and ~/.local/share/llm-gateway.")
	for _, path := range []string{filepath.Join(custom, "store.db"), rec} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s deleted by --purge: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.env.Home, ".local/share/llm-gateway")); err == nil {
		t.Fatal("default data folder kept after purge")
	}
}

// A settings file the user made lives in the config folder, which --purge deletes: it is
// named on the deleted line, not listed as kept. Without --purge, or when purge is
// declined, it is still listed as kept.
func TestUninstallPurgeNamesUnownedSettingsAsDeleted(t *testing.T) {
	setup := func() *fake {
		f := newFake(t, "linux")
		f.install(InstallOptions{Yes: true})
		f.uninstall(UninstallOptions{Yes: true})
		f.write(".config/llm-gateway/claude-settings.json", `{"mine":true}`)
		return f
	}
	const keepLine = "~/.config/llm-gateway/claude-settings.json (not made by install)"

	f := setup()
	f.uninstall(UninstallOptions{Purge: true, Yes: true})
	out := f.out.String()
	lacks(t, out, keepLine)
	contains(t, out, "purge   ~/.config/llm-gateway and ~/.local/share/llm-gateway (including your own claude-settings.json)")
	contains(t, out, "Deleted ~/.config/llm-gateway and ~/.local/share/llm-gateway (including your own claude-settings.json).")
	if _, err := os.Stat(filepath.Join(f.env.Home, ".config/llm-gateway/claude-settings.json")); err == nil {
		t.Fatal("settings file kept after purge")
	}

	f = setup()
	f.uninstall(UninstallOptions{Yes: true})
	contains(t, f.out.String(), keepLine)

	f = setup()
	f.answer("y", "n")
	f.uninstall(UninstallOptions{Purge: true})
	out = f.out.String()
	contains(t, out, "Keeping the config and the data.")
	contains(t, out, keepLine)
	if _, err := os.Stat(filepath.Join(f.env.Home, ".config/llm-gateway/claude-settings.json")); err != nil {
		t.Fatalf("settings file deleted although purge was declined: %v", err)
	}
}

// A capture.dir inside the default data folder is deleted by --purge, so it is not
// listed as kept.
func TestUninstallPurgeDoesNotListCaptureDirInsideDataFolder(t *testing.T) {
	f := newFake(t, "linux")
	f.install(InstallOptions{Yes: true})
	inside := filepath.Join(f.env.Home, ".local/share/llm-gateway/captures")
	f.write(".local/share/llm-gateway/captures/store.db", "gone")
	f.env.LoadConfig = func(string) (config.Config, error) {
		cfg := config.Defaults()
		cfg.Capture.Dir = inside
		return cfg, nil
	}
	f.uninstall(UninstallOptions{Purge: true, Yes: true})
	lacks(t, f.out.String(), "~/.local/share/llm-gateway/captures (capture.dir")
	if _, err := os.Stat(filepath.Join(inside, "store.db")); err == nil {
		t.Fatal("capture.dir inside the data folder kept after purge")
	}
}

func TestWithin(t *testing.T) {
	cases := []struct {
		name, dir, root string
		want            bool
	}{
		{"equal", "/a/llm-gateway", "/a/llm-gateway", true},
		{"equal with trailing slash", "/a/llm-gateway/", "/a/llm-gateway", true},
		{"child", "/a/llm-gateway/captures", "/a/llm-gateway", true},
		{"sibling with shared prefix", "/a/llm-gateway-2", "/a/llm-gateway", false},
		{"parent", "/a", "/a/llm-gateway", false},
		{"unrelated", "/b/captures", "/a/llm-gateway", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := within(c.dir, c.root); got != c.want {
				t.Fatalf("within(%q, %q) = %v, want %v", c.dir, c.root, got, c.want)
			}
		})
	}
}

// On macOS --purge also lists the service logs as kept.
func TestUninstallPurgeKeepsMacLogs(t *testing.T) {
	f, _ := launchdFake(t)
	f.install(InstallOptions{Yes: true})
	f.write("Library/Logs/llm-gateway/gateway.log", "x")
	f.uninstall(UninstallOptions{Purge: true, Yes: true})
	contains(t, f.out.String(), "~/Library/Logs/llm-gateway (the service logs)")
	if _, err := os.Stat(filepath.Join(f.env.Home, "Library/Logs/llm-gateway/gateway.log")); err != nil {
		t.Fatal("logs deleted by --purge")
	}
}

// A hand-written function is detected, shown and replaced only on yes, with a .bak;
// on no it is kept and no block is added, so there are never two (spec AC16).
func TestHandWrittenFunction(t *testing.T) {
	const mine = "# llm-gateway: load the gateway settings only when it answers\nclaude() {\n  echo mine\n}\n"
	f := newFake(t, "linux")
	f.write(".zshrc", "export A=1\n"+mine)
	f.answer("y", "y")
	if code := f.install(InstallOptions{}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	contains(t, f.out.String(), "Found a hand-written claude function in ~/.zshrc (line 2).")
	contains(t, f.out.String(), "  │   echo mine")
	rc := f.read(".zshrc")
	if strings.Contains(rc, "echo mine") || strings.Count(rc, "claude() {") != 1 {
		t.Fatalf("zshrc:\n%s", rc)
	}
	if f.read(".zshrc.bak") != "export A=1\n"+mine {
		t.Fatal("no .bak of the original")
	}

	n := newFake(t, "linux")
	n.write(".zshrc", mine)
	n.answer("n", "y")
	if code := n.install(InstallOptions{}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, n.out)
	}
	if n.read(".zshrc") != mine {
		t.Fatal("zshrc changed after no")
	}
	contains(t, n.out.String(), "managed claude function: you kept your own")
}

// A function in another shell's file is reported, not edited (spec E3).
func TestFunctionInOtherShellReported(t *testing.T) {
	f := newFake(t, "linux")
	f.write(".bashrc", "claude() {\n  echo bash\n}\n")
	f.install(InstallOptions{Yes: true})
	contains(t, f.out.String(), "Also found a claude function in ~/.bashrc (another shell's file): left as it is.")
	if f.read(".bashrc") != "claude() {\n  echo bash\n}\n" {
		t.Fatal(".bashrc edited")
	}
}

// A unit not made by install is shown and replaced with a .bak on yes (spec AC16).
func TestHandMadeUnitReplaced(t *testing.T) {
	f := newFake(t, "linux")
	unit := "[Service]\nExecStart=/opt/gateway\n"
	f.write(".config/systemd/user/llm-gateway.service", unit)
	f.answer("y", "y")
	if code := f.install(InstallOptions{}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	contains(t, f.out.String(), "Found a systemd service file not made by install")
	if f.read(".config/systemd/user/llm-gateway.service.bak") != unit {
		t.Fatal("no .bak of the hand-made unit")
	}
	contains(t, f.read(".config/systemd/user/llm-gateway.service"), managedMark)
	f.uninstall(UninstallOptions{Yes: true})
	if f.read(".config/systemd/user/llm-gateway.service") != unit {
		t.Fatal("hand-made unit not put back")
	}
}

// An unsupported shell gets the lines printed with the file they belong in (AC15).
func TestUnsupportedShellPrintsLines(t *testing.T) {
	f := newFake(t, "linux")
	f.setEnv("SHELL", "/usr/bin/fish")
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	out := f.out.String()
	contains(t, out, "Add these lines by hand:")
	contains(t, out, "    "+markBegin)
	contains(t, out, "~/.config/fish/config.fish")
	for _, rc := range []string{".zshrc", ".bashrc", ".bash_profile"} {
		if _, err := os.Stat(filepath.Join(f.env.Home, rc)); err == nil {
			t.Fatalf("%s written", rc)
		}
	}
}

// --no-service does everything but the service, and says it won't start at login (AC8);
// with no service manager install offers it.
func TestNoServicePath(t *testing.T) {
	f := newFake(t, "linux")
	if code := f.install(InstallOptions{Yes: true, NoService: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	if a := f.actions(); len(a) > 0 {
		t.Fatalf("ran %v", a)
	}
	if _, err := os.Stat(filepath.Join(f.env.Home, ".config/systemd/user/llm-gateway.service")); err == nil {
		t.Fatal("unit written")
	}
	contains(t, f.out.String(), "Start the gateway by hand (it won't start at login):")
	contains(t, f.out.String(), "~/.local/bin/gateway -config ~/.config/llm-gateway/config.yaml")

	n := newFake(t, "linux")
	n.svc.ok = false
	n.answer("y", "y")
	if code := n.install(InstallOptions{}); code != ExitOK {
		t.Fatalf("offered no-service: exit %d\n%s", code, n.out)
	}
	contains(t, n.out.String(), "no systemd user session (common in containers, WSL and minimal distros)")
	contains(t, n.out.String(), "Continue without the service (--no-service)?")
}

// Without a terminal, Proceed is not taken as yes (research D14).
func TestNoTerminalNeedsYes(t *testing.T) {
	f := newFake(t, "linux")
	f.env.Interactive = false
	before := f.snapshot()
	if code := f.install(InstallOptions{}); code != ExitStopped {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	contains(t, f.out.String(), "run again with --yes")
	if !maps.Equal(before, f.snapshot()) {
		t.Fatal("files written")
	}
}

// An existing config is never overwritten, and the settings come from it.
func TestExistingConfigKept(t *testing.T) {
	f := newFake(t, "linux")
	cfg := "listen_addr: 127.0.0.1:8197\n"
	f.write(".config/llm-gateway/config.yaml", cfg)
	f.install(InstallOptions{Yes: true})
	if f.read(".config/llm-gateway/config.yaml") != cfg {
		t.Fatal("config overwritten")
	}
	contains(t, f.read(".config/llm-gateway/claude-settings.json"), "127.0.0.1:8197")
	contains(t, f.read(".zshrc"), "http://127.0.0.1:8197/healthz")
}

// The sandbox goes in the settings only when its prerequisites passed (AC13).
func TestSandboxOnlyWithPrerequisites(t *testing.T) {
	f := newFake(t, "linux")
	f.install(InstallOptions{Yes: true})
	contains(t, f.read(".config/llm-gateway/claude-settings.json"), `"sandbox": true`)
	contains(t, f.read(".config/llm-gateway/config.yaml"), "sandbox: true")

	n := newFake(t, "linux")
	delete(n.path, "socat")
	n.install(InstallOptions{Yes: true})
	contains(t, n.read(".config/llm-gateway/claude-settings.json"), `"sandbox": false`)
	contains(t, n.read(".config/llm-gateway/config.yaml"), "sandbox: false")
}

// The upgrade path: the running gateway's own ports are not busy (spec E2).
func TestRunningGatewayPortsFree(t *testing.T) {
	f := newFake(t, "linux")
	f.busy[7197] = "gateway (pid 7)"
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	contains(t, f.out.String(), "7197 held by the running gateway")
}

// The summary's UI line is the address alone: the UI serves as soon as it binds (spec 030),
// so no start-up warm-up is claimed.
func TestSummaryUILineHasNoWarmupClaim(t *testing.T) {
	f := newFake(t, "linux")
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	var ui string
	for line := range strings.SplitSeq(f.out.String(), "\n") {
		if strings.HasPrefix(line, "  UI ") {
			ui = line
		}
	}
	if ui != "  UI         http://127.0.0.1:7199" {
		t.Fatalf("UI line = %q\n%s", ui, f.out)
	}
	lacks(t, ui, "warm-up")
	lacks(t, ui, "30 s")
}

// A gateway that never answers /healthz fails the install with the log hint.
func TestUnhealthyAfterInstall(t *testing.T) {
	f := newFake(t, "linux")
	f.healthy = false
	if code := f.install(InstallOptions{Yes: true}); code != ExitStopped {
		t.Fatalf("exit %d", code)
	}
	contains(t, f.out.String(), "did not answer /healthz")
	contains(t, f.out.String(), "journalctl --user -u llm-gateway.service -f")
}

// A client just installed into ~/.local/bin, not yet on PATH, is found (spec E5).
func TestClientFoundInLocalBin(t *testing.T) {
	f := newFake(t, "linux")
	delete(f.path, "claude")
	f.write(".local/bin/claude", "#!/bin/sh\n")
	f.install(InstallOptions{Check: true})
	contains(t, f.out.String(), "~/.local/bin/claude")
	lacks(t, f.out.String(), "✗")
}

// A settings file the user made, already identical to install's, is taken over with a
// .bak, so uninstall puts the user's file back instead of deleting it (AC19).
func TestIdenticalUnmanagedSettingsRestoredOnUninstall(t *testing.T) {
	f := newFake(t, "linux")
	f.install(InstallOptions{Yes: true})
	settings := f.read(".config/llm-gateway/claude-settings.json")
	f.uninstall(UninstallOptions{Yes: true})
	f.write(".config/llm-gateway/claude-settings.json", settings) // the user's own, same bytes
	before := f.snapshot()

	f.out.Reset()
	f.install(InstallOptions{DryRun: true})
	contains(t, f.out.String(), "unchanged, not made by install; a copy is kept as ~/.config/llm-gateway/claude-settings.json.bak")
	if code := f.install(InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	contains(t, f.out.String(), "(original kept as ~/.config/llm-gateway/claude-settings.json.bak)")
	f.uninstall(UninstallOptions{Yes: true})
	if after := f.snapshot(); !maps.Equal(before, after) {
		t.Fatalf("before %v\nafter %v", keys(before), keys(after))
	}
}

// B5: with network effects on, the summary says which Node versions get them (spec 024
// AC3); with them off it says nothing about Node.
func TestSummaryNamesNodeVersions(t *testing.T) {
	f := newFake(t, "linux")
	c := testClient()
	c.NetworkNote = "Node programs are seen from Node 22.21 or 24.0"
	f.out.Reset()
	if code := Install(f.env, c, InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, f.out)
	}
	contains(t, f.out.String(), "    note     Node programs are seen from Node 22.21 or 24.0")

	off := newFake(t, "linux")
	off.write(".config/llm-gateway/config.yaml", "effects:\n  network:\n    enabled: false\n")
	off.out.Reset()
	if code := Install(off.env, c, InstallOptions{Yes: true}); code != ExitOK {
		t.Fatalf("network off: exit %d\n%s", code, off.out)
	}
	lacks(t, off.out.String(), "Node programs")
}
