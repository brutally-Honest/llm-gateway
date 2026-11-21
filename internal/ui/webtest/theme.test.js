import test from "node:test";
import assert from "node:assert/strict";
import { initTheme, loadTheme, pickTheme } from "../web/theme.js";

const memory = (init = {}) => {
  const m = { ...init };
  return { m, getItem: (k) => (k in m ? m[k] : null), setItem: (k, v) => { m[k] = v; } };
};
const root = () => ({ attrs: {}, setAttribute(k, v) { this.attrs[k] = v; } });

test("pickStoresAndApplies", () => {
  const st = memory();
  const r = root();
  pickTheme(st, r, "light");
  assert.equal(r.attrs["data-theme"], "light");
  assert.equal(st.m.theme, "light");
});

test("usesStoredThemeOverOs", () => {
  // the OS preference is never consulted: a stored value wins and the default is dark
  assert.equal(initTheme(memory({ theme: "light" }), root()), "light");
  assert.equal(initTheme(memory(), root()), "dark");
});

test("storageUnavailableDefaultsDark", () => {
  const broken = { getItem() { throw new Error("denied"); }, setItem() { throw new Error("denied"); } };
  const r = root();
  assert.equal(initTheme(broken, r), "dark");
  assert.equal(r.attrs["data-theme"], "dark");
  assert.equal(pickTheme(broken, r, "light"), "light");
  assert.equal(r.attrs["data-theme"], "light");
  assert.equal(loadTheme(null), "dark");
});

test("unknownStoredValueDefaultsDark", () => {
  assert.equal(loadTheme(memory({ theme: "solarized" })), "dark");
});

// spec 027 C2: the style is stored and guarded like the theme: "lean" unless "stark" is stored,
// and it sets data-style on the root.
test("styleIsStoredAndAppliedDefaultingToLean", async () => {
  const { initStyle, loadStyle, pickStyle } = await import("../web/theme.js");
  const st = memory();
  const r = root();
  assert.equal(initStyle(st, r), "lean");
  assert.equal(r.attrs["data-style"], "lean");
  pickStyle(st, r, "stark");
  assert.equal(r.attrs["data-style"], "stark");
  assert.equal(st.m.style, "stark");
  assert.equal(st.m.theme, undefined, "the style leaves the theme alone");
  assert.equal(initStyle(memory({ style: "stark" }), root()), "stark");
  assert.equal(loadStyle(memory({ style: "clarity" })), "lean", "an older style name falls back to lean");
  assert.equal(loadStyle(memory({ style: "neon" })), "lean");
  const broken = { getItem() { throw new Error("denied"); }, setItem() { throw new Error("denied"); } };
  assert.equal(loadStyle(broken), "lean");
  assert.equal(pickStyle(broken, r, "stark"), "stark", "it still applies without storage");
  assert.equal(pickStyle(st, r, "bogus"), "lean");
});

// spec 027: boot.js sets the stored theme and style before the first paint, so a light or
// stark page never flashes dark lean on load. It cannot import theme.js (it is a plain
// script, which runs before paint), so it must agree with loadTheme and loadStyle on every
// stored value, including missing, unknown and unreadable storage; and index.html loads it
// in <head>, before the stylesheet, as a plain (blocking) script.
test("bootAppliesTheStoredThemeAndStyleBeforePaint", async () => {
  const { readFileSync } = await import("node:fs");
  const vm = await import("node:vm");
  const { loadStyle } = await import("../web/theme.js");
  const src = readFileSync(new URL("../web/boot.js", import.meta.url), "utf8");
  const broken = { getItem() { throw new Error("denied"); }, setItem() {} };
  const cases = [{}, { theme: "light" }, { theme: "dark", style: "stark" }, { theme: "solarized", style: "clarity" }, { style: "lean" }, null];
  for (const init of cases) {
    const storage = init ? memory(init) : broken;
    const r = root();
    vm.runInNewContext(src, { document: { documentElement: r }, window: { localStorage: storage } });
    assert.equal(r.attrs["data-theme"], loadTheme(storage), `theme for ${JSON.stringify(init)}`);
    assert.equal(r.attrs["data-style"], loadStyle(storage), `style for ${JSON.stringify(init)}`);
  }
  const html = readFileSync(new URL("../web/index.html", import.meta.url), "utf8");
  const head = html.split("</head>")[0];
  assert.match(head, /<script src="boot\.js"><\/script>\s*<link rel="stylesheet" href="style\.css">/, "boot.js is a plain script before the stylesheet");
});
