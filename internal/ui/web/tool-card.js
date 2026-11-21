// copyButton is an icon button that copies getText() to the clipboard (027 C4). The icon is
// drawn by the stylesheet from data-state; its label and tooltip say "Copy", then "Copied"
// or "Copy failed" for a moment.
export function copyButton(getText) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = "copy";
  const say = (state, word) => {
    b.dataset.state = state;
    b.title = word;
    b.setAttribute("aria-label", word);
  };
  say("idle", "Copy");
  b.addEventListener("click", async () => {
    try {
      await globalThis.navigator.clipboard.writeText(getText());
      say("copied", "Copied");
    } catch {
      say("failed", "Copy failed");
    }
    setTimeout(() => say("idle", "Copy"), 1500);
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
