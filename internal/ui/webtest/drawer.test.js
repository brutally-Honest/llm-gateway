import test from "node:test";
import assert from "node:assert/strict";
import { byTag, installDom, textOf, withClass } from "./dom-stub.js";
import { call, item } from "./fixtures.js";

installDom();
const { createTimelinePage } = await import("../web/timeline-page.js");

const summary = { id: "s1", client: "c", models: ["m"], first_at: "2026-01-01T00:00:00Z", last_at: "2026-01-01T00:01:00Z", exchange_count: 3, model_calls: 3, main_errors: 0, active: true, usage: {}, tool_states: {} };
const prompt = (id, text) => item(id, "user_prompt", { role: "user", text });
const entries = () => [call("a", 1), prompt("a", "one"), call("b", 2, { prev: "a" }), call("c", 3, { prev: "b" })];

function setup(callId = null, es = entries(), loaded = true) {
  const navs = [];
  const nav = { push: (r) => navs.push(["push", r]), replace: (r) => navs.push(["replace", r]), back: () => navs.push(["back"]) };
  const p = createTimelinePage({ nav });
  const data = { summary, entries: es, error: "", snapshotError: "", callId, loaded };
  p.render(data);
  const drawer = withClass(p.el, "drawer")[0];
  const btn = (label) => byTag(drawer, "button").find((b) => b.textContent.includes(label));
  return { p, navs, data, drawer, btn, show: (id) => p.render({ ...data, callId: id }) };
}

test("opensFromLinkBarAndEnter", () => {
  const s = setup();
  assert.equal(s.drawer.hidden, true);
  const link = withClass(s.p.el, "call-link")[0];
  link.fire("click");
  s.show("a");
  assert.equal(s.drawer.hidden, false);
  assert.match(textOf(s.drawer), /Call #1/);
  const bar = withClass(s.p.el, "strip-bar")[1];
  assert.match(bar.href, /#\/sessions\/s1\/call\/b$/);
  bar.fire("click");
  s.show("b");
  assert.match(textOf(s.drawer), /Call #2/);
  s.show(null);
  s.p.openFirstCall();
  assert.deepEqual(s.navs.at(-1), ["push", { name: "session", id: "s1", call: "a" }]);
});

test("arrowsStepAndEscReturnsFocus", () => {
  const s = setup();
  const link = withClass(s.p.el, "call-link")[1];
  link.focus();
  link.fire("click");
  s.show("b");
  s.p.stepCall(1);
  assert.deepEqual(s.navs.at(-1), ["replace", { name: "session", id: "s1", call: "c" }]);
  s.p.stepCall(-1);
  assert.deepEqual(s.navs.at(-1), ["replace", { name: "session", id: "s1", call: "a" }]);
  assert.deepEqual(s.p.position(), { index: 2, total: 3 });
  s.p.closeDrawer();
  assert.deepEqual(s.navs.at(-1), ["back"]);
  s.show(null);
  assert.equal(s.drawer.hidden, true);
  assert.equal(globalThis.document.activeElement, link);
});

test("firstAndLastStepDisabled", () => {
  const s = setup("a");
  assert.equal(s.btn("previous").disabled, true);
  assert.equal(s.btn("next").disabled, false);
  const n = s.navs.length;
  s.p.stepCall(-1);
  assert.equal(s.navs.length, n);
  s.show("c");
  assert.equal(s.btn("next").disabled, true);
  assert.equal(s.btn("previous").disabled, false);
  s.p.stepCall(1);
  assert.equal(s.navs.length, n);
});

test("unknownCallShowsNoSuchCallNote", () => {
  const s = setup("zzz");
  assert.equal(s.drawer.hidden, true);
  assert.match(textOf(s.p.el), /No such call/);
  const early = setup("zzz", [], false);
  assert.doesNotMatch(textOf(early.p.el), /No such call/);
});

test("focusFallsToTurnHeading", () => {
  const s = setup();
  const link = withClass(s.p.el, "call-link")[0];
  link.focus();
  link.fire("click");
  s.show("a");
  link.isConnected = false; // a redraw dropped the opener
  s.show(null);
  const head = withClass(s.p.el, "turn-head")[0];
  assert.equal(globalThis.document.activeElement, head);
});

test("backClosesDrawer", () => {
  const s = setup("a"); // restored from a reload: no opener
  assert.equal(s.drawer.hidden, false);
  s.p.closeDrawer();
  assert.deepEqual(s.navs.at(-1), ["replace", { name: "session", id: "s1", call: null }]);
  s.show(null); // the browser's Back lands here too
  assert.equal(s.drawer.hidden, true);
  assert.equal(s.p.position().total, 1);
});

test("streamDropKeepsDrawer", () => {
  const s = setup("b");
  // a dropped stream redraws with the same history and a snapshot note; nothing closes
  s.p.render({ ...s.data, callId: "b", snapshotError: "boom" });
  assert.equal(s.drawer.hidden, false);
  assert.match(textOf(s.drawer), /Call #2/);
  assert.match(textOf(s.p.el), /Live snapshot failed/);
});
