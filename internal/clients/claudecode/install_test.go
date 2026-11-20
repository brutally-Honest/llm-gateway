package claudecode

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	addr = "127.0.0.1:7197"
	base = "http://127.0.0.1:7197/anthropic"
)

// With network effects off the settings still point Claude Code at the gateway (023 D7).
func TestInstallSettingsAlwaysHasBaseURL(t *testing.T) {
	var doc struct {
		Env   map[string]string `json:"env"`
		Hooks map[string]any    `json:"hooks"`
	}
	if err := json.Unmarshal(InstallSettings(addr, base, nil), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Env["ANTHROPIC_BASE_URL"] != base || len(doc.Env) != 1 || len(doc.Hooks) == 0 {
		t.Fatalf("env %v, %d hooks", doc.Env, len(doc.Hooks))
	}
	net := &NetSettings{BaseURL: base, HTTPProxyAddr: "127.0.0.1:7198", HTTPProxyPort: 7198}
	if string(InstallSettings(addr, base, net)) != string(SettingsDoc(addr, net)) {
		t.Fatal("with network on it is not the printed settings document")
	}
}

func TestCheckSettings(t *testing.T) {
	good := InstallSettings(addr, base, nil)
	if why := CheckSettings(good, addr, base); why != "" {
		t.Fatalf("good settings: %s", why)
	}
	cases := map[string][]byte{
		"not valid JSON":                         []byte("{"),
		"no env.ANTHROPIC_BASE_URL":              []byte(`{"hooks":{}}`),
		"env.ANTHROPIC_BASE_URL is http:/":       InstallSettings(addr, "http://127.0.0.1:9/anthropic", nil),
		"a hook points at http://127.0.0.1:8197": []byte(strings.ReplaceAll(string(good), "7197/_gateway", "8197/_gateway")),
		"no hooks":                               []byte(`{"env":{"ANTHROPIC_BASE_URL":"` + base + `"}}`),
	}
	for want, data := range cases {
		if why := CheckSettings(data, addr, base); !strings.HasPrefix(why, want) {
			t.Errorf("want %q, got %q", want, why)
		}
	}
}

// The function is the one docs/clients/claude-code.md §6 shows.
func TestShellFunc(t *testing.T) {
	got := ShellFunc("http://127.0.0.1:7197/healthz", "$HOME/.config/llm-gateway/claude-settings.json")
	for _, want := range []string{
		"claude() {\n",
		"if curl -fsS --max-time 1 http://127.0.0.1:7197/healthz >/dev/null 2>&1; then",
		`command claude --settings "$HOME/.config/llm-gateway/claude-settings.json" "$@"`,
		`command claude "$@"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("lacks %q:\n%s", want, got)
		}
	}
	if HooksPath() != "/_gateway/hooks/claude-code" {
		t.Fatal(HooksPath())
	}
}
