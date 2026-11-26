package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRules_QuotaProbeIsSideCall(t *testing.T) {
	if !(Profile{}).IsSideCall([]string{"quota"}) {
		t.Fatal("quota probe not a side call")
	}
}

func TestRules_QuotaWithOtherMessagesIsNotSideCall(t *testing.T) {
	if (Profile{}).IsSideCall([]string{"quota", "hello"}) {
		t.Fatal("quota with other messages classed as side call")
	}
}

func TestRules_TitleCallIsSideCall(t *testing.T) {
	if !(Profile{}).IsSideCall([]string{"<session>\nfoo\n</session>\nWrite the title for it"}) {
		t.Fatal("title call not a side call")
	}
}

func TestRules_PlainPromptNotSideCall(t *testing.T) {
	for _, in := range [][]string{{"fix the bug"}, {"<session> hi"}, nil} {
		if (Profile{}).IsSideCall(in) {
			t.Fatalf("%q classed as side call", in)
		}
	}
}

func TestRules_StripsReminders(t *testing.T) {
	got := (Profile{}).StripReminders("<system-reminder>a</system-reminder>hello <system-reminder>b</system-reminder>world")
	if got != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestRules_TitleOfResponse(t *testing.T) {
	got, ok := (Profile{}).TitleOf(`{"title":"Fix login"}`)
	if !ok || got != "Fix login" {
		t.Fatalf("got %q %v", got, ok)
	}
}

func TestRules_UnparseableTitleIgnored(t *testing.T) {
	for _, in := range []string{"", "not json", `{"title":""}`, `{"error":"x"}`} {
		if got, ok := (Profile{}).TitleOf(in); ok || got != "" {
			t.Fatalf("%q -> %q %v", in, got, ok)
		}
	}
}

func TestRules_AgentToolName(t *testing.T) {
	if !(Profile{}).IsAgentTool("Agent") || (Profile{}).IsAgentTool("Bash") {
		t.Fatal("agent tool name wrong")
	}
}

func TestRules_SplitReminders(t *testing.T) {
	const a, b = "<system-reminder>a</system-reminder>", "<system-reminder>b</system-reminder>"
	body, rs := (Profile{}).SplitReminders(a + "hello " + b + "world")
	if body != "hello world" || len(rs) != 2 || rs[0] != a || rs[1] != b {
		t.Fatalf("got %q %q", body, rs)
	}
	body, rs = (Profile{}).SplitReminders("plain")
	if body != "plain" || len(rs) != 0 {
		t.Fatalf("plain: %q %q", body, rs)
	}
	body, rs = (Profile{}).SplitReminders("hi <system-reminder>cut")
	if body != "hi" || len(rs) != 1 || rs[0] != "<system-reminder>cut" {
		t.Fatalf("unclosed: %q %q", body, rs)
	}
}

// fixtureTexts is the user texts of a side_calls fixture's request, as the timeline reads
// them: each user message's text blocks joined, a plain-string content as one text.
func fixtureTexts(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "side_calls", name))
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range req.Messages {
		if m.Role != "user" {
			continue
		}
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			out = append(out, s)
			continue
		}
		var blocks []struct{ Type, Text string }
		if err := json.Unmarshal(m.Content, &blocks); err != nil {
			t.Fatal(err)
		}
		text := ""
		for _, bl := range blocks {
			if bl.Type == "text" {
				if text != "" {
					text += "\n"
				}
				text += bl.Text
			}
		}
		out = append(out, text)
	}
	return out
}

// 018 AC1, AC2: an away summary and a prompt suggestion, as captured, are side calls with
// their own names.
func TestRules_AwaySummaryAndPromptSuggestionAreSideCalls(t *testing.T) {
	for name, want := range map[string]string{
		"02-away-summary.json":      "away summary",
		"04-prompt-suggestion.json": "prompt suggestion",
	} {
		texts := fixtureTexts(t, name)
		if !(Profile{}).IsSideCall(texts) {
			t.Errorf("%s: not a side call", name)
		}
		if got := (Profile{}).SideCallName(texts); got != want {
			t.Errorf("%s: name %q, want %q", name, got, want)
		}
	}
}

// 018 AC4: the turns around them stay turns, even the one whose prompt quotes both
// markers; so does a request whose last text is a prompt after a marker.
func TestRules_TurnsBesideSideCallsAreNotSideCalls(t *testing.T) {
	for _, name := range []string{"01-turn.json", "03-turn-quotes-markers.json", "05-turn-after-suggestion.json"} {
		if texts := fixtureTexts(t, name); (Profile{}).IsSideCall(texts) {
			t.Errorf("%s: classed as side call (%q)", name, (Profile{}).SideCallName(texts))
		}
	}
	away := fixtureTexts(t, "02-away-summary.json")
	if (Profile{}).IsSideCall(append(away, "Commit this.")) {
		t.Error("a prompt after an away summary's text classed as side call")
	}
}

func TestRules_SideCallNames(t *testing.T) {
	for _, c := range []struct {
		texts []string
		want  string
	}{
		{[]string{"quota"}, "quota probe"},
		{[]string{"<session>\nfoo\n</session>\nWrite the title for it"}, "title generation"},
		{[]string{"fix the bug"}, "side call"},
	} {
		if got := (Profile{}).SideCallName(c.texts); got != c.want {
			t.Errorf("%q: name %q, want %q", c.texts, got, c.want)
		}
	}
}

// 046 U1: a prompt's title with its wrappers taken off, and whether the client added the
// text by itself.
func TestRules_PromptTitle(t *testing.T) {
	const tag = `<pasted_content id="ab12">`
	const end = `</pasted_content id="ab12">`
	const mid = "The user sent a new message while you were working:\n"
	const midEnd = "\n\nThis is how Claude Code surfaces messages the user sends mid-turn, so reply to it."
	caveat := "<local-command-caveat>Caveat: ignore these.</local-command-caveat>\n"
	cases := []struct {
		name, in, want string
		synthetic      bool
	}{
		{"plain", "fix the login\n  button", "fix the login button", false},
		{"empty", "", "", false},
		{"blank", " \n ", "", false},
		{"pasted", tag + "fix the login button" + end, "fix the login button", false},
		{"pasted later in the text", "please read " + tag + "the log" + end + " now", "please read the log now", false},
		{"pasted unclosed", tag + "fix the login button", "fix the login button", false},
		{"pasted tag without a close", "<pasted_content id=\"ab\nfix the login button", "fix the login button", false},
		{"tag named mid-sentence", "why does <pasted_content> show in titles", "why does <pasted_content> show in titles", false},
		{"unclosed tag named mid-sentence", "explain <pasted_content id=x and the parser", "explain <pasted_content id=x and the parser", false},
		{"wrapper on its own lines", tag + "\nfix the login button\n" + end, "fix the login button", false},
		{"two wrappers with typed text between", "first " + tag + "\nthe log\n" + end + "\nthen compare with " + `<pasted_content id="cd34">` + "\nthe diff\n" + `</pasted_content id="cd34">` + "\nand fix it", "first the log then compare with the diff and fix it", false},
		{"unclosed tag at a line start", "check this\n  <pasted_content id=\"ab\nthe rest stays", "check this the rest stays", false},
		{"typed unclosed tag name at a line start", "<pasted_content should not show in the rail", "<pasted_content should not show in the rail", false},
		{"typed unclosed tag name on a later line", "note\n<pasted_content should stay", "note <pasted_content should stay", false},
		{"quoted unclosed tag at a line start keeps the lines after", "check this\n<pasted_content id=\"ab12\nthe rest stays", "check this the rest stays", false},
		{"a bare tag alone is not an empty title", "<pasted_content>", "<pasted_content>", false},
		{"closing tag at a line start without an opening", "the log\n" + end + "\nfix the login button", "the log fix the login button", false},
		{"mid-turn", mid + "also fix the footer" + midEnd, "also fix the footer", false},
		{"mid-turn without closing paragraph", mid + "also fix the footer", "also fix the footer", false},
		{"mid-turn with pasted inside", mid + tag + "also fix the footer" + end + midEnd, "also fix the footer", false},
		{"slash command with args", "<command-message>demo-skill</command-message>\n<command-name>/demo-skill</command-name>\n<command-args>the login page</command-args>", "/demo-skill the login page", false},
		{"slash command without args", "<command-message>demo-skill</command-message>\n<command-name>/demo-skill</command-name>\n<command-args></command-args>", "/demo-skill", false},
		{"slash command with skill body", "<command-message>demo-skill</command-message>\n<command-name>/demo-skill</command-name>\n<command-args>go</command-args>\nBase directory for this skill: /home/u/.claude/skills/demo-skill\n\n# Demo", "/demo-skill go", false},
		{"local command alone", caveat + "<command-name>/clear</command-name>\n<command-message>clear</command-message>\n<command-args></command-args>\n<local-command-stdout></local-command-stdout>", "/clear", true},
		{"local command with stderr", caveat + "<command-name>/demo</command-name>\n<local-command-stderr>boom</local-command-stderr>", "/demo", true},
		{"local command with typed text", caveat + "<command-name>/clear</command-name>\n<local-command-stdout></local-command-stdout>\n" + tag + "fix the login button" + end, "fix the login button", false},
		{"typed slash command with args and no output block", "<command-name>/demo-skill</command-name>\n<command-message>demo-skill</command-message>\n<command-args>do the thing</command-args>", "/demo-skill do the thing", false},
		{"local command without caveat", "<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args></command-args>\n<local-command-stdout>Set model to demo</local-command-stdout>", "/model", true},
		{"local command without caveat with typed text", "<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args></command-args>\n<local-command-stdout>Set model to demo</local-command-stdout>\n\nfix the login button", "fix the login button", false},
		{"local command without caveat with stderr", "<command-name>/model</command-name>\n<command-args></command-args>\n<local-command-stderr>boom</local-command-stderr>", "/model", true},
		{"typed args naming an output tag", "<command-message>review</command-message>\n<command-name>/review</command-name>\n<command-args>why does <local-command-stdout> leak into titles</command-args>", "/review why does <local-command-stdout> leak into titles", false},
		{"skill body naming an output tag", "<command-message>demo-skill</command-message>\n<command-name>/demo-skill</command-name>\n<command-args>do it</command-args>\nBase directory for this skill: /home/u/.claude/skills/demo-skill\n\nthe body shows <local-command-stdout>x</local-command-stdout> in a sample", "/demo-skill do it", false},
		{"local command then typed text naming a tag", caveat + "<command-name>/clear</command-name>\n<local-command-stdout></local-command-stdout>\nexplain the <command-name> tag", "explain the <command-name> tag", false},
		{"local command then tool loaded", caveat + "<command-name>/clear</command-name>\n<local-command-stdout></local-command-stdout>\nTool loaded.", "(system) tool loaded", true},
		{"local command then task notification", caveat + "<command-name>/clear</command-name>\n<local-command-stdout></local-command-stdout>\n<task-notification><summary>Background command \"npm test\" completed</summary></task-notification>", `Background command "npm test" completed`, true},
		{"slash command with no readable name", "<command-name></command-name>\n<command-args>x</command-args>", "(system) slash command", true},
		{"local command with no readable name", caveat + "<local-command-stdout>ok</local-command-stdout>", "(system) local command", true},
		{"task notification", "<task-notification>\n<task-id>x1</task-id>\n<summary>Background command \"npm test\" completed</summary>\n</task-notification>", `Background command "npm test" completed`, true},
		{"task notification without summary", "<task-notification><task-id>x1</task-id></task-notification>", "(system) background task", true},
		{"tool loaded", " Tool loaded.\n", "(system) tool loaded", true},
		{"tool loaded mid-sentence", "why does Tool loaded. appear twice", "why does Tool loaded. appear twice", false},
		{"tool loaded then text", "Tool loaded. now fix x", "Tool loaded. now fix x", false},
		{"another session", "Another Claude session sent a message:\n<agent-message>done</agent-message>", "(system) message from another session", true},
		{"skill body", "Base directory for this skill: /home/u/.claude/skills/demo-skill\n\n# Demo", "(system) skill demo-skill", true},
		{"skill body with trailing slash", "Base directory for this skill: /home/u/.claude/skills/demo-skill/", "(system) skill demo-skill", true},
		{"compaction request", "CRITICAL: Respond with TEXT ONLY. Do not call tools.", "(system) compaction request", true},
		{"continued", "This session is being continued from a previous conversation that ran out of context.", "(system) continued after compaction", true},
		{"web page", "Web page content:\nhello", "(system) web page content", true},
		{"claude md", "The following is the user's CLAUDE.md configuration.\nrules", "(system) CLAUDE.md context", true},
		{"usage reset", "Your claude.ai usage limit has reset. Continue.", "(system) usage limit reset", true},
		{"claude md then typed", "The following is the user's CLAUDE.md configuration.\n<user_claude_md>\nrules\n\nmore rules\n</user_claude_md>\nfix the login button", "fix the login button", false},
		{"claude md then pasted typed", "The following is the user's CLAUDE.md configuration.\n<user_claude_md>rules</user_claude_md>\n" + tag + "fix the login button" + end, "fix the login button", false},
		{"claude md with a closed tag only", "The following is the user's CLAUDE.md configuration.\n<user_claude_md>rules</user_claude_md>\n", "(system) CLAUDE.md context", true},
		{"usage reset then typed", "Your claude.ai usage limit has reset. Carry on, but do not repeat work that is already complete.\nfix the login button", "fix the login button", false},
		{"usage reset only", "Your claude.ai usage limit has reset. Carry on, but do not repeat work that is already complete.", "(system) usage limit reset", true},
		{"task notification then typed", "<task-notification><summary>Background command \"npm test\" completed</summary></task-notification>\nfix the login button", "fix the login button", false},
		{"two task notifications", "<task-notification><summary>first</summary></task-notification>\n<task-notification><summary>second</summary></task-notification>", "first", true},
		{"subagent message then typed", "Another Claude session sent a message:\n<agent-message>done</agent-message>\nfix the login button", "fix the login button", false},
		{"two synthetic blocks", "The following is the user's CLAUDE.md configuration.\n<user_claude_md>rules</user_claude_md>\nYour claude.ai usage limit has reset. Carry on.", "(system) CLAUDE.md context", true},
		{"tool loaded block then typed", "Tool loaded.\nfix the login button", "fix the login button", false},
		{"tool loaded block twice", "Tool loaded.\nTool loaded.", "(system) tool loaded", true},
		{"typed text with a line break after a typed block", "fix the login button\nTool loaded.", "fix the login button Tool loaded.", false},
		{"subagent message quoting its closing tag", "Another Claude session sent a message:\n<agent-message from=\"a1\">found the bug: the parser stops at </agent-message> too early in the stream</agent-message>", "(system) message from another session", true},
		{"subagent message quoting its closing tag then typed", "Another Claude session sent a message:\n<agent-message from=\"a1\">found the bug: the parser stops at </agent-message> too early in the stream</agent-message>\nfix the login button", "fix the login button", false},
		{"task notification quoting its closing tag", "<task-notification><summary>parser stops at </task-notification> too early</summary></task-notification>", "parser stops at </task-notification> too early", true},
		{"task notification quoting its closing tag then typed", "<task-notification><summary>parser stops at </task-notification> too early</summary></task-notification>\nfix the login button", "fix the login button", false},
		{"claude md quoting its closing tag", "The following is the user's CLAUDE.md configuration.\n<user_claude_md>\nclose with </user_claude_md> when done\nmore rules\n</user_claude_md>", "(system) CLAUDE.md context", true},
		{"claude md quoting its closing tag then typed", "The following is the user's CLAUDE.md configuration.\n<user_claude_md>\nclose with </user_claude_md> when done\nmore rules\n</user_claude_md>\nfix the login button", "fix the login button", false},
		{"usage reset sentence repeated in typed text", "Your claude.ai usage limit has reset. Carry on, but do not repeat work that is already complete.\nwhy does it say do not repeat work that is already complete.", "why does it say do not repeat work that is already complete.", false},
		{"phrase later in a real prompt", "please explain: Another Claude session sent a message: and Base directory for this skill: x", "please explain: Another Claude session sent a message: and Base directory for this skill: x", false},
	}
	for _, c := range cases {
		got, syn := (Profile{}).PromptTitle(c.in)
		if got != c.want || syn != c.synthetic {
			t.Errorf("%s: PromptTitle = %q, %v; want %q, %v", c.name, got, syn, c.want, c.synthetic)
		}
	}
}
