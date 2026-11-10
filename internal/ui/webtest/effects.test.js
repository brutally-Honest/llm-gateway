import test from "node:test";
import assert from "node:assert/strict";
import { attributionText, placeEffects, toolNumbers } from "../web/effects.js";

const at = (s) => new Date(Date.UTC(2026, 10, 8, 10, 0, s)).toISOString();
const fileChange = (id, s, path) => ({ id, kind: "file_change", at: at(s), path, change: "modified" });

// AC14: each effect lands in the turn whose time range holds it.
test("placesEffectsInTurns", () => {
  const turns = [{ start: at(10) }, { start: at(20) }];
  const effects = [
    fileChange(1, 5, "/w/early.txt"), // before the first prompt
    fileChange(2, 12, "/w/a.txt"), // inside turn 1
    { id: 3, kind: "file_read", at: at(15), path: "/w/a.txt" },
    fileChange(4, 20, "/w/b.txt"), // at turn 2's start: turn 2
    { id: 5, kind: "net_conn", at: at(99), host: "example.com", port: 443 }, // after the last turn's start
    { id: 6, kind: "net_conn", at: at(100), host: "example.com", port: 443 },
  ];
  const p = placeEffects(turns, effects);
  assert.deepEqual(p.before.items.map((e) => e.id), [1]);
  assert.deepEqual(p.turns[0].items.map((e) => e.id), [2, 3]);
  assert.deepEqual(p.turns[1].items.map((e) => e.id), [4, 5, 6]);
  assert.deepEqual([p.turns[0].changed, p.turns[0].read, p.turns[0].sites], [1, 1, 0]);
  assert.deepEqual([p.turns[1].changed, p.turns[1].read, p.turns[1].sites], [1, 0, 1]);
});

// spec 015 AC12: the label as glyph and word, then "#n Name" from the model call that asked
// for the tool, the name alone when no captured call asked, every candidate for several;
// nothing when the server sent no label.
test("attributionText", () => {
  const entries = [
    { kind: "model_call", exchange_id: "ex1", start: "2026-11-10T10:00:00Z" },
    { kind: "tool_call", exchange_id: "ex1", tool_call_id: "t1", name: "Bash" },
    { kind: "model_call", exchange_id: "ex2", start: "2026-11-10T10:00:05Z" },
    { kind: "tool_call", exchange_id: "ex2", tool_call_id: "t2", name: "Edit" },
  ];
  const nos = toolNumbers(entries, new Map([["ex1", 1], ["ex2", 2]]));
  assert.equal(attributionText({ label: "named", tool: { id: "t2", name: "Edit" } }, nos), "● named: #2 Edit");
  assert.equal(attributionText({ label: "only_one", tool: { id: "t9", name: "Bash" } }, nos), "◐ only one running: Bash");
  assert.equal(
    attributionText({ label: "several", candidates: [{ id: "t1", name: "Bash" }, { id: "t3", name: "Bash", agent_id: "a1" }] }, nos),
    "◌ several running: #1 Bash, Bash (agent)",
  );
  assert.equal(attributionText({ label: "outside" }, nos), "○ outside any tool call");
  assert.equal(attributionText({ label: "harness" }, nos), "⚙ harness");
  assert.equal(attributionText(undefined, nos), "");
});
