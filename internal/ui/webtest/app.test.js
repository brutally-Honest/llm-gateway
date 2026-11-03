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
class FakeEventSource {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSED = 2;
  readyState = FakeEventSource.OPEN;
  constructor(url) {
    this.url = url;
    streams.push(this);
  }
  addEventListener() {}
  close() {
    this.readyState = FakeEventSource.CLOSED;
  }
}
const session = { id: "s1", title: "Seeded", client: "c", models: ["m"], first_at: "2026-01-01T00:00:00Z", last_at: "2026-01-01T00:01:00Z", exchange_count: 2, model_calls: 2, main_errors: 0, active: true, usage: {}, tool_states: {} };
const entries = [call("a", 1), item("a", "user_prompt", { role: "user", text: "one" }), call("b", 2, { prev: "a" })];
const replies = {
  "/api/sessions/s1": session,
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
  window: { localStorage: null, addEventListener() {}, scrollTo() {}, scrollY: 0, innerHeight: 0 },
  location: { hash: "#/sessions/s1/call/b", replace() {} },
  history: { back() {} },
  fetch: async (url) => {
    const body = replies[String(url).split("?")[0]];
    return { ok: Boolean(body), statusText: "not stubbed", json: async () => body };
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
    assert.equal(withClass(page, "call-link").length, 2, "the history stays");
  };
  const first = streams.at(-1);
  assert.equal(first.url, "/api/sessions/s1/stream");
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
