import { timeElement, usageText } from "./entries.js";

function cell(tr, text, cls) {
  const td = document.createElement("td");
  if (cls) td.className = cls;
  td.textContent = text;
  tr.append(td);
  return td;
}

function loadMore(data, onMore) {
  if (!data.next_after) return null;
  const b = document.createElement("button");
  b.textContent = "Load more";
  b.addEventListener("click", () => onMore());
  return b;
}

// SessionList renders data = { items, next_after, error, onMore }.
export class SessionList extends HTMLElement {
  render(data) {
    this.replaceChildren();
    if (data.error) return this.append(note("error", data.error));
    if (data.items.length === 0) return this.append(note("empty", "No sessions captured yet."));
    const { table, body } = tableWithHead([
      ["Client"], ["Models"], ["First"], ["Last"], ["Exchanges", "num"], ["Model calls", "num"],
      ["Tool calls", "num"], ["Errors", "num"], ["Tokens", "num"], [""],
    ]);
    for (const s of data.items) {
      const tr = document.createElement("tr");
      cell(tr, s.client);
      cell(tr, s.models.join(", "));
      for (const iso of [s.first_at, s.last_at]) {
        const td = document.createElement("td");
        td.className = "time";
        td.append(timeElement(iso));
        tr.append(td);
      }
      cell(tr, String(s.exchange_count), "num");
      cell(tr, String(s.model_calls), "num");
      cell(tr, String(s.tool_calls), "num");
      cell(tr, String(s.errors), "num");
      cell(tr, usageText(s.usage), "num");
      const td = cell(tr, "");
      if (s.active) {
        const b = document.createElement("span");
        b.className = "badge";
        b.textContent = "active";
        td.append(b);
      }
      body.append(tr);
    }
    this.append(table);
    const more = loadMore(data, data.onMore);
    if (more) this.append(more);
  }
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

export { loadMore };
customElements.define("session-list", SessionList);
