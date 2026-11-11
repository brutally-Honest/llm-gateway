package timeline

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// RenderViewJSON writes the view as one JSON line: the object GET /api/sessions/{id}/view
// returns (spec 016 AC15).
func RenderViewJSON(w io.Writer, v View) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", b)
	return err
}

// RenderViewText writes the view as text: the overview, then one block per turn with its
// prompt, times, step lines, tool lines and effects, the between-turn blocks in place,
// and the side calls folded into one line (spec 016 AC15). Times print in loc.
func RenderViewText(w io.Writer, v View, loc *time.Location) error {
	p := &viewPrinter{w: w, loc: loc}
	p.overview(v)
	between := map[int][]Between{}
	for _, b := range v.Between {
		between[b.After] = append(between[b.After], b)
	}
	for _, b := range between[0] {
		p.between(b)
	}
	for _, t := range v.Turns {
		p.turn(t)
		if t.N > 0 {
			for _, b := range between[t.N] {
				p.between(b)
			}
		}
	}
	return p.err
}

type viewPrinter struct {
	w   io.Writer
	loc *time.Location
	err error
}

func (p *viewPrinter) line(indent int, format string, a ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, "%s%s\n", strings.Repeat("  ", indent), fmt.Sprintf(format, a...))
}

// FormatDuration renders milliseconds as 0.4s, 32s, 2m 05s or 1h 03m; "—" when unknown.
func FormatDuration(ms *int64) string {
	if ms == nil {
		return "—"
	}
	s := float64(*ms) / 1000
	switch {
	case s < 10:
		return strconv.FormatFloat(s, 'f', 1, 64) + "s"
	case s < 60:
		return strconv.Itoa(int(s+0.5)) + "s"
	}
	total := int(s + 0.5)
	m, sec := total/60, total%60
	if m < 60 {
		return fmt.Sprintf("%dm %02ds", m, sec)
	}
	return fmt.Sprintf("%dh %02dm", m/60, m%60)
}

func dur(ms int64) string { return FormatDuration(&ms) }

func (p *viewPrinter) clock(rfc string) string {
	t, err := time.Parse(time.RFC3339Nano, rfc)
	if err != nil {
		return rfc
	}
	return t.In(p.loc).Format("15:04:05")
}

func (p *viewPrinter) stamp(rfc string) string {
	t, err := time.Parse(time.RFC3339Nano, rfc)
	if err != nil {
		return rfc
	}
	return t.In(p.loc).Format(time.RFC3339)
}

func count(n *int64) string {
	if n == nil {
		return "—"
	}
	return groupDigits(*n)
}

// groupDigits writes n with thousands separators: 12,345.
func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func (p *viewPrinter) overview(v View) {
	o := v.Overview
	head := []string{o.Title, o.Status.Glyph + " " + o.Status.Word, o.Client}
	if len(o.Models) > 0 {
		head = append(head, strings.Join(o.Models, ", "))
	}
	if o.Folder != "" {
		head = append(head, o.Folder)
	}
	if o.From != "" {
		head = append(head, p.stamp(o.From)+" – "+p.stamp(o.To))
	}
	p.line(0, "%s", strings.Join(head, " · "))
	files := fmt.Sprintf("%s changed", plural(o.Files.Changed, "file", "files"))
	if o.Files.Changed > 0 {
		files += fmt.Sprintf(" (+%d −%d lines)", o.Files.Added, o.Files.Removed)
	}
	cmds := plural(o.Commands.Count, "command", "commands")
	if o.Commands.LongestMS != nil {
		cmds += fmt.Sprintf(" (longest %s, %s)", o.Commands.Longest, FormatDuration(o.Commands.LongestMS))
	}
	p.line(0, "time %s (model %s · tools %s) · %s · %d read · %s · %s (%s) · context %s",
		dur(o.Time.ClockMS), dur(o.Time.ModelMS), dur(o.Time.ToolMS), files, o.Files.Read, cmds,
		plural(o.Sites.Count, "site", "sites"), plural(o.Sites.Connections, "connection", "connections"), count(o.Context.Last))
	t := o.Tokens
	tokens := fmt.Sprintf("tokens: %s input · %s output · %s cache write · %s cache read", count(t.Input), count(t.Output), count(t.CacheWrite), count(t.CacheRead))
	if t.Incomplete {
		tokens += " (some calls did not report)"
	}
	p.line(0, "%s", tokens)
	if len(o.NeedsLook) > 0 {
		p.line(0, "needs a look:")
		for _, l := range o.NeedsLook {
			p.line(1, "%s %s — %s", l.Glyph, l.Title, l.Sub)
		}
		if o.MoreLook > 0 {
			p.line(1, "and %d more", o.MoreLook)
		}
	}
	if len(v.SideCalls) > 0 {
		parts := make([]string, len(v.SideCalls))
		for i, s := range v.SideCalls {
			parts[i] = fmt.Sprintf("#%d %s %s", s.N, s.Name, s.Status)
		}
		p.line(0, "side calls: %s", strings.Join(parts, ", "))
	}
	if o.Empty != "" {
		p.line(0, "%s", o.Empty)
	}
}

func (p *viewPrinter) turn(t Turn) {
	p.line(0, "")
	name := "Turn " + strconv.Itoa(t.N)
	if t.N == 0 {
		name = "Before the first prompt"
	}
	switch {
	case t.HooksOnly:
		p.line(0, "%s · %s · hooks only: no model calls captured", name, p.clock(t.At))
	default:
		head := fmt.Sprintf("%s · %s · %s (model %s · tools %s · waiting %s)", name, p.clock(t.At), dur(t.ClockMS),
			dur(t.ModelMS), dur(t.ToolMS), dur(t.WaitMS))
		if t.End != nil {
			head += " · " + t.End.Glyph + " " + t.End.Word
		}
		p.line(0, "%s", head)
	}
	if t.N > 0 {
		p.line(1, "%s", oneLineText(t.Prompt, 200))
	}
	if n := len(t.Reminders); n > 0 {
		p.line(1, "(+%s)", plural(n, "reminder", "reminders"))
	}
	for _, s := range t.Steps {
		p.step(s, 1)
	}
	if len(t.HookTools) > 0 {
		if t.HooksOnly {
			p.line(1, "the hooks saw:")
		} else {
			p.line(1, "seen by hooks only:")
		}
		for _, l := range t.HookTools {
			p.tool(l, 2)
		}
	}
	if len(t.Loose) > 0 {
		p.line(1, "○ not caused by a tool call:")
		for _, e := range t.Loose {
			p.effect(e, 2)
		}
	}
	if t.Answer != "" {
		p.line(1, "answer (#%d): %s", t.AnswerCall, oneLineText(t.Answer, 200))
	}
}

func (p *viewPrinter) step(s Step, indent int) {
	head := fmt.Sprintf("#%d %s %s", s.N, s.Did, dur(s.DurMS))
	if s.Errored {
		head = fmt.Sprintf("#%d ✗ %s %s", s.N, s.Status, dur(s.DurMS))
	}
	switch n := len(s.Tools); {
	case n == 1:
		head += ", ran:"
	case n > 1:
		head += fmt.Sprintf(", ran %d at once:", n)
	}
	if s.Thinking {
		head += " [thinking]"
	}
	p.line(indent, "%s", head)
	for _, l := range s.Tools {
		p.tool(l, indent+1)
	}
	if len(s.Several) > 0 {
		p.line(indent+1, "◌ not pinned to one tool call: %s ran together", strings.Join(s.SeveralNames, " and "))
		for _, e := range s.Several {
			p.effect(e, indent+2)
		}
	}
}

func (p *viewPrinter) tool(l ToolLine, indent int) {
	state := l.State.Glyph
	if l.Chip != ChipOK {
		state += " " + l.State.Word
	}
	parts := []string{state, l.Name}
	if l.Agent != "" {
		parts = append(parts, "["+l.Agent+"]")
	}
	if l.Summary != "" {
		parts = append(parts, l.Summary)
	}
	if l.Added >= 0 && (l.Added > 0 || l.Removed > 0) {
		parts = append(parts, fmt.Sprintf("+%d −%d", l.Added, l.Removed))
	}
	text := strings.Join(parts, " ")
	if l.DurMS != nil {
		approx := ""
		if l.Approx {
			approx = "≈"
		}
		text += " · " + approx + FormatDuration(l.DurMS)
	}
	p.line(indent, "%s", text)
	if l.FirstError != "" {
		p.line(indent+1, "! %s", l.FirstError)
	}
	for _, e := range l.Effects {
		p.effect(e, indent+1)
	}
	if l.Sub != nil {
		p.line(indent+1, "sub-agent %s · %s:", l.Sub.Label, plural(l.Sub.Calls, "model call", "model calls"))
		for _, s := range l.Sub.Steps {
			p.step(s, indent+2)
		}
	}
}

func (p *viewPrinter) effect(e EffectLine, indent int) {
	label := "— no label"
	if e.Mark != nil {
		label = e.Mark.Glyph + " " + e.Mark.Word
	}
	target := e.Path
	if e.Kind == "net" {
		target = e.Host + ":" + strconv.Itoa(e.Port)
	}
	parts := []string{label, e.What.Glyph, e.What.Word, target}
	switch {
	case e.Kind == "net":
		parts = append(parts, fmt.Sprintf("↑ %d B ↓ %d B", e.Up, e.Down))
	case e.Kind == "change" && e.Added >= 0:
		parts = append(parts, fmt.Sprintf("+%d −%d", e.Added, e.Removed))
	case e.Kind == "change" && e.NoCopy && e.Size != nil:
		parts = append(parts, fmt.Sprintf("%d B, no copy", *e.Size))
	case e.Detail != "":
		parts = append(parts, e.Detail)
	}
	if e.During != "" {
		parts = append(parts, "(during "+e.During+")")
	}
	p.line(indent, "%s", strings.Join(parts, " "))
}

func (p *viewPrinter) between(b Between) {
	p.line(0, "")
	p.line(0, "%s · %s", strings.TrimSuffix(strings.SplitN(b.Text, ":", 2)[0], "."), p.clock(b.At))
	for _, e := range b.Effects {
		p.effect(e, 1)
	}
}

// oneLineText is s on one line, cut to n runes.
func oneLineText(s string, n int) string {
	return cutRunes(strings.Join(strings.Fields(s), " "), n)
}
