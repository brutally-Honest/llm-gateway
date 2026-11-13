package claudecode

import (
	"encoding/json"
	"strings"
)

const (
	reminderOpen  = "<system-reminder>"
	reminderClose = "</system-reminder>"
)

// IsSideCall reports whether a request is one Claude Code makes on its own behalf: the
// quota probe (exactly one user message, equal to "quota") or title generation (a user
// message starting "<session>" that contains "Write the title").
func (Profile) IsSideCall(userTexts []string) bool {
	if len(userTexts) == 1 && userTexts[0] == "quota" {
		return true
	}
	for _, t := range userTexts {
		if strings.HasPrefix(t, "<session>") && strings.Contains(t, "Write the title") {
			return true
		}
	}
	return false
}

// SideCallName names a side call in words: "quota probe", "title generation", else "side
// call".
func (p Profile) SideCallName(userTexts []string) string {
	if len(userTexts) == 1 && userTexts[0] == "quota" {
		return "quota probe"
	}
	for _, t := range userTexts {
		if strings.HasPrefix(t, "<session>") && strings.Contains(t, "Write the title") {
			return "title generation"
		}
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
