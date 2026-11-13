package timeline

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// vcall is one model call while the view is built.
type vcall struct {
	n          int
	id         string
	start, end int64
	status     int
	usage      *CallUsage
	fact       *CallFact
	errored    bool
	errType    string
	prompt     *Entry // the call's own user prompt, when it has one
	texts      []string
	thinking   bool
	tools      []*vtool
	turn       *vturn
	subOf      *vtool     // the agent tool call whose sub-agent made this call
	several    []*vEffect // several-running effects whose latest candidate this call asked for
}

// vtool is one tool call while the view is built: asked by a captured call, or seen by
// hooks alone.
type vtool struct {
	id, name, input string
	call            *vcall
	chip            string
	agentID         string
	agentType       string
	hookOnly        bool
	hasHooks        bool
	start, end      int64 // hook span; end 0 while it never ended
	firstHook       int64
	workDir         string
	resultErr       bool
	resultText      string
	sum             core.ToolSummary
	turn            *vturn
	sub             []*vcall   // the sub-agent's calls, for an agent tool call
	effects         []*vEffect // effects labelled for this call
}

// vturn is one turn while the view is built.
type vturn struct {
	t        Turn
	at       int64
	calls    []*vcall
	hookOnly []*vtool
	prompt   *Entry
	hookText string
	loose    []*vEffect // harness, outside and unlabelled effects of the turn
}

// promptHook is one prompt hook event.
type promptHook struct {
	at   int64
	text string
	used bool
}

// builder holds one BuildView run.
type builder struct {
	in        ViewInput
	folder    string
	calls     []*vcall
	byID      map[string]*vcall
	tools     map[string]*vtool
	toolOrder []*vtool
	turns     []*vturn // numbered turns in order; turn 0, when present, is first
	side      []*vcall
	results   map[string]*Entry
	effects   []*vEffect
	between   map[int][]*vEffect // after turn N -> the edits seen between N and N+1
}

// BuildView builds one session's view from one timeline build, its hook rows and its
// labelled effects. It reads nothing but in.Content and in.Lines.
func BuildView(in ViewInput) View {
	b := &builder{in: in, byID: map[string]*vcall{}, tools: map[string]*vtool{}, results: map[string]*Entry{}, between: map[int][]*vEffect{}}
	b.folder = sessionFolder(in.Hooks)
	b.readEntries()
	b.readHooks()
	b.summarize()
	b.buildTurns()
	b.nestSubAgents()
	b.toolTimes()
	b.placeEffects()
	v := View{Version: in.Version}
	b.renderTurns(&v)
	b.lists(&v)
	b.overview(&v)
	hashTurns(&v)
	return v
}

// sessionFolder is the working folder of the session's first hook that names one.
func sessionFolder(hooks []store.HookRow) string {
	for _, h := range sortHooks(hooks) {
		if h.WorkDir != "" {
			return h.WorkDir
		}
	}
	return ""
}

func (b *builder) content(hash string) []byte {
	if b.in.Content == nil || hash == "" {
		return nil
	}
	return b.in.Content(hash)
}

// readEntries collects the model calls, their prompts, texts, errors and tool calls, and
// the tool results, from the timeline entries.
func (b *builder) readEntries() {
	var cur *vcall
	for i := range b.in.Entries {
		e := &b.in.Entries[i]
		switch e.Kind {
		case KindExchange, KindFolded:
			cur = nil
			if e.Call == nil {
				continue
			}
			x := b.in.Exchanges[e.RequestID]
			end := x.EndedAt
			if end < x.StartedAt {
				end = x.StartedAt
			}
			cur = &vcall{n: len(b.calls) + 1, id: e.RequestID, start: e.Start, end: end, status: e.Status, usage: e.Call,
				fact: b.in.Facts[e.RequestID]}
			if x.StartedAt != 0 {
				cur.start = x.StartedAt
			}
			if cur.fact == nil {
				cur.fact = &CallFact{Thread: -1}
			}
			if e.Status >= 400 {
				cur.errored = true
			}
			b.calls = append(b.calls, cur)
			b.byID[cur.id] = cur
		case KindMessage:
			if cur == nil {
				continue
			}
			if e.Thinking != "" {
				cur.thinking = true
			}
			switch e.Role {
			case "user":
				if cur.prompt == nil && (strings.TrimSpace(e.Text) != "" || e.Omitted > 0) {
					cur.prompt = e
				}
			case "assistant":
				if t := strings.TrimSpace(stripPlaceholders(e.Text)); t != "" {
					cur.texts = append(cur.texts, t)
				}
			}
		case KindToolCall:
			if cur == nil || e.ToolCallID == "" {
				continue
			}
			if _, seen := b.tools[e.ToolCallID]; seen {
				continue
			}
			t := &vtool{id: e.ToolCallID, name: e.Name, input: e.Text, call: cur, chip: e.Chip,
				agentID: e.AgentID, agentType: e.AgentType}
			if t.chip == "" {
				t.chip = ChipNoHooks
			}
			cur.tools = append(cur.tools, t)
			b.tools[t.id] = t
			b.toolOrder = append(b.toolOrder, t)
		case KindToolResult:
			if _, ok := b.results[e.ToolCallID]; !ok && e.ToolCallID != "" {
				b.results[e.ToolCallID] = e
			}
		case KindError:
			if cur != nil {
				cur.errored = true
				if cur.errType == "" {
					cur.errType = e.Name
				}
			}
		case KindHarness:
			if e.ToolCallID == "" {
				continue
			}
			t, ok := b.tools[e.ToolCallID]
			if !ok {
				t = &vtool{id: e.ToolCallID, name: e.Name, hookOnly: true, chip: e.Chip}
				b.tools[t.id] = t
				b.toolOrder = append(b.toolOrder, t)
			}
			if t.agentID == "" {
				t.agentID, t.agentType = e.AgentID, e.AgentType
			}
		}
	}
	for id, r := range b.results {
		if t, ok := b.tools[id]; ok {
			t.resultErr, t.resultText = r.IsError, r.Text
		}
	}
}

// stripPlaceholders drops the "[reasoning]" and other block placeholders the timeline
// puts in a message's text: thinking is never shown inline (AC7).
func stripPlaceholders(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		switch l {
		case "[reasoning]", "[media]", "[unknown]":
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// readHooks gives each tool call its hook span, folder and input, from the session's hook
// rows in the store's order.
func (b *builder) readHooks() {
	for _, h := range sortHooks(b.in.Hooks) {
		if h.ToolUseID == "" {
			continue
		}
		t, ok := b.tools[h.ToolUseID]
		if !ok {
			continue
		}
		if !t.hasHooks {
			t.hasHooks, t.firstHook = true, h.ReceivedAt
		}
		if t.workDir == "" {
			t.workDir = h.WorkDir
		}
		if t.agentID == "" && h.AgentID != "" {
			t.agentID, t.agentType = h.AgentID, h.AgentType
		}
		if t.name == "" {
			t.name = h.ToolName
		}
		switch core.HarnessPhase(h.Phase) {
		case core.PhasePreTool:
			if t.start == 0 || h.ReceivedAt < t.start {
				t.start = h.ReceivedAt
			}
			if t.input == "" && h.InputHash != "" {
				t.input = string(b.content(h.InputHash))
			}
		case core.PhasePostTool, core.PhasePostToolFailed, core.PhasePermissionDenied:
			if t.end == 0 || h.ReceivedAt < t.end {
				t.end = h.ReceivedAt
			}
		}
	}
}

// summarize asks the profile for each tool call's line.
func (b *builder) summarize() {
	for _, t := range b.toolOrder {
		t.sum = core.ToolSummary{Kind: core.ToolOther, Added: -1, Removed: -1}
		if b.in.Summarizer != nil {
			dir := t.workDir
			if dir == "" {
				dir = b.folder
			}
			t.sum = b.in.Summarizer.SummarizeTool(t.name, []byte(t.input), dir)
		}
		if t.sum.Kind == core.ToolOther && b.in.Rules != nil && b.in.Rules.IsAgentTool(t.name) {
			t.sum.Kind = core.ToolAgent
		}
	}
}

// isAgent reports whether t spawns a sub-agent (its span is not tool time, E5).
func (t *vtool) isAgent() bool { return t.sum.Kind == core.ToolAgent }

// buildTurns splits the calls into turns by the 013 rule: a main-thread call with a
// prompt of its own starts a turn, side calls are folded apart, every other call joins the
// open turn. A prompt hook no call turn answers starts a hooks-only turn (AC7, 013 AC18).
func (b *builder) buildTurns() {
	var callTurns []*vturn
	var before *vturn
	var open *vturn
	for _, c := range b.calls {
		switch {
		case c.fact.SideCall:
			b.side = append(b.side, c)
			continue
		case c.fact.Main && c.prompt != nil:
			open = &vturn{at: c.start, prompt: c.prompt}
			callTurns = append(callTurns, open)
		case open == nil:
			if before == nil {
				before = &vturn{at: c.start}
			}
			before.calls = append(before.calls, c)
			c.turn = before
			continue
		}
		open.calls = append(open.calls, c)
		c.turn = open
	}

	// Prompt hooks: each call turn takes the latest one received before its first call
	// (a second of skew allowed); the rest start hooks-only turns.
	var hooks []*promptHook
	for _, h := range sortHooks(b.in.Hooks) {
		if core.HarnessPhase(h.Phase) != core.PhasePrompt {
			continue
		}
		ph := &promptHook{at: h.ReceivedAt}
		if b.in.Prompts != nil {
			if text, ok := b.in.Prompts.PromptOf(b.content(h.PayloadHash)); ok {
				ph.text = text
			}
		}
		hooks = append(hooks, ph)
	}
	const skew = int64(time.Second)
	prev := int64(-1 << 62)
	for i, t := range callTurns {
		limit := t.at + skew
		if i+1 < len(callTurns) {
			limit = min(limit, callTurns[i+1].at-1)
		}
		var pick *promptHook
		for _, h := range hooks {
			if !h.used && h.at > prev && h.at <= limit {
				pick = h
			}
		}
		if pick != nil {
			pick.used = true
			t.at = min(t.at, pick.at)
		}
		prev = t.calls[0].start
	}
	turns := callTurns
	for _, h := range hooks {
		if !h.used {
			turns = append(turns, &vturn{at: h.at, hookText: h.text, t: Turn{HooksOnly: true}})
		}
	}
	sort.SliceStable(turns, func(i, j int) bool { return turns[i].at < turns[j].at })
	for i, t := range turns {
		t.t.N = i + 1
	}
	if before != nil {
		turns = append([]*vturn{before}, turns...)
	}
	b.turns = turns

	// Tool calls seen only by hooks go to the turn their first hook falls in.
	for _, t := range b.toolOrder {
		if !t.hookOnly {
			t.turn = t.call.turn
			continue
		}
		t.turn = b.turnAt(t.firstHook)
		if t.turn != nil {
			t.turn.hookOnly = append(t.turn.hookOnly, t)
		}
	}
}

// turnAt is the numbered turn holding time at: the last one that started at or before it,
// else the first one; nil when there is none.
func (b *builder) turnAt(at int64) *vturn {
	var out *vturn
	for _, t := range b.turns {
		if t.t.N == 0 {
			continue
		}
		if out == nil || t.at <= at {
			out = t
		}
		if t.at > at {
			break
		}
	}
	return out
}

// nestSubAgents puts each sub-agent's calls under the agent tool call that spawned it: the
// one whose input's prompt is the sub-agent thread's first prompt.
func (b *builder) nestSubAgents() {
	byPrompt := map[string]*vtool{}
	for _, t := range b.toolOrder {
		if t.isAgent() && !t.hookOnly {
			if p := agentPrompt(t.input); p != "" {
				if _, ok := byPrompt[p]; !ok {
					byPrompt[p] = t
				}
			}
		}
	}
	threadTool := map[int]*vtool{}
	for _, c := range b.calls {
		if !c.fact.Sub || c.fact.SideCall {
			continue
		}
		t, ok := threadTool[c.fact.Thread]
		if !ok {
			t = byPrompt[c.fact.Prompt]
			threadTool[c.fact.Thread] = t
		}
		if t != nil {
			c.subOf = t
			t.sub = append(t.sub, c)
		}
	}
}

// toolTimes gives each tool call without hooks a run time from the gap between the call
// that asked for it and the next call of that call's thread, marked approximate (E3).
func (b *builder) toolTimes() {
	next := map[*vcall]*vcall{}
	last := map[int]*vcall{}
	for _, c := range b.calls {
		th := c.fact.Thread
		if th < 0 || c.fact.SideCall {
			continue
		}
		if p, ok := last[th]; ok {
			next[p] = c
		}
		last[th] = c
	}
	for _, t := range b.toolOrder {
		if t.hasHooks || t.call == nil {
			continue
		}
		if n, ok := next[t.call]; ok && n.start > t.call.end {
			t.start, t.end = t.call.end, n.start
		}
	}
}

// span is the tool call's run time, and whether it is approximate; ok is false when it is
// not known.
func (t *vtool) span() (from, to int64, approx, ok bool) {
	if t.start == 0 || t.end == 0 || t.end < t.start {
		return 0, 0, false, false
	}
	return t.start, t.end, !t.hasHooks, true
}

// union is the length of the union of the spans, each cut to [lo, hi].
func union(spans [][2]int64, lo, hi int64) int64 {
	var s [][2]int64
	for _, x := range spans {
		a, z := max(x[0], lo), min(x[1], hi)
		if z > a {
			s = append(s, [2]int64{a, z})
		}
	}
	sort.Slice(s, func(i, j int) bool { return s[i][0] < s[j][0] })
	var total int64
	var cur [2]int64
	have := false
	for _, x := range s {
		if !have || x[0] > cur[1] {
			if have {
				total += cur[1] - cur[0]
			}
			cur, have = x, true
		} else if x[1] > cur[1] {
			cur[1] = x[1]
		}
	}
	if have {
		total += cur[1] - cur[0]
	}
	return total
}

func ms(ns int64) int64 { return ns / int64(time.Millisecond) }

func rfc(ns int64) string {
	if ns == 0 {
		return ""
	}
	return nsTimeUTC(ns)
}

func nsTimeUTC(ns int64) string { return time.Unix(0, ns).UTC().Format(time.RFC3339Nano) }

// statusWord is an HTTP status as words: "500 Internal Server Error", or the error type
// when the status has no name.
func statusWord(status int, errType string) string {
	if status == 0 {
		if errType != "" {
			return errType
		}
		return "error"
	}
	if t := http.StatusText(status); t != "" {
		return strconv.Itoa(status) + " " + t
	}
	if errType != "" {
		return strconv.Itoa(status) + " " + errType
	}
	return "status " + strconv.Itoa(status)
}

// chipMarks are the tool call states as glyph and word (013 AC13); ok is the glyph alone
// on a line.
var chipMarks = map[string]Mark{
	ChipOK:        {"✓", "ok"},
	ChipFailed:    {"✗", "failed"},
	ChipNotRun:    {"⊘", "not run"},
	ChipUnmatched: {"?", "unmatched"},
	ChipPending:   {"…", "pending"},
	ChipNoHooks:   {"–", "no hooks"},
}

func chipMark(chip string) Mark {
	if m, ok := chipMarks[chip]; ok {
		return m
	}
	return chipMarks[ChipNoHooks]
}
