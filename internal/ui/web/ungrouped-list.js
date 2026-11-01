import { timeElement, usageText } from "./entries.js";
import { loadMore, note, tableWithHead } from "./session-list.js";

// UngroupedList renders data = { items, next_after, error, onMore }.
export class UngroupedList extends HTMLElement {
  render(data) {
    this.replaceChildren();
    if (data.error) return this.append(note("error", data.error));
    if (data.items.length === 0) return this.append(note("empty", "No ungrouped exchanges."));
    const { table, body } = tableWithHead([["Time"], ["Client"], ["Models"], ["Tokens", "usage"], ["Errors", "num"]]);
    for (const x of data.items) {
      const tr = document.createElement("tr");
      const t = document.createElement("td");
      t.className = "time";
      const a = document.createElement("a");
      a.href = `#/exchanges/${encodeURIComponent(x.id)}`;
      a.append(timeElement(x.first_at));
      t.append(a);
      tr.append(t);
      for (const [text, cls] of [[x.client], [x.models.join(", ")], [usageText(x.usage), "usage"], [String(x.errors), "num"]]) {
        const td = document.createElement("td");
        if (cls) td.className = cls;
        td.textContent = text;
        tr.append(td);
      }
      body.append(tr);
    }
    this.append(table);
    const more = loadMore(data, data.onMore);
    if (more) this.append(more);
  }
}

customElements.define("ungrouped-list", UngroupedList);
