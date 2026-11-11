package timeline

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// maxLook is how many "Needs a look" items show before "and N more".
const maxLook = 8

// titleRunes is the longest title taken from a prompt.
const titleRunes = 120

// overview builds the top of the page (AC2): title, status, range, folder, model and
// client, the five cards, "Needs a look", tokens and the side calls.
func (b *builder) overview(v *View) {
	o := Overview{Client: b.in.Session.Client, Key: b.in.Session.SessionKey, Models: []string{}, NeedsLook: []LookItem{}}
	o.Status = Mark{"■", "ended"}
	if b.in.Active {
		o.Status = Mark{"●", "active"}
	}
	o.FolderFull, o.Folder = b.folder, b.display(b.folder)
	if b.folder != "" && o.Folder == "." {
		o.Folder = b.folder
		if h := strings.TrimSuffix(b.in.Home, "/"); h != "" {
			if rel, ok := strings.CutPrefix(b.folder, h+"/"); ok {
				o.Folder = "~/" + rel
			}
		}
	}

	from, to := b.sessionRange()
	o.From, o.To = rfc(from), rfc(to)
	models := map[string]int{}
	var usages []CallUsage
	for _, c := range b.calls {
		if c.usage != nil {
			usages = append(usages, *c.usage)
			if !c.fact.SideCall && c.usage.Model != UnknownModel {
				models[c.usage.Model]++
			}
		}
	}
	for m := range models {
		o.Models = append(o.Models, m)
	}
	sort.Slice(o.Models, func(i, j int) bool {
		if models[o.Models[i]] != models[o.Models[j]] {
			return models[o.Models[i]] > models[o.Models[j]]
		}
		return o.Models[i] < o.Models[j]
	})
	o.Calls = len(b.calls)
	var busy int64 // each turn's model ∪ tool time: its clock less its waiting
	for _, t := range v.Turns {
		if t.N > 0 {
			o.Turns++
		}
		o.Time.ModelMS += t.ModelMS
		o.Time.ToolMS += t.ToolMS
		busy += t.ClockMS - t.WaitMS
	}
	o.Time.ClockMS = ms(max(0, to-from))
	o.Time.WaitMS = max(0, o.Time.ClockMS-busy)

	o.Files.Changed = len(v.Files.Changed)
	for _, f := range v.Files.Changed {
		o.Files.Added += f.Added
		o.Files.Removed += f.Removed
	}
	o.Files.Read = len(v.Files.Read)
	o.Commands.Count = len(v.Commands.Items)
	for _, c := range v.Commands.Items {
		if c.DurMS != nil && (o.Commands.LongestMS == nil || *c.DurMS > *o.Commands.LongestMS) {
			d := *c.DurMS
			o.Commands.LongestMS = &d
			words := strings.Fields(c.Summary)
			o.Commands.Longest = strings.Join(words[:min(2, len(words))], " ")
		}
	}
	o.Sites.Count = len(v.Sites.Items)
	for _, s := range v.Sites.Items {
		o.Sites.Connections += s.Connections
	}
	o.Context.Last = v.Context.Now

	sum := Summarize(usages).Total
	o.Tokens = TokenLine{Input: sum.Input.Value, Output: sum.Output.Value, CacheWrite: sum.CacheWrite.Value,
		CacheRead: sum.CacheRead.Value, Incomplete: sum.Incomplete()}

	v.SideCalls = []SideCall{}
	namer, _ := b.in.Rules.(SideCallNamer)
	for _, c := range b.side {
		s := SideCall{N: c.n, ExchangeID: c.id, At: rfc(c.start), DurMS: ms(c.end - c.start), Name: "side call", Status: "ok"}
		if namer != nil {
			s.Name = namer.SideCallName(c.fact.UserTexts)
		}
		if c.errored {
			s.Status = statusWord(c.status, c.errType)
		}
		v.SideCalls = append(v.SideCalls, s)
	}

	b.title(&o, v)
	look := b.needsLook(v)
	if len(look) > maxLook {
		o.MoreLook = len(look) - maxLook
		look = look[:maxLook]
	}
	o.NeedsLook = look
	if o.Turns == 0 {
		switch {
		case len(b.side) > 0:
			o.Empty = "Only side calls were captured: no prompt, so no turns."
		default:
			o.Empty = "Nothing was captured for this session yet."
		}
	}
	v.Overview = o
}

// sessionRange is the session's first and last activity: calls, hook events and effects.
func (b *builder) sessionRange() (from, to int64) {
	see := func(a, z int64) {
		if a > 0 && (from == 0 || a < from) {
			from = a
		}
		if z > to {
			to = z
		}
	}
	for _, c := range b.calls {
		see(c.start, c.end)
	}
	for _, h := range b.in.Hooks {
		see(h.ReceivedAt, h.ReceivedAt)
	}
	for _, e := range b.in.Effects {
		see(e.At, max(e.At, e.EndedAt))
	}
	return from, to
}

// title names the session, never by its key (D10): the latest generated title, else the
// first prompt, else what is known: "Hooks only · <folder>", "No prompt · <folder>", or
// with no folder the client and start time.
func (b *builder) title(o *Overview, v *View) {
	if b.in.Rules != nil {
		for i := len(b.side) - 1; i >= 0; i-- {
			c := b.side[i]
			if c.errored {
				continue
			}
			if t, ok := b.in.Rules.TitleOf(strings.Join(c.texts, "\n")); ok {
				o.Title, o.TitleSource, o.TitleCall = t, "generated", c.n
				return
			}
		}
	}
	for _, t := range v.Turns {
		if t.N > 0 && strings.TrimSpace(t.Prompt) != "" {
			o.Title, o.TitleSource = cutRunes(strings.Join(strings.Fields(t.Prompt), " "), titleRunes), "prompt"
			return
		}
	}
	kind, src := "No prompt", "no_prompt"
	if len(b.calls) == 0 {
		kind, src = "Hooks only", "hooks_only"
	}
	o.TitleSource = src
	if o.Folder != "" {
		o.Title = kind + " · " + o.Folder
		return
	}
	from, _ := b.sessionRange()
	when := ""
	if from > 0 {
		when = " · " + time.Unix(0, from).Local().Format("Jan 2 15:04")
	}
	o.Title = kind + " · " + o.Client + when
}

// needsLook is the fixed set of things worth a second look (Decisions for Karthik 5):
// failed tool calls, errored model calls outside side calls, changes with no tool
// running, effects not pinned to one tool call, hooks-only turns and unmatched calls.
func (b *builder) needsLook(v *View) []LookItem {
	var out []LookItem
	var tools []ToolLine
	var steps []Step
	var walkStep func(s Step)
	walkStep = func(s Step) {
		steps = append(steps, s)
		for _, l := range s.Tools {
			tools = append(tools, l)
			if l.Sub != nil {
				for _, x := range l.Sub.Steps {
					walkStep(x)
				}
			}
		}
	}
	turnOf := map[string]int{}
	for _, t := range v.Turns {
		for _, s := range t.Steps {
			walkStep(s)
		}
		tools = append(tools, t.HookTools...)
		for _, s := range steps {
			if _, ok := turnOf[s.ExchangeID]; !ok {
				turnOf[s.ExchangeID] = t.N
			}
		}
	}
	where := func(n int) string {
		if n == 0 {
			return "before the first prompt"
		}
		return "turn " + strconv.Itoa(n)
	}
	for _, l := range tools {
		if l.Chip != ChipFailed {
			continue
		}
		sub := where(l.Turn)
		if l.FirstError != "" {
			sub += " · " + l.FirstError
		}
		title := l.Name + " failed"
		if l.Summary != "" {
			title += " on " + l.Summary
		}
		out = append(out, LookItem{Tone: "bad", Glyph: "✗", Title: title, Sub: sub, Go: "Open", Target: Target{Kind: "tool", ID: l.ID, Turn: l.Turn}})
	}
	for _, s := range steps {
		if !s.Errored {
			continue
		}
		n := turnOf[s.ExchangeID]
		out = append(out, LookItem{Tone: "bad", Glyph: "✗", Title: "Model call #" + strconv.Itoa(s.N) + " failed: " + s.Status,
			Sub: where(n), Go: "Open", Target: Target{Kind: "call", ID: s.ExchangeID, Turn: n}})
	}
	seen := map[string]bool{}
	for _, f := range v.Files.Changed {
		for _, c := range f.Changes {
			if c.Label != string(core.AttrOutside) || seen[f.FullPath] {
				continue
			}
			seen[f.FullPath] = true
			out = append(out, LookItem{Tone: "warn", Glyph: "○", Title: f.Path + " changed with no tool running",
				Sub: c.Where + " · most likely your own edit", Go: "Open", Target: Target{Kind: "file", ID: f.FullPath, Turn: c.Turn}})
		}
	}
	for _, t := range v.Turns {
		var group func(s Step)
		group = func(s Step) {
			if len(s.Several) > 0 {
				files, conns := 0, 0
				for _, e := range s.Several {
					if e.Kind == "net" {
						conns++
					} else {
						files++
					}
				}
				var what []string
				if files > 0 {
					what = append(what, plural(files, "file change", "file changes"))
				}
				if conns > 0 {
					what = append(what, plural(conns, "connection", "connections"))
				}
				out = append(out, LookItem{Tone: "info", Glyph: "◌", Title: strings.Join(what, " and ") + " can't be pinned to one tool call",
					Sub: where(t.N) + ": " + strings.Join(s.SeveralNames, " and ") + " ran together", Go: "Show in " + where(t.N),
					Target: Target{Kind: "several", ID: s.ExchangeID, Turn: t.N}})
			}
			for _, l := range s.Tools {
				if l.Sub != nil {
					for _, x := range l.Sub.Steps {
						group(x)
					}
				}
			}
		}
		for _, s := range t.Steps {
			group(s)
		}
	}
	for _, t := range v.Turns {
		if t.HooksOnly {
			out = append(out, LookItem{Tone: "info", Glyph: "◇", Title: "Turn " + strconv.Itoa(t.N) + " has no model calls captured",
				Sub: "only the hooks saw it: its tool calls show, the conversation does not", Go: "Show turn " + strconv.Itoa(t.N),
				Target: Target{Kind: "turn", Turn: t.N}})
		}
	}
	for _, l := range tools {
		if l.Chip != ChipUnmatched {
			continue
		}
		title := l.Name
		if l.Summary != "" {
			title += " " + l.Summary
		}
		out = append(out, LookItem{Tone: "info", Glyph: "?", Title: title + " ran with no captured call asking for it",
			Sub: where(l.Turn) + " · ? unmatched", Go: "Open", Target: Target{Kind: "tool", ID: l.ID, Turn: l.Turn}})
	}
	return out
}
