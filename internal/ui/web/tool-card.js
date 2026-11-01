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
  if (entry.truncated) d.append(truncatedLink(entry));
  return d;
}

// ToolCard renders data = { call, result, prettyInput, isOpen(key), setOpen(key, open) }.
// result is null while none has arrived.
export class ToolCard extends HTMLElement {
  render(data) {
    const { call, result } = data;
    this.className = "tool-card";
    this.replaceChildren();
    const head = document.createElement("div");
    head.className = "tool-head";
    const name = document.createElement("strong");
    name.textContent = call.name || "tool";
    head.append(name);
    const state = document.createElement("span");
    if (!result) {
      state.className = "badge pending";
      state.textContent = "pending";
    } else if (result.is_error === true) {
      state.className = "badge err";
      state.textContent = "error";
      this.classList.add("is-error");
    }
    if (state.textContent) head.append(state);
    this.append(head);
    this.append(section("input", data.prettyInput, call, data.isOpen(`${call.id}:in`), (o) => data.setOpen(`${call.id}:in`, o), "tool-input"));
    if (result) {
      this.append(section("result", result.text || "", result, data.isOpen(`${result.id}:out`), (o) => data.setOpen(`${result.id}:out`, o), "tool-result"));
    }
  }
}

customElements.define("tool-card", ToolCard);
