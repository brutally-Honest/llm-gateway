import test from "node:test";
import assert from "node:assert/strict";
import { buildStats, buildTurns, contextSeries, deltaFor, stripBars } from "../web/model.js";
import { call, item } from "./fixtures.js";
import { liveBatch } from "../web/entries.js";

test("twoThousandCallsModelUnderBudget", () => {
  const es = [];
  for (let i = 1; i <= 2000; i++) {
    const id = `x${i}`;
    es.push(call(id, i, { prev: i > 1 ? `x${i - 1}` : "" }), item(id, "user_prompt", { text: `p${i}` }));
  }
  const byId = new Map(es.filter((e) => e.kind === "model_call").map((e) => [e.exchange_id, e]));
  const t = performance.now();
  buildTurns(es);
  stripBars(es);
  contextSeries(es);
  buildStats(es, {});
  for (const e of byId.values()) deltaFor(e, byId);
  assert.ok(performance.now() - t < 500, "model over 2000 calls stays under 500ms");
});

// A live stream's snapshot of a 2,000-call session is one exchange event per exchange.
// Applied one at a time each re-sorts every entry (about 9 s on the seeded session); as one
// batch it is a single merge (013 manual check, the frozen renderer on an active session).
test("snapshotOfTwoThousandExchangesMergesUnderBudget", () => {
  const es = [];
  for (let i = 1; i <= 2000; i++) {
    const id = `x${i}`;
    es.push(call(id, i), item(id, "user_prompt", { text: `p${i}` }), item(id, "tool_call", { name: "Read" }), item(id, "tool_result", {}), item(id, "assistant_text", { text: "ok" }));
  }
  const events = [];
  for (let i = 0; i < es.length; i += 5) events.push({ name: "exchange", data: es.slice(i, i + 5) });
  const t = performance.now();
  const patch = liveBatch(events, es);
  const ms = performance.now() - t;
  assert.equal(patch.entries.length, es.length);
  assert.ok(ms < 500, `a 2000-exchange snapshot merges in ${Math.round(ms)}ms; budget 500ms`);
});
