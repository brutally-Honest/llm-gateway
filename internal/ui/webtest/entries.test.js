import test from "node:test";
import assert from "node:assert/strict";
import { compareEntries, omittedNote, hookLabel, hookInputEntry, prettyJSON, readableJSON } from "../web/entries.js";

test("module loads and compareEntries orders by time", () => {
  const a = { start: "2026-01-01T00:00:05.12Z", exchange_id: "x", index: 0 };
  const b = { start: "2026-01-01T00:00:05.123Z", exchange_id: "x", index: 0 };
  assert.equal(compareEntries(a, b), -1);
  assert.equal(compareEntries(b, a), 1);
  assert.equal(compareEntries(a, a), 0);
});

test("exchange sorts before hook lines on an exact time tie", () => {
  const at = "2026-01-01T00:00:05Z";
  const hook = { id: "hook:1", kind: "hook_event", exchange_id: "", index: 0, start: at };
  const call = { id: "a:0", kind: "tool_call", exchange_id: "a", index: 0, start: at };
  assert.equal(compareEntries(call, hook), -1);
  assert.equal(compareEntries(hook, call), 1);
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

// spec 046 U10: the tool dialog's Input shows strings with line breaks as real lines.
test("readableJSONShowsLineBreaksInStrings", () => {
  const text = JSON.stringify({ command: "echo one\necho two", n: 2, flag: true, list: ["a", "b\nc"], nested: { text: "x\ny" } });
  const out = readableJSON(text);
  assert.ok(out.includes("echo one\n    echo two"), "a real newline between the two lines");
  assert.ok(!out.includes("\\n"), "no literal backslash-n");
  assert.equal(out, [
    "{",
    '  "command": "echo one',
    '    echo two",',
    '  "n": 2,',
    '  "flag": true,',
    '  "list": [',
    '    "a",',
    '    "b',
    '      c"',
    "  ],",
    '  "nested": {',
    '    "text": "x',
    '      y"',
    "  }",
    "}",
  ].join("\n"));
  // a string without a newline is its JSON form
  assert.equal(readableJSON(JSON.stringify({ a: "plain" })), prettyJSON(JSON.stringify({ a: "plain" })));
  assert.equal(readableJSON(JSON.stringify({ a: "b" })), prettyJSON(JSON.stringify({ a: "b" })));
  // quotes and backslashes inside a multi-line string are escaped as JSON escapes them
  assert.equal(readableJSON(JSON.stringify({ msg: 'say "hi"\nok\\' })), '{\n  "msg": "say \\"hi\\"\n    ok\\\\"\n}');
  // a top-level multi-line string and an empty container keep their JSON shape
  assert.equal(readableJSON(JSON.stringify("p\nq")), '"p\n  q"');
  assert.equal(readableJSON(JSON.stringify({ e: [], o: {} })), '{\n  "e": [],\n  "o": {}\n}');
});

test("readableJSONReturnsNonJSONUnchanged", () => {
  assert.equal(readableJSON("not json\nat all"), "not json\nat all");
  assert.equal(readableJSON(""), "");
});
