package claudecode

import (
	"encoding/json"
	"fmt"
	"strings"
)

// What `gateway install` needs from Claude Code (spec 023, research D3).
const (
	// Binary is the command Claude Code installs.
	Binary = "claude"
	// InstallerCmd is Claude Code's official native installer, from
	// https://code.claude.com/docs/en/setup (023 research, Dependencies).
	InstallerCmd = "curl -fsSL https://claude.ai/install.sh | bash"
	// LoginNote is what the user still does after installing it.
	LoginNote = "run claude once and follow the browser prompts to log in"
	// SettingsFile is the settings file the shell function loads, in the gateway's config
	// folder (docs/clients/claude-code.md §6).
	SettingsFile = "claude-settings.json"
)

// HooksPath is the hooks ingest route for Claude Code on the API listener.
func HooksPath() string { return "/_gateway/hooks/" + Name }

// InstallSettings is the settings file install writes: SettingsDoc for the config, and
// always an env block with ANTHROPIC_BASE_URL, so a claude the function starts reaches
// the gateway even with network effects off (023 research D7).
func InstallSettings(hooksAddr, baseURL string, net *NetSettings) []byte {
	if net != nil {
		return SettingsDoc(hooksAddr, net)
	}
	var doc map[string]any
	_ = json.Unmarshal(HookSnippet(hooksAddr), &doc)
	doc["env"] = map[string]any{"ANTHROPIC_BASE_URL": baseURL}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return append(out, '\n')
}

// CheckSettings says why a settings file does not point at the gateway on hooksAddr with
// baseURL, or "" when it does.
func CheckSettings(data []byte, hooksAddr, baseURL string) string {
	var doc struct {
		Env   map[string]string `json:"env"`
		Hooks map[string][]struct {
			Hooks []struct {
				URL string `json:"url"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "not valid JSON"
	}
	if got := doc.Env["ANTHROPIC_BASE_URL"]; got != baseURL {
		if got == "" {
			return "no env.ANTHROPIC_BASE_URL (want " + baseURL + ")"
		}
		return fmt.Sprintf("env.ANTHROPIC_BASE_URL is %s, want %s", got, baseURL)
	}
	want := "http://" + hooksAddr + HooksPath()
	n := 0
	for _, matchers := range doc.Hooks {
		for _, m := range matchers {
			for _, h := range m.Hooks {
				if h.URL != want {
					return "a hook points at " + h.URL + ", want " + want
				}
				n++
			}
		}
	}
	if n == 0 {
		return "no hooks"
	}
	return ""
}

// ShellFunc is the `claude` function: it loads the gateway's settings only when the
// gateway answers, so a stopped gateway means plain Claude Code, never a broken one
// (docs/clients/claude-code.md §6).
func ShellFunc(healthURL, settingsPath string) string {
	return strings.Join([]string{
		"claude() {",
		"  if curl -fsS --max-time 1 " + healthURL + " >/dev/null 2>&1; then",
		`    command claude --settings "` + settingsPath + `" "$@"`,
		"  else",
		`    echo "llm-gateway is not running: starting plain Claude Code" >&2`,
		`    command claude "$@"`,
		"  fi",
		"}",
	}, "\n") + "\n"
}
