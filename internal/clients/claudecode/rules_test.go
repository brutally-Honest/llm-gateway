package claudecode

import "testing"

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

func TestRules_StripReminders(t *testing.T) {
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
