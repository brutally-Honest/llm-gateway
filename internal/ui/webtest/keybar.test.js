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
  b = keyBar(turns, { index: 2, total: 3 });
  assert.equal(b.level, "TURNS");
  assert.equal(b.position, "turn 2/3");
  assert.deepEqual(keys(b), [
    "j/k turn", "e next error", "t next tool", "Enter open first call", "h hooks", "/ filter", "Backspace Sessions",
  ]);
  b = keyBar(call, { index: 3, total: 4 });
  assert.equal(b.level, "CALL");
  assert.equal(b.position, "call #3/4");
  assert.deepEqual(keys(b), ["↑/↓ call", "Esc close", "t back to turn"]);
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
  for (const [key, type] of [["k", "turn"], ["e", "nextError"], ["t", "nextTool"], ["Enter", "openFirstCall"], ["h", "hooks"], ["/", "filter"], ["Backspace", "toSessions"]]) {
    assert.equal(handleKey(turns, { key }).type, type);
  }
  assert.deepEqual(handleKey(turns, { key: "k" }), { type: "turn", delta: -1 });
  const cards = [{ errored: false, tools: [] }, { errored: true, tools: [{}] }, { errored: false, tools: [{}] }, { errored: true, tools: [] }];
  assert.equal(turnTarget(cards, 0, "error"), 1);
  assert.equal(turnTarget(cards, 1, "error"), 3);
  assert.equal(turnTarget(cards, 3, "error"), 1);
  assert.equal(turnTarget(cards, 0, "tool"), 1);
  assert.equal(turnTarget(cards, 2, "tool"), 1);
  assert.equal(turnTarget([{ errored: false, tools: [] }], 0, "error"), -1);
  assert.equal(turnTarget([], 0, "tool"), -1);
});
