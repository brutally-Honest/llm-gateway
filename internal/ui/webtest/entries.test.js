import test from "node:test";
import assert from "node:assert/strict";
import { compareEntries } from "../web/entries.js";

test("module loads and compareEntries orders by time", () => {
  const a = { start: "2026-01-01T00:00:05.12Z", exchange_id: "x", index: 0 };
  const b = { start: "2026-01-01T00:00:05.123Z", exchange_id: "x", index: 0 };
  assert.equal(compareEntries(a, b), -1);
  assert.equal(compareEntries(b, a), 1);
  assert.equal(compareEntries(a, a), 0);
});
