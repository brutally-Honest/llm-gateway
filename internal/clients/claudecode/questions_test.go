package claudecode

import (
	"fmt"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

const askInputJSON = `{"questions":[
 {"question":"Which layout?","header":"Layout","multiSelect":false,"options":[{"label":"Cards","description":"one card each"},{"label":"Table","description":"one row each"}]},
 {"question":"Where do loose effects go?","header":"Loose","multiSelect":false,"options":[{"label":"A","description":"in the steps"},{"label":"B","description":"in the panels"}]},
 {"question":"Which chips open?","header":"Chips","multiSelect":true,"options":[{"label":"Files","description":""},{"label":"Sites","description":""}]}]}`

// The result as Claude Code writes it: an answer with notes, one with a selected preview,
// a question with no option picked, and the closing sentences.
const askResult = `The user answered: "Which layout?"=(no option selected) notes: like the current stepper, "Where do loose effects go?"="B" selected preview:
 rows, in order notes: keep it short, "Which chips open?"="Files, Sites". You can now continue with these answers in mind. Read the answers carefully — they may request clarification.`

func TestQuestions_ReadsQuestionsAnswersAndNotes(t *testing.T) {
	qs, ok := Profile{}.Questions(askTool, []byte(askInputJSON), askResult)
	if !ok || len(qs) != 3 {
		t.Fatalf("questions = %+v, %v", qs, ok)
	}
	got := func(q core.Question) string {
		return fmt.Sprintf("%s|%s|multi=%v|%d options|answer=%q|notes=%q", q.Header, q.Text, q.Multi, len(q.Options), q.Answer, q.Notes)
	}
	want := []string{
		`Layout|Which layout?|multi=false|2 options|answer=""|notes="like the current stepper"`,
		`Loose|Where do loose effects go?|multi=false|2 options|answer="B"|notes="keep it short"`,
		`Chips|Which chips open?|multi=true|2 options|answer="Files, Sites"|notes=""`,
	}
	for i := range want {
		if g := got(qs[i]); g != want[i] {
			t.Errorf("question %d = %s\n want %s", i, g, want[i])
		}
	}
	if qs[1].Options[1].Description != "in the panels" {
		t.Errorf("option = %+v", qs[1].Options[1])
	}
}

func TestQuestions_OtherToolsAndBadInput(t *testing.T) {
	if _, ok := (Profile{}).Questions("Bash", []byte(askInputJSON), askResult); ok {
		t.Error("Bash read as questions")
	}
	if _, ok := (Profile{}).Questions(askTool, []byte(`not json`), askResult); ok {
		t.Error("bad input read as questions")
	}
	qs, ok := Profile{}.Questions(askTool, []byte(askInputJSON), "")
	if !ok || qs[0].Answer != "" || qs[0].Notes != "" {
		t.Errorf("no result: %+v %v", qs, ok)
	}
}

func TestSummarizeTool_AskUserQuestion(t *testing.T) {
	s := Profile{}.SummarizeTool(askTool, []byte(askInputJSON), "/w")
	if s.Kind != core.ToolAsk || s.Arg != "3 questions: Layout, Loose, Chips" {
		t.Errorf("summary = %+v", s)
	}
}
