package claudecode

import "testing"

// spec 015 AC5, AC6, D7, L2: which Claude Code inputs name a path or host. Bash matches
// whole words only, a relative path against the call's working folder; read tools name
// nothing; the harness host list matches exactly.
func TestClaudeCode_NamesTarget(t *testing.T) {
	p := Profile{}
	cases := []struct {
		name, tool, input, workDir, target string
		want                               bool
	}{
		{"edit names its file", "Edit", `{"file_path":"/w/a.txt"}`, "/w", "/w/a.txt", true},
		{"write, relative path", "Write", `{"file_path":"src/b.go"}`, "/w", "/w/src/b.go", true},
		{"notebook", "NotebookEdit", `{"notebook_path":"/w/n.ipynb"}`, "/w", "/w/n.ipynb", true},
		{"edit of another file", "Edit", `{"file_path":"/w/a.txt"}`, "/w", "/w/b.txt", false},
		{"read names nothing", "Read", `{"file_path":"/w/a.txt"}`, "/w", "/w/a.txt", false},
		{"glob names nothing", "Glob", `{"pattern":"**/*"}`, "/w", "/w/a.txt", false},
		{"webfetch host", "WebFetch", `{"url":"https://Example.com/x?q=1"}`, "/w", "example.com", true},
		{"webfetch other host", "WebFetch", `{"url":"https://example.com/"}`, "/w", "example.org", false},
		{"bash absolute path", "Bash", `{"command":"rm /w/a.txt"}`, "/w", "/w/a.txt", true},
		{"bash relative path", "Bash", `{"command":"touch notes/todo.md && ls"}`, "/w", "/w/notes/todo.md", true},
		{"bash ./ path", "Bash", `{"command":"node ./gen.js"}`, "/w", "/w/gen.js", true},
		{"bash near miss", "Bash", `{"command":"cp a.txt.bak x"}`, "/w", "/w/a.txt", false},
		{"bash path inside another", "Bash", `{"command":"cat sub/a.txt"}`, "/w", "/w/a.txt", false},
		{"bash path outside workdir", "Bash", `{"command":"cat ../a.txt"}`, "/w", "/a.txt", false},
		{"bash host in url", "Bash", `{"command":"curl -s https://api.github.com/repos"}`, "/w", "api.github.com", true},
		{"bash host in longer host", "Bash", `{"command":"curl https://xapi.github.com"}`, "/w", "api.github.com", false},
		{"bash sentence dot", "Bash", `{"command":"echo see a.txt."}`, "/w", "/w/a.txt", true},
		{"other tool", "TodoWrite", `{"todos":["/w/a.txt"]}`, "/w", "/w/a.txt", false},
	}
	for _, c := range cases {
		if got := p.NamesTarget(c.tool, []byte(c.input), c.workDir, c.target); got != c.want {
			t.Errorf("%s: NamesTarget = %v, want %v", c.name, got, c.want)
		}
	}
	for host, want := range map[string]bool{"downloads.claude.ai": true, "API.anthropic.com": true,
		"mcp-proxy.anthropic.com": true, "http-intake.logs.us5.datadoghq.com": true, "bridge.claudeusercontent.com": true,
		"registry.npmjs.org": false, "anthropic.com": false} {
		if got := p.HarnessHost(host); got != want {
			t.Errorf("HarnessHost(%q) = %v, want %v", host, got, want)
		}
	}
}
