import test from "node:test";
import assert from "node:assert/strict";
import { chipOf } from "../web/model.js";

// The browser only displays the chip the server set; it never derives one.
test("sixChipsMapToGlyphAndText", () => {
  const cases = [
    ["ok", "✓", "ok"],
    ["failed", "✗", "failed"],
    ["not-run", "⊘", "not run"],
    ["unmatched", "?", "unmatched"],
    ["pending", "…", "pending"],
    ["no-hooks", "–", "no hooks"],
    [undefined, "–", "no hooks"],
  ];
  for (const [chip, glyph, text] of cases) {
    const c = chipOf({ chip });
    assert.equal(c.glyph, glyph);
    assert.equal(c.text, text);
  }
});

test("hoverTitleKeepsFullStateReasonAndOutcome", () => {
  assert.equal(chipOf({ chip: "not-run", state: "asked-not-run", reason: "denied" }).title, "asked-not-run: denied");
  assert.equal(chipOf({ chip: "failed", state: "asked+ran+reported", outcome: "failed" }).title, "asked+ran+reported: failed");
});

test("chipIsNotDerivedFromState", () => {
  const c = chipOf({ chip: "pending", state: "ran-not-reported", outcome: "succeeded" });
  assert.equal(c.key, "pending");
  assert.match(c.title, /ran-not-reported/);
  assert.equal(chipOf({ state: "asked+ran+reported", outcome: "failed" }).key, "no-hooks");
});
