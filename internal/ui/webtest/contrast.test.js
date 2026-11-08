import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../web/style.css", import.meta.url), "utf8");

function tokens(selector) {
  const block = css.split(selector)[1].split("}")[0];
  return Object.fromEntries([...block.matchAll(/--([a-z-]+):\s*(#[0-9a-fA-F]{6})/g)].map((m) => [m[1], m[2]]));
}

function lum(hex) {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  const [r, g, b] = c.map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
const ratio = (a, b) => {
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
};

// every text colour against every background it is drawn on
const PAIRS = [
  ["fg", "bg"], ["muted", "bg"], ["accent", "bg"], ["err", "bg"], ["ok", "bg"],
  ["fg", "surface"], ["muted", "surface"], ["accent", "surface"],
  ["badge-fg", "ok"], ["badge-fg", "err"], ["badge-fg", "muted"],
];

test("allTextPairsMeet4_5InBothThemes", () => {
  for (const [name, selector] of [["dark", ':root[data-theme="dark"]'], ["light", ':root[data-theme="light"]']]) {
    const t = tokens(selector);
    for (const [fg, bg] of PAIRS) {
      assert.ok(t[fg] && t[bg], `${name}: missing ${fg} or ${bg}`);
      assert.ok(ratio(t[fg], t[bg]) >= 4.5, `${name}: ${fg} on ${bg} is ${ratio(t[fg], t[bg]).toFixed(2)}`);
    }
  }
});
