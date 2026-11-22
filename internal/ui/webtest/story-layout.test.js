import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { installDom, withClass } from "./dom-stub.js";

const css = readFileSync(new URL("../web/style.css", import.meta.url), "utf8");

function rule(sel) {
  const re = new RegExp(`(^|\\n)${sel.replace(/[.#[\]()*+?^$|\\>]/g, "\\$&")}\\s*\\{([^}]*)\\}`);
  const m = css.match(re);
  assert.ok(m, `style.css has a rule for ${sel}`);
  const out = {};
  for (const d of m[2].split(";")) {
    const i = d.indexOf(":");
    if (i > 0) out[d.slice(0, i).trim()] = d.slice(i + 1).trim();
  }
  return out;
}

// 029 AC9: the Preview / Raw bar does not touch the first turn card.
test("storyBarHasSpaceBelow", () => {
  const bar = rule(".story-bar");
  assert.match(bar["margin-bottom"] ?? "", /^[1-9]\d*px$/, "a non-zero gap below the bar");
});

// 029 AC10: a long prompt or answer scrolls inside a capped body, with no width shift.
test("turnTextBodyIsCappedAndScrollsInside", () => {
  const body = rule(".chapter .tx > .tx-b");
  assert.match(body["max-height"] ?? "", /^\d+(\.\d+)?rem$/, "a height cap");
  assert.equal(body["overflow-y"], "auto");
  assert.equal(body["scrollbar-gutter"], "stable", "the scrollbar does not change the text width");
  assert.match(body.padding ?? "", /\S/, "padding stays inside the scroll box");
});

test("rawInsideChapterAddsNoScrollOfItsOwn", () => {
  const raw = rule(".chapter .tx-b pre.raw");
  // the cap lives on the body, so Raw grows inside it: no second cap, no nested scrollbar
  assert.equal(raw["max-height"], "none");
  assert.equal(raw.overflow, "visible");
  // and the body's cap is not removed by any later chapter rule
  assert.doesNotMatch(css, /\.chapter \.tx[^{]*\.tx-b\s*\{[^}]*(max-height:\s*none|overflow(-y)?:\s*visible)/);
});

test("copyButtonStaysInHeaderOutsideScrollBox", async () => {
  const head = rule(".chapter .tx > .tx-h");
  assert.equal(head["max-height"], undefined);
  assert.equal(head.overflow, undefined);
  // and in the built block: the copy button is in .tx-h, never inside the scrolling .tx-b
  installDom();
  const { textBlock } = await import("../web/story.js");
  for (const opts of [{ shared: true }, {}]) {
    const box = textBlock("prompt", "Prompt", "some text", [], null, opts);
    const h = withClass(box, "tx-h")[0];
    const b = withClass(box, "tx-b")[0];
    assert.ok(h && b, "a head and a body");
    assert.equal(withClass(h, "copy").length, 1, "the copy button is in the header");
    assert.equal(withClass(b, "copy").length, 0, "and not in the scroll box");
  }
});

// a markdown code block inside the capped body must not be a second scroller vertically
test("markdownCodeBlockInsideTheTurnBodyHasNoCapOfItsOwn", () => {
  const pre = rule(".chapter .tx-b .md pre");
  assert.equal(pre["max-height"], "none");
  assert.equal(pre["overflow-y"], "visible");
  // long lines still scroll sideways inside the block rather than overflow the card
  assert.equal(pre["overflow-x"], "auto");
});
