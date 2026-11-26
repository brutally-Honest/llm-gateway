import { timeElement } from "./entries.js";
import { FILTERS } from "./filters.js";
import { bytes, dur, plural } from "./fmt.js";
import { LABEL, explain, totalCost } from "./cost.js";

export const NO_TITLE = "no prompt captured";
// PENDING_TITLE is the title link's text while a row's facts are built: the placeholder bar
// is drawn over it, so it is what a screen reader reads (spec 043 AC6).
export const PENDING_TITLE = "Details loading";
const DASH = "—";

// ---- shared small builders (also used by the ungrouped and exchange views) ----

export function loadMore(data, onMore) {
  if (!data.next_after) return null;
  const b = document.createElement("button");
  b.textContent = "Load more";
  b.disabled = Boolean(data.loadingMore);
  b.addEventListener("click", () => onMore());
  return b;
}

// tableWithHead builds a table whose header cells carry the same class as their
// column's values, so right-aligned numbers sit under right-aligned headers.
export function tableWithHead(columns) {
  const table = document.createElement("table");
  const thead = document.createElement("thead");
  const head = document.createElement("tr");
  for (const [label, cls, title] of columns) {
    const th = document.createElement("th");
    th.scope = "col";
    if (cls) th.className = cls;
    if (title) th.title = title;
    th.textContent = label;
    // a column with no visible header (the status icons) is still named for screen readers
    if (!label && title) th.setAttribute("aria-label", title);
    head.append(th);
  }
  thead.append(head);
  const body = document.createElement("tbody");
  table.append(thead, body);
  return { table, body };
}

export function note(cls, text) {
  const p = document.createElement("p");
  p.className = cls;
  p.textContent = text;
  return p;
}

// pageHead is a page's top, as the sessions list has it (016's theme): its title and its
// labelled counts beside it. stats is a list of [count, word] pairs; null counts are left out.
export function pageHead(title, stats = []) {
  const head = document.createElement("div");
  head.className = "sp-head";
  const h = document.createElement("h1");
  h.textContent = title;
  const line = document.createElement("p");
  line.className = "stats";
  for (const [count, word] of stats) {
    if (count === null || count === undefined) continue;
    const st = document.createElement("span");
    st.className = "stat";
    const b = document.createElement("b");
    b.textContent = String(count);
    st.append(b, ` ${word}`);
    line.append(st);
  }
  head.append(h, line);
  return head;
}

// card is a bordered list card, as the sessions list's table sits in.
export function card(...nodes) {
  const box = document.createElement("div");
  box.className = "sp-list";
  box.append(...nodes);
  return box;
}

// pageBox is a page in the sessions list's column.
export function pageBox(cls) {
  const box = document.createElement("section");
  box.className = `page ${cls}`.trim();
  return box;
}

// ---- pure row model ----

// counter shows a summed counter: "—" when no call reported it, never 0 (AC1).
export function counter(c) {
  return c && typeof c.value === "number" ? String(c.value) : DASH;
}

// statusOf names a session's status with a glyph, so colour is never the only signal; the
// glyphs are the session page's (016).
// An ordinary ended session is "ended" and the list shows no glyph for it (027 C7b); only an
// active, failed or hooks-only one does.
export function statusOf(s) {
  if (s.active) return { key: "active", glyph: "●", text: "active" };
  if (s.hooks_only) return { key: "hooks-only", glyph: "◇", text: "hooks only" };
  // a pending row cannot say failed or ended yet: a quiet placeholder, not the empty "ended" cell (spec 043 D3)
  if (s.pending) return { key: "pending", glyph: "○", text: PENDING_TITLE.toLowerCase() };
  if (toolCounts(s.tool_states).failed > 0 || (s.main_errors || 0) > 0) return { key: "failed", glyph: "✗", text: "failed" };
  return { key: "ended", glyph: "", text: "ended" };
}

// lookText lists what needs a look and of what kinds, in the server's words (spec 017 AC2),
// without a count prefix (027 C7b); "" when nothing does.
export function lookText(l) {
  const n = (l || {}).needs_look || 0;
  if (!n) return "";
  return ((l || {}).look_kinds || []).map((k) => k.words).join(", ");
}

// toolCounts is a session's tool calls (captured or hook-seen, every state) and the failed
// ones, from the server's per-state counts.
function toolCounts(states) {
  let total = 0;
  for (const v of Object.values(states || {})) if (typeof v === "number") total += v;
  return { total, failed: (states || {}).failed || 0 };
}

// rowCells is everything a list row shows, as plain words (spec 017 AC1). A hooks-only
// session has no model calls to count (E5).
export function rowCells(s) {
  const u = s.usage || {};
  const l = s.list || {};
  const models = s.models || [];
  const tools = toolCounts(s.tool_states);
  const e = s.effects || {};
  // a pending row has no has_hooks: hooks only is decided from hooks_only alone (spec 043 D15)
  const pending = Boolean(s.pending);
  const hooksOnly = Boolean(s.hooks_only) || (!pending && !s.has_calls && Boolean(s.has_hooks));
  // zero-valued parts are left out (027 C7b); a cell with nothing left shows "—"
  const nonZero = (n, one, many) => (n ? plural(n, one, many) : "");
  const mainCalls = l.main_calls ?? s.model_calls ?? 0;
  const calls = [nonZero(mainCalls, "call"), l.side_calls ? `${l.side_calls} side` : ""].filter(Boolean).join(" · ");
  const prompts = nonZero(l.prompts ?? 0, "prompt");
  const toolText = nonZero(tools.total, "tool call");
  const changes = [nonZero(e.changed ?? 0, "file changed", "files changed"), nonZero(l.commands ?? 0, "command"), nonZero(e.sites ?? 0, "site")].filter(Boolean).join(" · ");
  const work = [prompts, hooksOnly ? "no model calls captured" : calls, toolText].filter(Boolean).join(" · ");
  return {
    hooksOnly,
    pending,
    work: work || (tools.failed ? "" : DASH),
    id: s.id,
    start: s.first_at,
    title: pending ? PENDING_TITLE : s.title || NO_TITLE,
    key: s.session_key || "",
    folder: l.folder || "",
    folderFull: l.folder_full || l.folder || "",
    model: models.length ? models[0] + (models.length > 1 ? ` +${models.length - 1}` : "") : "",
    status: statusOf(s),
    prompts,
    calls: hooksOnly ? "no model calls captured" : calls,
    tools: toolText,
    failed: tools.failed,
    changes: changes || DASH,
    time: dur(l.clock_ms ?? null),
    input: hooksOnly ? DASH : counter(u.input),
    output: hooksOnly ? DASH : counter(u.output),
    cacheWrite: hooksOnly ? DASH : counter(u.cache_write),
    cacheRead: hooksOnly ? DASH : counter(u.cache_read),
    cost: hooksOnly ? { text: DASH, title: "" } : totalCost(u.cost),
    look: lookText(l),
  };
}

// compact renders a counter shown in the list as 3.7k, 94.5k or 15.1M; "—" stays "—".
// The exact value is kept for the cell's label.
export function compact(text) {
  const n = Number(text);
  if (text === DASH || text === "" || Number.isNaN(n)) return text;
  if (n < 1000) return String(n);
  if (n < 1e6) return `${(n / 1e3).toFixed(n < 1e4 ? 1 : 0)}k`;
  if (n < 1e9) return `${(n / 1e6).toFixed(1)}M`;
  return `${(n / 1e9).toFixed(1)}B`;
}

// startText is a session's start as "8:47 PM" (today), "Yesterday 7:08 PM" or
// "Nov 11, 7:12 PM" in the browser's time zone.
export function startText(iso, now = new Date()) {
  const d = new Date(iso || "");
  if (Number.isNaN(d.getTime())) return DASH;
  const time = d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  const day = (x) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((day(now) - day(d)) / 86400000);
  if (days === 0) return time;
  if (days === 1) return `Yesterday ${time}`;
  const date = d.toLocaleDateString(undefined, { month: "short", day: "numeric", ...(d.getFullYear() !== now.getFullYear() ? { year: "numeric" } : {}) });
  return `${date}, ${time}`;
}

// unassignedRow is one unassigned connection as the list's section shows it (spec 017
// AC2): its label, the client (the harness label names one), host, bytes each way and time.
export function unassignedRow(e) {
  const a = e.attribution || null;
  const port = e.port && e.port !== 443 ? `:${e.port}` : "";
  return {
    id: e.id,
    label: a ? a.label : "unassigned",
    client: (a && a.client) || e.client || DASH,
    host: `${e.host || DASH}${port}`,
    bytes: `↑ ${bytes(e.bytes_up ?? null)} · ↓ ${bytes(e.bytes_down ?? null)}`,
    at: e.at,
  };
}

// todayCells is the totals line from /api/today, since local midnight (spec 017 AC3); 0
// for every count when nothing ran (E1).
export function todayCells(t) {
  const x = t || {};
  return {
    sessions: x.sessions ?? 0,
    calls: x.calls ?? 0,
    changed: x.changed ?? 0,
    sites: x.sites ?? 0,
    input: counter(x.input),
    output: counter(x.output),
    cost: totalCost(x.cost),
  };
}

// sparkCounts is the server's per-hour counts and peak for one tile ("sessions" or
// "calls"), or null when there is nothing to draw: no buckets, or a peak of 0.
export function sparkCounts(t, name) {
  const h = (t && t.hourly) || {};
  const counts = h[name];
  const peak = h[`peak_${name}`];
  if (!Array.isArray(counts) || counts.length === 0 || !(peak > 0)) return null;
  return { counts, peak };
}

// sparkline is a row of thin bars, one per hour. Heights are set by CSS from --v and
// --peak, both the server's numbers; nothing is computed here. Null when empty.
export function sparkline(spec) {
  if (!spec) return null;
  const el = document.createElement("span");
  el.className = "spark";
  el.setAttribute("aria-hidden", "true");
  el.style.setProperty("--peak", String(spec.peak));
  for (const n of spec.counts) {
    const bar = document.createElement("i");
    bar.style.setProperty("--v", String(n));
    el.append(bar);
  }
  return el;
}

// localMidnight is the start of the browser-local day, as an RFC 3339 instant (AC4).
export function localMidnight(now = new Date()) {
  const d = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  return d.toISOString();
}

// ungroupedText is the link text under the table; "" when none exist. A first page that
// has more behind it reads "N+".
export function ungroupedText(items, next) {
  const n = items.length;
  if (n === 0) return "";
  return `${n}${next ? "+" : ""} ungrouped ${n === 1 && !next ? "exchange" : "exchanges"}`;
}

// moveSelection returns the new selected index; none selected starts at the first row.
export function moveSelection(index, delta, total) {
  if (total <= 0) return -1;
  if (index < 0) return 0;
  return Math.max(0, Math.min(total - 1, index + delta));
}

// ---- the page ----

const FILTER_LABELS = { all: "All", look: "Needs a look", active: "Active", hooks: "Hooks only" };

// PAGE_SIZE is the rows of one page; the server's default and maximum limit.
export const PAGE_SIZE = 50;

// pageCount is the pages an unfiltered list of total sessions has; an empty list has one.
export function pageCount(total) {
  return Math.max(1, Math.ceil((total || 0) / PAGE_SIZE));
}

function td(tr, text, cls) {
  const c = document.createElement("td");
  if (cls) c.className = cls;
  if (text !== null) c.textContent = text;
  tr.append(c);
  return c;
}

function span(cls, text) {
  const e = document.createElement("span");
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// rowNode builds a session's row once; fillRow writes its values. A live update refills the
// same row, so the title link it holds, and the focus on it, stay in the page (F1).
function rowNode(id) {
  const tr = document.createElement("tr");
  tr.dataset.id = id;
  const parts = { tr, cells: [], a: document.createElement("a"), place: span("splace"), look: span("slook"), key: {} };
  parts.a.href = `#/sessions/${encodeURIComponent(id)}`;
  parts.a.className = "stitle";
  for (const cls of COLUMNS.map((c) => c[1])) parts.cells.push(td(tr, null, cls));
  parts.cells[2].append(parts.a, parts.place, parts.look);
  // each count an unbreakable part, " · " between them as text, so the cell wraps only at
  // the dots, as Work and Changes do (setSegments); the cost is a line of its own
  parts.tokens = [span("tok seg"), span("tok seg"), span("tok seg"), span("tok seg")];
  parts.seps = [span("", " · "), span("", " · "), span("", " · ")];
  parts.cost = span("cost");
  // the bar under the figures: input, output, cache write and cache read side by side, each as
  // wide as its own count; the numbers are already in text, so the bar is hidden from screen readers
  parts.bar = span("tokbar");
  parts.bar.setAttribute("aria-hidden", "true");
  parts.barSegs = ["in", "out", "cw", "cache"].map((k) => span(`tokseg tokseg-${k}`));
  parts.bar.append(...parts.barSegs);
  parts.cells[6].append(parts.tokens[0], parts.seps[0], parts.tokens[1], parts.seps[1], parts.tokens[2], parts.seps[2], parts.tokens[3], parts.cost, parts.bar);
  // Work: prompts, calls and tool calls as segments, then the failed tools standing out
  parts.work = span("");
  parts.failedSep = span("");
  parts.failed = span("bad seg");
  parts.cells[3].append(parts.work, parts.failedSep, parts.failed);
  return parts;
}

// setText writes text only when it changed, so an unchanged cell is not touched.
function setText(e, text) {
  if (e.textContent !== text) e.textContent = text;
}

// setSegments writes "a · b · c" with each part in a span that does not break, so a cell
// that wraps does so at the dots (research D49); like setText it leaves an unchanged cell.
function setSegments(e, text) {
  if (e.textContent === text) return;
  const nodes = [];
  (text ? text.split(" · ") : []).forEach((part, i) => {
    if (i) nodes.push(" · ");
    const sp = document.createElement("span");
    sp.className = "seg";
    sp.textContent = part;
    nodes.push(sp);
  });
  e.replaceChildren(...nodes);
}

// setTitle writes the hover text only when it changed; "" shows none.
function setTitle(e, text) {
  if (e.title !== (text || "")) e.title = text || "";
}

// setToken writes "3.7k in": the compact counter, its label, and the exact value on hover.
function setToken(e, text, label) {
  setText(e, `${compact(text)} ${label}`);
  setTitle(e, text === DASH ? `${label}: not reported` : `${label}: ${text}`);
}

// setTokenBar hands each of the server's raw counts to its segment's flex-grow, so the
// browser sizes the segments; a count that is missing or zero gets no segment, and a row
// with none (a hooks-only session) shows no bar.
export function setTokenBar(parts, counts) {
  let any = false;
  parts.barSegs.forEach((seg, i) => {
    const n = counts[i];
    const has = typeof n === "number" && Number.isFinite(n) && n > 0;
    // each write only on a change: an attribute write dirties layout (spec 042 AC3)
    if (seg.hidden !== !has) seg.hidden = !has;
    const grow = has ? String(n) : "";
    if (seg.style.flexGrow !== grow) seg.style.flexGrow = grow;
    any = any || has;
  });
  if (parts.bar.hidden !== !any) parts.bar.hidden = !any;
}

// setHidden writes the hidden flag only when it changed (spec 042 AC3).
function setHidden(e, hidden) {
  if (e.hidden !== hidden) e.hidden = hidden;
}

// emptyHeavy clears every heavy cell of a pending row (spec 043 AC6): the stylesheet draws a
// placeholder bar of fixed size in each (tr.pending), so the cells hold no text. Each write is
// on a change only, so a row that stays pending is not touched again.
function emptyHeavy(parts) {
  const { cells } = parts;
  setSegments(parts.work, "");
  setText(parts.failedSep, "");
  setText(parts.failed, "");
  setSegments(cells[4], "");
  setText(cells[5], "");
  for (const t of parts.tokens) {
    setText(t, "");
    setTitle(t, "");
    setHidden(t, true);
  }
  for (const e of [...parts.seps, parts.cost]) setHidden(e, true);
  setText(parts.cost, "");
  setTitle(parts.cost, "");
  setTokenBar(parts, []);
}

function fillRow(parts, s, selected) {
  // the same session object and the same selection: nothing to write (spec 042 AC3)
  if (parts.lastS === s && parts.lastSel === selected) return;
  parts.lastS = s;
  parts.lastSel = selected;
  const c = rowCells(s);
  const { tr, cells, a, place, look, key } = parts;
  // the selection marks are written only when the row's selected state changed
  if (parts.marked !== selected) {
    parts.marked = selected;
    if (selected) tr.setAttribute("aria-selected", "true");
    else tr.removeAttribute("aria-selected");
  }
  // a pending row's heavy cells are placeholder bars drawn by the stylesheet (spec 043 AC6);
  // aria-busy says so for screen readers, and both go when the facts arrive
  if (parts.pending !== c.pending) {
    parts.pending = c.pending;
    if (c.pending) tr.setAttribute("aria-busy", "true");
    else tr.removeAttribute("aria-busy");
  }
  const cls = [selected ? "selected" : "", c.pending ? "pending" : ""].filter(Boolean).join(" ");
  if (tr.className !== cls) tr.className = cls;
  if (key.start !== c.start) {
    key.start = c.start;
    const t = timeElement(c.start);
    t.textContent = startText(c.start);
    cells[1].replaceChildren(t);
  }
  setText(a, c.title);
  setTitle(a, c.key ? `session key ${c.key}` : "");
  setText(place, [c.folder, c.model].filter(Boolean).join(" · "));
  setTitle(place, c.folderFull);
  setText(look, c.look);
  setTitle(look, c.look);
  if (key.status !== c.status.key) {
    key.status = c.status.key;
    // the glyph alone; the word is its hover and its accessible name (027, research D2)
    const icon = span(`sicon sicon-${c.status.key}`, c.status.glyph);
    icon.setAttribute("role", "img");
    icon.setAttribute("aria-label", c.status.text);
    icon.title = c.status.text;
    // an ordinary ended session shows no glyph (027 C7b); the cell and its column stay
    if (c.status.glyph) cells[0].replaceChildren(icon);
    else cells[0].replaceChildren();
  }
  if (c.pending) {
    emptyHeavy(parts);
    return;
  }
  setSegments(parts.work, c.work);
  setText(parts.failedSep, c.failed && c.work ? " · " : "");
  setText(parts.failed, c.failed ? `${c.failed} failed` : "");
  setSegments(cells[4], c.changes);
  setText(cells[5], c.time);
  setHidden(parts.tokens[0], false); // a pending row hid it
  if (c.hooksOnly) {
    // no model calls, so no counts to show: one dash for the whole cell (027 C7b)
    setText(parts.tokens[0], DASH);
    setTitle(parts.tokens[0], "");
  } else {
    setToken(parts.tokens[0], c.input, "in");
    setToken(parts.tokens[1], c.output, "out");
    setToken(parts.tokens[2], c.cacheWrite, "cache write");
    setToken(parts.tokens[3], c.cacheRead, "cache read");
  }
  for (const e of [parts.tokens[1], parts.tokens[2], parts.tokens[3], ...parts.seps]) setHidden(e, c.hooksOnly);
  // an unknown cost says nothing once its label is gone, so it is left out (027 C7b)
  setHidden(parts.cost, c.hooksOnly || c.cost.text === DASH);
  const u = s.usage || {};
  setTokenBar(parts, c.hooksOnly ? [] : [u.input, u.output, u.cache_write, u.cache_read].map((x) => x && x.value));
  // the Tokens header already says the figure is API-equivalent, so the row does not repeat it
  setText(parts.cost, c.cost.text);
  setTitle(parts.cost, c.cost.title);
}

// COLUMNS are the list's headers and each column's class; every count carries its own label
// in words (spec 017 AC1).
const COLUMNS = [
  ["", "status", "Status: ● active, ✗ failed, ◇ hooks only; an ordinary ended session shows none"], ["Start", "time"], ["Session", "title"], ["Work", "work"],
  ["Changes", "changes"], ["Time", "dur"], ["Tokens", "tokens", `Second line: ${explain("")}`],
];

// unassignedShown is how many unassigned connections the list's section lists; the rest are
// on the unassigned connections page.
export const UNASSIGNED_SHOWN = 10;

// unassignedSection lists the unassigned connections (spec 017 AC2): label, client, host,
// bytes and time, with a link to the full page when there are more. null when none.
function unassignedSection(count, items) {
  if (!count) return null;
  const box = document.createElement("section");
  box.className = "sp-unassigned";
  const h = document.createElement("h2");
  h.textContent = `Unassigned connections (${count})`;
  const p = note("muted", "Connections through the gateway's proxy that no session could be linked to.");
  const shown = (items || []).slice(0, UNASSIGNED_SHOWN).map((e) => ({ r: unassignedRow(e) }));
  // a column that says the same on every row is left out (027 C7b)
  const showLabel = shown.some(({ r }) => r.label !== "unassigned");
  const showClient = shown.some(({ r }) => r.client !== DASH && r.client !== "unknown");
  const { table, body } = tableWithHead([
    ...(showLabel ? [["Label", "label"]] : []), ...(showClient ? [["Client", "client"]] : []),
    ["Host", "host"], ["Bytes", "bytes"], ["Time", "time"],
  ]);
  for (const { r } of shown) {
    const tr = document.createElement("tr");
    if (showLabel) td(tr, r.label, "label");
    if (showClient) td(tr, r.client, "client");
    td(tr, r.host, "host");
    td(tr, r.bytes, "bytes");
    const t = timeElement(r.at);
    t.textContent = startText(r.at);
    td(tr, null, "time").append(t);
    body.append(tr);
  }
  const list = document.createElement("div");
  list.className = "sp-list";
  list.append(table);
  box.append(h, p, list);
  if (count > Math.min(UNASSIGNED_SHOWN, (items || []).length)) {
    const a = document.createElement("a");
    a.className = "quiet";
    a.href = "#/effects/unassigned";
    a.textContent = `All ${count} unassigned connections →`;
    const more = document.createElement("p");
    more.append(a);
    box.append(more);
  }
  return box;
}

// loader is what the list shows until its first page arrives: a line saying so and a few
// placeholder rows the size of real ones.
function loader() {
  const box = document.createElement("div");
  box.className = "sp-loading";
  box.setAttribute("role", "status");
  const p = document.createElement("p");
  p.className = "muted";
  p.textContent = "Loading sessions…";
  box.append(p);
  for (let i = 0; i < 6; i++) {
    const r = document.createElement("div");
    r.className = "skel";
    r.setAttribute("aria-hidden", "true");
    box.append(r);
  }
  return box;
}

// createSessionsPage builds the landing page once; update(state) redraws the table, the
// totals and the notes without touching the filter input, so typing keeps its focus.
// state = { items, next_after, error, loaded, kind, q, total, scanned, checking, stalled, page, today,
// ungrouped: { items, next_after }, unassigned, unassignedItems }; page is the page shown (or
// being loaded). Until the
// page has loaded the box shows a loader. The rows are shown as given: the server filtered them.
// handlers = { onPage(n), onKind(kind), onQuery(text, now), onRetry() }.
export function createSessionsPage(handlers) {
  const el = document.createElement("section");
  el.className = "sessions-page";
  const head = document.createElement("div");
  head.className = "sp-head";
  const h1 = document.createElement("h1");
  // the tiles below are the day's totals; say so once, here, as muted text (027 C7b)
  const when = span("sp-when", "today");
  when.title = "since local midnight";
  h1.append("Sessions", " ", when);
  const today = document.createElement("p");
  today.className = "today";
  head.append(h1, today);
  const bar = document.createElement("div");
  bar.className = "filter-bar";
  const input = document.createElement("input");
  input.type = "search";
  input.placeholder = "Filter sessions";
  input.setAttribute("aria-label", "Filter sessions");
  const picker = document.createElement("span");
  picker.className = "seg-group";
  picker.setAttribute("role", "group");
  picker.setAttribute("aria-label", "Show");
  const kinds = new Map();
  for (const k of FILTERS) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "seg";
    b.textContent = FILTER_LABELS[k];
    // the URL owns the kind: the app sets the hash, and the new kind comes back in update()
    b.addEventListener("click", () => handlers.onKind && handlers.onKind(k));
    kinds.set(k, b);
    picker.append(b);
  }
  bar.append(input, picker);
  const list = document.createElement("div");
  list.className = "sp-list";
  // the pager sits right under the fixed-height box, so a page change never moves it
  const pager = document.createElement("nav");
  pager.className = "sp-pager";
  pager.setAttribute("aria-label", "Pages");
  pager.hidden = true;
  // built once and only updated, so the pager never changes shape and a button keeps its focus
  const pagerBtn = (label, step) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "btn sm";
    b.textContent = label;
    b.addEventListener("click", () => handlers.onPage && handlers.onPage((state.page || 1) + step));
    return b;
  };
  const prevBtn = pagerBtn("‹ Previous", -1);
  const nextBtn = pagerBtn("Next ›", 1);
  const pageLabel = span("pg-label", "");
  pageLabel.setAttribute("aria-live", "polite");
  pager.append(prevBtn, pageLabel, nextBtn);
  const foot = document.createElement("div");
  foot.className = "sp-foot";
  const unassigned = document.createElement("div");
  // The list's status line sits under the pager, below the fixed-height box, so it never moves
  // the rows. It is one polite live region, built once: how many rows a filter is still
  // checking (spec 043 AC6), or that the follow-up gave up, with a Retry (E1).
  const status = document.createElement("div");
  status.className = "sp-status";
  status.setAttribute("aria-live", "polite");
  const checkingMsg = span("muted");
  const stalledMsg = span("muted", "Details are not available yet");
  const stalledRetry = document.createElement("button");
  stalledRetry.type = "button";
  stalledRetry.className = "btn sm";
  stalledRetry.textContent = "Retry";
  stalledRetry.addEventListener("click", () => handlers.onRetry && handlers.onRetry());
  for (const e of [checkingMsg, stalledMsg, stalledRetry]) e.hidden = true;
  status.append(checkingMsg, stalledMsg, stalledRetry);
  el.append(head, bar, list, pager, status, foot, unassigned);

  let kind = "all";
  let lastPage = 1;
  let state = { items: [], next_after: null, error: "", loaded: true, page: 1, today: null, ungrouped: { items: [], next_after: null }, unassigned: 0, unassignedItems: [] };

  // drawPager updates the pager: Previous, a "Page N of M" label ("Page N" when filtered: only
  // each page's next_after says more follow) and Next. The same three nodes are kept, so the
  // pager never changes shape and a button keeps its focus. A list of one page shows no pager.
  // A button that has just become disabled cannot keep focus: the other arrow takes it, else
  // the list box (a pager with no enabled button leaves nowhere else to go).
  list.tabIndex = -1;
  function drawPager() {
    const filtering = kind !== "all" || (state.q || "") !== "";
    const page = state.page || 1;
    // an unknown total (a failed cold load) reads "Page N", not "Page N of 1" (spec 042)
    const unknownTotal = !filtering && !(state.total > 0);
    const last = filtering ? null : pageCount(state.total);
    // On an error the held next_after belongs to an older page, so a filtered Next stays off:
    // where page n+1 starts is unknown (spec 042 E4). Unfiltered, the last good total still counts.
    // spec 030 AC12: an unfiltered error with an unknown total (a failed first load) turns Next on,
    // since loadPage corrects a page past the last one to the last page, so trying it is safe.
    const more = filtering
      ? Boolean(state.next_after) && !state.error
      : (Boolean(state.error) && unknownTotal) || (!unknownTotal && page < last);
    const single = page <= 1 && !more;
    // an error keeps the pager, so a failed page is never a dead end (spec 042 AC2, spec 030 AC12)
    const show = !single || Boolean(state.error);
    pager.hidden = !show;
    if (!show) return;
    const was = document.activeElement;
    const text = filtering || unknownTotal ? `Page ${page}` : `Page ${page} of ${last}`;
    if (pageLabel.textContent !== text) pageLabel.textContent = text;
    prevBtn.disabled = page <= 1;
    nextBtn.disabled = !more;
    if (was !== prevBtn && was !== nextBtn) return;
    if (!was.disabled) return;
    const other = was === prevBtn ? nextBtn : prevBtn;
    (other.disabled ? list : other).focus({ preventScroll: true });
  }
  let selected = "";
  let shown = [];
  let retryBtn = null; // the Retry button now in the box, if any
  // The server searches: the page only reports the text (the app waits for typing to settle).
  // Esc clears the box and reports at once, in every browser; Chrome clears a search box on
  // Esc by itself and fires `search`, not `input`, so that is handled too (F4).
  const report = (now) => handlers.onQuery && handlers.onQuery(input.value, now);
  input.addEventListener("input", () => report(false));
  input.addEventListener("search", () => report(true));
  input.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" || input.value === "") return;
    input.value = "";
    report(true);
  });
  // The table and one row per session id are kept across redraws (F1): a live update
  // refills the rows in place and moves only rows whose place changed. When a move takes
  // the focused link out of the page for a moment, focus is put back on it.
  const { table, body } = tableWithHead(COLUMNS);
  const rows = new Map();

  function drawRows() {
    if (list.children.length !== 1 || list.children[0] !== table) list.replaceChildren(table);
    const focused = document.activeElement;
    const want = [];
    const keep = new Set();
    for (const s of shown) {
      let parts = rows.get(s.id);
      if (!parts) {
        parts = rowNode(s.id);
        rows.set(s.id, parts);
      }
      fillRow(parts, s, s.id === selected);
      want.push(parts.tr);
      keep.add(s.id);
    }
    for (const id of [...rows.keys()]) if (!keep.has(id)) rows.delete(id);
    // move only the rows that are out of place, so an unchanged row is never re-inserted and
    // a live update does not force a full table layout (spec 042 AC3)
    let moved = false;
    want.forEach((tr, i) => {
      if (body.children[i] === tr) return;
      body.insertBefore(tr, body.children[i] || null);
      moved = true;
    });
    while (body.children.length > want.length) {
      body.children[body.children.length - 1].remove();
      moved = true;
    }
    if (moved) {
      const back = [...rows.values()].find((p) => p.a === focused);
      if (back && document.activeElement !== focused) focused.focus({ preventScroll: true });
    }
  }

  // What the tiles, the footer, the unassigned section and the error box were last built from:
  // the tiles are rebuilt when their totals changed (compared by value); the footer, the
  // unassigned section and the error box when their input changed (by reference), so a live
  // session event does not replace nodes it does not change (spec 042 AC3).
  const NONE = Symbol("none");
  let builtToday = NONE;
  let builtUngrouped = NONE;
  let builtUnassigned = NONE;
  let builtUnassignedItems = NONE;
  let builtError = null;
  let errorBox = null;
  const pressed = new Map(); // the aria-pressed value last written to each kind button

  function drawToday() {
    // compared by value: a refetch returns a new object with equal totals (spec 042)
    const key = JSON.stringify(state.today ?? null);
    if (key === builtToday) return;
    builtToday = key;
    const t = todayCells(state.today);
    today.replaceChildren();
    const stat = (value, label, exact, spark) => {
      const sp = document.createElement("span");
      sp.className = "stat";
      const b = document.createElement("b");
      b.textContent = value;
      if (exact !== undefined && exact !== value) sp.title = exact === DASH ? "not reported" : exact;
      sp.append(b, ` ${label}`);
      // The tile stacks bottom-up (column-reverse), so the first child sits lowest.
      const bars = sparkline(spark);
      if (bars) sp.prepend(bars);
      today.append(sp);
    };
    stat(String(t.sessions), t.sessions === 1 ? "session" : "sessions", undefined, sparkCounts(state.today, "sessions"));
    stat(String(t.calls), t.calls === 1 ? "model call" : "model calls", "side calls included", sparkCounts(state.today, "calls"));
    stat(String(t.changed), t.changed === 1 ? "file changed" : "files changed");
    stat(String(t.sites), t.sites === 1 ? "site" : "sites");
    stat(compact(t.input), "input", t.input);
    stat(compact(t.output), "output", t.output);
    stat(t.cost.text, `${LABEL} cost`, t.cost.title);
  }

  // drawStatus writes the status line; each part only when it changed (spec 042 AC3)
  function drawStatus() {
    const filtering = kind !== "all" || (state.q || "") !== "";
    const shownList = state.loaded && !state.error;
    const n = filtering && shownList ? state.checking || 0 : 0;
    // a requested page the walk has not reached keeps the URL (spec 043 AC6, D13): say so, so the pager's page and the URL's are not a silent mismatch
    const want = n && state.want && state.want !== state.page ? state.want : 0;
    setText(checkingMsg, n ? `${n} ${n === 1 ? "session" : "sessions"} still being checked${want ? ` — page ${want} shows once they are` : ""}` : "");
    setHidden(checkingMsg, !n);
    const stalled = Boolean(state.stalled) && shownList;
    setHidden(stalledMsg, !stalled);
    setHidden(stalledRetry, !stalled);
  }

  function redraw() {
    drawStatus();
    for (const [k, b] of kinds) {
      const on = String(k === kind);
      if (pressed.get(k) === on) continue;
      pressed.set(k, on);
      b.setAttribute("aria-pressed", on);
    }
    drawToday();
    // a page change starts at the top of the box
    if ((state.page || 1) !== lastPage) {
      lastPage = state.page || 1;
      list.scrollTop = 0;
    }
    drawPager();
    // a Retry that is replaced (the reload shows the skeleton) cannot keep focus: the box takes it
    const retryHadFocus = Boolean(retryBtn) && document.activeElement === retryBtn;
    if (!state.error && retryHadFocus) list.focus({ preventScroll: true });
    if (state.error) {
      shown = [];
      selected = "";
      // the error and a Retry sit in the box; the pager stays usable (spec 042 AC2). The box
      // is built when the error is new or changed, else left in place (spec 042 AC3).
      if (builtError !== state.error || list.children[0] !== errorBox) {
        const box = document.createElement("div");
        box.className = "sp-error";
        const retry = document.createElement("button");
        retry.type = "button";
        retry.className = "btn sm";
        retry.textContent = "Retry";
        retry.addEventListener("click", () => handlers.onRetry && handlers.onRetry());
        box.append(note("error", state.error), retry);
        list.replaceChildren(box);
        // as before, an error shows no ungrouped link; it is built again when the list returns
        foot.replaceChildren();
        builtUngrouped = NONE;
        builtError = state.error;
        errorBox = box;
        retryBtn = retry;
        if (retryHadFocus) retry.focus({ preventScroll: true });
      }
      return;
    }
    builtError = null;
    errorBox = null;
    if (!state.loaded) {
      shown = [];
      selected = "";
      // the loader replaces the list on each call, so the footer clears with it, as before
      list.replaceChildren(loader());
      foot.replaceChildren();
      builtUngrouped = NONE;
      return;
    }
    // the server has filtered and searched: every row held is a row shown
    shown = state.items;
    if (!shown.some((s) => s.id === selected)) selected = "";
    const filtering = kind !== "all" || (state.q || "") !== "";
    if (shown.length === 0) {
      selected = "";
      if (!(state.total > 0)) list.replaceChildren(note("empty", "No sessions captured yet."));
      else if (state.next_after) list.replaceChildren(note("muted", `No matches in the newest ${plural(state.scanned || 0, "session")} yet`));
      else list.replaceChildren(note("empty", filtering ? "no sessions match" : "No sessions captured yet."));
    } else {
      drawRows();
    }
    if (state.ungrouped !== builtUngrouped) {
      builtUngrouped = state.ungrouped;
      foot.replaceChildren();
      const ug = ungroupedText(state.ungrouped.items, state.ungrouped.next_after);
      if (ug) {
        const a = document.createElement("a");
        a.className = "quiet";
        a.href = "#/ungrouped";
        a.textContent = `${ug} →`;
        const p = document.createElement("p");
        p.append(a);
        foot.append(p);
      }
    }
    if (state.unassigned !== builtUnassigned || state.unassignedItems !== builtUnassignedItems) {
      builtUnassigned = state.unassigned;
      builtUnassignedItems = state.unassignedItems;
      const ua = unassignedSection(state.unassigned, state.unassignedItems);
      if (ua) unassigned.replaceChildren(ua);
      else unassigned.replaceChildren();
    }
  }

  redraw();
  return {
    el,
    update(next) {
      state = { ...state, ...next };
      if (next.kind !== undefined) kind = next.kind;
      redraw();
    },
    // setQuery puts text in the search box (the URL changed: Back, a link); typing is never
    // overwritten, since update() leaves the box alone.
    setQuery(q) {
      if (input.value.trim() !== q) input.value = q;
    },
    query: () => input.value.trim(),
    // position is the key bar's session n/total, or null when nothing is selected.
    position() {
      const i = shown.findIndex((s) => s.id === selected);
      return i < 0 ? null : { index: i + 1, total: shown.length };
    },
    select(delta) {
      const i = moveSelection(shown.findIndex((s) => s.id === selected), delta, shown.length);
      selected = i < 0 ? "" : shown[i].id;
      redraw();
      const row = el.querySelector("tr.selected");
      if (row && row.scrollIntoView) row.scrollIntoView({ block: "nearest" });
      return this.position();
    },
    selectedId: () => selected,
    focusFilter() {
      input.focus();
    },
  };
}
