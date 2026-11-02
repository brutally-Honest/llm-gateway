import { timeElement } from "./entries.js";
import { filterSessions, FILTERS } from "./filters.js";
import { CHIP_KEYS, chipOf } from "./model.js";

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
  for (const [label, cls] of columns) {
    const th = document.createElement("th");
    th.scope = "col";
    if (cls) th.className = cls;
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

// ---- pure row model ----

// counter shows a summed counter: "—" when no call reported it, never 0 (AC1).
function counter(c) {
  return c && typeof c.value === "number" ? String(c.value) : DASH;
}

// countedChips turns tool_states, the server's per-chip counts, into the chips with a
// count above zero, in display order. It only reads the counts.
export function countedChips(toolStates) {
  const out = [];
  for (const key of CHIP_KEYS) {
    const count = (toolStates || {})[key];
    if (typeof count === "number" && count > 0) {
      const chip = chipOf({ chip: key });
      out.push({ key, glyph: chip.glyph, text: chip.text, title: chip.title, count });
    }
  }
  return out;
}

// statusOf names a session's status with a glyph, so colour is never the only signal.
export function statusOf(s) {
  if (s.hooks_only) return { key: "hooks-only", glyph: "◇", text: "hooks only" };
  if (s.active) return { key: "active", glyph: "●", text: "active" };
  return { key: "ended", glyph: "○", text: "ended" };
}

// rowCells is everything a list row shows, as plain data. Errors read main_errors.
export function rowCells(s) {
  const u = s.usage || {};
  const sources = [];
  if (s.has_calls) sources.push("API");
  if (s.has_hooks || s.hooks_only) sources.push("hooks");
  return {
    id: s.id,
    start: s.first_at,
    title: s.title || NO_TITLE,
    sources: sources.join(" + "),
    calls: s.model_calls ?? 0,
    chips: countedChips(s.tool_states),
    errors: s.main_errors ?? 0,
    status: statusOf(s),
    input: counter(u.input),
    output: counter(u.output),
    cacheWrite: counter(u.cache_write),
    cacheRead: counter(u.cache_read),
  };
}

// todayCells is the totals line from /api/today; 0 sessions and calls when nothing ran.
export function todayCells(t) {
  const x = t || {};
  return {
    sessions: x.sessions ?? 0,
    calls: x.calls ?? 0,
    input: counter(x.input),
    output: counter(x.output),
    cacheWrite: counter(x.cache_write),
    cacheRead: counter(x.cache_read),
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

const FILTER_LABELS = { all: "All", errors: "Has errors", gaps: "Has gaps", hooks: "Hooks only" };

function td(tr, text, cls) {
  const c = document.createElement("td");
  if (cls) c.className = cls;
  if (text !== null) c.textContent = text;
  tr.append(c);
  return c;
}

function chipNode(c) {
  const s = document.createElement("span");
  s.className = `chip chip-${c.key}`;
  s.title = c.title;
  s.textContent = `${c.glyph} ${c.count}`;
  s.setAttribute("aria-label", `${c.count} ${c.text}`);
  return s;
}

function rowNode(s, selected) {
  const c = rowCells(s);
  const tr = document.createElement("tr");
  tr.dataset.id = c.id;
  if (selected) {
    tr.className = "selected";
    tr.setAttribute("aria-selected", "true");
  }
  const start = td(tr, null, "time");
  start.append(timeElement(c.start));
  const title = td(tr, null, "title");
  const a = document.createElement("a");
  a.href = `#/sessions/${encodeURIComponent(c.id)}`;
  a.textContent = c.title;
  title.append(a);
  td(tr, c.sources);
  td(tr, String(c.calls), "num");
  const chips = td(tr, null, "chips");
  for (const ch of c.chips) chips.append(chipNode(ch), document.createTextNode(" "));
  td(tr, String(c.errors), "num");
  td(tr, `${c.status.glyph} ${c.status.text}`, `status status-${c.status.key}`);
  td(tr, c.input, "num");
  td(tr, c.output, "num");
  td(tr, c.cacheWrite, "num");
  td(tr, c.cacheRead, "num");
  return tr;
}

const COLUMNS = [
  ["Start"], ["Title"], ["Sources"], ["Calls", "num"], ["Tools"], ["Errors", "num"], ["Status"],
  ["Input", "num"], ["Output", "num"], ["Cache write", "num"], ["Cache read", "num"],
];

// createSessionsPage builds the landing page once; update(state) redraws the table, the
// totals and the notes without touching the filter input, so typing keeps its focus.
// state = { items, next_after, error, today, ungrouped: { items, next_after } }.
// handlers = { onMore }.
export function createSessionsPage(handlers) {
  const el = document.createElement("section");
  el.className = "sessions-page";
  const today = document.createElement("p");
  today.className = "today";
  const bar = document.createElement("div");
  bar.className = "filter-bar";
  const input = document.createElement("input");
  input.type = "search";
  input.placeholder = "Filter sessions";
  input.setAttribute("aria-label", "Filter sessions");
  const picker = document.createElement("span");
  picker.setAttribute("role", "group");
  picker.setAttribute("aria-label", "Show");
  const kinds = new Map();
  for (const k of FILTERS) {
    const b = document.createElement("button");
    b.type = "button";
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
  filterNote.className = "muted";
  const list = document.createElement("div");
  const foot = document.createElement("div");
  el.append(today, bar, filterNote, list, foot);

  let kind = "all";
  let state = { items: [], next_after: null, error: "", today: null, ungrouped: { items: [], next_after: null } };
  let selected = "";
  let shown = [];
  input.addEventListener("input", () => redraw());

  function redraw() {
    for (const [k, b] of kinds) b.setAttribute("aria-pressed", String(k === kind));
    const t = todayCells(state.today);
    today.textContent = `Today: ${t.sessions} sessions, ${t.calls} calls — input ${t.input}, output ${t.output}, cache-write ${t.cacheWrite}, cache-read ${t.cacheRead}`;
    list.replaceChildren();
    foot.replaceChildren();
    filterNote.textContent = "";
    if (state.error) {
      list.append(note("error", state.error));
      return;
    }
    const f = filterSessions(state.items, { kind, text: input.value, hasMore: Boolean(state.next_after) });
    shown = f.rows;
    filterNote.textContent = f.note;
    if (!shown.some((s) => s.id === selected)) selected = "";
    if (state.items.length === 0) {
      list.append(note("empty", "No sessions captured yet."));
    } else if (shown.length === 0) {
      list.append(note("empty", "no sessions match"));
    } else {
      const { table, body } = tableWithHead(COLUMNS);
      const frag = document.createDocumentFragment();
      for (const s of shown) frag.append(rowNode(s, s.id === selected));
      body.append(frag);
      list.append(table);
    }
    const more = loadMore(state, handlers.onMore);
    if (more) foot.append(more);
    const ug = ungroupedText(state.ungrouped.items, state.ungrouped.next_after);
    if (ug) {
      const a = document.createElement("a");
      a.className = "quiet";
      a.href = "#/ungrouped";
      a.textContent = ug;
      const p = document.createElement("p");
      p.append(a);
      foot.append(p);
    }
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
