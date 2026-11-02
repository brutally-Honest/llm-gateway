import { hookInputEntry, omittedNote, prettyJSON } from "./entries.js";

// copyButton copies getText() to the clipboard.
export function copyButton(getText) {
  const b = document.createElement("button");
  b.className = "copy";
  b.textContent = "Copy";
  b.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(getText());
      b.textContent = "Copied";
    } catch {
      b.textContent = "Copy failed";
    }
    setTimeout(() => (b.textContent = "Copy"), 1500);
  });
  return b;
}

// truncatedLink points a truncated entry at its exchange's detail.
export function truncatedLink(entry) {
  const a = document.createElement("a");
  a.className = "truncated";
  a.href = `#/exchanges/${encodeURIComponent(entry.exchange_id)}`;
  a.textContent = "truncated — open raw";
  return a;
}

// section renders a labelled, copyable block. entry is the exchange entry whose raw link
// it shows when truncated; null shows no link (the hook input has no detail page).
function section(label, text, entry, open, onToggle, cls) {
  const d = document.createElement("details");
  d.className = cls;
  d.open = open;
  d.addEventListener("toggle", () => onToggle(d.open));
  const s = document.createElement("summary");
  s.textContent = label;
  const pre = document.createElement("pre");
  pre.textContent = text;
  d.append(s, copyButton(() => text), pre);
  if (entry && entry.truncated) d.append(truncatedLink(entry));
  return d;
}

function span(cls, text, title) {
  const s = document.createElement("span");
  s.className = cls;
  s.textContent = text;
  if (title) s.title = title;
  return s;
}

// chipNode is a chip as glyph plus text; the hover title carries the full state, reason
// and outcome, and the label keeps it readable without hover.
export function chipNode(chip) {
  const s = span(`chip chip-${chip.key}`, `${chip.glyph} ${chip.text}`, chip.title);
  s.setAttribute("aria-label", `${chip.text}: ${chip.title}`);
  return s;
}

// toolRowNode renders one tool row: chip, name, agent label, error mark, and the pretty
// input and result on expansion, each copyable. ctx = { isOpen(key), setOpen(key, open) }.
// A hook-seen tool has no call, so it has no input or result to open.
export function toolRowNode(row, ctx) {
  const box = document.createElement("div");
  box.className = "tool-row";
  const head = document.createElement("div");
  head.className = "tool-head";
  head.append(chipNode(row.chip), span("", row.name || "tool"));
  if (row.agent) head.append(span("badge harness", row.agent));
  if (row.resultError) {
    box.classList.add("is-error");
    head.append(span("badge err", "✗ error"));
  }
  box.append(head);
  const call = row.call;
  if (!call) return box;
  if (call.input_differs) {
    const hook = section("hook input", call.hook_input || "", null, ctx.isOpen(`${call.id}:hook`), (o) => ctx.setOpen(`${call.id}:hook`, o), "tool-input");
    // the note follows the hook input's own truncation; it shows no raw link
    if (hookInputEntry(call).truncated) hook.append(span("muted", omittedNote(call.hook_omitted_bytes)));
    box.append(hook);
  }
  box.append(section("input", prettyJSON(call.text || ""), call, ctx.isOpen(`${call.id}:in`), (o) => ctx.setOpen(`${call.id}:in`, o), "tool-input"));
  if (row.result) {
    const r = row.result;
    box.append(section("result", r.text || "", r, ctx.isOpen(`${r.id}:out`), (o) => ctx.setOpen(`${r.id}:out`, o), "tool-result"));
  }
  return box;
}
