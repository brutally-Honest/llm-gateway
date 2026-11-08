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

// The turn rail is fixed from the header's bottom to the key bar's top, using their
// measured heights, and scrolls on its own; the content is offset by its width. A sticky
// rail misfit at the page's top and end (013 manual check, F7 then F7b). Below 760 px
// wide the rail returns to normal flow, above the turns.
test("railIsFixedBetweenHeaderAndKeyBar", () => {
  const rail = rule(".rail");
  assert.equal(rail.position, "fixed");
  assert.equal(rail.top, "var(--head-h)");
  assert.equal(rail.bottom, "var(--keybar-h)");
  assert.equal(rail.width, "var(--rail-w)");
  assert.equal(rail["overflow-y"], "auto");
  assert.equal(rail["max-height"], undefined, "no computed height: top and bottom set it");
  assert.equal(rule(".timeline-layout")["padding-left"], "calc(var(--rail-w) + 1rem)", "the content clears the rail");
  const head = rule("header");
  assert.equal(head.position, "sticky");
  assert.equal(head.top, "0");
  const root = rule(":root");
  assert.match(root["--head-h"] || "", /^\d/, "a fallback header height before the shell measures it");
  assert.match(root["--keybar-h"] || "", /^\d/, "a fallback key bar height");
  assert.match(root["--rail-w"] || "", /^\d/, "the rail's width");
});

test("narrowWindowPutsTheRailBackInFlow", () => {
  const m = css.match(/@media \(max-width: 760px\) \{([\s\S]*?)\n\}/);
  assert.ok(m, "style.css has a 760px media block");
  const inner = (sel) => {
    const r = m[1].match(new RegExp(`${sel.replace(".", "\\.")}\\s*\\{([^}]*)\\}`));
    assert.ok(r, `the media block styles ${sel}`);
    return r[1];
  };
  assert.match(inner(".rail"), /position:\s*static/);
  assert.match(inner(".rail"), /order:\s*-1/, "the rail comes before the turns");
  assert.match(inner(".timeline-layout"), /padding-left:\s*0/);
});

// The header and the key bar can wrap to two lines, so the shell measures them and
// publishes their heights, which place the fixed rail between them.
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
