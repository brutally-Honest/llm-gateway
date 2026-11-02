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
