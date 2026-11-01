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

// usageText is the usage totals in the same words as the CLI viewer: all four counters,
// unknown when no call reported one, then the partial, incomplete and errored flags.
export function usageText(u) {
  if (!u.calls) return "no model calls";
  if (u.all_errored) return `no usage (${u.errored} errored)`;
  const n = (c) => counterText(c.value);
  let s = `input ${n(u.input)}, output ${n(u.output)}, cache-write ${n(u.cache_write)}, cache-read ${n(u.cache_read)}`;
  if (u.partial > 0) s += `; partial (${u.partial} calls)`;
  if (u.missing > 0) s += `; incomplete: ${u.missing} of ${u.reporting + u.missing} model calls missing usage`;
  for (const [name, c] of [["input", u.input], ["output", u.output], ["cache-write", u.cache_write], ["cache-read", u.cache_read]]) {
    if (c.unreported > 0) s += `; ${name} incomplete (${c.unreported} calls did not report)`;
  }
  if (u.errored > 0) s += `; ${u.errored} errored`;
  return s;
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

// isRealPrompt is true for a user message with text of its own. A user message that only
// carries tool results has none: it neither starts a turn nor shows a Prompt block.
export function isRealPrompt(e) {
  return e.kind === "user_prompt" && (Boolean((e.text || "").trim()) || Boolean(e.truncated));
}

// groupTurns splits entries into turns, each starting at a real user prompt. Entries before
// the first prompt form a turn of their own.
export function groupTurns(entries) {
  const turns = [];
  for (const e of entries) {
    const real = isRealPrompt(e);
    if (real || turns.length === 0) turns.push({ prompt: real ? e : null, entries: [] });
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

// countText is "1 <one>" for a count of 1, else "<n> <many>".
export function countText(n, one, many) {
  return `${n} ${n === 1 ? one : many}`;
}
