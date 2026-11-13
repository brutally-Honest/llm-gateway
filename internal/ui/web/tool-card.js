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
