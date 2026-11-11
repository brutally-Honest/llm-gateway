package claudecode

import (
	"encoding/json"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// toolInput is every field a Claude Code tool's input may carry that a summary reads.
type toolInput struct {
	Command      string `json:"command"`
	FilePath     string `json:"file_path"`
	NotebookPath string `json:"notebook_path"`
	Path         string `json:"path"`
	Pattern      string `json:"pattern"`
	URL          string `json:"url"`
	Query        string `json:"query"`
	Skill        string `json:"skill"`
	SubagentType string `json:"subagent_type"`
	Description  string `json:"description"`
	OldString    string `json:"old_string"`
	NewString    string `json:"new_string"`
	Content      string `json:"content"`
	NewSource    string `json:"new_source"`
	Edits        []struct {
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	} `json:"edits"`
}

// SummarizeTool turns a Claude Code tool call into a line (spec 016 AC5): Bash its
// command; Read, Write, Edit, MultiEdit and NotebookEdit their file, with the lines an
// edit adds and removes; Grep its pattern and folder; Glob its pattern; WebFetch its URL
// without the scheme; WebSearch its query; Skill its name; Agent its type and description.
// Any other tool is "other" with no argument, so it shows by name alone.
func (Profile) SummarizeTool(name string, input []byte, workDir string) core.ToolSummary {
	out := core.ToolSummary{Kind: core.ToolOther, Added: -1, Removed: -1}
	var in toolInput
	if json.Unmarshal(input, &in) != nil {
		in = toolInput{}
	}
	path := func(p string) string {
		if p == "" {
			return ""
		}
		return absIn(p, workDir)
	}
	switch name {
	case "Bash":
		out.Kind, out.Arg = core.ToolCommand, oneLine(in.Command)
	case "Read":
		out.Kind, out.Path = core.ToolFile, path(in.FilePath)
	case "Write":
		out.Kind, out.Path = core.ToolFile, path(in.FilePath)
		out.Added, out.Removed = lineCount(in.Content), -1
	case "Edit":
		out.Kind, out.Path = core.ToolFile, path(in.FilePath)
		out.Added, out.Removed = editLines(in.OldString, in.NewString)
	case "MultiEdit":
		out.Kind, out.Path = core.ToolFile, path(in.FilePath)
		out.Added, out.Removed = 0, 0
		for _, e := range in.Edits {
			a, r := editLines(e.OldString, e.NewString)
			out.Added, out.Removed = out.Added+a, out.Removed+r
		}
	case "NotebookEdit":
		out.Kind, out.Path = core.ToolFile, path(in.NotebookPath)
	case "Grep":
		out.Kind, out.Path = core.ToolSearch, path(in.Path)
		out.Arg = "“" + in.Pattern + "”"
	case "Glob":
		out.Kind, out.Arg, out.Path = core.ToolSearch, in.Pattern, path(in.Path)
	case "WebFetch":
		out.Kind = core.ToolFetch
		out.Arg = strings.TrimPrefix(strings.TrimPrefix(in.URL, "https://"), "http://")
	case "WebSearch":
		out.Kind, out.Arg = core.ToolFetch, in.Query
	case "Skill":
		out.Kind, out.Arg = core.ToolSkill, in.Skill
	case "Agent":
		out.Kind = core.ToolAgent
		out.Arg = strings.Join(nonEmpty(in.SubagentType, in.Description), " · ")
	}
	return out
}

// oneLine is a command's first line, with " …" when more follow.
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i]) + " …"
	}
	return s
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// lineCount is how many lines s holds; a last line without a newline counts.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// editLines is the lines an edit adds and removes: the old and new text with their common
// leading and trailing lines left out.
func editLines(oldS, newS string) (added, removed int) {
	a, b := strings.Split(oldS, "\n"), strings.Split(newS, "\n")
	if oldS == "" {
		a = nil
	}
	if newS == "" {
		b = nil
	}
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	return len(b), len(a)
}

var _ core.ToolSummarizer = Profile{}
