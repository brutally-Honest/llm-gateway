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
  assert.equal(c.sources, undefined, "no Sources column (027)");
  assert.deepEqual([c.status.glyph, c.status.text], ["✗", "failed"], "a session with a failed tool call");
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
  assert.equal(one.changes, "1 file changed · 1 command", "027 C7b: the zero site is left out");
});

// spec 027 C7b item 9: a glyph only for active, failed or hooks-only rows; an ordinary ended
// session has none, but its cell and column stay.
test("statusGlyphOnlyForActiveFailedOrHooksOnly", () => {
  const clean = { tool_states: { ok: 3 }, main_errors: 0 };
  assert.deepEqual([rowCells(session(clean)).status.key, rowCells(session(clean)).status.glyph], ["ended", ""]);
  assert.equal(rowCells(session({ ...clean, main_errors: 1 })).status.key, "failed", "an errored model call is a failure");
  assert.equal(rowCells(session({ ...clean, active: true })).status.key, "active");
  assert.equal(rowCells(session({ ...clean, hooks_only: true })).status.key, "hooks-only");
  const p = livePage([session({ id: "a", ...clean }), session({ id: "b", active: true, ...clean }), session({ id: "c" })]);
  const first = (id) => byTag(p.row(id), "td")[0];
  assert.equal(withClass(first("a"), "sicon").length, 0, "no glyph for an ordinary ended session");
  assert.equal(byTag(p.row("a"), "td").length, 7, "the cell stays");
  assert.equal(withClass(first("b"), "sicon")[0].attrs["aria-label"], "active");
  const bad = withClass(first("c"), "sicon")[0];
  assert.deepEqual([bad.textContent, bad.title, bad.attrs["aria-label"]], ["✗", "failed", "failed"]);
  // a live update that ends the session takes the glyph away again
  p.page.update({ items: [session({ id: "b", ...clean })] });
  assert.equal(withClass(first("b"), "sicon").length, 0);
});

// spec 027 C7b item 10: zero-valued parts of Work and Changes are left out; all zero is "—".
test("zeroPartsOfWorkAndChangesAreLeftOut", () => {
  const empty = session({ tool_states: {}, list: { prompts: 0, main_calls: 0, side_calls: 0, commands: 0, clock_ms: 0 }, effects: { changed: 0, read: 0, sites: 0 }, main_errors: 0 });
  const e = rowCells(empty);
  assert.equal(e.work, "—");
  assert.equal(e.changes, "—");
  const some = rowCells(session({ tool_states: {}, list: { prompts: 1, main_calls: 0, side_calls: 2, commands: 0, clock_ms: 0 }, effects: { changed: 0, read: 0, sites: 3 } }));
  assert.equal(some.work, "1 prompt · 2 side");
  assert.equal(some.changes, "3 sites");
  const p = livePage([empty]);
  assert.equal(p.cell("s1", 3), "—");
  assert.equal(p.cell("s1", 4), "—");
  // failures alone still show, without a leading dot
  const f = livePage([session({ tool_states: { failed: 1 }, list: { prompts: 0, main_calls: 0, side_calls: 0, commands: 0, clock_ms: 0 }, effects: {} })]);
  assert.equal(f.cell("s1", 3), "1 tool call · 1 failed");
});

// spec 027 C7b item 11: a hooks-only row's Tokens cell is a single dash, no cost, no bar.
test("hooksOnlyTokensCellIsASingleDash", () => {
  const p = livePage([session({ hooks_only: true, has_calls: false, usage: {}, models: [] })]);
  const cell = byTag(p.row("s1"), "td")[6];
  const visible = all(cell).filter((e) => e !== cell && e.textContent !== "" && !e.hidden && !all(cell).some((a) => a.hidden && all(a).includes(e)));
  assert.deepEqual(visible.map((e) => e.textContent), ["—"]);
  // a live update to a row with calls shows the counts again
  p.page.update({ items: [session()] });
  assert.equal(p.cell("s1", 6).startsWith("10 in · — out · 7 cache read"), true);
});

// spec 017 E5: a hooks-only row says it has no model calls, and shows no token counts.
test("hooksOnlyRowSaysNoModelCalls", () => {
  const h = rowCells(session({ hooks_only: true, has_calls: false, active: false, usage: {}, models: [] }));
  assert.equal(h.calls, "no model calls captured");
  assert.deepEqual([h.status.glyph, h.status.text], ["◇", "hooks only"], "a finished hooks-only session");
  // D33: Status says whether it is still going; with no Sources column (027) the Work cell
  // says a hooks-only session has no model calls
  const live = rowCells(session({ hooks_only: true, has_calls: false, active: true, usage: {}, models: [] }));
  assert.deepEqual([live.status.glyph, live.status.text], ["●", "active"], "an active hooks-only session");
  assert.equal(live.calls, "no model calls captured");
  assert.deepEqual([h.input, h.output, h.cacheRead], ["—", "—", "—"]);
  assert.equal(h.model, "");
});

// spec 017 AC2, 027 C7b item 13: a row lists what needs a look, in the server's words, with
// no "N need(s) a look:" prefix.
test("rowSaysWhatNeedsALook", () => {
  assert.equal(lookText({ needs_look: 0, look_kinds: [] }), "");
  assert.equal(lookText(undefined), "");
  const kinds = [{ kind: "failed_tool", count: 2, words: "2 failed tool calls" }, { kind: "failed_call", count: 1, words: "1 failed model call" }];
  assert.equal(lookText({ needs_look: 3, look_kinds: kinds }), "2 failed tool calls, 1 failed model call");
  assert.equal(lookText({ needs_look: 1, look_kinds: [kinds[1]] }), "1 failed model call");
  const p = livePage([session({ list: { ...session().list, needs_look: 3, look_kinds: kinds } })]);
  const line = withClass(p.row("s1"), "slook")[0];
  assert.equal(line.textContent, "2 failed tool calls, 1 failed model call");
  // the line is cut with an ellipsis when long, so hovering it shows it whole
  assert.equal(line.title, "2 failed tool calls, 1 failed model call");
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
  assert.equal(textOf(today).replace(/\s+/g, " ").trim(), "2 sessions 5 model calls 7 files changed 1 site 3.7k input — output — API-eq. cost");
  // spec 020 AC6, AC8: the day's API-equivalent cost, partial when calls were left out
  p.page.update({ today: { sessions: 2, calls: 5, changed: 7, sites: 1, input: counter(3707), output: counter(null),
    cost: { usd: "1.5", source: "Test", priced: 4, no_price: 1, no_price_models: ["m9"], no_usage: 0, errored: 0, estimated: 0, at_least: 0, partial: true } } });
  const stat = withClass(withClass(p.page.el, "today")[0], "stat").at(-1);
  assert.equal(textOf(stat).replace(/\s+/g, " ").trim(), "≥ $1.50 API-eq. cost");
  assert.match(stat.title, /1 call left out: no price for m9/);
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
  assert.equal(p.cell("s1", 3), "2 prompts · 2001 calls · 8 tool calls · 1 failed", "the Work cell updates in place");
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
  assert.match(startText(new Date(2026, 10, 13, 20, 47).toISOString(), now), /^\d{1,2}:\d\d/, "today: the time only (027 C7b)");
  assert.doesNotMatch(startText(new Date(2026, 10, 13, 20, 47).toISOString(), now), /Today/);
  assert.match(startText(new Date(2026, 10, 12, 19, 8).toISOString(), now), /^Yesterday \d/);
  assert.match(startText(new Date(2026, 10, 10, 6, 9).toISOString(), now), /^Nov 10, \d/);
  assert.match(startText(new Date(2025, 10, 10, 6, 9).toISOString(), now), /2025/);

  const p = livePage([session({ usage: { input: counter(3707), output: counter(null), cache_write: counter(0), cache_read: counter(15074885) } })]);
  const tds = byTag(p.row("s1"), "td");
  const toks = withClass(tds[6], "tok");
  assert.deepEqual(toks.map((t) => t.textContent), ["3.7k in", "— out", "15.1M cache read"]);
  assert.equal(toks[0].title, "in: 3707", "the exact value is kept");
  assert.equal(toks[1].title, "out: not reported");
  assert.equal(p.link("s1").title, "session key k-123", "the key shows on hover only");
  assert.equal(withClass(tds[2], "splace")[0].textContent, "~/work/proj · m1 +1");
  assert.equal(withClass(tds[2], "splace")[0].title, "/home/u/work/proj");
  // 027: Work and Tools are one cell, every part a segment, failures standing out
  assert.equal(p.cell("s1", 3), "2 prompts · 3 calls · 1 side · 8 tool calls · 1 failed");
  assert.deepEqual(withClass(tds[3], "seg").map((x) => x.textContent), ["2 prompts", "3 calls", "1 side", "8 tool calls", "1 failed"], "it wraps only at the dots (D49)");
  assert.equal(withClass(tds[3], "bad")[0].textContent, "1 failed", "failures stand out");
  assert.equal(p.cell("s1", 4), "2 files changed · 4 commands · 1 site");
  assert.deepEqual(withClass(tds[4], "seg").map((x) => x.textContent), ["2 files changed", "4 commands", "1 site"], "it wraps only at the dots (D49)");
  assert.equal(p.cell("s1", 5), "2m 05s");
  // 027: the status is a glyph alone, named by its word for hover and screen readers
  const icon = withClass(tds[0], "sicon")[0];
  assert.equal(icon.textContent, "✗", "this session has a failed tool call");
  assert.equal(icon.title, "failed");
  assert.equal(icon.attrs["aria-label"], "failed");
  assert.equal(icon.attrs.role, "img");
  const ths = byTag(p.page.el, "th").map((x) => x.textContent);
  assert.deepEqual(ths.slice(0, 7), ["", "Start", "Session", "Work", "Changes", "Time", "Tokens"], "no Sources, Status or Tools header");
  assert.equal(byTag(p.page.el, "th")[0].attrs["aria-label"], byTag(p.page.el, "th")[0].title, "the icon column is named");
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
  assert.deepEqual(byTag(sec, "th").map((x) => x.textContent), ["Label", "Client", "Host", "Bytes", "Time"], "both vary, so both columns show");
  assert.ok(!byTag(sec, "a").some((a) => a.href === "#/effects/unassigned"), "no link when every one is listed");
  p.page.update({ unassigned: 40 });
  assert.ok(byTag(withClass(p.page.el, "sp-unassigned")[0], "a").some((a) => a.href === "#/effects/unassigned" && /All 40/.test(a.textContent)));
});

// spec 027 C7b item 15: Label shows only when some label is not "unassigned", Client only
// when some client is neither unknown nor "—".
test("unassignedColumnsHiddenWhenConstant", () => {
  const plain = { id: 7, kind: "net_conn", client: "unknown", host: "example.com", port: 443, bytes_up: 1, bytes_down: 2, at: "2026-01-01T00:00:07Z" };
  const heads = (items) => {
    const p = livePage([session()]);
    p.page.update({ unassigned: items.length, unassignedItems: items });
    const sec = withClass(p.page.el, "sp-unassigned")[0];
    const tr = byTag(byTag(sec, "tbody")[0], "tr")[0];
    return [byTag(sec, "th").map((x) => x.textContent), byTag(tr, "td").length];
  };
  assert.deepEqual(heads([plain, { ...plain, id: 8, client: undefined }]), [["Host", "Bytes", "Time"], 3]);
  assert.deepEqual(heads([plain, { ...plain, id: 8, attribution: { label: "harness", client: "" } }]), [["Label", "Host", "Bytes", "Time"], 4]);
  assert.deepEqual(heads([plain, { ...plain, id: 8, client: "client-a" }]), [["Client", "Host", "Bytes", "Time"], 4]);
});

test("noUnassignedSectionWhenNone", () => {
  const p = livePage([session()]);
  p.page.update({ unassigned: 0, unassignedItems: [] });
  assert.equal(withClass(p.page.el, "sp-unassigned").length, 0);
});

// spec 020 AC6, AC8, research D13: a row's API-equivalent cost, a labelled second line of
// the Tokens cell (a column of its own overflowed the 1,600 px list), partial when calls
// were left out.
test("rowShowsApiEquivalentCost", () => {
  const cost = { usd: "12.345", source: "Test", priced: 9, no_price: 0, no_price_models: [], no_usage: 1, errored: 0, estimated: 0, at_least: 0, partial: true };
  const c = rowCells(session({ usage: { ...session().usage, cost } }));
  assert.equal(c.cost.text, "≥ $12.35");
  assert.match(c.cost.title, /1 call left out: no usage counts/);
  assert.equal(rowCells(session({ hooks_only: true, has_calls: false })).cost.text, "—");
  const p = livePage([session({ usage: { ...session().usage, cost: { ...cost, no_usage: 0, partial: false } } })]);
  const th = byTag(p.page.el, "th").find((x) => x.textContent === "Tokens");
  assert.match(th.title, /API-equivalent: .* not what was charged/, "the Tokens header explains the cost line");
  const line = withClass(p.page.el, "cost")[0];
  assert.equal(textOf(line).trim(), "$12.35", "the cost is the Tokens cell's second line; the header says API-equivalent (027 C7b)");
  // 020 D17: the counts are unbreakable parts with " · " as text between them, so the cell
  // wraps only at the dots, as Work and Changes do; the cost follows, a line of its own, and
  // under it the token bar (027 C6a), which holds no text
  const cell = byTag(p.page.el, "td").find((x) => x.className === "tokens");
  assert.deepEqual(cell.children.map((c) => [c.className || "", c.textContent]),
    [["tok seg", "10 in"], ["", " · "], ["tok seg", "— out"], ["", " · "], ["tok seg", "7 cache read"], ["cost", "$12.35"], ["tokbar", ""]]);
  assert.match(line.title, /not what was charged/);
});

// spec 027 C6a: the day's totals are stat tiles, each a number with its label, in the same
// order as the line they replace; "today" is said once in the heading (C7b), not above them.
test("todayTotalsAreStatTiles", () => {
  const p = livePage([session()]);
  p.page.update({ today: { sessions: 2, calls: 5, changed: 7, sites: 1, input: counter(3707), output: counter(null) } });
  const today = withClass(p.page.el, "today")[0];
  const tiles = withClass(today, "stat");
  assert.deepEqual(tiles.map((t) => textOf(t).replace(/\s+/g, " ").trim()), ["2 sessions", "5 model calls", "7 files changed", "1 site", "3.7k input", "— output", "— API-eq. cost"]);
  for (const t of tiles) assert.equal(byTag(t, "b").length, 1, "each tile has one big number");
  assert.equal(withClass(today, "today-label").length, 0, "no separate Today row above the tiles");
  assert.ok(!/today/i.test(textOf(today)), "the tiles' own text does not say it");
});

// spec 027 C7b item 7: the heading says "today" once, muted, with the since-midnight hover.
test("headingSaysTodayOnceWithItsHover", () => {
  const p = livePage([session()]);
  const h1 = byTag(p.page.el, "h1")[0];
  assert.equal(textOf(h1).replace(/\s+/g, " ").trim(), "Sessions today");
  const when = withClass(h1, "sp-when")[0];
  assert.equal(when.textContent, "today");
  assert.equal(when.title, "since local midnight");
});

// spec 027 C6a: under a row's token figures a thin bar, one segment per count, each sized by
// handing the count to flex-grow; a missing or zero count gets no segment.
test("tokenBarSegmentsGrowByTheirCounts", () => {
  const p = livePage([session({ usage: { input: counter(3000), output: counter(0), cache_write: counter(0), cache_read: counter(7000) } })]);
  const bar = withClass(byTag(p.row("s1"), "td")[6], "tokbar")[0];
  assert.equal(bar.attrs["aria-hidden"], "true", "the numbers are in text already");
  const segs = withClass(bar, "tokseg");
  assert.equal(segs.length, 3);
  assert.deepEqual(segs.map((s) => s.style.flexGrow), ["3000", "", "7000"]);
  assert.deepEqual(segs.map((s) => Boolean(s.hidden)), [false, true, false], "a zero count has no segment");
  assert.ok(!bar.hidden);
  // a live update with other counts re-sizes the same segments
  p.page.update({ items: [session({ usage: { input: counter(5), output: counter(10), cache_write: counter(0), cache_read: counter(null) } })] });
  const again = withClass(byTag(p.row("s1"), "td")[6], "tokseg");
  assert.deepEqual(again.map((s) => s.style.flexGrow), ["5", "10", ""]);
  // nothing reported: no bar at all
  p.page.update({ items: [session({ usage: {} })] });
  assert.ok(withClass(byTag(p.row("s1"), "td")[6], "tokbar")[0].hidden);
});

// spec 027 C6b: the sessions and model-calls tiles carry a sparkline of the server's hourly
// counts: one bar per bucket with --v from the count, --peak on the container, hidden from
// assistive tech (the numbers are in text). No other tile gets one.
const hourlyToday = (h) => ({ sessions: 2, calls: 5, changed: 7, sites: 1, input: counter(3), output: counter(1), hourly: h });
const tileFor = (today, word) => withClass(today, "stat").find((t) => textOf(t).includes(word));

test("sparklinesShowOneBarPerBucketFromServerNumbers", () => {
  const p = livePage([session()]);
  p.page.update({ today: hourlyToday({ start: "2026-01-01T00:00:00Z", calls: [3, 0, 12], sessions: [1, 0, 2], peak_calls: 12, peak_sessions: 2 }) });
  const today = withClass(p.page.el, "today")[0];
  const calls = withClass(tileFor(today, "model calls"), "spark")[0];
  assert.equal(calls.attrs["aria-hidden"], "true");
  assert.equal(calls.styleProps["--peak"], "12");
  assert.deepEqual(byTag(calls, "i").map((b) => b.styleProps["--v"]), ["3", "0", "12"]);
  const sess = withClass(tileFor(today, "sessions"), "spark")[0];
  assert.equal(sess.styleProps["--peak"], "2");
  assert.deepEqual(byTag(sess, "i").map((b) => b.styleProps["--v"]), ["1", "0", "2"]);
  assert.equal(withClass(today, "spark").length, 2, "only the sessions and model-calls tiles");
  assert.equal(textOf(today).replace(/\s+/g, " ").trim(), "2 sessions 5 model calls 7 files changed 1 site 3 input 1 output — API-eq. cost");
});

test("sparklineIsAbsentWhenPeakIsZeroOrHourlyMissing", () => {
  for (const h of [undefined, { calls: [0, 0], sessions: [0, 0], peak_calls: 0, peak_sessions: 0 }, { calls: [], sessions: [], peak_calls: 0, peak_sessions: 0 }]) {
    const p = livePage([session()]);
    p.page.update({ today: hourlyToday(h) });
    assert.equal(withClass(withClass(p.page.el, "today")[0], "spark").length, 0);
  }
  const p = livePage([session()]);
  p.page.update({ today: hourlyToday({ calls: [0, 4], sessions: [0, 0], peak_calls: 4, peak_sessions: 0 }) });
  const today = withClass(p.page.el, "today")[0];
  assert.equal(withClass(tileFor(today, "model calls"), "spark").length, 1);
  assert.equal(withClass(tileFor(today, "sessions"), "spark").length, 0, "peak 0 hides just that one");
});

// spec 027 C7b: a row whose cost is unknown shows no cost line (a bare "—" said nothing)
test("unknownCostLineIsLeftOut", () => {
  const p = livePage([session({ usage: { ...session().usage, cost: null } })]);
  const cost = withClass(byTag(p.row("s1"), "td")[6], "cost")[0];
  assert.equal(cost.hidden, true);
});
