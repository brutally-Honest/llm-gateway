package claudecode

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// harnessHosts are the hosts Claude Code connects to on its own behalf: update downloads,
// telemetry, its API side checks and the claude.ai MCP proxy (spec 015 research D5). An
// entry starting with "." matches that domain's subdomains. A tool call whose input names
// one of these hosts still wins (named beats harness, D6).
var harnessHosts = []string{
	"downloads.claude.ai",
	"http-intake.logs.us5.datadoghq.com",
	"api.anthropic.com",
	"mcp-proxy.anthropic.com",
}

// HarnessHost reports whether host is Claude Code's own traffic.
func (Profile) HarnessHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, h := range harnessHosts {
		if host == h || (strings.HasPrefix(h, ".") && strings.HasSuffix(host, h)) {
			return true
		}
	}
	return false
}

// NamesTarget reports whether a Claude Code tool call's input names target, an absolute
// path or a host. Write, Edit, MultiEdit and NotebookEdit name their file; Read its file;
// Grep and Glob their folder and everything under it (the working folder when they give
// none); WebFetch its URL's host; Bash any path or host appearing as a whole word in its
// command, a path also relative to workDir. No shell parsing (spec 015 AC5, D7).
func (p Profile) NamesTarget(tool string, input []byte, workDir, target string) bool {
	if target == "" {
		return false
	}
	isPath := filepath.IsAbs(target)
	switch tool {
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		if !isPath {
			return false
		}
		for _, w := range p.WritePaths(tool, input) {
			if absIn(w, workDir) == target {
				return true
			}
		}
	case "Read", "Grep", "Glob":
		if !isPath {
			return false
		}
		path, _, ok := p.ReadTarget(tool, input)
		if !ok {
			return false
		}
		if path == "" {
			path = workDir
		}
		if path == "" {
			return false
		}
		path = absIn(path, workDir)
		if tool == "Read" {
			return path == target
		}
		return path == target || strings.HasPrefix(target, strings.TrimSuffix(path, "/")+"/")
	case "WebFetch":
		var in struct {
			URL string `json:"url"`
		}
		if isPath || json.Unmarshal(input, &in) != nil {
			return false
		}
		u, err := url.Parse(in.URL)
		return err == nil && strings.EqualFold(u.Hostname(), target)
	case "Bash":
		var in struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(input, &in) != nil || in.Command == "" {
			return false
		}
		if !isPath {
			return wholeWord(strings.ToLower(in.Command), strings.ToLower(target), hostChar)
		}
		if wholeWord(in.Command, target, pathChar) {
			return true
		}
		if workDir == "" {
			return false
		}
		rel, err := filepath.Rel(workDir, target)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			return false
		}
		return wholeWord(in.Command, rel, pathChar) || wholeWord(in.Command, "./"+rel, pathChar)
	}
	return false
}

// absIn is p made absolute against dir and cleaned.
func absIn(p, dir string) string {
	if filepath.IsAbs(p) || dir == "" {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, p)
}

// pathChar is a byte that can continue a path, so a match next to one is not a whole word.
func pathChar(b byte) bool {
	return hostChar(b) || b == '/' || b == '~'
}

// hostChar is a byte that can continue a host name.
func hostChar(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '.' || b == '-' || b == '_'
}

// wholeWord reports whether word occurs in s with no word byte right before or after it.
func wholeWord(s, word string, isWord func(byte) bool) bool {
	for from := 0; from <= len(s)-len(word); {
		i := strings.Index(s[from:], word)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(word)
		before := i == 0 || !isWord(s[i-1])
		// A trailing dot ends a sentence, not a name: "see a.txt." still names a.txt.
		after := end == len(s) || !isWord(s[end]) || (s[end] == '.' && (end+1 == len(s) || !isWord(s[end+1])))
		if before && after {
			return true
		}
		from = i + 1
	}
	return false
}

var _ core.AttributionRules = Profile{}
