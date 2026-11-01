// Pure helpers shared by the components.

const rel = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

// formatLocal renders an RFC 3339 instant in the browser's local time.
export function formatLocal(iso) {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? String(iso) : d.toLocaleString();
}

// formatRelative renders the distance from now, such as "3 minutes ago".
export function formatRelative(iso, now = Date.now()) {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "";
  const s = Math.round((t - now) / 1000);
  const units = [["day", 86400], ["hour", 3600], ["minute", 60]];
  for (const [name, size] of units) {
    if (Math.abs(s) >= size) return rel.format(Math.trunc(s / size), name);
  }
  return rel.format(s, "second");
}

// timeElement is a <time> showing local time, with the relative time as its hover title.
export function timeElement(iso) {
  const el = document.createElement("time");
  el.dateTime = iso;
  el.textContent = formatLocal(iso);
  el.title = formatRelative(iso);
  return el;
}

// upsertSession replaces the row with the same id or adds it, newest activity first.
export function upsertSession(rows, row) {
  const out = rows.filter((r) => r.id !== row.id);
  out.push(row);
  out.sort((a, b) => (a.last_at < b.last_at ? 1 : a.last_at > b.last_at ? -1 : 0));
  return out;
}

// addUnique appends the items whose id is not yet held, keeping order.
export function addUnique(rows, items) {
  const seen = new Set(rows.map((r) => r.id));
  return rows.concat(items.filter((i) => !seen.has(i.id)));
}

export function usageText(u) {
  const n = (c) => (c.value === null || c.value === undefined ? "?" : String(c.value));
  return `in ${n(u.input)} / out ${n(u.output)}`;
}

// startKey pads the fraction of an RFC 3339 instant to nanoseconds so instants compare as strings.
function startKey(s) {
  const m = /^(.*?)(?:\.(\d+))?Z$/.exec(s || "");
  return m ? `${m[1]}.${(m[2] || "").padEnd(9, "0")}` : String(s);
}

// compareEntries orders by exchange start, then exchange id, then index, as the server does.
export function compareEntries(a, b) {
  const x = startKey(a.start);
  const y = startKey(b.start);
  if (x !== y) return x < y ? -1 : 1;
  if (a.exchange_id !== b.exchange_id) return a.exchange_id < b.exchange_id ? -1 : 1;
  return a.index - b.index;
}

// replaceExchange drops every entry of the exchange the list belongs to and inserts the
// list at its sorted positions. `entries` and `exchange` events are applied the same way.
export function replaceExchange(entries, list) {
  if (!list || list.length === 0) return entries;
  const ids = new Set(list.map((e) => e.exchange_id));
  const out = entries.filter((e) => !ids.has(e.exchange_id));
  out.push(...list);
  out.sort(compareEntries);
  return out;
}

// pairTools maps each tool_call_id to its tool_result entry.
export function pairTools(entries) {
  const results = new Map();
  for (const e of entries) {
    if (e.kind === "tool_result" && e.tool_call_id) results.set(e.tool_call_id, e);
  }
  return results;
}

// groupTurns splits entries into turns, each starting at a user prompt. Entries before the
// first prompt form a turn of their own.
export function groupTurns(entries) {
  const turns = [];
  for (const e of entries) {
    if (e.kind === "user_prompt" || turns.length === 0) turns.push({ prompt: e.kind === "user_prompt" ? e : null, entries: [] });
    turns[turns.length - 1].entries.push(e);
  }
  return turns;
}

// matchIds returns the set of entry ids that pass the text filter and tool picker, or null
// when neither is set. A tool call and its result stand or fall together.
export function matchIds(entries, text, tool) {
  const q = (text || "").trim().toLowerCase();
  if (!q && !tool) return null;
  const results = pairTools(entries);
  const callOf = new Map();
  for (const e of entries) if (e.kind === "tool_call" && e.tool_call_id) callOf.set(e.tool_call_id, e);
  const hit = (e) => [e.text, e.name, e.model].some((s) => s && s.toLowerCase().includes(q));
  const ids = new Set();
  for (const e of entries) {
    if (e.kind === "tool_call") {
      const r = results.get(e.tool_call_id);
      if (tool && e.name !== tool) continue;
      if (!q || hit(e) || (r && hit(r))) {
        ids.add(e.id);
        if (r) ids.add(r.id);
      }
    } else if (e.kind === "tool_result") {
      const c = callOf.get(e.tool_call_id);
      if (c) continue; // decided with its call
      if (!tool && hit(e)) ids.add(e.id);
    } else if (!tool && hit(e)) {
      ids.add(e.id);
    }
  }
  return ids;
}

// prettyJSON indents text that parses as JSON and returns anything else unchanged.
export function prettyJSON(text) {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

// counterText renders a token counter; null is unknown, never 0.
export function counterText(n) {
  return n === null || n === undefined ? "unknown" : String(n);
}
