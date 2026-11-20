package install

import (
	"regexp"
	"slices"
	"strings"
)

const (
	markBegin = "# >>> llm-gateway >>>"
	markEnd   = "# <<< llm-gateway <<<"
)

// shellBlock is the marker block: the function and, on macOS when it is missing,
// ~/.local/bin on PATH (spec AC15).
func shellBlock(e *Env, c Client, p Paths, healthURL string) string {
	var b strings.Builder
	b.WriteString(markBegin + "\n")
	b.WriteString("# " + managedMark + "; `gateway uninstall` removes it. Edits here are overwritten.\n")
	if e.GOOS == "darwin" && !onPath(e, p.BinDir) {
		b.WriteString(`case ":$PATH:" in *":$HOME/.local/bin:"*) ;; *) export PATH="$HOME/.local/bin:$PATH" ;; esac` + "\n")
	}
	b.WriteString(strings.TrimRight(c.ShellFunc(healthURL, p.shellPath(p.Settings)), "\n") + "\n")
	b.WriteString(markEnd + "\n")
	return b.String()
}

func onPath(e *Env, dir string) bool {
	return slices.Contains(strings.Split(e.Getenv("PATH"), ":"), dir)
}

// funcStart matches the first line of a shell function named name: `name() {`,
// `name () {` or `function name`.
func funcStart(name string) *regexp.Regexp {
	q := regexp.QuoteMeta(name)
	return regexp.MustCompile(`^\s*(function\s+` + q + `\b|` + q + `\s*\(\)\s*(\{|$))`)
}

// span is a range of lines [Start, End) in a file.
type span struct{ Start, End int }

// rcFile is a shell startup file split into lines, with the marker block and any
// hand-written definition of the function found in it.
type rcFile struct {
	lines   []string
	trailNL bool
	block   *span  // the marker block, markers included
	defs    []span // hand-written definitions outside the block, with a leading comment
}

func parseRC(data, name string) rcFile {
	f := rcFile{trailNL: strings.HasSuffix(data, "\n")}
	if data != "" {
		f.lines = strings.Split(strings.TrimSuffix(data, "\n"), "\n")
	}
	start := funcStart(name)
	for i := 0; i < len(f.lines); i++ {
		line := f.lines[i]
		if strings.TrimSpace(line) == markBegin && f.block == nil {
			end := i + 1
			for end < len(f.lines) && strings.TrimSpace(f.lines[end]) != markEnd {
				end++
			}
			if end < len(f.lines) {
				end++
			}
			f.block = &span{i, end}
			i = end - 1
			continue
		}
		if start.MatchString(line) {
			end := i + 1
			if !strings.Contains(line, "}") || strings.Count(line, "{") > strings.Count(line, "}") {
				for end < len(f.lines) && strings.TrimSpace(f.lines[end]) != "}" {
					end++
				}
				if end < len(f.lines) {
					end++
				}
			}
			s := i
			// A comment line just above that names the gateway goes with it (research D10).
			above := ""
			if s > 0 {
				above = strings.TrimSpace(f.lines[s-1])
			}
			if strings.HasPrefix(above, "#") && strings.Contains(above, "llm-gateway") &&
				above != markEnd && above != markBegin && (f.block == nil || s-1 >= f.block.End) {
				s--
			}
			f.defs = append(f.defs, span{s, end})
			i = end - 1
		}
	}
	return f
}

func (f rcFile) join(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func (f rcFile) text(s span) string { return f.join(f.lines[s.Start:s.End]) }

// withBlock returns the file with block in place: replacing the old block, else the
// hand-written definitions (the first one's place gets the block) when replace is true,
// else appended. replaced is the hand-written text the block took the place of.
func (f rcFile) withBlock(block string, replace bool) (out, replaced string) {
	blockLines := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	var lines []string
	switch {
	case replace && len(f.defs) > 0:
		// Drop every hand-written definition; the block goes where the old block was,
		// else where the first definition was, so there are never two (AC16).
		var kept, gone []string
		at, next := -1, 0
		cut := slices.Clone(f.defs)
		if f.block != nil {
			cut = append(cut, *f.block)
			slices.SortFunc(cut, func(a, b span) int { return a.Start - b.Start })
		}
		for _, d := range cut {
			kept = append(kept, f.lines[next:d.Start]...)
			if f.block != nil && d == *f.block {
				at = len(kept)
			} else {
				gone = append(gone, f.lines[d.Start:d.End]...)
				if f.block == nil && at < 0 {
					at = len(kept)
				}
			}
			next = d.End
		}
		kept = append(kept, f.lines[next:]...)
		lines = slices.Concat(kept[:at], blockLines, kept[at:])
		replaced = f.join(gone)
	case f.block != nil:
		lines = slices.Concat(f.lines[:f.block.Start], blockLines, f.lines[f.block.End:])
	default:
		lines = slices.Clone(f.lines)
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, blockLines...)
	}
	return f.join(lines), replaced
}

// withoutBlock removes the marker block, putting restore (the hand-written lines it
// replaced, if any) back in its place (spec E7). A blank line install added above an
// appended block goes with it.
func (f rcFile) withoutBlock(restore string) string {
	if f.block == nil {
		return f.join(f.lines)
	}
	var back []string
	if restore != "" {
		back = strings.Split(strings.TrimSuffix(restore, "\n"), "\n")
	}
	before := f.lines[:f.block.Start]
	after := f.lines[f.block.End:]
	if restore == "" && len(after) == 0 && len(before) > 0 && strings.TrimSpace(before[len(before)-1]) == "" {
		before = before[:len(before)-1]
	}
	return f.join(slices.Concat(before, back, after))
}
