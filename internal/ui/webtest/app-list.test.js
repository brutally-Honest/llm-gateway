import test from "node:test";
import assert from "node:assert/strict";
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
const loadMoreBtn = () => byTag(byId("app"), "button").find((b) => b.textContent === "Load more");
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
  // the same for a Load more of an older query
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
  loadMoreBtn().fire("click");
  await settle();
  await go("#/?q=other");
  more.resolve();
  await settle();
  assert.ok(!ids().includes("late"), "a page of another query is dropped");
});

test("loadMoreSendsAfterKindAndQAppendsWithoutDuplicatesAndShowsSkeletonRows", async (t) => {
  const pageOne = Array.from({ length: 3 }, (_, i) => sess(`p${i}`, `p${i}`, stamp(30 - i)));
  await fresh(t, "#/?kind=active&q=p", () => ({ items: pageOne, next_after: "t1", total: 100, scanned: 50 }));
  assert.deepEqual(ids(), ["p0", "p1", "p2"]);
  const gate = deferred();
  serve = async () => {
    await gate.promise;
    return { items: [pageOne[2], sess("p3", "p3", stamp(10))], next_after: null, total: 100, scanned: 20 };
  };
  loadMoreBtn().fire("click");
  await settle();
  assert.deepEqual(reqObj(lastReq()), { kind: "active", q: "p", after: "t1" });
  assert.ok(skeletons().length > 0, "skeleton rows under the table");
  assert.equal(loadMoreBtn().disabled, true);
  assert.deepEqual(ids(), ["p0", "p1", "p2"], "the rows stay meanwhile");
  loadMoreBtn().fire("click");
  await settle();
  assert.equal(sessionReqs.filter((p) => p.get("after")).length, 1, "a second click while it loads asks nothing");
  gate.resolve();
  await settle();
  assert.deepEqual(ids(), ["p0", "p1", "p2", "p3"], "appended, no duplicate");
  assert.equal(skeletons().length, 0);
  assert.equal(loadMoreBtn(), undefined, "no next page, no button");
});

test("loadMoreErrorReplacesTheSkeleton", async (t) => {
  await fresh(t, "#/", () => ({ items: [sess("a", "a", stamp(1))], next_after: "t1", total: 100, scanned: 1 }));
  serve = () => null;
  loadMoreBtn().fire("click");
  await settle();
  assert.match(text(), /store boom/);
  assert.equal(skeletons().length, 0);
});

test("emptyStatesFollowTheServersAnswer", async (t) => {
  await fresh(t, "#/", () => ({ items: [], next_after: null, total: 0, scanned: 0 }));
  assert.match(text(), /No sessions captured yet\./);
  // a filter that nothing matches, every row tested
  serve = () => ({ items: [], next_after: null, total: 12, scanned: 12 });
  await go("#/?kind=look");
  assert.match(text(), /no sessions match/);
  assert.doesNotMatch(text(), /No sessions captured yet/);
  // a short page with more to test: say so, keep Load more, sum what was scanned
  serve = (p) => (p.get("after") ? { items: [], next_after: "t2", total: 2000, scanned: 500 } : { items: [], next_after: "t1", total: 2000, scanned: 500 });
  await go("#/?q=rare");
  assert.match(text(), /No matches in the newest 500 sessions yet/);
  assert.ok(loadMoreBtn(), "Load more is kept");
  loadMoreBtn().fire("click");
  await settle();
  assert.match(text(), /No matches in the newest 1000 sessions yet/, "summed across pages");
  serve = () => ({ items: [sess("hit", "rare hit", stamp(1))], next_after: null, total: 2000, scanned: 40 });
  loadMoreBtn().fire("click");
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

test("liveEventInAFilteredListUpdatesInPlaceAndRefetchesAForANewSession", async (t) => {
  const rowA = sess("a", "alpha", stamp(5), { active: true });
  const rowB = sess("b", "beta", stamp(4), { active: true });
  await fresh(t, "#/?kind=active", () => ({ items: [rowA, rowB], next_after: "t1", total: 9, scanned: 9 }));
  serve = () => ({ items: [sess("old", "older page", stamp(1), { active: true })], next_after: null, total: 9, scanned: 5 });
  loadMoreBtn().fire("click");
  await settle();
  assert.deepEqual(ids(), ["a", "b", "old"]);
  const stream = streams.at(-1);
  sessionReqs.length = 0;
  // a session the list holds: in place, no request
  stream.emit("session", { ...rowA, title: "alpha renamed", last_at: stamp(6) });
  t.mock.timers.tick(1000);
  await settle();
  assert.deepEqual(ids(), ["a", "b", "old"]);
  assert.match(text(), /alpha renamed/);
  assert.equal(sessionReqs.length, 0, "the server is not asked for a session already held");
  // a session the list does not hold: after the burst settles, page 1 is read and merged;
  // the page already loaded stays
  serve = (p) => ({ items: [sess("c", "gamma new", stamp(9), { active: true }), { ...rowA, title: "alpha renamed", last_at: stamp(6) }], next_after: "other", total: 10, scanned: 3 });
  stream.emit("session", sess("c", "gamma new", stamp(9), { active: true }));
  stream.emit("session", sess("c", "gamma new", stamp(9), { active: true }));
  t.mock.timers.tick(400);
  await settle();
  assert.equal(sessionReqs.length, 0, "debounced");
  t.mock.timers.tick(200);
  await settle();
  assert.equal(sessionReqs.length, 1, "one refetch for the burst");
  assert.deepEqual(reqObj(lastReq()), { kind: "active" }, "page 1 for the current kind and q");
  assert.deepEqual(ids(), ["c", "a", "b", "old"], "merged by id, loaded pages kept");
  assert.equal(sessionReqs.filter((p) => p.get("after")).length, 0, "no paging in the refetch");
});

test("liveEventInAnUnfilteredListIsUpsertedAtOnce", async (t) => {
  await fresh(t, "#/", () => ({ items: [sess("a", "alpha", stamp(2))], next_after: null, total: 1, scanned: 1 }));
  sessionReqs.length = 0;
  streams.at(-1).emit("session", sess("n", "brand new", stamp(8)));
  assert.deepEqual(ids(), ["n", "a"]);
  t.mock.timers.tick(1000);
  await settle();
  assert.equal(sessionReqs.length, 0);
});

test("loadMoreAnsweredAfterThePageOneRefreshOnReturnIsDroppedSoNoRowsAreSkipped", async (t) => {
  const pg = (from, n) => Array.from({ length: n }, (_, i) => sess(`r${from + i}`, `r${from + i}`, `2026-01-01T01:00:00.${String(999 - from - i).padStart(3, "0")}Z`));
  const answers = { "": { items: pg(0, 50), next_after: "t50", total: 300, scanned: 50 }, t50: { items: pg(50, 50), next_after: "t100", total: 300, scanned: 50 }, t100: { items: pg(100, 50), next_after: "t150", total: 300, scanned: 50 } };
  await fresh(t, "#/", (p) => answers[p.get("after") || ""]);
  loadMoreBtn().fire("click");
  await settle();
  assert.equal(ids().length, 100);
  await go("#/ungrouped");
  const p1 = deferred();
  const p3 = deferred();
  serve = async (p) => {
    const a = p.get("after") || "";
    if (a === "") await p1.promise;
    if (a === "t100") await p3.promise;
    return answers[a];
  };
  sessionReqs.length = 0;
  await go("#/"); // back on the list: the held rows stay while page 1 is read again
  assert.equal(ids().length, 100, "held rows are shown while page 1 loads");
  loadMoreBtn().fire("click"); // Load more for the old deep position
  await settle();
  p1.resolve(); // page 1 lands first
  await settle();
  p3.resolve(); // then the answer for the old position
  await settle();
  assert.deepEqual(ids(), pg(0, 50).map((r) => r.id), "the stale page is not appended");
  sessionReqs.length = 0;
  serve = (p) => answers[p.get("after") || ""];
  loadMoreBtn().fire("click");
  await settle();
  assert.equal(lastReq().get("after"), "t50", "Load more continues from page 1's own position");
  assert.equal(ids().length, 100);
  assert.ok(ids().includes("r60"), "rows 50-99 are not skipped");
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
