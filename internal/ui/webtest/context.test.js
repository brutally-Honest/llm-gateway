import test from "node:test";
import assert from "node:assert/strict";
import { contextOf, contextSeries, deltaFor } from "../web/model.js";
import { call } from "./fixtures.js";

const u = (input, cw, cr) => ({ has_usage: true, partial: false, input, output: 1, cache_write: cw, cache_read: cr });
const none = { has_usage: false, partial: false, input: null, output: null, cache_write: null, cache_read: null };
const index = (es) => new Map(es.map((e) => [e.exchange_id, e]));

test("seriesLeavesOutSideAndSubAgent", () => {
  const es = [
    call("a", 1, { usage: u(100, 0, 0) }),
    call("q", 2, { main: false, side: true, usage: u(5, 0, 0) }),
    call("s", 3, { main: false, thread: 1, usage: u(7, 0, 0) }),
    call("b", 4, { prev: "a", usage: u(200, 10, 5) }),
  ];
  const s = contextSeries(es);
  assert.deepEqual(s.points.map((p) => [p.n, p.value]), [[1, 100], [4, 215]]);
});

test("deltaMeasuredFromPreviousCallWithUsage", () => {
  const es = [call("a", 1, { usage: u(100, 0, 0) }), call("b", 2, { prev: "a", status: 429, usage: none }),
    call("c", 3, { prev: "b", usage: u(160, 0, 0) })];
  const d = deltaFor(es[2], index(es));
  assert.deepEqual([d.kind, d.value, d.from], ["delta", 60, "a"]);
  assert.equal(deltaFor(es[1], index(es)).kind, "none");
});

test("callWithUnreportedCounterIsDashAndPartial", () => {
  assert.deepEqual(contextOf(call("a", 1, { usage: { ...u(10, 0, 0), cache_read: null, partial: true } })), { value: null, partial: true });
  assert.deepEqual(contextOf(call("a", 1, { usage: none })), { value: null, partial: false });
  assert.deepEqual(contextOf(call("a", 1, { usage: u(10, 2, 3) })), { value: 15, partial: false });
});

test("firstCallOfThreadHasNoDelta", () => {
  const es = [call("a", 1)];
  assert.equal(deltaFor(es[0], index(es)).kind, "first");
});

test("noMainCallsContextNote", () => {
  const s = contextSeries([call("q", 1, { main: false, side: true })]);
  assert.deepEqual(s.points, []);
  assert.match(s.note, /no main-thread calls/);
  assert.match(contextSeries([]).note, /no main-thread calls/);
});
