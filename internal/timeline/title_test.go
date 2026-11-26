package timeline_test

import (
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// 046 U1: turn titles and the session title. titleRules is viewRules plus a title rule of
// its own: "SYS: x" is a synthetic prompt titled "(system) x", "<p>x</p>" is x.
type titleRules struct{ viewRules }

func (titleRules) PromptTitle(p string) (string, bool) {
	if s, ok := strings.CutPrefix(p, "SYS: "); ok {
		return "(system) " + s, true
	}
	p = strings.TrimSuffix(strings.TrimPrefix(p, "<p>"), "</p>")
	return p, false
}

// titleSession is a session whose turns are msgs, one per call, each call carrying the
// messages before it.
func titleSession(msgs ...string) *fixture {
	f := newFake()
	var seen []string
	for i, m := range msgs {
		seen = append(seen, "u:"+m)
		f.call(vcallSpec{id: "c" + string(rune('a'+i)), from: float64(i*2 + 1), to: float64(i*2 + 2), msgs: append(append([]string{}, seen...), "a:ok")})
		seen = append(seen, "a:ok")
	}
	return &fixture{f: f}
}

func (x *fixture) viewWith(t *testing.T, rules timeline.ViewRules) timeline.View {
	t.Helper()
	x.rules = rules
	return x.view(t, true)
}

func TestView_TurnTitleFollowsTheRuleAndPromptStaysWhole(t *testing.T) {
	v := titleSession("<p>fix the  login\nbutton</p>", "SYS: tool loaded").viewWith(t, titleRules{})
	if len(v.Turns) != 2 {
		t.Fatalf("turns = %d", len(v.Turns))
	}
	eq(t, "turn 1 title", v.Turns[0].Title, "fix the login button")
	eq(t, "turn 1 prompt is whole", v.Turns[0].Prompt, "<p>fix the  login\nbutton</p>")
	eq(t, "turn 2 title", v.Turns[1].Title, "(system) tool loaded")
	eq(t, "turn 2 prompt is whole", v.Turns[1].Prompt, "SYS: tool loaded")
}

// turn 0 (calls before any prompt) is titled by its own text
func TestView_TurnZeroTitle(t *testing.T) {
	x := titleSession("<p>fix</p>")
	x.f.call(vcallSpec{id: "pre", from: 0.1, to: 0.2, msgs: []string{"u:<r>only a reminder</r>", "a:hello"}})
	// the source lists exchanges in start order
	ex := x.f.exchanges
	x.f.exchanges = append([]store.ExchangeRow{ex[len(ex)-1]}, ex[:len(ex)-1]...)
	v := x.viewWith(t, titleRules{})
	eq(t, "turn 0 n", v.Turns[0].N, 0)
	eq(t, "turn 0 title", v.Turns[0].Title, "Before the first prompt")
}

func TestView_TurnTitleWithoutATitler(t *testing.T) {
	v := titleSession("<p>fix the  login\nbutton</p>").view(t, true)
	eq(t, "title is the collapsed prompt", v.Turns[0].Title, "<p>fix the login button</p>")
	long := strings.Repeat("word ", 60)
	v = titleSession(long).view(t, true)
	if n := len([]rune(v.Turns[0].Title)); n != 120 {
		t.Errorf("title runes = %d, want 120", n)
	}
}

func TestView_SessionTitleSkipsSyntheticTurns(t *testing.T) {
	v := titleSession("SYS: tool loaded", "SYS: skill demo-skill", "<p>fix the login button</p>").viewWith(t, titleRules{})
	eq(t, "title", v.Overview.Title+" ("+v.Overview.TitleSource+")", "fix the login button (prompt)")
}

func TestView_SessionTitleOfOnlySyntheticTurnsIsTheFirst(t *testing.T) {
	v := titleSession("SYS: tool loaded", "SYS: skill demo-skill").viewWith(t, titleRules{})
	eq(t, "title", v.Overview.Title+" ("+v.Overview.TitleSource+")", "(system) tool loaded (prompt)")
}

// the list title (digest) and the overview title agree for a session whose first prompts
// are synthetic, and for one whose prompts all are
func TestDigestTitleAgreesWithTheOverview(t *testing.T) {
	for _, msgs := range [][]string{
		{"SYS: tool loaded", "SYS: skill demo-skill", "<p>fix the login button</p>", "later prompt"},
		{"SYS: tool loaded", "SYS: skill demo-skill"},
		{"<p>fix the login button</p>", "SYS: tool loaded"},
	} {
		x := titleSession(msgs...)
		v := x.viewWith(t, titleRules{})
		d, err := timeline.SessionDigest(x.f, store.SessionRow{PrincipalID: "p", Client: "c", SessionKey: "k"}, titleRules{})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, strings.Join(msgs, "|"), d.Title, v.Overview.Title)
		if d.Title == "" {
			t.Errorf("empty digest title for %v", msgs)
		}
	}
}

// without a PromptTitler the digest title is the first prompt as before
func TestDigestTitleWithoutATitler(t *testing.T) {
	x := titleSession("SYS: tool loaded", "<p>fix</p>")
	d, err := timeline.SessionDigest(x.f, store.SessionRow{PrincipalID: "p", Client: "c", SessionKey: "k"}, viewRules{})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "title", d.Title, "SYS: tool loaded")
}
