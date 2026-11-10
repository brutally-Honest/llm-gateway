import { getJSON } from "./api.js";
import { effectsListNode } from "./drawer-sections.js";
import { COVERAGE, countsText, placeEffects, toolNumbers } from "./effects.js";
import { countText, hookLabel, timeElement } from "./entries.js";
import { filterTurns, toolNames } from "./filters.js";
import { createDrawer } from "./drawer.js";
import { turnTarget } from "./keys.js";
import { agentLabel, buildStats, buildTurns, callNumbers, contextPlot, formatDuration, hooksLabel, railItems, stripBars } from "./model.js";
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

// bucketSig names what an effects bucket holds; effects only ever append, so the count and
// the last id tell a changed bucket from an unchanged one.
function bucketSig(b) {
  const n = b.items.length;
  return `${n}:${n ? b.items[n - 1].id : 0}`;
}

// cardSig names everything a card shows except its effects, which redraw on their own
// (patchEffects): two cards with the same signature draw the same element.
function cardSig(card) {
  return JSON.stringify({ ...card, effects: undefined });
}

// reconcile makes parent's children exactly `want`, in order, touching only what differs:
// a child not wanted is removed, a wanted one missing or out of place is inserted, and a
// child already in place is left alone.
function reconcile(parent, want) {
  const keep = new Set(want);
  for (const c of [...parent.children]) if (!keep.has(c)) c.remove();
  want.forEach((node, i) => {
    if (parent.children[i] !== node) parent.insertBefore(node, parent.children[i] || null);
  });
}

function contextText(c) {
  if (!c || c.value === null) return "—";
  return `${c.partial ? "~" : ""}${c.value}`;
}

// callContextText is the context beside a call link: its own part after a separator,
// with digit grouping, so it never runs into the duration (F8).
function callContextText(c) {
  if (!c || c.value === null) return " · ctx —";
  return ` · ctx ${c.partial ? "~" : ""}${c.value.toLocaleString("en-US")}`;
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
  const fxRows = new Map(); // effects key -> { sig, update(bucket) } of each drawn effects row
  let drawnCards = []; // { card, node } of each card in the list, in order
  let keyed = new Map(); // "kind:number" -> { sig, node, head } of each card in the list
  const heads = new WeakMap(); // card element -> its heading
  let noMatchNode = null;
  const noMatch = () => (noMatchNode ||= note("empty", "No entries match the filter."));
  let data = { summary: null, entries: [], effects: [], error: "", snapshotError: "" };
  let tool = "";
  let built = null;
  let builtFor = null;
  let drawn = { index: 0, total: 0 };

  input.addEventListener("input", () => drawList());
  // A cleared filter brings every turn back with the current one in view, not turn 1. Esc
  // clears it in every browser; Chrome clears a search box on Esc itself and fires
  // `search`, not `input` (F4).
  const unfiltered = () => {
    drawList();
    markCurrent(true);
  };
  input.addEventListener("search", () => {
    if (input.value === "") unfiltered();
    else drawList();
  });
  input.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" || input.value === "") return;
    input.value = "";
    unfiltered();
  });
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
      // the glyph and the word, as on the turn heading, so colour is never the only sign (AC34)
      if (it.errored) meta.append(el("span", "badge err", "✗ error"));
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
    // the badge beside the link shows the error; the link's own name says it too (AC34)
    if (c.errored) a.setAttribute("aria-label", `${c.label}, ✗ ${c.errorWord}`);
    a.href = buildRoute({ name: "session", id: sessionId, call: c.exchangeId });
    const box = el("span", "call");
    a.addEventListener("click", () => drawer.noteOpener(a));
    box.append(a);
    if (opts.context) box.append(el("span", "muted call-context", callContextText(c.context)));
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

  // toolNos maps each tool call id to its model call's #n, rebuilt when the entries change
  // (spec 015: the attribution text names "#n Bash").
  let nosFor = null;
  let nos = new Map();
  const toolNos = () => {
    if (nosFor !== data.entries) {
      nos = toolNumbers(data.entries, callNumbers(data.entries));
      nosFor = data.entries;
    }
    return nos;
  };

  // effectsRow is one folded row of a turn's effects, with its counts; it expands to the
  // files, then the connections (AC14). Only its own box redraws on a toggle, and on a live
  // effects update that changes its bucket (fxRows).
  function effectsRow(key, b) {
    const box = el("div", "effects");
    let bucket = b;
    const draw = () => {
      box.replaceChildren();
      const shown = open.has(key);
      const t = button(`${shown ? "▾" : "▸"} ${countsText(bucket)}`, () => { toggle(key, !open.has(key)); draw(); });
      t.className = "effects-toggle";
      t.title = COVERAGE;
      t.setAttribute("aria-expanded", String(shown));
      box.append(t);
      if (shown) box.append(effectsListNode(bucket.items, { isOpen: (k) => open.has(k), setOpen: toggle, getJSON, redraw: draw, toolNos: toolNos() }));
    };
    draw();
    fxRows.set(key, {
      box,
      sig: bucketSig(b),
      update(next) {
        bucket = next;
        this.sig = bucketSig(next);
        draw();
      },
    });
    return box;
  }

  function cardNode(card, sessionId, hooksOnlyNote) {
    const box = el("section", `turn card-${card.kind}${card.errored ? " is-error" : ""}`);
    const head = el("h2", "turn-head", card.heading);
    head.setAttribute("tabindex", "-1");
    heads.set(box, head);
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
    if (card.effects && card.effects.items.length > 0) box.append(effectsRow(effectsKey(card), card.effects));
    return box;
  }

  const effectsKey = (card) => `effects:${card.kind}:${card.number}`;

  // placed puts the session's effects in its turns; effects before the first prompt go to
  // the "before" card, or to a row of their own when the session has none.
  function placed(cards) {
    const tc = cards.filter((c) => c.kind === "turn");
    const p = placeEffects(tc, data.effects || []);
    const before = cards.find((c) => c.kind === "before");
    for (const c of cards) c.effects = null;
    tc.forEach((c, i) => { c.effects = p.turns[i]; });
    if (before) before.effects = p.before;
    return before ? null : p.before;
  }

  // drawList brings the list up to date with keyed updates: each card is keyed by its kind
  // and number and carries a signature of what it shows. A card whose signature is unchanged
  // keeps its element (and so its expanded rows, focus and scroll); a new card is added and
  // a changed one replaced; then only the effects rows whose effects changed redraw (AC14).
  function drawList() {
    drawnCards = [];
    cardEls.clear();
    headEls.clear();
    if (data.entries.length === 0) {
      keyed.clear();
      fxRows.clear();
      const early = placeEffects([], data.effects || []).before;
      if (early.items.length > 0) list.replaceChildren(effectsRow("effects:before", early));
      else if (!data.error && !data.building) list.replaceChildren(note("empty", "No entries yet."));
      else list.replaceChildren();
      return;
    }
    const { cards, note: hooksNote } = turns();
    const early = placed(cards);
    let shown = filterTurns(cards, { text: input.value, tool });
    if (opts.errorsOnly) shown = shown.filter((c) => c.errored);
    const sessionId = data.summary ? data.summary.id : "";
    const ctxSig = JSON.stringify([opts.hooks, opts.reminders, opts.context, sessionId, hooksNote]);
    const want = [];
    if (early && early.items.length > 0) {
      const row = fxRows.get("effects:before");
      want.push(row && row.box.parentNode === list ? row.box : effectsRow("effects:before", early));
    }
    const next = new Map();
    for (const card of shown) {
      const key = `${card.kind}:${card.number}`;
      const sig = `${ctxSig}|${cardSig(card)}`;
      let k = keyed.get(key);
      if (!k || k.sig !== sig) {
        const node = cardNode(card, sessionId, hooksNote);
        k = { sig, node, head: heads.get(node) };
      }
      next.set(key, k);
      if (card.kind === "turn") {
        cardEls.set(card.number, k.node);
        headEls.set(card.number, k.head);
      }
      drawnCards.push({ card, node: k.node });
      want.push(k.node);
    }
    keyed = next;
    if (shown.length === 0) want.push(noMatch());
    reconcile(list, want);
    patchEffects();
    markCurrent(false);
  }

  // patchEffects places the effects again and redraws only the effects rows whose bucket
  // changed; a turn that gains its first effect gets a row appended. Every other element
  // of the list stays as it is, and an expanded row stays open (AC14).
  function patchEffects() {
    if (data.entries.length === 0) {
      drawList(); // no turns to keep
      return;
    }
    const { cards } = turns();
    const early = placed(cards);
    // the drawn cards may be filtered copies; the buckets were placed on the turns' own cards
    const buckets = new Map(cards.map((c) => [effectsKey(c), c.effects]));
    const rows = drawnCards.map(({ card, node }) => [effectsKey(card), buckets.get(effectsKey(card)), node, (row) => node.append(row)]);
    if (early) rows.unshift(["effects:before", early, list, (row) => list.prepend(row)]);
    for (const [key, b, parent, insert] of rows) {
      if (!b || b.items.length === 0) continue;
      const row = fxRows.get(key);
      // a row left in a replaced card's old element is not this card's row
      if (!row || row.box.parentNode !== parent) insert(effectsRow(key, b));
      else if (row.sig !== bucketSig(b)) row.update(b);
    }
  }

  return {
    el: root,
    render(next) {
      data = next;
      status.replaceChildren();
      if (data.building) status.append(note("empty", "building session…"));
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
    // setEffects shows the session's effects; only the effects rows that changed redraw.
    setEffects(effects) {
      data = { ...data, effects };
      patchEffects();
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
