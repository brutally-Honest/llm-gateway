package ui

import (
	"context"
	"math"
	"os"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/effects"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// maxUnassignedNoted is the most unassigned connections a view reads to name them.
const maxUnassignedNoted = 50

// viewDeps is what a session view is built with beyond the store: the client rules and
// the attribution skew, shared by the API and the CLI.
type viewDeps struct {
	rules map[string]ClientRules
	skew  time.Duration
}

// sessionView builds one session's view (spec 016 AC1) from a timeline build: it reads the
// session's hook rows and effects, labels the effects (spec 015), names the unassigned
// connections seen while it ran, and hands everything to timeline.BuildView.
func (vd viewDeps) sessionView(c *store.CtxReader, who sessIdent, bs builtSession, hooks []store.HookRow, active bool, version string) (timeline.View, error) {
	ident := store.SessionIdent{Principal: who.principal, Client: who.client, Key: who.name}
	rows, err := c.SessionEffects(ident)
	if err != nil {
		return timeline.View{}, err
	}
	content := func(hash string) []byte {
		b, err := c.Content(hash)
		if err != nil {
			return nil
		}
		return b
	}
	rules := vd.rules[who.client]
	attrRules, _ := rules.(core.AttributionRules)
	attrs := effects.Attribute(hooks, rows, attrRules, content, vd.skew)
	labels := make([]timeline.EffectLabel, len(attrs))
	for i, a := range attrs {
		labels[i] = toLabel(a)
	}

	in := timeline.ViewInput{
		Session:   store.SessionRow{PrincipalID: who.principal, Client: who.client, SessionKey: who.name},
		Entries:   bs.entries,
		Facts:     bs.calls,
		Exchanges: bs.byID,
		Hooks:     hooks,
		Effects:   rows,
		Labels:    labels,
		Content:   content,
		Lines:     lineCounter(content),
		Active:    active,
		Version:   version,
	}
	if rules != nil {
		in.Rules = rules
	}
	in.Summarizer, _ = rules.(core.ToolSummarizer)
	in.Prompts, _ = rules.(core.PromptReader)
	in.Home, _ = os.UserHomeDir()

	if from, to := activityRange(bs, hooks); from > 0 {
		un, err := c.UnassignedConnsBetween(from, to, maxUnassignedNoted)
		if err != nil {
			return timeline.View{}, err
		}
		lookup := vd.harnessLookup()
		for _, e := range un {
			in.Unassigned = append(in.Unassigned, timeline.UnassignedConn{Host: e.Host, Port: e.Port, At: e.At,
				Label: toLabel(effects.Unassigned(e, lookup))})
		}
	}
	return timeline.BuildView(in), nil
}

// harnessLookup asks every client's attribution rules, by name, whether a host is its
// harness's own traffic.
func (vd viewDeps) harnessLookup() effects.HarnessLookup {
	return Deps{Rules: vd.rules}.harnessLookup("")
}

func toLabel(a effects.Attribution) timeline.EffectLabel {
	l := timeline.EffectLabel{Label: string(a.Label), Client: a.Client}
	if a.Tool != nil {
		l.Tool = &timeline.LabelTool{ID: a.Tool.ID, Name: a.Tool.Name, AgentID: a.Tool.AgentID}
	}
	for _, c := range a.Candidates {
		l.Candidates = append(l.Candidates, timeline.LabelTool{ID: c.ID, Name: c.Name, AgentID: c.AgentID})
	}
	return l
}

// lineCounter counts a file change's lines from its stored versions, by the diff the
// dialog shows: a created file's lines are all added, a deleted file's all removed. A
// change with no stored copy (binary or over the copy limit) has no count.
func lineCounter(content func(string) []byte) func(store.EffectRow) (int, int, bool) {
	return func(e store.EffectRow) (int, int, bool) {
		if e.Kind != string(core.EffectFileChange) {
			return 0, 0, false
		}
		switch {
		case e.Change == string(core.FileDeleted) && e.BeforeHash != "":
			_, r := effects.LineCounts(string(content(e.BeforeHash)), "")
			return 0, r, true
		case e.AfterHash == "":
			return 0, 0, false
		case e.BeforeHash == "":
			if e.Change != string(core.FileCreated) {
				return 0, 0, false
			}
			a, _ := effects.LineCounts("", string(content(e.AfterHash)))
			return a, 0, true
		}
		a, r := effects.LineCounts(string(content(e.BeforeHash)), string(content(e.AfterHash)))
		return a, r, true
	}
}

// activityRange is the session's first and last activity: its exchanges and hook events.
func activityRange(bs builtSession, hooks []store.HookRow) (from, to int64) {
	see := func(a, z int64) {
		if a > 0 && (from == 0 || a < from) {
			from = a
		}
		to = max(to, z)
	}
	for _, x := range bs.byID {
		see(x.StartedAt, max(x.StartedAt, x.EndedAt))
	}
	for _, h := range hooks {
		see(h.ReceivedAt, h.ReceivedAt)
	}
	return from, to
}

// lastActivity is when the session last did anything: its latest exchange start or hook.
func lastActivity(bs builtSession, hooks []store.HookRow) int64 {
	var last int64
	for _, x := range bs.byID {
		last = max(last, x.StartedAt)
	}
	for _, h := range hooks {
		last = max(last, h.ReceivedAt)
	}
	return last
}

// BuildView builds a session's view straight from the store, with no cache: what
// `gateway timeline` prints (spec 016 AC15). full keeps every text whole.
func BuildView(ctx context.Context, r *store.Reader, s store.SessionRow, rules map[string]ClientRules, skew time.Duration, full bool, now time.Time) (timeline.View, error) {
	c := r.WithContext(ctx)
	who := sessIdent{s.PrincipalID, s.Client, s.SessionKey}
	cut := entryCut
	if full {
		cut = math.MaxInt
	}
	exs, err := c.SessionExchanges(s.PrincipalID, s.Client, s.SessionKey)
	if err != nil {
		return timeline.View{}, err
	}
	bs, err := buildFrom(c, who, exs, rules[s.Client], cut)
	if err != nil {
		return timeline.View{}, err
	}
	hooks, err := c.HookEvents(s.PrincipalID, s.Client, s.SessionKey)
	if err != nil {
		return timeline.View{}, err
	}
	vd := viewDeps{rules: rules, skew: skew}
	return vd.sessionView(c, who, bs, hooks, isActive(lastActivity(bs, hooks), now), "")
}
