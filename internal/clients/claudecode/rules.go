package claudecode

import (
	"encoding/json"
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
