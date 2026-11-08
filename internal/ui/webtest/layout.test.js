import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { StubElement, installDom } from "./dom-stub.js";

const css = readFileSync(new URL("../web/style.css", import.meta.url), "utf8");

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

// The turn rail sits below the header and above the key bar at any window height, and
// scrolls on its own: it used to stop at 80% of the window with dead space below
// (013 manual check, F7). The header stays at the top so "below the header" holds while
// the page scrolls.
test("railStaysBetweenHeaderAndKeyBarAtAnyHeight", () => {
  const rail = rule(".rail");
  assert.equal(rail.position, "sticky");
  assert.equal(rail.top, "var(--head-h)");
  assert.equal(rail["max-height"], "calc(100vh - var(--head-h) - var(--keybar-h))");
  assert.equal(rail["overflow-y"], "auto");
  const head = rule("header");
  assert.equal(head.position, "sticky");
  assert.equal(head.top, "0");
  const root = rule(":root");
  assert.match(root["--head-h"] || "", /^\d/, "a fallback header height before the shell measures it");
  assert.match(root["--keybar-h"] || "", /^\d/, "a fallback key bar height");
});

// The header and the key bar can wrap to two lines, so the shell measures them and
// publishes their heights for the rail's sticky offset and height.
test("shellPublishesHeaderAndKeyBarHeights", async () => {
  installDom();
  const watched = new Map();
  globalThis.ResizeObserver = class {
    constructor(fn) {
      this.fn = fn;
    }
    observe(e) {
      watched.set(e, this.fn);
    }
  };
  Object.assign(document, {
    createDocumentFragment: () => new StubElement("fragment"),
    createTextNode: (text) => Object.assign(new StubElement("text"), { textContent: text }),
  });
  try {
    const { mountShell } = await import("../web/shell.js");
    const head = Object.assign(new StubElement("header"), { offsetHeight: 41 });
    const bar = Object.assign(new StubElement("footer"), { offsetHeight: 29 });
    const props = {};
    const root = new StubElement("html");
    root.style = { setProperty: (k, v) => (props[k] = v) };
    mountShell({ head, bar, storage: null, root, theme: "dark" });
    assert.ok(watched.has(head) && watched.has(bar), "the shell watches both");
    for (const fn of new Set(watched.values())) fn([]);
    assert.equal(props["--head-h"], "41px");
    assert.equal(props["--keybar-h"], "29px");
    bar.offsetHeight = 52; // the key bar wrapped to two lines
    watched.get(bar)([]);
    assert.equal(props["--keybar-h"], "52px");
  } finally {
    delete globalThis.ResizeObserver;
  }
});
