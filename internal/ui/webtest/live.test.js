import test from "node:test";
import assert from "node:assert/strict";
import { loadAllPages } from "../web/api.js";

test("loadsEveryTimelinePage", async () => {
  const pages = { undefined: { entries: [1, 2], next_after: "a" }, a: { entries: [3], next_after: "b" }, b: { entries: [4], next_after: null } };
  const asked = [];
  const got = [];
  await loadAllPages(async (after) => {
    asked.push(after);
    return pages[after];
  }, (p) => got.push(...p.entries));
  assert.deepEqual(asked, [undefined, "a", "b"]);
  assert.deepEqual(got, [1, 2, 3, 4]);
});

test("loadAllPagesStopsWhenTheViewMovedOn", async () => {
  let calls = 0;
  await loadAllPages(async () => {
    calls++;
    return { next_after: "more" };
  }, () => false);
  assert.equal(calls, 1);
});

test("drawerKeepsCallFocusAndScrollOnLiveData", async () => {
  const { installDom, withClass } = await import("./dom-stub.js");
  installDom();
  const { call, item } = await import("./fixtures.js");
  const { createTimelinePage } = await import("../web/timeline-page.js");
  const summary = { id: "s1", client: "c", models: [], first_at: "", last_at: "", exchange_count: 2, model_calls: 2, main_errors: 0, active: true, usage: {}, tool_states: {} };
  const p = createTimelinePage();
  const es = [call("a", 1), item("a", "tool_call", { name: "Bash", tool_call_id: "t", chip: "pending" }), call("b", 2, { prev: "a" })];
  p.render({ summary, entries: es, error: "", snapshotError: "", callId: "a", loaded: true });
  const drawer = withClass(p.el, "drawer")[0];
  const body = drawer.children[1];
  body.scrollTop = 120;
  const prev = drawer.children[0].children.find((c) => c.textContent.includes("next"));
  prev.focus();
  const chipText = () => drawer.children[0].children[1].children.map((c) => c.textContent).join();
  assert.match(chipText(), /pending/);
  const done = [call("a", 1), item("a", "tool_call", { name: "Bash", tool_call_id: "t", chip: "ok" }), call("b", 2, { prev: "a" }), call("c", 3, { prev: "b" })];
  p.render({ summary, entries: done, error: "", snapshotError: "", callId: "a", loaded: true });
  assert.equal(drawer.hidden, false);
  assert.match(drawer.children[0].children[0].textContent, /Call #1/);
  assert.equal(body.scrollTop, 120);
  assert.equal(globalThis.document.activeElement, prev);
  assert.match(chipText(), /ok/);
  assert.equal(p.position().total, 3); // position and next see the new call
  assert.equal(prev.disabled, false);
});

test("lateCallRenumbersTheOpenDrawerWithoutMovingIt", async () => {
  const { installDom, withClass } = await import("./dom-stub.js");
  installDom();
  const { call } = await import("./fixtures.js");
  const { createTimelinePage } = await import("../web/timeline-page.js");
  const summary = { id: "s1", client: "c", models: [], first_at: "", last_at: "", exchange_count: 2, model_calls: 2, main_errors: 0, active: true, usage: {}, tool_states: {} };
  const p = createTimelinePage();
  const shown = (entries) => p.render({ summary, entries, error: "", snapshotError: "", callId: "c", loaded: true });
  shown([call("a", 1), call("c", 3, { prev: "a" })]);
  const title = withClass(p.el, "drawer")[0].children[0].children[0];
  assert.match(title.textContent, /Call #2/);
  shown([call("a", 1), call("b", 2, { prev: "a" }), call("c", 3, { prev: "b" })]);
  assert.match(title.textContent, /Call #3/);
  assert.equal(p.position().index, 3); // still call c, now third of three
});

test("followOnlyWhenDrawerClosedAndAtBottom", async () => {
  const { shouldFollow } = await import("../web/drawer.js");
  assert.equal(shouldFollow(false, true), true);
  assert.equal(shouldFollow(true, true), false);
  assert.equal(shouldFollow(false, false), false);
});

test("liveUpdatesAllViews", async () => {
  const { installDom, withClass, textOf } = await import("./dom-stub.js");
  installDom();
  const { call, item } = await import("./fixtures.js");
  const { createTimelinePage } = await import("../web/timeline-page.js");
  const summary = { id: "s1", client: "c", models: [], first_at: "", last_at: "", exchange_count: 1, model_calls: 1, main_errors: 0, active: true, usage: {}, tool_states: {} };
  const p = createTimelinePage();
  const first = [item("a", "user_prompt", { role: "user", text: "go" }), call("a", 1)];
  p.render({ summary, entries: first, error: "", snapshotError: "", callId: "a", loaded: true });
  const bars = () => withClass(p.el, "strip-bar").length;
  const turns = () => withClass(p.el, "turn-head").length;
  assert.equal(bars(), 1);
  const more = [...first, item("b", "user_prompt", { role: "user", text: "next" }), call("b", 2, { prev: "a", status: 500 })];
  p.render({ summary: { ...summary, exchange_count: 2, model_calls: 2, main_errors: 1 }, entries: more, error: "", snapshotError: "", callId: "a", loaded: true });
  assert.equal(bars(), 2);
  assert.equal(turns(), 2);
  assert.equal(withClass(p.el, "rail-item").length, 2);
  assert.match(textOf(withClass(p.el, "stats")[0]), /2 calls/);
  assert.match(textOf(withClass(p.el, "stats")[0]), /1 error/);
});

// AC14: a live effects update redraws only the effects rows whose effects changed; every
// turn element stays the same node, and an expanded row stays open.
test("liveEffectsRedrawOnlyTheChangedRows", async () => {
  const { installDom, withClass } = await import("./dom-stub.js");
  installDom();
  const { call, item } = await import("./fixtures.js");
  const { createTimelinePage } = await import("../web/timeline-page.js");
  const summary = { id: "s1", client: "c", models: [], first_at: "", last_at: "", exchange_count: 2, model_calls: 2, main_errors: 0, active: true, usage: {}, tool_states: {} };
  const at = (s) => new Date(Date.UTC(2026, 0, 1, 0, 0, s)).toISOString();
  const fx = (id, s, path) => ({ id, kind: "file_change", at: at(s), path, change: "created", size: 1 });
  const p = createTimelinePage();
  const entries = [
    item("a", "user_prompt", { role: "user", text: "one" }), call("a", 1),
    item("b", "user_prompt", { role: "user", text: "two" }), call("b", 3, { prev: "a" }),
    item("c", "user_prompt", { role: "user", text: "three" }), call("c", 5, { prev: "b" }),
  ];
  p.render({ summary, entries, effects: [], error: "", snapshotError: "", loaded: true });
  p.setEffects([fx(1, 1, "/w/a"), fx(2, 3, "/w/b")]);
  const sections = () => withClass(p.el, "turn");
  const before = sections();
  const rows = () => withClass(p.el, "effects");
  const [row1, row2] = rows();
  const toggle1 = withClass(row1, "effects-toggle")[0];
  toggle1.fire("click");
  assert.equal(withClass(row1, "effects-toggle")[0].attrs["aria-expanded"], "true");
  const row1Kids = [...row1.children];
  const row2Kids = [...row2.children];

  // turn 3 gains its first effect, then turn 2 grows: turn 1 is never touched
  p.setEffects([fx(1, 1, "/w/a"), fx(2, 3, "/w/b"), fx(3, 5, "/w/c")]);
  p.setEffects([fx(1, 1, "/w/a"), fx(2, 3, "/w/b"), fx(3, 5, "/w/c"), fx(4, 4, "/w/b2")]);
  const after = sections();
  assert.equal(after.length, before.length);
  after.forEach((s, i) => assert.equal(s, before[i], `section ${i} replaced`));
  assert.equal(rows().length, 3);
  assert.equal(rows()[0], row1);
  assert.deepEqual(row1.children, row1Kids, "turn 1's row redrew though its effects did not change");
  assert.equal(withClass(row1, "effects-toggle")[0].attrs["aria-expanded"], "true");
  assert.equal(rows()[1], row2);
  assert.notDeepEqual(row2.children, row2Kids);
  assert.match(withClass(row2, "effects-toggle")[0].textContent, /2 changed/);
  assert.match(withClass(rows()[2], "effects-toggle")[0].textContent, /1 changed/);
});
