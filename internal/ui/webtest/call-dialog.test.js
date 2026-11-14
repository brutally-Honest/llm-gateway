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
