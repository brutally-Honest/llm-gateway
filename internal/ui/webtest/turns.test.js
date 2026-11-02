import test from "node:test";
import assert from "node:assert/strict";
import { buildTurns, hooksLabel, splitReminders } from "../web/model.js";
import { all, byTag, installDom, textOf, withClass } from "./dom-stub.js";
import { call, item } from "./fixtures.js";

const prompt = (id, text) => item(id, "user_prompt", { role: "user", text });
const reply = (id, text) => item(id, "assistant_text", { role: "assistant", text });

test("turnStartsAtMainPrompt", () => {
  const { turns, cards } = buildTurns([
    call("a", 1), prompt("a", "one"), reply("a", "r1"),
    call("b", 2, { prev: "a" }), item("b", "user_prompt", { text: "" }), reply("b", "r1b"),
    call("c", 3, { prev: "b" }), prompt("c", "two"),
  ]);
  assert.equal(turns.length, 2);
  assert.equal(cards.length, 2);
  assert.equal(turns[0].number, 1);
  assert.equal(turns[0].prompt.text, "one");
  assert.deepEqual(turns[0].texts, ["r1", "r1b"]);
  assert.equal(turns[0].counts.calls, 2);
  assert.equal(turns[0].durationMs, 2000);
  assert.deepEqual(turns[0].calls.map((c) => c.label), ["#1 · 1.0s", "#2 · 1.0s"]);
  assert.equal(turns[1].prompt.text, "two");
});

test("otherThreadCallJoinsOpenTurn", () => {
  const { turns } = buildTurns([
    call("a", 1), prompt("a", "go"),
    call("s", 2, { main: false, thread: 1 }), prompt("s", "sub prompt"),
    call("b", 3, { prev: "a" }),
  ]);
  assert.equal(turns.length, 1);
  assert.deepEqual(turns[0].calls.map((c) => c.n), [1, 2, 3]);
});

test("sideCallsNeverStartTurn", () => {
  const { cards, turns } = buildTurns([
    call("a", 1), prompt("a", "go"),
    call("q", 2, { main: false, side: true, thread: 1 }), prompt("q", "quota"),
    call("b", 3, { prev: "a" }),
    call("p", 4, { main: false, side: true, thread: 2 }), prompt("p", "other"),
  ]);
  assert.equal(turns.length, 1);
  assert.deepEqual(cards.map((c) => c.kind), ["turn", "side"]);
  assert.equal(cards[1].number, null);
  assert.deepEqual(cards[1].calls.map((c) => c.n), [2, 4]);
  assert.equal(turns[0].prompt.text, "go");
});

test("beforeFirstPromptCard", () => {
  const { cards, turns } = buildTurns([
    call("x", 1, { main: false, side: true, thread: 1 }), prompt("x", "quota"),
    call("y", 2, { thread: 2 }),
    call("a", 3), prompt("a", "hello"),
  ]);
  assert.deepEqual(cards.map((c) => c.kind), ["before", "turn"]);
  assert.equal(cards[0].number, null);
  assert.deepEqual(cards[0].calls.map((c) => c.n), [1, 2]);
  assert.equal(turns.length, 1);
  assert.equal(turns[0].number, 1);
});

test("onlySideCallsOneCardNoTurns", () => {
  const { cards, turns } = buildTurns([
    call("x", 1, { main: false, side: true }), prompt("x", "quota"),
    call("y", 2, { main: false, side: true }), prompt("y", "quota"),
  ]);
  assert.equal(turns.length, 0);
  assert.equal(cards.length, 1);
  assert.equal(cards[0].kind, "before");
  assert.equal(cards[0].calls.length, 2);
});

test("hooksOnlySessionStartsTurnsAtPrompt", () => {
  const h = (id, phase, extra = {}) => ({ id: `hook:${id}`, kind: "hook_event", phase, ...extra });
  const r = buildTurns([
    h(1, "prompt", { text: "first" }),
    h(2, "pre_tool", { tool_call_id: "t1", name: "Bash", chip: "unmatched", state: "ran-not-asked-or-not-captured" }), h(3, "post_tool", { tool_call_id: "t1", name: "Bash", chip: "unmatched", state: "ran-not-asked-or-not-captured", outcome: "succeeded" }),
    h(4, "prompt", { text: "second" }),
    h(5, "pre_tool", { tool_call_id: "t2", name: "Read", chip: "unmatched", state: "ran-not-asked-or-not-captured" }), h(6, "post_tool_failed", { tool_call_id: "t2", name: "Read", chip: "unmatched", state: "ran-not-asked-or-not-captured", outcome: "failed" }),
  ]);
  assert.equal(r.turns.length, 2);
  assert.match(r.note, /no model calls/);
  assert.equal(r.turns[0].tools[0].chip.text, "unmatched");
  assert.equal(r.turns[1].tools[0].chip.text, "unmatched");
  assert.equal(r.turns[1].tools[0].chip.title, "ran-not-asked-or-not-captured · hooks: failed");
  assert.equal(r.turns[0].calls.length, 0);
  assert.equal(r.turns[0].hooksOnly, true);
});

test("erroredSideCallStillRed", () => {
  const { cards } = buildTurns([
    call("a", 1), prompt("a", "go"),
    call("q", 2, { main: false, side: true, status: 500 }),
    item("q", "error", { text: "boom" }),
  ]);
  const side = cards.find((c) => c.kind === "side");
  assert.equal(side.errored, true);
  assert.equal(side.calls[0].errored, true);
  assert.equal(side.calls[0].errorWord, "error");
  assert.equal(cards[0].errored, false);
});

test("agentLabelAndErrorMarkKept", () => {
  const { turns } = buildTurns([
    call("a", 1), prompt("a", "go"),
    item("a", "tool_call", { name: "Agent", tool_call_id: "t1", chip: "ok", state: "asked+ran+reported", outcome: "succeeded" }),
    call("s", 2, { main: false, thread: 1 }),
    item("s", "tool_call", { name: "Read", tool_call_id: "t2", agent_id: "ag1", agent_type: "explore", state: "pending" }),
    item("s", "tool_result", { tool_call_id: "t2", is_error: true }),
    item("s", "error", { text: "x" }),
  ]);
  const t = turns[0];
  assert.equal(t.calls[1].agent, "agent explore ag1");
  assert.equal(t.calls[1].errored, true);
  assert.equal(t.tools[1].agent, "agent explore ag1");
  assert.equal(t.tools[1].resultError, true);
  assert.equal(t.tools[0].agent, "");
  assert.equal(t.tools[0].chip.text, "ok");
});

test("turnHeadingCountsAndDuration", () => {
  const { cards } = buildTurns([
    call("a", 1), prompt("a", "go"), item("a", "tool_call", { name: "Bash", tool_call_id: "t1" }), item("a", "tool_call", { name: "Read", tool_call_id: "t2" }),
    call("b", 2, { prev: "a", ms: 1250 }),
    call("q", 3, { main: false, side: true }),
    call("c", 4, { prev: "b" }), prompt("c", "two"),
  ]);
  assert.equal(cards[0].heading, "Turn 1 · 2 calls · 2 tools · 2.3s");
  assert.equal(cards[1].heading, "Side calls · 1 call · 0 tools · 1.0s");
  assert.equal(cards[2].heading, "Turn 2 · 1 call · 0 tools · 1.0s");
});

test("callLinksLabelled", () => {
  const { turns } = buildTurns([call("a", 1, { ms: 850 }), prompt("a", "go"), call("b", 2, { prev: "a", ms: 125000 })]);
  assert.deepEqual(turns[0].calls.map((c) => c.label), ["#1 · 850ms", "#2 · 2m05s"]);
  assert.deepEqual(turns[0].calls.map((c) => c.exchangeId), ["a", "b"]);
});

test("hooksHiddenBehindToggle", () => {
  const h = (id, tool, start, extra = {}) => ({ id: `hook:${id}`, kind: "hook_event", phase: "pre_tool", name: "Bash", tool_call_id: tool, start, ...extra });
  const { turns } = buildTurns([
    call("a", 1), prompt("a", "go"), item("a", "tool_call", { name: "Bash", tool_call_id: "t1" }),
    call("b", 2, { prev: "a" }), prompt("b", "two"),
    h(1, "t1", "2026-01-01T00:00:01Z", { agent_id: "ag", agent_type: "explore" }),
    h(2, "", "2026-01-01T00:00:02Z", { phase: "stop" }),
  ]);
  assert.deepEqual(turns[0].hooks.map((x) => x.id), ["hook:1"]);
  assert.deepEqual(turns[1].hooks.map((x) => x.id), ["hook:2"]);
  assert.equal(hooksLabel(1), "1 hook");
  assert.equal(hooksLabel(2), "2 hooks");
});

test("remindersFoldedByDefault", () => {
  const r = "<system-reminder>be good</system-reminder>";
  assert.deepEqual(splitReminders(`${r}\nfix it\n${r}`), { body: "fix it", reminders: [r, r] });
  assert.deepEqual(splitReminders("plain"), { body: "plain", reminders: [] });
  const { turns } = buildTurns([call("a", 1), prompt("a", `fix\n${r}`)]);
  assert.equal(turns[0].prompt.text, `fix\n${r}`);
  assert.equal(turns[0].prompt.body, "fix");
  assert.equal(turns[0].prompt.reminders.length, 1);
});

test("truncatedLinkAndCopyOffered", async () => {
  installDom();
  const { toolRowNode } = await import("../web/tool-card.js");
  const { turns } = buildTurns([
    call("a", 1), prompt("a", "go"),
    item("a", "tool_call", { name: "Bash", tool_call_id: "t1", text: '{"cmd":"ls"}', truncated: true }),
    item("a", "tool_result", { tool_call_id: "t1", text: "out", truncated: true }),
  ]);
  const node = toolRowNode(turns[0].tools[0], { isOpen: () => false, setOpen() {} });
  const sections = all(node).filter((e) => e.tagName === "details");
  assert.equal(sections.length, 2);
  for (const s of sections) {
    assert.equal(withClass(s, "truncated").length, 1);
    assert.equal(withClass(s, "truncated")[0].textContent, "truncated — open raw");
    assert.equal(withClass(s, "copy").length, 1);
  }
  assert.match(all(sections[0]).find((e) => e.tagName === "pre").textContent, /"cmd": "ls"/);
});

const summary = { id: "s1", client: "c", models: ["m"], first_at: "2026-01-01T00:00:00Z", last_at: "2026-01-01T00:01:00Z", exchange_count: 3, model_calls: 3, main_errors: 0, active: false, usage: {}, tool_states: {} };
async function railPage(entries) {
  installDom();
  const { createTimelinePage } = await import("../web/timeline-page.js");
  const p = createTimelinePage();
  p.render({ summary, entries, error: "", snapshotError: "" });
  return p;
}
const threeTurns = () => [
  call("a", 1), prompt("a", "alpha"), item("a", "tool_call", { name: "Bash", tool_call_id: "t1", chip: "ok" }),
  call("b", 2, { prev: "a", status: 500 }), prompt("b", "beta"), item("b", "error", { text: "x" }),
  call("c", 3, { prev: "b" }), prompt("c", "gamma"),
];

test("railListsEveryTurn", async () => {
  const p = await railPage(threeTurns());
  const items = withClass(p.el, "rail-item");
  assert.equal(items.length, 3);
  assert.match(textOf(items[0]), /alpha/);
  assert.match(textOf(items[0]), /ok/);
  assert.match(textOf(items[0]), /1 call/);
  assert.match(textOf(items[0]), /1\.0s/);
  assert.ok(items[1].className.includes("is-error"));
});

test("railSelectionMarksCurrent", async () => {
  const p = await railPage(threeTurns());
  const scrolled = [];
  for (const c of withClass(p.el, "turn")) c.scrollIntoView = () => scrolled.push(c);
  const items = withClass(p.el, "rail-item");
  items[2].fire("click");
  const now = withClass(p.el, "rail-item");
  assert.deepEqual(now.map((i) => i.attrs["aria-current"]), ["false", "false", "true"]);
  assert.equal(withClass(p.el, "is-current").length, 1);
  assert.deepEqual(p.position(), { index: 3, total: 3 });
});

test("showOptionsApplyAtOnce", async () => {
  const r = "<system-reminder>secret</system-reminder>";
  const hook = { id: "hook:1", kind: "hook_event", phase: "pre_tool", name: "Bash", tool_call_id: "t1", start: "2026-01-01T00:00:01Z" };
  const p = await railPage([...threeTurns().slice(0, 1), prompt("a", `fix ${r}`), item("a", "tool_call", { name: "Bash", tool_call_id: "t1" }), ...threeTurns().slice(3), hook]);
  const opt = (label) => byTag(p.el, "label").find((l) => textOf(l).includes(label)).children[0];
  const flip = (label) => { const b = opt(label); b.checked = !b.checked; b.fire("change"); };
  assert.equal(withClass(p.el, "hook-event").length, 0);
  flip("Hooks inline");
  assert.equal(withClass(p.el, "hook-event").length, 1);
  assert.doesNotMatch(textOf(withClass(p.el, "timeline")[0]), /secret/);
  flip("System reminders");
  assert.match(textOf(withClass(p.el, "timeline")[0]), /secret/);
  assert.equal(withClass(p.el, "call-context").length, 3);
  flip("Context per call");
  assert.equal(withClass(p.el, "call-context").length, 0);
  assert.equal(withClass(p.el, "turn").length, 3);
  flip("Only turns with errors");
  assert.equal(withClass(p.el, "turn").length, 1);
  assert.match(textOf(withClass(p.el, "turn")[0]), /Turn 2/);
  assert.equal(withClass(p.el, "rail-item").length, 3);
});
