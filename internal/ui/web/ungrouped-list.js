import { timeElement } from "./entries.js";
import { card, compact, counter, loadMore, note, pageBox, pageHead, startText, tableWithHead } from "./sessions-page.js";

// The ungrouped exchanges page (spec 017 AC12): the exchanges no session claimed, in the
// sessions list's column and cards, with labelled counts and glyph-and-word statuses.

// ungroupedRow is one ungrouped exchange as the page shows it: its start, first prompt,
// client, model, status as glyph and word, and its tokens each with its word ("—" where the
// call reported none, never 0).
export function ungroupedRow(x) {
  const u = x.usage || {};
  const failed = (x.errors || 0) > 0;
  return {
    id: x.id,
    at: x.first_at,
    title: x.title || "no prompt captured",
    client: x.client || "unknown",
    model: (x.models || []).join(", ") || "—",
    status: failed ? { key: "failed", glyph: "✗", text: "failed" } : { key: "ok", glyph: "✓", text: "ok" },
    tokens: `${compact(counter(u.input))} in · ${compact(counter(u.output))} out · ${compact(counter(u.cache_read))} cache read`,
  };
}

// ungroupedStats are the page's labelled counts: exchanges (N+ while more pages exist) and
// how many of them failed.
export function ungroupedStats(items, next) {
  const n = items.length;
  const failed = items.filter((x) => (x.errors || 0) > 0).length;
  return [[`${n}${next ? "+" : ""}`, n === 1 && !next ? "exchange" : "exchanges"], [failed, "failed"]];
}

function td(tr, text, cls) {
  const c = document.createElement("td");
  if (cls) c.className = cls;
  if (text !== null) c.textContent = text;
  tr.append(c);
  return c;
}

// createUngroupedPage returns { el, render(data) }; data = { items, next_after, error, onMore }.
export function createUngroupedPage() {
  const el = pageBox("ungrouped-page");
  return {
    el,
    render(data) {
      const items = data.items || [];
      el.replaceChildren(pageHead("Ungrouped exchanges", data.error ? [] : ungroupedStats(items, data.next_after)));
      if (data.error) return el.append(card(note("error", data.error)));
      if (items.length === 0) return el.append(card(note("empty", "No ungrouped exchanges.")));
      const { table, body } = tableWithHead([["Start", "time"], ["Request", "title"], ["Client"], ["Model"], ["Status", "status"], ["Tokens", "tokens"]]);
      for (const x of items) {
        const r = ungroupedRow(x);
        const tr = document.createElement("tr");
        const t = timeElement(r.at);
        t.textContent = startText(r.at);
        td(tr, null, "time").append(t);
        const a = document.createElement("a");
        a.className = "stitle";
        a.href = `#/exchanges/${encodeURIComponent(r.id)}`;
        a.textContent = r.title;
        td(tr, null, "title").append(a);
        td(tr, r.client, "src");
        td(tr, r.model, "src");
        const st = document.createElement("span");
        st.className = `st st-${r.status.key}`;
        st.textContent = `${r.status.glyph} ${r.status.text}`;
        td(tr, null, "status").append(st);
        td(tr, r.tokens, "tokens");
        body.append(tr);
      }
      el.append(card(table));
      const more = loadMore(data, data.onMore);
      if (more) el.append(more);
    },
  };
}
