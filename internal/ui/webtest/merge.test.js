import test from "node:test";
import assert from "node:assert/strict";
import { mergeRow, mergeRows, olderThan } from "../web/merge.js";

// spec 043 AC5, D10: the merge rule for a row that arrives while one of the same id is held.
const v = (n, o = {}) => ({ exchanges: n, max_row: n, parsed: n, hooks: n, max_hook: n, ...o });
const full = (o = {}) => ({
  id: "a", session_key: "k", client: "c", first_at: "2026-01-01T00:00:00Z", last_at: "2026-01-01T00:05:00Z", exchange_count: 3,
  active: false, hooks_only: false, has_calls: true, effects: { changed: 1 }, row_version: v(5), version: v(5),
  title: "Fix it", models: ["m"], usage: {}, tool_states: {}, main_errors: 0, list: { prompts: 2, folder: "~/p", folder_full: "/home/u/p" }, ...o,
});
const pending = (o = {}) => ({
  id: "a", session_key: "k", client: "c", first_at: "2026-01-01T00:00:00Z", last_at: "2026-01-01T00:05:00Z", exchange_count: 3,
  active: false, hooks_only: false, has_calls: true, effects: { changed: 1 }, list: { folder: "~/p", folder_full: "/home/u/p" }, row_version: v(5), pending: true, ...o,
});

test("olderThanIsComponentwiseAndStrict", () => {
  assert.equal(olderThan(v(4), v(5)), true);
  assert.equal(olderThan(v(5), v(5)), false, "equal is not older");
  assert.equal(olderThan(v(5, { hooks: 9 }), v(5)), false, "one field ahead is not older");
  assert.equal(olderThan(v(4, { hooks: 9 }), v(5)), false, "mixed is not older");
  assert.equal(olderThan(v(4), v(5, { hooks: 3 })), false);
  assert.equal(olderThan(undefined, v(5)), false, "a missing version is never older");
  assert.equal(olderThan(v(4), undefined), false);
});

test("pendingThenFullAtEqualVersionsShowsTheFacts", () => {
  const out = mergeRow(pending(), full());
  assert.equal(out.title, "Fix it");
  assert.equal(out.pending, undefined);
});

test("fullThenPendingKeepsTheFactsAndTakesTheCheapPart", () => {
  const held = full();
  const out = mergeRow(held, pending({ last_at: "2026-01-01T00:09:00Z", active: true, exchange_count: 4, row_version: v(6), list: { folder: "~/new", folder_full: "/home/u/new" } }));
  assert.equal(out.title, "Fix it", "the facts stay");
  assert.equal(out.pending, undefined, "shown as filled");
  assert.deepEqual(out.version, v(5), "the facts keep their own version");
  assert.deepEqual([out.last_at, out.active, out.exchange_count, out.row_version], ["2026-01-01T00:09:00Z", true, 4, v(6)]);
  assert.deepEqual(out.list, { prompts: 2, folder: "~/new", folder_full: "/home/u/new" }, "the cheap folder wins, the other counts stay");
});

test("aPendingRowNeverWipesFactsEvenWhenItsVersionIsNewer", () => {
  const out = mergeRow(full({ version: v(2), row_version: v(2) }), pending({ row_version: v(9) }));
  assert.equal(out.title, "Fix it");
  assert.equal(out.pending, undefined);
  assert.deepEqual(out.row_version, v(9));
});

test("olderIncomingFactsAreRejectedButANewerCheapPartIsTaken", () => {
  const held = full({ version: v(5), row_version: v(5), title: "new" });
  const out = mergeRow(held, full({ version: v(3), row_version: v(6), title: "old", last_at: "2026-01-01T00:08:00Z" }));
  assert.equal(out.title, "new");
  assert.deepEqual(out.version, v(5));
  assert.equal(out.last_at, "2026-01-01T00:08:00Z");
  assert.deepEqual(out.row_version, v(6));
});

test("aStrictlyOlderRowVersionKeepsTheHeldCheapPart", () => {
  const held = full({ row_version: v(7), last_at: "2026-01-01T00:09:00Z" });
  const out = mergeRow(held, full({ row_version: v(4), version: v(7), last_at: "2026-01-01T00:01:00Z", title: "facts" }));
  assert.equal(out.last_at, "2026-01-01T00:09:00Z", "the older cheap part is rejected");
  assert.deepEqual(out.row_version, v(7));
  assert.equal(out.title, "facts", "facts at an equal version are taken");
  const same = mergeRow(held, pending({ row_version: v(4), last_at: "2026-01-01T00:01:00Z" }));
  assert.equal(same, held, "an older pending row changes nothing");
});

test("aRowWithoutRowVersionIsTakenAsItComes", () => {
  const held = full();
  const old = { id: "a", title: "older server", last_at: "2026-01-01T00:00:01Z" };
  assert.equal(mergeRow(held, old), old);
});

test("aNewRowIsTakenAndAnUnchangedRowKeepsItsObject", () => {
  const p = pending();
  assert.equal(mergeRow(undefined, p), p);
  const held = full();
  assert.equal(mergeRow(held, { ...held }), held, "equal content: the held object, so nothing is written");
  assert.equal(mergeRow(held, pending()), held, "a pending row with the same cheap part adds nothing");
});

test("mergeRowsFollowsTheAnswersOrderAndDropsRowsItLacks", () => {
  const a = full({ id: "a" });
  const b = full({ id: "b" });
  const out = mergeRows([a, b], [pending({ id: "c" }), pending({ id: "b" })]);
  assert.deepEqual(out.map((r) => r.id), ["c", "b"]);
  assert.equal(out[1], b, "b keeps its facts and is the same object");
  assert.equal(out[0].pending, true);
});
