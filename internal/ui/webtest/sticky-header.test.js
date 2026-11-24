import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../web/style.css", import.meta.url), "utf8");

// A colour token is a hex code: #rrggbb, or #rrggbbaa for a see-through (glass) colour, or a
// gradient token (--name-grad) whose stops are hex codes (spec 027 C6a).
function tokens(selector) {
  const block = css.split(selector)[1].split("}")[0];
  const out = Object.fromEntries([...block.matchAll(/--([a-z0-9-]+):\s*(#[0-9a-fA-F]{6}(?:[0-9a-fA-F]{2})?)\b/g)].map((m) => [m[1], m[2]]));
  for (const m of block.matchAll(/--([a-z0-9-]+-grad):\s*([^;]+);/g)) out[m[1]] = [...m[2].matchAll(/#[0-9a-fA-F]{6}\b/g)].map((x) => x[0]);
  return out;
}

const alpha = (hex) => (hex.length === 9 ? parseInt(hex.slice(7, 9), 16) / 255 : 1);

// spec 027 C2: each theme is one base; a style may override some of its colours.
const STYLES = ["lean", "stark"];
const THEMES = ["dark", "light"];
const styleBlock = (style, theme) => `:root[data-style="${style}"][data-theme="${theme}"]`;
const merged = (style, theme) => ({ ...tokens(`:root[data-theme="${theme}"]`), ...(css.includes(styleBlock(style, theme)) ? tokens(styleBlock(style, theme)) : {}) });

// rule returns the declarations of the first rule whose selector is exactly sel.
function rule(sel) {
  const re = new RegExp(`(^|\\n)${sel.replace(/[.#[\]()*+?^$|\\]/g, "\\$&")}\\s*\\{([^}]*)\\}`);
  const m = css.match(re);
  assert.ok(m, `style.css has a rule for ${sel}`);
  const out = {};
  for (const d of m[2].split(";")) {
    const i = d.indexOf(":");
    if (i > 0) out[d.slice(0, i).trim()] = d.slice(i + 1).trim();
  }
  return out;
}

// splitLayers splits a background value at its top-level commas; commas inside parentheses do not split.
function splitLayers(value) {
  const layers = [];
  let depth = 0;
  let cur = "";
  for (const ch of value) {
    if (ch === "(") depth++;
    if (ch === ")") depth--;
    if (ch === "," && depth === 0) {
      layers.push(cur.trim());
      cur = "";
    } else cur += ch;
  }
  layers.push(cur.trim());
  return layers;
}

// spec 042 AC1: the sessions header paints above the rows, which are positioned for their stretched link.
test("sessionsHeaderStacksAboveRows", () => {
  const r = rule(".sp-list th");
  assert.equal(r.position, "sticky");
  assert.ok(/^\d+$/.test(r["z-index"] ?? "") && parseInt(r["z-index"], 10) >= 1, `z-index is ${r["z-index"]}`);
});

// spec 042 AC1: the header background is opaque in every style and theme, so rows never show through it.
test("sessionsHeaderIsOpaqueInEveryStyleAndTheme", () => {
  for (const style of STYLES) {
    for (const theme of THEMES) {
      const value = style === "lean" ? rule(':root[data-style="lean"] .sp-list th').background : rule(".sp-list th").background;
      const bottom = splitLayers(value).at(-1);
      const m = bottom.match(/^var\(--([a-z0-9-]+)\)$/);
      assert.ok(m, `${style} ${theme}: the bottom background layer "${bottom}" is not a var(--token)`);
      const token = m[1];
      const t = merged(style, theme);
      assert.ok(t[token], `${style} ${theme}: token --${token} is missing`);
      assert.equal(alpha(t[token]), 1, `${style} ${theme}: the header background --${token} (${t[token]}) is see-through`);
    }
  }
});

// spec 042 AC1: the line under the header is a box-shadow, because a collapsed border does not travel with a sticky cell.
test("sessionsHeaderHasALineUnderIt", () => {
  assert.match(rule(".sp-list th")["box-shadow"], /var\(--border\)/);
});
