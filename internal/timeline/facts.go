package timeline

import (
	"encoding/json"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// FactRules is the client knowledge CallFacts needs. A nil FactRules means no side calls,
// prompts taken whole and no agent tools.
type FactRules interface {
	// IsSideCall reports whether a request's user message texts mark a side call.
	IsSideCall(userTexts []string) bool
	// StripReminders removes injected reminder blocks from a prompt.
	StripReminders(text string) string
	// IsAgentTool reports whether a tool name spawns a sub-agent.
	IsAgentTool(name string) bool
}

// CallFact is what the timeline knows about one model call beyond its own exchange: the
// thread it belongs to and the call it continues, whether the client's rules call it a
// side call, whether it is a main-thread call, and how its request's messages split into
// those carried from PrevID and new ones. A folded call (events not parsed) has thread -1
// and is never main. Prompt is the call's first user prompt with reminders removed; "" when
// it has none. UserTexts are its user message texts, as the side-call rule read them.
type CallFact struct {
	Thread    int
	PrevID    string
	SideCall  bool
	Main      bool
	Sub       bool // a sub-agent's call
	Carried   int
	New       int
	Prompt    string
	UserTexts []string
}

// CallFacts classifies every model call of a session, keyed by request id. A thread is
// main when it holds a user prompt and is not a sub-agent: its tool calls carry an agent
// id or type, or its first prompt is the prompt of an agent tool call made in another
// thread. evs holds the events already loaded for each request, so nothing is read twice.
func CallFacts(src Source, built []Entry, rules FactRules, evs map[string][]store.EventRow) (map[string]*CallFact, error) {
	type call struct {
		req    string
		thread int
	}
	var calls []call
	reqThread := map[string]int{}
	sub := map[int]bool{}
	hasPrompt := map[int]bool{}
	firstPrompt := map[int]string{}
	agentPrompts := map[string]int{} // prompt of an agent tool call -> thread that made it
	facts := map[string]*CallFact{}

	for _, e := range built {
		switch e.Kind {
		case KindFolded:
			if e.Call != nil {
				facts[e.RequestID] = &CallFact{Thread: -1}
				calls = append(calls, call{req: e.RequestID, thread: -1})
			}
		case KindExchange:
			if e.Call == nil {
				continue
			}
			texts, err := UserTextsOf(src, evs[e.RequestID])
			if err != nil {
				return nil, err
			}
			f := &CallFact{Thread: e.Thread, PrevID: e.PrevID, Carried: e.Carried,
				New: NewMessages(e.RequestMessages, e.Carried), UserTexts: texts}
			if rules != nil {
				f.SideCall = rules.IsSideCall(texts)
			}
			for _, t := range texts {
				if rules != nil {
					t = rules.StripReminders(t)
				}
				if t = strings.TrimSpace(t); t != "" {
					f.Prompt = t
					break
				}
			}
			if f.Prompt != "" {
				hasPrompt[e.Thread] = true
				if _, ok := firstPrompt[e.Thread]; !ok && !f.SideCall {
					firstPrompt[e.Thread] = f.Prompt
				}
			}
			reqThread[e.RequestID] = e.Thread
			facts[e.RequestID] = f
			calls = append(calls, call{req: e.RequestID, thread: e.Thread})
		case KindToolCall:
			th, ok := reqThread[e.RequestID]
			if !ok {
				continue
			}
			if e.AgentID != "" || e.AgentType != "" {
				sub[th] = true
			}
			if rules != nil && rules.IsAgentTool(e.Name) {
				if p := agentPrompt(e.Text); p != "" {
					agentPrompts[p] = th
				}
			}
		}
	}
	for th, p := range firstPrompt {
		if parent, ok := agentPrompts[p]; ok && parent != th {
			sub[th] = true
		}
	}
	for _, c := range calls {
		if c.thread < 0 {
			continue
		}
		f := facts[c.req]
		f.Sub = sub[c.thread] && !f.SideCall
		f.Main = hasPrompt[c.thread] && !f.SideCall && !sub[c.thread]
	}
	return facts, nil
}

// agentPrompt is the "prompt" field of an agent tool call's input, trimmed; "" when none.
func agentPrompt(input string) string {
	var in struct {
		Prompt string `json:"prompt"`
	}
	if json.Unmarshal([]byte(input), &in) != nil {
		return ""
	}
	return strings.TrimSpace(in.Prompt)
}
