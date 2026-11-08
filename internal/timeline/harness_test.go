package timeline_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

const sec = int64(1_000_000_000)

var hookSeq int64

// hook adds a hook row for session "k". payload distinguishes byte-identical copies.
func (f *fake) hook(phase, id string, at int64, payload string) *store.HookRow {
	hookSeq++
	f.hooks = append(f.hooks, store.HookRow{Seq: hookSeq, PrincipalID: "p", Client: "c", SessionKey: "k", ToolUseID: id, Phase: phase,
		SourceName: phase, ToolName: "Bash", PayloadHash: hashOf(payload), ReceivedAt: at})
	return &f.hooks[len(f.hooks)-1]
}

// hk adds a hook row whose payload is unique to phase and id.
func (f *fake) hk(phase, id string) *store.HookRow {
	return f.hook(phase, id, int64(len(f.hooks)+1)*10*sec, phase+"|"+id)
}

// callFixture: exchange r1 holds the tool_call "t1" with input {"a":1,"b":2}.
func callFixture() *fake {
	f := newFake()
	f.thread("r1", 1, "u:hi")
	f.toolCall("r1", "t1", "Bash", `{"a":1,"b":2}`)
	return f
}

func toolCallOf(t *testing.T, f *fake, id string) timeline.Entry {
	t.Helper()
	for _, e := range build(t, f, timeline.Options{}) {
		if e.Kind == timeline.KindToolCall && e.ToolCallID == id {
			return e
		}
	}
	t.Fatalf("no tool call %s", id)
	return timeline.Entry{}
}

func wantState(t *testing.T, e timeline.Entry, state, reason, outcome string) {
	t.Helper()
	if e.State != state || e.Reason != reason || e.Outcome != outcome {
		t.Errorf("got %q/%q/%q, want %q/%q/%q", e.State, e.Reason, e.Outcome, state, reason, outcome)
	}
}

func withResult(f *fake, id string) {
	f.thread("r2", 2, "u:hi")
	f.toolResult("r2", id, "ok", false)
}

func TestStates_AskedRanReported(t *testing.T) {
	f := callFixture()
	withResult(f, "t1")
	f.hk("pre_tool", "t1")
	f.hk("post_tool", "t1")
	wantState(t, toolCallOf(t, f, "t1"), timeline.StateAskedRanReported, "", timeline.OutcomeSucceeded)
}

func TestStates_RanNotReported(t *testing.T) {
	f := callFixture()
	f.hk("post_tool", "t1")
	wantState(t, toolCallOf(t, f, "t1"), timeline.StateRanNotReported, "", timeline.OutcomeSucceeded)
}

func TestStates_FailureOutcome(t *testing.T) {
	f := callFixture()
	f.hk("post_tool_failed", "t1")
	wantState(t, toolCallOf(t, f, "t1"), timeline.StateRanNotReported, "", timeline.OutcomeFailed)
	withResult(f, "t1")
	wantState(t, toolCallOf(t, f, "t1"), timeline.StateAskedRanReported, "", timeline.OutcomeFailed)
}

func TestStates_DeniedWithID(t *testing.T) {
	f := callFixture()
	f.hk("pre_tool", "t1")
	f.hk("permission_denied", "t1")
	wantState(t, toolCallOf(t, f, "t1"), timeline.StateAskedNotRun, timeline.ReasonDenied, "")
}

func TestStates_DeniedWithoutIDIsNotRun(t *testing.T) {
	f := callFixture()
	f.hk("permission_denied", "")
	withResult(f, "t1")
	wantState(t, toolCallOf(t, f, "t1"), timeline.StateAskedNotRun, timeline.ReasonNotRun, "")
}

func TestStates_NotRunByLaterResult(t *testing.T) {
	f := callFixture()
	f.hk("pre_tool", "t1")
	withResult(f, "t1")
	wantState(t, toolCallOf(t, f, "t1"), timeline.StateAskedNotRun, timeline.ReasonNotRun, "")
}

func TestStates_Pending(t *testing.T) {
	f := callFixture()
	f.hk("pre_tool", "t1")
	wantState(t, toolCallOf(t, f, "t1"), timeline.StatePending, "", "")
}

func TestStates_NoHookEventsMeansNoHarnessData(t *testing.T) {
	f := callFixture()
	withResult(f, "t1")
	wantState(t, toolCallOf(t, f, "t1"), timeline.StateNoHarnessData, "", "")
}

func TestStates_PartialHookDataKeepsRemainingStates(t *testing.T) {
	f := callFixture()
	f.toolCall("r1", "t2", "Bash", `{}`)
	f.hk("post_tool", "t2") // the events for t1 were never delivered
	wantState(t, toolCallOf(t, f, "t1"), timeline.StatePending, "", "")
	wantState(t, toolCallOf(t, f, "t2"), timeline.StateRanNotReported, "", timeline.OutcomeSucceeded)
}

func TestStates_SessionJoinIsExactString(t *testing.T) {
	f := callFixture()
	for _, key := range []string{"K", "k ", "k\n", " k"} {
		f.hk("post_tool", "t1").SessionKey = key
	}
	wantState(t, toolCallOf(t, f, "t1"), timeline.StateNoHarnessData, "", "")
}

func TestStates_ToolJoinIsExactID(t *testing.T) {
	f := callFixture()
	f.hk("post_tool", "T1")
	f.hk("post_tool", "t1 ")
	wantState(t, toolCallOf(t, f, "t1"), timeline.StatePending, "", "")
}

func (f *fake) preInput(id, input string) {
	f.hk("pre_tool", id).InputHash = f.put(input)
}

func TestStates_InputDiffersFlagged(t *testing.T) {
	f := callFixture()
	f.preInput("t1", `{"a":1,"b":3}`)
	e := toolCallOf(t, f, "t1")
	if !e.InputDiffers || e.HookInput != `{"a":1,"b":3}` || e.Text != `{"a":1,"b":2}` {
		t.Errorf("got differs=%v hook=%q text=%q", e.InputDiffers, e.HookInput, e.Text)
	}
	wantState(t, e, timeline.StatePending, "", "")
}

func TestStates_InputKeyOrderIsNotADifference(t *testing.T) {
	f := callFixture()
	f.preInput("t1", `{ "b": 2, "a": 1 }`)
	if e := toolCallOf(t, f, "t1"); e.InputDiffers || e.HookInput != "" {
		t.Errorf("flagged: %+v", e)
	}
	g := callFixture()
	g.preInput("t1", `{"b":2,"a":1.0}`) // numbers as written: 1.0 is not 1
	if !toolCallOf(t, g, "t1").InputDiffers {
		t.Error("1.0 vs 1 not flagged")
	}
}

func TestStates_UnmatchedHookShown(t *testing.T) {
	f := callFixture()
	f.hk("post_tool", "zz")
	f.hk("notice", "")
	var got []timeline.Entry
	for _, e := range build(t, f, timeline.Options{}) {
		if e.Kind == timeline.KindHarness {
			got = append(got, e)
		}
	}
	if len(got) != 2 || got[0].State != timeline.StateUnmatched || got[0].ToolCallID != "zz" ||
		got[1].State != "" || got[1].Phase != "notice" {
		t.Errorf("got %+v", got)
	}
}

func harnessCount(t *testing.T, f *fake) int {
	n := 0
	for _, e := range build(t, f, timeline.Options{}) {
		if e.Kind == timeline.KindHarness {
			n++
		}
	}
	return n
}

func TestStates_DuplicateShownOnce(t *testing.T) {
	f := callFixture()
	f.hook("notice", "", 5*sec, "same")
	f.hook("notice", "", 5*sec+sec, "same") // exactly 1 s later
	f.hook("post_tool", "zz", 5*sec, "withid")
	f.hook("post_tool", "zz", 500*sec, "withid") // far apart, but has an id
	if n := harnessCount(t, f); n != 2 {
		t.Errorf("got %d harness entries, want 2", n)
	}
}

func TestStates_DuplicatesFarApartWithoutIDShownTwice(t *testing.T) {
	f := callFixture()
	f.hook("notice", "", 5*sec, "same")
	f.hook("notice", "", 5*sec+sec+1, "same")
	if n := harnessCount(t, f); n != 2 {
		t.Errorf("got %d, want 2", n)
	}
}

func TestStates_ArrivalOrderDoesNotChangeStates(t *testing.T) {
	// Hook first, exchange second, and the reverse: same states.
	hooksFirst := func() *fake {
		f := newFake()
		f.hk("pre_tool", "t1")
		f.hk("post_tool", "t1")
		f.thread("r1", 1, "u:hi")
		f.toolCall("r1", "t1", "Bash", `{}`)
		withResult(f, "t1")
		return f
	}()
	exchangesFirst := func() *fake {
		f := callFixture()
		withResult(f, "t1")
		f.hk("pre_tool", "t1")
		f.hk("post_tool", "t1")
		return f
	}()
	a, b := toolCallOf(t, hooksFirst, "t1"), toolCallOf(t, exchangesFirst, "t1")
	wantState(t, a, timeline.StateAskedRanReported, "", timeline.OutcomeSucceeded)
	wantState(t, b, timeline.StateAskedRanReported, "", timeline.OutcomeSucceeded)
}

func TestStates_SeededPermutationsGiveIdenticalStates(t *testing.T) {
	base := newFake()
	base.thread("r1", 1, "u:hi")
	for i := 0; i < 5; i++ {
		base.toolCall("r1", fmt.Sprintf("t%d", i), "Bash", `{}`)
	}
	base.thread("r2", 2, "u:hi")
	base.toolResult("r2", "t0", "ok", false)
	base.toolResult("r2", "t1", "ok", false)
	base.toolResult("r2", "t2", "ok", false)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("t%d", i)
		base.hk("pre_tool", id)
		if i == 0 || i == 3 {
			base.hk("post_tool", id)
		}
		if i == 2 {
			base.hk("post_tool_failed", id)
		}
	}
	states := func(f *fake) map[string][3]string {
		out := map[string][3]string{}
		for _, e := range build(t, f, timeline.Options{}) {
			if e.Kind == timeline.KindToolCall {
				out[e.ToolCallID] = [3]string{e.State, e.Reason, e.Outcome}
			}
		}
		return out
	}
	want := states(base)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 100; i++ {
		f := newFake()
		f.contents = base.contents
		f.events = base.events
		f.exchanges = append([]store.ExchangeRow(nil), base.exchanges...)
		f.hooks = append([]store.HookRow(nil), base.hooks...)
		rng.Shuffle(len(f.hooks), func(a, b int) { f.hooks[a], f.hooks[b] = f.hooks[b], f.hooks[a] })
		rng.Shuffle(len(f.exchanges), func(a, b int) { f.exchanges[a], f.exchanges[b] = f.exchanges[b], f.exchanges[a] })
		// The reader hands exchanges back sorted by start, whatever order they were stored in.
		sort.Slice(f.exchanges, func(a, b int) bool { return f.exchanges[a].StartedAt < f.exchanges[b].StartedAt })
		if got := states(f); !reflect.DeepEqual(got, want) {
			t.Fatalf("shuffle %d: got %v, want %v", i, got, want)
		}
	}
}

func TestStates_AgentLabelsOnEventAndCall(t *testing.T) {
	f := callFixture()
	h := f.hk("pre_tool", "t1")
	h.AgentID, h.AgentType = "ag1", "Explore"
	u := f.hk("post_tool", "zz")
	u.AgentID, u.AgentType = "ag2", "Plan"
	e := toolCallOf(t, f, "t1")
	if e.AgentID != "ag1" || e.AgentType != "Explore" {
		t.Errorf("call labels %q %q", e.AgentID, e.AgentType)
	}
	for _, x := range build(t, f, timeline.Options{}) {
		if x.Kind == timeline.KindHarness && (x.AgentID != "ag2" || x.AgentType != "Plan") {
			t.Errorf("event labels %q %q", x.AgentID, x.AgentType)
		}
	}
}

func TestStates_ResultInSameExchangeIsNotLater(t *testing.T) {
	f := callFixture()
	f.toolResult("r1", "t1", "ok", false)
	f.hk("pre_tool", "t1")
	wantState(t, toolCallOf(t, f, "t1"), timeline.StatePending, "", "")
}

func TestStates_UnmatchedHookSitsBetweenExchangesByTime(t *testing.T) {
	f := callFixture() // r1 starts at 1
	f.thread("r2", 100, "u:hi again")
	f.hook("notice", "", 50, "mid")
	f.hook("notice", "", 500, "late")
	var order []string
	for _, e := range build(t, f, timeline.Options{}) {
		switch e.Kind {
		case timeline.KindHarness:
			order = append(order, fmt.Sprintf("hook@%d", e.Start))
		case timeline.KindExchange, timeline.KindFolded:
			order = append(order, e.RequestID)
		}
	}
	want := []string{"r1", "hook@50", "r2", "hook@500"}
	if !equal(order, want) {
		t.Errorf("got %v, want %v", order, want)
	}
}

func TestStates_SessionIsPrincipalClientAndKey(t *testing.T) {
	f := callFixture()
	f.hk("post_tool", "t1").Client = "other"
	f.hk("post_tool", "t1").PrincipalID = "other"
	wantState(t, toolCallOf(t, f, "t1"), timeline.StateNoHarnessData, "", "")
	if n := harnessCount(t, f); n != 0 {
		t.Errorf("%d foreign hook entries shown", n)
	}
}

// TestChipOf_MapsEveryStateAndOutcome pins the whole state-to-chip table, so a change to
// any one row is caught here and not only through an end-to-end fixture.
func TestChipOf_MapsEveryStateAndOutcome(t *testing.T) {
	for _, c := range []struct{ state, outcome, want string }{
		{timeline.StateAskedRanReported, timeline.OutcomeSucceeded, timeline.ChipOK},
		{timeline.StateAskedRanReported, "", timeline.ChipOK},
		{timeline.StateAskedRanReported, timeline.OutcomeFailed, timeline.ChipFailed},
		{timeline.StateAskedNotRun, "", timeline.ChipNotRun},
		{timeline.StateUnmatched, "", timeline.ChipUnmatched},
		{timeline.StatePending, "", timeline.ChipPending},
		{timeline.StateRanNotReported, "", timeline.ChipPending},
		{timeline.StateNoHarnessData, "", timeline.ChipNoHooks},
		{"", "", timeline.ChipNoHooks},
		{"something new", "", timeline.ChipNoHooks},
	} {
		if got := timeline.ChipOf(c.state, c.outcome); got != c.want {
			t.Errorf("ChipOf(%q, %q) = %q, want %q", c.state, c.outcome, got, c.want)
		}
	}
}
