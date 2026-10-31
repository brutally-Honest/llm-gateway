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
