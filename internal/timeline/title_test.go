package timeline_test

import (
	"encoding/json"
	"fmt"
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
		// a message's text blocks arrive joined with a newline: text after a synthetic
		// first line is a later block, and when it is typed it gives the title (046 P2-b)
		if first, rest, cut := strings.Cut(s, "\n"); cut && strings.TrimSpace(rest) != "" {
			if t, syn := (titleRules{}).PromptTitle(rest); !syn {
				return t, false
			}
			return "(system) " + first, true
		}
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

// 046 P2-b: a first message whose synthetic part is followed by typed text in a later block
// titles the session with the typed text, in the list and in the overview alike
func TestSessionTitleOfAMergedSyntheticAndTypedMessage(t *testing.T) {
	for _, msgs := range [][]string{
		{"SYS: client md context\n<p>fix the login button</p>"},
		{"SYS: tool loaded", "SYS: client md context\n<p>fix the login button</p>", "later prompt"},
		{"SYS: client md context\n<r>reminder</r><p>fix the login button</p>"},
	} {
		d, o := digestAndOverview(t, titleSession(msgs...))
		eq(t, strings.Join(msgs, "|")+" digest", d, "fix the login button")
		eq(t, strings.Join(msgs, "|")+" overview", o, "fix the login button")
	}
	d, o := digestAndOverview(t, titleSession("SYS: first\nSYS: second"))
	eq(t, "two synthetic parts: digest", d, "(system) first")
	eq(t, "two synthetic parts: overview", o, "(system) first")
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

// subAgentTitleSession is a main thread of msgs, one per call, in which the call after the
// first two asks a sub-agent (tool "Task") to investigate the cache; the sub-agent's own
// call carries that prompt, which is not synthetic.
func subAgentTitleSession(msgs ...string) *fixture {
	f := newFake()
	var seen []string
	for i, m := range msgs {
		seen = append(seen, "u:"+m)
		spec := vcallSpec{id: "c" + string(rune('a'+i)), from: float64(i*4 + 1), to: float64(i*4 + 2), msgs: append(append([]string{}, seen...), "a:ok")}
		if i == 1 {
			spec.tools = [][3]string{{"t1", "Task", `{"prompt":"investigate the cache"}`}}
		}
		f.call(spec)
		if i == 1 {
			f.call(vcallSpec{id: "sub", from: float64(i*4) + 2.2, to: float64(i*4) + 2.5, msgs: []string{"u:investigate the cache", "a:found it"}})
		}
		seen = append(seen, "a:ok")
	}
	return &fixture{f: f}
}

func digestAndOverview(t *testing.T, x *fixture) (digest, overview string) {
	t.Helper()
	v := x.viewWith(t, titleRules{})
	d, err := timeline.SessionDigest(x.f, store.SessionRow{PrincipalID: "p", Client: "c", SessionKey: "k"}, titleRules{})
	if err != nil {
		t.Fatal(err)
	}
	return d.Title, v.Overview.Title
}

// 046 P2-a: a sub-agent's prompt (written by the model, not synthetic) never becomes the
// list title; the digest reads the same main-thread prompts the overview does
func TestDigestTitleIgnoresSubAgentPrompts(t *testing.T) {
	d, o := digestAndOverview(t, subAgentTitleSession("SYS: tool loaded", "SYS: skill demo-skill", "<p>fix the login button</p>"))
	eq(t, "digest title", d, "fix the login button")
	eq(t, "overview title", o, "fix the login button")
}

func TestDigestTitleOfOnlySyntheticMainPromptsIgnoresSubAgentPrompts(t *testing.T) {
	d, o := digestAndOverview(t, subAgentTitleSession("SYS: tool loaded", "SYS: skill demo-skill"))
	eq(t, "digest title", d, "(system) tool loaded")
	eq(t, "overview title", o, "(system) tool loaded")
}

// mediaMsg adds a user message of an image block, after a text block when text is not empty.
func mediaMsg(f *fake, id, text string, pos int) {
	var blocks []map[string]string
	if text != "" {
		raw, _ := json.Marshal(map[string]string{"type": "text", "text": text})
		blocks = append(blocks, map[string]string{"hash": f.put(string(raw)), "type": "text"})
	}
	blocks = append(blocks, map[string]string{"hash": "", "type": "media"})
	payload, _ := json.Marshal(map[string]any{"role": "user", "blocks": blocks})
	f.add(id, store.EventRow{Kind: "message", ContentHash: hashOf(fmt.Sprintf("user|media|%d|%s", pos, text)), Payload: payload})
}

// mediaSession is titleSession where "IMG" is an image-only message and "IMG: x" is
// the text x with an image.
func mediaSession(msgs ...string) *fixture {
	f := newFake()
	for i := range msgs {
		id := "c" + string(rune('a'+i))
		x := f.exchange(id, vat(float64(i*2+1)), "ok")
		x.EndedAt = vat(float64(i*2 + 2))
		f.request(id, "m1")
		for j, p := range msgs[:i+1] {
			if p == "IMG" {
				mediaMsg(f, id, "", j)
			} else if text, ok := strings.CutPrefix(p, "IMG: "); ok {
				mediaMsg(f, id, text, j)
			} else {
				f.msg(id, "user", p)
			}
			f.msg(id, "assistant", "ok")
		}
		f.usage(id, false, n(10), n(5), n(0), n(0))
	}
	return &fixture{f: f}
}

// 046 P3-1: a message of only an image starts a turn titled "[media]", but it is not a
// typed prompt: the session title, in the overview and in the list, is the first typed one.
func TestSessionTitleSkipsAMediaOnlyPrompt(t *testing.T) {
	x := mediaSession("IMG", "<p>fix the button</p>")
	v := x.viewWith(t, titleRules{})
	if len(v.Turns) != 2 {
		t.Fatalf("turns = %d", len(v.Turns))
	}
	eq(t, "rail title of the media turn", v.Turns[0].Title, "[media]")
	eq(t, "overview title", v.Overview.Title+" ("+v.Overview.TitleSource+")", "fix the button (prompt)")
	d, err := timeline.SessionDigest(x.f, store.SessionRow{PrincipalID: "p", Client: "c", SessionKey: "k"}, titleRules{})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "digest title", d.Title, "fix the button")
}

func TestSessionTitleKeepsATextAndImagePrompt(t *testing.T) {
	v := mediaSession("IMG: look at this", "later prompt").viewWith(t, titleRules{})
	eq(t, "title", v.Overview.Title, "look at this [media]")
}

// no typed prompt at all: every prompt is synthetic, and D4 takes the first one's title.
// (A session of image-only messages alone starts no turn, as no call of it is a main
// call with text, so it reads "No prompt" as before.)
func TestSessionTitleOfOnlySyntheticAndMediaPromptsIsTheFirst(t *testing.T) {
	v := mediaSession("IMG", "SYS: tool loaded").viewWith(t, titleRules{})
	eq(t, "title", v.Overview.Title+" ("+v.Overview.TitleSource+")", "[media] (prompt)")
}
