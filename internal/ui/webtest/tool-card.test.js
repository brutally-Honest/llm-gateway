import test from "node:test";
import assert from "node:assert/strict";
import { all, installDom, withClass } from "./dom-stub.js";

installDom();
const { toolRowNode } = await import("../web/tool-card.js");
const { chipOf } = await import("../web/model.js");

const ctx = { isOpen: () => false, setOpen() {} };
const row = (call, extra = {}) => ({ id: call.id, name: call.name, chip: chipOf(call), agent: "", resultError: false, call, result: null, ...extra });

// section finds the <details> under the row whose summary has the label.
function section(node, label) {
  return all(node).find((c) => c.tagName === "details" && c.children.find((s) => s.tagName === "summary" && s.textContent === label));
}

test("the omitted-bytes note is appended to the hook section with its text", () => {
  const call = { id: "c", name: "Bash", input_differs: true, hook_input: "{}", hook_omitted_bytes: 42 };
  const notes = withClass(section(toolRowNode(row(call), ctx), "hook input"), "muted");
  assert.equal(notes.length, 1);
  assert.equal(notes[0].textContent, "… 42 bytes omitted");
});

test("the hook section takes its truncation from the hook input, not the call input", () => {
  const notes = (call) => withClass(section(toolRowNode(row(call), ctx), "hook input"), "muted");
  // the call input is cut short but the hook input is whole: no note
  assert.equal(notes({ id: "c", name: "Bash", input_differs: true, truncated: true, hook_omitted_bytes: 0 }).length, 0);
  // the hook input is cut short and the call input is whole: the note shows
  assert.equal(notes({ id: "c", name: "Bash", input_differs: true, truncated: false, hook_omitted_bytes: 5 }).length, 1);
});

test("the hook section has no raw link, while the input section keeps its own", () => {
  const node = toolRowNode(row({ id: "c", name: "Bash", exchange_id: "ex", input_differs: true, hook_input: "{}", hook_omitted_bytes: 5, truncated: true }), ctx);
  assert.equal(withClass(section(node, "hook input"), "truncated").length, 0);
  assert.equal(withClass(section(node, "input"), "truncated").length, 1);
});
