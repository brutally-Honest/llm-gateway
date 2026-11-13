package timeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// The coverage lines: what each tab covers and what the gateway does not see (014 AC15,
// split by tab).
const (
	filesCoverage    = "Changes seen at the client's hook checkpoints, plus the files its own read and search tools looked at. Commands such as cat or ls read files too, but only the read tools are recorded as reads."
	commandsCoverage = "Every shell command the client's hooks or model calls show, in the order it ran."
	sitesCoverage    = "Connections through the gateway's proxy only: HTTPS shows host, bytes and timing, never content. MCP servers show only if they use the proxy."
	contextCoverage  = "Each model call's context: input + cache write + cache read tokens. Side calls are separate requests and are left out."
)

// lists builds the Files, Commands, Sites and Context tabs (AC9, AC10).
func (b *builder) lists(v *View) {
	b.filesList(v)
	b.commandsList(v)
	b.sitesList(v)
	b.contextView(v)
}

func (b *builder) filesList(v *View) {
	f := FilesList{Changed: []FileItem{}, Read: []ReadItem{}, Searches: []SearchItem{}, Coverage: filesCoverage}
	byPath := map[string]*FileItem{}
	var order []string
	readBy := map[string]*ReadItem{}
	var readOrder []string
	for _, e := range b.effects {
		switch e.line.Kind {
		case "change":
			it, ok := byPath[e.row.Path]
			if !ok {
				it = &FileItem{Path: e.line.Path, FullPath: e.row.Path, Lines: true, Changes: []EffectLine{}, Reads: []EffectLine{}}
				byPath[e.row.Path] = it
				order = append(order, e.row.Path)
			}
			it.Changes = append(it.Changes, e.line)
			it.Last = e.line
			if e.line.Added >= 0 {
				it.Added += e.line.Added
				it.Removed += e.line.Removed
			} else {
				it.Lines = false
			}
		case "read":
			it, ok := readBy[e.row.Path]
			if !ok {
				it = &ReadItem{Path: e.line.Path, FullPath: e.row.Path, Reads: []EffectLine{}}
				readBy[e.row.Path] = it
				readOrder = append(readOrder, e.row.Path)
			}
			it.Count++
			it.Reads = append(it.Reads, e.line)
		}
	}
	for _, p := range order {
		it := byPath[p]
		if r, ok := readBy[p]; ok {
			it.Reads = r.Reads
		}
		f.Changed = append(f.Changed, *it)
	}
	for _, p := range readOrder {
		it := readBy[p]
		var where []string
		seen := map[string]bool{}
		for _, r := range it.Reads {
			if r.Where != "" && !seen[r.Where] {
				seen[r.Where] = true
				where = append(where, r.Where)
			}
		}
		it.Where = strings.Join(where, ", ")
		f.Read = append(f.Read, *it)
	}
	for _, t := range b.toolOrder {
		if t.sum.Kind != core.ToolSearch {
			continue
		}
		s := SearchItem{ToolID: t.id, Name: t.name, Summary: b.summaryText(t.sum), Agent: t.agentType}
		if t.turn != nil && t.turn.t.N > 0 {
			s.Where = "turn " + strconv.Itoa(t.turn.t.N)
		}
		f.Searches = append(f.Searches, s)
	}
	if len(f.Changed)+len(f.Read)+len(f.Searches) == 0 {
		f.Empty = "No file changes, reads or searches were seen in this session."
	}
	v.Files = f
}

func (b *builder) commandsList(v *View) {
	c := CommandsList{Items: []CommandItem{}, Coverage: commandsCoverage}
	cmds := make([]*vtool, 0)
	for _, t := range b.toolOrder {
		if t.sum.Kind == core.ToolCommand {
			cmds = append(cmds, t)
		}
	}
	sort.SliceStable(cmds, func(i, j int) bool { return toolAt(cmds[i]) < toolAt(cmds[j]) })
	for _, t := range cmds {
		it := CommandItem{ToolID: t.id, Summary: b.summaryText(t.sum), State: chipMark(t.chip), Chip: t.chip, HooksOnly: t.hookOnly}
		if t.turn != nil {
			it.Turn = t.turn.t.N
		}
		if from, to, approx, ok := t.span(); ok {
			d := ms(to - from)
			it.DurMS, it.Approx = &d, approx
		}
		changed, sites := map[string]bool{}, map[string]bool{}
		for _, e := range t.effects {
			switch e.line.Kind {
			case "change":
				changed[e.row.Path] = true
			case "net":
				sites[e.row.Host] = true
			}
		}
		var did []string
		if n := len(changed); n > 0 {
			did = append(did, plural(n, "file", "files")+" changed")
		}
		if n := len(sites); n > 0 {
			did = append(did, plural(n, "site", "sites"))
		}
		if n := b.unpinnedOf(t); n > 0 {
			did = append(did, plural(n, "effect", "effects")+" not pinned to one call")
		}
		it.Did = strings.Join(did, " · ")
		if it.Did == "" {
			it.Did = "nothing changed"
		}
		c.Items = append(c.Items, it)
	}
	if len(c.Items) == 0 {
		c.Empty = "No shell commands were seen in this session."
	}
	v.Commands = c
}

// unpinnedOf counts the several-running effects that name t among their candidates.
func (b *builder) unpinnedOf(t *vtool) int {
	n := 0
	for _, e := range b.effects {
		if e.label.Label != string(core.AttrSeveral) {
			continue
		}
		for _, c := range e.label.Candidates {
			if c.ID == t.id {
				n++
				break
			}
		}
	}
	return n
}

// toolAt is when a tool call started: its first hook, else the call that asked for it.
func toolAt(t *vtool) int64 {
	if t.start > 0 {
		return t.start
	}
	if t.firstHook > 0 {
		return t.firstHook
	}
	if t.call != nil {
		return t.call.end
	}
	return 0
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func (b *builder) sitesList(v *View) {
	s := SitesList{Items: []SiteItem{}, Coverage: sitesCoverage}
	byHost := map[string]*SiteItem{}
	var order []string
	who := map[string][]string{}
	turns := map[string][]string{}
	for _, e := range b.effects {
		if e.line.Kind != "net" {
			continue
		}
		it, ok := byHost[e.row.Host]
		if !ok {
			it = &SiteItem{Host: e.row.Host, Port: e.row.Port, Conns: []EffectLine{}}
			byHost[e.row.Host] = it
			order = append(order, e.row.Host)
		}
		it.Connections++
		it.Up += e.row.BytesUp
		it.Down += e.row.BytesDown
		it.Conns = append(it.Conns, e.line)
		if e.label.Label == string(core.AttrHarness) {
			it.Harness = true
		}
		w := ""
		switch {
		case e.label.Label == string(core.AttrHarness):
			w = b.in.Session.Client + " itself"
		case e.tool != nil:
			w = b.shortName(e.tool)
		case len(e.line.Candidates) > 0:
			w = strings.Join(e.line.Candidates, ", ")
		case e.label.Label == string(core.AttrOutside):
			w = "no tool running"
		}
		who[e.row.Host] = appendNew(who[e.row.Host], w)
		turns[e.row.Host] = appendNew(turns[e.row.Host], e.line.Where)
	}
	for _, h := range order {
		it := byHost[h]
		it.Who = strings.Join(who[h], ", ")
		it.Where = strings.Join(turns[h], ", ")
		s.Items = append(s.Items, *it)
	}
	if len(s.Items) == 0 {
		s.Empty = "No connections through the gateway's proxy were seen in this session."
	}
	if n := len(b.in.Unassigned); n > 0 {
		u := b.in.Unassigned[0]
		note := &UnassignedNote{Count: n, Host: u.Host, At: rfc(u.At)}
		if m, ok := labelMarks[u.Label.Label]; ok {
			note.Label = m.Glyph + " " + m.Word
			if u.Label.Client != "" {
				note.Label += " · " + u.Label.Client
			}
		}
		s.Unassigned = note
	}
	v.Sites = s
}

func appendNew(list []string, s string) []string {
	if s == "" {
		return list
	}
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// ctxOf is a call's context: input + cache write + cache read; nil when any is unreported
// (shown "—", never 0).
func ctxOf(c *vcall) *int64 {
	if c.usage == nil || c.usage.Input == nil || c.usage.CacheWrite == nil || c.usage.CacheRead == nil {
		return nil
	}
	v := *c.usage.Input + *c.usage.CacheWrite + *c.usage.CacheRead
	return &v
}

// contextView builds the Context tab (AC10, D12): the main thread's calls, then each
// sub-agent's apart; side calls are left out. Each row says what its call added since the
// previous call of its thread.
func (b *builder) contextView(v *View) {
	cv := ContextView{Main: []CtxRow{}, Subs: []CtxThread{}, Coverage: contextCoverage}
	var mains []*vcall
	subs := map[int][]*vcall{}
	var subOrder []int
	for _, c := range b.calls {
		switch {
		case c.fact.SideCall:
		case c.fact.Main:
			mains = append(mains, c)
		case c.fact.Sub:
			if _, ok := subs[c.fact.Thread]; !ok {
				subOrder = append(subOrder, c.fact.Thread)
			}
			subs[c.fact.Thread] = append(subs[c.fact.Thread], c)
		}
	}
	cv.Main = b.ctxRows(mains, "first call: system prompt, tool definitions and your prompt")
	for _, r := range cv.Main {
		if r.Ctx != nil && *r.Ctx > cv.Peak {
			cv.Peak = *r.Ctx
		}
	}
	if n := len(cv.Main); n > 0 {
		cv.Now, cv.First = cv.Main[n-1].Ctx, cv.Main[0].Ctx
		if cv.Now != nil && cv.First != nil {
			g := *cv.Now - *cv.First
			cv.Growth = &g
		}
	}
	for _, th := range subOrder {
		calls := subs[th]
		label := "Sub-agent"
		if calls[0].subOf != nil {
			label = "Sub-agent " + calls[0].subOf.agentLabel()
		}
		cv.Subs = append(cv.Subs, CtxThread{Label: label, Rows: b.ctxRows(calls, "first call of the sub-agent: its instructions and task")})
	}
	var notes []string
	for _, t := range b.turns {
		if t.t.HooksOnly {
			notes = append(notes, "Turn "+strconv.Itoa(t.t.N)+" has no model calls captured.")
		}
	}
	if len(mains) == 0 {
		notes = append([]string{"No main-thread model calls were captured."}, notes...)
	}
	cv.Note = strings.Join(notes, " ")
	v.Context = cv
}

func (b *builder) ctxRows(calls []*vcall, first string) []CtxRow {
	rows := make([]CtxRow, 0, len(calls))
	for i, c := range calls {
		r := CtxRow{N: c.n, ExchangeID: c.id, Ctx: ctxOf(c)}
		if c.turn != nil {
			r.Turn = c.turn.t.N
		}
		if i == 0 {
			r.Why = first
			rows = append(rows, r)
			continue
		}
		// The previous call is the last one that answered: an errored call has no reply
		// and added nothing.
		var p *vcall
		for j := i - 1; j >= 0; j-- {
			if !calls[j].errored {
				p = calls[j]
				break
			}
		}
		if p == nil {
			r.Why = first
			rows = append(rows, r)
			continue
		}
		parts := []string{"#" + strconv.Itoa(p.n) + "'s reply"}
		if len(p.tools) > 0 {
			counts := map[string]int{}
			var names []string
			for _, t := range p.tools {
				if counts[t.name] == 0 {
					names = append(names, t.name)
				}
				counts[t.name]++
			}
			for i, n := range names {
				if counts[n] > 1 {
					names[i] = n + " ×" + strconv.Itoa(counts[n])
				}
			}
			parts = append(parts, plural(len(p.tools), "tool result", "tool results")+" ("+strings.Join(names, ", ")+")")
		}
		if p.turn != c.turn {
			parts = append(parts, "your next prompt")
		}
		r.Why = "added " + strings.Join(parts, ", ")
		if pc := ctxOf(p); pc != nil && r.Ctx != nil {
			d := *r.Ctx - *pc
			r.Delta = &d
		}
		rows = append(rows, r)
	}
	return rows
}

// hashTurns sets each turn's content hash, so a live update resends only the turns that
// changed (AC12).
func hashTurns(v *View) {
	for i := range v.Turns {
		v.Turns[i].Hash = ""
		raw, _ := json.Marshal(v.Turns[i])
		sum := sha256.Sum256(raw)
		v.Turns[i].Hash = hex.EncodeToString(sum[:8])
	}
}
