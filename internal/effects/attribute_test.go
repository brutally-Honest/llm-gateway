package effects

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// fakeAttrRules: an input names a target when it contains it; "harness.example" is the
// harness's own host; "Agent" spawns sub-agents.
type fakeAttrRules struct{}

func (fakeAttrRules) NamesTarget(_ string, input []byte, _, target string) bool {
	return strings.Contains(string(input), target)
}
func (fakeAttrRules) HarnessHost(host string) bool { return host == "harness.example" }
func (fakeAttrRules) IsAgentTool(tool string) bool { return tool == "Agent" }

const ms = int64(time.Millisecond)

// AC3–AC10, E1, E2, E4, E10, E13: one session's hook rows and effects yield every label;
// an interrupted call ends at the turn end, a sub-agent call at its agent's end, the
// Agent call is never a candidate, a span's start moves back by the skew and its end does
// not, named beats harness, and with no rules only timing labels are given.
func TestAttribute_Labels(t *testing.T) {
	var rows []store.HookRow
	id := int64(0)
	hook := func(at int64, phase core.HarnessPhase, tool, name, agent, input string) int64 {
		id++
		rows = append(rows, store.HookRow{ID: id, ToolUseID: tool, Phase: string(phase), ToolName: name,
			AgentID: agent, InputHash: input, ReceivedAt: at * ms, WorkDir: "/w"})
		return id
	}
	inputs := map[string][]byte{"in-write": []byte(`{"file_path":"/w/a"}`), "in-curl": []byte(`curl harness.example`)}

	hook(0, core.PhasePrompt, "", "", "", "")
	hook(100, core.PhasePreTool, "t1", "Write", "", "in-write")
	t1Post := hook(200, core.PhasePostTool, "t1", "Write", "", "in-write")
	hook(1000, core.PhasePreTool, "t2", "Bash", "", "")
	hook(1500, core.PhasePostTool, "t2", "Bash", "", "")
	hook(2000, core.PhasePreTool, "t3", "Bash", "", "")
	hook(2010, core.PhasePreTool, "t4", "Bash", "", "")
	hook(2500, core.PhasePostTool, "t3", "Bash", "", "")
	hook(2510, core.PhasePostTool, "t4", "Bash", "", "")
	hook(4000, core.PhasePreTool, "t5", "Bash", "", "") // interrupted: no post-tool hook
	hook(4500, core.PhaseTurnEnd, "", "", "", "")
	hook(4900, core.PhasePrompt, "", "", "", "")
	hook(5000, core.PhasePreTool, "t6", "Agent", "", "")
	hook(5010, core.PhasePostTool, "t6", "Agent", "", "") // background agent: ends early
	hook(5020, core.PhaseAgentStart, "", "", "ag1", "")
	hook(5050, core.PhaseTurnEnd, "", "", "", "")
	hook(5100, core.PhasePreTool, "t7", "Bash", "ag1", "") // no post-tool hook
	hook(5600, core.PhaseAgentEnd, "", "", "ag1", "")
	hook(6000, core.PhasePostTool, "t8", "Bash", "", "") // received before its pre-tool hook
	hook(6005, core.PhasePreTool, "t8", "Bash", "", "")
	hook(7000, core.PhasePreTool, "t9", "Bash", "", "in-curl")
	hook(7500, core.PhasePostTool, "t9", "Bash", "", "in-curl")
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ReceivedAt < rows[j].ReceivedAt })

	change := func(path string, from, to int64) store.EffectRow {
		return store.EffectRow{Kind: string(core.EffectFileChange), Path: path, WindowFrom: from * ms, WindowTo: to * ms}
	}
	conn := func(host string, at int64) store.EffectRow {
		return store.EffectRow{Kind: string(core.EffectNetConn), Host: host, At: at * ms}
	}
	cases := []struct {
		name   string
		effect store.EffectRow
		label  core.Attribution
		tools  []string // the picked tool, or the candidates
	}{
		{"named write", change("/w/a", 150, 250), core.AttrNamed, []string{"t1"}},
		{"read names its own call", store.EffectRow{Kind: string(core.EffectFileRead), Path: "/w/r", HookID: t1Post}, core.AttrNamed, []string{"t1"}},
		{"only one running", change("/w/b", 1100, 1600), core.AttrOnlyOne, []string{"t2"}},
		{"previous call's end is not widened", change("/w/b2", 1520, 1700), core.AttrOutside, nil},
		{"several running", change("/w/c", 2100, 2600), core.AttrSeveral, []string{"t3", "t4"}},
		{"outside any tool call", conn("x.example", 3000), core.AttrOutside, nil},
		{"interrupted call still running", conn("x.example", 4200), core.AttrOnlyOne, []string{"t5"}},
		{"interrupted call ends at turn end", conn("x.example", 4700), core.AttrOutside, nil},
		{"harness while a call runs", conn("harness.example", 4200), core.AttrHarness, nil},
		{"sub-agent call; Agent never a candidate", conn("x.example", 5300), core.AttrOnlyOne, []string{"t7"}},
		{"sub-agent call ends at its agent's end", conn("x.example", 5700), core.AttrOutside, nil},
		{"post before pre; start widened by skew", conn("x.example", 5950), core.AttrOnlyOne, []string{"t8"}},
		{"named beats harness", conn("harness.example", 7200), core.AttrNamed, []string{"t9"}},
		{"no window, no label", store.EffectRow{Kind: string(core.EffectFileChange), Path: "/w/old"}, "", nil},
	}
	effects := make([]store.EffectRow, len(cases))
	for i, c := range cases {
		effects[i] = c.effect
	}
	read := func(h string) []byte { return inputs[h] }
	got := Attribute(rows, effects, fakeAttrRules{}, read, 100*time.Millisecond, true)
	for i, c := range cases {
		if a := got[i]; a.Label != c.label || !sameTools(a, c.tools) {
			t.Errorf("%s: got %s %v %v, want %s %v", c.name, a.Label, a.Tool, a.Candidates, c.label, c.tools)
		}
	}

	// AC9: unassigned connections are harness or unlabelled.
	un := Attribute(nil, []store.EffectRow{conn("harness.example", 1), conn("x.example", 1)}, fakeAttrRules{}, read, 0, false)
	if un[0].Label != core.AttrHarness || un[1].Label != "" {
		t.Errorf("unassigned = %+v", un)
	}
	// AC10: with no rules, timing only: the Write is only one running, the harness host too.
	none := Attribute(rows, []store.EffectRow{change("/w/a", 150, 250), conn("harness.example", 4200)}, nil, read, 100*time.Millisecond, true)
	if none[0].Label != core.AttrOnlyOne || none[1].Label != core.AttrOnlyOne {
		t.Errorf("no rules = %s, %s; want only_one twice", none[0].Label, none[1].Label)
	}
}

func sameTools(a Attribution, want []string) bool {
	var got []string
	if a.Tool != nil {
		got = append(got, a.Tool.ID)
	}
	for _, c := range a.Candidates {
		got = append(got, c.ID)
	}
	return strings.Join(got, ",") == strings.Join(want, ",")
}
