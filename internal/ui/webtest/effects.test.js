import test from "node:test";
import assert from "node:assert/strict";
import { placeEffects } from "../web/effects.js";

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
