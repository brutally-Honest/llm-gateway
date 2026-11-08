import test from "node:test";
import assert from "node:assert/strict";
import { compareEntries, replaceHooks, liveBatch, liveEffect, omittedNote, hookLabel, hookInputEntry } from "../web/entries.js";

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

const exA = { id: "a:0", kind: "tool_call", exchange_id: "a", index: 0, start: "2026-01-01T00:00:01Z" };
const hookOld = { id: "hook:1", kind: "hook_event", exchange_id: "", index: 0, start: "2026-01-01T00:00:02Z" };
const hookNew = { id: "hook:2", kind: "hook_event", exchange_id: "", index: 0, start: "2026-01-01T00:00:03Z" };

test("a hooks event replaces the hook group and keeps the exchanges (never replaceExchange)", () => {
  const patch = liveEffect("hooks", [hookNew], [exA, hookOld]);
  assert.deepEqual(patch.entries, [exA, hookNew]);
  assert.equal(patch.snapshotError, "");
});

test("an empty hooks event removes the hook group and keeps the exchanges", () => {
  assert.deepEqual(liveEffect("hooks", [], [exA, hookOld]).entries, [exA]);
});

test("an exchange or entries event replaces its exchange, not the hook group", () => {
  const next = { ...exA, id: "a:1", index: 1, start: "2026-01-01T00:00:04Z" };
  for (const name of ["exchange", "entries"]) {
    const patch = liveEffect(name, [next], [exA, hookOld]);
    assert.deepEqual(patch.entries, [hookOld, next], name);
    assert.equal(patch.snapshotError, "");
  }
});

test("a session event sets the summary and leaves the entries alone", () => {
  assert.deepEqual(liveEffect("session", { id: "s" }, [exA]), { summary: { id: "s" } });
});

test("a snapshot_error event reports its message", () => {
  assert.deepEqual(liveEffect("snapshot_error", { error: { message: "boom" } }, []), { snapshotError: "boom" });
  assert.deepEqual(liveEffect("snapshot_error", {}, []), { snapshotError: "unknown" });
});

test("an unknown event name is dropped", () => {
  assert.equal(liveEffect("mystery", [exA], [exA]), null);
});

test("omittedNote gives the wording only when bytes were omitted", () => {
  assert.equal(omittedNote(0), "");
  assert.equal(omittedNote(undefined), "");
  assert.equal(omittedNote(42), "… 42 bytes omitted");
});

test("hookLabel names an unknown event by its stored name, with the call id", () => {
  assert.equal(hookLabel({ phase: "unknown", text: "evt-x" }), "hook unknown: evt-x");
  assert.equal(hookLabel({ phase: "unknown", text: "evt-x", tool_call_id: "toolu_1" }), "hook unknown: evt-x (toolu_1)");
});

test("hookLabel names a known event by phase and name", () => {
  assert.equal(hookLabel({ phase: "pre", name: "evt-pre" }), "hook pre evt-pre");
  assert.equal(hookLabel({ phase: "post", name: "evt-post", tool_call_id: "t" }), "hook post evt-post (t)");
});

test("hookInputEntry takes truncation from the hook input, not the call input", () => {
  const call = { id: "c", exchange_id: "ex", truncated: true, hook_omitted_bytes: 0 };
  assert.equal(hookInputEntry(call).truncated, false);
  assert.equal(hookInputEntry(call).exchange_id, "ex");
  assert.equal(hookInputEntry({ ...call, hook_omitted_bytes: 5 }).truncated, true);
  assert.equal(hookInputEntry({ id: "c", exchange_id: "ex" }).truncated, false);
});

// oneByOne is the reference liveBatch must match: liveEffect applied event by event.
function oneByOne(events, entries) {
  let patch = null;
  let cur = entries;
  for (const { name, data } of events) {
    const p = liveEffect(name, data, cur);
    if (!p) continue;
    if (p.entries) cur = p.entries;
    patch = { ...patch, ...p };
  }
  return patch;
}

test("liveBatchMatchesApplyingEachEventInOrder", () => {
  let n = 0;
  const at = () => new Date(Date.UTC(2026, 0, 1, 0, 0, 0, ++n)).toISOString();
  const ex = (id, k = 1) => Array.from({ length: k }, (_, i) => ({ id: `${id}:${n}:${i}`, kind: "tool_call", exchange_id: id, index: i, start: at() }));
  const hook = () => ({ id: `hook:${++n}`, kind: "hook_event", exchange_id: "", index: 0, start: at() });
  let seed = 7;
  const rnd = (k) => (seed = (seed * 1103515245 + 12345) % 2147483648) % k;
  const ids = ["a", "b", "c", "d"];
  for (let round = 0; round < 200; round++) {
    const base = [...ex("a"), ...ex("b", 2), hook()].sort(compareEntries);
    const events = [];
    for (let i = 0, len = 1 + rnd(12); i < len; i++) {
      const pick = rnd(9);
      if (pick < 4) events.push({ name: pick % 2 ? "exchange" : "entries", data: ex(ids[rnd(4)], 1 + rnd(3)) });
      else if (pick === 4) events.push({ name: "exchange", data: [...ex(ids[rnd(4)]), ...ex(ids[rnd(4)])] });
      else if (pick === 5) events.push({ name: "hooks", data: rnd(2) ? [hook()] : [] });
      else if (pick === 6) events.push({ name: "snapshot_error", data: { error: { message: `e${i}` } } });
      else if (pick === 7) events.push({ name: rnd(2) ? "session" : "mystery", data: { id: `s${i}` } });
      else events.push({ name: "exchange", data: [] });
    }
    assert.deepEqual(liveBatch(events, base), oneByOne(events, base), `round ${round}: ${events.map((e) => e.name).join(",")}`);
  }
  assert.equal(liveBatch([], [exA]), null);
  assert.equal(liveBatch([{ name: "mystery", data: [] }], [exA]), null);
});
