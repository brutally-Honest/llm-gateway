import test from "node:test";
import assert from "node:assert/strict";
import { handleKey, isTextInput, keyBar, turnTarget } from "../web/keys.js";

const keys = (bar) => bar.shortcuts.map((s) => `${s.key} ${s.label}`);
const sessions = { name: "sessions" };
const turns = { name: "session", id: "s", call: null };
const call = { name: "session", id: "s", call: "x" };

test("levelsShowOnlyWorkingShortcuts", () => {
  let b = keyBar(sessions, { index: 1, total: 3 });
  assert.equal(b.level, "SESSIONS");
  assert.equal(b.position, "session 1/3");
  assert.deepEqual(keys(b), ["↑/↓ select", "Enter open", "/ filter"]);
  // the session page and its call drawer have no key bar (spec 016 AC13, Q4)
  for (const route of [turns, call]) {
    b = keyBar(route, { index: 2, total: 3 });
    assert.deepEqual(b, { level: "", position: "", shortcuts: [] });
  }
});

test("shortcutsIgnoredInInputAndEscLeaves", () => {
  assert.equal(handleKey(turns, { key: "j", inInput: true }), null);
  assert.equal(handleKey(turns, { key: "Backspace", inInput: true }), null);
  assert.deepEqual(handleKey(turns, { key: "Escape", inInput: true }), { type: "leaveInput" });
  assert.deepEqual(handleKey(turns, { key: "j" }), { type: "turn", delta: 1 });
  assert.equal(handleKey(turns, { key: "j", ctrlKey: true }), null);
  assert.deepEqual(handleKey(call, { key: "Escape" }), { type: "close" });
  assert.equal(handleKey(sessions, { key: "j" }), null);
  assert.equal(isTextInput({ tagName: "INPUT", type: "search" }), true);
  assert.equal(isTextInput({ tagName: "INPUT", type: "checkbox" }), false);
  assert.equal(isTextInput({ tagName: "BUTTON" }), false);
});

test("ungroupedAndExchangeShowSessionsLevel", () => {
  for (const route of [{ name: "ungrouped" }, { name: "exchange", id: "e" }]) {
    const b = keyBar(route, { index: 1, total: 2 });
    assert.equal(b.level, "SESSIONS");
    assert.equal(b.position, "");
    assert.deepEqual(keys(b), ["Backspace Sessions"]);
    assert.deepEqual(handleKey(route, { key: "Backspace" }), { type: "toSessions" });
    assert.equal(handleKey(route, { key: "j" }), null);
  }
});

test("turnKeysMoveAndFindErrors", () => {
  // j/k step through turns; 013's other session keys are gone (spec 016 AC13, Q4)
  assert.deepEqual(handleKey(turns, { key: "k" }), { type: "turn", delta: -1 });
  for (const key of ["e", "t", "Enter", "h", "/", "Backspace"]) assert.equal(handleKey(turns, { key }), null, key);
  assert.deepEqual(handleKey(call, { key: "ArrowDown" }), { type: "call", delta: 1 });
  assert.equal(handleKey(call, { key: "t" }), null);
  const cards = [{ errored: false, tools: [] }, { errored: true, tools: [{}] }, { errored: false, tools: [{}] }, { errored: true, tools: [] }];
  assert.equal(turnTarget(cards, 0, "error"), 1);
  assert.equal(turnTarget(cards, 1, "error"), 3);
  assert.equal(turnTarget(cards, 3, "error"), 1);
  assert.equal(turnTarget(cards, 0, "tool"), 1);
  assert.equal(turnTarget(cards, 2, "tool"), 1);
  assert.equal(turnTarget([{ errored: false, tools: [] }], 0, "error"), -1);
  assert.equal(turnTarget([], 0, "tool"), -1);
});
