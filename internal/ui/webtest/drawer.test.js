import test from "node:test";
import assert from "node:assert/strict";
import { byTag, installDom, textOf, withClass } from "./dom-stub.js";
import { call, item } from "./fixtures.js";

installDom();
const { createDrawerHost } = await import("./drawer-host.js");

const summary = { id: "s1", client: "c", models: ["m"], first_at: "2026-01-01T00:00:00Z", last_at: "2026-01-01T00:01:00Z", exchange_count: 3, model_calls: 3, main_errors: 0, active: true, usage: {}, tool_states: {} };
const prompt = (id, text) => item(id, "user_prompt", { role: "user", text });
const entries = () => [call("a", 1), prompt("a", "one"), call("b", 2, { prev: "a" }), call("c", 3, { prev: "b" })];

function setup(callId = null, es = entries(), loaded = true) {
  const navs = [];
  const nav = { push: (r) => navs.push(["push", r]), replace: (r) => navs.push(["replace", r]), back: () => navs.push(["back"]) };
  const p = createDrawerHost({ nav });
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
  const bar = withClass(s.p.el, "call-link")[1];
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
  assert.equal(s.p.render({ ...s.data, callId: "zzz" }).missing, true); // app.js shows "No such call"
  const early = setup("zzz", [], false);
  assert.equal(early.p.render({ ...early.data, callId: "zzz" }).missing, false);
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
  assert.equal(s.p.position().index, 0);
});

test("streamDropKeepsDrawer", () => {
  const s = setup("b");
  // a dropped stream redraws with the same history and a snapshot note; nothing closes
  s.p.render({ ...s.data, callId: "b", snapshotError: "boom" });
  assert.equal(s.drawer.hidden, false);
  assert.match(textOf(s.drawer), /Call #2/);
});

// ---- drawer sections (T014) ----
const none = { has_usage: false, partial: false, input: null, output: null, cache_write: null, cache_read: null };
const use = (input, cw, cr, output = 5) => ({ has_usage: true, partial: false, input, output, cache_write: cw, cache_read: cr });
const tool = (id, tcid, state, extra = {}) => item(id, "tool_call", { name: "Bash", tool_call_id: tcid, state, chip: "ok", ...extra });
const detailOf = (o = {}) => ({
  id: "b", status: 200, session_id: "s1", client_disconnected: false, upstream_aborted: false, gateway_error: "",
  request_body_stored: true, response_body_stored: true, side_call: false, prev_id: "a", thread_new: false,
  messages: [{ role: "user", text: "old", new: false }, { role: "assistant", text: "mid", new: false }, { role: "user", text: "fresh", new: true }],
  carried: 2, new_count: 1, response: { text: "all done", stop_reason: "end_turn" }, response_tool_call_ids: [], ...o,
});

async function open(callId, es, detail) {
  const navs = [];
  const nav = { push() {}, replace() {}, back() {} };
  const loadDetail = typeof detail === "function" ? detail : async () => detail;
  const p = createDrawerHost({ nav, loadDetail });
  p.render({ summary, entries: es, error: "", snapshotError: "", callId, loaded: true });
  await new Promise((r) => setTimeout(r, 0));
  const drawer = withClass(p.el, "drawer")[0];
  return { p, navs, drawer, text: () => textOf(drawer).replace(/:\s+/g, ": ").replace(/\s+/g, " ") };
}

test("toolChainShowsThreeSteps", async () => {
  const es = [call("a", 1), call("b", 2, { prev: "a" }), tool("b", "t1", "asked+ran+reported", { outcome: "succeeded", agent_type: "Explore", agent_id: "x1" })];
  const s = await open("b", es, detailOf({ response_tool_call_ids: ["t1"] }));
  const t = s.text();
  assert.match(t, /Bash/);
  assert.match(t, /✓ asked/);
  assert.match(t, /✓ ran/);
  assert.match(t, /✓ reported/);
  assert.match(t, /agent Explore x1/);
});

test("chainShowsRanWithReportedMissing", async () => {
  const es = [call("a", 1), call("b", 2, { prev: "a" }), tool("b", "t1", "ran-not-reported", { chip: "pending" })];
  const s = await open("b", es, detailOf({ response_tool_call_ids: ["t1"] }));
  assert.match(s.text(), /✓ ran/);
  assert.match(s.text(), /— reported missing/);
});

test("contextPartsAndDelta", async () => {
  const es = [call("a", 1, { usage: use(100, 0, 0) }), call("b", 2, { prev: "a", usage: use(120, 30, 10) })];
  const s = await open("b", es, detailOf());
  const t = s.text();
  assert.match(t, /Context size: 160/);
  assert.match(t, /Input: 120/);
  assert.match(t, /Cache write: 30/);
  assert.match(t, /Cache read: 10/);
  assert.match(t, /\+60 since #1/);
  const plot = byTag(s.drawer, "svg");
  assert.equal(plot.length, 1);
  assert.equal(plot[0].attrs.class, "ctx-svg"); // the main page's plot, styled the same
  assert.equal(byTag(s.drawer, "polyline")[0].attrs.points, "20,52 780,8");
  const dots = byTag(s.drawer, "circle");
  assert.equal(dots.length, 2);
  const marked = dots.filter((d) => d.attrs.class === "is-this");
  assert.equal(marked.length, 1);
  assert.equal(marked[0], dots[1]);
  assert.equal(marked[0].attrs.r, "6");
  assert.match(textOf(marked[0]), /#2 · context 160 · this call/);
  assert.match(s.text(), /this call is marked/);
});

test("messagesMarkedCarriedOrNew", async () => {
  const es = [call("a", 1), call("b", 2, { prev: "a" })];
  const s = await open("b", es, detailOf());
  assert.match(s.text(), /2 carried over · 1 new/);
  assert.equal(withClass(s.drawer, "is-carried").length, 2);
  assert.equal(withClass(s.drawer, "is-new").length, 1);
  assert.doesNotMatch(s.text(), /starts a new thread/);
  assert.match(s.text(), /all done/);
  assert.match(s.text(), /Stop reason: end_turn/);
  assert.match(s.text(), /Output tokens: 5/);
});

test("newThreadMarked", async () => {
  const es = [call("a", 1)];
  const s = await open("a", es, detailOf({ thread_new: true, prev_id: "", carried: 0, new_count: 3 }));
  assert.match(s.text(), /starts a new thread/);
  assert.match(s.text(), /first call/);
  assert.match(s.text(), /0 carried over · 3 new/);
});

test("healthShowsDisconnectAbortError", async () => {
  const es = [call("a", 1)];
  const s = await open("a", es, detailOf({ client_disconnected: true, upstream_aborted: true, gateway_error: "upstream_timeout" }));
  const t = s.text();
  assert.match(t, /Client disconnected: yes/);
  assert.match(t, /Upstream aborted: yes/);
  assert.match(t, /Gateway error: upstream_timeout/);
  assert.match(t, /Session: s1/);
});

test("rawLinksAndFullPageLink", async () => {
  const es = [call("a", 1)];
  const s = await open("a", es, detailOf());
  const hrefs = byTag(s.drawer, "a").map((a) => a.href);
  assert.ok(hrefs.includes("/api/exchanges/a/body?part=request"));
  assert.ok(hrefs.includes("/api/exchanges/a/body?part=response"));
  assert.ok(hrefs.includes("#/exchanges/a"));
  assert.match(s.text(), /All headers/);
  assert.match(s.text(), /Full page/);
});

test("missingSectionStatesReason", async () => {
  const es = [call("a", 1)];
  const d = detailOf({ messages: [], messages_unavailable: "request_body_not_stored", response: { text: "", stop_reason: "", unavailable: "capture_truncated" }, request_body_stored: false });
  const s = await open("a", es, d);
  assert.match(s.text(), /request body not stored/);
  assert.match(s.text(), /capture truncated/);
  assert.match(s.text(), /Messages: —/);
  assert.match(s.text(), /Response: —/);
  assert.match(s.text(), /request body: not stored/);
  assert.ok(byTag(s.drawer, "a").some((a) => a.href === "/api/exchanges/a/body?part=response"));
  // a failed detail fetch still leaves the context, the raw links and the reason
  const bad = await open("a", es, async () => { throw new Error("boom"); });
  assert.match(bad.text(), /detail unavailable: boom/);
  assert.match(bad.text(), /Context size: 10/);
  assert.ok(byTag(bad.drawer, "a").some((a) => a.href === "#/exchanges/a"));
});

test("noUsageCallShowsDashesAndNoDelta", async () => {
  const es = [call("a", 1, { usage: use(100, 0, 0) }), call("b", 2, { prev: "a", status: 429, usage: none })];
  const s = await open("b", es, detailOf({ status: 429, messages: [], messages_unavailable: "nothing_parsed", response: { text: "", stop_reason: "", unavailable: "nothing_parsed" } }));
  const t = s.text();
  assert.match(t, /Context size: —/);
  assert.match(t, /Input: —/);
  assert.match(t, /Change since previous call: —/);
  assert.doesNotMatch(t, /[+-]\d+ since/);
  assert.doesNotMatch(t, /Context size: 0/);
  assert.match(t, /Output tokens: —/);
});

// ---- closing always lands on the session URL (fix-batch item 9) ----
// A browser history stand-in: push, replace and back move through a stack of calls the
// way the URL hash does, and every move re-renders the page with the new top.
function withHistory(startCall, startStack = [null]) {
  const stack = startCall === undefined ? startStack : [...startStack, startCall];
  const hist = { stack };
  const top = () => hist.stack.at(-1);
  const nav = {
    push: (r) => { hist.stack.push(r.call); sync(); },
    replace: (r) => { hist.stack[hist.stack.length - 1] = r.call; sync(); },
    back: () => { hist.stack.pop(); sync(); },
  };
  const p = createDrawerHost({ nav });
  const data = { summary, entries: entries(), error: "", snapshotError: "", loaded: true };
  const sync = () => p.render({ ...data, callId: top() });
  sync();
  const drawer = withClass(p.el, "drawer")[0];
  // click a link or bar: the page notes the opener, then the browser follows the href.
  const clickToCall = (node, id) => { node.fire("click"); hist.stack.push(id); sync(); };
  return { p, hist, top, drawer, clickToCall, bars: () => withClass(p.el, "call-link") };
}

test("closeFromInAppOpenLandsOnSessionUrl", () => {
  const s = withHistory(undefined);
  s.clickToCall(s.bars()[0], "a");
  assert.equal(s.drawer.hidden, false);
  s.p.closeDrawer();
  assert.equal(s.top(), null);
  assert.equal(s.drawer.hidden, true);
});

test("closeAfterSteppingLandsOnSessionUrl", () => {
  const s = withHistory(undefined);
  s.clickToCall(s.bars()[0], "a");
  s.p.stepCall(1);
  s.p.stepCall(1);
  s.p.stepCall(-1);
  assert.equal(s.top(), "b");
  s.p.closeDrawer();
  assert.equal(s.top(), null);
  assert.equal(s.drawer.hidden, true);
});

test("closeOfReloadRestoredDrawerStaysInTheApp", () => {
  const s = withHistory("b", []); // a reload or pasted URL: the only history entry is the call
  assert.equal(s.drawer.hidden, false);
  s.p.closeDrawer();
  assert.equal(s.top(), null);
  assert.equal(s.hist.stack.length, 1, "closing must not leave the app");
  assert.equal(s.drawer.hidden, true);
  const stepped = withHistory("a", []);
  stepped.p.stepCall(1);
  stepped.p.closeDrawer();
  assert.deepEqual(stepped.hist.stack, [null]);
});

test("closeAfterOpeningAnotherCallWhileOpenLandsOnSessionUrl", () => {
  const opened = withHistory(undefined);
  opened.clickToCall(opened.bars()[0], "a");
  opened.clickToCall(opened.bars()[2], "c"); // a bar clicked with the drawer already open
  opened.p.closeDrawer();
  assert.equal(opened.top(), null);
  const restored = withHistory("a", []);
  restored.clickToCall(restored.bars()[2], "c");
  restored.p.closeDrawer();
  assert.equal(restored.top(), null);
});
