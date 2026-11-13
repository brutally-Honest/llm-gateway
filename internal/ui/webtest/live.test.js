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
  const { createDrawerHost } = await import("./drawer-host.js");
  const summary = { id: "s1", client: "c", models: [], first_at: "", last_at: "", exchange_count: 2, model_calls: 2, main_errors: 0, active: true, usage: {}, tool_states: {} };
  const p = createDrawerHost();
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
  const { createDrawerHost } = await import("./drawer-host.js");
  const summary = { id: "s1", client: "c", models: [], first_at: "", last_at: "", exchange_count: 2, model_calls: 2, main_errors: 0, active: true, usage: {}, tool_states: {} };
  const p = createDrawerHost();
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
