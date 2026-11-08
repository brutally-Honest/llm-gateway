package ui

import (
	"encoding/json"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// callFactsJSON is what the browser needs to place a model call: the call it continues,
// whether the client's rules call it a side call, and whether it is a main-thread call.
// Carried and New split the request's messages into those shared with PrevID and the
// rest. A call that was folded (its events were not parsed) has thread -1 and is never
// main.
//
// Nothing on the wire may depend on a call's position among other calls, or one late
// commit would change, and so resend, every later exchange. So the call's number is not
// sent: the browser numbers calls from the entry's start and exchange id. Thread is kept
// off the wire for the same reason, since a late call that starts a thread renumbers the
// threads after it.
type callFactsJSON struct {
	Thread   int    `json:"-"`
	PrevID   string `json:"prev_id"`
	SideCall bool   `json:"side_call"`
	Main     bool   `json:"main"`
	Carried  int    `json:"carried"`
	New      int    `json:"new"`
}

// callFacts classifies every model call of a session, keyed by request id. Side calls
// come from the client rules; a nil rules means none, and prompts are taken whole. A
// thread is main when it holds a user prompt and is not a sub-agent: its tool calls
// carry an agent id or type, or its first prompt is the prompt of an agent tool call
// made in another thread.
// evs holds the events already loaded for each request, so nothing is read twice.
func callFacts(src timeline.Source, built []timeline.Entry, rules ClientRules, evs map[string][]store.EventRow) (map[string]*callFactsJSON, error) {
	type call struct {
		req    string
		thread int
		side   bool
		prompt string // first user prompt, reminders removed
	}
	var calls []call
	reqThread := map[string]int{}
	sub := map[int]bool{}
	hasPrompt := map[int]bool{}
	firstPrompt := map[int]string{}
	agentPrompts := map[string]int{} // prompt of an agent tool call -> thread that made it
	facts := map[string]*callFactsJSON{}

	for _, e := range built {
		switch e.Kind {
		case timeline.KindFolded:
			if e.Call != nil {
				facts[e.RequestID] = &callFactsJSON{Thread: -1}
				calls = append(calls, call{req: e.RequestID, thread: -1})
			}
		case timeline.KindExchange:
			if e.Call == nil {
				continue
			}
			texts, err := timeline.UserTextsOf(src, evs[e.RequestID])
			if err != nil {
				return nil, err
			}
			c := call{req: e.RequestID, thread: e.Thread}
			if rules != nil {
				c.side = rules.IsSideCall(texts)
			}
			for _, t := range texts {
				if rules != nil {
					t = rules.StripReminders(t)
				}
				if t = strings.TrimSpace(t); t != "" {
					c.prompt = t
					break
				}
			}
			if c.prompt != "" {
				hasPrompt[e.Thread] = true
				if _, ok := firstPrompt[e.Thread]; !ok && !c.side {
					firstPrompt[e.Thread] = c.prompt
				}
			}
			reqThread[e.RequestID] = e.Thread
			facts[e.RequestID] = &callFactsJSON{Thread: e.Thread, PrevID: e.PrevID,
				SideCall: c.side, Carried: e.Carried, New: timeline.NewMessages(e.RequestMessages, e.Carried)}
			calls = append(calls, c)
		case timeline.KindToolCall:
			th, ok := reqThread[e.RequestID]
			if !ok {
				continue
			}
			if e.AgentID != "" || e.AgentType != "" {
				sub[th] = true
			}
			if rules != nil && rules.IsAgentTool(e.Name) {
				var in struct {
					Prompt string `json:"prompt"`
				}
				if json.Unmarshal([]byte(e.Text), &in) == nil && strings.TrimSpace(in.Prompt) != "" {
					agentPrompts[strings.TrimSpace(in.Prompt)] = th
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
		facts[c.req].Main = hasPrompt[c.thread] && !c.side && !sub[c.thread]
	}
	return facts, nil
}
