import test from "node:test";
import assert from "node:assert/strict";
import { filterSessions, filterTurns, toolNames } from "../web/filters.js";
import { buildTurns } from "../web/model.js";
import { call, item } from "./fixtures.js";

const row = (id, o = {}) => ({ id, title: id, client: "client-x", models: [], main_errors: 0, side_errors: 0,
  tool_states: {}, usage: { missing: 0, partial: 0 }, has_calls: true, has_hooks: false, ...o });

test("hasErrorsIgnoresSideCalls", () => {
  const rows = [row("a", { main_errors: 1 }), row("b", { errors: 2, side_errors: 2 }), row("c")];
  assert.deepEqual(filterSessions(rows, { kind: "errors" }).rows.map((r) => r.id), ["a"]);
});

test("hasGapsMatchesUnmatchedAndPartial", () => {
  const rows = [row("a", { tool_states: { unmatched: 1 } }), row("b", { usage: { missing: 1, partial: 0 } }),
    row("c", { usage: { missing: 0, partial: 2 } }), row("d")];
  assert.deepEqual(filterSessions(rows, { kind: "gaps" }).rows.map((r) => r.id), ["a", "b", "c"]);
  const hooks = [row("h", { has_hooks: true, has_calls: false }), row("x", { has_hooks: true })];
  assert.deepEqual(filterSessions(hooks, { kind: "hooks" }).rows.map((r) => r.id), ["h"]);
});

test("hasGapsMatchesHooksOnlySession", () => {
  const h = row("h", { hooks_only: true, has_calls: false, has_hooks: true,
    tool_states: { ok: 0, failed: 0, "not-run": 0, unmatched: 2, pending: 0, "no-hooks": 0 } });
  assert.deepEqual(filterSessions([h, row("d")], { kind: "gaps" }).rows.map((r) => r.id), ["h"]);
});

test("filterTextMatchesLoadedRows", () => {
  const rows = [row("a", { title: "Fix the Parser" }), row("b", { title: "other" })];
  const r = filterSessions(rows, { text: "parser", hasMore: true });
  assert.deepEqual(r.rows.map((x) => x.id), ["a"]);
  assert.equal(r.note, "filtering 2 loaded");
  assert.equal(r.loadMore, true);
  assert.equal(filterSessions(rows, {}).note, "");
});

test("noMatchNoteWithLoadMore", () => {
  const r = filterSessions([row("a")], { text: "zzz", hasMore: true });
  assert.equal(r.rows.length, 0);
  assert.equal(r.note, "no sessions match");
  assert.equal(r.loadMore, true);
});

test("toolPickerFiltersLoadedTurns", () => {
  const { cards } = buildTurns([
    call("a", 1), item("a", "user_prompt", { text: "first" }), item("a", "tool_call", { name: "Bash", tool_call_id: "t1" }),
    call("b", 2, { prev: "a" }), item("b", "user_prompt", { text: "second" }), item("b", "tool_call", { name: "Read", tool_call_id: "t2" }),
  ]);
  assert.deepEqual(toolNames(cards), ["Bash", "Read"]);
  const f = filterTurns(cards, { tool: "Read" });
  assert.equal(f.length, 1);
  assert.equal(f[0].prompt.text, "second");
  assert.equal(filterTurns(cards, { text: "FIRST" }).length, 1);
});
