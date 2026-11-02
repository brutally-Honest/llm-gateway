import { countText, hookLabel, timeElement } from "./entries.js";
import { filterTurns, toolNames } from "./filters.js";
import { agentLabel, buildStats, buildTurns, formatDuration, hooksLabel } from "./model.js";
import { buildRoute } from "./route.js";
import { countedChips, note } from "./sessions-page.js";
import { chipNode, copyButton, toolRowNode, truncatedLink } from "./tool-card.js";

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function button(label, onClick) {
  const b = el("button", "", label);
  b.type = "button";
  b.addEventListener("click", onClick);
  return b;
}

function contextText(c) {
  if (!c || c.value === null) return "—";
  return `${c.partial ? "~" : ""}${c.value}`;
}

// createTimelinePage builds the session page once; render(data) redraws it from
// data = { summary, entries, error, snapshotError }. The toolbar is built once so typing
// keeps focus across live re-renders. All text goes in through textContent.
export function createTimelinePage() {
  const root = el("section", "timeline-page");
  const header = el("section", "session-head");
  const stats = el("div", "stats");
  const bar = el("div", "toolbar");
  const input = el("input");
  input.type = "search";
  input.placeholder = "Filter text";
  input.setAttribute("aria-label", "Filter text");
  const picker = el("select");
  picker.setAttribute("aria-label", "Tool");
  bar.append(input, picker);
  const status = el("div");
  const list = el("div", "timeline");
  root.append(header, stats, bar, status, list);

  const open = new Set(); // section keys and hook toggles that stay open across redraws
  let data = { summary: null, entries: [], error: "", snapshotError: "" };
  let tool = "";
  let built = null;
  let builtFor = null;

  input.addEventListener("input", () => drawList());
  picker.addEventListener("change", () => {
    tool = picker.value;
    drawList();
  });

  const toggle = (key, on) => (on ? open.add(key) : open.delete(key));
  const ctx = { isOpen: (k) => open.has(k), setOpen: toggle };

  function turns() {
    if (builtFor !== data.entries) {
      built = buildTurns(data.entries);
      builtFor = data.entries;
    }
    return built;
  }

  function drawHeader() {
    const s = data.summary;
    header.replaceChildren();
    stats.replaceChildren();
    if (!s) return;
    const row = el("div", "totals");
    row.append(el("strong", "", s.client), el("span", "", (s.models || []).join(", ")));
    const span = el("span", "muted");
    span.append(timeElement(s.first_at), el("span", "", " – "), timeElement(s.last_at));
    row.append(span, el("span", "", countText(s.exchange_count, "exchange", "exchanges")));
    if (s.active) row.append(el("span", "live", "● live"));
    header.append(row);
    const st = buildStats(data.entries, s);
    const line = el("div", "totals");
    line.append(el("span", "", countText(st.calls, "call", "calls")));
    if (st.sideCalls) line.append(el("span", "muted", `${st.sideCalls} side`));
    line.append(el("span", "", `model time ${formatDuration(st.modelTimeMs)}`));
    for (const c of countedChips(st.toolStates)) line.append(chipNode({ ...c, title: `${c.count} ${c.text}` }), el("span", "muted", String(c.count)));
    line.append(el("span", st.errors ? "err-text" : "", countText(st.errors, "error", "errors")));
    line.append(el("span", "", `context ${contextText(st.context)}`));
    stats.append(line);
  }

  function drawPicker(cards) {
    const names = toolNames(cards);
    if (tool && !names.includes(tool)) names.push(tool);
    picker.replaceChildren();
    const all = el("option", "", "All tools");
    all.value = "";
    picker.append(all);
    for (const n of names) {
      const o = el("option", "", n);
      o.value = n;
      picker.append(o);
    }
    picker.value = tool;
  }

  function promptNode(card) {
    const p = card.prompt;
    const d = el("div", "entry prompt");
    const head = el("div", "entry-head");
    head.append(el("span", "muted", "Prompt"));
    if (p.text) head.append(copyButton(() => p.text));
    d.append(head, el("pre", "", p.body));
    if (p.reminders.length > 0) {
      const key = `${card.number}:reminders`;
      const box = el("div", "reminders");
      const draw = () => {
        box.replaceChildren();
        if (open.has(key)) {
          box.append(button("hide reminders", () => { toggle(key, false); draw(); }));
          for (const r of p.reminders) box.append(el("pre", "muted", r));
        } else {
          box.append(button(`+${p.reminders.length} ${p.reminders.length === 1 ? "reminder" : "reminders"}`, () => { toggle(key, true); draw(); }));
        }
      };
      draw();
      d.append(box);
    }
    if (p.truncated) d.append(truncatedLink(p));
    return d;
  }

  function callLink(c, sessionId) {
    const a = el("a", `call-link${c.errored ? " is-error" : ""}`, c.label);
    a.href = buildRoute({ name: "session", id: sessionId, call: c.exchangeId });
    const box = el("span", "call");
    box.append(a);
    if (c.agent) box.append(el("span", "badge harness", c.agent));
    if (c.errored) box.append(el("span", "badge err", `✗ ${c.errorWord}`));
    return box;
  }

  function hooksNode(card) {
    const box = el("div", "hooks");
    const key = `${card.kind}:${card.number}:${card.start}:hooks`;
    const draw = () => {
      box.replaceChildren();
      const b = button(hooksLabel(card.hooks.length), () => { toggle(key, !open.has(key)); draw(); });
      b.setAttribute("aria-expanded", String(open.has(key)));
      box.append(b);
      if (!open.has(key)) return;
      for (const h of card.hooks) {
        const line = el("div", "entry hook-event muted", hookLabel(h));
        const agent = agentLabel(h);
        if (agent) line.append(el("span", "badge harness", agent));
        box.append(line);
      }
    };
    draw();
    return box;
  }

  function cardNode(card, sessionId, hooksOnlyNote) {
    const box = el("section", `turn card-${card.kind}${card.errored ? " is-error" : ""}`);
    const head = el("h2", "turn-head", card.heading);
    if (card.errored) head.append(el("span", "badge err", "✗ error"));
    box.append(head);
    if (card.prompt) box.append(promptNode(card));
    if (card.calls.length > 0) {
      const links = el("div", "calls");
      for (const c of card.calls) links.append(callLink(c, sessionId));
      box.append(links);
    }
    for (const t of card.tools) box.append(toolRowNode(t, ctx));
    for (const text of card.texts) {
      const d = el("div", "entry assistant");
      d.append(el("pre", "", text));
      box.append(d);
    }
    if (card.hooksOnly && hooksOnlyNote) box.append(el("p", "muted", hooksOnlyNote));
    if (card.hooks.length > 0) box.append(hooksNode(card));
    return box;
  }

  function drawList() {
    list.replaceChildren();
    if (data.entries.length === 0) {
      if (!data.error) list.append(note("empty", "No entries yet."));
      return;
    }
    const { cards, note: hooksNote } = turns();
    const shown = filterTurns(cards, { text: input.value, tool });
    const sessionId = data.summary ? data.summary.id : "";
    for (const card of shown) list.append(cardNode(card, sessionId, hooksNote));
    if (shown.length === 0) list.append(note("empty", "No entries match the filter."));
  }

  return {
    el: root,
    render(next) {
      data = next;
      status.replaceChildren();
      if (data.error) status.append(note("error", data.error));
      if (data.snapshotError) status.append(note("error", `Live snapshot failed: ${data.snapshotError}`));
      drawHeader();
      drawPicker(turns().cards);
      drawList();
    },
  };
}
