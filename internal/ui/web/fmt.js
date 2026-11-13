// Formatting only (spec 016): every fact on the session page comes from the server's view;
// these turn its numbers and times into text, as `gateway timeline` does.

// dur renders milliseconds as 0.4s, 32s, 2m 05s or 1h 03m; "—" when unknown.
export function dur(ms) {
  if (ms === null || ms === undefined) return "—";
  const s = ms / 1000;
  if (s < 10) return `${s.toFixed(1)}s`;
  if (s < 60) return `${Math.round(s)}s`;
  const total = Math.round(s);
  const m = Math.floor(total / 60);
  const pad = (n) => String(n).padStart(2, "0");
  if (m < 60) return `${m}m ${pad(total % 60)}s`;
  return `${Math.floor(m / 60)}h ${pad(m % 60)}m`;
}

// clock renders an RFC 3339 instant as local HH:MM:SS (or HH:MM).
export function clock(iso, seconds = true) {
  const d = new Date(iso || "");
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n) => String(n).padStart(2, "0");
  return `${pad(d.getHours())}:${pad(d.getMinutes())}${seconds ? `:${pad(d.getSeconds())}` : ""}`;
}

// num renders a count with digit grouping; "—" when unknown, never 0.
export const num = (n) => (n === null || n === undefined ? "—" : Number(n).toLocaleString("en-US"));

// signed renders a change with its sign; "—" when unknown.
export const signed = (n) => (n === null || n === undefined ? "—" : `${n >= 0 ? "+" : "−"}${num(Math.abs(n))}`);

// bytes renders a size in B, KB or MB; "—" when unknown.
export function bytes(n) {
  if (n === null || n === undefined) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

// plural is "1 file" or "3 files".
export const plural = (n, one, many = `${one}s`) => `${n ?? 0} ${n === 1 ? one : many}`;

// el builds an element; all text goes in through textContent, never as HTML.
export function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined && text !== null) e.textContent = String(text);
  return e;
}

// button builds a real button that runs onClick.
export function button(cls, text, onClick) {
  const b = el("button", cls, text);
  b.type = "button";
  if (onClick) b.addEventListener("click", onClick);
  return b;
}

// mark is a status as glyph and word ({ glyph, word } from the view); the glyph alone
// when alone is set (an ok tool line).
export const markText = (m, alone = false) => (m ? (alone ? m.glyph : `${m.glyph} ${m.word}`) : "");
