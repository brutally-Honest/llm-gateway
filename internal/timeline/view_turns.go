package timeline

import (
	"sort"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// renderTurns turns the built turns into the view's turns and between blocks.
func (b *builder) renderTurns(v *View) {
	v.Turns = []Turn{}
	for _, t := range b.turns {
		b.renderTurn(t)
		v.Turns = append(v.Turns, t.t)
	}
	afters := make([]int, 0, len(b.between))
	for a := range b.between {
		afters = append(afters, a)
	}
	sort.Ints(afters)
	v.Between = []Between{}
	for _, a := range afters {
		bt := Between{After: a, Title: "Seen " + betweenWhere(a), Text: "A change while no tool call was running: most likely your own edit."}
		for _, e := range b.between[a] {
			bt.Effects = append(bt.Effects, e.line)
		}
		bt.At = bt.Effects[0].At
		v.Between = append(v.Between, bt)
	}
}

func (b *builder) renderTurn(vt *vturn) {
	t := &vt.t
	t.atNS = vt.at
	t.At = rfc(vt.at)
	t.Reminders = []string{}
	text := vt.hookText
	if vt.prompt != nil {
		text = vt.prompt.Text
		t.PromptTruncated = vt.prompt.Omitted > 0
	}
	if b.in.Rules != nil {
		body, rem := b.in.Rules.SplitReminders(text)
		t.Prompt = body
		if rem != nil {
			t.Reminders = rem
		}
	} else {
		t.Prompt = strings.TrimSpace(text)
	}
	if t.N == 0 {
		t.Prompt = "Before the first prompt"
	}

	t.Steps = []Step{}
	var mains []*vcall
	for _, c := range vt.calls {
		if c.subOf == nil {
			mains = append(mains, c)
			t.Steps = append(t.Steps, b.step(c, false))
		}
	}
	t.HookTools = []ToolLine{}
	for _, tool := range vt.hookOnly {
		t.HookTools = append(t.HookTools, b.toolLine(tool))
	}
	t.Loose = []EffectLine{}
	for _, e := range vt.loose {
		t.Loose = append(t.Loose, e.line)
	}
	t.Models = turnModels(vt.calls)
	b.turnTimes(vt, mains)
	t.Counts = countTurn(t)
}

// turnModels is the models a turn's calls used, sub-agents included, in the order they
// were first used; side calls and calls whose model is unknown are left out.
func turnModels(calls []*vcall) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range calls {
		if c.usage == nil || c.usage.Model == UnknownModel || c.usage.Model == "" || (c.fact != nil && c.fact.SideCall) {
			continue
		}
		if !seen[c.usage.Model] {
			seen[c.usage.Model] = true
			out = append(out, c.usage.Model)
		}
	}
	return out
}

// step is one model call's line, with the tool calls it asked for.
func (b *builder) step(c *vcall, lastOfSub bool) Step {
	s := Step{N: c.n, ExchangeID: c.id, At: rfc(c.start), DurMS: ms(c.end - c.start), Errored: c.errored,
		Thinking: c.thinking, Tools: []ToolLine{}, Several: []EffectLine{}, SeveralNames: []string{}}
	if c.errored {
		s.Status = statusWord(c.status, c.errType)
	}
	if c.subOf != nil {
		s.Agent = c.subOf.agentLabel()
	}
	switch {
	case c.errored:
		s.Did = "failed"
	case c.thinking:
		s.Did = "thought and wrote"
	case len(c.tools) > 0:
		s.Did = "wrote"
	case lastOfSub:
		s.Did = "reported back to the main agent"
	default:
		s.Did = "wrote the answer"
	}
	for _, t := range c.tools {
		s.Tools = append(s.Tools, b.toolLine(t))
	}
	seen := map[string]bool{}
	for _, e := range c.several {
		s.Several = append(s.Several, e.line)
		for _, n := range e.line.Candidates {
			if !seen[n] {
				seen[n] = true
				s.SeveralNames = append(s.SeveralNames, n)
			}
		}
	}
	return s
}

// agentLabel names the sub-agent an agent tool call spawned: the agent type its calls
// carry, else its own summary.
func (t *vtool) agentLabel() string {
	for _, c := range t.sub {
		for _, x := range c.tools {
			if x.agentType != "" {
				return x.agentType
			}
			if x.agentID != "" {
				return x.agentID
			}
		}
	}
	if a, _, ok := strings.Cut(t.sum.Arg, " · "); ok && a != "" {
		return a
	}
	return "sub-agent"
}

// toolLine is one tool call's line (AC5), with its effects and, for an agent tool call,
// its sub-agent's steps.
func (b *builder) toolLine(t *vtool) ToolLine {
	l := ToolLine{ID: t.id, Name: t.name, Chip: t.chip, State: chipMark(t.chip), Kind: string(t.sum.Kind),
		Summary: b.summaryText(t.sum), FullPath: t.sum.Path, Added: t.sum.Added, Removed: t.sum.Removed,
		Short: b.shortName(t), Effects: []EffectLine{}}
	if t.turn != nil {
		l.Turn = t.turn.t.N
	}
	if t.call != nil {
		l.CallN = t.call.n
	}
	if t.agentID != "" || t.agentType != "" {
		l.Agent = t.agentType
		if l.Agent == "" {
			l.Agent = t.agentID
		}
	}
	if from, to, approx, ok := t.span(); ok {
		d := ms(to - from)
		l.DurMS, l.Approx = &d, approx
	}
	known, added, removed := false, 0, 0
	for _, e := range t.effects {
		l.Effects = append(l.Effects, e.line)
		if e.line.Kind == "change" && e.line.Added >= 0 {
			known, added, removed = true, added+e.line.Added, removed+e.line.Removed
		}
	}
	if known {
		l.Added, l.Removed = added, removed
	}
	if t.chip == ChipFailed || t.resultErr {
		l.Added, l.Removed = -1, -1
		l.FirstError = firstLine(t.resultText)
	}
	if t.sum.Kind == core.ToolOther {
		l.Summary = ""
	}
	if len(t.sub) > 0 {
		sa := &SubAgent{Label: t.agentLabel(), Calls: len(t.sub), Steps: []Step{}}
		for i, c := range t.sub {
			sa.Steps = append(sa.Steps, b.step(c, i == len(t.sub)-1))
		}
		l.Sub = sa
	}
	return l
}

// firstLine is the first non-empty line of a tool's error output, with a wrapping tag
// such as <error>…</error> removed.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "<") {
			if i := strings.Index(l, ">"); i > 0 && !strings.Contains(l[:i], " ") {
				l = strings.TrimSpace(l[i+1:])
			}
		}
		if i := strings.LastIndex(l, "</"); i >= 0 && strings.HasSuffix(l, ">") {
			l = strings.TrimSpace(l[:i])
		}
		if l != "" {
			if len(l) > 300 {
				l = l[:300] + "…"
			}
			return l
		}
	}
	return ""
}

// turnTimes sets a turn's clock time and its model, tool and waiting parts (D5, Q1): the
// clock runs from the prompt to the final answer; with no final answer it ends at the last
// call or tool end and the turn says how it ended.
func (b *builder) turnTimes(vt *vturn, mains []*vcall) {
	t := &vt.t
	if t.HooksOnly || t.N == 0 {
		return
	}
	var modelSpans, toolSpans [][2]int64
	end := vt.at
	for _, c := range vt.calls {
		modelSpans = append(modelSpans, [2]int64{c.start, c.end})
		end = max(end, c.end)
		for _, x := range c.tools {
			if x.isAgent() {
				continue
			}
			if from, to, _, ok := x.span(); ok {
				toolSpans = append(toolSpans, [2]int64{from, to})
				end = max(end, to)
			} else if x.start > 0 {
				end = max(end, x.start)
			}
		}
	}
	for _, x := range vt.hookOnly {
		if from, to, _, ok := x.span(); ok && !x.isAgent() {
			toolSpans = append(toolSpans, [2]int64{from, to})
			end = max(end, to)
		}
	}
	var last *vcall
	for _, c := range mains {
		if c.fact.Main {
			last = c
		}
	}
	if last == nil && len(mains) > 0 {
		last = mains[len(mains)-1]
	}
	isLast := len(b.turns) > 0 && b.turns[len(b.turns)-1] == vt
	switch {
	case last != nil && !last.errored && len(last.tools) == 0:
		end = last.end
		t.Answer = strings.Join(last.texts, "\n\n")
		t.AnswerCall = last.n
	case isLast && b.in.Active:
		t.End = &Mark{"◷", "running"}
	case last != nil && last.errored:
		t.End = &Mark{"✗", "ended by error"}
	default:
		t.End = &Mark{"■", "interrupted"}
	}
	t.ClockMS = ms(max(0, end-vt.at))
	t.ModelMS = ms(union(modelSpans, vt.at, end))
	t.ToolMS = ms(union(toolSpans, vt.at, end))
	busy := ms(union(append(append([][2]int64{}, modelSpans...), toolSpans...), vt.at, end))
	t.WaitMS = max(0, t.ClockMS-busy)
}

// countTurn is a turn's pills: tool calls, distinct files changed and read, searches,
// distinct sites, failed tool calls.
func countTurn(t *Turn) TurnCounts {
	var c TurnCounts
	changed, read, sites := map[string]bool{}, map[string]bool{}, map[string]bool{}
	addEffect := func(e EffectLine) {
		switch e.Kind {
		case "change":
			changed[e.FullPath] = true
		case "read":
			read[e.FullPath] = true
		case "net":
			sites[e.Host] = true
		}
	}
	var walkTool func(l ToolLine)
	var walkStep func(s Step)
	walkTool = func(l ToolLine) {
		c.Tools++
		if l.Chip == ChipFailed {
			c.Failed++
		}
		if l.Kind == string(core.ToolSearch) {
			c.Searched++
		}
		for _, e := range l.Effects {
			addEffect(e)
		}
		if l.Sub != nil {
			for _, s := range l.Sub.Steps {
				walkStep(s)
			}
		}
	}
	walkStep = func(s Step) {
		for _, l := range s.Tools {
			walkTool(l)
		}
		for _, e := range s.Several {
			addEffect(e)
		}
	}
	for _, s := range t.Steps {
		walkStep(s)
	}
	for _, l := range t.HookTools {
		walkTool(l)
	}
	for _, e := range t.Loose {
		addEffect(e)
	}
	c.Changed, c.Read, c.Sites = len(changed), len(read), len(sites)
	return c
}
