package timeline

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// vEffect is one effect while the view is built.
type vEffect struct {
	row   store.EffectRow
	label EffectLabel
	line  EffectLine
	tool  *vtool // the tool call it sits under
	turn  *vturn // the turn it is shown in
	after int    // between turns after and after+1; -1 when not between
}

// labelMarks are the attribution labels as glyph and word (spec 015 AC12).
var labelMarks = map[string]Mark{
	string(core.AttrNamed):   {"●", "named"},
	string(core.AttrOnlyOne): {"◐", "only one running"},
	string(core.AttrSeveral): {"◌", "several running"},
	string(core.AttrOutside): {"○", "outside any tool call"},
	string(core.AttrHarness): {"⚙", "harness"},
}

var changeMarks = map[string]Mark{
	string(core.FileCreated):  {"+", "created"},
	string(core.FileModified): {"~", "modified"},
	string(core.FileDeleted):  {"−", "deleted"},
}

// display is a path as the page shows it: relative to the session's folder, else absolute
// with the home folder as "~" (Decisions for Karthik 4, E7).
func (b *builder) display(p string) string {
	if p == "" {
		return ""
	}
	if b.folder != "" {
		if p == b.folder {
			return "."
		}
		if rel, ok := strings.CutPrefix(p, strings.TrimSuffix(b.folder, "/")+"/"); ok {
			return rel
		}
	}
	if h := strings.TrimSuffix(b.in.Home, "/"); h != "" {
		if p == h {
			return "~"
		}
		if rel, ok := strings.CutPrefix(p, h+"/"); ok {
			return "~/" + rel
		}
	}
	return filepath.Clean(p)
}

// summaryText is a tool call's argument as the line shows it (core.ToolSummary).
func (b *builder) summaryText(s core.ToolSummary) string {
	p := b.display(s.Path)
	switch {
	case s.Arg == "":
		return p
	case p == "":
		return s.Arg
	}
	return s.Arg + " in " + p
}

// shortName names a tool call apart from parallel calls of the same tool: "#16 Bash npm
// install", its input's first three words with trailing flags dropped (D8).
func (b *builder) shortName(t *vtool) string {
	head := ""
	if t.call != nil {
		head = "#" + strconv.Itoa(t.call.n) + " "
	}
	head += t.name
	if t.agentID != "" || (t.call != nil && t.call.subOf != nil) {
		head += " (agent)"
	}
	words := strings.Fields(b.summaryText(t.sum))
	if len(words) > 3 {
		words = words[:3]
	}
	for len(words) > 1 && strings.HasPrefix(words[len(words)-1], "-") {
		words = words[:len(words)-1]
	}
	if len(words) == 0 {
		return head
	}
	return head + " " + strings.Join(words, " ")
}

// placeEffects builds each effect's line and puts it in its place (AC6, AC8): under the
// tool call it is labelled for; after the step of its latest candidate when several were
// running (D6); between two turns when it is an outside change whose checkpoint window
// starts before the next turn's prompt (D9); else loose in the turn holding its time, with
// the tool calls running then (D7).
func (b *builder) placeEffects() {
	for i, row := range b.in.Effects {
		var lbl EffectLabel
		if i < len(b.in.Labels) {
			lbl = b.in.Labels[i]
		}
		e := &vEffect{row: row, label: lbl, after: -1}
		e.line = b.effectLine(row, lbl)
		b.effects = append(b.effects, e)
		b.place(e)
		e.line.Turn, e.line.Where = 0, ""
		switch {
		case e.after >= 0:
			e.line.Where = betweenWhere(e.after)
		case e.turn != nil && e.turn.t.N > 0:
			e.line.Turn, e.line.Where = e.turn.t.N, "turn "+strconv.Itoa(e.turn.t.N)
		}
		e.line.By, e.line.Why = b.byWords(e), b.whyWords(e)
	}
}

func betweenWhere(after int) string {
	if after == 0 {
		return "before turn 1"
	}
	return "between turns " + strconv.Itoa(after) + " and " + strconv.Itoa(after+1)
}

func (b *builder) place(e *vEffect) {
	at := e.row.At
	if t := e.label.Tool; t != nil {
		if tool, ok := b.tools[t.ID]; ok && tool.turn != nil {
			e.tool, e.turn = tool, tool.turn
			tool.effects = append(tool.effects, e)
			e.line.Tool = tool.id
			return
		}
	}
	turn := b.turnAt(at)
	if e.label.Label == string(core.AttrSeveral) {
		var latest *vtool
		ok := len(e.label.Candidates) > 0
		for _, c := range e.label.Candidates {
			t, found := b.tools[c.ID]
			if !found || t.call == nil {
				ok = false
				break
			}
			if latest == nil || t.call.n > latest.call.n {
				latest = t
			}
			e.line.Candidates = append(e.line.Candidates, b.shortName(t))
		}
		if ok {
			e.turn = latest.turn
			latest.call.several = append(latest.call.several, e)
			return
		}
	}
	if e.label.Label == string(core.AttrOutside) && e.row.Kind == string(core.EffectFileChange) &&
		turn != nil && e.row.WindowFrom != 0 && e.row.WindowFrom < turn.at {
		e.after = turn.t.N - 1
		b.between[e.after] = append(b.between[e.after], e)
		return
	}
	if turn == nil {
		return
	}
	e.turn = turn
	turn.loose = append(turn.loose, e)
	var during []string
	for _, t := range b.toolOrder {
		if t.isAgent() {
			continue
		}
		if from, to, _, ok := t.span(); ok && from <= at && at <= to {
			during = append(during, b.toolRef(t))
		}
	}
	e.line.During = strings.Join(during, ", ")
}

// toolRef is "#n Name", or the name alone for a call seen only by hooks.
func (b *builder) toolRef(t *vtool) string {
	if t.call != nil {
		return "#" + strconv.Itoa(t.call.n) + " " + t.name
	}
	return t.name
}

// effectLine is an effect's line before it is placed.
func (b *builder) effectLine(row store.EffectRow, lbl EffectLabel) EffectLine {
	l := EffectLine{ID: row.ID, At: rfc(row.At), Added: -1, Removed: -1, Label: lbl.Label}
	if m, ok := labelMarks[lbl.Label]; ok {
		mm := m
		l.Mark = &mm
	}
	switch row.Kind {
	case string(core.EffectFileChange):
		l.Kind, l.Change = "change", row.Change
		l.What = changeMarks[row.Change]
		if l.What.Word == "" {
			l.What = Mark{"?", row.Change}
		}
		l.Path, l.FullPath = b.display(row.Path), row.Path
		if row.Change != string(core.FileDeleted) {
			size := row.Size
			l.Size = &size
		}
		l.HasCopy = row.AfterHash != ""
		l.HasDiff = row.BeforeHash != "" && (row.AfterHash != "" || row.Change == string(core.FileDeleted))
		l.NoCopy = row.AfterHash == "" && row.Change != string(core.FileDeleted)
		if b.in.Lines != nil {
			if a, r, ok := b.in.Lines(row); ok {
				l.Added, l.Removed = a, r
			}
		}
	case string(core.EffectFileRead):
		l.Kind, l.What = "read", Mark{"→", "read"}
		if lbl.Tool != nil {
			if t, ok := b.tools[lbl.Tool.ID]; ok && t.sum.Kind == core.ToolSearch {
				l.Kind, l.What = "search", Mark{"⌕", "searched"}
			}
		}
		l.Path, l.FullPath, l.Detail = b.display(row.Path), row.Path, row.Detail
	case string(core.EffectNetConn):
		l.Kind, l.Host, l.Port, l.Detail = "net", row.Host, row.Port, row.Detail
		l.Up, l.Down, l.Outcome = row.BytesUp, row.BytesDown, row.Outcome
		l.What = Mark{"⇄", "connected"}
		if row.Outcome != "" && row.Outcome != string(core.NetOK) {
			l.What = Mark{"✕", row.Outcome}
		}
		if row.EndedAt > 0 {
			d := ms(row.EndedAt - row.At)
			l.DurMS = &d
		}
	default:
		l.Kind, l.What = row.Kind, Mark{"?", row.Kind}
	}
	return l
}

// byWords names the tool call or calls an effect's label points at, the part a list adds
// after the label's glyph and word (AC9: "who changed it in words"): one tool for named and
// only one running, every candidate for several running, nothing for outside and harness,
// whose label says it all. The story shows the same label words (AC6).
func (b *builder) byWords(e *vEffect) string {
	switch e.label.Label {
	case string(core.AttrNamed), string(core.AttrOnlyOne):
		if e.tool != nil {
			return b.shortName(e.tool)
		}
		return toolWords(e.label.Tool)
	case string(core.AttrSeveral):
		if len(e.line.Candidates) > 0 {
			return strings.Join(e.line.Candidates, ", ")
		}
		names := make([]string, 0, len(e.label.Candidates))
		for _, c := range e.label.Candidates {
			if t, ok := b.tools[c.ID]; ok {
				names = append(names, b.shortName(t))
			} else {
				names = append(names, c.Name)
			}
		}
		return strings.Join(names, ", ")
	}
	return ""
}

func toolWords(t *LabelTool) string {
	if t == nil {
		return "a tool call"
	}
	return t.Name
}

// whyWords is why an effect carries its label, as a sentence (AC11).
func (b *builder) whyWords(e *vEffect) string {
	what := "file"
	if e.row.Kind == string(core.EffectNetConn) {
		what = "host"
	}
	name := toolWords(e.label.Tool)
	if e.tool != nil {
		name = b.shortName(e.tool)
	}
	switch e.label.Label {
	case string(core.AttrNamed):
		if e.row.Kind == string(core.EffectFileRead) {
			return name + " read this file: the read comes from its own hook event."
		}
		return name + " names this " + what + " in its input."
	case string(core.AttrOnlyOne):
		return name + " was the only tool call running at the time, but its input does not name this " + what + "."
	case string(core.AttrSeveral):
		names := strings.Join(e.line.Candidates, " and ")
		if names == "" {
			names = strconv.Itoa(len(e.label.Candidates)) + " tool calls"
		}
		return names + " were running together and none names this " + what + ", so no single one is picked."
	case string(core.AttrOutside):
		return "No tool call was running. Most likely your own edit, or a background process."
	case string(core.AttrHarness):
		return e.row.Host + " is one of " + b.in.Session.Client + "'s own hosts (updates, telemetry), not something a tool asked for."
	}
	return "This effect has no label: it was stored before labels existed, or the client is unknown."
}
