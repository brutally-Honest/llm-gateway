package claudecode

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// askTool is Claude Code's tool that puts multiple-choice questions to the user.
const askTool = "AskUserQuestion"

// askInput is the AskUserQuestion input: the questions with their options.
type askInput struct {
	Questions []struct {
		Question    string `json:"question"`
		Header      string `json:"header"`
		MultiSelect bool   `json:"multiSelect"`
		Options     []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

// askSummary is an AskUserQuestion line: "2 questions: Stepper, Chip panels".
func askSummary(input []byte) string {
	var in askInput
	if json.Unmarshal(input, &in) != nil || len(in.Questions) == 0 {
		return ""
	}
	var heads []string
	for _, q := range in.Questions {
		if h := strings.TrimSpace(q.Header); h != "" {
			heads = append(heads, h)
		}
	}
	n := strconv.Itoa(len(in.Questions)) + " question"
	if len(in.Questions) != 1 {
		n += "s"
	}
	if len(heads) == 0 {
		return n
	}
	return n + ": " + strings.Join(heads, ", ")
}

// The result's closing sentences, which follow the last answer.
var askTails = []string{". You can now continue", ". Read the answers carefully"}

// Questions reads an AskUserQuestion call: the questions and options from its input, and
// each answer and note from its result, which reads
//
//	The user answered: "<question>"="<answer>" [selected preview: …] [notes: …], "<question>"="…". You can now continue…
//
// A question whose answer is not found keeps an empty Answer; ok is false for any other
// tool or an input that is not the tool's shape.
func (Profile) Questions(name string, input []byte, result string) ([]core.Question, bool) {
	if name != askTool {
		return nil, false
	}
	var in askInput
	if json.Unmarshal(input, &in) != nil || len(in.Questions) == 0 {
		return nil, false
	}
	marks := make([]int, len(in.Questions))
	for i, q := range in.Questions {
		marks[i] = strings.Index(result, `"`+q.Question+`"=`)
	}
	out := make([]core.Question, 0, len(in.Questions))
	for i, q := range in.Questions {
		cq := core.Question{Header: q.Header, Text: q.Question, Multi: q.MultiSelect}
		for _, o := range q.Options {
			cq.Options = append(cq.Options, core.QuestionOption{Label: o.Label, Description: o.Description})
		}
		if marks[i] >= 0 {
			from := marks[i] + len(q.Question) + 3
			// the segment ends where the next question's answer starts, or at the closing
			// sentences after the last one
			to := len(result)
			for _, m := range marks {
				if m > marks[i] && m < to {
					to = m
				}
			}
			seg := result[from:to]
			// a picked or typed answer is quoted; "(no option selected)" is not, and leaves
			// Answer empty
			rest := seg
			if strings.HasPrefix(seg, `"`) {
				if j := strings.IndexByte(seg[1:], '"'); j >= 0 {
					cq.Answer, rest = seg[1:1+j], seg[2+j:]
				}
			}
			for _, t := range askTails {
				if k := strings.Index(rest, t); k >= 0 {
					rest = rest[:k]
				}
			}
			rest = strings.TrimSuffix(strings.TrimSpace(rest), ",")
			if k := strings.Index(rest, "notes: "); k >= 0 {
				cq.Notes = strings.TrimSpace(rest[k+len("notes: "):])
			}
		}
		out = append(out, cq)
	}
	return out, true
}

var _ core.QuestionReader = Profile{}
