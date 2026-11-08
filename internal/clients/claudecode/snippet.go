package claudecode

import (
	"encoding/json"
	"sort"
)

// HookSnippet is the ~/.claude/settings.json fragment that sends every hook event in the
// phase table to the gateway at addr. It uses http hooks (research D1): a connection
// failure is non-blocking in Claude Code, and the hook is async so it never delays a
// tool. It sets no decision field, so Claude Code carries on as it would without it.
func HookSnippet(addr string) []byte {
	names := make([]string, 0, len(phases))
	for n := range phases {
		names = append(names, n)
	}
	sort.Strings(names)
	hook := map[string]any{
		"type":    "http",
		"url":     "http://" + addr + "/_gateway/hooks/" + Name,
		"async":   true,
		"timeout": 5,
	}
	events := make(map[string]any, len(names))
	for _, n := range names {
		events[n] = []any{map[string]any{"hooks": []any{hook}}}
	}
	out, _ := json.MarshalIndent(map[string]any{"hooks": events}, "", "  ")
	return append(out, '\n')
}

// NetSettings is what the printed settings document needs for network effects (spec
// 014): where Claude Code sends its model calls, and the gateway's proxy listeners.
type NetSettings struct {
	// BaseURL is ANTHROPIC_BASE_URL: the gateway's listener and the adapter's prefix.
	BaseURL string
	// HTTPProxyAddr is the HTTP proxy listener, host:port.
	HTTPProxyAddr string
	// HTTPProxyPort and SocksProxyPort are the ports the sandbox block points at; a
	// SocksProxyPort of 0 leaves socksProxyPort out (SOCKS5 off).
	HTTPProxyPort, SocksProxyPort int
	// Sandbox adds the sandbox block, so every Bash command's traffic uses the proxy.
	Sandbox bool
}

// SettingsDoc is the Claude Code settings document the gateway prints: the hooks of
// HookSnippet and, with net set, an env block pointing Claude Code at the gateway and its
// proxy, plus a sandbox block unless net.Sandbox is false. A nil net prints exactly
// HookSnippet (AC12). It is saved as its own file and loaded with --settings; nothing
// here writes a settings file.
func SettingsDoc(hooksAddr string, net *NetSettings) []byte {
	if net == nil {
		return HookSnippet(hooksAddr)
	}
	var doc map[string]any
	_ = json.Unmarshal(HookSnippet(hooksAddr), &doc)
	doc["env"] = map[string]any{
		"ANTHROPIC_BASE_URL": net.BaseURL,
		"HTTPS_PROXY":        "http://" + net.HTTPProxyAddr,
		"HTTP_PROXY":         "http://" + net.HTTPProxyAddr,
		"NO_PROXY":           "127.0.0.1,localhost",
	}
	if net.Sandbox {
		network := map[string]any{"httpProxyPort": net.HTTPProxyPort}
		if net.SocksProxyPort != 0 {
			network["socksProxyPort"] = net.SocksProxyPort
		}
		doc["sandbox"] = map[string]any{"enabled": true, "network": network}
	}
	out, _ := json.MarshalIndent(doc, "", "  ")
	return append(out, '\n')
}
