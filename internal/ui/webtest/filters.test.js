import test from "node:test";
import assert from "node:assert/strict";
import { filterSessions, FILTERS } from "../web/filters.js";

const row = (id, o = {}) => ({ id, title: id, client: "client-x", models: [], main_errors: 0, side_errors: 0,
  tool_states: {}, usage: { missing: 0, partial: 0 }, has_calls: true, has_hooks: false, active: false, ...o });

// spec 017 AC4: All, Needs a look, Active, Hooks only, in that order.
test("filtersAreTheSpecKinds", () => {
  assert.deepEqual(FILTERS, ["all", "look", "active", "hooks"]);
});

// Needs a look reads the server's count only: a session whose side calls errored has
// nothing to look at unless the server says so (the old "Has errors" rule ignored side
// calls the same way).
test("needsLookReadsTheServerCount", () => {
  const rows = [row("a", { list: { needs_look: 2 } }), row("b", { errors: 2, side_errors: 2, list: { needs_look: 0 } }), row("c")];
  assert.deepEqual(filterSessions(rows, { kind: "look" }).rows.map((r) => r.id), ["a"]);
});

test("activeReadsTheServerFlag", () => {
  const rows = [row("a", { active: true }), row("b"), row("h", { hooks_only: true, has_calls: false, has_hooks: true, active: true })];
  assert.deepEqual(filterSessions(rows, { kind: "active" }).rows.map((r) => r.id), ["a", "h"]);
});

test("hooksOnlyMatchesHooksOnlySessions", () => {
  const hooks = [row("h", { has_hooks: true, has_calls: false }), row("x", { has_hooks: true }), row("y", { hooks_only: true, has_calls: false })];
  assert.deepEqual(filterSessions(hooks, { kind: "hooks" }).rows.map((r) => r.id), ["h", "y"]);
});

test("filterTextMatchesLoadedRows", () => {
  const rows = [row("a", { title: "Fix the Parser" }), row("b", { title: "other" }), row("c", { list: { folder: "~/work/parser-kit" } })];
  const r = filterSessions(rows, { text: "parser", hasMore: true });
  assert.deepEqual(r.rows.map((x) => x.id), ["a", "c"], "the folder is searched too");
  assert.equal(r.note, "filtering 3 loaded");
  assert.equal(r.loadMore, true);
  assert.equal(filterSessions(rows, {}).note, "");
});

// E6: nothing matches → "no sessions match", Load more kept while more rows exist.
test("noMatchNoteWithLoadMore", () => {
  const r = filterSessions([row("a")], { text: "zzz", hasMore: true });
  assert.equal(r.rows.length, 0);
  assert.equal(r.note, "no sessions match");
  assert.equal(r.loadMore, true);
  assert.equal(filterSessions([row("a")], { kind: "look", hasMore: true }).note, "no sessions match");
});
