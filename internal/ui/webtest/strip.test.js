import test from "node:test";
import assert from "node:assert/strict";
import { stripBars, MIN_BAR_WIDTH } from "../web/model.js";
import { call, item } from "./fixtures.js";
import { installDom, withClass } from "./dom-stub.js";

const prompt = (id, text) => item(id, "user_prompt", { role: "user", text });

test("oneBarPerCallMarkedByTurn", () => {
  const bars = stripBars([
    call("a", 1), prompt("a", "one"), item("a", "tool_call", { name: "Bash", tool_call_id: "t1" }),
    call("b", 2, { prev: "a" }), call("c", 3, { prev: "b", status: 500 }), prompt("c", "two"),
  ]);
  assert.equal(bars.length, 3);
  assert.deepEqual(bars.map((b) => b.turn), [1, 1, 2]);
  assert.deepEqual(bars.map((b) => b.turnStart), [true, false, true]);
  assert.match(bars[0].title, /tool Bash/);
  assert.match(bars[2].title, /status 500/);
  assert.equal(bars[2].errored, true);
});

test("sideCallBarsMuted", () => {
  const bars = stripBars([call("a", 1), prompt("a", "go"), call("q", 2, { main: false, side: true })]);
  assert.equal(bars[0].muted, false);
  assert.equal(bars[1].muted, true);
  assert.match(bars[1].title, /side call/);
});

test("barsKeepMinWidth", () => {
  const bars = stripBars([call("a", 1, { ms: 100000 }), call("b", 2, { ms: 1 }), call("c", 3, { ms: 0 })]);
  assert.equal(bars.length, 3);
  assert.ok(bars.every((b) => b.width >= MIN_BAR_WIDTH));
  assert.ok(bars[0].width > bars[1].width);
});

test("activatingBarOpensDrawerRoute", async () => {
  installDom();
  const { createTimelinePage } = await import("../web/timeline-page.js");
  const p = createTimelinePage();
  const summary = { id: "s 1", client: "c", models: [], first_at: "", last_at: "", exchange_count: 2, model_calls: 2, main_errors: 0, active: false, usage: {}, tool_states: {} };
  p.render({ summary, entries: [call("a", 1), prompt("a", "go"), call("q", 2, { main: false, side: true })], error: "", snapshotError: "" });
  const bars = withClass(p.el, "strip-bar");
  assert.deepEqual(bars.map((b) => b.href), ["#/sessions/s%201/call/a", "#/sessions/s%201/call/q"]);
  assert.ok(bars[1].className.includes("is-muted"));
  assert.match(bars[1].title, /side call/);
  assert.ok(Number.parseInt(bars[0].style.width, 10) >= MIN_BAR_WIDTH);
});

test("errorBarsCarryAGlyphAndAWord", async () => {
  installDom();
  const { createTimelinePage } = await import("../web/timeline-page.js");
  const p = createTimelinePage();
  const summary = { id: "s", client: "c", models: [], first_at: "", last_at: "", exchange_count: 2, model_calls: 2, main_errors: 1, active: false, usage: {}, tool_states: {} };
  p.render({ summary, entries: [call("a", 1), prompt("a", "go"), call("b", 2, { prev: "a", status: 500, ms: 1 })], error: "", snapshotError: "" });
  const [ok, bad] = withClass(p.el, "strip-bar");
  assert.equal(ok.children.length, 0);
  assert.equal(bad.children[0].textContent, "✗");
  assert.match(bad.attrs["aria-label"], /error/);
  assert.ok(Number.parseInt(bad.style.width, 10) >= 14);
});
