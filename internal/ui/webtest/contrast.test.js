import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const css = readFileSync(new URL("../web/style.css", import.meta.url), "utf8");

function tokens(selector) {
  const block = css.split(selector)[1].split("}")[0];
  return Object.fromEntries([...block.matchAll(/--([a-z0-9-]+):\s*(#[0-9a-fA-F]{6})/g)].map((m) => [m[1], m[2]]));
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

// every text colour against every background it is drawn on (spec 016 AC14): the text,
// status and diff colours on the page, card and panel shades and on the tinted rows they sit
// in, and the button text on the strong accent
const TEXTS = ["text", "hi", "muted", "accent", "ok", "bad", "warn", "add", "del", "model", "toolc"];
const BACKS = ["bg", "card", "panel", "panel2", "accent-bg", "ok-bg", "bad-bg", "warn-bg", "add-bg", "del-bg", "tool-bg"];
const PAIRS = [...TEXTS.flatMap((fg) => BACKS.map((bg) => [fg, bg])), ["on-accent", "accent-strong"]];

// spec 027 C2: each theme is one base; a style may override some of its colours. What a
// page shows is the base with the style's overrides on top, so every style × theme pair is
// checked as merged, and a style block the list below does not name fails the test.
const STYLES = ["lean", "stark"];
const THEMES = ["dark", "light"];
const styleBlock = (style, theme) => `:root[data-style="${style}"][data-theme="${theme}"]`;
const merged = (style, theme) => ({ ...tokens(`:root[data-theme="${theme}"]`), ...(css.includes(styleBlock(style, theme)) ? tokens(styleBlock(style, theme)) : {}) });

test("everyStyleBlockIsChecked", () => {
  const found = [...css.matchAll(/:root\[data-style="([a-z]+)"\]\[data-theme="([a-z]+)"\]/g)].map((m) => [m[1], m[2]]);
  assert.ok(found.length >= 2, "the style blocks are found");
  for (const [style, theme] of found) assert.ok(STYLES.includes(style) && THEMES.includes(theme), `${style} ${theme} is not checked`);
});

test("allTextPairsMeet4_5InBothThemes", () => {
  for (const [name, t] of STYLES.flatMap((s) => THEMES.map((th) => [`${s} ${th}`, merged(s, th)]))) {
    for (const [fg, bg] of PAIRS) {
      assert.ok(t[fg] && t[bg], `${name}: missing ${fg} or ${bg}`);
      assert.ok(ratio(t[fg], t[bg]) >= 4.5, `${name}: ${fg} on ${bg} is ${ratio(t[fg], t[bg]).toFixed(2)}`);
    }
  }
});

// spec 017 AC12: the ungrouped, unassigned and full exchange pages draw only with the theme's
// tokens, so every colour they use is one of the pairs checked above in both themes.
test("otherPagesUseOnlyThemeColours", () => {
  const rules = [...css.matchAll(/([^{}]+)\{([^}]*)\}/g)].filter((m) => /\.page\b|\.page-sub|\.ungrouped-page|\.unassigned-page|\.exchange-page|\.st-ok|\.st-failed/.test(m[1]));
  assert.ok(rules.length >= 8, `found ${rules.length} rules for the pages`);
  for (const [, sel, body] of rules) {
    assert.doesNotMatch(body, /#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(/, `${sel.trim()} uses a colour outside the theme`);
  }
});
