import test from "node:test";
import assert from "node:assert/strict";
import { callCost, costLines, explain, totalCost, usd } from "../web/cost.js";

// spec 020 AC4, research D11: exact strings are rounded half up for display only.
test("usdRoundsTheExactString", () => {
  assert.equal(usd("0"), "$0.00");
  assert.equal(usd("1.234"), "$1.23");
  assert.equal(usd("1.235"), "$1.24", "half up, on the digits");
  assert.equal(usd("0.005"), "$0.01");
  assert.equal(usd("0.0049"), "< $0.01", "above zero but under a cent");
  assert.equal(usd("1234567.999"), "$1,234,568.00");
  assert.equal(usd("0.0452108", 4), "$0.0452");
  assert.equal(usd("0.000008", 4), "< $0.0001");
  assert.equal(usd("12345678901234.5"), "$12,345,678,901,234.50", "no float: digits past 2^53 stay exact");
  assert.equal(usd(undefined), "—");
});

const total = (o = {}) => ({ usd: "1.234", source: "Test", priced: 3, no_price: 0, no_price_models: [], no_usage: 0, errored: 0, estimated: 0, at_least: 0, partial: false, ...o });

// spec 020 AC6, AC8: a whole total is the amount; a partial one is "≥" with the reasons.
test("totalCostMarksPartial", () => {
  const whole = totalCost(total());
  assert.equal(whole.text, "$1.23");
  assert.match(whole.title, /API-equivalent: what these tokens would cost at Test's published API prices — not what was charged/);

  const part = totalCost(total({ no_price: 2, no_price_models: ["m9"], no_usage: 1, partial: true }));
  assert.equal(part.text, "≥ $1.23");
  assert.match(part.title, /2 calls left out: no price for m9/);
  assert.match(part.title, /1 call left out: no usage counts/);

  const est = totalCost(total({ estimated: 1, at_least: 1, partial: true }));
  assert.equal(est.text, "≥ $1.23");
  assert.match(est.title, /1 call estimated/);
  assert.match(est.title, /1 call may have used more/);
});

// spec 020 E5: nothing priced but calls left out is "—"; nothing at all is $0.00.
test("totalCostWithNothingPriced", () => {
  assert.equal(totalCost(total({ usd: "0", priced: 0, no_price: 1, no_price_models: ["m9"], partial: true })).text, "—");
  assert.equal(totalCost(total({ usd: "0", priced: 0 })).text, "$0.00");
  const errd = totalCost(total({ usd: "0", priced: 0, errored: 2 }));
  assert.equal(errd.text, "$0.00", "an error response cost nothing (E3)");
  assert.match(errd.title, /2 calls failed with no usage and cost nothing/);
  assert.equal(totalCost(undefined).text, "—");
});

// spec 020 AC5, AC8: one call's cost, its reasons, and its breakdown by token kind.
test("callCostAndLines", () => {
  const c = { status: "priced", source: "Test", usd: "0.0531508", lines: [
    { kind: "input", tokens: 2, usd_per_mtok: "4", usd: "0.000008" },
    { kind: "cache_write_1h", tokens: 199, usd_per_mtok: "8", usd: "0.001592" },
    { kind: "cache_read", tokens: 226054, usd_per_mtok: "0.2", usd: "0.0452108" },
  ] };
  assert.equal(callCost(c).text, "$0.05");
  assert.equal(callCost({ ...c, estimated: true }).text, "$0.05 (estimated)");
  assert.equal(callCost({ ...c, at_least: true }).text, "≥ $0.05");
  assert.deepEqual(costLines(c)[2], { kind: "Cache read", tokens: "226,054", rate: "$0.2 / MTok", amount: "$0.0452" });
  assert.equal(costLines(c)[0].amount, "< $0.0001");

  const np = callCost({ status: "no_price", reason: "no price for m9", source: "" });
  assert.equal(np.text, "— no price for m9");
  assert.match(np.title, /left out of every total/);
  assert.equal(callCost({ status: "no_usage", reason: "the stored reply has no usage counts" }).text, "—");
  assert.match(callCost({ status: "errored", reason: "the call failed" }).title, /Nothing was billed/);
  assert.deepEqual(costLines({ status: "no_price" }), []);
});

test("explainWithoutSource", () => {
  assert.match(explain(""), /the provider's published API prices/);
});
