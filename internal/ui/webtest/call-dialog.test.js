import test from "node:test";
import assert from "node:assert/strict";
import { byTag, installDom, textOf, withClass } from "./dom-stub.js";

// The call details dialog (spec 017 AC5–AC11, AC21). It replaced the 013 drawer: each of the
// drawer's tests is carried here (or into app.test.js) assertion by assertion.

installDom();
const { createCallDialog, changeText, sawText } = await import("../web/call-dialog.js");

const ids = ["a", "b", "c"];
// detail is the server's call details for one of three calls a, b, c (spec 017 T014).
function detail(id, o = {}) {
  const i = ids.indexOf(id);
  return {
    n: i + 1, exchange_id: id, turn: 1,
    facts: { model: "m1", dur_ms: 1000, start: "2026-01-01T00:00:01Z", stop: { word: "end turn" }, thread: { kind: "main", label: "main" }, ctx: 160 },
    context: { size: 160, delta: i ? 60 : null, prev_n: i ? i : 0, first: i === 0, why: i ? "added #1's reply" : "first call: system prompt", input: 120, cache_write: 30, cache_read: 10, output: 5 },
    saw: { first: i === 0, carried: i ? 2 : 0, new: 1, messages: [{ role: "user", text: "old", new: false }, { role: "assistant", text: "mid", new: false }, { role: "user", text: "fresh", new: true }] },
    thinking: false,
    response: { text: "all done", stop_reason: "end turn" },
    tools: [],
    health: { status: "200 OK", ok: true, items: [] },
    raw: { request: `/api/exchanges/${id}/body?part=request`, response: `/api/exchanges/${id}/body?part=response`, headers: `#/exchanges/${id}`, page: `#/exchanges/${id}` },
    nav: { index: i + 1, total: 3, prev: ids[i - 1] || "", next: ids[i + 1] || "" },
    ...o,
  };
}
const tick = () => new Promise((r) => setTimeout(r, 0));

// host is the page around the dialog the way app.js runs it: a history stack of call ids
// that push, replace and back move through, each move syncing the dialog to the top.
function host({ start = [null], load, missing } = {}) {
  const doc = globalThis.document;
  const page = doc.createElement("div");
  const heading = doc.createElement("h2");
  heading.className = "turn-head";
  const links = ids.map((id) => {
    const a = doc.createElement("a");
    a.className = "call-link";
    a.href = `#/sessions/s1/call/${id}`;
    return a;
  });
  page.append(heading, ...links);
  const hist = { stack: [...start], moves: [] };
  const top = () => hist.stack.at(-1);
  let dialog = null;
  const sync = () => dialog.sync({ sessionId: "s1", callId: top() });
  const nav = {
    push: (r) => { hist.moves.push(["push", r]); hist.stack.push(r.call); sync(); },
    replace: (r) => { hist.moves.push(["replace", r]); hist.stack[hist.stack.length - 1] = r.call; sync(); },
    back: () => { hist.moves.push(["back"]); hist.stack.pop(); sync(); },
  };
  const asked = [];
  dialog = createCallDialog({
    nav,
    loadCall: async (id) => {
      asked.push(id);
      return load ? load(id) : detail(id);
    },
    turnHeading: () => heading,
    onMissing: () => missing && missing(),
  });
  page.append(dialog.el);
  links.forEach((a, i) => a.addEventListener("click", () => dialog.noteOpener(a)));
  // clickTo is a call link clicked: the dialog notes the opener, then the browser follows it
  const clickTo = (i) => { links[i].focus(); links[i].fire("click"); hist.stack.push(ids[i]); sync(); };
  sync();
  const root = dialog.el;
  const btn = (label) => byTag(root, "button").find((b) => b.textContent.includes(label));
  return { dialog, root, hist, top, links, heading, asked, clickTo, btn, text: () => textOf(root).replace(/\s+/g, " ") };
}

// AC5 (carries opensFromLinkBarAndEnter, backClosesDrawer): a link opens the call in the
// dialog; the page's own targets push the call URL; Back closes it.
test("callDialogOpensFromUrlAndBackCloses", async () => {
  const h = host();
  assert.equal(h.dialog.isOpen(), false);
  h.clickTo(0);
  await tick();
  assert.equal(h.dialog.isOpen(), true);
  assert.equal(h.root.open, true);
  assert.match(h.text(), /Call #1/);
  h.clickTo(1);
  await tick();
  assert.match(h.text(), /Call #2/);
  h.hist.stack.pop(); // the browser's Back
  h.hist.stack.pop();
  h.dialog.sync({ sessionId: "s1", callId: null });
  assert.equal(h.dialog.isOpen(), false);
  h.dialog.openCall("a", h.heading);
  assert.deepEqual(h.hist.moves.at(-1), ["push", { name: "session", id: "s1", call: "a" }]);

  // restored from a reload: the dialog opens with no opener, and closing replaces the URL
  const r = host({ start: ["a"] });
  await tick();
  assert.equal(r.dialog.isOpen(), true);
  r.dialog.close();
  assert.deepEqual(r.hist.moves.at(-1), ["replace", { name: "session", id: "s1", call: null }]);
  assert.equal(r.dialog.isOpen(), false);
});

// E3 (carries unknownCallShowsNoSuchCallNote): a call the server does not have closes the
// dialog and the page shows the "no such call" note.
test("missingCallShowsNote", async () => {
  let noted = 0;
  const h = host({ start: ["zzz"], load: async () => { const e = new Error("no such call in this session"); e.status = 404; throw e; }, missing: () => noted++ });
  await tick();
  assert.equal(h.dialog.isOpen(), false, "no dialog for a call that is not there");
  assert.equal(noted, 1, "the page is told, so it shows the note");
});

// AC21 (carries drawerIsModalWithAFixedTop): the dialog is modal — opened with showModal(),
// so the page behind is dimmed and inert; a click on the dimmed page closes it, as Esc does;
// its top (title, facts, context line, message counts) stays while the rest scrolls.
test("callDialogIsModalWithAFixedTop", async () => {
  const doc = globalThis.document;
  let modal = 0;
  const realCreate = doc.createElement;
  doc.createElement = (tag) => {
    const e = realCreate(tag);
    if (tag === "dialog") {
      e.showModal = () => { modal++; e.open = true; };
      e.close = () => { e.open = false; };
    }
    return e;
  };
  try {
    const h = host({ start: [null, "b"] });
    await tick();
    assert.equal(modal, 1, "opened with showModal, which makes the page behind inert");
    assert.equal(globalThis.document.activeElement.attrs["aria-label"], "Close call", "focus starts on Close");
    const top = withClass(h.root, "call-top")[0];
    const body = withClass(h.root, "mb")[0];
    const topText = textOf(top).replace(/\s+/g, " ");
    assert.match(topText, /Model m1/);
    assert.match(topText, /Context 160 · \+60 since #1/);
    assert.match(topText, /2 carried over · 1 new/);
    assert.equal(withClass(top, "call-messages").length, 0, "the messages are not in the top");
    assert.equal(withClass(body, "call-messages").length, 1, "the messages scroll");
    assert.match(textOf(body), /all done/, "later sections scroll too");

    const scrim = withClass(h.root, "call-scrim")[0];
    assert.equal(scrim.tagName, "button", "the dimmed page is a real button");
    assert.equal(scrim.attrs.tabindex, "-1", "out of the Tab order: Close and Esc do the same");
    scrim.fire("click");
    assert.deepEqual(h.hist.moves.at(-1), ["replace", { name: "session", id: "s1", call: null }], "a click on the dimmed page closes the dialog");
    assert.equal(h.root.open, false, "the page works again once the dialog closes");

    const e = host({ start: [null, "b"] });
    await tick();
    let prevented = false;
    e.root.fire("cancel", { preventDefault: () => { prevented = true; } });
    assert.equal(prevented, true, "Esc does not close the <dialog> behind the URL's back");
    assert.equal(e.dialog.isOpen(), false, "Esc closes it through the URL");
  } finally {
    doc.createElement = realCreate;
  }
});

// carries arrowsStepAndEscReturnsFocus, focusFallsToTurnHeading: closing returns focus to
// the link that opened the dialog, or to the turn heading when a redraw dropped that link.
test("closeReturnsFocusToTheOpener", async () => {
  const h = host();
  h.clickTo(1);
  await tick();
  h.dialog.close();
  assert.deepEqual(h.hist.moves.at(-1), ["back"]);
  assert.equal(h.dialog.isOpen(), false);
  assert.equal(globalThis.document.activeElement, h.links[1]);

  const g = host();
  g.clickTo(0);
  await tick();
  g.links[0].isConnected = false; // a redraw dropped the opener
  g.dialog.close();
  assert.equal(globalThis.document.activeElement, g.heading);
});

// carries closeFromInAppOpenLandsOnSessionUrl, closeAfterSteppingLandsOnSessionUrl,
// closeOfReloadRestoredDrawerStaysInTheApp, closeAfterOpeningAnotherCallWhileOpenLandsOnSessionUrl:
// closing always lands on the session's URL, without leaving the app.
test("closeAlwaysLandsOnTheSessionUrl", async () => {
  const opened = host();
  opened.clickTo(0);
  await tick();
  opened.dialog.close();
  assert.equal(opened.top(), null);
  assert.equal(opened.dialog.isOpen(), false);

  const stepped = host();
  stepped.clickTo(0);
  await tick();
  stepped.dialog.step(1);
  await tick();
  stepped.dialog.step(1);
  await tick();
  stepped.dialog.step(-1);
  await tick();
  assert.equal(stepped.top(), "b");
  stepped.dialog.close();
  assert.equal(stepped.top(), null);

  const reload = host({ start: ["b"] }); // a reload or pasted URL: the only entry is the call
  await tick();
  reload.dialog.close();
  assert.equal(reload.top(), null);
  assert.equal(reload.hist.stack.length, 1, "closing must not leave the app");
  const reloadStepped = host({ start: ["a"] });
  await tick();
  reloadStepped.dialog.step(1);
  await tick();
  reloadStepped.dialog.close();
  assert.deepEqual(reloadStepped.hist.stack, [null]);

  const twice = host();
  twice.clickTo(0);
  await tick();
  twice.clickTo(2); // a link clicked with the dialog already open
  await tick();
  twice.dialog.close();
  assert.equal(twice.top(), null);
  const restored = host({ start: ["a"] });
  await tick();
  restored.clickTo(2);
  await tick();
  restored.dialog.close();
  assert.equal(restored.top(), null);
});

test("contextAndCountWords", () => {
  assert.equal(changeText({ first: true }), "first call of its thread");
  assert.equal(changeText({ delta: 60, prev_n: 1 }), "+60 since #1");
  assert.equal(changeText({ delta: null }), "—");
  assert.equal(sawText({ carried: 2, new: 1 }), "2 carried over · 1 new");
  assert.equal(sawText({ first: true, carried: 0, new: 3 }), "First call of its thread · 3 new");
  assert.equal(sawText({ carried: null, new: null }), "Messages: —");
});

// open shows one call's details in a fresh dialog and waits for them to land.
async function openOne(d, extra = {}) {
  const h = host({ start: [null, d.exchange_id], load: async () => d, ...extra });
  await tick();
  return h;
}
const flat = (h) => h.text().replace(/:\s+/g, ": ");

// AC6, AC7 (carries contextPartsAndDelta, newThreadMarked): the facts, then Context with its
// size, change since the previous call of its thread, why, and the three counters.
test("callDialogShowsFactsAndContext", async () => {
  const h = await openOne(detail("b"));
  const t = h.text();
  for (const re of [/Model m1/, /Duration 1\.0s/, /Ended end turn/, /Thread main/, /Context 160/]) assert.match(t, re);
  assert.match(t, /Model call · turn 1/);
  assert.match(t, /Call #2/);
  assert.match(t, /2 of 3 · main/);
  const ctx = byTag(h.root, "section").find((s) => /^Context/.test(textOf(s).trim()));
  const c = textOf(ctx).replace(/\s+/g, " ");
  for (const re of [/Size 160/, /Change \+60 since #1/, /Input 120/, /Cache write 30/, /Cache read 10/, /Output 5/, /added #1's reply/]) assert.match(c, re);
  const first = await openOne(detail("a"));
  assert.match(first.text(), /first call of its thread/);
  assert.match(first.text(), /First call of its thread · 1 new/);
});

// AC8 (carries messagesMarkedCarriedOrNew): what the call saw, marked carried or new with
// the counts; thinking behind a toggle; the response; each tool call opens its tool dialog.
test("callDialogShowsSawThinkingResponse", async () => {
  const opened = [];
  const t1 = { id: "t1", name: "Bash", kind: "command", state: { glyph: "✓", word: "ok" }, chip: "ok", summary: "npm test", dur_ms: 400 };
  const d = detail("b", { thinking: true, tools: [t1] });
  const h = host({ start: [null, "b"], load: async () => d });
  await tick();
  assert.match(h.text(), /2 carried over · 1 new/);
  assert.equal(withClass(h.root, "is-carried").length, 2);
  assert.equal(withClass(h.root, "is-new").length, 1);
  assert.match(h.text(), /all done/);
  assert.match(h.text(), /stop: end turn/);
  // a reply of tool calls alone, and messages with no text, say so instead of showing blank
  const toolsOnly = await openOne(detail("b", { tools: [t1], response: { text: "", stop_reason: "tool use" },
    saw: { first: false, carried: 1, new: 1, messages: [{ role: "assistant", text: "", new: false }, { role: "user", text: "", new: true }] } }));
  assert.match(toolsOnly.text(), /No text in the reply: it asked for the tool calls below\./);
  assert.equal(withClass(toolsOnly.root, "call-reply").length, 0, "no empty Preview / Raw block");
  assert.equal((toolsOnly.text().match(/no text: tool calls, tool results or thinking only/g) || []).length, 2);
  const toggle = byTag(h.root, "button").find((b) => b.textContent === "Show thinking");
  const box = withClass(h.root, "call-thinking")[0];
  assert.equal(box.hidden, true, "thinking starts folded");
  toggle.fire("click");
  assert.equal(box.hidden, false);
  assert.match(textOf(box), /raw response/);
  // a tool the call asked for opens its 016 tool dialog
  const g = host({ start: [null, "b"], load: async () => d });
  await tick();
  const dlg = createCallDialog({ nav: { push() {}, replace() {}, back() {} }, loadCall: async () => d, openTool: (id, from) => opened.push([id, from]) });
  dlg.sync({ sessionId: "s1", callId: "b" });
  await tick();
  const name = byTag(dlg.el, "button").find((b) => b.textContent === "Bash");
  name.fire("click");
  assert.deepEqual(opened.map((o) => o[0]), ["t1"]);
  assert.equal(opened[0][1], name, "the tool dialog returns focus to its row");
  assert.match(textOf(g.root), /1 asked for/);
});

// AC8, research D41: the reply is capped like a message box; "Show all" expands it in
// place and "Show less" caps it again; the cut-short link stays; the expanded reply holds
// through a live redraw of the same call and caps again on the next call; a reply that
// fits its box has no toggle.
test("replyIsCappedWithShowAll", async () => {
  const long = detail("b", { response: { text: "line\n".repeat(400), truncated: true, stop_reason: "end turn" } });
  const dlg = createCallDialog({ nav: { push() {}, replace() {}, back() {} }, loadCall: async (id) => (id === "b" ? long : detail(id)) });
  dlg.sync({ sessionId: "s1", callId: "b" });
  await tick();
  const reply = () => withClass(dlg.el, "call-reply")[0];
  const all = () => byTag(dlg.el, "button").find((b) => /^Show (all|less)$/.test(b.textContent));
  assert.ok(reply().className.split(" ").includes("is-capped"), "the reply starts capped");
  assert.equal(all().textContent, "Show all");
  assert.equal(all().attrs["aria-expanded"], "false");
  all().fire("click");
  assert.ok(!reply().className.split(" ").includes("is-capped"), "Show all expands it in place");
  assert.equal(all().textContent, "Show less");
  assert.equal(all().attrs["aria-expanded"], "true");
  assert.ok(byTag(dlg.el, "a").some((a) => /cut short/.test(a.textContent) && a.href === "/api/exchanges/b/body?part=response"), "the cut-short link stays");
  dlg.refresh();
  await tick();
  assert.ok(!reply().className.split(" ").includes("is-capped"), "a live redraw keeps it expanded");
  assert.equal(all().textContent, "Show less");
  all().fire("click");
  assert.ok(reply().className.split(" ").includes("is-capped"), "Show less caps it again");
  all().fire("click");
  dlg.sync({ sessionId: "s1", callId: "a" });
  await tick();
  assert.ok(reply().className.split(" ").includes("is-capped"), "the next call starts capped");
  assert.equal(all().hidden, false, "without layout the toggle stays");
  // with layout: a reply that fits its box has no toggle; one that overflows keeps it
  const proto = Object.getPrototypeOf(reply());
  let scroll = 100;
  Object.defineProperty(proto, "scrollHeight", { configurable: true, get: () => scroll });
  Object.defineProperty(proto, "clientHeight", { configurable: true, get: () => 100 });
  try {
    dlg.refresh();
    await tick();
    assert.equal(all().hidden, true, "a reply that fits has no toggle");
    scroll = 900;
    dlg.refresh();
    await tick();
    assert.equal(all().hidden, false, "a reply taller than its cap keeps the toggle");
  } finally {
    delete proto.scrollHeight;
    delete proto.clientHeight;
  }
});

// AC9 (carries healthShowsDisconnectAbortError, rawLinksAndFullPageLink): health in words,
// and the raw request, response stream, headers and full page linked.
test("callDialogHealthAndRaw", async () => {
  const sick = detail("b", { health: { status: "200 OK", ok: false, items: [
    { tone: "warn", text: "The client disconnected before the reply finished." },
    { tone: "bad", text: "The provider's reply stopped before it finished." },
    { tone: "bad", text: "Gateway error: upstream_timeout." },
  ] } });
  const h = await openOne(sick);
  for (const re of [/client disconnected/, /reply stopped/, /Gateway error: upstream_timeout/, /Status 200 OK/]) assert.match(h.text(), re);
  assert.equal(withClass(h.root, "tone-bad").length, 2);
  const hrefs = byTag(h.root, "a").map((a) => a.href);
  for (const want of ["/api/exchanges/b/body?part=request", "/api/exchanges/b/body?part=response", "#/exchanges/b"]) assert.ok(hrefs.includes(want), want);
  assert.match(h.text(), /Headers/);
  assert.match(h.text(), /Full exchange page/);
  const well = await openOne(detail("a"));
  assert.match(well.text(), /No disconnect, abort or error/);
});

// AC11 (carries missingSectionStatesReason, noUsageCallShowsDashesAndNoDelta): a section
// without data says why and shows "—"; the other sections and the raw links still work; a
// failed load leaves the raw links.
test("callDialogMissingDataSaysWhy", async () => {
  const d = detail("b", {
    facts: { model: "m1", dur_ms: 1000, start: "2026-01-01T00:00:01Z", stop: { word: "500 Internal Server Error", bad: true }, thread: { kind: "main", label: "main" }, ctx: null },
    context: { size: null, delta: null, first: false, input: null, cache_write: null, cache_read: null, output: null, note: "No usage reported: the call failed before the provider counted tokens." },
    saw: { first: false, carried: null, new: null, messages: [], unavailable: "The request body was not stored." },
    response: { unavailable: "The stored body is cut at the capture limit, so it could not be read in full." },
    raw: { request_missing: "The request body was not stored.", response: "/api/exchanges/b/body?part=response", headers: "#/exchanges/b", page: "#/exchanges/b" },
  });
  const h = await openOne(d);
  const t = flat(h);
  for (const re of [/Context —/, /Size —/, /Input —/, /Output —/, /Change —/, /No usage reported/, /request body was not stored/, /Messages: —/, /cut at the capture limit/]) assert.match(t, re);
  assert.doesNotMatch(textOf(withClass(h.root, "call-top")[0]), /— · —/, "the top line leaves out a change it does not know");
  assert.doesNotMatch(t, /Size 0|Input 0/);
  assert.doesNotMatch(t, /[+−-]\d+ since/);
  assert.equal(withClass(h.root, "bad").length, 1, "the error status is marked bad");
  assert.ok(byTag(h.root, "a").some((a) => a.href === "/api/exchanges/b/body?part=response"), "the stored response still links");
  // a reply cut at the limit says so and links the whole raw response
  const cut = await openOne(detail("b", { response: { text: "the start of it", truncated: true, stop_reason: "end turn" } }));
  const more = byTag(cut.root, "a").find((a) => /cut short/.test(a.textContent));
  assert.equal(more.href, "/api/exchanges/b/body?part=response");
  assert.equal(byTag((await openOne(detail("a"))).root, "a").filter((a) => /cut short/.test(a.textContent)).length, 0, "a whole reply has no such link");
  const failed = host({ start: [null, "b"], load: async () => { throw new Error("boom"); } });
  await tick();
  assert.match(failed.text(), /Call details unavailable: boom/);
  assert.ok(byTag(failed.root, "a").some((a) => a.href === "#/exchanges/b"), "the raw links need only the id");
});

// E2: a side call shows its kind as its thread, and its failure is a word, not red.
test("sideCallIsNotRed", async () => {
  const d = detail("a", { turn: 0, facts: { model: "m1", dur_ms: 100, start: "", stop: { word: "429 Too Many Requests" }, thread: { kind: "side", label: "quota probe" }, ctx: 10 } });
  const h = await openOne(d);
  assert.match(h.text(), /Side call/);
  assert.match(h.text(), /Thread quota probe/);
  assert.match(h.text(), /429 Too Many Requests/);
  assert.equal(withClass(h.root, "bad").length, 0, "not marked bad");
});

// AC10 (carries arrowsStepAndEscReturnsFocus's steps): ↑/↓ and Previous/Next move through the
// session's calls in order, replacing the URL so Back still closes the dialog.
test("arrowsStepThroughCalls", async () => {
  const h = host();
  h.clickTo(1);
  await tick();
  h.dialog.step(1);
  assert.deepEqual(h.hist.moves.at(-1), ["replace", { name: "session", id: "s1", call: "c" }]);
  await tick();
  assert.match(h.text(), /Call #3/);
  h.btn("Previous").fire("click");
  await tick();
  h.btn("Previous").fire("click");
  await tick();
  assert.deepEqual(h.hist.moves.at(-1), ["replace", { name: "session", id: "s1", call: "a" }]);
  assert.match(h.text(), /Call #1/);
  assert.equal(h.hist.moves.filter((m) => m[0] === "push").length, 0, "a step never adds history");
  h.dialog.close();
  assert.equal(h.top(), null, "Back-style close still lands on the session");
});

// E4 (carries firstAndLastStepDisabled): Previous is disabled on the first call and Next on
// the last; a step past either end does nothing.
test("stepsDisabledAtTheEnds", async () => {
  const h = host({ start: [null, "a"] });
  await tick();
  assert.equal(h.btn("Previous").disabled, true);
  assert.equal(h.btn("Next").disabled, false);
  const n = h.hist.moves.length;
  h.dialog.step(-1);
  assert.equal(h.hist.moves.length, n);
  h.dialog.step(1);
  await tick();
  h.dialog.step(1);
  await tick();
  assert.match(h.text(), /Call #3/);
  assert.equal(h.btn("Next").disabled, true);
  assert.equal(h.btn("Previous").disabled, false);
  const m = h.hist.moves.length;
  h.dialog.step(1);
  assert.equal(h.hist.moves.length, m);
  // while a call loads, neither step works: its place is not known yet
  const slow = host({ start: [null, "b"], load: () => new Promise(() => {}) });
  assert.equal(slow.btn("Previous").disabled, true);
  assert.equal(slow.btn("Next").disabled, true);
});
