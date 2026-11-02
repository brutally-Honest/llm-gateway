import test from "node:test";
import assert from "node:assert/strict";
import { contextOf, contextPlot, contextSeries, deltaFor } from "../web/model.js";
import { call } from "./fixtures.js";
import { all, installDom, withClass } from "./dom-stub.js";

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

const summary = { id: "s", client: "c", models: [], first_at: "", last_at: "", exchange_count: 4, model_calls: 4, main_errors: 0, active: false, usage: {}, tool_states: {} };

test("plotHasOnePointPerMainCallLeavingOutSideAndSubAgent", () => {
  const es = [
    call("a", 1, { usage: u(100, 0, 0) }),
    call("q", 2, { main: false, side: true, usage: u(5, 0, 0) }),
    call("s", 3, { main: false, thread: 1, usage: u(7, 0, 0) }),
    call("b", 4, { prev: "a", usage: u(300, 0, 0) }),
  ];
  const plot = contextPlot(es);
  assert.deepEqual(plot.points.map((p) => p.n), [1, 4]);
  assert.equal(plot.polyline, "20,52 780,8");
  assert.match(plot.label, /100 to 300 tokens over 2 calls/);
  assert.equal(contextPlot([call("q", 1, { main: false, side: true })]).note, "no main-thread calls to plot");
});

test("contextLineIsAnSvgPolylineWithADotPerCall", async () => {
  installDom();
  const { createTimelinePage } = await import("../web/timeline-page.js");
  const p = createTimelinePage();
  const es = [call("a", 1, { usage: u(100, 0, 0) }), call("q", 2, { main: false, side: true }), call("b", 3, { prev: "a", usage: u(300, 0, 0) })];
  p.render({ summary, entries: es, error: "", snapshotError: "" });
  const [line] = withClass(p.el, "context-line");
  const svg = line.children[0];
  assert.equal(svg.tagName, "svg");
  assert.equal(svg.attrs.role, "img");
  assert.match(svg.attrs["aria-label"], /over 2 calls/);
  const kids = all(svg).filter((e) => e !== svg);
  assert.equal(kids.filter((e) => e.tagName === "polyline").length, 1);
  assert.equal(kids.find((e) => e.tagName === "polyline").attrs.points, "20,52 780,8");
  const dots = kids.filter((e) => e.tagName === "circle");
  assert.equal(dots.length, 2);
  assert.match(dots[1].children[0].textContent, /#3 · context 300/);
});

test("contextLineKeepsTheNoMainCallsNote", async () => {
  installDom();
  const { createTimelinePage } = await import("../web/timeline-page.js");
  const p = createTimelinePage();
  p.render({ summary, entries: [call("q", 1, { main: false, side: true })], error: "", snapshotError: "" });
  const [line] = withClass(p.el, "context-line");
  assert.equal(all(line).filter((e) => e.tagName === "svg").length, 0);
  assert.match(line.children[0].textContent, /no main-thread calls to plot/);
});
