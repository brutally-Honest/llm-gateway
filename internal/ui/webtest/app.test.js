import test from "node:test";
import assert from "node:assert/strict";
import { StubElement, all, installDom, textOf, withClass } from "./dom-stub.js";

// E16 through the real app.js and api.js: only the browser is stubbed, so the stream's
// error event travels the same path it does in a page.
installDom();
const nodes = new Map();
const byId = (id) => {
  if (!nodes.has(id)) nodes.set(id, new StubElement("div"));
  return nodes.get(id);
};
const streams = [];
const listeners = {};
// plans[path] is a queue of statuses answered before the stubbed reply; 200 means the reply.
// An entry may be an object {status, statusText, code, message} for an error with its own body.
const plans = {};
const asked = [];
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
  // emit delivers a named server event, as the browser does
  emit(name, data) {
    this.handlers[name]({ data: JSON.stringify(data) });
  }
  close() {
    this.readyState = FakeEventSource.CLOSED;
  }
}
const session = { id: "s1", title: "Seeded", client: "c", models: ["m"], first_at: "2026-01-01T00:00:00Z", last_at: "2026-01-01T00:01:00Z", exchange_count: 2, model_calls: 2, main_errors: 0, active: true, usage: {}, tool_states: {} };
// viewOf is the server's session view (spec 016) of the two calls above, one turn each.
const step = (n, id) => ({ n, exchange_id: id, at: "2026-01-01T00:00:0" + n + "Z", dur_ms: 1000, did: "wrote the answer", tools: [] });
const turn = (n, id, prompt) => ({ n, hash: `h${n}`, prompt, reminders: [], at: step(n, id).at, clock_ms: 1000, counts: { calls: 1 }, steps: [step(n, id)], hook_tools: [], loose: [] });
const viewOf = (title, active) => ({
  version: "v1",
  overview: { title, status: active ? { glyph: "●", word: "active" } : { glyph: "■", word: "ended" }, client: "c", key: "k", models: ["m"],
    time: {}, files: {}, commands: {}, sites: {}, context: {}, needs_look: [], tokens: {} },
  side_calls: [], between: [], turns: [turn(1, "a", "one"), turn(2, "b", "two")],
  files: { changed: [], read: [], searches: [], coverage: "" }, commands: { items: [], coverage: "" }, sites: { items: [], coverage: "" },
  context: { main: [], subs: [], coverage: "" },
});
// callOf is the server's call details (spec 017 T014) of call b, the second of two.
const callOf = (id) => ({
  n: 2, exchange_id: id, turn: 2,
  facts: { model: "m", dur_ms: 1000, start: "2026-01-01T00:00:02Z", stop: { word: "end turn" }, thread: { kind: "main", label: "main" }, ctx: 10 },
  context: { size: 10, delta: null, input: 10, cache_write: 0, cache_read: 0, output: 1 },
  saw: { first: false, carried: 0, new: 0, messages: [] }, thinking: false, response: { text: "", stop_reason: "" }, tools: [],
  health: { status: "200 OK", ok: true, items: [] },
  raw: { headers: `#/exchanges/${id}`, page: `#/exchanges/${id}` },
  nav: { index: 2, total: 2, prev: "a", next: "" },
});
const replies = {
  "/api/sessions/s1/view": viewOf("Seeded", true),
  "/api/sessions/s1/calls/b": callOf("b"),
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
  location: { hash: "#/sessions/s1/call/b", replace() {} },
  history: { back() {} },
  fetch: async (url) => {
    const path = String(url).split("?")[0];
    asked.push(String(url));
    const planned = plans[path]?.shift() ?? 200;
    const reply = typeof planned === "number" ? { status: planned } : planned;
    const status = reply.status;
    if (status !== 200) return { ok: false, status, statusText: reply.statusText ?? "Gateway Timeout", json: async () => ({ error: { code: reply.code, message: reply.message ?? "store timed out" } }) };
    const body = typeof replies[path] === "function" ? replies[path](String(url)) : replies[path];
    return { ok: Boolean(body), status: body ? 200 : 404, statusText: "not stubbed", json: async () => body };
  },
});

const settle = () => new Promise((r) => setImmediate(r));
// the live indicator is a dot: its word is the accessible name (hidden text) and the title,
// its tone the class (spec 027 C7a item 2)
const liveDot = () => all(byId("shell-head")).find((e) => e.attrs.role === "status");
const liveText = () => {
  const d = liveDot();
  assert.equal(d.title, textOf(d).trim(), "the tooltip carries the same word");
  return textOf(d).trim();
};
const liveTone = () => liveDot().className;

test("droppedSessionStreamKeepsCallDialogAndHistoryAndShowsDisconnected", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  await import("../web/app.js");
  await settle();
  await settle();
  const page = byId("app");
  const dialog = withClass(page, "call-details")[0];
  const held = () => {
    assert.equal(dialog.open, true, "the call dialog stays open");
    assert.match(textOf(dialog), /Call #2/);
    assert.equal(withClass(page, "toc-item").length, 2, "the turns stay");
    assert.equal(withClass(page, "call-link").length, 2, "each turn's step keeps its call link");
  };
  const first = streams.at(-1);
  assert.equal(first.url, "/api/sessions/s1/stream?view=1");
  first.onopen();
  assert.equal(liveText(), "live");
  assert.equal(liveTone(), "live-dot live-ok", "green while live");
  assert.ok(!liveDot().hidden);
  held();

  // the browser is retrying by itself
  first.readyState = FakeEventSource.CONNECTING;
  first.onerror();
  assert.equal(liveText(), "live: disconnected, retrying");
  assert.equal(liveTone(), "live-dot live-warn", "amber while reconnecting");
  held();

  // the browser gave up: the page reopens the stream, and the view still holds meanwhile
  first.readyState = FakeEventSource.CLOSED;
  first.onerror();
  assert.equal(liveText(), "live: disconnected, retrying");
  held();
  assert.equal(streams.length, 1);
  t.mock.timers.tick(1000);
  assert.equal(streams.length, 2);
  streams[1].onopen();
  assert.equal(liveText(), "live");
  held();
});

// open s2 through the page's own hashchange handler, with the retry delay stubbed out.
async function openSession(sid, plan) {
  const delays = [];
  globalThis.__retryDelay = async (ms) => void delays.push(ms);
  const path = `/api/sessions/${sid}/view`;
  replies[path] = viewOf("Seeded", false);
  Object.assign(plans, plan(path));
  asked.length = 0;
  location.hash = `#/sessions/${sid}`;
  for (const fn of listeners.hashchange) fn();
  await settle();
  await settle();
  return { delays, path, text: () => textOf(byId("app")) };
}

test("retriesSessionLoadOnceAfter504", async () => {
  const r = await openSession("r1", (p) => ({ [p]: [504] }));
  assert.deepEqual(r.delays, [1000]);
  assert.equal(asked.filter((u) => u === r.path).length, 2, "the view is asked twice");
  assert.equal(withClass(byId("app"), "toc-item").length, 2, "the session shows");
  assert.doesNotMatch(r.text(), /building session|timed out/);
});

test("showsBuildingSessionWhileWaitingToRetry", async () => {
  let release;
  const held = new Promise((res) => (release = res));
  globalThis.__retryDelay = () => held;
  replies["/api/sessions/r4/view"] = viewOf("Seeded", false);
  plans["/api/sessions/r4/view"] = [504];
  location.hash = "#/sessions/r4";
  for (const fn of listeners.hashchange) fn();
  await settle();
  assert.match(textOf(byId("app")), /building session…/);
  release();
  await settle();
  await settle();
  assert.doesNotMatch(textOf(byId("app")), /building session/);
});

// a session the server does not have: the story says so, the rail stops loading, and the
// page offers a way back to the list
test("aMissingSessionSaysNotFoundWithALinkBackAndNoSpinner", async () => {
  const path = "/api/sessions/gone/view";
  plans[path] = [{ status: 404, statusText: "Not Found", code: "not_found", message: "no such session" }];
  asked.length = 0;
  location.hash = "#/sessions/gone";
  for (const fn of listeners.hashchange) fn();
  await settle();
  await settle();
  const app = byId("app");
  assert.match(textOf(app), /Session not found\./);
  const link = all(app).find((e) => e.tagName === "a" && textOf(e).trim() === "Back to the sessions list");
  assert.ok(link, "a link back to the list");
  assert.equal(link.href, "#/");
  assert.equal(withClass(app, "loading").length, 0, "no skeleton remains");
  assert.doesNotMatch(textOf(app), /Loading turns|Loading the session|building session/);
  assert.equal(asked.filter((u) => u === path).length, 1, "a 404 is not retried");
});

test("aServerErrorShowsItsMessageAndNoTurnsSkeleton", async () => {
  const path = "/api/sessions/broken/view";
  plans[path] = [{ status: 500, statusText: "Internal Server Error", message: "store broke" }];
  asked.length = 0;
  location.hash = "#/sessions/broken";
  for (const fn of listeners.hashchange) fn();
  await settle();
  await settle();
  const app = byId("app");
  assert.match(textOf(app), /store broke/);
  assert.equal(withClass(app, "loading").length, 0, "no skeleton remains");
  assert.doesNotMatch(textOf(app), /Loading turns/);
  assert.match(textOf(app), /No turns\./);
  assert.equal(asked.filter((u) => u === path).length, 1, "a 500 is not retried");
});

test("secondFailureShowsTheError", async () => {
  const r = await openSession("r2", (p) => ({ [p]: [504, 504] }));
  assert.deepEqual(r.delays, [1000]);
  assert.equal(asked.filter((u) => u === r.path).length, 2, "one retry only");
  assert.match(r.text(), /store timed out/);
  assert.doesNotMatch(r.text(), /building session/);
});

// A call's details get the same single retry on 504 as the session's view: the server
// answers 504 while a build runs past its store timeout, and a second later the cached
// build serves it (013 manual check, F9).
async function openCall(sid, plan) {
  const delays = [];
  globalThis.__retryDelay = async (ms) => void delays.push(ms);
  const path = `/api/sessions/${sid}`;
  replies[`${path}/view`] = viewOf("Seeded", false);
  replies[`${path}/calls/b`] = callOf("b");
  plans[`${path}/calls/b`] = plan;
  asked.length = 0;
  location.hash = `#/sessions/${sid}/call/b`;
  for (const fn of listeners.hashchange) fn();
  for (let i = 0; i < 8; i++) await settle();
  const dialog = withClass(byId("app"), "call-details")[0];
  return { delays, text: () => textOf(dialog), detailAsks: () => asked.filter((u) => u.startsWith(`${path}/calls/b`)).length };
}

test("callDetailRetriesOnceAfter504", async () => {
  const r = await openCall("d1", [504]);
  assert.deepEqual(r.delays, [1000]);
  assert.equal(r.detailAsks(), 2, "the details are asked twice");
  assert.doesNotMatch(r.text(), /unavailable/);
  assert.match(r.text(), /Call #2/);
});

test("callDetailSecond504ShowsTheReason", async () => {
  const r = await openCall("d2", [504, 504]);
  assert.deepEqual(r.delays, [1000]);
  assert.equal(r.detailAsks(), 2, "one retry only");
  assert.match(r.text(), /Call details unavailable: store timed out/);
});

// E3: a URL naming a call the session does not have shows the "no such call" note and no
// dialog; the next call URL clears the note and opens its call.
test("noSuchCallNoteClearsOnTheNextCall", async () => {
  const path = "/api/sessions/m1";
  replies[`${path}/view`] = viewOf("Seeded", false);
  replies[`${path}/calls/b`] = callOf("b");
  location.hash = "#/sessions/m1/call/zzz";
  for (const fn of listeners.hashchange) fn();
  for (let i = 0; i < 8; i++) await settle();
  const app = byId("app");
  assert.match(textOf(app), /No such call in this session\./);
  assert.equal(withClass(app, "call-details")[0].open, false, "no dialog");
  location.hash = "#/sessions/m1/call/b";
  for (const fn of listeners.hashchange) fn();
  for (let i = 0; i < 8; i++) await settle();
  assert.doesNotMatch(textOf(app), /No such call/);
  assert.match(textOf(withClass(app, "call-details")[0]), /Call #2/);
});

// AC12: the ungrouped exchanges page sits in the sessions list's column with its title and
// labelled counts, its rows in a card with glyph-and-word statuses and labelled tokens, and
// the breadcrumb back to Sessions.
test("ungroupedPageUsesLabelsAndBreadcrumb", async () => {
  const u = (v) => ({ value: v, unreported: 0 });
  replies["/api/ungrouped"] = {
    items: [
      { id: "x1", client: "unknown", models: ["m1"], first_at: "2026-01-01T00:00:00Z", errors: 0, title: "hello", usage: { input: u(120), output: u(12), cache_read: u(0) } },
      { id: "x2", client: "unknown", models: ["m1"], first_at: "2026-01-01T00:01:00Z", errors: 1, title: "", usage: { input: u(null), output: u(null), cache_read: u(null) } },
    ],
    next_after: null,
  };
  location.hash = "#/ungrouped";
  for (const fn of listeners.hashchange) fn();
  for (let i = 0; i < 6; i++) await settle();
  const app = byId("app");
  const page = withClass(app, "page")[0];
  assert.ok(page, "the page sits in the list's column");
  const head = textOf(withClass(page, "sp-head")[0]).replace(/\s+/g, " ");
  assert.match(head, /Ungrouped exchanges/);
  assert.match(head, /2 exchanges/);
  assert.match(head, /1 failed/);
  const rows = withClass(withClass(page, "sp-list")[0], "status").filter((c) => c.tagName === "td").map((c) => textOf(c).trim());
  assert.deepEqual(rows, ["✓ ok", "✗ failed"]);
  const tokens = withClass(page, "tokens").filter((c) => c.tagName === "td").map((c) => textOf(c).trim());
  assert.deepEqual(tokens, ["120 in · 12 out · 0 cache read", "— in · — out · — cache read"]);
  const links = withClass(page, "stitle").map((a) => [a.textContent, a.href]);
  assert.deepEqual(links, [["hello", "#/exchanges/x1"], ["no prompt captured", "#/exchanges/x2"]]);
  const crumbs = textOf(all(byId("shell-head")).find((e) => e.tagName === "nav")).replace(/\s+/g, " ").trim();
  assert.equal(crumbs, "Sessions /", "the page's heading says Ungrouped; the crumb is the way back");
});

// AC12, research D43: the full exchange page sits in the sessions list's column, titled with
// its call's number, its status as glyph and word and its facts labelled; the breadcrumb
// names its session and call, or Ungrouped for an exchange no session claims; the call
// dialog's Headers link (#/exchanges/{id}/headers) lands on the request headers.
test("exchangePageUsesLabelsAndBreadcrumb", async () => {
  const base = { method: "POST", path: "/v1/messages", protocol: "test", client: "c", stream: false, parse: "ok", started_at: "2026-01-01T00:00:00Z", ended_at: "2026-01-01T00:00:01Z", ttfb_ms: 120, duration_ms: 1000, request_body_stored: true, response_body_stored: true, request_headers: { "X-Test-Key": ["[REDACTED]"] }, response_headers: {} };
  replies["/api/exchanges/x1"] = { ...base, id: "x1", status: 200, status_word: "200 OK", model: "m1", session_id: "s1", session_title: "Seeded", call_n: 2, usage: { has_usage: true, input: 42, output: 7, cache_write: 0, cache_read: null } };
  replies["/api/exchanges/x2"] = { ...base, id: "x2", status: 500, status_word: "500 Internal Server Error", client: "unknown", usage: null };
  const go = async (hash) => {
    location.hash = hash;
    for (const fn of listeners.hashchange) fn();
    for (let i = 0; i < 6; i++) await settle();
  };
  const crumbs = () => all(byId("shell-head")).find((e) => e.tagName === "nav");
  await go("#/exchanges/x1/headers");
  const page = withClass(byId("app"), "exchange-page")[0];
  assert.ok(page && page.className.split(" ").includes("page"), "the sessions list's column");
  assert.equal(textOf(crumbs()).replace(/\s+/g, " ").trim(), "Sessions / Seeded /", "no crumb repeats the Call #2 heading");
  assert.equal(all(crumbs()).find((e) => e.tagName === "a" && e.textContent === "Seeded").href, "#/sessions/s1");
  const head = withClass(page, "sp-head")[0];
  assert.match(textOf(head), /Call #2/);
  assert.equal(withClass(head, "st-ok")[0].textContent, "✓ 200 OK");
  const facts = withClass(page, "mfact").map((f) => textOf(f).replace(/\s+/g, " ").trim());
  for (const want of ["Input tokens 42", "Output tokens 7", "Cache write 0", "Cache read —", "Time to first byte 120 ms"]) assert.ok(facts.includes(want), want);
  assert.ok(!facts.some((f) => /^Parse|^Stream/.test(f)), "a streamless exchange that parsed ok shows neither tile");
  const headers = page.children.find((c) => c.id === "headers");
  assert.equal(headers.tagName, "details");
  assert.equal(textOf(all(headers).find((e) => e.tagName === "summary")), "Request headers (1)");
  assert.equal(headers.open, true, "the Headers link opens the folded request headers");
  assert.equal(globalThis.document.activeElement, headers, "the Headers link lands on the request headers");
  assert.match(textOf(page), /\[REDACTED\]/);
  const tops = [];
  window.scrollTo = (x, y) => tops.push([x, y]);
  await go("#/exchanges/x2");
  assert.deepEqual(tops[0], [0, 0], "another page starts at its top");
  assert.equal(textOf(crumbs()).replace(/\s+/g, " ").trim(), "Sessions / Ungrouped /");
  const page2 = withClass(byId("app"), "exchange-page")[0];
  assert.match(textOf(withClass(page2, "sp-head")[0]), /Exchange/);
  assert.equal(withClass(page2, "st-failed")[0].textContent, "✗ 500 Internal Server Error");
  assert.ok(withClass(page2, "mfact").some((f) => /Tokens\s*not a model call/.test(textOf(f))));
});
