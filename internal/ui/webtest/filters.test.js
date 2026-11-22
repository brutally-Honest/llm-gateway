import test from "node:test";
import assert from "node:assert/strict";
import { FILTERS, normalizeKind } from "../web/filters.js";

// spec 017 AC4: All, Needs a look, Active, Hooks only, in that order. The server filters
// (spec 029); the browser only names the kinds.
test("filtersAreTheSpecKinds", () => {
  assert.deepEqual(FILTERS, ["all", "look", "active", "hooks"]);
});

// spec 029: a kind read from the URL is one of the four; anything else is "all".
test("normalizeKindFallsBackToAll", () => {
  for (const k of FILTERS) assert.equal(normalizeKind(k), k);
  for (const k of ["", "ALL", "nope", null, undefined]) assert.equal(normalizeKind(k), "all");
});
