package effects

import (
	"math"
	"sort"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// ToolRef names one tool call: its id, tool name and the sub-agent it ran in ("" for the
// main thread).
type ToolRef struct {
	ID, Name, AgentID string
}

// Attribution is one effect's label (spec 015). Tool is set for named and only_one;
// Candidates for several. An empty Label means none.
type Attribution struct {
	Label      core.Attribution
	Tool       *ToolRef
	Candidates []ToolRef
}

// InputFn reads a stored tool input by its content hash; nil when it cannot.
type InputFn func(hash string) []byte

// longSpan is how long a span may be before it is checked for every effect instead of
// being found by its start time.
const longSpan = int64(10 * time.Minute)

const openEnd = int64(math.MaxInt64)

// toolSpan is one tool call's running time, from its hook events, widened by the skew.
type toolSpan struct {
	ref        ToolRef
	start, end int64 // unix ns; end is openEnd while it never ended
	ended      bool
	inputHash  string
	workDir    string
}

// Attribute labels a session's effects from its hook rows, in receive order (spec 015
// AC3–AC10). rules may be nil (an unknown client): labels then come from timing only.
// assigned is false for unassigned effects, which are harness or unlabelled.
func Attribute(hooks []store.HookRow, effects []store.EffectRow, rules core.AttributionRules, input InputFn, skew time.Duration, assigned bool) []Attribution {
	out := make([]Attribution, len(effects))
	var spans []*toolSpan
	byHook := map[int64]*toolSpan{}
	if assigned {
		spans, byHook = buildSpans(hooks, rules, int64(skew))
	}
	short, long := splitSpans(spans)
	inputs := map[string][]byte{}
	read := func(hash string) []byte {
		if hash == "" || input == nil {
			return nil
		}
		if b, ok := inputs[hash]; ok {
			return b
		}
		b := input(hash)
		inputs[hash] = b
		return b
	}
	for i, e := range effects {
		out[i] = label(e, short, long, byHook, rules, read, assigned)
	}
	return out
}

// buildSpans pairs hook rows by tool-call id into spans (AC3). A call with no end event
// ends at the next prompt, turn end, turn failure or session end for the main thread, or
// at its agent's end (or the session end) for a sub-agent call. Calls of a tool that
// spawns a sub-agent are dropped (AC4). byHook maps every tool hook row id to its span.
func buildSpans(hooks []store.HookRow, rules core.AttributionRules, skew int64) ([]*toolSpan, map[int64]*toolSpan) {
	byID := map[string]*toolSpan{}
	byHook := map[int64]*toolSpan{}
	var order []*toolSpan
	var mainClose []int64
	agentClose := map[string][]int64{}
	var sessionEnd []int64
	for _, h := range hooks {
		switch core.HarnessPhase(h.Phase) {
		case core.PhasePrompt, core.PhaseTurnEnd, core.PhaseTurnFailed:
			mainClose = append(mainClose, h.ReceivedAt)
		case core.PhaseSessionEnd:
			mainClose = append(mainClose, h.ReceivedAt)
			sessionEnd = append(sessionEnd, h.ReceivedAt)
		case core.PhaseAgentEnd:
			if h.AgentID != "" {
				agentClose[h.AgentID] = append(agentClose[h.AgentID], h.ReceivedAt)
			}
		}
		if h.ToolUseID == "" {
			continue
		}
		s := byID[h.ToolUseID]
		if s == nil {
			s = &toolSpan{ref: ToolRef{ID: h.ToolUseID}, start: h.ReceivedAt, end: openEnd}
			byID[h.ToolUseID] = s
			order = append(order, s)
		}
		byHook[h.ID] = s
		if h.ReceivedAt < s.start {
			s.start = h.ReceivedAt
		}
		if s.ref.Name == "" {
			s.ref.Name = h.ToolName
		}
		if s.ref.AgentID == "" {
			s.ref.AgentID = h.AgentID
		}
		if h.InputHash != "" && (s.inputHash == "" || core.HarnessPhase(h.Phase) == core.PhasePreTool) {
			s.inputHash = h.InputHash
		}
		if s.workDir == "" {
			s.workDir = h.WorkDir
		}
		switch core.HarnessPhase(h.Phase) {
		case core.PhasePostTool, core.PhasePostToolFailed, core.PhasePermissionDenied:
			if !s.ended || h.ReceivedAt < s.end {
				s.end, s.ended = h.ReceivedAt, true
			}
		}
	}
	out := make([]*toolSpan, 0, len(order))
	for _, s := range order {
		if rules != nil && rules.IsAgentTool(s.ref.Name) {
			continue
		}
		if !s.ended {
			closers := mainClose
			if s.ref.AgentID != "" {
				closers = append(append([]int64(nil), agentClose[s.ref.AgentID]...), sessionEnd...)
				sort.Slice(closers, func(i, j int) bool { return closers[i] < closers[j] })
			}
			if j := sort.Search(len(closers), func(j int) bool { return closers[j] >= s.start }); j < len(closers) {
				s.end = closers[j]
			}
		}
		s.start -= skew
		if s.end != openEnd {
			s.end += skew
		}
		out = append(out, s)
	}
	return out, byHook
}

// splitSpans sorts the short spans by start and keeps the long or open ones apart, so
// finding an effect's candidates is a binary search plus a short walk.
func splitSpans(spans []*toolSpan) (short, long []*toolSpan) {
	for _, s := range spans {
		if s.end == openEnd || s.end-s.start > longSpan {
			long = append(long, s)
		} else {
			short = append(short, s)
		}
	}
	sort.SliceStable(short, func(i, j int) bool { return short[i].start < short[j].start })
	return short, long
}

// overlapping is every span that overlaps [from, to], in start order.
func overlapping(short, long []*toolSpan, from, to int64) []*toolSpan {
	var out []*toolSpan
	hi := sort.Search(len(short), func(i int) bool { return short[i].start > to })
	for i := hi - 1; i >= 0 && short[i].start >= from-longSpan; i-- {
		if short[i].end >= from {
			out = append(out, short[i])
		}
	}
	for _, s := range long {
		if s.start <= to && s.end >= from {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// label is one effect's attribution.
func label(e store.EffectRow, short, long []*toolSpan, byHook map[int64]*toolSpan, rules core.AttributionRules, read func(string) []byte, assigned bool) Attribution {
	harness := e.Kind == string(core.EffectNetConn) && rules != nil && rules.HarnessHost(e.Host)
	if !assigned {
		if harness {
			return Attribution{Label: core.AttrHarness}
		}
		return Attribution{}
	}
	var cands []*toolSpan
	var target string
	switch e.Kind {
	case string(core.EffectFileRead):
		s := byHook[e.HookID]
		if s == nil {
			return Attribution{}
		}
		ref := s.ref
		return Attribution{Label: core.AttrNamed, Tool: &ref}
	case string(core.EffectFileChange):
		if e.WindowTo == 0 {
			return Attribution{} // stored before 015 (E13)
		}
		from := e.WindowFrom
		if from == 0 {
			from = math.MinInt64 + longSpan
		}
		cands, target = overlapping(short, long, from, e.WindowTo), e.Path
	case string(core.EffectNetConn):
		cands, target = overlapping(short, long, e.At, e.At), e.Host
	default:
		return Attribution{}
	}
	if rules != nil && target != "" {
		var named *toolSpan
		n := 0
		for _, s := range cands {
			if rules.NamesTarget(s.ref.Name, read(s.inputHash), s.workDir, target) {
				named = s
				n++
			}
		}
		if n == 1 {
			ref := named.ref
			return Attribution{Label: core.AttrNamed, Tool: &ref}
		}
	}
	switch {
	case harness:
		return Attribution{Label: core.AttrHarness}
	case len(cands) == 1:
		ref := cands[0].ref
		return Attribution{Label: core.AttrOnlyOne, Tool: &ref}
	case len(cands) > 1:
		refs := make([]ToolRef, len(cands))
		for i, s := range cands {
			refs[i] = s.ref
		}
		return Attribution{Label: core.AttrSeveral, Candidates: refs}
	}
	return Attribution{Label: core.AttrOutside}
}
