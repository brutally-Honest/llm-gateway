import test from "node:test";
import assert from "node:assert/strict";
import { loadAllPages } from "../web/api.js";

test("loadsEveryTimelinePage", async () => {
  const pages = { undefined: { entries: [1, 2], next_after: "a" }, a: { entries: [3], next_after: "b" }, b: { entries: [4], next_after: null } };
  const asked = [];
  const got = [];
  await loadAllPages(async (after) => {
    asked.push(after);
    return pages[after];
  }, (p) => got.push(...p.entries));
  assert.deepEqual(asked, [undefined, "a", "b"]);
  assert.deepEqual(got, [1, 2, 3, 4]);
});

test("loadAllPagesStopsWhenTheViewMovedOn", async () => {
  let calls = 0;
  await loadAllPages(async () => {
    calls++;
    return { next_after: "more" };
  }, () => false);
  assert.equal(calls, 1);
});
