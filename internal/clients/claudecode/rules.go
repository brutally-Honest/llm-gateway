package claudecode

import (
	"encoding/json"
	"regexp"
	"strings"
)

const (
	reminderOpen  = "<system-reminder>"
	reminderClose = "</system-reminder>"
)

// Openings of the user texts Claude Code sends as the last message of a request it makes
// on its own: the recap shown when the user comes back, and the guess at their next
// prompt. Both are forks of the main thread, so everything before that text is the
// conversation as it stood (018 research D2, D3).
const (
	awayOpening    = "The user stepped away and is coming back."
	suggestOpening = "[SUGGESTION MODE:"
)

// sideCallKind names the side call a request's user texts mark, "" for none: the quota
// probe (exactly one user message, equal to "quota"), title generation (a user message
// starting "<session>" that contains "Write the title"), an away summary or a prompt
// suggestion (the last user text starts with its fixed opening).
func sideCallKind(userTexts []string) string {
	if len(userTexts) == 1 && userTexts[0] == "quota" {
		return "quota probe"
	}
	for _, t := range userTexts {
		if strings.HasPrefix(t, "<session>") && strings.Contains(t, "Write the title") {
			return "title generation"
		}
	}
	if n := len(userTexts); n > 0 {
		switch last := userTexts[n-1]; {
		case strings.HasPrefix(last, awayOpening):
			return "away summary"
		case strings.HasPrefix(last, suggestOpening):
			return "prompt suggestion"
		}
	}
	return ""
}

// IsSideCall reports whether a request is one Claude Code makes on its own behalf: a
// quota probe, title generation, an away summary or a prompt suggestion.
func (Profile) IsSideCall(userTexts []string) bool { return sideCallKind(userTexts) != "" }

// SideCallName names a side call in words: "quota probe", "title generation", "away
// summary", "prompt suggestion", else "side call".
func (Profile) SideCallName(userTexts []string) string {
	if k := sideCallKind(userTexts); k != "" {
		return k
	}
	return "side call"
}

// StripReminders removes every <system-reminder>…</system-reminder> block. An unclosed
// block is removed to the end of the text.
func (p Profile) StripReminders(text string) string {
	body, _ := p.SplitReminders(text)
	return body
}

// SplitReminders separates a prompt into the text the user wrote and its
// <system-reminder>…</system-reminder> blocks, each kept whole in order. An unclosed
// block runs to the end of the text and is the last reminder.
func (Profile) SplitReminders(text string) (string, []string) {
	var b strings.Builder
	var reminders []string
	for {
		i := strings.Index(text, reminderOpen)
		if i < 0 {
			b.WriteString(text)
			break
		}
		b.WriteString(text[:i])
		rest := text[i+len(reminderOpen):]
		j := strings.Index(rest, reminderClose)
		if j < 0 {
			reminders = append(reminders, text[i:])
			break
		}
		end := i + len(reminderOpen) + j + len(reminderClose)
		reminders = append(reminders, text[i:end])
		text = text[end:]
	}
	return strings.TrimSpace(b.String()), reminders
}

// TitleOf extracts the title from a title-generation reply of the form {"title":"…"}.
// ok is false when the reply is empty, unparseable or has no title.
func (Profile) TitleOf(responseText string) (string, bool) {
	var v struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(responseText)), &v); err != nil {
		return "", false
	}
	t := strings.TrimSpace(v.Title)
	return t, t != ""
}

// IsAgentTool reports whether a tool name spawns a sub-agent.
func (Profile) IsAgentTool(name string) bool { return name == "Agent" }

// The fixed openings and tags of the user texts Claude Code adds by itself (046 research
// D1). Each is matched only at the start of the text, after trimming.
const (
	midTurnOpening   = "The user sent a new message while you were working:"
	midTurnClosing   = "This is how Claude Code surfaces messages the user sends mid-turn"
	toolLoadedText   = "Tool loaded."
	otherSession     = "Another Claude session sent a message:"
	skillOpening     = "Base directory for this skill:"
	compactOpening   = "CRITICAL: Respond with TEXT ONLY"
	continuedOpen    = "This session is being continued from a previous conversation"
	webPageOpening   = "Web page content:"
	claudeMDOpening  = "The following is the user's CLAUDE.md configuration."
	usageResetOpen   = "Your claude.ai usage limit has reset."
	usageResetEnd    = "do not repeat work that is already complete."
	claudeMDEnd      = "</user_claude_md>"
	otherSessionEnd  = "</agent-message>"
	otherFooter      = `That "other Claude session" is an agent`
	otherFooterCurly = "That “other Claude session” is an agent"
	taskNoteEnd      = "</task-notification>"
	maxTitleDepth    = 8
)

// pastedTag matches a real pasted-content wrapper tag, opening or closing, and nothing a
// user merely typed about it. Two shapes count: a tag that starts a line (after optional
// spaces), which goes with the line break after it, and, when unclosed, only in the
// client's quoted attribute form (id="ab12, to the end of its line, so a typed line that
// merely begins with the tag name stays); and a complete tag carrying a quoted attribute (the client's id="…"), anywhere.
// A bare or unquoted tag in the middle of a sentence is left as typed. Each match stays
// inside one line, so several pasted blocks never merge.
var pastedTag = regexp.MustCompile(`(?m)^[ \t]*</?pasted_content\b[^>\n]*>\n?|^[ \t]*</?pasted_content[ \t]+[A-Za-z_-]+=["'][^>\n]*$\n?|</?pasted_content[ \t]+[A-Za-z_-]+=(?:"[^"\n]*"|'[^'\n]*')[^>\n]*>`)

// PromptTitle gives a turn's title from its prompt (reminders already removed), and
// whether the title is synthetic: text Claude Code added by itself, which a session title
// skips. Wrappers are unwrapped in turn; whitespace is collapsed; the length is the
// caller's to cut. A prompt that matches no pattern is returned as it is.
func (Profile) PromptTitle(prompt string) (string, bool) {
	title, synthetic := promptTitle(prompt, 0)
	return strings.Join(strings.Fields(title), " "), synthetic
}

func promptTitle(text string, depth int) (string, bool) {
	s := strings.TrimSpace(text)
	if s == "" {
		return "", false
	}
	if rest, ok := strings.CutPrefix(s, midTurnOpening); ok && depth < maxTitleDepth {
		if i := strings.Index(rest, midTurnClosing); i >= 0 {
			rest = rest[:i]
		}
		return promptTitle(rest, depth+1)
	}
	switch {
	case strings.HasPrefix(s, "<command-message>") || strings.HasPrefix(s, "<command-name>") || strings.HasPrefix(s, "<local-command-caveat>"):
		return commandTitle(s, depth)
	case strings.HasPrefix(s, "<task-notification>"):
		title := "(system) background task"
		if sum := stripPasted(tagBody(s, "summary")); sum != "" {
			title = sum
		}
		return withTail(title, s, taskNoteEnd, true, depth)
	case s == toolLoadedText:
		return "(system) tool loaded", true
	case strings.HasPrefix(s, toolLoadedText+"\n"):
		return withTail("(system) tool loaded", s, toolLoadedText, false, depth)
	case strings.HasPrefix(s, otherSession):
		if i := strings.LastIndex(s, otherSessionEnd); i >= 0 {
			rest := strings.TrimSpace(s[i+len(otherSessionEnd):])
			if strings.HasPrefix(rest, otherFooter) || strings.HasPrefix(rest, otherFooterCurly) {
				// The client's own explanatory paragraph, not typed text; its end is not fixed.
				return "(system) message from another session", true
			}
		}
		return withTail("(system) message from another session", s, otherSessionEnd, true, depth)
	case strings.HasPrefix(s, skillOpening):
		return skillTitle(strings.TrimPrefix(s, skillOpening)), true
	case strings.HasPrefix(s, compactOpening):
		return "(system) compaction request", true
	case strings.HasPrefix(s, continuedOpen):
		return "(system) continued after compaction", true
	case strings.HasPrefix(s, webPageOpening):
		return "(system) web page content", true
	case strings.HasPrefix(s, claudeMDOpening):
		return withTail("(system) CLAUDE.md context", s, claudeMDEnd, true, depth)
	case strings.HasPrefix(s, usageResetOpen):
		return withTail("(system) usage limit reset", s, usageResetEnd, false, depth)
	}
	// Safety net: stripping must never turn a typed prompt into an empty title, which the
	// overview would skip; fall back to the text as typed.
	if t := stripPasted(s); t != "" {
		return t, false
	}
	return s, false
}

// withTail finishes a synthetic opening whose text ends at a known marker. A message is
// the user's text blocks joined with a single newline (timeline fillMessage and textOf), so
// the text after the marker may be a later block; when it holds text, the message is
// synthetic only if that text is too (the first title stays then). An opening with no
// marker, or none after it, is synthetic as a whole. Openings with no known end (skill
// body, continuation summary, web page, compaction request) cannot be cut apart: a
// single newline does not tell a block's own line breaks from the join.
//
// A closing tag ends the block at its last occurrence, so a body that quotes its own
// closing tag does not leak the rest of the body as typed text (last is true). A fixed
// sentence or line ends it at the first, since typed text after it may repeat it.
func withTail(title, s, end string, last bool, depth int) (string, bool) {
	i := strings.Index(s, end)
	if last {
		i = strings.LastIndex(s, end)
	}
	if i < 0 || depth >= maxTitleDepth {
		return title, true
	}
	rest := strings.TrimSpace(s[i+len(end):])
	if rest == "" {
		return title, true
	}
	if t, syn := promptTitle(rest, depth+1); !syn && t != "" {
		return t, false
	}
	return title, true
}

// stripPasted removes pasted-content tags, keeping the text between them.
func stripPasted(s string) string { return strings.TrimSpace(pastedTag.ReplaceAllString(s, "")) }

// tagBody is the trimmed text of the first <tag>…</tag> in s; "" when absent. An unclosed
// tag runs to the end of s.
func tagBody(s, tag string) string {
	open, closing := "<"+tag+">", "</"+tag+">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	rest := s[i+len(open):]
	if j := strings.Index(rest, closing); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// commandBlockTags are the blocks Claude Code writes ahead of a slash command or a local
// command's output; only these may form the leading run.
var commandBlockTags = []string{"local-command-caveat", "command-name", "command-message", "command-args", "local-command-stdout", "local-command-stderr"}

// commandRun is the leading run of command blocks of a message: each block complete
// (opening tag to its own closing tag), separated only by whitespace.
type commandRun struct {
	bodies map[string]string // first body per tag
	rest   string            // everything after the run, as typed
	local  bool              // an output block or the caveat is in the run
}

func leadingCommandRun(s string) commandRun {
	run := commandRun{bodies: map[string]string{}}
	for {
		t := strings.TrimLeft(s, " \t\r\n")
		matched := false
		for _, tag := range commandBlockTags {
			rest, ok := strings.CutPrefix(t, "<"+tag+">")
			if !ok {
				continue
			}
			body, after, closed := strings.Cut(rest, "</"+tag+">")
			if !closed {
				break
			}
			if _, seen := run.bodies[tag]; !seen {
				run.bodies[tag] = strings.TrimSpace(body)
			}
			if tag != "command-name" && tag != "command-message" && tag != "command-args" {
				run.local = true
			}
			s, matched = after, true
			break
		}
		if !matched {
			run.rest = s
			return run
		}
	}
}

// name is the "/name" the run's command blocks name; "" when none.
func (r commandRun) name() string {
	n := r.bodies["command-name"]
	if n == "" {
		n = r.bodies["command-message"]
	}
	if n == "" {
		return ""
	}
	if !strings.HasPrefix(n, "/") {
		n = "/" + n
	}
	return n
}

// commandTitle titles a message that opens with command blocks. With an output block or the
// caveat in the leading run it is a local command: the rest, as typed, is titled again (a
// synthetic rest stays synthetic); with nothing left, the command's name, synthetic. Without
// one it is a typed slash command, "/name args" from the run, and whatever follows the run
// (a skill body) is dropped. Tag-like text in args or after the run is never stripped.
// A command with no readable name is synthetic, so a "(system)" title never leads a session.
func commandTitle(s string, depth int) (string, bool) {
	run := leadingCommandRun(s)
	name := run.name()
	if !run.local {
		if name == "" {
			return "(system) slash command", true
		}
		if args := stripPasted(run.bodies["command-args"]); args != "" {
			return name + " " + args, false
		}
		return name, false
	}
	if strings.TrimSpace(run.rest) != "" {
		if t, syn := promptTitle(run.rest, depth+1); t != "" {
			return t, syn
		}
	}
	if name == "" {
		return "(system) local command", true
	}
	return name, true
}

// skillTitle names a loaded skill by the last element of its directory.
func skillTitle(afterOpening string) string {
	path, _, _ := strings.Cut(strings.TrimSpace(afterOpening), "\n")
	path = strings.TrimRight(strings.TrimSpace(path), `/\`)
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		path = path[i+1:]
	}
	if path == "" {
		return "(system) skill"
	}
	return "(system) skill " + path
}
