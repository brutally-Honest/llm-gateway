import test from "node:test";
import assert from "node:assert/strict";
import { StubElement, all, installDom, textOf, withClass } from "./dom-stub.js";
import { call, item } from "./fixtures.js";

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
const entries = [call("a", 1), item("a", "user_prompt", { role: "user", text: "one" }), call("b", 2, { prev: "a" })];
// viewOf is the server's session view (spec 016) of the two calls above, one turn each.
const step = (n, id) => ({ n, exchange_id: id, at: "2026-01-01T00:00:0" + n + "Z", dur_ms: 1000, did: "wrote the answer", tools: [] });
const turn = (n, id, prompt) => ({ n, hash: `h${n}`, prompt, reminders: [], at: step(n, id).at, clock_ms: 1000, counts: {}, steps: [step(n, id)], hook_tools: [], loose: [] });
const viewOf = (title, active) => ({
  version: "v1",
  overview: { title, status: active ? { glyph: "●", word: "active" } : { glyph: "■", word: "ended" }, client: "c", key: "k", models: ["m"],
    time: {}, files: {}, commands: {}, sites: {}, context: {}, needs_look: [], tokens: {} },
  side_calls: [], between: [], turns: [turn(1, "a", "one"), turn(2, "b", "two")],
  files: { changed: [], read: [], searches: [], coverage: "" }, commands: { items: [], coverage: "" }, sites: { items: [], coverage: "" },
  context: { main: [], subs: [], coverage: "" },
});
const replies = {
  "/api/sessions/s1/view": viewOf("Seeded", true),
  "/api/sessions/s1/timeline": { entries, next_after: null },
  "/api/exchanges/b": { id: "b", status: 200, session_id: "s1", messages: [], carried: 0, new_count: 0, response: { text: "", stop_reason: "" }, response_tool_call_ids: [] },
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
    const status = plans[path]?.shift() ?? 200;
    if (status !== 200) return { ok: false, status, statusText: "Gateway Timeout", json: async () => ({ error: { message: "store timed out" } }) };
    const body = typeof replies[path] === "function" ? replies[path](String(url)) : replies[path];
    return { ok: Boolean(body), status: body ? 200 : 404, statusText: "not stubbed", json: async () => body };
  },
});

const settle = () => new Promise((r) => setImmediate(r));
const liveText = () => all(byId("shell-head")).find((e) => e.attrs.role === "status").textContent;

test("droppedSessionStreamKeepsDrawerAndHistoryAndShowsDisconnected", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  await import("../web/app.js");
  await settle();
  const page = byId("app");
  const drawer = withClass(page, "drawer")[0];
  const held = () => {
    assert.equal(drawer.hidden, false, "the drawer stays open");
    assert.match(textOf(drawer), /Call #2/);
    assert.equal(withClass(page, "toc-item").length, 2, "the turns stay");
    assert.equal(withClass(page, "call-link").length, 2, "each turn's step keeps its call link");
  };
  const first = streams.at(-1);
  assert.equal(first.url, "/api/sessions/s1/stream?view=1");
  first.onopen();
  assert.equal(liveText(), "live");
  held();

  // the browser is retrying by itself
  first.readyState = FakeEventSource.CONNECTING;
  first.onerror();
  assert.equal(liveText(), "live: disconnected, retrying");
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
  replies[`/api/sessions/${sid}/timeline`] ||= { entries, next_after: null };
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

test("secondFailureShowsTheError", async () => {
  const r = await openSession("r2", (p) => ({ [p]: [504, 504] }));
  assert.deepEqual(r.delays, [1000]);
  assert.equal(asked.filter((u) => u === r.path).length, 2, "one retry only");
  assert.match(r.text(), /store timed out/);
  assert.doesNotMatch(r.text(), /building session/);
});

// The drawer's call list is the only paged load left (the page itself loads one view): its
// first page gets the single 504 retry, a later page does not.
test("laterPage504IsNotRetried", async () => {
  const delays = [];
  globalThis.__retryDelay = async (ms) => void delays.push(ms);
  replies["/api/sessions/r3/view"] = viewOf("Seeded", false);
  replies["/api/sessions/r3/timeline"] = { entries, next_after: "p2" };
  plans["/api/sessions/r3/timeline"] = [200, 504];
  asked.length = 0;
  location.hash = "#/sessions/r3/call/b";
  for (const fn of listeners.hashchange) fn();
  for (let i = 0; i < 8; i++) await settle();
  assert.deepEqual(delays, []);
  assert.equal(asked.filter((u) => u.startsWith("/api/sessions/r3/timeline")).length, 2, "page 2 asked once");
  assert.match(textOf(byId("app")), /store timed out/);
});

// A call's detail gets the same single retry on 504 as the session summary: the server
// answers 504 while a build runs past its store timeout, and a second later the cached
// build serves it (013 manual check, F9).
async function openCall(sid, plan) {
  const delays = [];
  globalThis.__retryDelay = async (ms) => void delays.push(ms);
  const path = `/api/sessions/${sid}`;
  replies[`${path}/view`] = viewOf("Seeded", false);
  replies[`${path}/timeline`] = { entries, next_after: null };
  plans["/api/exchanges/b"] = plan;
  asked.length = 0;
  location.hash = `#/sessions/${sid}/call/b`;
  for (const fn of listeners.hashchange) fn();
  for (let i = 0; i < 8; i++) await settle();
  const drawer = withClass(byId("app"), "drawer")[0];
  return { delays, text: () => textOf(drawer), detailAsks: () => asked.filter((u) => u.startsWith("/api/exchanges/b")).length };
}

test("callDetailRetriesOnceAfter504", async () => {
  const r = await openCall("d1", [504]);
  assert.deepEqual(r.delays, [1000]);
  assert.equal(r.detailAsks(), 2, "the detail is asked twice");
  assert.doesNotMatch(r.text(), /detail unavailable/);
});

test("callDetailSecond504ShowsTheReason", async () => {
  const r = await openCall("d2", [504, 504]);
  assert.deepEqual(r.delays, [1000]);
  assert.equal(r.detailAsks(), 2, "one retry only");
  assert.match(r.text(), /detail unavailable: store timed out/);
});
