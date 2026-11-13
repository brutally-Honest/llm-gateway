import test from "node:test";
import assert from "node:assert/strict";
import { attributionText } from "../web/effects.js";

// spec 015 AC12: the label as glyph and word, then "#n Name" from the model call that asked
// for the tool, the name alone when no captured call asked, every candidate for several;
// nothing when the server sent no label.
test("attributionText", () => {
  const nos = new Map([["t1", 1], ["t2", 2]]);
  assert.equal(attributionText({ label: "named", tool: { id: "t2", name: "Edit" } }, nos), "● named: #2 Edit");
  assert.equal(attributionText({ label: "only_one", tool: { id: "t9", name: "Bash" } }, nos), "◐ only one running: Bash");
  assert.equal(
    attributionText({ label: "several", candidates: [{ id: "t1", name: "Bash" }, { id: "t3", name: "Bash", agent_id: "a1" }] }, nos),
    "◌ several running: #1 Bash, Bash (agent)",
  );
  assert.equal(attributionText({ label: "outside" }, nos), "○ outside any tool call");
  assert.equal(attributionText({ label: "harness" }, nos), "⚙ harness");
  assert.equal(attributionText({ label: "harness", client: "client-a" }, nos), "⚙ harness: client-a");
  assert.equal(attributionText(undefined, nos), "");
});
