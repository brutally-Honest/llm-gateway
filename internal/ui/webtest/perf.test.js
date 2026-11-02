import test from "node:test";
import assert from "node:assert/strict";
import { buildStats, buildTurns, contextSeries, deltaFor, stripBars } from "../web/model.js";
import { call, item } from "./fixtures.js";

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
