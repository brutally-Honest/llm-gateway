import { countText, groupTurns, isRealPrompt, matchIds, pairTools, prettyJSON, timeElement, usageText } from "./entries.js";
import "./model-chip.js";
import { note } from "./session-list.js";
import { copyButton, truncatedLink } from "./tool-card.js";
import "./tool-card.js";

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function textBlock(entry, label, cls) {
  const d = el("div", `entry ${cls}`);
  const head = el("div", "entry-head");
  head.append(el("span", "muted", label));
  if (entry.text) head.append(copyButton(() => entry.text));
  d.append(head, el("pre", "", entry.text || ""));
  if (entry.truncated) d.append(truncatedLink(entry));
  return d;
}

// SessionView renders data = { summary, entries, error, snapshotError }. The toolbar is
// built once so typing keeps focus across live re-renders.
export class SessionView extends HTMLElement {
  constructor() {
    super();
    this.open = new Set();
    this.text = "";
    this.tool = "";
  }

  build() {
    this.header = el("section", "session-head");
    const bar = el("div", "toolbar");
    this.input = el("input");
    this.input.type = "search";
    this.input.placeholder = "Filter text";
    this.input.addEventListener("input", () => {
      this.text = this.input.value;
      this.drawList();
    });
    this.picker = el("select");
    this.picker.addEventListener("change", () => {
      this.tool = this.picker.value;
      this.drawList();
    });
    bar.append(this.input, this.picker);
    this.status = el("div");
    this.list = el("div", "timeline");
    this.append(this.header, bar, this.status, this.list);
  }

  render(data) {
    if (!this.header) this.build();
    this.data = data;
    this.status.replaceChildren();
    if (data.error) this.status.append(note("error", data.error));
    if (data.snapshotError) this.status.append(note("error", `Live snapshot failed: ${data.snapshotError}`));
    this.drawHeader();
    this.drawPicker();
    this.drawList();
  }

  drawHeader() {
    const s = this.data.summary;
    this.header.replaceChildren();
    if (!s) return;
    const row = el("div", "totals");
    row.append(el("strong", "", s.client));
    row.append(el("span", "", s.models.join(", ")));
    const span = el("span", "muted");
    span.append(timeElement(s.first_at), " – ", timeElement(s.last_at));
    row.append(span);
    row.append(el("span", "", countText(s.exchange_count, "exchange", "exchanges")));
    row.append(el("span", "", countText(s.model_calls, "model call", "model calls")));
    row.append(el("span", "", countText(s.tool_calls, "tool call", "tool calls")));
    row.append(el("span", "", countText(s.errors, "error", "errors")));
    row.append(el("span", "", `tokens ${usageText(s.usage)}`));
    if (s.active) row.append(el("span", "badge", "active"));
    this.header.append(row);
  }

  drawPicker() {
    const names = [...new Set(this.data.entries.filter((e) => e.kind === "tool_call" && e.name).map((e) => e.name))].sort();
    if (this.tool && !names.includes(this.tool)) names.push(this.tool);
    this.picker.replaceChildren();
    const all = el("option", "", "All tools");
    all.value = "";
    this.picker.append(all);
    for (const n of names) {
      const o = el("option", "", n);
      o.value = n;
      this.picker.append(o);
    }
    this.picker.value = this.tool;
  }

  setOpen(key, open) {
    if (open) this.open.add(key);
    else this.open.delete(key);
  }

  drawList() {
    const entries = this.data.entries;
    this.list.replaceChildren();
    if (entries.length === 0) {
      if (!this.data.error) this.list.append(note("empty", "No entries yet."));
      return;
    }
    const keep = matchIds(entries, this.text, this.tool);
    const results = pairTools(entries);
    const called = new Set(entries.filter((e) => e.kind === "tool_call" && e.tool_call_id).map((e) => e.tool_call_id));
    let shown = 0;
    for (const turn of groupTurns(entries)) {
      const box = el("section", "turn");
      for (const e of turn.entries) {
        if (keep && !keep.has(e.id)) continue;
        if (e.kind === "tool_result" && called.has(e.tool_call_id)) continue; // shown in its call's card
        const node = this.entryNode(e, results);
        if (node) box.append(node);
      }
      if (box.childElementCount > 0) {
        this.list.append(box);
        shown++;
      }
    }
    if (shown === 0) this.list.append(note("empty", "No entries match the filter."));
  }

  entryNode(e, results) {
    switch (e.kind) {
      case "user_prompt":
        if (!isRealPrompt(e)) return null; // only tool results: no Prompt block
        return textBlock(e, "Prompt", "prompt");
      case "assistant_text":
        return textBlock(e, "Assistant", "assistant");
      case "message":
        return textBlock(e, e.role || "message", "message");
      case "thinking": {
        const d = el("details", "entry thinking");
        d.open = this.open.has(`${e.id}:think`);
        d.addEventListener("toggle", () => this.setOpen(`${e.id}:think`, d.open));
        d.append(el("summary", "", "thinking"), el("pre", "", e.text || ""));
        if (e.truncated) d.append(truncatedLink(e));
        return d;
      }
      case "tool_call": {
        const card = document.createElement("tool-card");
        card.render({
          call: e,
          result: e.tool_call_id ? results.get(e.tool_call_id) || null : null,
          prettyInput: prettyJSON(e.text || ""),
          isOpen: (k) => this.open.has(k),
          setOpen: (k, o) => this.setOpen(k, o),
        });
        return card;
      }
      case "tool_result": {
        const card = document.createElement("tool-card");
        card.render({
          call: { id: e.id, name: "result without call", text: "", truncated: false },
          result: e,
          prettyInput: "",
          isOpen: (k) => this.open.has(k),
          setOpen: (k, o) => this.setOpen(k, o),
        });
        return card;
      }
      case "model_call": {
        const chip = document.createElement("model-chip");
        chip.render(e);
        return chip;
      }
      case "error": {
        const d = textBlock(e, `Error${e.name ? ` ${e.name}` : ""}${e.status ? ` (${e.status})` : ""}`, "error-entry");
        return d;
      }
      case "marker":
        return el("div", "entry marker muted", e.text || "");
      case "exchange":
        return el("div", "entry muted", `${e.method || ""} ${e.path || ""} ${e.status ?? ""}`.trim());
      default:
        return null;
    }
  }
}

customElements.define("session-view", SessionView);
