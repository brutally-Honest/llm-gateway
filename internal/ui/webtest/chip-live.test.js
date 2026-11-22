import test from "node:test";
import assert from "node:assert/strict";
import { all, installDom, textOf, withClass } from "./dom-stub.js";

// Spec 017 E10 (AC14, 016 AC12): a live update while a chip's panel is open keeps the
// panel open and shows the update. view.test.js covers the "All" chip only; this opens
// the "failed" chip, then a live event adds one more failure.

installDom();
const { createSessionPage } = await import("../web/session-page.js");

const mark = (glyph, word) => ({ glyph, word });
const failedTool = (id, n, summary, err) => ({ id, name: "Bash", kind: "command", state: mark("✗", "failed"), chip: "failed", summary, added: 0, removed: 0, dur_ms: 1000, first_error: err, call_n: n, turn: 1, effects: [] });
const step = (n, tools) => ({ n, exchange_id: `e${n}`, at: "2026-11-10T10:00:05Z", dur_ms: 1000, did: "ran", tools });
const viewWith = (failures, hash) => ({
  version: "v1",
  overview: { title: "T", title_source: "generated", status: mark("■", "ended"), client: "c", key: "k", models: ["m1"], time: { clock_ms: 1, model_ms: 1, tool_ms: 1, wait_ms: 0, between_ms: 0 }, files: { changed: 0, added: 0, removed: 0, read: 0 }, commands: { count: failures.length, longest: "", longest_ms: 0 }, sites: { count: 0, connections: 0 }, context: { last: 1 }, needs_look: [], tokens: {} },
  side_calls: [],
  turns: [{
    n: 1, hash, prompt: "prompt 1", at: "2026-11-10T10:00:00Z", clock_ms: 1000, model_ms: 500, tool_ms: 500, wait_ms: 0,
    counts: { calls: failures.length, tools: failures.length, changed: 0, read: 0, searched: 0, sites: 0, failed: failures.length },
    steps: failures.map((f, i) => step(i + 1, [f])),
  }],
  between: [],
  files: { coverage: "", changed: [], read: [], searches: [] },
  commands: { coverage: "", items: [] },
  sites: { coverage: "", items: [] },
  context: { coverage: "", now: 1, first: 1, growth: 0, peak: 1, main: [], subs: [] },
});

const byId = (root, id) => all(root).find((e) => e.id === id);

test("liveUpdateKeepsAnOpenChipPanelOpenAndUpdated", () => {
  const first = failedTool("t1", 1, "npm test", "Error: one");
  const second = failedTool("t2", 2, "go vet", "Error: two");
  const page = createSessionPage({
    callHref: (id) => `#/sessions/s1/call/${id}`,
    loadTool: async () => ({}), loadDiff: async () => ({}), loadContent: async () => ({}), openCall: () => {},
  });
  globalThis.document.getElementById = (id) => all(page.el).find((e) => e.id === id) || null;
  page.setView(viewWith([first], "h1"));

  const t1 = byId(page.el, "turn-1");
  const chip = (turn) => withClass(turn, "chip").find((b) => b.dataset.chip === "failed");
  const rows = (turn) => withClass(byId(turn, "cp-1-failed"), "frow").map((r) => textOf(r).replace(/\s+/g, " ").trim());
  assert.equal(chip(t1).textContent, "✗ 1 failed");

  // open the panel as a user would
  chip(t1).fire("click");
  assert.equal(byId(t1, "cp-1-failed").hidden, false);
  assert.deepEqual(rows(t1), ["#1 ✗ failed Bash npm test 1.0s Error: one"]);

  // a live event brings one more failure into the same turn
  const next = viewWith([first, second], "h1b");
  page.applyEvent({ ...next, full: false, order: [1] });

  const again = byId(page.el, "turn-1");
  assert.notEqual(again, t1, "the turn was rebuilt");
  assert.equal(byId(again, "cp-1-failed").hidden, false, "(a) the panel stays open");
  assert.equal(chip(again).attrs["aria-expanded"], "true");
  assert.ok(chip(again).classes.has("on"), "the chip stays marked selected");
  assert.deepEqual(rows(again), ["#1 ✗ failed Bash npm test 1.0s Error: one", "#2 ✗ failed Bash go vet 1.0s Error: two"], "(b) the panel shows the new failure");
  assert.equal(chip(again).textContent, "✗ 2 failed", "(c) the chip's count updated");
});
