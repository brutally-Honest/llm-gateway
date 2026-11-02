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
