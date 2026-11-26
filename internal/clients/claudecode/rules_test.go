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
		{"mid-turn", mid + "also fix the footer" + midEnd, "also fix the footer", false},
		{"mid-turn without closing paragraph", mid + "also fix the footer", "also fix the footer", false},
		{"mid-turn with pasted inside", mid + tag + "also fix the footer" + end + midEnd, "also fix the footer", false},
		{"slash command with args", "<command-message>demo-skill</command-message>\n<command-name>/demo-skill</command-name>\n<command-args>the login page</command-args>", "/demo-skill the login page", false},
		{"slash command without args", "<command-message>demo-skill</command-message>\n<command-name>/demo-skill</command-name>\n<command-args></command-args>", "/demo-skill", false},
		{"slash command with skill body", "<command-message>demo-skill</command-message>\n<command-name>/demo-skill</command-name>\n<command-args>go</command-args>\nBase directory for this skill: /home/u/.claude/skills/demo-skill\n\n# Demo", "/demo-skill go", false},
		{"local command alone", caveat + "<command-name>/clear</command-name>\n<command-message>clear</command-message>\n<command-args></command-args>\n<local-command-stdout></local-command-stdout>", "/clear", true},
		{"local command with stderr", caveat + "<command-name>/demo</command-name>\n<local-command-stderr>boom</local-command-stderr>", "/demo", true},
		{"local command with typed text", caveat + "<command-name>/clear</command-name>\n<local-command-stdout></local-command-stdout>\n" + tag + "fix the login button" + end, "fix the login button", false},
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
		{"phrase later in a real prompt", "please explain: Another Claude session sent a message: and Base directory for this skill: x", "please explain: Another Claude session sent a message: and Base directory for this skill: x", false},
	}
	for _, c := range cases {
		got, syn := (Profile{}).PromptTitle(c.in)
		if got != c.want || syn != c.synthetic {
			t.Errorf("%s: PromptTitle = %q, %v; want %q, %v", c.name, got, syn, c.want, c.synthetic)
		}
	}
}
