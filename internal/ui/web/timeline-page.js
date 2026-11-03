import { countText, hookLabel, timeElement } from "./entries.js";
import { filterTurns, toolNames } from "./filters.js";
import { createDrawer } from "./drawer.js";
import { turnTarget } from "./keys.js";
import { agentLabel, buildStats, buildTurns, contextPlot, formatDuration, hooksLabel, railItems, stripBars } from "./model.js";
import { fitContextPlot } from "./plot.js";
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
export function createTimelinePage({ nav, loadDetail } = {}) {
  const go = nav || { push() {}, replace() {}, back() {} };
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
  const strip = el("div", "strip");
  const ctxLine = el("div", "context-line");
  const rail = el("nav", "rail");
  rail.setAttribute("aria-label", "Turns");
  const optionsBox = el("div", "show-options");
  const railList = el("div", "rail-list");
  rail.append(optionsBox, railList);
  const main = el("div", "timeline-main");
  main.append(bar, status, list);
  const layout = el("div", "timeline-layout");
  layout.append(main, rail); // main first so the filter box leads the tab order; CSS puts the rail left
  const drawer = createDrawer({ nav: go, loadDetail, turnHeading: () => headEls.get(turnCards()[current] && turnCards()[current].number) });
  root.append(header, stats, strip, ctxLine, layout, drawer.el);

  // Show options apply at once (AC16); context per call starts on.
  const opts = { hooks: false, reminders: false, context: true, errorsOnly: false };
  const OPTION_LABELS = [
    ["hooks", "Hooks inline"], ["reminders", "System reminders"],
    ["context", "Context per call"], ["errorsOnly", "Only turns with errors"],
  ];
  const optionBoxes = new Map();
  for (const [name, label] of OPTION_LABELS) {
    const row = el("label", "option");
    const box = el("input");
    box.type = "checkbox";
    box.checked = opts[name];
    box.addEventListener("change", () => setOption(name, Boolean(box.checked)));
    optionBoxes.set(name, box);
    row.append(box, el("span", "", label));
    optionsBox.append(row);
  }
  let current = 0; // index among the numbered turns
  const cardEls = new Map(); // turn number -> its card element
  const railEls = new Map();
  const headEls = new Map(); // turn number -> its heading, where focus falls back to

  const open = new Set(); // section keys and hook toggles that stay open across redraws
  let data = { summary: null, entries: [], error: "", snapshotError: "" };
  let tool = "";
  let built = null;
  let builtFor = null;
  let drawn = { index: 0, total: 0 };

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

  function setOption(name, on) {
    opts[name] = on;
    optionBoxes.get(name).checked = on;
    drawList();
  }

  function turnCards() {
    return turns().cards.filter((c) => c.kind === "turn");
  }

  // markCurrent marks the current turn in the rail and the list, and scrolls to it.
  function markCurrent(scroll) {
    for (const [n, e] of railEls) e.setAttribute("aria-current", String(n === current + 1));
    const tc = turnCards();
    const card = tc[current];
    for (const [n, e] of cardEls) {
      if (card && n === card.number) e.classList.add("is-current");
      else e.classList.remove("is-current");
    }
    const target = card && cardEls.get(card.number);
    if (scroll && target && typeof target.scrollIntoView === "function") target.scrollIntoView({ block: "start" });
  }

  function select(index) {
    const n = turnCards().length;
    if (n === 0) return;
    current = Math.max(0, Math.min(n - 1, index));
    markCurrent(true);
  }

  function drawRail() {
    railList.replaceChildren();
    railEls.clear();
    const items = railItems(turns().cards);
    if (current >= items.length) current = Math.max(0, items.length - 1);
    for (const it of items) {
      const b = el("button", `rail-item${it.errored ? " is-error" : ""}`);
      b.type = "button";
      b.append(el("strong", "", `${it.number}`), el("span", "rail-prompt", ` ${it.prompt}`));
      const meta = el("span", "rail-meta muted");
      for (const c of it.chips) meta.append(chipNode({ ...c, title: `${c.count} ${c.text}` }), el("span", "", String(c.count)));
      meta.append(el("span", "", countText(it.calls, "call", "calls")), el("span", "", it.duration));
      b.append(meta);
      b.addEventListener("click", () => select(it.number - 1));
      railEls.set(it.number, b);
      railList.append(b);
    }
  }

  function drawStrip() {
    strip.replaceChildren();
    const sessionId = data.summary ? data.summary.id : "";
    for (const b of stripBars(data.entries)) {
      const a = el("a", `strip-bar${b.muted ? " is-muted" : ""}${b.errored ? " is-error" : ""}${b.turnStart ? " turn-start" : ""}`);
      a.href = buildRoute({ name: "session", id: sessionId, call: b.exchangeId });
      a.title = b.title;
      a.addEventListener("click", () => drawer.noteOpener(a));
      a.setAttribute("aria-label", b.title);
      a.style.width = `${b.width}px`;
      if (b.errored) a.append(el("span", "strip-mark", "✗"));
      strip.append(a);
    }
  }

  function drawContext() {
    ctxLine.replaceChildren();
    if (data.entries.length === 0) return;
    const plot = contextPlot(data.entries);
    if (plot.note) {
      ctxLine.append(note("muted", plot.note));
      return;
    }
    ctxLine.append(fitContextPlot((width) => contextPlot(data.entries, width)));
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
        if (opts.reminders) {
          for (const r of p.reminders) box.append(el("pre", "muted", r));
        } else if (open.has(key)) {
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
    a.addEventListener("click", () => drawer.noteOpener(a));
    box.append(a);
    if (opts.context) box.append(el("span", "muted call-context", `context ${contextText(c.context)}`));
    if (c.agent) box.append(el("span", "badge harness", c.agent));
    if (c.errored) box.append(el("span", "badge err", `✗ ${c.errorWord}`));
    return box;
  }

  function hooksNode(card) {
    const box = el("div", "hooks");
    const key = `${card.kind}:${card.number}:${card.start}:hooks`;
    const draw = () => {
      box.replaceChildren();
      const shown = opts.hooks || open.has(key);
      const b = button(hooksLabel(card.hooks.length), () => { toggle(key, !open.has(key)); draw(); });
      b.setAttribute("aria-expanded", String(shown));
      if (!opts.hooks) box.append(b);
      if (!shown) return;
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
    head.setAttribute("tabindex", "-1");
    if (card.kind === "turn") headEls.set(card.number, head);
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
    let shown = filterTurns(cards, { text: input.value, tool });
    if (opts.errorsOnly) shown = shown.filter((c) => c.errored);
    const sessionId = data.summary ? data.summary.id : "";
    cardEls.clear();
    headEls.clear();
    for (const card of shown) {
      const node = cardNode(card, sessionId, hooksNote);
      if (card.kind === "turn") cardEls.set(card.number, node);
      list.append(node);
    }
    markCurrent(false);
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
      drawRail();
      drawStrip();
      drawContext();
      drawList();
      drawn = drawer.sync({ sessionId: data.summary ? data.summary.id : "", callId: data.callId || null, entries: data.entries, loaded: Boolean(data.loaded) });
      if (drawn.missing) status.append(note("error", "No such call in this session."));
    },
    drawerOpen: () => drawer.isOpen(),
    stepCall: (delta) => drawer.step(delta),
    closeDrawer: () => drawer.close(),
    // backToTurn closes the drawer and makes the call's turn the current one.
    backToTurn() {
      const id = drawer.callId();
      const bar = stripBars(data.entries).find((b) => b.exchangeId === id);
      if (bar && bar.turn) {
        const i = turnCards().findIndex((c) => c.number === bar.turn);
        if (i >= 0) current = i;
      }
      drawer.close();
    },
    // position is the key bar position: the current turn among the numbered turns.
    position() {
      if (drawer.isOpen()) return { index: drawn.index, total: drawn.total };
      const total = turnCards().length;
      return { index: total ? current + 1 : 0, total };
    },
    moveTurn(delta) {
      select(current + delta);
    },
    nextError() {
      const i = turnTarget(turnCards(), current, "error");
      if (i >= 0) select(i);
    },
    nextTool() {
      const i = turnTarget(turnCards(), current, "tool");
      if (i >= 0) select(i);
    },
    // firstCallId is the exchange id of the current turn's first call, or "".
    firstCallId() {
      const card = turnCards()[current];
      return card && card.calls.length ? card.calls[0].exchangeId : "";
    },
    // openFirstCall opens the drawer on the current turn's first call, from its heading.
    openFirstCall() {
      const id = this.firstCallId();
      const card = turnCards()[current];
      if (id) drawer.openCall(id, headEls.get(card.number));
    },
    toggleHooks() {
      setOption("hooks", !opts.hooks);
    },
  };
}
