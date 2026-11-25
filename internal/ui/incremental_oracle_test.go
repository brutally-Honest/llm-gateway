package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// The oracle of spec 040 (AC1): a session's store inputs are applied one at a time, and
// after every one the session is built through the incremental path (from the previous
// build) and through the full path; the entries, call facts, timeline facts, exchange map,
// row ids and the session view built from each must be byte-for-byte equal.

const (
	oracleSession = "s1"
	oracleClient  = "oracle-client"
)

// oracleRules is the test's client: "Agent" spawns sub-agents, and an effect names a tool
// call whose input contains its path or host.
type oracleRules struct{ testRules }

func (oracleRules) IsAgentTool(n string) bool { return n == "Agent" }
func (oracleRules) NamesTarget(_ string, input []byte, _, target string) bool {
	return strings.Contains(string(input), target)
}
func (oracleRules) HarnessHost(host string) bool { return host == "harness.example" }

// oTool is one tool use (id, name, input) and what it returned (out).
type oTool struct {
	id, name, input, out string
	isErr                bool
}

// oItem is one message of a conversation: its text, the tool calls an assistant message
// makes, or the tool results a user message returns.
type oItem struct {
	role    string
	text    string
	uses    []oTool
	results []oTool
}

func (it oItem) key() string {
	k := it.role + "|" + it.text
	for _, u := range it.uses {
		k += "|use:" + u.id
	}
	for _, r := range it.results {
		k += "|res:" + r.id
	}
	return k
}

// oCall is one model call: its exchange row (id, start, length) and what its request
// carried (items, the thread's history) and its reply.
type oCall struct {
	id            string
	startMs, dur  int
	items         []oItem
	reply         oItem
	input, output int
}

// oConv is one conversation thread as the client keeps it: each call resends the history.
type oConv struct {
	items   []oItem
	agentID string // the sub-agent this thread is, when its tools' hooks name one
}

func (c *oConv) user(text string) {
	c.items = append(c.items, oItem{role: "user", text: text})
}

// call makes the call with the thread's history so far and appends its reply.
func (c *oConv) call(id string, startMs int, reply oItem) oCall {
	cl := oCall{id: id, startMs: startMs, dur: 3000, items: append([]oItem(nil), c.items...), reply: reply,
		input: 100 + len(c.items)*10, output: 20}
	c.items = append(c.items, reply)
	return cl
}

// result appends the user message returning the last reply's tool calls.
func (c *oConv) result() {
	last := c.items[len(c.items)-1]
	if len(last.uses) == 0 {
		return
	}
	var res []oTool
	for _, u := range last.uses {
		u.out = "output of " + u.id
		res = append(res, u)
	}
	c.items = append(c.items, oItem{role: "user", results: res})
}

func oReply(id, tool string) oItem {
	tu := oTool{id: "tu_" + id, name: tool}
	switch tool {
	case "Write":
		tu.input = fmt.Sprintf(`{"content":"x","file_path":"/w/%s.txt"}`, id)
	case "Edit":
		tu.input = fmt.Sprintf(`{"file_path":"/w/%s.go","new_string":"b","old_string":"a"}`, id)
	default:
		tu.input = fmt.Sprintf(`{"command":"make %s"}`, id)
	}
	return oItem{role: "assistant", text: "doing " + id, uses: []oTool{tu}}
}

func oText(id string) oItem { return oItem{role: "assistant", text: "done " + id} }

func oHash(s string) string { return sha("o-" + s) }

// oEvents is the canonical events and content of one parsed request: the request event,
// the history's messages and tool events, the reply and usage.
func oEvents(c oCall, status core.ParseStatus) ([]core.StoredEvent, []core.Content) {
	var evs []core.StoredEvent
	var contents []core.Content
	seen := map[string]bool{}
	put := func(hash string, b []byte) {
		if !seen[hash] {
			seen[hash] = true
			contents = append(contents, core.Content{Hash: hash, Bytes: b})
		}
	}
	add := func(e core.StoredEvent) {
		e.Seq = len(evs)
		evs = append(evs, e)
	}
	add(core.StoredEvent{Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)})
	item := func(it oItem, src core.Source) {
		partial := status == core.ParsePartial && src == core.SourceResponse
		type block struct {
			Hash string `json:"hash"`
			Type string `json:"type"`
		}
		var blocks []block
		if it.text != "" {
			raw, _ := json.Marshal(map[string]string{"type": "text", "text": it.text})
			bh := oHash("b-" + it.role + it.text)
			put(bh, raw)
			blocks = append(blocks, block{bh, "text"})
		}
		for _, u := range it.uses {
			blocks = append(blocks, block{oHash("use-" + u.id), "tool_block"})
		}
		for _, r := range it.results {
			blocks = append(blocks, block{oHash("res-" + r.id), "tool_result"})
		}
		mh := oHash("m-" + it.key())
		put(mh, []byte(mh))
		p, _ := json.Marshal(map[string]any{"role": it.role, "blocks": blocks})
		add(core.StoredEvent{Kind: core.KindMessage, Source: src, Partial: partial, ContentHash: mh, Payload: p})
		for _, u := range it.uses {
			ih := oHash("in-" + u.input)
			put(ih, []byte(u.input))
			p, _ := json.Marshal(map[string]string{"name": u.name})
			add(core.StoredEvent{Kind: core.KindToolCall, Source: src, Partial: partial, ContentHash: ih, ToolCallID: u.id, Payload: p})
		}
		for _, r := range it.results {
			oh := oHash("out-" + r.out)
			raw, _ := json.Marshal(r.out)
			put(oh, raw)
			p, _ := json.Marshal(map[string]bool{"is_error": r.isErr})
			add(core.StoredEvent{Kind: core.KindToolResult, Source: src, ContentHash: oh, ToolCallID: r.id, Payload: p})
		}
	}
	for _, it := range c.items {
		item(it, core.SourceRequestHistory)
	}
	item(c.reply, core.SourceResponse)
	add(core.StoredEvent{Kind: core.KindUsage,
		Payload: []byte(fmt.Sprintf(`{"input_tokens":%d,"output_tokens":%d}`, c.input, c.output))})
	return evs, contents
}

// oracleStep is one store input of a scenario.
type oracleStep struct {
	desc string
	run  func(ctx context.Context, s *store.Store) error
}

// oWorld builds steps; seq numbers the hook events as the gateway would.
type oWorld struct{ seq int64 }

func oAt(ms int) time.Time { return baseTime.Add(time.Duration(ms) * time.Millisecond) }

func (w *oWorld) save(c oCall) func(context.Context, *store.Store) error {
	return func(ctx context.Context, s *store.Store) error {
		ex := &core.Exchange{RequestID: c.id, PrincipalID: core.PrincipalLocal, Protocol: "test",
			Client: oracleClient, Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/messages", Status: 200,
			SessionKey: oracleSession, Start: oAt(c.startMs)}
		ex.End = ex.Start.Add(time.Duration(c.dur) * time.Millisecond)
		return s.SaveExchange(ctx, ex)
	}
}

func oParse(ctx context.Context, s *store.Store, c oCall, status core.ParseStatus) error {
	var evs []core.StoredEvent
	var contents []core.Content
	if status == core.ParseOK || status == core.ParsePartial {
		evs, contents = oEvents(c, status)
	}
	return s.SaveParse(ctx, c.id, core.PrincipalLocal, status, evs, contents)
}

// row stores the exchange row alone, before its parse.
func (w *oWorld) row(c oCall) oracleStep {
	return oracleStep{fmt.Sprintf("row %s at %dms", c.id, c.startMs), w.save(c)}
}

// parse lands the parse of a stored row.
func (w *oWorld) parse(c oCall, status core.ParseStatus) oracleStep {
	return oracleStep{fmt.Sprintf("parse %s (%s)", c.id, status), func(ctx context.Context, s *store.Store) error {
		return oParse(ctx, s, c, status)
	}}
}

// call stores the row and its parse together.
func (w *oWorld) call(c oCall, status core.ParseStatus) oracleStep {
	save := w.save(c)
	return oracleStep{fmt.Sprintf("call %s at %dms (%s)", c.id, c.startMs, status), func(ctx context.Context, s *store.Store) error {
		if err := save(ctx, s); err != nil {
			return err
		}
		return oParse(ctx, s, c, status)
	}}
}

// hook stores one hook event received atMs; agent is the sub-agent id it carries, if any.
func (w *oWorld) hook(atMs int, phase core.HarnessPhase, tool oTool, agent string) oracleStep {
	return w.hookRaw(atMs, phase, tool.id, tool.name, tool.input, agent, "")
}

func (w *oWorld) hookRaw(atMs int, phase core.HarnessPhase, id, name, input, agent, payload string) oracleStep {
	w.seq++
	seq := w.seq
	if payload == "" {
		payload = fmt.Sprintf(`{"n":%d}`, seq)
	}
	ev := core.HarnessEvent{Phase: phase, SessionKey: oracleSession, ToolCallID: id, ToolName: name,
		WorkDir: "/w", AgentID: agent}
	if agent != "" {
		ev.AgentType = "general"
	}
	if input != "" {
		ev.ToolInput = []byte(input)
	}
	return oracleStep{fmt.Sprintf("hook %s %s at %dms", phase, id, atMs), func(ctx context.Context, s *store.Store) error {
		return s.SaveHook(ctx, &store.HookRecord{PrincipalID: core.PrincipalLocal, Client: oracleClient,
			Seq: seq, ReceivedAt: oAt(atMs), Payload: []byte(payload), Event: ev})
	}}
}

// file stores a file change found in the checkpoint window fromMs..toMs.
func (w *oWorld) file(path string, fromMs, toMs int) oracleStep {
	e := core.Effect{Kind: core.EffectFileChange, At: oAt(toMs), Principal: core.PrincipalLocal, Client: oracleClient,
		SessionKey: oracleSession, Root: "/w", Path: path, Change: core.FileModified, Mode: "git",
		WindowFrom: oAt(fromMs), WindowTo: oAt(toMs)}
	return oracleStep{fmt.Sprintf("file change %s window %d..%dms", path, fromMs, toMs), func(ctx context.Context, s *store.Store) error {
		return s.SaveEffects(ctx, []core.Effect{e}, nil)
	}}
}

// net stores a network connection that began atMs and ended endMs.
func (w *oWorld) net(host string, atMs, endMs int) oracleStep {
	e := core.Effect{Kind: core.EffectNetConn, At: oAt(atMs), EndedAt: oAt(endMs), Principal: core.PrincipalLocal,
		Client: oracleClient, SessionKey: oracleSession, Host: host, Port: 443, Via: "connect", Outcome: core.NetOK,
		BytesUp: 10, BytesDown: 20}
	return oracleStep{fmt.Sprintf("net %s %d..%dms", host, atMs, endMs), func(ctx context.Context, s *store.Store) error {
		return s.SaveEffects(ctx, []core.Effect{e}, nil)
	}}
}

// oracleDiff describes the first difference between two JSON encodings; "" when equal.
func oracleDiff(what string, a, b any) string {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return fmt.Sprintf("%s: marshal: %v / %v", what, errA, errB)
	}
	if bytes.Equal(ja, jb) {
		return ""
	}
	i := 0
	for i < len(ja) && i < len(jb) && ja[i] == jb[i] {
		i++
	}
	around := func(j []byte) string {
		lo, hi := max(i-200, 0), min(i+200, len(j))
		return string(j[lo:hi])
	}
	return fmt.Sprintf("%s differ at byte %d (incremental %d bytes, full %d bytes)\n  incremental: ...%s...\n  full:        ...%s...",
		what, i, len(ja), len(jb), around(ja), around(jb))
}

// entriesDiff is oracleDiff for the entries, naming the first differing entry.
func entriesDiff(inc, full []timeline.Entry) string {
	for i := 0; i < len(inc) && i < len(full); i++ {
		if d := oracleDiff(fmt.Sprintf("entries[%d] (kind %s, request %s)", i, full[i].Kind, full[i].RequestID), inc[i], full[i]); d != "" {
			return d
		}
	}
	if len(inc) != len(full) {
		return fmt.Sprintf("entries: incremental has %d, full has %d", len(inc), len(full))
	}
	return ""
}

// checkOracle applies steps to a fresh store one at a time and, after each, compares the
// incremental build (from the previous build) with the full build and the views built
// from them.
func checkOracle(t *testing.T, steps []oracleStep) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })

	ctx := t.Context()
	c := r.WithContext(ctx)
	who := sessIdent{core.PrincipalLocal, oracleClient, oracleSession}
	rules := ClientRules(oracleRules{})
	vd := viewDeps{rules: map[string]ClientRules{oracleClient: rules}, skew: 50 * time.Millisecond}
	counts := map[string]int{}
	var prev *builtSession
	for i, st := range steps {
		fail := func(format string, args ...any) {
			t.Helper()
			t.Fatalf("step %d (%s): %s", i, st.desc, fmt.Sprintf(format, args...))
		}
		if err := st.run(ctx, s); err != nil {
			fail("store write: %v", err)
		}
		inc, info, err := buildSessionInc(c, who, rules, entryCut, prev)
		if err != nil {
			fail("incremental build: %v", err)
		}
		full, err := buildSession(c, who, rules, entryCut)
		if err != nil {
			fail("full build: %v", err)
		}
		if info.Incremental {
			counts["incremental"]++
		} else {
			counts["full:"+info.Reason]++
		}
		for _, d := range []string{
			entriesDiff(inc.entries, full.entries),
			oracleDiff("facts", inc.facts, full.facts),
			oracleDiff("calls", inc.calls, full.calls),
			oracleDiff("byID", inc.byID, full.byID),
			oracleDiff("rowID", inc.rowID, full.rowID),
			oracleDiff("rows", inc.rows, full.rows),
		} {
			if d != "" {
				fail("%s", d)
			}
		}
		hooks, err := c.HookEvents(who.principal, who.client, who.name)
		if err != nil {
			fail("hook events: %v", err)
		}
		vInc, err := vd.sessionView(c, who, inc, hooks, true, "v")
		if err != nil {
			fail("view of incremental build: %v", err)
		}
		vFull, err := vd.sessionView(c, who, full, hooks, true, "v")
		if err != nil {
			fail("view of full build: %v", err)
		}
		if d := oracleDiff("view", vInc, vFull); d != "" {
			fail("%s", d)
		}
		prev = &inc
	}
	t.Logf("%d steps; builds: %v", len(steps), counts)
	// The comparison above proves nothing about the incremental path when every update fell
	// back to a full build, so require that it ran. Only the first build (no_state) and a
	// few large_delta builds on tiny states are expected to be full: at least 80% of the steps
	// of a scenario of 10+ steps, and half of a shorter one, must be incremental.
	n := len(steps)
	if inc := counts["incremental"]; (n >= 10 && 5*inc < 4*n) || (n < 10 && 2*inc < n) {
		t.Errorf("incremental path ran in only %d of %d steps; builds: %v", inc, n, counts)
	}
}

// oracleLinear: 12 calls in one thread, each with a tool call whose result is in the next
// call, and pre and post hooks after each.
func oracleLinear() []oracleStep {
	w := &oWorld{}
	c := &oConv{}
	c.user("build the thing")
	var steps []oracleStep
	for i := 1; i <= 12; i++ {
		id := fmt.Sprintf("r%02d", i)
		cl := c.call(id, i*10000, oReply(id, "Bash"))
		steps = append(steps, w.call(cl, core.ParseOK))
		c.result()
		steps = append(steps,
			w.hook(i*10000+2000, core.PhasePreTool, cl.reply.uses[0], ""),
			w.hook(i*10000+5000, core.PhasePostTool, cl.reply.uses[0], ""))
	}
	return steps
}

// oracleOutOfOrder: calls committed in an order unlike their start order, with a call that
// opens a new thread and renumbers the later ones, and one that changes a later call's
// best match.
func oracleOutOfOrder() []oracleStep {
	w := &oWorld{}
	main := &oConv{}
	main.user("fix the bug")
	calls := map[string]oCall{}
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("c%d", i)
		calls[id] = main.call(id, i*10000, oReply(id, "Bash"))
		main.result()
		if i == 2 {
			// A retry of c2's request from the same history, started just after it.
			calls["L"] = (&oConv{items: calls["c2"].items}).call("L", 12000, oReply("L", "Edit"))
		}
	}
	// A second thread in the middle of the first one's time.
	other := &oConv{}
	other.user("write the docs")
	calls["t1"] = other.call("t1", 35000, oReply("t1", "Write"))
	other.result()
	calls["t2"] = other.call("t2", 45000, oText("t2"))
	// A thread that starts before t1 and is committed last: t1's thread number moves.
	third := &oConv{}
	third.user("check the logs")
	calls["N"] = third.call("N", 25000, oText("N"))
	// D diverges from c2's history; E continues D, so E's best match is D when D is stored
	// and an earlier call when it is not.
	div := &oConv{items: calls["c2"].items}
	div.items = append(div.items, calls["c2"].reply)
	div.user("actually, take another approach")
	calls["D"] = div.call("D", 65000, oText("D"))
	div.user("and then test it")
	calls["E"] = div.call("E", 70000, oText("E"))

	order := []string{"c1", "c2", "c3", "c5", "c6", "t1", "t2", "E", "c4", "L", "N", "D"}
	var steps []oracleStep
	for _, id := range order {
		cl := calls[id]
		steps = append(steps, w.call(cl, core.ParseOK))
		if len(cl.reply.uses) > 0 {
			steps = append(steps, w.hook(cl.startMs+1500, core.PhasePreTool, cl.reply.uses[0], ""))
		}
	}
	return steps
}

// oracleParseLate: rows are stored first and their parses land later, out of order; one
// parse failed, one was skipped, one is partial.
func oracleParseLate() []oracleStep {
	w := &oWorld{}
	c := &oConv{}
	c.user("start")
	var calls []oCall
	var steps []oracleStep
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("p%d", i)
		cl := c.call(id, i*10000, oReply(id, "Bash"))
		c.result()
		calls = append(calls, cl)
		steps = append(steps, w.row(cl))
	}
	steps = append(steps, w.hook(15000, core.PhasePreTool, calls[0].reply.uses[0], ""))
	status := map[int]core.ParseStatus{5: core.ParseFailed, 4: core.ParseSkipped, 2: core.ParsePartial}
	for _, i := range []int{3, 0, 5, 1, 4, 2} {
		st, ok := status[i]
		if !ok {
			st = core.ParseOK
		}
		steps = append(steps, w.parse(calls[i], st))
	}
	return steps
}

// oracleLateHooks: hooks received before calls already stored, a post hook for a call made
// five calls back, a hook with no tool call, and a permission-denied phase.
func oracleLateHooks() []oracleStep {
	w := &oWorld{}
	c := &oConv{}
	c.user("go")
	var calls []oCall
	var steps []oracleStep
	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("h%02d", i)
		cl := c.call(id, i*10000, oReply(id, "Bash"))
		c.result()
		calls = append(calls, cl)
		steps = append(steps, w.call(cl, core.ParseOK))
	}
	tool := func(i int) oTool { return calls[i-1].reply.uses[0] }
	steps = append(steps,
		w.hook(31000, core.PhasePreTool, tool(3), ""),                                       // before the latest start
		w.hook(95000, core.PhasePostTool, tool(5), ""),                                      // five calls back
		w.hook(96000, core.PhasePostTool, tool(7), ""),                                      // a hook
		w.hookRaw(50500, core.PhasePreTool, "tu_ghost", "Bash", `{"command":"ls"}`, "", ""), // no such tool call
		w.hook(70500, core.PhasePermissionDenied, tool(7), ""),
		w.hook(20500, core.PhasePostToolFailed, tool(2), ""),
		w.hook(40500, core.PhasePermissionRequested, tool(4), ""),
		w.hookRaw(12000, core.PhasePreTool, tool(1).id, tool(1).name, `{"command":"other"}`, "", ""), // input differs
		w.hookRaw(65000, core.PhasePrompt, "", "", "", "", ""),
		w.hookRaw(65000, core.PhasePrompt, "", "", "", "", `{"n":"dup"}`),
		w.hookRaw(65200, core.PhasePrompt, "", "", "", "", `{"n":"dup"}`), // a repeated payload
		w.hook(105000, core.PhasePostTool, tool(10), ""),
	)
	return steps
}

// oracleSubAgent: an agent tool call in the main thread whose prompt starts a second thread
// (sub-agent by prompt), a second agent whose thread only hooks identify, and the
// sub-agents' hooks landing after their calls.
func oracleSubAgent() []oracleStep {
	w := &oWorld{}
	const prompt1, prompt2 = "Find auth bugs", "Summarize the repo"
	agentReply := func(id, prompt string) oItem {
		in, _ := json.Marshal(map[string]string{"description": "d", "prompt": prompt, "subagent_type": "general"})
		return oItem{role: "assistant", text: "delegating " + id,
			uses: []oTool{{id: "tu_" + id, name: "Agent", input: string(in)}}}
	}
	main := &oConv{}
	main.user("review the project")
	m1 := main.call("m1", 10000, agentReply("m1", prompt1))
	sub1 := &oConv{agentID: "ag1"}
	sub1.user(prompt1)
	s1 := sub1.call("s1", 20000, oReply("s1", "Bash"))
	sub1.result()
	s2 := sub1.call("s2", 30000, oReply("s2", "Edit"))
	sub1.result()
	s3 := sub1.call("s3", 40000, oText("s3"))
	main.result()
	m2 := main.call("m2", 50000, agentReply("m2", prompt2))
	sub2 := &oConv{agentID: "ag2"}
	sub2.user("Read the README and report") // not the agent tool's prompt: only hooks name it
	u1 := sub2.call("u1", 60000, oReply("u1", "Bash"))
	sub2.result()
	u2 := sub2.call("u2", 70000, oText("u2"))
	main.result()
	m3 := main.call("m3", 80000, oText("m3"))

	return []oracleStep{
		w.call(m1, core.ParseOK),
		w.hook(10500, core.PhasePreTool, m1.reply.uses[0], ""),
		w.call(s1, core.ParseOK),
		w.call(s2, core.ParseOK),
		w.hook(20500, core.PhasePreTool, s1.reply.uses[0], "ag1"), // after its calls
		w.hook(21500, core.PhasePostTool, s1.reply.uses[0], "ag1"),
		w.hook(30500, core.PhasePreTool, s2.reply.uses[0], "ag1"),
		w.call(s3, core.ParseOK),
		w.call(m2, core.ParseOK),
		w.call(u1, core.ParseOK),
		w.call(u2, core.ParseOK),
		w.hook(60500, core.PhasePreTool, u1.reply.uses[0], "ag2"), // flips thread 3 from main to sub
		w.hook(61500, core.PhasePostTool, u1.reply.uses[0], "ag2"),
		w.call(m3, core.ParseOK),
		w.hook(42000, core.PhasePostTool, m1.reply.uses[0], ""),
		w.hook(82000, core.PhasePostTool, m2.reply.uses[0], ""),
	}
}

// oracleLateEffects: file changes and network connections landing after later calls, and
// before the hook that names their tool.
func oracleLateEffects() []oracleStep {
	w := &oWorld{}
	c := &oConv{}
	c.user("write the files")
	var calls []oCall
	var steps []oracleStep
	for i := 1; i <= 8; i++ {
		id := fmt.Sprintf("e%d", i)
		cl := c.call(id, i*10000, oReply(id, "Write"))
		c.result()
		calls = append(calls, cl)
		steps = append(steps, w.call(cl, core.ParseOK))
		if i == 1 {
			steps = append(steps, w.hook(11000, core.PhasePreTool, cl.reply.uses[0], ""))
		}
	}
	steps = append(steps,
		w.file("/w/e2.txt", 20000, 28000), // lands after call 8
		w.net("registry.example", 33000, 34000),
		w.file("/w/e5.txt", 50000, 58000), // before the hook that names its tool
		w.hook(50500, core.PhasePreTool, calls[4].reply.uses[0], ""),
		w.hook(51500, core.PhasePostTool, calls[4].reply.uses[0], ""),
		w.file("/w/e3.txt", 0, 100000), // a window over everything: several candidates
		w.hook(20500, core.PhasePreTool, calls[1].reply.uses[0], ""),
		w.hook(21500, core.PhasePostTool, calls[1].reply.uses[0], ""),
		w.net("api.example", 60000, 61500),
		w.hook(60500, core.PhasePreTool, calls[5].reply.uses[0], ""),
		w.file("/w/e7.txt", 69000, 78000),
		w.hook(70500, core.PhasePreTool, calls[6].reply.uses[0], ""),
	)
	return steps
}

// oracleHooksOnly: hooks and no exchange at all, then an exchange naming their tool calls.
func oracleHooksOnly() []oracleStep {
	w := &oWorld{}
	c := &oConv{}
	c.user("go")
	cl := c.call("x1", 20000, oReply("x1", "Bash"))
	return []oracleStep{
		w.hookRaw(1000, core.PhasePrompt, "", "", "", "", ""),
		w.hook(2000, core.PhasePreTool, cl.reply.uses[0], ""),
		w.hook(3000, core.PhasePostTool, cl.reply.uses[0], ""),
		w.hookRaw(4000, core.PhasePreTool, "tu_other", "Bash", `{"command":"ls"}`, "", ""),
		w.call(cl, core.ParseOK),
	}
}

// oracleRandom is a mixed session of about 25 inputs drawn from every kind, deterministic
// in seed: calls with start times that sometimes go back, rows before their parses, forks
// and new threads, sub-agents, and hooks and effects at random times.
func oracleRandom(seed uint64) []oracleStep {
	rng := rand.New(rand.NewPCG(seed, seed*7919+1))
	w := &oWorld{}
	var (
		steps   []oracleStep
		convs   = []*oConv{{}}
		pending []oCall
		tools   []oTool
		owner   = map[string]*oConv{}
		pos     int
		nCall   int
		agentFx []string // prompts of agent tool calls whose thread is not started yet
		phases  = []core.HarnessPhase{core.PhasePreTool, core.PhasePostTool, core.PhasePostToolFailed,
			core.PhasePermissionDenied, core.PhasePermissionRequested}
	)
	convs[0].user("first task")
	for len(steps) < 25 {
		switch r := rng.IntN(100); {
		case r < 50: // a call
			var c *oConv
			switch p := rng.IntN(100); {
			case p < 65:
				c = convs[rng.IntN(len(convs))]
			case p < 80: // a fork: a prefix of a thread, then a different prompt
				src := convs[rng.IntN(len(convs))]
				c = &oConv{items: append([]oItem(nil), src.items[:rng.IntN(len(src.items)+1)]...)}
				c.user(fmt.Sprintf("fork %d", nCall))
				convs = append(convs, c)
			default: // a new thread, the agent's when an agent call is waiting for one
				c = &oConv{}
				if len(agentFx) > 0 {
					c.user(agentFx[0])
					c.agentID = fmt.Sprintf("ag%d", nCall)
					agentFx = agentFx[1:]
				} else {
					c.user(fmt.Sprintf("new task %d", nCall))
				}
				convs = append(convs, c)
			}
			if n := len(c.items); n > 0 && len(c.items[n-1].uses) > 0 && rng.IntN(10) > 0 {
				c.result()
			}
			if rng.IntN(4) == 0 {
				c.user(fmt.Sprintf("next prompt %d", nCall))
			}
			nCall++
			id := fmt.Sprintf("q%02d", nCall)
			pos = max(pos+rng.IntN(30000)-8000, 0)
			var reply oItem
			switch p := rng.IntN(100); {
			case p < 55:
				reply = oReply(id, "Bash")
			case p < 65:
				reply = oReply(id, "Edit")
			case p < 72:
				prompt := fmt.Sprintf("subtask %d", nCall)
				in, _ := json.Marshal(map[string]string{"description": "d", "prompt": prompt})
				reply = oItem{role: "assistant", text: "delegating " + id, uses: []oTool{{id: "tu_" + id, name: "Agent", input: string(in)}}}
				agentFx = append(agentFx, prompt)
			default:
				reply = oText(id)
			}
			cl := c.call(id, pos, reply)
			for _, u := range reply.uses {
				tools = append(tools, u)
				owner[u.id] = c
			}
			switch p := rng.IntN(100); {
			case p < 50:
				steps = append(steps, w.call(cl, core.ParseOK))
			case p < 75:
				steps = append(steps, w.row(cl))
				pending = append(pending, cl)
			case p < 83:
				steps = append(steps, w.call(cl, core.ParseFailed))
			case p < 91:
				steps = append(steps, w.call(cl, core.ParseSkipped))
			default:
				steps = append(steps, w.call(cl, core.ParsePartial))
			}
		case r < 62: // a late parse
			if len(pending) == 0 {
				continue
			}
			i := rng.IntN(len(pending))
			status := core.ParseOK
			switch rng.IntN(10) {
			case 0:
				status = core.ParseFailed
			case 1:
				status = core.ParsePartial
			}
			steps = append(steps, w.parse(pending[i], status))
			pending = append(pending[:i], pending[i+1:]...)
		case r < 85: // a hook, at any time up to a little after the latest call
			if len(tools) == 0 {
				continue
			}
			tool := tools[rng.IntN(len(tools))]
			if rng.IntN(12) == 0 {
				tool = oTool{id: "tu_ghost", name: "Bash", input: `{"command":"ghost"}`}
			}
			if rng.IntN(8) == 0 {
				tool.input = `{"command":"changed"}`
			}
			agent := ""
			if o := owner[tool.id]; o != nil && rng.IntN(3) > 0 {
				agent = o.agentID
			}
			steps = append(steps, w.hook(max(pos+rng.IntN(40000)-30000, 0), phases[rng.IntN(len(phases))], tool, agent))
		default: // an effect
			to := max(pos+rng.IntN(30000)-20000, 1000)
			if rng.IntN(2) == 0 {
				steps = append(steps, w.file(fmt.Sprintf("/w/f%d.txt", rng.IntN(5)), max(to-rng.IntN(15000), 0), to))
			} else {
				steps = append(steps, w.net(fmt.Sprintf("h%d.example", rng.IntN(3)), to, to+rng.IntN(3000)))
			}
		}
	}
	for _, cl := range pending {
		steps = append(steps, w.parse(cl, core.ParseOK))
	}
	return steps
}

func TestIncrementalOracle(t *testing.T) {
	scenarios := []struct {
		name  string
		steps []oracleStep
	}{
		{"S1_linear", oracleLinear()},
		{"S2_out_of_order", oracleOutOfOrder()},
		{"S3_parse_late", oracleParseLate()},
		{"S4_late_hooks", oracleLateHooks()},
		{"S5_sub_agent", oracleSubAgent()},
		{"S6_effects_late", oracleLateEffects()},
		{"S7_hooks_only", oracleHooksOnly()},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) { checkOracle(t, sc.steps) })
	}
}

func TestIncrementalOracle_Randomized(t *testing.T) {
	for seed := uint64(1); seed <= 20; seed++ {
		t.Run(fmt.Sprintf("S8_seed%02d", seed), func(t *testing.T) { checkOracle(t, oracleRandom(seed)) })
	}
}
