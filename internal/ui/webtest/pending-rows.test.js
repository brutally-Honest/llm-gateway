import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createSessionsPage, rowCells } from "../web/sessions-page.js";
import { StubElement, byTag, installDom, textOf, withClass } from "./dom-stub.js";

// spec 043 AC6, D3, D15: a pending row (the server's cheap fields only) renders at once with a
// quiet placeholder in every heavy cell; the facts fill in without moving anything.
const css = readFileSync(new URL("../web/style.css", import.meta.url), "utf8");
const v = (n) => ({ exchanges: n, max_row: n, parsed: n, hooks: n, max_hook: n });
const counter = (x) => ({ value: x, unreported: 0 });
const pending = (o = {}) => ({
  id: "s1", session_key: "k-123", client: "c", first_at: "2026-01-01T00:00:00Z", last_at: "2026-01-01T00:05:00Z", exchange_count: 3,
  active: false, hooks_only: false, has_calls: true, effects: { changed: 2, read: 0, sites: 1 },
  list: { folder: "~/work/proj", folder_full: "/home/u/work/proj" }, row_version: v(5), pending: true, ...o,
});
const full = (o = {}) => ({
  ...pending(), pending: undefined, title: "Fix it", has_hooks: true, model_calls: 3, main_errors: 0, models: ["m1"], tool_states: { ok: 4 },
  usage: { input: counter(10), output: counter(20), cache_write: counter(0), cache_read: counter(7) },
  list: { prompts: 2, main_calls: 3, side_calls: 1, commands: 4, clock_ms: 125000, folder: "~/work/proj", folder_full: "/home/u/work/proj", needs_look: 1, look_kinds: [{ words: "1 failed tool" }] },
  version: v(5), ...o,
});
function mount(items, extra = {}, handlers = {}) {
  installDom();
  Object.assign(document, {
    createDocumentFragment: () => new StubElement("fragment"),
    createTextNode: (text) => Object.assign(new StubElement("text"), { textContent: text }),
  });
  const page = createSessionsPage(handlers);
  page.update({ items, ...extra });
  const row = (id) => byTag(page.el, "tr").find((r) => r.dataset && r.dataset.id === id);
  return { page, row, cells: (id) => byTag(row(id), "td") };
}

test("aPendingRowShowsItsCheapCellsAndAPlaceholderInEveryHeavyCell", () => {
  const p = mount([pending()]);
  const tr = p.row("s1");
  const td = p.cells("s1");
  assert.equal(tr.className, "pending");
  assert.equal(tr.attrs["aria-busy"], "true");
  // real: start, folder in the place line, the session key on hover
  assert.match(textOf(td[1]), /\d/);
  assert.equal(withClass(tr, "splace")[0].textContent, "~/work/proj", "the folder, and no model yet");
  assert.equal(withClass(tr, "splace")[0].title, "/home/u/work/proj");
  const link = byTag(tr, "a")[0];
  assert.equal(link.title, "session key k-123");
  assert.equal(link.textContent, "Details loading", "what a screen reader reads under the title bar");
  // placeholders: every heavy cell holds no text of its own (the stylesheet draws its bar)
  for (const i of [3, 4, 5]) assert.equal(textOf(td[i]).trim(), "", `cell ${i} is empty`);
  assert.ok(td[6].children.every((e) => e.hidden), "Tokens: no count, dot, cost or bar shows");
  assert.equal(withClass(td[6], "cost")[0].hidden, true);
  assert.equal(withClass(td[6], "tokbar")[0].hidden, true);
  assert.equal(withClass(tr, "slook")[0].textContent, "", "no needs-a-look text");
  // the status cell is a placeholder, not the empty "ended" cell
  const icon = withClass(td[0], "sicon")[0];
  assert.ok(icon, "a glyph");
  assert.equal(icon.attrs["aria-label"], "details loading");
  assert.ok(icon.className.includes("sicon-pending"));
});

test("aPendingRowIsActiveOrHooksOnlyFromItsCheapFieldsAlone", () => {
  const a = mount([pending({ active: true }), pending({ id: "s2", hooks_only: true, has_calls: false })]);
  assert.equal(withClass(a.cells("s1")[0], "sicon")[0].attrs["aria-label"], "active");
  assert.equal(withClass(a.cells("s2")[0], "sicon")[0].attrs["aria-label"], "hooks only");
  assert.equal(rowCells(pending({ has_calls: false })).hooksOnly, false, "no has_hooks on a pending row: not guessed");
  assert.equal(rowCells(pending({ hooks_only: true })).hooksOnly, true);
  assert.equal(rowCells(pending()).status.key, "pending");
});

test("factsArrivingFillTheCellsAndWriteOnlyWhatChanged", () => {
  const p = mount([pending({ active: true })]);
  const tr = p.row("s1");
  const td = p.cells("s1");
  const start = byTag(td[1], "time")[0];
  const status = withClass(td[0], "sicon")[0];
  // count the writes to cells that must not change
  let writes = 0;
  for (const e of [td[0], td[1]]) {
    const re = e.replaceChildren.bind(e);
    e.replaceChildren = (...a) => {
      writes++;
      return re(...a);
    };
  }
  const place = withClass(tr, "splace")[0];
  let placeWrites = 0;
  let t = place.textContent;
  Object.defineProperty(place, "textContent", { get: () => t, set: (x) => { placeWrites++; t = x; } });
  p.page.update({ items: [full({ active: true })] });
  assert.equal(writes, 0, "the start and status cells are not rewritten");
  assert.equal(byTag(td[1], "time")[0], start);
  assert.equal(withClass(td[0], "sicon")[0], status);
  assert.equal(placeWrites, 1, "the place line gains only the model");
  assert.equal(place.textContent, "~/work/proj · m1");
  assert.equal(tr.className, "");
  assert.equal(tr.attrs["aria-busy"], undefined);
  assert.equal(byTag(tr, "a")[0].textContent, "Fix it");
  assert.match(textOf(td[3]), /2 prompts/);
  assert.match(textOf(td[4]), /2 files changed/);
  assert.equal(td[5].textContent, "2m 05s");
  assert.match(textOf(td[6]), /10 in/);
  assert.equal(withClass(td[6], "tok")[0].hidden, false, "the first count shows again");
  assert.equal(withClass(tr, "slook")[0].textContent, "1 failed tool");
});

test("aRowThatStaysPendingIsNotRewritten", () => {
  const p = mount([pending()]);
  const td = p.cells("s1");
  let writes = 0;
  for (const e of [td[3], td[4], td[5], td[6], ...withClass(td[6], "tok")]) {
    let t = e.textContent;
    const re = e.replaceChildren.bind(e);
    e.replaceChildren = (...a) => {
      writes++;
      return re(...a);
    };
    Object.defineProperty(e, "textContent", { get: () => t, set: (x) => { writes++; t = x; } });
  }
  p.page.update({ items: [pending({ last_at: "2026-01-01T00:09:00Z", exchange_count: 4 })] });
  assert.equal(writes, 0, "the heavy cells are left alone");
});

// ---- no layout jump (checked by the rules; the browser check is in docs/manual-checks) ----
const ruleBody = (sel, last = false) => {
  const at = `\n${sel} {`;
  const i = last ? css.lastIndexOf(at) : css.indexOf(at);
  assert.ok(i >= 0, `style.css has a rule for ${sel}`);
  return css.slice(i, css.indexOf("}", i));
};
const rem = (body, prop) => {
  const m = new RegExp(`${prop}: (\\d+(?:\\.\\d+)?)rem`).exec(body);
  assert.ok(m, `${prop} in ${body}`);
  return Number(m[1]);
};

const px = (body, prop) => {
  const m = new RegExp(`${prop}: (\\d+)px`).exec(body);
  assert.ok(m, `${prop} in ${body}`);
  return Number(m[1]);
};
const wideCss = css.slice(css.indexOf("@media (min-width: 1600px) {\n.sessions-page > .sp-list th.work"));
const wideRule = (sel) => {
  const i = wideCss.indexOf(`\n${sel} {`);
  assert.ok(i >= 0, `the wide block has ${sel}`);
  return wideCss.slice(i, wideCss.indexOf("}", i));
};

test("theSessionsTableIsFixedWithExplicitWidthsAtBothBreakpoints", () => {
  assert.match(ruleBody(".sessions-page > .sp-list table"), /table-layout: fixed/);
  assert.equal(px(ruleBody(".sessions-page > .sp-list th.status"), "width"), 23);
  assert.equal(px(ruleBody(".sessions-page > .sp-list th.time"), "width"), 134);
  assert.equal(px(ruleBody(".sessions-page > .sp-list th.dur"), "width"), 74);
  // below 1,600 px: Work, Changes and Tokens 175 px; Session (no width) takes the rest
  assert.equal(px(ruleBody(".sessions-page > .sp-list th.work, .sessions-page > .sp-list th.changes, .sessions-page > .sp-list th.tokens"), "width"), 175);
  // the automatic layout's Session rule stays for other .sp-list tables (ungrouped exchanges); here it is off
  const title = ruleBody(".sessions-page > .sp-list th.title, .sessions-page > .sp-list td.title");
  assert.match(title, /width: auto/);
  assert.match(title, /max-width: none/);
  assert.match(title, /min-width: 0/);
  // from 1,600 px
  assert.equal(px(wideRule(".sessions-page > .sp-list th.work"), "width"), 330);
  assert.equal(px(wideRule(".sessions-page > .sp-list th.changes"), "width"), 267);
  assert.equal(px(wideRule(".sessions-page > .sp-list th.tokens"), "width"), 231);
});

test("onlyTheSessionsTableIsFixedAndNoColumnHasAFloorAnyMore", () => {
  assert.equal(css.split("\n").filter((l) => /^[^/ ].*table-layout: fixed/.test(l)).length, 1, "one fixed table");
  // the unassigned connections table sits under .sp-unassigned, not directly in .sessions-page
  assert.doesNotMatch(css, /sp-unassigned[^{]*table-layout/);
  assert.match(ruleBody(".sp-unassigned .sp-list table"), /min-width: 0/);
  // the sessions table has no floor on any column (other .sp-list tables keep main's 12.5rem on Work, Changes, Tokens)
  const pending = css.split("\n").filter((l) => /^\.sp-list tr\.pending|^\.sessions-page > \.sp-list [th]d\./.test(l)).join("\n");
  assert.doesNotMatch(pending, /min-width: ?[1-9]\d*(\.\d+)?rem/, "no min-width floors on the heavy columns");
  assert.match(css, /\.sessions-page > \.sp-list td\.work, \.sessions-page > \.sp-list td\.changes, \.sessions-page > \.sp-list td\.tokens \{ min-width: 0; \}/);
});

test("heavyPlaceholdersReserveTheLinesTheFilledCellsTakeAtEachWidth", () => {
  assert.match(css, /\n\.sp-list \{ --pend-work: 3; --pend-changes: 2; --pend-figures: 2; \}/);
  assert.match(css, /min-width: 1600px\) \{\n\.sp-list \{ --pend-work: 1; --pend-changes: 1; --pend-figures: 1; \}/);
  assert.match(ruleBody(".sp-list tr.pending td.work::before"), /margin-bottom: calc\(var\(--pend-work\) \* 1lh - 1em\)/);
  assert.match(ruleBody(".sp-list tr.pending td.changes::before"), /margin-bottom: calc\(var\(--pend-changes\) \* 1lh - 1em\)/);
  assert.match(ruleBody(".sp-list tr.pending td.tokens::before"), /margin-bottom: calc\(var\(--pend-figures\) \* 1lh - 1em\)/);
  // no hard-coded row height
  assert.doesNotMatch(css.split("\n").filter((l) => l.includes("tr.pending")).join("\n"), /\btr\.pending\s*\{[^}]*height/);
});

test("placeholdersKeepTheLinesTheFilledCellsHave", () => {
  // the title link keeps its line (its text is only made transparent) and the place line is
  // kept even with no model yet
  const t = ruleBody(".sp-list tr.pending a.stitle");
  assert.match(t, /color: transparent/);
  assert.doesNotMatch(t, /display: none|height:/, "the link's own height is the real one");
  assert.match(ruleBody(".sp-list tr.pending .splace"), /display: block/);
  // Tokens: figures lines, then the cost line and the bar (4px bar and its 5px gap)
  assert.match(ruleBody(".sp-list tr.pending td.tokens::before"), /width: 10rem/);
  assert.match(ruleBody(".sp-list tr.pending td.tokens::after"), /height: 1em/);
  assert.match(ruleBody(".sp-list tr.pending td.tokens::after"), /margin-bottom: calc\(1lh - 1em \+ 9px\)/, "the cost line, then the 4px bar and its 5px gap");
  // a one-line bar in Work, Changes and Time
  assert.match(ruleBody(".sp-list tr.pending td.work::before, .sp-list tr.pending td.changes::before, .sp-list tr.pending td.dur::before, .sp-list tr.pending td.tokens::before"), /height: 1em/);
});

test("placeholderBarsUseThemeTokensOfBothStylesAndBothThemesAndNeverAnimate", () => {
  const bar = ruleBody(".sp-list tr.pending td.work::before, .sp-list tr.pending td.changes::before, .sp-list tr.pending td.dur::before, .sp-list tr.pending td.tokens::before");
  assert.match(bar, /background: var\(--panel2\)/);
  // --panel2 is defined for dark and light, Lean and Stark (Stark inherits the base pair)
  for (const sel of [':root, :root[data-theme="dark"]', ':root[data-theme="light"]', ':root[data-style="lean"][data-theme="dark"]', ':root[data-style="lean"][data-theme="light"]']) {
    assert.match(ruleBody(sel), /--panel2:/, `${sel} defines --panel2`);
  }
  const pendingRules = css.split("\n").filter((l) => l.includes("tr.pending") || l.includes("sicon-pending")).join("\n");
  assert.doesNotMatch(pendingRules, /animation|transition/, "no animation, so nothing to reduce");
});

// ---- the filtered list's message ----
test("aFilteredListSaysHowManySessionsAreStillBeingChecked", () => {
  const p = mount([full()], { kind: "look", q: "", checking: 3 });
  const status = withClass(p.page.el, "sp-status")[0];
  assert.equal(status.attrs["aria-live"], "polite", "a polite live region");
  const msg = status.children[0];
  assert.equal(msg.textContent, "3 sessions still being checked");
  assert.equal(msg.hidden, false);
  p.page.update({ checking: 1 });
  assert.equal(msg.textContent, "1 session still being checked");
  p.page.update({ checking: 0 });
  assert.equal(msg.hidden, true);
  assert.equal(msg.textContent, "");
  // a search counts as filtering too; an unfiltered list never shows it
  p.page.update({ kind: "all", q: "abc", checking: 2 });
  assert.equal(msg.textContent, "2 sessions still being checked");
  p.page.update({ kind: "all", q: "", checking: 2 });
  assert.equal(msg.hidden, true);
});

test("theStalledNoteOffersRetry", () => {
  let retries = 0;
  const p = mount([pending()], { stalled: true }, { onRetry: () => retries++ });
  const status = withClass(p.page.el, "sp-status")[0];
  const [, note, retry] = status.children;
  assert.equal(note.textContent, "Details are not available yet");
  assert.equal(note.hidden, false);
  assert.equal(retry.hidden, false);
  retry.fire("click");
  assert.equal(retries, 1);
  p.page.update({ stalled: false });
  assert.equal(note.hidden, true);
  assert.equal(retry.hidden, true);
});
