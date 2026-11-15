import { timeElement } from "./entries.js";
import { filterSessions, FILTERS } from "./filters.js";
import { bytes, dur, plural } from "./fmt.js";
import { LABEL, explain, totalCost } from "./cost.js";

export const NO_TITLE = "no prompt captured";
const DASH = "—";

// ---- shared small builders (also used by the ungrouped and exchange views) ----

export function loadMore(data, onMore) {
  if (!data.next_after) return null;
  const b = document.createElement("button");
  b.textContent = "Load more";
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
export function statusOf(s) {
  if (s.active) return { key: "active", glyph: "●", text: "active" };
  if (s.hooks_only) return { key: "hooks-only", glyph: "◇", text: "hooks only" };
  return { key: "ended", glyph: "■", text: "ended" };
}

// sourcesOf is how a session was captured, as glyph and word: API calls, hook events.
export function sourcesOf(s) {
  const out = [];
  if (s.has_calls) out.push("⇄ API");
  if (s.has_hooks || s.hooks_only) out.push("⚑ hooks");
  return out.join(" + ") || DASH;
}

// lookText says how many items need a look and of what kinds, in the server's words
// (spec 017 AC2); "" when nothing does.
export function lookText(l) {
  const n = (l || {}).needs_look || 0;
  if (!n) return "";
  const kinds = ((l || {}).look_kinds || []).map((k) => k.words).join(", ");
  return `${n} ${n === 1 ? "needs" : "need"} a look${kinds ? `: ${kinds}` : ""}`;
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
  const hooksOnly = Boolean(s.hooks_only) || (!s.has_calls && Boolean(s.has_hooks));
  let calls = plural(l.main_calls ?? s.model_calls ?? 0, "call");
  if (l.side_calls) calls += ` · ${l.side_calls} side`;
  return {
    id: s.id,
    start: s.first_at,
    title: s.title || NO_TITLE,
    key: s.session_key || "",
    folder: l.folder || "",
    folderFull: l.folder_full || l.folder || "",
    model: models.length ? models[0] + (models.length > 1 ? ` +${models.length - 1}` : "") : "",
    sources: sourcesOf(s),
    status: statusOf(s),
    prompts: plural(l.prompts ?? 0, "prompt"),
    calls: hooksOnly ? "no model calls captured" : calls,
    tools: plural(tools.total, "tool call"),
    failed: tools.failed,
    changes: [plural(e.changed ?? 0, "file changed", "files changed"), plural(l.commands ?? 0, "command"), plural(e.sites ?? 0, "site")].join(" · "),
    time: dur(l.clock_ms ?? null),
    input: hooksOnly ? DASH : counter(u.input),
    output: hooksOnly ? DASH : counter(u.output),
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

// startText is a session's start as "Today 8:47 PM", "Yesterday 7:08 PM" or
// "Nov 11, 7:12 PM" in the browser's time zone.
export function startText(iso, now = new Date()) {
  const d = new Date(iso || "");
  if (Number.isNaN(d.getTime())) return DASH;
  const time = d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  const day = (x) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((day(now) - day(d)) / 86400000);
  if (days === 0) return `Today ${time}`;
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
  parts.cells[1].append(parts.a, parts.place, parts.look);
  parts.tokens = [span("tok"), span("tok"), span("tok")];
  parts.cost = span("cost");
  parts.cells[8].append(...parts.tokens, parts.cost);
  parts.tools = [span(""), span("bad")];
  parts.cells[5].append(...parts.tools);
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
  text.split(" · ").forEach((part, i) => {
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

function fillRow(parts, s, selected) {
  const c = rowCells(s);
  const { tr, cells, a, place, look, key } = parts;
  tr.className = selected ? "selected" : "";
  if (selected) tr.setAttribute("aria-selected", "true");
  else tr.removeAttribute("aria-selected");
  if (key.start !== c.start) {
    key.start = c.start;
    const t = timeElement(c.start);
    t.textContent = startText(c.start);
    cells[0].replaceChildren(t);
  }
  setText(a, c.title);
  setTitle(a, c.key ? `session key ${c.key}` : "");
  setText(place, [c.folder, c.model].filter(Boolean).join(" · "));
  setTitle(place, c.folderFull);
  setText(look, c.look);
  setTitle(look, c.look);
  setText(cells[2], c.sources);
  if (key.status !== c.status.key) {
    key.status = c.status.key;
    const pill = span(`spill spill-${c.status.key}`, `${c.status.glyph} ${c.status.text}`);
    cells[3].replaceChildren(pill);
  }
  setSegments(cells[4], `${c.prompts} · ${c.calls}`);
  setText(parts.tools[0], c.tools);
  setText(parts.tools[1], c.failed ? ` · ${c.failed} failed` : "");
  setSegments(cells[6], c.changes);
  setText(cells[7], c.time);
  setToken(parts.tokens[0], c.input, "in");
  setToken(parts.tokens[1], c.output, "out");
  setToken(parts.tokens[2], c.cacheRead, "cache read");
  setText(parts.cost, `${c.cost.text} ${LABEL}`);
  setTitle(parts.cost, c.cost.title);
}

// COLUMNS are the list's headers and each column's class; every count carries its own label
// in words (spec 017 AC1).
const COLUMNS = [
  ["Start", "time"], ["Session", "title"], ["Sources", "src"], ["Status", "status"], ["Work", "work"],
  ["Tools", "tools"], ["Changes", "changes"], ["Time", "dur"], ["Tokens", "tokens", `Second line: ${explain("")}`],
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
  const { table, body } = tableWithHead([["Label", "label"], ["Client", "client"], ["Host", "host"], ["Bytes", "bytes"], ["Time", "time"]]);
  for (const e of (items || []).slice(0, UNASSIGNED_SHOWN)) {
    const r = unassignedRow(e);
    const tr = document.createElement("tr");
    td(tr, r.label, "label");
    td(tr, r.client, "client");
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
// state = { items, next_after, error, loaded, today, ungrouped: { items, next_after },
// unassigned, unassignedItems }; until the first page has loaded the list shows a loader.
// handlers = { onMore }.
export function createSessionsPage(handlers) {
  const el = document.createElement("section");
  el.className = "sessions-page";
  const head = document.createElement("div");
  head.className = "sp-head";
  const h1 = document.createElement("h1");
  h1.textContent = "Sessions";
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
    b.addEventListener("click", () => {
      kind = k;
      redraw();
    });
    kinds.set(k, b);
    picker.append(b);
  }
  bar.append(input, picker);
  const filterNote = document.createElement("p");
  filterNote.className = "muted sp-filter-note";
  const list = document.createElement("div");
  list.className = "sp-list";
  const foot = document.createElement("div");
  foot.className = "sp-foot";
  const unassigned = document.createElement("div");
  el.append(head, bar, filterNote, list, foot, unassigned);

  let kind = "all";
  let state = { items: [], next_after: null, error: "", loaded: true, today: null, ungrouped: { items: [], next_after: null }, unassigned: 0, unassignedItems: [] };
  let selected = "";
  let shown = [];
  input.addEventListener("input", () => redraw());
  // Esc clears the filter and shows the whole list in every browser; Chrome clears a search
  // box on Esc by itself and fires `search`, not `input`, so that is handled too (F4).
  input.addEventListener("search", () => redraw());
  input.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" || input.value === "") return;
    input.value = "";
    redraw();
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
    const have = Array.from(body.children);
    if (have.length !== want.length || have.some((tr, i) => tr !== want[i])) {
      body.replaceChildren(...want);
      const back = [...rows.values()].find((p) => p.a === focused);
      if (back && document.activeElement !== focused) focused.focus({ preventScroll: true });
    }
  }

  function redraw() {
    for (const [k, b] of kinds) b.setAttribute("aria-pressed", String(k === kind));
    const t = todayCells(state.today);
    today.replaceChildren();
    const stat = (value, label, exact) => {
      const sp = document.createElement("span");
      sp.className = "stat";
      const b = document.createElement("b");
      b.textContent = value;
      if (exact !== undefined && exact !== value) sp.title = exact === DASH ? "not reported" : exact;
      sp.append(b, ` ${label}`);
      today.append(sp);
    };
    today.append("Today");
    today.title = "since local midnight";
    stat(String(t.sessions), t.sessions === 1 ? "session" : "sessions");
    stat(String(t.calls), t.calls === 1 ? "model call" : "model calls", "side calls included");
    stat(String(t.changed), t.changed === 1 ? "file changed" : "files changed");
    stat(String(t.sites), t.sites === 1 ? "site" : "sites");
    stat(compact(t.input), "input", t.input);
    stat(compact(t.output), "output", t.output);
    stat(t.cost.text, `${LABEL} cost`, t.cost.title);
    foot.replaceChildren();
    filterNote.textContent = "";
    if (state.error) {
      list.replaceChildren(note("error", state.error));
      return;
    }
    if (!state.loaded) {
      list.replaceChildren(loader());
      return;
    }
    const f = filterSessions(state.items, { kind, text: input.value, hasMore: Boolean(state.next_after) });
    shown = f.rows;
    filterNote.textContent = f.note;
    if (!shown.some((s) => s.id === selected)) selected = "";
    if (state.items.length === 0) {
      list.replaceChildren(note("empty", "No sessions captured yet."));
    } else if (shown.length === 0) {
      list.replaceChildren(note("empty", "no sessions match"));
    } else {
      drawRows();
    }
    const more = loadMore(state, handlers.onMore);
    if (more) foot.append(more);
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
    const ua = unassignedSection(state.unassigned, state.unassignedItems);
    if (ua) unassigned.replaceChildren(ua);
    else unassigned.replaceChildren();
  }

  redraw();
  return {
    el,
    update(next) {
      state = { ...state, ...next };
      redraw();
    },
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
