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

// The session page is one page that scrolls (spec 027 C4, replacing spec 016 AC3's three
// scrolling columns with Karthik's OK, 2026-11-21): the turns as contents on the left stay
// in view, the main column holds the overview and the tabs, the story first; the tab bar
// stays under the header. Nothing in the story scrolls on its own, and the story ends at its
// last turn, with no empty space after it (Karthik, 2026-11-13).
test("sessionPageIsOneScrollWithContentsInView", () => {
  assert.doesNotMatch(css, /body\[data-page="session"\] \{[^}]*(height: 100vh|overflow: hidden)/, "the window scrolls, not columns");
  const cols = rule(".cols");
  assert.equal(cols.display, "grid");
  assert.equal(cols["grid-template-areas"], '"toc story"');
  assert.equal(cols.height, undefined, "the page grows with its story");
  const toc = rule(".toc");
  assert.equal(toc.position, "sticky");
  assert.equal(toc["align-self"], "start");
  const story = rule(".story");
  assert.equal(story.overflow, undefined, "the story does not scroll inside a box");
  assert.equal(story["max-height"], undefined);
  assert.equal(rule(".sesstabs > .tabs").position, "sticky");
  assert.ok(!/\.story::after/.test(css), "no empty space under the last turn");
});

// Narrower windows give the contents less room; below 760 px they go and the story is alone.
test("narrowWindowFallsBackToOnePage", () => {
  const mid = css.match(/@media \(max-width: 1080px\) \{([\s\S]*?)\n\}/);
  assert.ok(mid, "style.css has a 1080px media block");
  assert.match(mid[1], /\.cols \{ grid-template-columns: 15rem minmax\(0, 1fr\);/);
  const narrow = css.match(/@media \(max-width: 760px\) \{([\s\S]*?)\n\}/);
  assert.ok(narrow, "style.css has a 760px media block");
  assert.match(narrow[1], /grid-template-areas: "story"/);
  assert.match(narrow[1], /\.toc \{ display: none; \}/);
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

// The call dialog's reply is folded until "Show all"; shown all, the Raw view's <pre> drops
// the generic pre cap too, so the whole reply shows (research D41, seen in the 017 slice 2
// check). Spec 027 C3 (Karthik, 2026-11-21): the panel is one scroll, so neither the folded
// reply nor a message box scrolls on its own, and the panel is docked full height on the right.
test("callPanelFoldsWithoutInnerScrolls", () => {
  const capped = rule(".call-reply.is-capped .tx-b");
  assert.equal(capped["max-height"], "12rem");
  assert.equal(capped.overflow, "hidden", "folded, not a box that scrolls");
  assert.equal(rule(".call-reply:not(.is-capped) .tx-b pre")["max-height"], "none", "Raw shows all too");
  const msg = rule(".call-messages .msg");
  assert.equal(msg["max-height"], undefined, "a message is not capped into a scroll box");
  assert.equal(msg.overflow, undefined);
  const panel = rule("dialog.call-details");
  assert.equal(panel.margin, "8px 8px 8px auto", "docked on the right");
  assert.equal(panel.height, "calc(100vh - 16px)", "full height");
  assert.equal(rule(".mb").overflow, "auto", "the body is the one scroll");
});

// spec 017 AC12: the full exchange page's two header tables line up: the Name column has one
// width in both, so Value starts at the same place (seen in the slice 3 check).
test("exchangeHeaderTablesLineUp", () => {
  assert.equal(rule(".exchange-page .sp-list th.hname").width, "18rem");
});

// spec 017 AC1, research D49, spec 020 D17: at 1,440 px the sessions list's Session column
// keeps at least 20rem and nothing is cut — it has that minimum, and Work, Changes and
// Tokens wrap onto two lines at their dots to make the room (Tools and Time stay on one
// line; the Tokens cell's cost stays a line of its own). Checked in the browser at 1,440
// and 2,560.
test("sessionColumnKeepsItsMinimumAt1440", () => {
  assert.equal(rule(".sp-list th.title, .sp-list td.title")["min-width"], "20rem");
  // below the page's 1,600 px width (1,440 included) Work, Changes and Tokens wrap, two lines wide
  const narrow = css.match(/@media \(max-width: 1599px\) \{\n\.sp-list td\.work, \.sp-list td\.changes, \.sp-list td\.tokens \{([^}]*)\}/);
  assert.ok(narrow, "Work, Changes and Tokens wrap below 1,600 px");
  assert.match(narrow[1], /white-space: normal/);
  assert.match(narrow[1], /min-width: 12\.5rem/, "wide enough for two lines, not more");
  assert.equal(rule(".sp-list td .seg")["white-space"], "nowrap", "they wrap only at the dots");
  assert.equal(rule(".sp-list td.tokens .cost")["display"], "block", "the cost is a line of its own");
  assert.equal(rule(".sp-list td.tokens .cost")["white-space"], "nowrap", "and never breaks inside");
  // wider, every count cell stays on one line
  assert.equal(rule(".sp-list td.work, .sp-list td.tools, .sp-list td.changes, .sp-list td.dur, .sp-list td.tokens")["white-space"], "nowrap");
});

// spec 027 C4 (Karthik, 2026-11-21): nothing shifts sideways when a scrollbar comes or goes:
// the page and every box that scrolls on its own keep room for their scrollbar.
test("scrollbarsNeverShiftTheLayout", () => {
  assert.equal(rule("html")["scrollbar-gutter"], "stable both-edges", "the page, with a matching strip on the left");
  assert.equal(rule(".scroll")["scrollbar-gutter"], "stable", "the turn list");
  assert.equal(rule(".mb")["scrollbar-gutter"], "stable", "a dialog's body");
  assert.equal(rule(".cols")["grid-template-columns"], "20rem minmax(0, 1fr)", "the turn list is 20rem wide");
});

// spec 027 C6a: a click outside an open dialog lands on its scrim, a fixed child that must
// cover the window. backdrop-filter (and filter, transform) on a dialog would make the dialog
// the box that scrim is placed in, so the click outside would hit nothing; and while a modal
// is open the page behind it does not scroll.
test("dialogsCloseOnAClickOutsideAndHoldThePage", () => {
  const dialogRules = [...css.matchAll(/([^{}]+)\{([^}]*)\}/g)].filter((m) => /\bdialog\b/.test(m[1]) && !/::backdrop/.test(m[1]));
  assert.ok(dialogRules.length >= 3, "the dialog rules are found");
  for (const [, sel, body] of dialogRules) assert.doesNotMatch(body, /(^|[;\s])(-webkit-)?(backdrop-filter|filter|transform)\s*:/, `${sel.trim()} would trap the scrim`);
  assert.equal(rule("html:has(dialog:modal)").overflow, "hidden");
});

// spec 027 C6a: a whole session row opens the session. The title stays the row's one real
// link and stretches over the row, rather than a click handler on the row.
test("wholeSessionRowOpensTheSession", () => {
  assert.equal(rule(".sp-list tbody tr:has(a.stitle)").position, "relative");
  const cover = rule(".sp-list a.stitle::after");
  assert.equal(cover.position, "absolute");
  assert.equal(cover.inset, "0");
  assert.equal(cover.content, '""');
});
