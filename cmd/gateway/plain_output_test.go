package main

import (
	"strings"
	"testing"
)

// runPlain runs the gateway command line args with no environment and returns its exit
// code and output.
func runPlain(t *testing.T, args ...string) (int, string) {
	t.Helper()
	inTempDir(t, nil)
	var out syncBuffer
	code := run(t.Context(), deps{args: args, lookupEnv: func(k string) (string, bool) {
		if k == "GATEWAY_CAPTURE_DIR" {
			return t.TempDir() + "/none", true // a store that does not exist
		}
		return "", false
	}, stdout: &out})
	return code, out.String()
}

// B14 (spec 024 AC10): the commands a person runs print plain text, never JSON log lines:
// help, usage and flag errors of every subcommand, --version, and the failures of the
// store readers. Structured logging is for the running gateway only.
func TestSubcommands_PrintPlainText(t *testing.T) {
	cases := []struct {
		args []string
		code int
		want string
	}{
		{[]string{"help"}, exitOK, "Usage:\n  gateway [-config <path>]"},
		{[]string{"-h"}, exitOK, "Usage:\n  gateway [-config <path>]"},
		{[]string{"--help"}, exitOK, "Usage:\n  gateway [-config <path>]"},
		{[]string{"--version"}, exitOK, "gateway " + version + "\n"},
		{[]string{"-version"}, exitOK, "gateway " + version + "\n"},
		{[]string{"version"}, exitOK, "gateway " + version + "\n"},
		{[]string{"install", "--help"}, exitOK, "Usage: gateway install"},
		{[]string{"uninstall", "--help"}, exitOK, "Usage: gateway uninstall"},
		{[]string{"doctor", "--help"}, exitOK, "Usage: gateway doctor"},
		{[]string{"sessions", "--help"}, exitOK, "Usage: gateway sessions [-config <path>]"},
		{[]string{"timeline", "--help"}, exitOK, "Usage: gateway timeline"},
		{[]string{"dump", "--help"}, exitOK, "Usage: gateway dump"},
		{[]string{"hooks", "print", "--help"}, exitOK, "Usage: gateway hooks print [-config <path>]"},
		{[]string{"backfill-sessions", "--help"}, exitOK, "Usage: gateway backfill-sessions [-config <path>]"},
		{[]string{"sessions", "-bogus"}, exitConfig, "gateway sessions: invalid flags"},
		{[]string{"timeline"}, exitConfig, "gateway timeline: invalid flags (reason: session key or -last required)"},
		{[]string{"dump", "one", "two"}, exitConfig, "gateway dump: invalid flags (reason: unexpected argument, count: 2)"},
		{[]string{"hooks"}, exitConfig, "gateway hooks: invalid flags"},
		{[]string{"sessions"}, exitRuntime, "gateway sessions: sessions failed ("},
		{[]string{"bogus-command"}, exitConfig, "gateway: unexpected argument (the gateway takes none"},
		{[]string{"-bogus"}, exitConfig, "gateway: flag provided but not defined: -bogus"},
	}
	for _, c := range cases {
		code, out := runPlain(t, c.args...)
		if strings.Contains(out, `{"level"`) || strings.HasPrefix(strings.TrimSpace(out), "{") {
			t.Errorf("gateway %s: printed a JSON log line:\n%s", strings.Join(c.args, " "), out)
			continue
		}
		if code != c.code || !strings.Contains(out, c.want) {
			t.Errorf("gateway %s: exit %d, output %q; want %d and %q", strings.Join(c.args, " "), code, out, c.code, c.want)
		}
	}
}
