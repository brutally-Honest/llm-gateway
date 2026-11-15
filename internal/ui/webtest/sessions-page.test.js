import test from "node:test";
import assert from "node:assert/strict";
import { compact, createSessionsPage, localMidnight, lookText, moveSelection, rowCells, startText, todayCells, ungroupedText, unassignedRow } from "../web/sessions-page.js";
import { StubElement, all, byTag, installDom, textOf, withClass } from "./dom-stub.js";
import { upsertSession } from "../web/entries.js";
import { filterSessions } from "../web/filters.js";
import { handleKey, keyBar } from "../web/keys.js";

const counter = (v) => ({ value: v, unreported: 0 });
const session = (o = {}) => ({
  id: "s1", session_key: "k-123", first_at: "2026-01-01T00:00:00Z", last_at: "2026-01-01T00:05:00Z", title: "Fix it",
  has_calls: true, has_hooks: true, model_calls: 3, errors: 5, main_errors: 2, active: false, hooks_only: false, models: ["m1", "m2"],
  tool_states: { ok: 4, failed: 1, "not-run": 0, unmatched: 1, pending: 2, "no-hooks": 0 },
  usage: { input: counter(10), output: counter(null), cache_write: counter(0), cache_read: counter(7) },
  effects: { changed: 2, read: 0, sites: 1 },
  list: { prompts: 2, main_calls: 3, side_calls: 1, commands: 4, clock_ms: 125000, folder: "~/work/proj", folder_full: "/home/u/work/proj", needs_look: 0, look_kinds: [] },
  ...o,
});

// spec 017 AC1: every count carries its label in words; tokens no call reported are "—".
test("rowShowsLabelledCounts", () => {
  const c = rowCells(session());
  assert.equal(c.title, "Fix it");
  assert.equal(c.key, "k-123", "the key is kept for the hover, never shown as the title");
  assert.equal(c.folder, "~/work/proj");
  assert.equal(c.folderFull, "/home/u/work/proj");
  assert.equal(c.model, "m1 +1");
  assert.equal(c.sources, "⇄ API + ⚑ hooks");
  assert.deepEqual([c.status.glyph, c.status.text], ["■", "ended"]);
  assert.equal(c.prompts, "2 prompts");
  assert.equal(c.calls, "3 calls · 1 side");
  assert.equal(c.tools, "8 tool calls");
  assert.equal(c.failed, 1);
  assert.equal(c.changes, "2 files changed · 4 commands · 1 site");
  assert.equal(c.time, "2m 05s");
  assert.deepEqual([c.input, c.output, c.cacheRead], ["10", "—", "7"]);
  assert.equal(rowCells(session({ active: true })).status.text, "active");
  const none = rowCells(session({ usage: {}, title: "" }));
  assert.equal(none.input, "—");
  assert.equal(none.title, "no prompt captured");
  const one = rowCells(session({ list: { prompts: 1, main_calls: 1, side_calls: 0, commands: 1, clock_ms: 900 }, effects: { changed: 1, read: 0, sites: 0 } }));
  assert.equal(one.prompts, "1 prompt");
  assert.equal(one.calls, "1 call", "no side calls, no side count");
  assert.equal(one.changes, "1 file changed · 1 command · 0 sites");
});

// spec 017 E5: a hooks-only row says it has no model calls, and shows no token counts.
test("hooksOnlyRowSaysNoModelCalls", () => {
  const h = rowCells(session({ hooks_only: true, has_calls: false, active: false, usage: {}, models: [] }));
  assert.equal(h.calls, "no model calls captured");
  assert.deepEqual([h.status.glyph, h.status.text], ["◇", "hooks only"], "a finished hooks-only session");
  assert.equal(h.sources, "⚑ hooks");
  // D33: Status says whether it is still going; Sources already says hooks only
  const live = rowCells(session({ hooks_only: true, has_calls: false, active: true, usage: {}, models: [] }));
  assert.deepEqual([live.status.glyph, live.status.text], ["●", "active"], "an active hooks-only session");
  assert.equal(live.sources, "⚑ hooks");
  assert.deepEqual([h.input, h.output, h.cacheRead], ["—", "—", "—"]);
  assert.equal(h.model, "");
});

// spec 017 AC2: a row says how many items need a look and of what kinds, in the server's words.
test("rowSaysWhatNeedsALook", () => {
  assert.equal(lookText({ needs_look: 0, look_kinds: [] }), "");
  assert.equal(lookText(undefined), "");
  const kinds = [{ kind: "failed_tool", count: 2, words: "2 failed tool calls" }, { kind: "failed_call", count: 1, words: "1 failed model call" }];
  assert.equal(lookText({ needs_look: 3, look_kinds: kinds }), "3 need a look: 2 failed tool calls, 1 failed model call");
  assert.equal(lookText({ needs_look: 1, look_kinds: [kinds[1]] }), "1 needs a look: 1 failed model call");
  const p = livePage([session({ list: { ...session().list, needs_look: 3, look_kinds: kinds } })]);
  const line = withClass(p.row("s1"), "slook")[0];
  assert.equal(line.textContent, "3 need a look: 2 failed tool calls, 1 failed model call");
  // the line is cut with an ellipsis when long, so hovering it shows it whole
  assert.equal(line.title, "3 need a look: 2 failed tool calls, 1 failed model call");
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

// spec 017 AC3, E1: the Today line shows the server's totals, 0 for each when nothing ran.
test("todayLineZerosOnEmptyStore", () => {
  const t = todayCells(null);
  assert.deepEqual([t.sessions, t.calls, t.changed, t.sites, t.input, t.output], [0, 0, 0, 0, "—", "—"]);
  const kb = keyBar({ name: "sessions" }, null);
  assert.equal(kb.level, "SESSIONS");
  assert.equal(kb.position, "");
  assert.ok(kb.shortcuts.length > 0);
  assert.match(localMidnight(new Date(2026, 5, 15, 13, 30)), /^\d{4}-\d\d-\d\dT/);
  assert.equal(new Date(localMidnight(new Date(2026, 5, 15, 13, 30))).getTime(), new Date(2026, 5, 15).getTime());
});

test("todayLineShowsServerTotals", () => {
  const full = todayCells({ sessions: 2, calls: 5, changed: 7, sites: 3, input: counter(9), output: counter(0), cache_read: counter(1) });
  assert.deepEqual([full.sessions, full.calls, full.changed, full.sites, full.input, full.output], [2, 5, 7, 3, "9", "0"]);
  const p = livePage([session()]);
  p.page.update({ today: { sessions: 2, calls: 5, changed: 7, sites: 1, input: counter(3707), output: counter(null) } });
  const today = withClass(p.page.el, "today")[0];
  assert.equal(textOf(today).replace(/\s+/g, " ").trim(), "Today 2 sessions 5 model calls 7 files changed 1 site 3.7k input — output");
});

// spec 017 AC4: the filter shows the matching loaded rows; the kinds are buttons marked pressed.
test("filterShowsMatchingLoadedRows", () => {
  const rows = [session({ id: "a", title: "alpha", list: { ...session().list, needs_look: 1 } }), session({ id: "b", title: "beta", active: true })];
  const p = livePage(rows);
  const shown = () => byTag(p.page.el, "tr").filter((r) => r.dataset && r.dataset.id).map((r) => r.dataset.id);
  const kind = (label) => byTag(p.page.el, "button").find((b) => b.textContent === label);
  assert.deepEqual(byTag(withClass(p.page.el, "seg-group")[0], "button").map((b) => b.textContent), ["All", "Needs a look", "Active", "Hooks only"]);
  kind("Needs a look").fire("click");
  assert.deepEqual(shown(), ["a"]);
  assert.equal(kind("Needs a look").attrs["aria-pressed"], "true");
  assert.equal(kind("All").attrs["aria-pressed"], "false");
  kind("Active").fire("click");
  assert.deepEqual(shown(), ["b"]);
  kind("All").fire("click");
  assert.deepEqual(shown(), ["a", "b"]);
});

test("filterSaysFilteringNLoaded", () => {
  const rows = [session({ id: "a", title: "alpha" }), session({ id: "b", title: "beta" })];
  assert.equal(filterSessions(rows, { text: "alp", hasMore: true }).note, "filtering 2 loaded");
  const p = livePage(rows);
  p.page.update({ next_after: "tok" });
  const input = byTag(p.page.el, "input")[0];
  input.value = "alp";
  input.fire("input");
  assert.match(textOf(p.page.el), /filtering 2 loaded/);
});

// spec 017 E6: nothing matches → "no sessions match" once, in the list (D34), and Load
// more stays while more exist.
test("filterMatchingNothingKeepsLoadMore", () => {
  const none = filterSessions([session()], { text: "zzz", hasMore: true });
  assert.equal(none.note, "filtering 1 loaded");
  assert.equal(none.loadMore, true);
  const p = livePage([session()]);
  p.page.update({ next_after: "tok" });
  const input = byTag(p.page.el, "input")[0];
  input.value = "zzz";
  input.fire("input");
  assert.equal(textOf(p.page.el).split("no sessions match").length - 1, 1, "said once, not in the note too");
  assert.match(textOf(p.page.el), /filtering 1 loaded/);
  assert.ok(byTag(p.page.el, "button").some((b) => b.textContent === "Load more"), "Load more is kept");
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
  // a cell's text with its parts' spaces collapsed (Work and Changes are segment spans, D49)
  const cell = (id, i) => textOf(byTag(row(id), "td")[i]).replace(/\s+/g, " ").trim();
  return { page, link, row, cell };
}

test("focusedRowSurvivesALiveUpdate", () => {
  const older = session({ id: "s2", title: "older", last_at: "2026-01-01T00:01:00Z" });
  const p = livePage([session({ list: { ...session().list, main_calls: 2000, side_calls: 0 } }), older]);
  const a = p.link("s1");
  const tr = p.row("s1");
  a.focus();
  p.page.update({ items: [session({ title: "Fix it again", list: { ...session().list, main_calls: 2001, side_calls: 0 } }), older] });
  assert.equal(p.link("s1"), a, "the same link element is kept");
  assert.equal(p.row("s1"), tr, "the same row element is kept");
  assert.equal(document.activeElement, a, "focus stays on the link");
  assert.equal(a.textContent, "Fix it again", "the title updates in place");
  assert.equal(p.cell("s1", 4), "2 prompts · 2001 calls", "the Work cell updates in place");
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

// Esc in the filter box restores the whole list (F4). Chrome clears a search box on Esc
// and fires `search`, not `input`; other browsers do not clear it at all.
function filteredPage() {
  const p = livePage([session(), session({ id: "s2", title: "older", last_at: "2026-01-01T00:01:00Z" })]);
  const input = byTag(p.page.el, "input")[0];
  const rows = () => byTag(p.page.el, "tr").filter((r) => r.dataset && r.dataset.id).length;
  const listed = () => byTag(p.page.el, "tbody").length > 0 && all(p.page.el).some((e) => e.tagName === "table") && !all(p.page.el).some((e) => e.textContent === "no sessions match");
  input.value = "zz";
  input.fire("input");
  assert.ok(!listed(), "the filter hides every row");
  return { input, rows, listed };
}

test("escInSessionFilterRestoresTheList", () => {
  const f = filteredPage();
  f.input.fire("keydown", { key: "Escape" });
  assert.equal(f.input.value, "", "Esc clears the box");
  assert.ok(f.listed(), "the rows show again");
  assert.equal(f.rows(), 2);
});

test("chromeSearchEventRestoresTheSessionList", () => {
  const f = filteredPage();
  f.input.value = ""; // Chrome clears the box itself, then fires search
  f.input.fire("search");
  assert.ok(f.listed(), "the rows show again");
});

// The sessions list formats for reading (visual cleanup, 2026-11-13; spec 017 AC1): compact
// token counters labelled in words with the exact value on hover, starts as Today /
// Yesterday / a date, the key only on the title's hover, failures standing out.
test("listFormatsForReading", () => {
  assert.deepEqual(["—", "0", "999", "3707", "94487", "15074885", "2100000000"].map(compact), ["—", "0", "999", "3.7k", "94k", "15.1M", "2.1B"]);
  const now = new Date(2026, 10, 13, 22, 0);
  assert.match(startText(new Date(2026, 10, 13, 20, 47).toISOString(), now), /^Today \d/);
  assert.match(startText(new Date(2026, 10, 12, 19, 8).toISOString(), now), /^Yesterday \d/);
  assert.match(startText(new Date(2026, 10, 10, 6, 9).toISOString(), now), /^Nov 10, \d/);
  assert.match(startText(new Date(2025, 10, 10, 6, 9).toISOString(), now), /2025/);

  const p = livePage([session({ usage: { input: counter(3707), output: counter(null), cache_write: counter(0), cache_read: counter(15074885) } })]);
  const tds = byTag(p.row("s1"), "td");
  const toks = withClass(tds[8], "tok");
  assert.deepEqual(toks.map((t) => t.textContent), ["3.7k in", "— out", "15.1M cache read"]);
  assert.equal(toks[0].title, "in: 3707", "the exact value is kept");
  assert.equal(toks[1].title, "out: not reported");
  assert.equal(p.link("s1").title, "session key k-123", "the key shows on hover only");
  assert.equal(withClass(tds[1], "splace")[0].textContent, "~/work/proj · m1 +1");
  assert.equal(withClass(tds[1], "splace")[0].title, "/home/u/work/proj");
  assert.equal(byTag(tds[5], "span").map((x) => x.textContent).join(""), "8 tool calls · 1 failed");
  assert.equal(withClass(tds[5], "bad")[0].textContent, " · 1 failed", "failures stand out");
  assert.equal(p.cell("s1", 6), "2 files changed · 4 commands · 1 site");
  assert.deepEqual(withClass(tds[6], "seg").map((x) => x.textContent), ["2 files changed", "4 commands", "1 site"], "it wraps only at the dots (D49)");
  assert.equal(p.cell("s1", 7), "2m 05s");
});

test("listShowsALoaderUntilItsFirstPage", () => {
  installDom();
  const page = createSessionsPage({ onMore() {} });
  page.update({ loaded: false });
  const text = () => all(page.el).map((e) => e.textContent).join(" ");
  assert.match(text(), /Loading sessions…/);
  assert.doesNotMatch(text(), /No sessions captured yet/, "an empty list is not claimed before it loads");
  page.update({ loaded: true, items: [] });
  assert.match(text(), /No sessions captured yet/);
  assert.doesNotMatch(text(), /Loading sessions/);
});

// spec 017 E1: an empty store shows the list's empty note, zeros on the Today line and no
// unassigned section.
test("emptyStoreShowsEmptyNote", () => {
  installDom();
  const page = createSessionsPage({ onMore() {} });
  page.update({ loaded: true, items: [], today: { sessions: 0, calls: 0, changed: 0, sites: 0, input: counter(null), output: counter(null) }, unassigned: 0 });
  assert.match(textOf(page.el), /No sessions captured yet\./);
  assert.match(textOf(withClass(page.el, "today")[0]).replace(/\s+/g, " "), /0 sessions 0 model calls 0 files changed 0 sites/);
  assert.equal(withClass(page.el, "sp-unassigned").length, 0);
});

// spec 017 AC2: the unassigned connections get their own section, each with its label,
// client, host, bytes and time; a harness label names the client it belongs to.
test("unassignedSectionListsConnections", () => {
  const plain = { id: 7, kind: "net_conn", client: "unknown", host: "example.com", port: 8443, bytes_up: 120, bytes_down: 4096, at: "2026-01-01T00:00:07Z" };
  const harness = { ...plain, id: 8, host: "harness.example", port: 443, attribution: { label: "harness", client: "client-a" } };
  const r = unassignedRow(plain);
  assert.deepEqual([r.label, r.client, r.host, r.bytes], ["unassigned", "unknown", "example.com:8443", "↑ 120 B · ↓ 4.0 KB"]);
  const h = unassignedRow(harness);
  assert.deepEqual([h.label, h.client, h.host], ["harness", "client-a", "harness.example"]);

  const p = livePage([session()]);
  p.page.update({ unassigned: 2, unassignedItems: [plain, harness] });
  const sec = withClass(p.page.el, "sp-unassigned")[0];
  assert.ok(sec, "the section is shown");
  assert.match(textOf(sec), /Unassigned connections \(2\)/);
  const rows = byTag(byTag(sec, "tbody")[0], "tr").map((tr) => byTag(tr, "td").slice(0, 4).map((td) => td.textContent));
  assert.deepEqual(rows, [["unassigned", "unknown", "example.com:8443", "↑ 120 B · ↓ 4.0 KB"], ["harness", "client-a", "harness.example", "↑ 120 B · ↓ 4.0 KB"]]);
  assert.ok(!byTag(sec, "a").some((a) => a.href === "#/effects/unassigned"), "no link when every one is listed");
  p.page.update({ unassigned: 40 });
  assert.ok(byTag(withClass(p.page.el, "sp-unassigned")[0], "a").some((a) => a.href === "#/effects/unassigned" && /All 40/.test(a.textContent)));
});

test("noUnassignedSectionWhenNone", () => {
  const p = livePage([session()]);
  p.page.update({ unassigned: 0, unassignedItems: [] });
  assert.equal(withClass(p.page.el, "sp-unassigned").length, 0);
});
