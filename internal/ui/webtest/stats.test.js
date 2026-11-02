import test from "node:test";
import assert from "node:assert/strict";
import { buildStats, buildTurns } from "../web/model.js";
import { call, item } from "./fixtures.js";

const summary = { errors: 3, main_errors: 1, side_errors: 2, model_calls: 3, tool_states: { ok: 2 },
  usage: { input: { value: 30 }, output: { value: 15 } } };

test("statsReadMainErrorsNotErrors", () => {
  const s = buildStats([call("a", 1)], summary);
  assert.equal(s.errors, 1);
  assert.equal(s.input, 30);
  assert.equal(s.output, 15);
  assert.deepEqual(s.toolStates, { ok: 2 });
});

test("sideCallsCountAndKeepLink", () => {
  const entries = [call("a", 1), item("a", "user_prompt", { text: "go" }), call("q", 2, { main: false, side: true }), call("b", 3)];
  const s = buildStats(entries, { ...summary, model_calls: undefined });
  assert.equal(s.calls, 3);
  assert.equal(s.sideCalls, 1);
  assert.equal(s.modelTimeMs, 3000);
  const side = buildTurns(entries).cards.flatMap((c) => c.calls).find((c) => c.side);
  assert.equal(side.link, "#2");
  assert.equal(side.exchangeId, "q");
});

test("contextIsLastMainCall", () => {
  const u = (input, cr) => ({ has_usage: true, partial: false, input, output: 1, cache_write: 5, cache_read: cr });
  const entries = [
    call("a", 1, { usage: u(100, 10) }),
    call("b", 2, { prev: "a", usage: u(200, 20) }),
    call("q", 3, { main: false, side: true, usage: u(999, 999) }),
  ];
  assert.equal(buildStats(entries, summary).context.value, 225);
  // last main call without usage borrows the previous call's, flagged partial
  entries[1].usage = { has_usage: false, input: null, output: null, cache_write: null, cache_read: null };
  const c = buildStats(entries, summary).context;
  assert.equal(c.value, 115);
  assert.equal(c.partial, true);
  assert.equal(buildStats([], summary).context, null);
});
