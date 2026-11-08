package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// readInput is the part of a Read, Grep or Glob input the effect rules read.
type readInput struct {
	FilePath string `json:"file_path"`
	Path     string `json:"path"`
	Pattern  string `json:"pattern"`
	Offset   *int   `json:"offset"`
	Limit    *int   `json:"limit"`
}

// ReadTarget names the path Claude Code's Read, Grep or Glob looked at. Read gives its
// file_path and the line range from offset and limit; Grep and Glob give their path,
// "" when they searched the working folder, and their pattern.
func (Profile) ReadTarget(tool string, input []byte) (path, detail string, ok bool) {
	var in readInput
	if json.Unmarshal(input, &in) != nil {
		return "", "", false
	}
	switch tool {
	case "Read":
		if in.FilePath == "" {
			return "", "", false
		}
		return in.FilePath, lineRange(in.Offset, in.Limit), true
	case "Grep", "Glob":
		if in.Pattern == "" {
			return "", "", false
		}
		return in.Path, in.Pattern, true
	}
	return "", "", false
}

// lineRange is "lines a–b" from Read's 1-based offset and line count.
func lineRange(offset, limit *int) string {
	start := 1
	if offset != nil && *offset > 0 {
		start = *offset
	}
	switch {
	case limit != nil && *limit > 0:
		return fmt.Sprintf("lines %d–%d", start, start+*limit-1)
	case offset != nil:
		return fmt.Sprintf("from line %d", start)
	}
	return ""
}

// ReadContent pulls what a read tool returned out of a stored PostToolUse payload: the
// file text of a Read, the matched text of a Grep, the file list of a Glob, a plain
// string as it is, and anything else as its JSON.
func (Profile) ReadContent(payload []byte) ([]byte, bool) {
	var m struct {
		Response json.RawMessage `json:"tool_response"`
	}
	if json.Unmarshal(payload, &m) != nil || len(m.Response) == 0 || string(m.Response) == "null" {
		return nil, false
	}
	var s string
	if json.Unmarshal(m.Response, &s) == nil {
		return []byte(s), true
	}
	var r struct {
		File *struct {
			Content *string `json:"content"`
		} `json:"file"`
		Content   *string  `json:"content"`
		Filenames []string `json:"filenames"`
	}
	if json.Unmarshal(m.Response, &r) == nil {
		switch {
		case r.File != nil && r.File.Content != nil:
			return []byte(*r.File.Content), true
		case r.Content != nil:
			return []byte(*r.Content), true
		case r.Filenames != nil:
			return []byte(strings.Join(r.Filenames, "\n")), true
		}
	}
	out, err := json.MarshalIndent(json.RawMessage(m.Response), "", "  ")
	if err != nil {
		return nil, false
	}
	return out, true
}

// WritePaths are the paths Claude Code's Write, Edit, MultiEdit and NotebookEdit name.
func (Profile) WritePaths(tool string, input []byte) []string {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	if json.Unmarshal(input, &in) != nil {
		return nil
	}
	switch tool {
	case "Write", "Edit", "MultiEdit":
		if in.FilePath != "" {
			return []string{in.FilePath}
		}
	case "NotebookEdit":
		if in.NotebookPath != "" {
			return []string{in.NotebookPath}
		}
	}
	return nil
}

// ExtraDirs are the additionalDirectories of Claude Code's user settings and of the
// project and local settings in workDir. A relative folder is read against workDir, and
// "~/" against the home folder. A missing or unreadable file adds nothing.
func (Profile) ExtraDirs(workDir string) []string {
	home, _ := os.UserHomeDir()
	files := []string{}
	if home != "" {
		files = append(files, filepath.Join(home, ".claude", "settings.json"))
	}
	if workDir != "" {
		files = append(files, filepath.Join(workDir, ".claude", "settings.json"),
			filepath.Join(workDir, ".claude", "settings.local.json"))
	}
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		for _, d := range additionalDirectories(f) {
			switch {
			case d == "~" || strings.HasPrefix(d, "~/"):
				if home == "" {
					continue
				}
				d = filepath.Join(home, strings.TrimPrefix(d, "~"))
			case !filepath.IsAbs(d):
				if workDir == "" {
					continue
				}
				d = filepath.Join(workDir, d)
			}
			d = filepath.Clean(d)
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	return out
}

// additionalDirectories reads permissions.additionalDirectories from one settings file.
func additionalDirectories(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s struct {
		Permissions struct {
			AdditionalDirectories []string `json:"additionalDirectories"`
		} `json:"permissions"`
	}
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return s.Permissions.AdditionalDirectories
}

var _ core.EffectRules = Profile{}
