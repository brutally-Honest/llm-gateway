import test from "node:test";
import assert from "node:assert/strict";
import { breadcrumb } from "../web/shell.js";

test("breadcrumbStartsAtSessions", () => {
  assert.deepEqual(breadcrumb({ name: "sessions" }, ""), [{ text: "Sessions" }]);
  for (const route of [{ name: "ungrouped" }, { name: "exchange", id: "e" }, { name: "session", id: "s", call: null }]) {
    const b = breadcrumb(route, "t");
    assert.deepEqual(b[0], { text: "Sessions", href: "#/" });
  }
});

test("breadcrumbShowsSessionTitle", () => {
  const b = breadcrumb({ name: "session", id: "s1", call: null }, "Fix the bug");
  assert.deepEqual(b, [{ text: "Sessions", href: "#/" }, { text: "Fix the bug" }]);
  assert.equal(breadcrumb({ name: "session", id: "s1", call: null }, "")[1].text, "s1");
});

// spec 027 C2: the header's Settings button opens a dialog with a Theme and a Style picker;
// a pick applies and stores at once and marks the pressed button.
test("settingsDialogPicksThemeAndStyle", async () => {
  const { StubElement, all, installDom } = await import("./dom-stub.js");
  installDom();
  const { mountShell } = await import("../web/shell.js");
  const head = new StubElement("header");
  const bar = new StubElement("footer");
  const root = new StubElement("html");
  const m = {};
  const storage = { getItem: (k) => m[k] ?? null, setItem: (k, v) => { m[k] = v; } };
  const shell = mountShell({ head, bar, storage, root, theme: "dark", look: "lean" });
  const open = all(head).find((e) => e.className === "settings-open");
  const dlg = all(head).find((e) => e.tagName === "dialog");
  assert.ok(open && dlg, "a Settings button and its dialog");
  assert.equal(open.attrs["aria-haspopup"], "dialog");
  let shown = false;
  dlg.showModal = () => { shown = true; dlg.open = true; };
  dlg.close = () => { dlg.open = false; };
  open.fire("click");
  assert.ok(shown && shell.settingsOpen(), "the button opens it");
  const button = (text) => all(dlg).find((e) => e.tagName === "button" && e.textContent === text);
  const names = all(dlg).filter((e) => e.className === "setting-name").map((e) => e.textContent);
  assert.deepEqual(names, ["Theme", "Style"]);
  assert.equal(button("Dark").attrs["aria-pressed"], "true");
  assert.equal(button("Lean").attrs["aria-pressed"], "true");
  button("Stark").fire("click");
  assert.equal(root.attrs["data-style"], "stark");
  assert.equal(m.style, "stark");
  assert.equal(button("Stark").attrs["aria-pressed"], "true");
  assert.equal(button("Lean").attrs["aria-pressed"], "false");
  button("Light").fire("click");
  assert.equal(root.attrs["data-theme"], "light");
  assert.equal(root.attrs["data-style"], "stark", "the theme keeps the style");
  button("Close").fire("click");
  assert.equal(shell.settingsOpen(), false);
  assert.ok(!all(head).some((e) => e.attrs && e.attrs["aria-label"] === "Theme" && e.parentNode === head), "no theme picker left in the header itself");
});
