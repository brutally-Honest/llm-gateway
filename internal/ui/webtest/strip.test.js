import test from "node:test";
import assert from "node:assert/strict";
import { stripBars, MIN_BAR_WIDTH } from "../web/model.js";
import { call, item } from "./fixtures.js";

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
