package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/clients/claudecode"
	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/install"
	"github.com/brutally-honest/llm-gateway/internal/protocols/anthropic"
)

// The install commands write text for a person, like the viewer (ADR 0007), to d.stdout.

const helpText = `gateway — an observability-first LLM gateway

Usage:
  gateway [-config <path>]     run the gateway (what the login service runs)
  gateway install [flags]      set up the gateway, its login service and the claude function
  gateway uninstall [flags]    remove what install set up; keeps your config and data
  gateway doctor [--json]      check the install and the running gateway
  gateway hooks print          print the Claude Code settings document
  gateway sessions | timeline | dump | backfill-sessions
                               read the capture store
  gateway help                 this text

Run "gateway <command> --help" for a command's flags.
Install guide: ` + install.DocsURL + `
`

const installHelp = `Usage: gateway install [--yes] [--install-deps] [--no-shell] [--no-service] [--check] [--dry-run]

Checks what the gateway needs (the preflight table), offers to install what is missing,
shows the plan, asks, then writes the config (only if none exists), the Claude Code
settings file, the login service and the claude function, and waits for /healthz.
Running it again upgrades: config and data are kept, managed files rewritten.

  --check         preflight only: no offers, nothing written or run
  --dry-run       preflight, then every file it would write (contents or diff) and every
                  command it would run; nothing written or run
  --yes           do not ask "Proceed?" (or before replacing a hand-written setup);
                  does NOT install dependencies
  --install-deps  answer yes to every offer to install a missing dependency
  --no-shell      do not add the claude function to your shell's startup file
  --no-service    no login service: the gateway won't start at login

Guide: ` + install.DocsURL + `
`

const uninstallHelp = `Usage: gateway uninstall [--yes] [--force] [--purge]

Shows what will be removed and kept, asks, then stops and removes the login service,
removes the claude function's marker block (putting back a function it replaced) and the
settings file. The config, the captured data and the gateway binary are kept. No
dependency is ever uninstalled.

  --yes    do not ask; run from a session routed through this gateway it still asks
           before stopping the service, since that cuts the session off
  --force  with --yes, do not ask that either
  --purge  also delete the config and the captured data (asks once more, naming the size)

A replaced service file of your own is put back as it was before install: enabled at
login and running again if it was.

Guide: ` + install.DocsURL + `#uninstall
`

const doctorHelp = `Usage: gateway doctor [--json]

Checks the dependencies and the live install: the binary, the login service, each
listener, the config, the Claude Code settings file, the claude function, hooks, the
store and leftovers of an older install. Each is pass, warn or fail with a fix and a
link. Exits 1 on any fail.

  --json  print the checks as one JSON document

Guide: ` + install.DocsURL + `#doctor
`

func runHelp(d deps) int {
	_, _ = io.WriteString(d.stdout, helpText)
	return exitOK
}

// installCmd parses one install command's flags; ok is false when it already printed
// usage or an error, and code is then the exit code.
func installFlags(d deps, name, help string, define func(*flag.FlagSet)) (ok bool, code int) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	define(fs)
	switch err := fs.Parse(d.args[1:]); {
	case errors.Is(err, flag.ErrHelp):
		_, _ = io.WriteString(d.stdout, help)
		return false, exitOK
	case err != nil:
		_, _ = fmt.Fprintf(d.stdout, "gateway %s: %v\n\n%s", name, err, help)
		return false, install.ExitUsage
	case fs.NArg() > 0:
		_, _ = fmt.Fprintf(d.stdout, "gateway %s: unexpected argument %q\n\n%s", name, fs.Arg(0), help)
		return false, install.ExitUsage
	}
	return true, 0
}

func runInstall(d deps) int {
	var o install.InstallOptions
	if ok, code := installFlags(d, "install", installHelp, func(fs *flag.FlagSet) {
		fs.BoolVar(&o.Yes, "yes", false, "")
		fs.BoolVar(&o.InstallDeps, "install-deps", false, "")
		fs.BoolVar(&o.NoShell, "no-shell", false, "")
		fs.BoolVar(&o.NoService, "no-service", false, "")
		fs.BoolVar(&o.Check, "check", false, "")
		fs.BoolVar(&o.DryRun, "dry-run", false, "")
	}); !ok {
		return code
	}
	return install.Install(installEnv(d), claudeClient(), o)
}

func runUninstall(d deps) int {
	var o install.UninstallOptions
	if ok, code := installFlags(d, "uninstall", uninstallHelp, func(fs *flag.FlagSet) {
		fs.BoolVar(&o.Yes, "yes", false, "")
		fs.BoolVar(&o.Purge, "purge", false, "")
		fs.BoolVar(&o.Force, "force", false, "")
	}); !ok {
		return code
	}
	return install.Uninstall(installEnv(d), claudeClient(), o)
}

func runDoctor(d deps) int {
	var o install.DoctorOptions
	if ok, code := installFlags(d, "doctor", doctorHelp, func(fs *flag.FlagSet) {
		fs.BoolVar(&o.JSON, "json", false, "")
	}); !ok {
		return code
	}
	return install.Doctor(installEnv(d), claudeClient(), o)
}

func installEnv(d deps) *install.Env {
	if d.installEnv != nil {
		return d.installEnv
	}
	return install.RealEnv(version, d.stdin, d.stdout, loadInstallConfig(d.lookupEnv))
}

// loadInstallConfig loads the config file as the login service will: from the file and
// defaults, with none of this shell's GATEWAY_* overrides, which the service never sees.
func loadInstallConfig(lookupEnv func(string) (string, bool)) func(string) (config.Config, error) {
	return func(path string) (config.Config, error) {
		adapters, _ := registered()
		env := func(k string) (string, bool) {
			if k == "HOME" || k == "XDG_DATA_HOME" {
				return lookupEnv(k)
			}
			return "", false
		}
		cfg, _, err := config.Load(config.Options{Path: filepath.Clean(path), LookupEnv: env, Upstreams: upstreamSpecs(adapters)})
		var ce *config.Error
		if errors.As(err, &ce) {
			msg := ce.Message() // the key, the value of a refused listener, the reason
			if ce.Line > 0 {
				msg += fmt.Sprintf(" (line %d)", ce.Line)
			}
			return cfg, errors.New(msg)
		}
		if err == nil && cfg.Capture.Dir == "" {
			cfg.Capture.Dir, _ = config.DefaultCaptureDir(env)
		}
		return cfg, err
	}
}

// claudeClient is the one client the installer wires (023 research D3).
func claudeClient() install.Client {
	baseURL := func(cfg config.Config) string {
		return "http://" + cfg.ListenAddr + anthropic.Adapter{}.Prefix()
	}
	return install.Client{
		Name:         "Claude Code",
		Binary:       claudecode.Binary,
		VersionArgs:  []string{"--version"},
		InstallerCmd: claudecode.InstallerCmd,
		LoginNote:    claudecode.LoginNote,
		SettingsFile: claudecode.SettingsFile,
		Settings: func(cfg config.Config, sandbox bool) []byte {
			n := netSettings(cfg)
			if n != nil {
				n.Sandbox = n.Sandbox && sandbox
			}
			return claudecode.InstallSettings(cfg.ListenAddr, baseURL(cfg), n)
		},
		CheckSettings: func(data []byte, cfg config.Config) string {
			return claudecode.CheckSettings(data, cfg.ListenAddr, baseURL(cfg))
		},
		ShellFunc:   claudecode.ShellFunc,
		NetworkNote: claudecode.NodeNote,
		HooksPath:   claudecode.HooksPath(),
		Routed: func(getenv func(string) string, cfg config.Config) string {
			return claudecode.Routed(getenv, baseURL(cfg))
		},
	}
}

// isHelp says whether args ask for the top-level help: the `help` command. The -h and
// -help flags keep their JSON usage line (spec 000).
func isHelp(args []string) bool {
	return len(args) > 0 && strings.TrimSpace(args[0]) == "help"
}
