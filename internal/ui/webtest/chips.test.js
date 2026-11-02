import test from "node:test";
import assert from "node:assert/strict";
import { chipOf } from "../web/model.js";

test("sixStatesMapToGlyphAndText", () => {
  const cases = [
    [{ state: "asked+ran+reported", outcome: "succeeded" }, "✓", "ok"],
    [{ state: "asked+ran+reported", outcome: "failed" }, "✗", "failed"],
    [{ state: "asked-not-run", reason: "denied" }, "⊘", "not run"],
    [{ state: "ran-not-asked-or-not-captured" }, "?", "unmatched"],
    [{ state: "pending" }, "…", "pending"],
    [{ state: "ran-not-reported", outcome: "succeeded" }, "…", "pending"],
    [{ state: "no harness data" }, "–", "no hooks"],
    [{}, "–", "no hooks"],
  ];
  for (const [call, glyph, text] of cases) {
    const c = chipOf(call);
    assert.equal(c.glyph, glyph);
    assert.equal(c.text, text);
  }
  assert.equal(chipOf({ state: "asked-not-run", reason: "denied" }).title, "asked-not-run: denied");
  assert.equal(chipOf({ state: "asked+ran+reported", outcome: "failed" }).title, "asked+ran+reported: failed");
});

test("ranNotReportedStaysPendingInEndedSession", () => {
  const c = chipOf({ state: "ran-not-reported", outcome: "succeeded" }, { ended: true });
  assert.equal(c.key, "pending");
  assert.match(c.title, /ran-not-reported/);
});
