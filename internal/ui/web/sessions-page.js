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
    effects: effectsCell(s.effects),
  };
}

// effectsCell is a session's effect counts, "changed · read · sites", with a title naming
// each; "—" when the session has none.
export function effectsCell(e) {
  if (!e || (e.changed === 0 && e.read === 0 && e.sites === 0)) return { text: DASH, title: "no effects" };
  return { text: `${e.changed} · ${e.read} · ${e.sites}`, title: `${e.changed} changed files · ${e.read} files read · ${e.sites} sites` };
}

// unassignedText is the quiet link text for connections linked to no session; "" for none.
export function unassignedText(n) {
  if (!n) return "";
  return `${n} unassigned ${n === 1 ? "connection" : "connections"}`;
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

// rowNode builds a session's row once; fillRow writes its values. A live update refills the
// same row, so the title link it holds, and the focus on it, stay in the page (F1).
function rowNode(id) {
  const tr = document.createElement("tr");
  tr.dataset.id = id;
  const parts = { tr, cells: [], a: document.createElement("a"), key: {} };
  parts.a.href = `#/sessions/${encodeURIComponent(id)}`;
  for (const cls of ["time", "title", "", "num", "chips", "num", "", "num", "num", "num", "num", "num"]) parts.cells.push(td(tr, null, cls));
  parts.cells[1].append(parts.a);
  return parts;
}

// setText writes text only when it changed, so an unchanged cell is not touched.
function setText(e, text) {
  if (e.textContent !== text) e.textContent = text;
}

function fillRow(parts, s, selected) {
  const c = rowCells(s);
  const { tr, cells, a, key } = parts;
  tr.className = selected ? "selected" : "";
  if (selected) tr.setAttribute("aria-selected", "true");
  else tr.removeAttribute("aria-selected");
  if (key.start !== c.start) {
    key.start = c.start;
    cells[0].replaceChildren(timeElement(c.start));
  }
  setText(a, c.title);
  setText(cells[2], c.sources);
  setText(cells[3], String(c.calls));
  const chips = c.chips.map((ch) => `${ch.key}:${ch.count}`).join(" ");
  if (key.chips !== chips) {
    key.chips = chips;
    cells[4].replaceChildren();
    for (const ch of c.chips) cells[4].append(chipNode(ch), document.createTextNode(" "));
  }
  setText(cells[5], String(c.errors));
  cells[6].className = `status status-${c.status.key}`;
  setText(cells[6], `${c.status.glyph} ${c.status.text}`);
  setText(cells[7], c.input);
  setText(cells[8], c.output);
  setText(cells[9], c.cacheWrite);
  setText(cells[10], c.cacheRead);
  setText(cells[11], c.effects.text);
  if (cells[11].title !== c.effects.title) cells[11].title = c.effects.title;
}

const COLUMNS = [
  ["Start"], ["Title"], ["Sources"], ["Calls", "num"], ["Tools"], ["Errors", "num"], ["Status"],
  ["Input", "num"], ["Output", "num"], ["Cache write", "num"], ["Cache read", "num"], ["Effects", "num"],
];

// createSessionsPage builds the landing page once; update(state) redraws the table, the
// totals and the notes without touching the filter input, so typing keeps its focus.
// state = { items, next_after, error, today, ungrouped: { items, next_after }, unassigned }.
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
  let state = { items: [], next_after: null, error: "", today: null, ungrouped: { items: [], next_after: null }, unassigned: 0 };
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
    today.textContent = `Today: ${t.sessions} sessions, ${t.calls} calls — input ${t.input}, output ${t.output}, cache-write ${t.cacheWrite}, cache-read ${t.cacheRead}`;
    foot.replaceChildren();
    filterNote.textContent = "";
    if (state.error) {
      list.replaceChildren(note("error", state.error));
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
      a.textContent = ug;
      const p = document.createElement("p");
      p.append(a);
      foot.append(p);
    }
    const ua = unassignedText(state.unassigned);
    if (ua) {
      const a = document.createElement("a");
      a.className = "quiet";
      a.href = "#/effects/unassigned";
      a.textContent = ua;
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
