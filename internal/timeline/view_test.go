package timeline_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// The session view (spec 016): one fixture session shaped like the seed
// (docs/manual-checks/seed-session.mjs), built through timeline.Build and CallFacts as
// the gateway builds it, with the effects' labels given as spec 015 would give them.

const vt0 = int64(1_800_000_000_000_000_000) // fixture epoch, unix ns

func vat(sec float64) int64 { return vt0 + int64(sec*1e9) }

// viewRules is a client's rules as the view reads them: "quota" and "title:" prompts are
// side calls, reminders are <r>…</r> blocks, "Task" spawns a sub-agent, and a side call's
// "TITLE: x" reply is a generated title.
type viewRules struct{}

func (viewRules) IsSideCall(texts []string) bool {
	for _, t := range texts {
		if t == "quota" || strings.HasPrefix(t, "title:") {
			return true
		}
	}
	return false
}

func (viewRules) StripReminders(s string) string {
	body, _ := viewRules{}.SplitReminders(s)
	return body
}

func (viewRules) IsAgentTool(n string) bool { return n == "Task" }

func (viewRules) SplitReminders(s string) (string, []string) {
	var rem []string
	for {
		i := strings.Index(s, "<r>")
		j := strings.Index(s, "</r>")
		if i < 0 || j < i {
			return strings.TrimSpace(s), rem
		}
		rem = append(rem, s[i+3:j])
		s = s[:i] + s[j+4:]
	}
}

func (viewRules) TitleOf(resp string) (string, bool) {
	t, ok := strings.CutPrefix(resp, "TITLE: ")
	return t, ok
}

// viewSummarizer summarises the fixture's tools: Bash is a command, Edit and Read a file,
// Grep a search.
type viewSummarizer struct{}

func (viewSummarizer) SummarizeTool(name string, input []byte, _ string) core.ToolSummary {
	var in map[string]string
	_ = json.Unmarshal(input, &in)
	s := core.ToolSummary{Kind: core.ToolOther, Added: -1, Removed: -1}
	switch name {
	case "Bash":
		s.Kind, s.Arg = core.ToolCommand, in["command"]
	case "Edit", "Read":
		s.Kind, s.Path = core.ToolFile, in["file_path"]
	case "Grep":
		s.Kind, s.Arg = core.ToolSearch, in["pattern"]
	}
	return s
}

// call adds a model call: its request, its messages ("u:" or "a:" then text), its tool
// calls (id, name, JSON input), the results it carries (id, text, "!" first for an error)
// and its usage.
type vcallSpec struct {
	id         string
	from, to   float64
	msgs       []string
	tools      [][3]string
	results    [][2]string
	in, cw, cr int64
	model      string // "m1" when empty
}

func (f *fake) call(c vcallSpec) {
	x := f.exchange(c.id, vat(c.from), "ok")
	x.EndedAt = vat(c.to)
	model := c.model
	if model == "" {
		model = "m1"
	}
	f.request(c.id, model)
	for _, m := range c.msgs {
		role := "user"
		if m[0] == 'a' {
			role = "assistant"
		}
		f.msg(c.id, role, m[2:])
	}
	for _, r := range c.results {
		text, isErr := strings.CutPrefix(r[1], "!")
		f.toolResult(c.id, r[0], text, isErr)
	}
	for _, t := range c.tools {
		f.toolCall(c.id, t[0], t[1], t[2])
	}
	f.usage(c.id, false, n(c.in), n(5), n(c.cw), n(c.cr))
}

// vhook adds a hook row of session k.
func (f *fake) vhook(phase core.HarnessPhase, tool, name string, sec float64, extra ...func(*store.HookRow)) {
	h := store.HookRow{ID: int64(len(f.hooks) + 1), Seq: int64(len(f.hooks) + 1), PrincipalID: "p", Client: "c", SessionKey: "k",
		ToolUseID: tool, Phase: string(phase), SourceName: string(phase), ToolName: name, ReceivedAt: vat(sec), WorkDir: "/w",
		PayloadHash: hashOf(fmt.Sprintf("%s|%s|%v", phase, tool, sec))}
	for _, x := range extra {
		x(&h)
	}
	f.hooks = append(f.hooks, h)
}

// fixture is the seed-shaped session, with its effects and their labels.
type fixture struct {
	f       *fake
	effects []store.EffectRow
	labels  []timeline.EffectLabel
	lines   map[string][2]int  // a change's line counts by path; src/a.js is 2 1 always
	rules   timeline.ViewRules // the view's rules when it has them; viewRules{} when nil
}

func (x *fixture) effect(e store.EffectRow, l timeline.EffectLabel) {
	e.ID = int64(len(x.effects) + 1)
	e.Principal, e.Client, e.SessionKey = "p", "c", "k"
	x.effects = append(x.effects, e)
	x.labels = append(x.labels, l)
}

func tool(id, name string) *timeline.LabelTool { return &timeline.LabelTool{ID: id, Name: name} }

func seedFixture(withTitle bool) *fixture {
	f := newFake()
	x := &fixture{f: f}
	fix := "Fix the retry<r>be brief</r>"
	ran := "Running the tests"
	edit := "Editing two files"
	done := "Fixed the retry."
	f.call(vcallSpec{id: "q", from: 0.2, to: 0.3, msgs: []string{"u:quota"}})
	if withTitle {
		f.call(vcallSpec{id: "ttl", from: 0.4, to: 0.6, msgs: []string{"u:title: name it", "a:TITLE: Retry fix"}})
	}
	// Turn 1: a failed command, two edits at once, the answer.
	f.vhook(core.PhasePrompt, "", "", 0.9)
	f.call(vcallSpec{id: "a1", from: 1, to: 3, msgs: []string{"u:" + fix, "a:" + ran},
		tools: [][3]string{{"t1", "Bash", `{"command":"npm test"}`}}, in: 100})
	f.vhook(core.PhasePreTool, "t1", "Bash", 3.2)
	f.vhook(core.PhasePostToolFailed, "t1", "Bash", 4.2)
	f.call(vcallSpec{id: "a2", from: 5, to: 6, msgs: []string{"u:" + fix, "a:" + ran, "a:" + edit},
		results: [][2]string{{"t1", "!Error: 1 failing\nat retry.test.js:4"}},
		tools:   [][3]string{{"t2", "Edit", `{"file_path":"/w/src/a.js"}`}, {"t3", "Edit", `{"file_path":"/w/src/b.js"}`}}, in: 100, cw: 50, cr: 100})
	f.vhook(core.PhasePreTool, "t2", "Edit", 6.1)
	f.vhook(core.PhasePreTool, "t3", "Edit", 6.2)
	f.vhook(core.PhasePostTool, "t3", "Edit", 6.4)
	f.vhook(core.PhasePostTool, "t2", "Edit", 6.5)
	f.call(vcallSpec{id: "a3", from: 7, to: 8, msgs: []string{"u:" + fix, "a:" + ran, "a:" + edit, "a:" + done},
		results: [][2]string{{"t2", "ok"}, {"t3", "ok"}}, in: 100, cr: 250})
	x.effect(store.EffectRow{Kind: "net_conn", At: vat(3.5), EndedAt: vat(3.8), Host: "registry.npmjs.org", Port: 443, BytesUp: 100, BytesDown: 4096, Outcome: "ok"},
		timeline.EffectLabel{Label: "only_one", Tool: tool("t1", "Bash")})
	x.effect(store.EffectRow{Kind: "file_change", At: vat(6.3), Path: "/w/src/a.js", Change: "modified", Size: 120, BeforeHash: "b1", AfterHash: "a1"},
		timeline.EffectLabel{Label: "named", Tool: tool("t2", "Edit")})
	x.effect(store.EffectRow{Kind: "file_change", At: vat(6.35), Path: "/w/dist/out.js", Change: "created", Size: 2048},
		timeline.EffectLabel{Label: "several", Candidates: []timeline.LabelTool{*tool("t2", "Edit"), *tool("t3", "Edit")}})
	x.effect(store.EffectRow{Kind: "net_conn", At: vat(6.3), EndedAt: vat(6.6), Host: "telemetry.example", Port: 443, Outcome: "ok"},
		timeline.EffectLabel{Label: "harness", Client: "c"})

	// Between turns 1 and 2: an edit no tool made, its window open before turn 2's prompt.
	x.effect(store.EffectRow{Kind: "file_change", At: vat(10.5), Path: "/w/notes.md", Change: "modified", Size: 10, BeforeHash: "b2", AfterHash: "a2", WindowFrom: vat(9)},
		timeline.EffectLabel{Label: "outside"})

	// Turn 2: a sub-agent searches, then the answer.
	ask := "Find the key helper"
	f.vhook(core.PhasePrompt, "", "", 9.9)
	f.call(vcallSpec{id: "b1", from: 10, to: 10.4, msgs: []string{"u:" + fix, "a:" + ran, "a:" + edit, "a:" + done, "u:" + ask},
		tools: [][3]string{{"t4", "Task", `{"prompt":"find makeKey","subagent_type":"Explore"}`}}, in: 100, cr: 400})
	f.vhook(core.PhasePreTool, "t4", "Task", 10.45)
	f.call(vcallSpec{id: "s1", from: 10.5, to: 10.6, msgs: []string{"u:find makeKey"},
		tools: [][3]string{{"t5", "Grep", `{"pattern":"makeKey"}`}}, in: 50})
	f.vhook(core.PhasePreTool, "t5", "Grep", 10.62, func(h *store.HookRow) { h.AgentID, h.AgentType = "ag1", "Explore" })
	f.vhook(core.PhasePostTool, "t5", "Grep", 10.7, func(h *store.HookRow) { h.AgentID, h.AgentType = "ag1", "Explore" })
	f.call(vcallSpec{id: "s2", from: 10.8, to: 11, msgs: []string{"u:find makeKey", "a:makeKey is in src/keys.js"},
		results: [][2]string{{"t5", "src/keys.js"}}, in: 50, cr: 60})
	f.vhook(core.PhasePostTool, "t4", "Task", 11.1)
	f.call(vcallSpec{id: "b2", from: 11.2, to: 12, msgs: []string{"u:" + fix, "a:" + ran, "a:" + edit, "a:" + done, "u:" + ask, "a:It is makeKey in src/keys.js."},
		results: [][2]string{{"t4", "makeKey is in src/keys.js"}}, in: 100, cr: 500})
	x.effect(store.EffectRow{Kind: "file_read", At: vat(10.65), Path: "/w/src/keys.js", Detail: "matches"},
		timeline.EffectLabel{Label: "named", Tool: tool("t5", "Grep")})

	// Turn 3: hooks only, one command.
	f.vhook(core.PhasePrompt, "", "", 20)
	f.vhook(core.PhasePreTool, "h1", "Bash", 20.5, func(h *store.HookRow) { h.InputHash = f.put(`{"command":"ls -la src/payment"}`) })
	f.vhook(core.PhasePostTool, "h1", "Bash", 22)
	return x
}

// view builds the fixture's view as the gateway does: the timeline build, the call facts,
// then BuildView. rules false builds it for a client the gateway has no profile for.
func (x *fixture) view(t *testing.T, rules bool) timeline.View {
	t.Helper()
	f := x.f
	sess := store.SessionRow{PrincipalID: "p", Client: "c", SessionKey: "k"}
	entries, err := timeline.Build(f, sess, timeline.Options{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	var fr timeline.FactRules
	if rules {
		fr = viewRules{}
	}
	facts, err := timeline.CallFacts(f, entries, fr, f.events)
	if err != nil {
		t.Fatal(err)
	}
	exs := map[string]store.ExchangeRow{}
	for _, e := range f.exchanges {
		exs[e.RequestID] = e
	}
	in := timeline.ViewInput{
		Session: sess, Entries: entries, Facts: facts, Exchanges: exs, Hooks: f.hooks,
		Effects: x.effects, Labels: x.labels,
		Content: func(h string) []byte { return f.contents[h] },
		Lines: func(e store.EffectRow) (int, int, bool) {
			if e.Path == "/w/src/a.js" {
				return 2, 1, true
			}
			if l, ok := x.lines[e.Path]; ok {
				return l[0], l[1], true
			}
			return 0, 0, false
		},
		Version: "v",
	}
	if rules {
		in.Rules, in.Summarizer = viewRules{}, viewSummarizer{}
		if x.rules != nil {
			in.Rules = x.rules
		}
	}
	return timeline.BuildView(in)
}

// findTool is a tool line anywhere in the view, sub-agents and hook tools included.
func findTool(v timeline.View, id string) *timeline.ToolLine {
	var walk func(steps []timeline.Step) *timeline.ToolLine
	walk = func(steps []timeline.Step) *timeline.ToolLine {
		for si := range steps {
			for ti := range steps[si].Tools {
				l := &steps[si].Tools[ti]
				if l.ID == id {
					return l
				}
				if l.Sub != nil {
					if got := walk(l.Sub.Steps); got != nil {
						return got
					}
				}
			}
		}
		return nil
	}
	for i := range v.Turns {
		if l := walk(v.Turns[i].Steps); l != nil {
			return l
		}
		for j := range v.Turns[i].HookTools {
			if v.Turns[i].HookTools[j].ID == id {
				return &v.Turns[i].HookTools[j]
			}
		}
	}
	return nil
}

func mustTool(t *testing.T, v timeline.View, id string) *timeline.ToolLine {
	t.Helper()
	l := findTool(v, id)
	if l == nil {
		t.Fatalf("no tool line %s", id)
	}
	return l
}

func stepNumbers(steps []timeline.Step) []int {
	out := []int{}
	for _, s := range steps {
		out = append(out, s.N)
	}
	return out
}

func eq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// AC4, AC5, AC7: turns from main-thread prompts and prompt hooks, side calls apart, calls
// numbered by start across every thread, steps, tool lines, the sub-agent under its tool,
// the hooks-only turn, the answer.
func TestView_Turns(t *testing.T) {
	v := seedFixture(true).view(t, true)
	if len(v.SideCalls) != 2 || v.SideCalls[0].N != 1 || v.SideCalls[1].N != 2 {
		t.Fatalf("side calls = %+v", v.SideCalls)
	}
	if len(v.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(v.Turns))
	}
	t1, t2, t3 := v.Turns[0], v.Turns[1], v.Turns[2]
	eq(t, "turn 1 n", t1.N, 1)
	eq(t, "turn 1 prompt", t1.Prompt, "Fix the retry")
	if len(t1.Reminders) != 1 || t1.Reminders[0] != "be brief" {
		t.Errorf("turn 1 reminders = %q", t1.Reminders)
	}
	eq(t, "turn 2 prompt", t2.Prompt, "Find the key helper")
	eq(t, "steps of turn 1", fmt.Sprint(stepNumbers(t1.Steps)), "[3 4 5]")
	eq(t, "steps of turn 2", fmt.Sprint(stepNumbers(t2.Steps)), "[6 9]")
	eq(t, "step 3 did", t1.Steps[0].Did, "wrote")
	eq(t, "step 5 did", t1.Steps[2].Did, "wrote the answer")
	eq(t, "turn 1 answer", t1.Answer, "Fixed the retry.")
	eq(t, "turn 1 answer call", t1.AnswerCall, 5)
	eq(t, "turn 1 counts", fmt.Sprintf("%+v", t1.Counts), "{Calls:3 Tools:3 Changed:2 Read:0 Searched:0 Sites:2 Failed:1}")
	eq(t, "turn 2 counts", fmt.Sprintf("%+v", t2.Counts), "{Calls:4 Tools:2 Changed:0 Read:0 Searched:1 Sites:0 Failed:0}")

	bash := mustTool(t, v, "t1")
	eq(t, "t1 state", bash.State.Glyph+" "+bash.State.Word, "✗ failed")
	eq(t, "t1 summary", bash.Summary, "npm test")
	eq(t, "t1 first error", bash.FirstError, "Error: 1 failing")
	edit := mustTool(t, v, "t2")
	eq(t, "t2 state", edit.State.Glyph+" "+edit.State.Word, "✓ ok")
	eq(t, "t2 summary", edit.Summary, "src/a.js")
	if len(t1.Steps[1].Tools) != 2 {
		t.Errorf("step 4 ran %d tools, want 2", len(t1.Steps[1].Tools))
	}

	task := mustTool(t, v, "t4")
	if task.Sub == nil {
		t.Fatal("t4 has no sub-agent")
	}
	eq(t, "sub-agent label", task.Sub.Label, "Explore")
	eq(t, "sub-agent calls", task.Sub.Calls, 2)
	eq(t, "sub-agent steps", fmt.Sprint(stepNumbers(task.Sub.Steps)), "[7 8]")
	eq(t, "sub-agent last step", task.Sub.Steps[1].Did, "reported back to the main agent")
	grep := mustTool(t, v, "t5")
	eq(t, "t5 agent", grep.Agent, "Explore")
	if len(t2.Steps[0].Tools) != 1 {
		t.Errorf("the sub-agent's tools leaked into the main step: %+v", t2.Steps[0].Tools)
	}

	eq(t, "turn 3 hooks only", t3.HooksOnly, true)
	if len(t3.Steps) != 0 || len(t3.HookTools) != 1 {
		t.Fatalf("turn 3 steps %d, hook tools %d", len(t3.Steps), len(t3.HookTools))
	}
	ls := t3.HookTools[0]
	eq(t, "h1 state", ls.State.Glyph+" "+ls.State.Word, "? unmatched")
	eq(t, "h1 summary", ls.Summary, "ls -la src/payment")
	eq(t, "h1 call number", ls.CallN, 0)

	// a turn's hash is its content: the same build gives the same hashes, a changed turn a
	// new one, the others theirs (AC12)
	again := seedFixture(true).view(t, true)
	for i := range v.Turns {
		eq(t, fmt.Sprintf("turn %d hash", v.Turns[i].N), again.Turns[i].Hash, v.Turns[i].Hash)
	}
	grown := seedFixture(true)
	grown.f.vhook(core.PhasePreTool, "h2", "Bash", 22)
	g := grown.view(t, true)
	eq(t, "turn 1 hash after turn 3 grew", g.Turns[0].Hash, v.Turns[0].Hash)
	if g.Turns[2].Hash == v.Turns[2].Hash {
		t.Error("turn 3 kept its hash though it gained a tool call")
	}
}

// AC4: clock from the prompt to the last main-thread call's end, model the union of call
// spans, tools the union of non-agent tool spans, waiting the rest; tool run times from
// their hook pairs.
func TestView_Times(t *testing.T) {
	v := seedFixture(true).view(t, true)
	want := []struct{ clock, model, tool, wait int64 }{{7100, 4000, 1400, 1700}, {2100, 1500, 80, 520}, {0, 0, 0, 0}}
	for i, w := range want {
		tr := v.Turns[i]
		got := [4]int64{tr.ClockMS, tr.ModelMS, tr.ToolMS, tr.WaitMS}
		if got != [4]int64{w.clock, w.model, w.tool, w.wait} {
			t.Errorf("turn %d clock/model/tool/wait = %v, want %v", tr.N, got, w)
		}
	}
	for id, ms := range map[string]int64{"t1": 1000, "t2": 400, "t3": 200, "t4": 650, "t5": 80, "h1": 1500} {
		l := mustTool(t, v, id)
		if l.DurMS == nil || *l.DurMS != ms {
			t.Errorf("%s run time = %v, want %d ms", id, l.DurMS, ms)
		}
	}
	eq(t, "session clock", v.Overview.Time.ClockMS, int64(21800))
	eq(t, "session model", v.Overview.Time.ModelMS, int64(5500))
	eq(t, "session tools", v.Overview.Time.ToolMS, int64(1480))
	// waiting is inside turns, the turn headers' sum: 1.7 + 0.52 = 2.22 s; between turns is
	// the clock no turn covers: 21.8 − (7.1 + 2.1) = 12.6 s; the four parts sum to the clock
	tc := v.Overview.Time
	eq(t, "session waiting", tc.WaitMS, int64(2220))
	eq(t, "session between turns", tc.BetweenMS, int64(12600))
	eq(t, "session parts sum to the clock", tc.ModelMS+tc.ToolMS+tc.WaitMS+tc.BetweenMS, tc.ClockMS)
}

// AC6, AC8: a labelled effect under its tool; several after the step of its candidates;
// harness in the turn's loose group with what was running; an outside edit whose window
// opened before the next prompt between the two turns.
func TestView_Placement(t *testing.T) {
	v := seedFixture(true).view(t, true)
	edit := mustTool(t, v, "t2")
	if len(edit.Effects) != 1 {
		t.Fatalf("t2 effects = %+v", edit.Effects)
	}
	e := edit.Effects[0]
	eq(t, "named path", e.Path, "src/a.js")
	eq(t, "named full path", e.FullPath, "/w/src/a.js")
	eq(t, "named lines", fmt.Sprint(e.Added, e.Removed), "2 1")
	eq(t, "named mark", e.Mark.Glyph+" "+e.Mark.Word, "● named")
	eq(t, "named what", e.What.Glyph+" "+e.What.Word, "~ modified")
	eq(t, "named by", e.By, "#4 Edit src/a.js")
	if n := len(mustTool(t, v, "t3").Effects); n != 0 {
		t.Errorf("t3 effects = %d, want 0", n)
	}
	net := mustTool(t, v, "t1").Effects
	if len(net) != 1 || net[0].Host != "registry.npmjs.org" || net[0].Label != "only_one" {
		t.Errorf("t1 effects = %+v", net)
	}
	eq(t, "only one by", net[0].By, "#3 Bash npm test")

	step := v.Turns[0].Steps[1]
	if len(step.Several) != 1 || step.Several[0].Path != "dist/out.js" {
		t.Fatalf("step 4 several = %+v", step.Several)
	}
	eq(t, "several names", strings.Join(step.SeveralNames, " and "), "#4 Edit src/a.js and #4 Edit src/b.js")
	eq(t, "several by", step.Several[0].By, "#4 Edit src/a.js, #4 Edit src/b.js")

	loose := v.Turns[0].Loose
	if len(loose) != 1 || loose[0].Host != "telemetry.example" || loose[0].Label != "harness" {
		t.Fatalf("turn 1 loose = %+v", loose)
	}
	eq(t, "loose during", loose[0].During, "#4 Edit, #4 Edit")
	eq(t, "harness by", loose[0].By, "")

	if len(v.Between) != 1 {
		t.Fatalf("between = %+v", v.Between)
	}
	b := v.Between[0]
	eq(t, "between after", b.After, 1)
	eq(t, "between title", b.Title, "Seen between turns 1 and 2")
	if len(b.Effects) != 1 || b.Effects[0].Path != "notes.md" {
		t.Errorf("between effects = %+v", b.Effects)
	}
	eq(t, "outside by", b.Effects[0].By, "")
	for _, tr := range v.Turns {
		for _, l := range tr.Loose {
			if l.Path == "notes.md" {
				t.Errorf("notes.md also in turn %d", tr.N)
			}
		}
	}

	read := mustTool(t, v, "t5").Effects
	if len(read) != 1 || read[0].Kind != "search" || read[0].What.Word != "searched" {
		t.Errorf("t5 effects = %+v", read)
	}
}

// AC9, AC10: Files groups changes by path with searches apart from reads; Commands lists
// every shell command in order, hooks-only ones too; Sites groups by host and says who;
// Context gives each thread's rows with what each call added.
func TestView_Lists(t *testing.T) {
	v := seedFixture(true).view(t, true)
	var paths []string
	for _, f := range v.Files.Changed {
		paths = append(paths, f.Path)
	}
	eq(t, "changed paths", strings.Join(paths, ","), "src/a.js,dist/out.js,notes.md")
	eq(t, "src/a.js lines", fmt.Sprint(v.Files.Changed[0].Lines, v.Files.Changed[0].Added, v.Files.Changed[0].Removed), "true 2 1")
	eq(t, "dist/out.js lines known", v.Files.Changed[1].Lines, false)
	eq(t, "reads", len(v.Files.Read), 0)
	// 027 C5: the same changes split by where they happened, oldest first
	var groups []string
	for _, g := range v.Files.ByTurn {
		var ps []string
		for _, f := range g.Files {
			ps = append(ps, fmt.Sprintf("%s(%d)", f.Path, len(f.Changes)))
		}
		groups = append(groups, fmt.Sprintf("%s#%d: %s", g.Label, g.Turn, strings.Join(ps, ",")))
	}
	eq(t, "files by turn", strings.Join(groups, " | "), "Turn 1#1: src/a.js(1),dist/out.js(1) | Between turns 1 and 2#0: notes.md(1)")
	if len(v.Files.Searches) != 1 || v.Files.Searches[0].ToolID != "t5" || v.Files.Searches[0].Agent != "Explore" {
		t.Errorf("searches = %+v", v.Files.Searches)
	}

	if len(v.Commands.Items) != 2 {
		t.Fatalf("commands = %+v", v.Commands.Items)
	}
	c1, c2 := v.Commands.Items[0], v.Commands.Items[1]
	eq(t, "command 1", c1.ToolID+" "+c1.Summary+" "+c1.Chip, "t1 npm test failed")
	eq(t, "command 1 did", c1.Did, "1 site")
	eq(t, "command 2", c2.ToolID+" "+c2.Summary, "h1 ls -la src/payment")
	eq(t, "command 2 hooks only", c2.HooksOnly, true)
	// the overview names the longest command whole; the page clamps it
	eq(t, "longest command", v.Overview.Commands.Longest, "ls -la src/payment")
	eq(t, "longest command time", *v.Overview.Commands.LongestMS, int64(1500))

	if len(v.Sites.Items) != 2 {
		t.Fatalf("sites = %+v", v.Sites.Items)
	}
	eq(t, "site 1 who", v.Sites.Items[0].Host+" "+v.Sites.Items[0].Who, "registry.npmjs.org #3 Bash npm test")
	eq(t, "site 2 harness", v.Sites.Items[1].Harness, true)

	var rows []string
	for _, r := range v.Context.Main {
		d := "—"
		if r.Delta != nil {
			d = fmt.Sprint(*r.Delta)
		}
		rows = append(rows, fmt.Sprintf("#%d %d %s", r.N, *r.Ctx, d))
	}
	eq(t, "main context", strings.Join(rows, "; "), "#3 100 —; #4 250 150; #5 350 100; #6 500 150; #9 600 100")
	eq(t, "#4 why", v.Context.Main[1].Why, "added #3's reply, 1 tool result (Bash)")
	eq(t, "#6 why", v.Context.Main[3].Why, "added #5's reply, your next prompt")
	if len(v.Context.Subs) != 1 || v.Context.Subs[0].Label != "Sub-agent Explore" || len(v.Context.Subs[0].Rows) != 2 {
		t.Fatalf("sub-agent context = %+v", v.Context.Subs)
	}
	eq(t, "sub-agent delta", *v.Context.Subs[0].Rows[1].Delta, int64(60))
	eq(t, "context peak", v.Context.Peak, int64(600))
	eq(t, "context note", v.Context.Note, "Turn 3 has no model calls captured.")
}

// AC2: a generated title wins, else the first prompt; a session with no prompt gets a
// readable title, never its key.
func TestView_Titles(t *testing.T) {
	v := seedFixture(true).view(t, true)
	eq(t, "generated", v.Overview.Title+" ("+v.Overview.TitleSource+")", "Retry fix (generated)")
	eq(t, "generated by call", v.Overview.TitleCall, 2)
	v = seedFixture(false).view(t, true)
	eq(t, "from the prompt", v.Overview.Title+" ("+v.Overview.TitleSource+")", "Fix the retry (prompt)")

	hooks := &fixture{f: newFake()}
	hooks.f.vhook(core.PhasePreTool, "h1", "Bash", 1)
	v = hooks.view(t, true)
	eq(t, "hooks only", v.Overview.Title+" ("+v.Overview.TitleSource+")", "Hooks only · /w (hooks_only)")

	// 027 C7b: with no folder the title is the client alone; the start is the list's own column
	bare := &fixture{f: newFake()}
	bare.f.vhook(core.PhasePreTool, "h1", "Bash", 1, func(h *store.HookRow) { h.WorkDir = "" })
	v = bare.view(t, true)
	eq(t, "hooks only, no folder", v.Overview.Title, "Hooks only · c")

	none := &fixture{f: newFake()}
	none.f.call(vcallSpec{id: "r", from: 1, to: 2, msgs: []string{"a:hello"}})
	v = none.view(t, true)
	eq(t, "no prompt source", v.Overview.TitleSource, "no_prompt")
	if !strings.HasPrefix(v.Overview.Title, "No prompt") || strings.Contains(v.Overview.Title, v.Overview.Key) {
		t.Errorf("no-prompt title = %q (key %q)", v.Overview.Title, v.Overview.Key)
	}
}

// AC5, 013 AC17: a client the gateway has no profile for still gets its view: tool names
// alone, prompts whole, no side calls, effects placed by their labels.
func TestView_UnknownClient(t *testing.T) {
	v := seedFixture(true).view(t, false)
	if len(v.SideCalls) != 0 {
		t.Errorf("side calls without rules = %+v", v.SideCalls)
	}
	if len(v.Turns) == 0 {
		t.Fatal("no turns")
	}
	found := false
	for _, tr := range v.Turns {
		if strings.Contains(tr.Prompt, "<r>be brief</r>") {
			found = true
		}
		if len(tr.Reminders) != 0 {
			t.Errorf("turn %d split reminders without rules: %q", tr.N, tr.Reminders)
		}
	}
	if !found {
		t.Error("the prompt was not kept whole")
	}
	for _, id := range []string{"t1", "t2", "t3", "t4"} {
		l := mustTool(t, v, id)
		if l.Summary != "" || l.Name == "" {
			t.Errorf("%s = %q %q, want the name alone", id, l.Name, l.Summary)
		}
	}
	if e := mustTool(t, v, "t2").Effects; len(e) != 1 || e[0].Path != "src/a.js" {
		t.Errorf("t2 effects = %+v", e)
	}
	eq(t, "client", v.Overview.Client, "c")
	eq(t, "commands without a profile", len(v.Commands.Items), 0)
}

// AC5, AC6 (research 2026-11-13, answer 1): a command's tool line sums the line counts of
// the changes labelled for it alone (named, only one running); a several-running change
// that names it among others is listed after the step and never counted on its line.
func TestView_CommandLineCountsLeaveOutSeveral(t *testing.T) {
	f := newFake()
	x := &fixture{f: f, lines: map[string][2]int{
		"/w/package-lock.json": {3, 0}, "/w/out.txt": {1, 1}, "/w/package.json": {5, 2}}}
	ask := "Install and test"
	f.vhook(core.PhasePrompt, "", "", 0.9)
	f.call(vcallSpec{id: "a1", from: 1, to: 2, msgs: []string{"u:" + ask},
		tools: [][3]string{{"t1", "Bash", `{"command":"npm install"}`}, {"t2", "Bash", `{"command":"npm test"}`}}, in: 100})
	f.vhook(core.PhasePreTool, "t1", "Bash", 2.1)
	f.vhook(core.PhasePreTool, "t2", "Bash", 2.2)
	f.vhook(core.PhasePostTool, "t2", "Bash", 2.8)
	f.vhook(core.PhasePostTool, "t1", "Bash", 2.9)
	f.call(vcallSpec{id: "a2", from: 3, to: 4, msgs: []string{"u:" + ask, "a:Done."},
		results: [][2]string{{"t1", "ok"}, {"t2", "ok"}}, in: 100})
	x.effect(store.EffectRow{Kind: "file_change", At: vat(2.3), Path: "/w/package-lock.json", Change: "modified", Size: 10, BeforeHash: "b1", AfterHash: "a1"},
		timeline.EffectLabel{Label: "named", Tool: tool("t1", "Bash")})
	x.effect(store.EffectRow{Kind: "file_change", At: vat(2.4), Path: "/w/out.txt", Change: "modified", Size: 10, BeforeHash: "b2", AfterHash: "a2"},
		timeline.EffectLabel{Label: "only_one", Tool: tool("t1", "Bash")})
	x.effect(store.EffectRow{Kind: "file_change", At: vat(2.5), Path: "/w/package.json", Change: "modified", Size: 10, BeforeHash: "b3", AfterHash: "a3"},
		timeline.EffectLabel{Label: "several", Candidates: []timeline.LabelTool{*tool("t1", "Bash"), *tool("t2", "Bash")}})
	v := x.view(t, true)

	install, test := mustTool(t, v, "t1"), mustTool(t, v, "t2")
	eq(t, "npm install kind", install.Kind, "command")
	eq(t, "npm install lines", fmt.Sprint(install.Added, install.Removed), "4 1")
	eq(t, "npm test lines", fmt.Sprint(test.Added, test.Removed), "-1 -1")
	step := v.Turns[0].Steps[0]
	if len(step.Several) != 1 || step.Several[0].Path != "package.json" {
		t.Fatalf("several = %+v", step.Several)
	}
	eq(t, "several lines", fmt.Sprint(step.Several[0].Added, step.Several[0].Removed), "5 2")
}

// AC4 (research 2026-11-13, Karthik's feedback): each turn names the models its calls used,
// sub-agents included, in first-use order; a side call's model is left out.
func TestView_TurnModels(t *testing.T) {
	f := newFake()
	x := &fixture{f: f}
	one, two := "Fix it", "And the docs"
	f.call(vcallSpec{id: "q", from: 0.2, to: 0.3, msgs: []string{"u:quota"}, model: "small"})
	f.vhook(core.PhasePrompt, "", "", 0.9)
	f.call(vcallSpec{id: "a1", from: 1, to: 2, msgs: []string{"u:" + one}, model: "big"})
	f.call(vcallSpec{id: "a2", from: 3, to: 4, msgs: []string{"u:" + one, "a:Fixed."}, model: "mid"})
	f.vhook(core.PhasePrompt, "", "", 4.9)
	f.call(vcallSpec{id: "b1", from: 5, to: 6, msgs: []string{"u:" + one, "a:Fixed.", "u:" + two, "a:Done."}, model: "mid"})
	v := x.view(t, true)

	if len(v.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(v.Turns))
	}
	eq(t, "turn 1 models", strings.Join(v.Turns[0].Models, ","), "big,mid")
	eq(t, "turn 2 models", strings.Join(v.Turns[1].Models, ","), "mid")
}

// 017 AC14: a turn counts its model calls, sub-agents' included; a hooks-only turn has none.
func TestView_TurnCountsModelCalls(t *testing.T) {
	v := seedFixture(true).view(t, true)
	eq(t, "turn 1 calls", v.Turns[0].Counts.Calls, 3)
	eq(t, "turn 2 calls, its sub-agent's included", v.Turns[1].Counts.Calls, 4)
	eq(t, "hooks-only calls", v.Turns[2].Counts.Calls, 0)
}

// 017 AC2: every "Needs a look" item has a kind, and the overview counts them by kind in a
// fixed order, in words, the items past the shown eight included.
func TestView_LookKinds(t *testing.T) {
	v := seedFixture(true).view(t, true)
	var kinds []string
	for _, l := range v.Overview.NeedsLook {
		kinds = append(kinds, l.Kind)
	}
	// spec 027 C4: the fixture's unmatched tool ran in its hooks-only turn, so that turn's item
	// names it instead of the tool being an item of its own
	eq(t, "item kinds", strings.Join(kinds, ","), "failed_tool,outside_change,unpinned,hooks_only_turn")
	var words []string
	for _, k := range v.Overview.LookKinds {
		words = append(words, fmt.Sprintf("%s=%d %s", k.Kind, k.Count, k.Words))
	}
	eq(t, "look kinds", strings.Join(words, "; "), "failed_tool=1 1 failed tool call; outside_change=1 1 change with no tool running; "+
		"unpinned=1 1 group of effects not pinned to one tool call; hooks_only_turn=1 1 hooks-only turn")
	hooks := v.Overview.NeedsLook[3]
	if !strings.HasPrefix(hooks.Sub, "only the hooks saw it: ") || strings.Contains(hooks.Sub, "the conversation does not") {
		t.Errorf("hooks-only turn's item sub = %q, want the unmatched tool named", hooks.Sub)
	}
}

// 017 AC2 narrowed (D31): a session that is hooks only has no hooks-only-turn item to look
// at, since every turn is one; a session with model calls still lists its hooks-only turns.
func TestView_HooksOnlyTurnLookOnlyBesideModelCalls(t *testing.T) {
	kindsOf := func(v timeline.View) string {
		var ks []string
		for _, l := range v.Overview.NeedsLook {
			ks = append(ks, l.Kind)
		}
		for _, k := range v.Overview.LookKinds {
			ks = append(ks, "kinds:"+k.Kind)
		}
		return strings.Join(ks, ",")
	}
	hooks := &fixture{f: newFake()}
	hooks.f.vhook(core.PhasePrompt, "", "", 1)
	hooks.f.vhook(core.PhasePrompt, "", "", 5)
	v := hooks.view(t, true)
	eq(t, "hooks-only session turns", len(v.Turns), 2)
	eq(t, "hooks-only session look", kindsOf(v), "")

	mixed := seedFixture(true).view(t, true)
	if k := kindsOf(mixed); !strings.Contains(k, "hooks_only_turn") || !strings.Contains(k, "kinds:hooks_only_turn") {
		t.Errorf("session with model calls: look = %s, want its hooks-only turn", k)
	}
}

// callInput is what the server reads for one fixture call beyond the view: its exchange,
// usage, thread fact and stored events.
func (x *fixture) callInput(t *testing.T, id string) timeline.CallInput {
	t.Helper()
	f := x.f
	var ex store.ExchangeRow
	for _, e := range f.exchanges {
		if e.RequestID == id {
			ex = e
		}
	}
	in := timeline.CallInput{Exchange: ex, Usage: timeline.CallOf(ex, f.events[id])}
	sess := store.SessionRow{PrincipalID: "p", Client: "c", SessionKey: "k"}
	entries, err := timeline.Build(f, sess, timeline.Options{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := timeline.CallFacts(f, entries, viewRules{}, f.events)
	if err != nil {
		t.Fatal(err)
	}
	in.Fact = facts[id]
	det, err := timeline.ExchangeDetail(f, ex, 1<<16)
	if err != nil {
		t.Fatal(err)
	}
	in.Detail = &det
	return in
}

func (x *fixture) details(t *testing.T, v timeline.View, id string) timeline.CallDetails {
	t.Helper()
	d, ok := timeline.CallDetailsOf(v, id, x.callInput(t, id))
	if !ok {
		t.Fatalf("no call %s in the view", id)
	}
	return d
}

func i64(p *int64) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprint(*p)
}

// 017 AC6, AC10: a call's labelled facts, and its place among the session's calls, side
// calls included, in call order.
func TestView_CallFacts(t *testing.T) {
	x := seedFixture(true)
	v := x.view(t, true)
	d := x.details(t, v, "a2")
	eq(t, "n", d.N, 4)
	eq(t, "turn", d.Turn, 1)
	eq(t, "model", d.Facts.Model, "m1")
	eq(t, "duration", i64(d.Facts.DurMS), "1000")
	eq(t, "start", d.Facts.Start, time.Unix(0, vat(5)).UTC().Format(time.RFC3339Nano))
	eq(t, "thread", d.Facts.Thread.Kind+" "+d.Facts.Thread.Label, "main main")
	eq(t, "ctx", i64(d.Facts.Ctx), "250")
	eq(t, "nav", fmt.Sprintf("%d/%d %s %s", d.Nav.Index, d.Nav.Total, d.Nav.Prev, d.Nav.Next), "4/9 a1 a3")
	eq(t, "health", fmt.Sprintf("%s %v %d", d.Health.Status, d.Health.OK, len(d.Health.Items)), "200 OK true 0")

	first := x.details(t, v, "q")
	eq(t, "first call has no previous", first.Nav.Prev+"|"+first.Nav.Next, "|ttl")
	last := x.details(t, v, "b2")
	eq(t, "last call has no next", last.Nav.Next, "")

	sub := x.details(t, v, "s1")
	eq(t, "sub-agent thread", sub.Facts.Thread.Kind+" "+sub.Facts.Thread.Label, "sub sub-agent Explore")
	eq(t, "sub-agent turn", sub.Turn, 2)

	if _, ok := timeline.CallDetailsOf(v, "nope", timeline.CallInput{}); ok {
		t.Error("a call not in the view was found (E3)")
	}
}

// 017 AC7: the context, its change since the previous call of its thread and why, and the
// three counters it is made of.
func TestView_CallContextDelta(t *testing.T) {
	x := seedFixture(true)
	v := x.view(t, true)
	a1 := x.details(t, v, "a1")
	eq(t, "first of thread", a1.Context.First, true)
	eq(t, "first size", i64(a1.Context.Size), "100")
	if a1.Context.Why == "" {
		t.Error("the thread's first call says why it is that size")
	}
	a2 := x.details(t, v, "a2")
	eq(t, "delta", i64(a2.Context.Delta), "150")
	eq(t, "since", a2.Context.PrevN, 3)
	eq(t, "parts", i64(a2.Context.Input)+" "+i64(a2.Context.CacheWrite)+" "+i64(a2.Context.CacheRead), "100 50 100")
	if a2.Context.Why == "" {
		t.Error("the change says what was added, in words")
	}
	s2 := x.details(t, v, "s2")
	eq(t, "sub-agent delta is against its own thread", i64(s2.Context.Delta)+" #"+fmt.Sprint(s2.Context.PrevN), "60 #7")
}

// 017 E2: a side call's thread is its kind from the client's profile, and its failure is
// not marked bad.
func TestView_SideCallThread(t *testing.T) {
	x := seedFixture(true)
	v := x.view(t, true)
	v.SideCalls[0].Name, v.SideCalls[0].Status = "quota probe", "429 Too Many Requests"
	d := x.details(t, v, "q")
	eq(t, "side thread", d.Facts.Thread.Kind+" "+d.Facts.Thread.Label, "side quota probe")
	eq(t, "status word", d.Facts.Stop.Word, "429 Too Many Requests")
	eq(t, "not bad", d.Facts.Stop.Bad, false)
	if d.Context.Delta != nil || d.Context.Why == "" {
		t.Errorf("side call context = %+v, want no delta and a reason", d.Context)
	}
	// its health says what the provider answered, as a warning, not in red
	x.f.exchanges[0].Status = 429
	d = x.details(t, v, "q")
	for _, i := range d.Health.Items {
		if i.Tone == "bad" {
			t.Errorf("side call health item %q is bad", i.Text)
		}
	}
	if len(d.Health.Items) == 0 {
		t.Error("the side call's 429 is in its health")
	}
}

// 017 AC11: a call with no usage shows "—" for every count and says why; a failed call's
// status is a word, marked bad, and its health says so.
func TestView_CallMissingUsage(t *testing.T) {
	x := &fixture{f: newFake()}
	f := x.f
	f.call(vcallSpec{id: "ok", from: 1, to: 2, msgs: []string{"u:hello"}, in: 10})
	e := f.exchange("bad", vat(3), "ok")
	e.EndedAt, e.Status = vat(4), 500
	ttfb := int64(1)
	e.TTFBNS = &ttfb
	f.request("bad", "m1")
	f.msg("bad", "user", "hello")
	v := x.view(t, true)
	d := x.details(t, v, "bad")
	eq(t, "counts", i64(d.Context.Size)+" "+i64(d.Context.Input)+" "+i64(d.Context.Output), "— — —")
	if !strings.Contains(d.Context.Note, "failed") {
		t.Errorf("note = %q, want why there is no usage", d.Context.Note)
	}
	eq(t, "stop", d.Facts.Stop.Word+" bad="+fmt.Sprint(d.Facts.Stop.Bad), "500 Internal Server Error bad=true")
	if d.Health.OK || len(d.Health.Items) == 0 || d.Health.Items[0].Tone != "bad" {
		t.Errorf("health = %+v", d.Health)
	}
}

// 017 AC8: what a call saw, each message marked carried over from the previous call of its
// thread or new, with the counts; its thinking flag, its reply and the tool calls it asked
// for.
func TestView_CallSawCarriedAndNew(t *testing.T) {
	x := seedFixture(true)
	v := x.view(t, true)
	d := x.details(t, v, "a2")
	var marks []string
	for _, m := range d.Saw.Messages {
		marks = append(marks, fmt.Sprintf("%s:%v", m.Role, m.New))
	}
	eq(t, "messages", strings.Join(marks, " "), "user:false assistant:false assistant:true")
	eq(t, "counts", fmt.Sprintf("%d carried %d new", *d.Saw.Carried, *d.Saw.New), "2 carried 1 new")
	eq(t, "not first", d.Saw.First, false)
	var tools []string
	for _, l := range d.Tools {
		tools = append(tools, l.ID+" "+l.Name)
	}
	eq(t, "tools asked for", strings.Join(tools, ", "), "t2 Edit, t3 Edit")

	raw := d.Raw
	if raw.Page != "#/exchanges/a2" || raw.Headers == "" {
		t.Errorf("raw = %+v", raw)
	}
}

// 017 AC8: a thread's first call is marked as such: the main thread's and a sub-agent's.
func TestView_FirstCallOfThread(t *testing.T) {
	x := seedFixture(true)
	v := x.view(t, true)
	eq(t, "main thread's first call", x.details(t, v, "a1").Saw.First, true)
	eq(t, "sub-agent's first call", x.details(t, v, "s1").Saw.First, true)
	eq(t, "sub-agent's second call", x.details(t, v, "s2").Saw.First, false)
}

// 017 AC11: a body that was not stored says so where it is missing, and the other sections
// and the raw links still work.
func TestView_CallBodyNotStored(t *testing.T) {
	x := &fixture{f: newFake()}
	f := x.f
	f.call(vcallSpec{id: "a", from: 1, to: 2, msgs: []string{"u:hello"}, in: 10})
	e := f.exchange("cut", vat(3), "ok")
	e.EndedAt, e.ResponseBody = vat(4), "resp-hash"
	f.request("cut", "m1")
	raw, _ := json.Marshal(map[string]string{"type": "text", "text": "Done."})
	reply, _ := json.Marshal(map[string]any{"role": "assistant", "stop_reason": "end_turn",
		"blocks": []map[string]string{{"hash": f.put(string(raw)), "type": "text"}}})
	f.add("cut", store.EventRow{Kind: "message", Source: "response", ContentHash: hashOf("reply"), Payload: reply})
	f.usage("cut", false, n(20), n(5), n(0), n(0))
	v := x.view(t, true)
	d := x.details(t, v, "cut")
	eq(t, "the reply still shows", d.Response.Text+" / "+d.Response.StopReason, "Done. / end turn")

	// a thinking block's placeholder is not reply text, nor message text (016 AC7)
	think, _ := json.Marshal(map[string]string{"type": "thinking", "thinking": "hmm"})
	both, _ := json.Marshal(map[string]any{"role": "assistant", "stop_reason": "end_turn",
		"blocks": []map[string]string{{"hash": f.put(string(think)), "type": "reasoning"}, {"hash": f.put(string(raw)), "type": "text"}}})
	e2 := f.exchange("thought", vat(5), "ok")
	e2.EndedAt, e2.RequestBody, e2.ResponseBody = vat(6), "req3", "resp3"
	f.request("thought", "m1")
	f.add("thought", store.EventRow{Kind: "message", ContentHash: hashOf("thought prior"), Payload: both})
	f.add("thought", store.EventRow{Kind: "message", Source: "response", ContentHash: hashOf("thought reply"), Payload: both})
	f.usage("thought", false, n(1), n(1), n(0), n(0))
	v = x.view(t, true)
	th := x.details(t, v, "thought")
	eq(t, "reply without the placeholder", th.Response.Text, "Done.")
	eq(t, "message without the placeholder", th.Saw.Messages[0].Text, "Done.")
	eq(t, "stop reason as a fact", d.Facts.Stop.Word, "end turn")
	if !strings.Contains(d.Saw.Unavailable, "request body was not stored") {
		t.Errorf("saw = %+v, want why there are no messages", d.Saw)
	}
	if d.Saw.Carried != nil || d.Saw.New != nil {
		t.Errorf("counts = %v %v, want — for both", d.Saw.Carried, d.Saw.New)
	}
	if d.Raw.Request != "" || !strings.Contains(d.Raw.RequestMissing, "not stored") {
		t.Errorf("raw request = %+v", d.Raw)
	}
	if d.Raw.Response == "" || d.Raw.Page == "" {
		t.Errorf("the stored response and the page still link: %+v", d.Raw)
	}
	eq(t, "context still built", i64(d.Context.Size), "20")
	eq(t, "model still known", d.Facts.Model, "m1")
}

// 017 AC11: a reply longer than the cut says it is cut, with the raw response to open; a
// call that failed says it has no reply because it failed, in words.
func TestView_CallReplyCutOrFailed(t *testing.T) {
	x := &fixture{f: newFake()}
	f := x.f
	long := strings.Repeat("all work and no play ", 200)
	e := f.exchange("long", vat(1), "ok")
	e.EndedAt, e.RequestBody, e.ResponseBody = vat(2), "req", "resp"
	f.request("long", "m1")
	f.msg("long", "user", "hi")
	raw, _ := json.Marshal(map[string]string{"type": "text", "text": long})
	reply, _ := json.Marshal(map[string]any{"role": "assistant", "stop_reason": "end_turn",
		"blocks": []map[string]string{{"hash": f.put(string(raw)), "type": "text"}}})
	f.add("long", store.EventRow{Kind: "message", Source: "response", ContentHash: hashOf("long reply"), Payload: reply})
	f.usage("long", false, n(10), n(5), n(0), n(0))
	bad := f.exchange("bad", vat(3), "ok")
	bad.EndedAt, bad.Status, bad.RequestBody, bad.ResponseBody = vat(4), 500, "req2", "resp2"
	ttfb := int64(1)
	bad.TTFBNS = &ttfb
	f.request("bad", "m1")
	f.msg("bad", "user", "hi")
	v := x.view(t, true)

	in := x.callInput(t, "long")
	det, err := timeline.ExchangeDetail(f, in.Exchange, 100)
	if err != nil {
		t.Fatal(err)
	}
	in.Detail = &det
	d, _ := timeline.CallDetailsOf(v, "long", in)
	if !d.Response.Truncated || len(d.Response.Text) > 100 {
		t.Errorf("reply = %d bytes, truncated %v; want it cut at 100 and marked", len(d.Response.Text), d.Response.Truncated)
	}
	b := x.details(t, v, "bad")
	if !strings.Contains(b.Response.Unavailable, "failed") || !strings.Contains(b.Response.Unavailable, "500 Internal Server Error") {
		t.Errorf("failed call's reply = %q, want why there is none", b.Response.Unavailable)
	}
}

// 027 C5: a file changed in two turns is in both turn groups, each with only that turn's
// change and lines, while the by-file list keeps one row with both changes.
func TestView_FilesByTurnSplitsAFileAcrossTurns(t *testing.T) {
	x := seedFixture(true)
	x.effect(store.EffectRow{Kind: "file_change", At: vat(10.7), Path: "/w/src/a.js", Change: "modified", Size: 130, BeforeHash: "a1", AfterHash: "a3"},
		timeline.EffectLabel{Label: "named", Tool: tool("t5", "Grep")})
	v := x.view(t, true)
	eq(t, "by file a.js changes", len(v.Files.Changed[0].Changes), 2)
	var got []string
	for _, g := range v.Files.ByTurn {
		for _, f := range g.Files {
			if f.Path == "src/a.js" {
				got = append(got, fmt.Sprintf("%s:%d", g.Label, len(f.Changes)))
			}
		}
	}
	eq(t, "a.js groups", strings.Join(got, ","), "Turn 1:1,Turn 2:1")
}

// 027 C4b: a turn is drawn as boxes while its main calls and their tool calls fit, as one
// line per call beyond that; a hooks-only turn or one with no call draws nothing.
func TestView_TurnFigure(t *testing.T) {
	v := seedFixture(true).view(t, true)
	eq(t, "fixture turn 1", v.Turns[1].Figure, timeline.FigureBoxes)
	steps := func(calls, tools int) []timeline.Step {
		out := make([]timeline.Step, calls)
		for i := 0; i < tools; i++ {
			out[i%calls].Tools = append(out[i%calls].Tools, timeline.ToolLine{})
		}
		return out
	}
	for _, c := range []struct {
		name        string
		calls, tool int
		hooks       bool
		want        string
	}{
		{"no calls", 0, 0, false, ""},
		{"hooks only", 1, 0, true, ""},
		{"one call", 1, 0, false, timeline.FigureBoxes},
		{"five calls, twelve boxes", 5, 7, false, timeline.FigureBoxes},
		{"six calls", 6, 0, false, timeline.FigureList},
		{"thirteen boxes", 5, 8, false, timeline.FigureList},
	} {
		turn := &timeline.Turn{Steps: steps(max(c.calls, 1), c.tool), HooksOnly: c.hooks}
		if c.calls == 0 {
			turn.Steps = nil
		}
		eq(t, c.name, timeline.TurnFigureOf(turn), c.want)
	}
}
