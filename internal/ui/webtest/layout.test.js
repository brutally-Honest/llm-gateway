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

// The session page fills the window below the header in three columns: the turn list, the
// story and the right card. Only the story and the two side lists scroll (spec 016 AC3,
// mockups/v4-story.html); the story ends at its last turn, with no empty space after it
// (Karthik, 2026-11-13).
test("storyScrollsAloneBetweenTheTwoCards", () => {
  const body = rule('body[data-page="session"]');
  assert.equal(body.height, "100vh");
  assert.equal(body.overflow, "hidden");
  const cols = rule(".cols");
  assert.equal(cols.display, "grid");
  assert.equal(cols["grid-template-areas"], '"toc story side"');
  assert.equal(cols.height, "calc(100vh - var(--head-h))");
  assert.equal(rule(".story").overflow, "auto");
  assert.equal(rule(".toc, .storycol, .sidebar").overflow, "hidden", "the story sits in a card like the two side columns");
  assert.match(rule(".scroll").overflow, /auto/);
  assert.ok(!/\.story::after/.test(css), "no empty space under the last turn");
});

// Narrower windows fall back to one scrolling page with the turn list sticky on the left;
// below 760 px the story and the right card stack.
test("narrowWindowFallsBackToOnePage", () => {
  const mid = css.match(/@media \(max-width: 1080px\) \{([\s\S]*?)\n\}/);
  assert.ok(mid, "style.css has a 1080px media block");
  assert.match(mid[1], /body\[data-page="session"\] \{ height: auto; overflow: auto; \}/);
  assert.match(mid[1], /\.toc \{ position: sticky;/);
  assert.match(mid[1], /\.story \{ overflow: visible; \}/);
  const narrow = css.match(/@media \(max-width: 760px\) \{([\s\S]*?)\n\}/);
  assert.ok(narrow, "style.css has a 760px media block");
  assert.match(narrow[1], /grid-template-areas: "story" "side"/);
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

// The call dialog's scrim is a button inside the dialog at z-index -1, under its content. The
// 013 drawer's scrim, painted the same way, dimmed the drawer itself and turned opaque under
// the global button:hover rule (Karthik, 2026-11-14). The dialog's ::backdrop does the
// dimming, so its scrim paints nothing, hovered or not, and the dialog's card stays bright.
test("callScrimDimsOnlyThePageBehind", () => {
  const scrim = rule(".call-scrim");
  assert.equal(scrim.all, "unset");
  assert.equal(scrim.position, "fixed");
  assert.equal(scrim.inset, "0");
  assert.equal(scrim["z-index"], "-1");
  assert.equal(rule(".call-scrim, .call-scrim:hover").background, "transparent", "hover keeps it clear");
  assert.equal(rule("dialog.details::backdrop").background, "var(--scrim)", "the backdrop dims the page");
});

// A dialog's title block is class "tx", as is a turn's prompt and answer box (017 AC13): the
// box's border and padding must not reach the dialog head (seen in the 017 slice 2 check).
test("dialogTitleBlockIsNotATextBox", () => {
  const head = rule(".mh .tx");
  assert.equal(head.border, "0");
  assert.equal(head.padding, "0");
});

// The call dialog's reply is capped like a message box until "Show all"; shown all, the Raw
// view's <pre> drops the generic pre cap too, so the whole reply shows (research D41, seen
// in the 017 slice 2 check).
test("callReplyCapAndShowAllCoverRaw", () => {
  const capped = rule(".call-reply.is-capped .tx-b");
  assert.equal(capped["max-height"], rule(".call-messages .msg")["max-height"], "the same cap as a message box");
  assert.equal(capped.overflow, "auto");
  assert.equal(rule(".call-reply:not(.is-capped) .tx-b pre")["max-height"], "none", "Raw shows all too");
});

// spec 017 AC12: the full exchange page's two header tables line up: the Name column has one
// width in both, so Value starts at the same place (seen in the slice 3 check).
test("exchangeHeaderTablesLineUp", () => {
  assert.equal(rule(".exchange-page .sp-list th.hname").width, "18rem");
});

// spec 017 AC1, research D49: at 1,440 px the sessions list's Session column keeps at least
// 20rem — it has that minimum, and Work and Changes wrap onto two lines to make the room
// (Tools, Time and Tokens stay on one line). Checked in the browser at 1,440 and 2,560.
test("sessionColumnKeepsItsMinimumAt1440", () => {
  assert.equal(rule(".sp-list th.title, .sp-list td.title")["min-width"], "20rem");
  // below the page's 1,600 px width (1,440 included) Work and Changes wrap, two lines wide
  const narrow = css.match(/@media \(max-width: 1599px\) \{\n\.sp-list td\.work, \.sp-list td\.changes \{([^}]*)\}/);
  assert.ok(narrow, "Work and Changes wrap below 1,600 px");
  assert.match(narrow[1], /white-space: normal/);
  assert.match(narrow[1], /min-width: 12\.5rem/, "wide enough for two lines, not more");
  assert.equal(rule(".sp-list td .seg")["white-space"], "nowrap", "they wrap only at the dots");
  // wider, every count cell stays on one line
  assert.equal(rule(".sp-list td.work, .sp-list td.tools, .sp-list td.changes, .sp-list td.dur, .sp-list td.tokens")["white-space"], "nowrap");
});
