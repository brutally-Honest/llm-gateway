import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { StubElement, all, byTag, installDom, textOf, withClass } from "./dom-stub.js";

// Spec 029 through the real app.js, api.js and pages: only the browser is stubbed. The
// server's filtering is the server's (Go tests); here the page must send kind, q and after,
// keep them in the URL, drop old answers, say what an empty page means, merge live events
// and show a skeleton while each part loads.
installDom();
const nodes = new Map();
const byId = (id) => {
  if (!nodes.has(id)) nodes.set(id, new StubElement("div"));
  return nodes.get(id);
};
// the stub has no selectors; app.js asks for "input" and the page for the selected row
StubElement.prototype.querySelector = function (sel) {
  if (sel === "input") return byTag(this, "input")[0] || null;
  if (sel === "tr.selected") return withClass(this, "selected")[0] || null;
  return null;
};
const streams = [];
const listeners = {};
class FakeEventSource {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 2;
  readyState = FakeEventSource.OPEN;
  constructor(url) {
    this.url = url;
    streams.push(this);
  }
  addEventListener(name, fn) {
    (this.handlers ||= {})[name] = fn;
  }
  emit(name, data) {
    this.handlers[name]({ data: JSON.stringify(data) });
  }
  close() {
    this.readyState = FakeEventSource.CLOSED;
  }
}

// the sessions endpoint: every request is recorded; serve(params) answers it (a promise
// holds the answer back), or null for 404
const sessionReqs = [];
let serve = () => ({ items: [], next_after: null, total: 0, scanned: 0 });
const other = { "/api/today": {}, "/api/ungrouped": { items: [], next_after: null } };
const replies = {};
const holds = {}; // path -> promise to wait for before answering
let hash = "#/";
const entries = []; // every hash assigned or replaced, to tell a new entry from a replacement
const location = {
  get hash() {
    return hash;
  },
  set hash(v) {
    entries.push(["push", v]);
    hash = v;
    for (const fn of listeners.hashchange) fn();
  },
  replace() {},
};
const history = {
  state: null,
  back() {},
  replaceState(_s, _t, url) {
    entries.push(["replace", url]);
    hash = url; // no hashchange: replaceState does not fire one
  },
};
Object.assign(document, {
  getElementById: byId,
  documentElement: Object.assign(new StubElement("html"), { scrollHeight: 0 }),
  createDocumentFragment: () => new StubElement("fragment"),
  createTextNode: (text) => Object.assign(new StubElement("text"), { textContent: text }),
});
Object.assign(globalThis, {
  EventSource: FakeEventSource,
  window: { localStorage: null, addEventListener: (name, fn) => (listeners[name] ||= []).push(fn), scrollTo() {}, scrollY: 0, innerHeight: 0 },
  location,
  history,
  fetch: async (url) => {
    const [path, qs] = String(url).split("?");
    if (path === "/api/sessions") {
      const params = new URLSearchParams(qs || "");
      sessionReqs.push(params);
      const body = await serve(params);
      return { ok: body !== null, status: body ? 200 : 500, statusText: "boom", json: async () => (body || { error: { message: "store boom" } }) };
    }
    if (holds[path]) await holds[path];
    const body = replies[path] === undefined ? other[path] : replies[path];
    if (body && body.__status) return { ok: false, status: body.__status, statusText: "nope", json: async () => ({ error: { message: body.message } }) };
    return { ok: Boolean(body), status: body ? 200 : 404, statusText: "not stubbed", json: async () => body };
  },
});

const settle = async () => {
  for (let i = 0; i < 6; i++) await new Promise((r) => setImmediate(r));
};
const sess = (id, title, last_at, o = {}) => ({ id, title, client: "c", models: ["m"], first_at: "2026-01-01T00:00:00Z", last_at, exchange_count: 1, model_calls: 1, main_errors: 0, active: false, usage: {}, tool_states: {}, ...o });
const stamp = (n) => `2026-01-01T00:${String(n).padStart(2, "0")}:00Z`;
const page = () => withClass(byId("app"), "sessions-page")[0];
const ids = () => byTag(byId("app"), "tr").filter((r) => r.dataset && r.dataset.id).map((r) => r.dataset.id);
const text = () => textOf(byId("app"));
const input = () => byTag(page(), "input")[0];
const kindBtn = (label) => byTag(withClass(page(), "seg-group")[0], "button").find((b) => b.textContent === label);
const pagerEl = () => withClass(byId("app"), "sp-pager")[0];
const PREV = "‹ Previous";
const NEXT = "Next ›";
const pagerBtn = (label) => byTag(pagerEl(), "button").find((b) => b.textContent === label);
const pagerLabel = () => textOf(withClass(pagerEl(), "pg-label")[0]);
const box = () => withClass(page(), "sp-list")[0];
const rows = (from, n) => Array.from({ length: n }, (_, i) => sess(`r${from + i}`, `row ${from + i}`, `2026-01-01T01:00:00.${String(999 - from - i).padStart(3, "0")}Z`));
const skeletons = () => withClass(byId("app"), "skel");
const lastReq = () => sessionReqs.at(-1);
const key = (k) => listeners.keydown.at(-1)({ key: k, preventDefault() {} });
const reqObj = (p) => Object.fromEntries(p.entries());
const deferred = () => {
  let resolve;
  const promise = new Promise((r) => (resolve = r));
  return { promise, resolve };
};

// start boots the app once, at #/ ; later tests move around with go().
let booted = false;
async function boot(t) {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  if (!booted) {
    booted = true;
    await import("../web/app.js");
  }
}
// go navigates as the browser does (a hash change), leaving the list first so each test
// starts a fresh list.
async function go(h) {
  hash = h;
  for (const fn of listeners.hashchange) fn();
  await settle();
}
async function fresh(t, h = "#/", answer) {
  await boot(t);
  await go("#/ungrouped");
  serve = answer || (() => ({ items: [], next_after: null, total: 0, scanned: 0 }));
  sessionReqs.length = 0;
  entries.length = 0;
  await go(h);
}

test("firstLoadAsksForPageOneAndShowsASkeletonUntilItArrives", async (t) => {
  // the app's own first load at #/, answered only when the gate opens
  const gate = deferred();
  serve = async () => {
    await gate.promise;
    return { items: [sess("a", "alpha", stamp(2)), sess("b", "beta", stamp(1))], next_after: "t1", total: 90, scanned: 2 };
  };
  await boot(t);
  assert.deepEqual(reqObj(lastReq()), {}, "no kind, q, after or limit on the first page of an unfiltered list");
  assert.match(text(), /Loading sessions…/);
  assert.ok(skeletons().length > 0, "the skeleton shows");
  assert.ok(skeletons().every((s) => s.attrs["aria-hidden"] === "true"));
  assert.ok(all(page()).some((e) => e.attrs.role === "status" && /Loading sessions…/.test(textOf(e))), "the status line stays for screen readers");
  gate.resolve();
  await settle();
  assert.deepEqual(ids(), ["a", "b"]);
  assert.equal(skeletons().length, 0, "replaced by the rows");
  assert.doesNotMatch(text(), /Loading sessions…/);
});

test("firstLoadErrorReplacesTheSkeleton", async (t) => {
  await fresh(t, "#/", () => null);
  assert.match(text(), /store boom/);
  assert.equal(skeletons().length, 0);
  assert.doesNotMatch(text(), /Loading sessions…/);
});

test("kindClickIsAHistoryEntryAndSendsKindToTheServer", async (t) => {
  await fresh(t, "#/", () => ({ items: [sess("a", "alpha", stamp(2))], next_after: null, total: 5, scanned: 5 }));
  const root = page();
  const streamsBefore = streams.length;
  const gate = deferred();
  serve = async (p) => {
    await gate.promise;
    return { items: [sess("act", "active one", stamp(3), { active: true })], next_after: null, total: 5, scanned: 5 };
  };
  kindBtn("Active").fire("click");
  await settle();
  assert.deepEqual(entries, [["push", "#/?kind=active"]], "a new history entry");
  assert.deepEqual(reqObj(lastReq()), { kind: "active" }, "kind is sent, q is not when empty");
  assert.match(text(), /Loading sessions…/, "the skeleton shows after a kind change");
  assert.deepEqual(ids(), [], "the old rows are gone");
  gate.resolve();
  await settle();
  assert.deepEqual(ids(), ["act"]);
  assert.equal(skeletons().length, 0);
  assert.equal(kindBtn("Active").attrs["aria-pressed"], "true");
  assert.equal(kindBtn("All").attrs["aria-pressed"], "false");
  assert.equal(page(), root, "the page is not rebuilt");
  assert.equal(streams.length, streamsBefore, "the stream is not restarted");
});

test("typingSettlesThenReplacesTheEntryAndReloads", async (t) => {
  await fresh(t, "#/?kind=look", () => ({ items: [sess("a", "alpha", stamp(2))], next_after: null, total: 3, scanned: 3 }));
  assert.equal(kindBtn("Needs a look").attrs["aria-pressed"], "true", "the picker follows the URL");
  sessionReqs.length = 0;
  const box = input();
  box.focus();
  for (const v of ["f", "fo", "foo bar"]) {
    box.value = v;
    box.fire("input");
    t.mock.timers.tick(100);
  }
  await settle();
  assert.equal(sessionReqs.length, 0, "nothing is sent while typing");
  assert.deepEqual(entries, []);
  t.mock.timers.tick(300);
  await settle();
  assert.deepEqual(entries, [["replace", "#/?kind=look&q=foo%20bar"]], "one replacement, no new entry");
  assert.equal(sessionReqs.length, 1, "one request once typing settles");
  assert.deepEqual(reqObj(lastReq()), { kind: "look", q: "foo bar" });
  assert.equal(input(), box, "the box is the same one");
  assert.equal(document.activeElement, box, "and it keeps its focus");
  assert.equal(box.value, "foo bar");
});

test("escClearsTheSearchAndTheUrl", async (t) => {
  await fresh(t, "#/?q=zed");
  assert.equal(input().value, "zed");
  sessionReqs.length = 0;
  input().fire("keydown", { key: "Escape" });
  await settle();
  assert.equal(input().value, "");
  assert.deepEqual(entries, [["replace", "#/"]]);
  assert.deepEqual(reqObj(lastReq()), {});
});

test("backRestoresKindAndSearchAndLoadsPageOne", async (t) => {
  const answer = (p) => ({ items: [sess("x", `for ${p.get("kind") || "all"} ${p.get("q") || ""}`, stamp(1))], next_after: null, total: 4, scanned: 4 });
  await fresh(t, "#/", answer);
  kindBtn("Hooks only").fire("click");
  await settle();
  input().value = "needle";
  input().fire("input");
  t.mock.timers.tick(300);
  await settle();
  assert.equal(hash, "#/?kind=hooks&q=needle");
  const root = page();
  const box = input();
  sessionReqs.length = 0;
  // Back: the entry before the replaced one, #/?kind=hooks
  await go("#/?kind=hooks");
  assert.deepEqual(reqObj(lastReq()), { kind: "hooks" });
  assert.equal(box.value, "", "the box follows the URL");
  assert.equal(kindBtn("Hooks only").attrs["aria-pressed"], "true");
  // Back again: #/
  await go("#/");
  assert.deepEqual(reqObj(lastReq()), {});
  assert.equal(kindBtn("All").attrs["aria-pressed"], "true");
  // Forward, and a link with a search and an unknown kind
  await go("#/?kind=bogus&q=a%26b");
  assert.deepEqual(reqObj(lastReq()), { q: "a&b" }, "an unknown kind reads as all");
  assert.equal(box.value, "a&b");
  assert.equal(page(), root, "the page is kept across all of it");
  // a hashchange to the same state loads nothing again
  const n = sessionReqs.length;
  await go("#/?q=a%26b");
  assert.equal(sessionReqs.length, n);
});

test("anOlderQueryAnswerIsDropped", async (t) => {
  await fresh(t);
  sessionReqs.length = 0;
  const first = deferred();
  const second = deferred();
  serve = async (p) => {
    const q = p.get("q");
    if (q === "a") {
      await first.promise;
      return { items: [sess("old", "answer for a", stamp(1))], next_after: null, total: 2, scanned: 2 };
    }
    await second.promise;
    return { items: [sess("new", "answer for ab", stamp(2))], next_after: null, total: 2, scanned: 2 };
  };
  input().value = "a";
  input().fire("input");
  t.mock.timers.tick(300);
  await settle();
  input().value = "ab";
  input().fire("input");
  t.mock.timers.tick(300);
  await settle();
  assert.deepEqual(sessionReqs.map((p) => p.get("q")), ["a", "ab"]);
  second.resolve();
  await settle();
  assert.deepEqual(ids(), ["new"]);
  first.resolve(); // the older query answers last
  await settle();
  assert.deepEqual(ids(), ["new"], "the older answer does not replace it");
  // the same for a page of an older query: Next answered after the query changed is dropped
  const more = deferred();
  serve = async () => ({ items: [sess("n1", "n1", stamp(3))], next_after: "tok", total: 9, scanned: 1 });
  await go("#/?q=zz");
  serve = async (p) => {
    if (p.get("after")) {
      await more.promise;
      return { items: [sess("late", "late", stamp(1))], next_after: null, total: 9, scanned: 1 };
    }
    return { items: [sess("fresh", "fresh", stamp(2))], next_after: null, total: 9, scanned: 1 };
  };
  pagerBtn(NEXT).fire("click");
  await settle();
  await go("#/?q=other");
  more.resolve();
  await settle();
  assert.ok(!ids().includes("late"), "a page of another query is dropped");
  assert.deepEqual(ids(), ["fresh"]);
});

// spec 029 amendment: an older page's answer never replaces a newer page's.
test("aStalePageAnswerIsDropped", async (t) => {
  const answers = { 1: rows(0, 50), 2: rows(50, 50), 3: rows(100, 50) };
  await fresh(t, "#/", () => ({ items: answers[1], next_after: "x", total: 560, scanned: 50 }));
  const slow = deferred();
  serve = async (p) => {
    const k = Number(p.get("offset") || 0) / 50 + 1;
    if (k === 2) await slow.promise;
    return { items: answers[k], next_after: "x", total: 560, scanned: 50 };
  };
  pagerBtn(NEXT).fire("click");
  await settle();
  pagerBtn(NEXT).fire("click");
  await settle();
  assert.equal(ids()[0], "r100", "page 3 is shown");
  slow.resolve(); // page 2 answers last
  await settle();
  assert.equal(ids()[0], "r100", "the older page's answer is dropped");
  assert.equal(pagerLabel(), "Page 3 of 12");
});

// ---- numbered pages (spec 029 amendment, AC8) ----

test("unfilteredNextAndPreviousSendOffsetsAndTheLabelFollows", async (t) => {
  const answer = (p) => ({ items: rows(Number(p.get("offset") || 0), 50), next_after: "x", total: 440, scanned: 50 });
  await fresh(t, "#/", answer);
  assert.equal(pagerEl().tagName, "nav");
  assert.equal(pagerEl().attrs["aria-label"], "Pages");
  assert.equal(pagerLabel(), "Page 1 of 9");
  assert.deepEqual(byTag(pagerEl(), "button").map((b) => b.textContent), [PREV, NEXT]);
  assert.equal(pagerBtn(PREV).disabled, true, "no Previous on page 1");
  assert.deepEqual(reqObj(sessionReqs[0]), {}, "page 1 sends no offset");
  pagerBtn(NEXT).fire("click");
  await settle();
  assert.deepEqual(entries.at(-1), ["push", "#/?page=2"], "a page click is a history entry");
  assert.deepEqual(reqObj(lastReq()), { offset: "50" }, "offset = (2 - 1) * 50, no after");
  assert.equal(pagerLabel(), "Page 2 of 9");
  assert.equal(ids()[0], "r50");
  pagerBtn(PREV).fire("click");
  await settle();
  assert.deepEqual(reqObj(lastReq()), {}, "back on page 1: no offset");
  assert.equal(hash, "#/", "page 1 leaves the page out of the URL");
  assert.equal(pagerLabel(), "Page 1 of 9");
  assert.equal(ids()[0], "r0");
});

test("nextIsDisabledOnTheLastPage", async (t) => {
  const answer = (p) => ({ items: rows(Number(p.get("offset") || 0), 50), next_after: "x", total: 440, scanned: 50 });
  await fresh(t, "#/?page=9", answer);
  assert.equal(pagerLabel(), "Page 9 of 9");
  assert.deepEqual(reqObj(lastReq()), { offset: "400" });
  assert.equal(pagerBtn(NEXT).disabled, true);
  assert.equal(pagerBtn(PREV).disabled, false);
  pagerBtn(PREV).fire("click");
  await settle();
  assert.deepEqual(reqObj(lastReq()), { offset: "350" });
  assert.equal(pagerLabel(), "Page 8 of 9");
  assert.equal(pagerBtn(NEXT).disabled, false);
});

test("skeletonShowsInsideTheBoxWhileAPageLoadsAndThePagerStays", async (t) => {
  await fresh(t, "#/", (p) => ({ items: rows(Number(p.get("offset") || 0), 50), next_after: "x", total: 300, scanned: 50 }));
  const gate = deferred();
  serve = async (p) => {
    await gate.promise;
    return { items: rows(Number(p.get("offset") || 0), 50), next_after: "x", total: 300, scanned: 50 };
  };
  pagerBtn(NEXT).fire("click");
  await settle();
  assert.ok(withClass(box(), "skel").length > 0, "the skeleton is inside the box");
  assert.match(textOf(box()), /Loading sessions…/);
  assert.deepEqual(ids(), []);
  assert.equal(pagerLabel(), "Page 2 of 6", "the pager shows the page being loaded");
  assert.equal(withClass(pagerEl(), "skel").length, 0);
  gate.resolve();
  await settle();
  assert.equal(ids().length, 50);
  assert.equal(skeletons().length, 0);
});

test("aPageClickScrollsTheBoxToItsTop", async (t) => {
  await fresh(t, "#/", (p) => ({ items: rows(Number(p.get("offset") || 0), 50), next_after: "x", total: 300, scanned: 50 }));
  box().scrollTop = 500;
  pagerBtn(NEXT).fire("click");
  await settle();
  assert.equal(box().scrollTop, 0);
});

test("filteredPagesSendAfterTokensPreviousUsesTheStoredStartAndNextEndsWithTheLastPage", async (t) => {
  const chain = { "": ["t1", 0], t1: ["t2", 1], t2: [null, 2] };
  const answer = (p) => {
    const [next, i] = chain[p.get("after") || ""];
    return { items: [sess(`f${i}`, `f${i}`, stamp(30 - i))], next_after: next, total: 900, scanned: 40 };
  };
  await fresh(t, "#/?kind=active&q=p", answer);
  assert.deepEqual(reqObj(lastReq()), { kind: "active", q: "p" }, "page 1 has no after");
  assert.equal(pagerLabel(), "Page 1");
  pagerBtn(NEXT).fire("click");
  await settle();
  assert.equal(hash, "#/?kind=active&q=p&page=2");
  assert.deepEqual(reqObj(lastReq()), { kind: "active", q: "p", after: "t1" });
  assert.equal(pagerLabel(), "Page 2");
  assert.deepEqual(ids(), ["f1"]);
  pagerBtn(NEXT).fire("click");
  await settle();
  assert.deepEqual(reqObj(lastReq()), { kind: "active", q: "p", after: "t2" });
  assert.equal(pagerLabel(), "Page 3");
  assert.equal(pagerBtn(NEXT).disabled, true, "no next_after: Next goes");
  // Previous is one request, from the stored start of the page before
  const n = sessionReqs.length;
  pagerBtn(PREV).fire("click");
  await settle();
  assert.equal(sessionReqs.length, n + 1);
  assert.deepEqual(reqObj(lastReq()), { kind: "active", q: "p", after: "t1" });
  assert.equal(pagerLabel(), "Page 2");
  assert.deepEqual(ids(), ["f1"]);
  pagerBtn(PREV).fire("click");
  await settle();
  assert.equal(sessionReqs.length, n + 2);
  assert.deepEqual(reqObj(lastReq()), { kind: "active", q: "p" });
  assert.equal(pagerLabel(), "Page 1");
  assert.equal(pagerBtn(PREV).disabled, true);
});

test("pageIsInTheUrlAndReloadOfAnUnfilteredPageThreeSendsOffset100", async (t) => {
  const answer = (p) => ({ items: rows(Number(p.get("offset") || 0), 50), next_after: "x", total: 560, scanned: 50 });
  await fresh(t, "#/?page=3", answer);
  assert.equal(sessionReqs.length, 1, "one request");
  assert.deepEqual(reqObj(lastReq()), { offset: "100" });
  assert.equal(ids()[0], "r100");
  assert.equal(pagerLabel(), "Page 3 of 12");
  assert.deepEqual(entries, [], "no entry is added for what the URL already says");
  // not a positive integer reads as page 1
  await go("#/?page=abc");
  assert.deepEqual(reqObj(lastReq()), {});
  assert.equal(pagerLabel(), "Page 1 of 12");
});

test("reloadOfPageThreeOfNineShowsItsLabelAndOffset100", async (t) => {
  const answer = (p) => ({ items: rows(Number(p.get("offset") || 0), 50), next_after: "x", total: 440, scanned: 50 });
  await fresh(t, "#/?page=3", answer);
  assert.equal(pagerLabel(), "Page 3 of 9");
  assert.deepEqual(reqObj(lastReq()), { offset: "100" });
});

test("aPageBeyondTheLastIsCorrectedToTheLastPageAndTheUrl", async (t) => {
  const answer = (p) => {
    const off = Number(p.get("offset") || 0);
    return { items: off >= 150 ? [] : rows(off, 50), next_after: off + 50 < 150 ? "x" : null, total: 150, scanned: 50 };
  };
  await fresh(t, "#/?page=99", answer);
  assert.deepEqual(sessionReqs.map((p) => p.get("offset")), ["4900", "100"], "asked, then the last page");
  assert.deepEqual(entries, [["replace", "#/?page=3"]], "the URL is corrected without a history entry");
  assert.equal(hash, "#/?page=3");
  assert.equal(ids()[0], "r100");
  assert.equal(pagerLabel(), "Page 3 of 3");
  // an empty store: one page, no pager, the URL goes back to page 1
  serve = () => ({ items: [], next_after: null, total: 0, scanned: 0 });
  sessionReqs.length = 0;
  entries.length = 0;
  await go("#/?page=4");
  assert.match(text(), /No sessions captured yet\./);
  assert.equal(pagerEl().hidden, true);
  assert.deepEqual(entries.at(-1), ["replace", "#/"]);
});

test("aFilteredPageInTheUrlWalksPagesOneToN", async (t) => {
  const chain = { "": ["t1", 0], t1: ["t2", 1], t2: ["t3", 2], t3: [null, 3] };
  const answer = (p) => {
    const [next, i] = chain[p.get("after") || ""];
    return { items: [sess(`w${i}`, `w${i}`, stamp(30 - i))], next_after: next, total: 900, scanned: 10 };
  };
  const gate = deferred();
  await fresh(t, "#/", () => ({ items: [], next_after: null, total: 0, scanned: 0 }));
  serve = async (p) => {
    if (p.get("after") === "t1") await gate.promise;
    return answer(p);
  };
  sessionReqs.length = 0;
  entries.length = 0;
  await go("#/?kind=active&page=3");
  assert.deepEqual(sessionReqs.map((p) => p.get("after")), [null, "t1"], "in order, the second held back");
  assert.ok(skeletons().length > 0, "the skeleton shows during the walk");
  assert.deepEqual(ids(), []);
  gate.resolve();
  await settle();
  assert.deepEqual(sessionReqs.map((p) => p.get("after")), [null, "t1", "t2"], "three requests for page 3");
  assert.deepEqual(ids(), ["w2"]);
  assert.equal(pagerLabel(), "Page 3");
  assert.deepEqual(entries, [], "the URL already says page 3");
  // the pages run out first: the last page reached is shown and the URL says so
  await go("#/?kind=active&page=9");
  assert.deepEqual(sessionReqs.slice(-4).map((p) => p.get("after")), [null, "t1", "t2", "t3"]);
  assert.deepEqual(ids(), ["w3"]);
  assert.deepEqual(entries.at(-1), ["replace", "#/?kind=active&page=4"]);
  assert.equal(pagerLabel(), "Page 4");
  assert.equal(pagerBtn(NEXT).disabled, true);
});

test("aKindOrSearchChangeGoesBackToPageOne", async (t) => {
  const answer = (p) => ({ items: rows(Number(p.get("offset") || 0), 50), next_after: "x", total: 560, scanned: 50 });
  await fresh(t, "#/?page=4", answer);
  assert.equal(pagerLabel(), "Page 4 of 12");
  sessionReqs.length = 0;
  kindBtn("Active").fire("click");
  await settle();
  assert.equal(hash, "#/?kind=active", "no page in the URL");
  assert.deepEqual(reqObj(lastReq()), { kind: "active" }, "no after, no offset");
  assert.equal(pagerLabel(), "Page 1");
  // a search typed on a later page
  serve = (p) => ({ items: [sess(`s${p.get("after") || 0}`, "s", stamp(3))], next_after: p.get("after") ? null : "t1", total: 9, scanned: 5 });
  await go("#/?kind=active&page=2");
  assert.equal(pagerLabel(), "Page 2");
  input().value = "needle";
  input().fire("input");
  t.mock.timers.tick(300);
  await settle();
  assert.equal(hash, "#/?kind=active&q=needle");
  assert.deepEqual(reqObj(lastReq()), { kind: "active", q: "needle" });
  assert.equal(pagerLabel(), "Page 1");
});

test("emptyStatesFollowTheServersAnswer", async (t) => {
  await fresh(t, "#/", () => ({ items: [], next_after: null, total: 0, scanned: 0 }));
  assert.match(text(), /No sessions captured yet\./);
  // a filter that nothing matches, every row tested
  serve = () => ({ items: [], next_after: null, total: 12, scanned: 12 });
  await go("#/?kind=look");
  assert.match(text(), /no sessions match/);
  assert.doesNotMatch(text(), /No sessions captured yet/);
  // a short page with more to test: say so, keep Next to go on, sum what was scanned
  serve = (p) => (p.get("after") ? { items: [], next_after: "t2", total: 2000, scanned: 500 } : { items: [], next_after: "t1", total: 2000, scanned: 500 });
  await go("#/?q=rare");
  assert.match(text(), /No matches in the newest 500 sessions yet/);
  assert.ok(pagerBtn(NEXT), "Next is there to continue");
  pagerBtn(NEXT).fire("click");
  await settle();
  assert.match(text(), /No matches in the newest 1000 sessions yet/, "summed across pages");
  serve = () => ({ items: [sess("hit", "rare hit", stamp(1))], next_after: null, total: 2000, scanned: 40 });
  pagerBtn(NEXT).fire("click");
  await settle();
  assert.deepEqual(ids(), ["hit"]);
  assert.doesNotMatch(text(), /No matches in the newest/);
});

test("keysWorkOverTheRowsShown", async (t) => {
  await fresh(t, "#/?kind=active", () => ({ items: [sess("a", "a", stamp(2)), sess("b", "b", stamp(1))], next_after: null, total: 9, scanned: 9 }));
  document.activeElement = null;
  key("ArrowDown");
  key("ArrowDown");
  key("Enter");
  assert.equal(hash, "#/sessions/b");
  await go("#/?kind=active");
  key("/");
  assert.equal(document.activeElement, input(), "/ focuses the search box");
});

test("liveEventInAFilteredListUpdatesInPlaceAndRefetchesPageOneForANewSession", async (t) => {
  const rowA = sess("a", "alpha", stamp(5), { active: true });
  const rowB = sess("b", "beta", stamp(4), { active: true });
  await fresh(t, "#/?kind=active", () => ({ items: [rowA, rowB], next_after: "t1", total: 9, scanned: 9 }));
  const stream = streams.at(-1);
  sessionReqs.length = 0;
  // a session the list holds: in place, no request
  stream.emit("session", { ...rowA, title: "alpha renamed", last_at: stamp(6) });
  t.mock.timers.tick(1000);
  await settle();
  assert.deepEqual(ids(), ["a", "b"]);
  assert.match(text(), /alpha renamed/);
  assert.equal(sessionReqs.length, 0, "the server is not asked for a session already held");
  // a session the list does not hold: after the burst settles, page 1 is read again
  serve = () => ({ items: [sess("c", "gamma new", stamp(9), { active: true }), { ...rowA, title: "alpha renamed", last_at: stamp(6) }, rowB], next_after: "other", total: 10, scanned: 3 });
  stream.emit("session", sess("c", "gamma new", stamp(9), { active: true }));
  stream.emit("session", sess("c", "gamma new", stamp(9), { active: true }));
  t.mock.timers.tick(400);
  await settle();
  assert.equal(sessionReqs.length, 0, "debounced");
  t.mock.timers.tick(200);
  await settle();
  assert.equal(sessionReqs.length, 1, "one refetch for the burst");
  assert.deepEqual(reqObj(lastReq()), { kind: "active" }, "page 1 for the current kind and q");
  assert.deepEqual(ids(), ["c", "a", "b"]);
  t.mock.timers.tick(5000); // end the cooldown
  // later pages: in place only, and no refetch for a session they do not hold
  serve = () => ({ items: [sess("old", "older page", stamp(1), { active: true })], next_after: null, total: 10, scanned: 5 });
  pagerBtn(NEXT).fire("click");
  await settle();
  assert.deepEqual(reqObj(lastReq()), { kind: "active", after: "other" });
  assert.deepEqual(ids(), ["old"]);
  sessionReqs.length = 0;
  stream.emit("session", sess("old", "older renamed", stamp(2), { active: true }));
  stream.emit("session", sess("z", "unseen", stamp(10), { active: true }));
  t.mock.timers.tick(10000);
  await settle();
  assert.deepEqual(ids(), ["old"], "no insert");
  assert.match(text(), /older renamed/, "updated in place");
  assert.equal(sessionReqs.length, 0, "no page-1 refetch while on a later page");
});

test("liveEventOnUnfilteredPageOneInsertsAtTheTopTrimsTo50AndTotalMovesThePageCount", async (t) => {
  await fresh(t, "#/", () => ({ items: rows(0, 50), next_after: "x", total: 100, scanned: 50 }));
  assert.equal(pagerLabel(), "Page 1 of 2");
  sessionReqs.length = 0;
  streams.at(-1).emit("session", sess("n", "brand new", "2026-01-01T02:00:00Z"));
  assert.equal(ids().length, 50, "trimmed to the page size");
  assert.equal(ids()[0], "n", "at the top");
  assert.ok(!ids().includes("r49"), "the last row leaves the page");
  serve = () => ({ items: [sess("n", "brand new", "2026-01-01T02:00:00Z"), ...rows(0, 49)], next_after: "x", total: 101, scanned: 50 });
  t.mock.timers.tick(500);
  await settle();
  assert.equal(sessionReqs.length, 1, "one request for the total");
  assert.deepEqual(reqObj(lastReq()), { limit: "1" }, "one row, for the total only");
  assert.equal(pagerLabel(), "Page 1 of 3", "101 sessions are three pages");
  assert.equal(ids().length, 50);
  // a session the page holds is an in-place update, with no request
  t.mock.timers.tick(5000);
  sessionReqs.length = 0;
  streams.at(-1).emit("session", sess("r3", "renamed", "2026-01-01T01:00:00.996Z"));
  t.mock.timers.tick(10000);
  await settle();
  assert.equal(sessionReqs.length, 0);
  assert.match(text(), /renamed/);
});

test("liveEventOnUnfilteredPageTwoUpdatesInPlaceWithNoInsertsAndTotalMovesThePageCount", async (t) => {
  await fresh(t, "#/?page=2", () => ({ items: rows(50, 50), next_after: "x", total: 560, scanned: 50 }));
  assert.equal(pagerLabel(), "Page 2 of 12");
  const before = ids();
  sessionReqs.length = 0;
  streams.at(-1).emit("session", sess("n", "brand new", "2026-01-01T02:00:00Z"));
  streams.at(-1).emit("session", sess("r50", "r50 renamed", "2026-01-01T01:00:00.949Z"));
  assert.deepEqual(ids(), before, "nothing inserted, nothing moved");
  assert.match(text(), /r50 renamed/, "a row on screen updates in place");
  serve = () => ({ items: rows(50, 50), next_after: "x", total: 700, scanned: 50 });
  t.mock.timers.tick(500);
  await settle();
  assert.deepEqual(reqObj(lastReq()), { offset: "50", limit: "1" }, "one row is read, for the total only");
  assert.deepEqual(ids(), before, "the rows do not shift under the reader");
  assert.equal(pagerLabel(), "Page 2 of 14", "the page count follows the total (700 is 14 pages)");
  t.mock.timers.tick(5000);
});

// a filtered dataset served by after-token = id of last row
function makeServer(data) {
  return (p) => {
    const after = p.get("after");
    const i = after ? data.findIndex((x) => x.id === after) + 1 : 0;
    const items = data.slice(i, i + 50);
    const more = i + 50 < data.length;
    return { items, next_after: more ? items.at(-1).id : null, total: data.length, scanned: items.length };
  };
}

test("aHeldRowWithNewActivityKeepsItsPlaceOnPageTwo", async (t) => {
  await fresh(t, "#/?page=2", () => ({ items: rows(50, 50), next_after: "x", total: 560, scanned: 50 }));
  const before = ids();
  const mid = before.indexOf("r80");
  streams.at(-1).emit("session", sess("r80", "r80 busy", "2026-01-01T02:00:00Z"));
  assert.equal(ids().indexOf("r80"), mid, "the row did not move");
  assert.deepEqual(ids(), before);
  assert.match(text(), /r80 busy/, "but it updated in place");
  t.mock.timers.tick(6000);
});

test("aHeldRowWithNewActivityKeepsItsPlaceOnAFilteredPageToo", async (t) => {
  const data = rows(0, 150).map((r) => ({ ...r, active: true }));
  await fresh(t, "#/?kind=active", makeServer(data));
  pagerBtn(NEXT).fire("click");
  await settle();
  const f = ids();
  streams.at(-1).emit("session", { ...data[70], title: "busy", last_at: "2026-01-01T02:00:00Z" });
  assert.deepEqual(ids(), f, "a filtered page does not re-sort either");
  t.mock.timers.tick(6000);
});

test("aFilteredPageOneRefetchDropsTheStaleStartsSoNextSkipsNoRow", async (t) => {
  let data = rows(0, 150).map((r) => ({ ...r, active: true }));
  await fresh(t, "#/?kind=active", (p) => makeServer(data)(p));
  pagerBtn(NEXT).fire("click");
  await settle();
  pagerBtn(NEXT).fire("click");
  await settle();
  pagerBtn(PREV).fire("click");
  await settle();
  pagerBtn(PREV).fire("click");
  await settle();
  const n = sess("N", "new", "2026-01-01T02:00:00Z", { active: true });
  data = [n, ...data];
  streams.at(-1).emit("session", n);
  t.mock.timers.tick(600);
  await settle();
  const p1 = ids();
  pagerBtn(NEXT).fire("click");
  await settle();
  const p2 = ids();
  pagerBtn(NEXT).fire("click");
  await settle();
  const p3 = ids();
  pagerBtn(PREV).fire("click");
  await settle();
  assert.deepEqual(ids(), p2, "Previous returns to the same page 2");
  // 151 matches: pages of 50 are N..r48, r49..r98, r99..r148 (r149 is page 4)
  assert.deepEqual([p1[0], p1.at(-1)], ["N", "r48"]);
  assert.deepEqual([p2[0], p2.at(-1)], ["r49", "r98"], "page 2 follows page 1 with no gap");
  assert.deepEqual([p3[0], p3.at(-1)], ["r99", "r148"], "page 3 follows page 2 with no gap");
  t.mock.timers.tick(6000);
});

const twoPages = (p) => ({ items: rows(Number(p.get("offset") || 0), 50), next_after: Number(p.get("offset") || 0) < 50 ? "x" : null, total: 100, scanned: 50 });

test("focusMovesToPreviousWhenNextBecomesDisabledOnTheLastPage", async (t) => {
  await fresh(t, "#/", twoPages);
  pagerBtn(NEXT).focus();
  pagerBtn(NEXT).fire("click");
  await settle();
  assert.equal(pagerLabel(), "Page 2 of 2");
  assert.equal(pagerBtn(NEXT).disabled, true);
  assert.equal(document.activeElement, pagerBtn(PREV), "focus moved to the other arrow");
});

test("focusStaysOnTheButtonClickedWhileItStaysEnabled", async (t) => {
  await fresh(t, "#/?page=2", () => ({ items: rows(50, 50), next_after: "x", total: 300, scanned: 50 }));
  pagerBtn(NEXT).focus();
  pagerBtn(NEXT).fire("click");
  await settle();
  assert.equal(document.activeElement, pagerBtn(NEXT));
  pagerBtn(PREV).focus();
  pagerBtn(PREV).fire("click");
  await settle();
  assert.equal(document.activeElement, pagerBtn(PREV));
  assert.equal(pagerLabel(), "Page 2 of 6");
});

test("focusMovesToNextWhenPreviousBecomesDisabledOnPageOne", async (t) => {
  await fresh(t, "#/?page=2", twoPages);
  pagerBtn(PREV).focus();
  pagerBtn(PREV).fire("click");
  await settle();
  assert.equal(pagerBtn(PREV).disabled, true);
  assert.equal(document.activeElement, pagerBtn(NEXT));
});

test("aPageChangeDoesNotReadTheUnassignedSectionAgainWhenItsCountIsUnchanged", async (t) => {
  const reads = [];
  replies["/api/effects/unassigned"] = { effects: [] };
  const base = (p) => ({ ...twoPages(p), unassigned_connections: 3 });
  await fresh(t, "#/", base);
  const count = () => reads.length;
  const orig = globalThis.fetch;
  globalThis.fetch = async (url, ...a) => {
    if (String(url).startsWith("/api/effects/unassigned")) reads.push(String(url));
    return orig(url, ...a);
  };
  try {
    pagerBtn(NEXT).fire("click");
    await settle();
    assert.equal(count(), 0, "page 2 with the same count: no unassigned request");
    pagerBtn(PREV).fire("click");
    await settle();
    assert.equal(count(), 1, "page 1 reads it");
    serve = (p) => ({ ...twoPages(p), unassigned_connections: 4 });
    pagerBtn(NEXT).fire("click");
    await settle();
    assert.equal(count(), 2, "a changed count reads it on any page");
  } finally {
    globalThis.fetch = orig;
    delete replies["/api/effects/unassigned"];
  }
});

test("aPageAnswerArrivingAfterLeavingTheListIsDropped", async (t) => {
  await fresh(t, "#/", (p) => ({ items: rows(Number(p.get("offset") || 0), 50), next_after: "x", total: 300, scanned: 50 }));
  const slow = deferred();
  serve = async (p) => {
    if (p.get("offset") === "50") await slow.promise;
    return { items: rows(Number(p.get("offset") || 0), 50), next_after: "x", total: 300, scanned: 50 };
  };
  pagerBtn(NEXT).fire("click");
  await settle();
  await go("#/ungrouped");
  await go("#/");
  assert.equal(pagerLabel(), "Page 1 of 6", "back on page 1");
  slow.resolve();
  await settle();
  assert.equal(ids()[0], "r0", "page 2's late answer does not replace page 1");
  assert.equal(pagerLabel(), "Page 1 of 6");
});

// the label has a fixed width, so Previous and Next never move between pages or modes
test("thePagerLabelHasAFixedMinWidthAndTabularNumbers", () => {
  const css = readFileSync(new URL("../web/style.css", import.meta.url), "utf8");
  const m = css.match(/(^|\n)\.sp-pager \.pg-label\s*\{([^}]*)\}/);
  assert.ok(m, "a rule for the pager label");
  const d = Object.fromEntries(m[2].split(";").filter((x) => x.includes(":")).map((x) => [x.slice(0, x.indexOf(":")).trim(), x.slice(x.indexOf(":") + 1).trim()]));
  const ch = Number.parseFloat(d["min-width"]);
  assert.match(d["min-width"], /^\d+(\.\d+)?ch$/);
  assert.ok(ch >= "Page 999 of 999".length, "wide enough for Page 999 of 999");
  assert.equal(d["font-variant-numeric"], "tabular-nums");
  assert.equal(d["text-align"], "center");
  assert.match(css, /\.sp-pager \{[^}]*flex-wrap: nowrap/, "the row does not wrap");
  assert.doesNotMatch(css, /\.pgap|\.sp-pager \.btn\[aria-current/, "no numbered-pager rules");
});

// the box's height is set by the stylesheet and does not depend on what it holds
test("theListBoxHasAFixedHeightThatDoesNotDependOnItsContent", () => {
  const css = readFileSync(new URL("../web/style.css", import.meta.url), "utf8");
  const m = css.match(/(^|\n)\.sessions-page > \.sp-list\s*\{([^}]*)\}/);
  assert.ok(m, "a rule for the sessions list box");
  const d = Object.fromEntries(m[2].split(";").filter((x) => x.includes(":")).map((x) => [x.slice(0, x.indexOf(":")).trim(), x.slice(x.indexOf(":") + 1).trim()]));
  assert.equal(d.height, "calc(100vh - 320px)", "the window less what sits above and below (measured)");
  assert.equal(d["min-height"], "360px");
  assert.equal(d.overflow, "auto", "the table scrolls inside the box");
  assert.equal(d["max-height"], undefined);
  assert.match(css, /\.sp-pager \{[^}]*display: flex/, "the pager has its own row below the box");
  assert.doesNotMatch(m[2], /(?<!min-)height:\s*auto/);
});

test("aFilteredListRefetchesPageOneAtMostEveryFiveSecondsKeepingTheLastEvent", async (t) => {
  const rowA = sess("a", "alpha", stamp(5), { active: true });
  await fresh(t, "#/?kind=active", () => ({ items: [rowA], next_after: "t1", total: 9, scanned: 9 }));
  const stream = streams.at(-1);
  sessionReqs.length = 0;
  stream.emit("session", sess("c", "gamma", stamp(9), { active: true }));
  t.mock.timers.tick(500);
  await settle();
  assert.equal(sessionReqs.length, 1, "the first event refetches after it settles");
  stream.emit("session", sess("d", "delta", stamp(10), { active: true }));
  stream.emit("session", sess("e", "eps", stamp(11), { active: true }));
  t.mock.timers.tick(4000);
  await settle();
  assert.equal(sessionReqs.length, 1, "no second refetch inside the five seconds");
  t.mock.timers.tick(1000);
  await settle();
  assert.equal(sessionReqs.length, 2, "the trailing refetch keeps the last events");
  t.mock.timers.tick(10000);
  await settle();
  assert.equal(sessionReqs.length, 2, "nothing more without a new event");
});

test("aFilteredListIgnoresAnEventOlderThanTheOldestRowOfPageOne", async (t) => {
  await fresh(t, "#/?kind=active", () => ({ items: [sess("a", "alpha", stamp(5), { active: true }), sess("b", "beta", stamp(4), { active: true })], next_after: "t1", total: 9, scanned: 9 }));
  const stream = streams.at(-1);
  sessionReqs.length = 0;
  stream.emit("session", sess("old", "too old", stamp(1), { active: true }));
  t.mock.timers.tick(10000);
  await settle();
  assert.equal(sessionReqs.length, 0, "it could not land on page 1");
  stream.emit("session", sess("new", "new one", stamp(9), { active: true }));
  t.mock.timers.tick(500);
  await settle();
  assert.equal(sessionReqs.length, 1, "a newer one still refetches");
  t.mock.timers.tick(5000); // let the cooldown end so no timer is left pending for the next test
});

test("typingALongSearchSendsAndShowsAQueryCappedAt200Bytes", async (t) => {
  await fresh(t, "#/");
  sessionReqs.length = 0;
  const box = input();
  box.value = "é".repeat(150); // 300 bytes
  box.fire("input");
  t.mock.timers.tick(300);
  await settle();
  const q = lastReq().get("q");
  assert.equal(new TextEncoder().encode(q).length, 200);
  assert.equal(q, "é".repeat(100));
  assert.equal(box.value, q, "the box shows the capped text");
  assert.deepEqual(entries.at(-1), ["replace", `#/?q=${encodeURIComponent(q)}`]);
});

// ---- skeletons on the session page and the call panel (spec 029 AC4) ----

const step = (n, id) => ({ n, exchange_id: id, at: `2026-01-01T00:00:0${n}Z`, dur_ms: 1000, did: "wrote the answer", tools: [] });
const turn = (n, id, prompt) => ({ n, hash: `h${n}`, prompt, reminders: [], at: step(n, id).at, clock_ms: 1000, counts: { calls: 1 }, steps: [step(n, id)], hook_tools: [], loose: [] });
const view = () => ({
  version: "v1",
  overview: { title: "Seeded", status: { glyph: "■", word: "ended" }, client: "c", key: "k", models: ["m"], time: {}, files: {}, commands: {}, sites: {}, context: {}, needs_look: [], tokens: {} },
  side_calls: [], between: [], turns: [turn(1, "a", "one"), turn(2, "b", "two")],
  files: { changed: [], read: [], searches: [], coverage: "" }, commands: { items: [], coverage: "" }, sites: { items: [], coverage: "" },
  context: { main: [], subs: [], coverage: "" },
});
const call = (id) => ({
  n: 2, exchange_id: id, turn: 2,
  facts: { model: "m", dur_ms: 1000, start: "2026-01-01T00:00:02Z", stop: { word: "end turn" }, thread: { kind: "main", label: "main" }, ctx: 10 },
  context: { size: 10, delta: null, input: 10, cache_write: 0, cache_read: 0, output: 1 },
  saw: { first: false, carried: 0, new: 0, messages: [] }, thinking: false, response: { text: "", stop_reason: "" }, tools: [],
  health: { status: "200 OK", ok: true, items: [] }, raw: { headers: `#/exchanges/${id}`, page: `#/exchanges/${id}` }, nav: { index: 2, total: 2, prev: "a", next: "" },
});
const status = (root) => all(root).filter((e) => e.attrs.role === "status");

test("sessionPageShowsASkeletonUntilItsViewArrivesAndAnErrorReplacesIt", async (t) => {
  await boot(t);
  await go("#/ungrouped");
  const gate = deferred();
  holds["/api/sessions/k1/view"] = gate.promise;
  replies["/api/sessions/k1/view"] = view();
  await go("#/sessions/k1");
  assert.match(text(), /Loading the session…/);
  assert.ok(skeletons().length > 0);
  assert.ok(skeletons().every((s) => s.attrs["aria-hidden"] === "true"));
  assert.ok(status(byId("app")).some((e) => /Loading the session…/.test(textOf(e))), "a status line stays");
  gate.resolve();
  await settle();
  assert.doesNotMatch(text(), /Loading the session…/);
  assert.equal(withClass(byId("app"), "toc-item").length, 2, "replaced by the content");
  assert.equal(skeletons().length, 0);
  // an error replaces it too
  await go("#/ungrouped");
  const gate2 = deferred();
  holds["/api/sessions/k2/view"] = gate2.promise;
  replies["/api/sessions/k2/view"] = { __status: 500, message: "view exploded" };
  await go("#/sessions/k2");
  assert.match(text(), /Loading the session…/);
  gate2.resolve();
  await settle();
  assert.match(text(), /view exploded/);
  assert.doesNotMatch(text(), /Loading the session…/);
});

test("callPanelShowsASkeletonWhileTheCallLoadsAndTheContentOrErrorReplacesIt", async (t) => {
  await boot(t);
  await go("#/ungrouped");
  replies["/api/sessions/k3/view"] = view();
  const gate = deferred();
  holds["/api/sessions/k3/calls/b"] = gate.promise;
  replies["/api/sessions/k3/calls/b"] = call("b");
  await go("#/sessions/k3/call/b");
  const dialog = () => withClass(byId("app"), "call-details")[0];
  assert.match(textOf(dialog()), /Loading call…/);
  assert.ok(withClass(dialog(), "skel").length > 0);
  assert.ok(withClass(dialog(), "skel").every((s) => s.attrs["aria-hidden"] === "true"));
  assert.ok(status(dialog()).some((e) => /Loading call…/.test(textOf(e))));
  gate.resolve();
  await settle();
  assert.match(textOf(dialog()), /Call #2/);
  assert.doesNotMatch(textOf(dialog()), /Loading call…/);
  assert.equal(withClass(dialog(), "skel").length, 0);
  // an error replaces it too
  await go("#/ungrouped");
  replies["/api/sessions/k4/view"] = view();
  const gate2 = deferred();
  holds["/api/sessions/k4/calls/b"] = gate2.promise;
  replies["/api/sessions/k4/calls/b"] = { __status: 500, message: "call exploded" };
  await go("#/sessions/k4/call/b");
  assert.match(textOf(dialog()), /Loading call…/);
  gate2.resolve();
  await settle();
  assert.match(textOf(dialog()), /call exploded/);
  assert.doesNotMatch(textOf(dialog()), /Loading call…/);
  assert.equal(withClass(dialog(), "skel").length, 0);
});
