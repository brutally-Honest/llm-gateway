package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

func runText(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := run(context.Background(), deps{args: args, lookupEnv: func(string) (string, bool) { return "", false }, stdout: &out})
	return code, out.String()
}

// gateway help and each command's --help print text for a person (spec 023 AC28).
func TestHelpTexts(t *testing.T) {
	code, out := runText(t, "help")
	if code != exitOK || !strings.Contains(out, "gateway install [flags]") || !strings.Contains(out, "gateway doctor [--json]") {
		t.Fatalf("help: %d\n%s", code, out)
	}
	for cmd, want := range map[string]string{
		"install":   "--install-deps  answer yes to every offer",
		"uninstall": "--purge  also delete the config folder (~/.config/llm-gateway) and the default data",
		"doctor":    "--json  print the checks as one JSON document",
	} {
		for _, flag := range []string{"--help", "-h"} {
			code, out := runText(t, cmd, flag)
			if code != exitOK || !strings.Contains(out, want) || !strings.Contains(out, "Usage: gateway "+cmd) {
				t.Fatalf("%s %s: %d\n%s", cmd, flag, code, out)
			}
		}
	}
}

func TestInstallBadFlag(t *testing.T) {
	code, out := runText(t, "install", "--nope")
	if code != 2 || !strings.Contains(out, "Usage: gateway install") {
		t.Fatalf("%d\n%s", code, out)
	}
	code, _ = runText(t, "doctor", "extra")
	if code != 2 {
		t.Fatalf("positional argument: %d", code)
	}
}

// The real client's settings pass its own check, and the sandbox goes in only when both
// the config and preflight allow it.
func TestClaudeClientSettings(t *testing.T) {
	c := claudeClient()
	cfg := config.Defaults()
	if why := c.CheckSettings(c.Settings(cfg, true), cfg); why != "" {
		t.Fatal(why)
	}
	if bytes.Contains(c.Settings(cfg, true), []byte(`"sandbox"`)) {
		t.Fatal("sandbox with effects.network.sandbox false")
	}
	cfg.Effects.Network.Sandbox = true
	if !bytes.Contains(c.Settings(cfg, true), []byte(`"sandbox"`)) || bytes.Contains(c.Settings(cfg, false), []byte(`"sandbox"`)) {
		t.Fatal("sandbox not gated on preflight")
	}
}

// The config the installer loads ignores this shell's GATEWAY_* overrides: the service
// never sees them.
func TestInstallConfigIgnoresEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("listen_addr: 127.0.0.1:8197\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := func(k string) (string, bool) {
		switch k {
		case "GATEWAY_LISTEN_ADDR":
			return "127.0.0.1:9", true
		case "HOME":
			return dir, true
		}
		return "", false
	}
	cfg, err := loadInstallConfig(env)(path)
	if err != nil || cfg.ListenAddr != "127.0.0.1:8197" || cfg.Capture.Dir != filepath.Join(dir, ".local/share/llm-gateway") {
		t.Fatalf("%v %+v", err, cfg.Capture)
	}
	if err := os.WriteFile(path, []byte("listen_addr: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadInstallConfig(env)(path); err == nil || !strings.Contains(err.Error(), "listen_addr") {
		t.Fatalf("bad config: %v", err)
	}
}
