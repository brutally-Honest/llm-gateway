import test from "node:test";
import assert from "node:assert/strict";
import { compareEntries, replaceHooks, pendingBadgeShown } from "../web/entries.js";

test("module loads and compareEntries orders by time", () => {
  const a = { start: "2026-01-01T00:00:05.12Z", exchange_id: "x", index: 0 };
  const b = { start: "2026-01-01T00:00:05.123Z", exchange_id: "x", index: 0 };
  assert.equal(compareEntries(a, b), -1);
  assert.equal(compareEntries(b, a), 1);
  assert.equal(compareEntries(a, a), 0);
});

test("empty hooks snapshot removes the hook group (E6)", () => {
  const call = { id: "a:0", kind: "tool_call", exchange_id: "a", index: 0, start: "2026-01-01T00:00:01Z" };
  const hook = { id: "hook:1", kind: "hook_event", exchange_id: "", index: 1, start: "2026-01-01T00:00:02Z" };
  const held = [call, hook];
  assert.deepEqual(replaceHooks(held, []), [call]);
  const next = { id: "hook:2", kind: "hook_event", exchange_id: "", index: 1, start: "2026-01-01T00:00:03Z" };
  assert.deepEqual(replaceHooks(held, [next]), [call, next]);
});

test("exchange sorts before hook lines on an exact time tie", () => {
  const at = "2026-01-01T00:00:05Z";
  const hook = { id: "hook:1", kind: "hook_event", exchange_id: "", index: 0, start: at };
  const call = { id: "a:0", kind: "tool_call", exchange_id: "a", index: 0, start: at };
  assert.equal(compareEntries(call, hook), -1);
  assert.equal(compareEntries(hook, call), 1);
  assert.deepEqual(replaceHooks([call], [hook]).map((e) => e.id), ["a:0", "hook:1"]);
});

test("pending badge hidden when a harness state exists", () => {
  assert.equal(pendingBadgeShown({ state: "pending" }, null), false);
  assert.equal(pendingBadgeShown({ state: "ran-not-reported" }, null), false);
  assert.equal(pendingBadgeShown({}, null), true);
  assert.equal(pendingBadgeShown({ state: "pending" }, { text: "x" }), false);
  assert.equal(pendingBadgeShown({}, { text: "x" }), false);
});
