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

// AC10 (carries drawerKeepsCallFocusAndScrollOnLiveData, lateCallRenumbersTheOpenDrawerWithoutMovingIt):
// a live update fetches the open call again and redraws it in place — the same call, its
// scroll and focus kept, its tool's new state shown, Next enabled once a later call exists,
// and its number updated when an earlier call arrives late.
test("callDialogKeepsItsCallOnLiveData", async () => {
  const { installDom, byTag, textOf, withClass } = await import("./dom-stub.js");
  installDom();
  const { createCallDialog } = await import("../web/call-dialog.js");
  const tool = (word) => ({ id: "t", name: "Bash", kind: "command", state: { glyph: word === "ok" ? "✓" : "…", word }, chip: word, summary: "npm test" });
  const mk = (n, nav, toolWord) => ({
    n, exchange_id: "a", turn: 1,
    facts: { model: "m", dur_ms: 1, start: "", stop: { word: "tool use" }, thread: { kind: "main", label: "main" }, ctx: 1 },
    context: { size: 1, first: true }, saw: { first: true, carried: 0, new: 1, messages: [{ role: "user", text: "go", new: true }] },
    thinking: false, response: { text: "ok" }, tools: [tool(toolWord)], health: { status: "200 OK", ok: true, items: [] },
    raw: { headers: "#/exchanges/a", page: "#/exchanges/a" }, nav,
  });
  let served = mk(1, { index: 1, total: 2, prev: "", next: "b" }, "pending");
  const asked = [];
  const d = createCallDialog({ nav: { push() {}, replace() {}, back() {} }, loadCall: async (id) => { asked.push(id); return served; } });
  const tick = () => new Promise((r) => setTimeout(r, 0));
  d.sync({ sessionId: "s1", callId: "a" });
  await tick();
  const body = withClass(d.el, "mb")[0]; // the part that scrolls
  body.scrollTop = 120;
  const next = byTag(d.el, "button").find((b) => b.textContent.includes("Next"));
  next.focus();
  assert.match(textOf(d.el), /pending/);

  served = mk(1, { index: 1, total: 3, prev: "", next: "b" }, "ok");
  d.refresh();
  await tick();
  assert.equal(d.isOpen(), true);
  assert.equal(d.callId(), "a", "the dialog stays on its call");
  assert.match(textOf(d.el), /Call #1/);
  assert.equal(body.scrollTop, 120, "its scroll is kept");
  assert.equal(globalThis.document.activeElement, next, "its focus is kept");
  assert.match(textOf(d.el), /✓ ok|ok/);
  assert.doesNotMatch(textOf(d.el), /pending/);
  assert.match(textOf(d.el), /1 of 3/, "the new call is counted");
  assert.equal(next.disabled, false);
  assert.deepEqual(asked, ["a", "a"], "the same call is fetched again");

  // an earlier call arrives late: the open call is renumbered, never moved
  served = { ...mk(2, { index: 2, total: 4, prev: "z", next: "b" }, "ok") };
  d.refresh();
  await tick();
  assert.match(textOf(d.el), /Call #2/);
  assert.equal(d.callId(), "a");
  // a refresh that fails keeps the call shown
  let loads = 0;
  const failing = createCallDialog({ nav: { push() {}, replace() {}, back() {} }, loadCall: async () => { if (++loads > 1) throw new Error("boom"); return served; } });
  failing.sync({ sessionId: "s1", callId: "a" });
  await tick();
  failing.refresh();
  await tick();
  assert.match(textOf(failing.el), /Call #2/);
  assert.doesNotMatch(textOf(failing.el), /unavailable/);
});
