import test from "node:test";
import assert from "node:assert/strict";
import { countedChips, createSessionsPage, localMidnight, moveSelection, rowCells, todayCells, ungroupedText } from "../web/sessions-page.js";
import { StubElement, all, byTag, installDom } from "./dom-stub.js";
import { upsertSession } from "../web/entries.js";
import { filterSessions } from "../web/filters.js";
import { handleKey, keyBar } from "../web/keys.js";

const counter = (v) => ({ value: v, unreported: 0 });
const session = (o = {}) => ({
  id: "s1", first_at: "2026-01-01T00:00:00Z", last_at: "2026-01-01T00:05:00Z", title: "Fix it",
  has_calls: true, has_hooks: true, model_calls: 3, errors: 5, main_errors: 2, active: false, hooks_only: false,
  tool_states: { ok: 4, failed: 0, "not-run": 0, unmatched: 1, pending: 3, "no-hooks": 0 },
  usage: { input: counter(10), output: counter(null), cache_write: counter(0), cache_read: counter(7) },
  ...o,
});

test("rowCellsShowDashForNoUsage", () => {
  const c = rowCells(session());
  assert.equal(c.input, "10");
  assert.equal(c.output, "—");
  assert.equal(c.cacheWrite, "0");
  assert.equal(c.cacheRead, "7");
  assert.equal(c.sources, "API + hooks");
  assert.equal(c.calls, 3);
  assert.deepEqual(c.chips.map((x) => [x.key, x.count]), [["ok", 4], ["unmatched", 1], ["pending", 3]]);
  assert.equal(c.status.text, "ended");
  const none = rowCells(session({ usage: {}, title: "" }));
  assert.equal(none.input, "—");
  assert.equal(none.title, "no prompt captured");
  assert.equal(rowCells(session({ active: true })).status.text, "active");
  const h = rowCells(session({ hooks_only: true, has_calls: false }));
  assert.equal(h.status.text, "hooks only");
  assert.equal(h.sources, "hooks");
  assert.deepEqual(countedChips({}), []);
  assert.deepEqual(countedChips({ ok: 1, failed: 2 }).map((x) => [x.key, x.glyph, x.count]), [["ok", "✓", 1], ["failed", "✗", 2]]);
});

test("errorsColumnReadsMainErrors", () => {
  assert.equal(rowCells(session()).errors, 2);
  assert.equal(rowCells(session({ main_errors: undefined })).errors, 0);
});

test("titleUpdatesWhenSessionEventArrives", () => {
  let rows = upsertSession([], session({ title: "" }));
  assert.equal(rowCells(rows[0]).title, "no prompt captured");
  rows = upsertSession(rows, session({ title: "Fix the parser" }));
  assert.equal(rows.length, 1);
  assert.equal(rowCells(rows[0]).title, "Fix the parser");
});

test("ungroupedLinkShownWhenExchangesExist", () => {
  assert.equal(ungroupedText([], null), "");
  assert.equal(ungroupedText([{}], null), "1 ungrouped exchange");
  assert.equal(ungroupedText([{}, {}, {}], null), "3 ungrouped exchanges");
  assert.equal(ungroupedText([{}, {}], "tok"), "2+ ungrouped exchanges");
});

test("emptyStoreShowsNoteAndZeroTotals", () => {
  const t = todayCells(null);
  assert.deepEqual([t.sessions, t.calls, t.input, t.output, t.cacheWrite, t.cacheRead], [0, 0, "—", "—", "—", "—"]);
  const full = todayCells({ sessions: 2, calls: 5, input: counter(9), output: counter(0), cache_write: counter(null), cache_read: counter(1) });
  assert.deepEqual([full.input, full.output, full.cacheWrite, full.cacheRead], ["9", "0", "—", "1"]);
  const kb = keyBar({ name: "sessions" }, null);
  assert.equal(kb.level, "SESSIONS");
  assert.equal(kb.position, "");
  assert.ok(kb.shortcuts.length > 0);
  assert.match(localMidnight(new Date(2026, 5, 15, 13, 30)), /^\d{4}-\d\d-\d\dT/);
  assert.equal(new Date(localMidnight(new Date(2026, 5, 15, 13, 30))).getTime(), new Date(2026, 5, 15).getTime());
});

test("filterAndLoadMoreCopy", () => {
  const rows = [session({ id: "a", title: "alpha" }), session({ id: "b", title: "beta", main_errors: 0 })];
  assert.equal(filterSessions(rows, { text: "alp", hasMore: true }).note, "filtering 2 loaded");
  assert.equal(filterSessions(rows, { kind: "errors", hasMore: true }).rows.length, 1);
  const none = filterSessions(rows, { text: "zzz", hasMore: true });
  assert.equal(none.note, "no sessions match");
  assert.equal(none.loadMore, true);
  assert.equal(filterSessions(rows, {}).note, "");
});

test("arrowsSelectEnterOpens", () => {
  const route = { name: "sessions" };
  const press = (key) => handleKey(route, { key });
  assert.deepEqual(press("ArrowDown"), { type: "select", delta: 1 });
  assert.deepEqual(press("ArrowUp"), { type: "select", delta: -1 });
  assert.deepEqual(press("Enter"), { type: "open" });
  assert.equal(moveSelection(-1, 1, 3), 0);
  assert.equal(moveSelection(0, -1, 3), 0);
  assert.equal(moveSelection(1, 1, 3), 2);
  assert.equal(moveSelection(2, 1, 3), 2);
  assert.equal(moveSelection(0, 1, 0), -1);
  assert.equal(keyBar(route, { index: 2, total: 3 }).position, "session 2/3");
});

// A live update must not rebuild the rows: a focused session link would leave the page and
// focus would drop to <body> (013 manual check step 1, F1). Rows are kept by session id.
function livePage(items) {
  installDom();
  Object.assign(document, {
    createDocumentFragment: () => new StubElement("fragment"),
    createTextNode: (text) => Object.assign(new StubElement("text"), { textContent: text }),
  });
  const page = createSessionsPage({ onMore() {} });
  page.update({ items });
  const link = (id) => byTag(page.el, "a").find((a) => a.href === `#/sessions/${id}`);
  const row = (id) => byTag(page.el, "tr").find((r) => r.dataset && r.dataset.id === id);
  const cell = (id, i) => byTag(row(id), "td")[i].textContent;
  return { page, link, row, cell };
}

test("focusedRowSurvivesALiveUpdate", () => {
  const older = session({ id: "s2", title: "older", last_at: "2026-01-01T00:01:00Z" });
  const p = livePage([session({ model_calls: 2000 }), older]);
  const a = p.link("s1");
  const tr = p.row("s1");
  a.focus();
  p.page.update({ items: [session({ model_calls: 2001, title: "Fix it again" }), older] });
  assert.equal(p.link("s1"), a, "the same link element is kept");
  assert.equal(p.row("s1"), tr, "the same row element is kept");
  assert.equal(document.activeElement, a, "focus stays on the link");
  assert.equal(a.textContent, "Fix it again", "the title updates in place");
  assert.equal(p.cell("s1", 3), "2001", "the Calls cell updates in place");
});

test("focusedRowKeepsFocusWhenTheOrderChanges", () => {
  const p = livePage([session(), session({ id: "s2", title: "older", last_at: "2026-01-01T00:01:00Z" })]);
  const body = byTag(p.page.el, "tbody")[0];
  // a browser blurs an element taken out of the page, as replaceChildren does to every row
  const replace = body.replaceChildren.bind(body);
  body.replaceChildren = (...nodes) => {
    if (body.children.some((r) => all(r).includes(document.activeElement))) document.activeElement = null;
    replace(...nodes);
  };
  const a = p.link("s2");
  a.focus();
  p.page.update({ items: [session({ id: "s2", title: "older", last_at: "2026-01-01T00:09:00Z" }), session()] });
  assert.deepEqual(body.children.map((r) => r.dataset.id), ["s2", "s1"], "the rows are reordered");
  assert.equal(p.link("s2"), a);
  assert.equal(document.activeElement, a, "focus returns to the moved link");
});
