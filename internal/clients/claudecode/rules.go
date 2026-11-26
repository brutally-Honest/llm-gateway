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
	midTurnOpening  = "The user sent a new message while you were working:"
	midTurnClosing  = "This is how Claude Code surfaces messages the user sends mid-turn"
	toolLoadedText  = "Tool loaded."
	otherSession    = "Another Claude session sent a message:"
	skillOpening    = "Base directory for this skill:"
	compactOpening  = "CRITICAL: Respond with TEXT ONLY"
	continuedOpen   = "This session is being continued from a previous conversation"
	webPageOpening  = "Web page content:"
	claudeMDOpening = "The following is the user's CLAUDE.md configuration."
	usageResetOpen  = "Your claude.ai usage limit has reset."
	usageResetEnd   = "do not repeat work that is already complete."
	claudeMDEnd     = "</user_claude_md>"
	otherSessionEnd = "</agent-message>"
	taskNoteEnd     = "</task-notification>"
	maxTitleDepth   = 8
)

// pastedTag matches a pasted-content opening or closing tag, with attributes. A tag with
// no ">" is removed to the end of its line.
var pastedTag = regexp.MustCompile(`(?m)</?pasted_content\b[^>\n]*(?:>|$)`)

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
	case strings.HasPrefix(s, "<command-message>") || strings.HasPrefix(s, "<command-name>"):
		return slashCommandTitle(s), false
	case strings.HasPrefix(s, "<local-command-caveat>"):
		return localCommandTitle(s)
	case strings.HasPrefix(s, "<task-notification>"):
		title := "(system) background task"
		if sum := stripPasted(tagBody(s, "summary")); sum != "" {
			title = sum
		}
		return withTail(title, s, taskNoteEnd, depth)
	case s == toolLoadedText:
		return "(system) tool loaded", true
	case strings.HasPrefix(s, toolLoadedText+"\n"):
		return withTail("(system) tool loaded", s, toolLoadedText, depth)
	case strings.HasPrefix(s, otherSession):
		return withTail("(system) message from another session", s, otherSessionEnd, depth)
	case strings.HasPrefix(s, skillOpening):
		return skillTitle(strings.TrimPrefix(s, skillOpening)), true
	case strings.HasPrefix(s, compactOpening):
		return "(system) compaction request", true
	case strings.HasPrefix(s, continuedOpen):
		return "(system) continued after compaction", true
	case strings.HasPrefix(s, webPageOpening):
		return "(system) web page content", true
	case strings.HasPrefix(s, claudeMDOpening):
		return withTail("(system) CLAUDE.md context", s, claudeMDEnd, depth)
	case strings.HasPrefix(s, usageResetOpen):
		return withTail("(system) usage limit reset", s, usageResetEnd, depth)
	}
	return stripPasted(s), false
}

// withTail finishes a synthetic opening whose text ends at a known marker. A message is
// the user's text blocks joined with a single newline (timeline fillMessage and textOf), so
// the text after the marker may be a later block; when it holds text, the message is
// synthetic only if that text is too (the first title stays then). An opening with no
// marker, or none after it, is synthetic as a whole. Openings with no known end (skill
// body, continuation summary, web page, compaction request) cannot be cut apart: a
// single newline does not tell a block's own line breaks from the join.
func withTail(title, s, end string, depth int) (string, bool) {
	i := strings.Index(s, end)
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

// removeTag removes every <tag>…</tag> block from s; an unclosed one goes to the end.
func removeTag(s, tag string) string {
	open, closing := "<"+tag+">", "</"+tag+">"
	for {
		i := strings.Index(s, open)
		if i < 0 {
			return s
		}
		rest := s[i+len(open):]
		j := strings.Index(rest, closing)
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + rest[j+len(closing):]
	}
}

// commandName is the "/name" a command block pair names; "" when none.
func commandName(s string) string {
	n := tagBody(s, "command-name")
	if n == "" {
		n = tagBody(s, "command-message")
	}
	n = strings.TrimSpace(n)
	if n == "" {
		return ""
	}
	if !strings.HasPrefix(n, "/") {
		n = "/" + n
	}
	return n
}

// slashCommandTitle is "/name args"; whatever follows the command blocks (a skill body)
// is dropped.
func slashCommandTitle(s string) string {
	name := commandName(s)
	if name == "" {
		return "(system) slash command"
	}
	if args := stripPasted(tagBody(s, "command-args")); args != "" {
		return name + " " + args
	}
	return name
}

// localCommandTitle drops the caveat, command and output blocks of a local command. What
// the user typed after them is the title; with nothing, the command's name, synthetic.
func localCommandTitle(s string) (string, bool) {
	name := commandName(s)
	for _, tag := range []string{"local-command-caveat", "command-name", "command-message", "command-args", "local-command-stdout", "local-command-stderr"} {
		s = removeTag(s, tag)
	}
	if typed := stripPasted(s); typed != "" {
		return typed, false
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
