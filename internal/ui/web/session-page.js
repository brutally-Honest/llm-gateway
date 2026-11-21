import { button, clock, dur, el, num, plural, skeleton } from "./fmt.js";
import { LABEL_LONG, totalCost } from "./cost.js";
import { createDialog } from "./dialog.js";
import { createSideCard } from "./side-card.js";
import { betweenNode, reveal, setTextMode, textMode, turnNode } from "./story.js";

// The session page (spec 016, laid out as chapters in spec 027 C4): one page that scrolls,
// the turns as contents on the left (they stay in view), then the overview and the tabs
// Story · Files · Commands · Sites · Context, the story being the turns as chapters; one
// details dialog. It renders the server's view (GET /api/sessions/{id}/view and the
// stream's view events) and computes nothing: every count, time, label and place comes
// from the view.

// reconcile makes parent's children exactly `want`, in order, touching only what differs.
function reconcile(parent, want) {
  const keep = new Set(want);
  for (const c of [...parent.children]) if (!keep.has(c)) c.remove();
  want.forEach((node, i) => {
    if (parent.children[i] !== node) parent.insertBefore(node, parent.children[i] || null);
  });
}

// turnId and betweenId are the element ids the turn list and "Show in turn" scroll to.
export const turnId = (n) => `turn-${n}`;
export const betweenId = (after) => `between-${after}`;

// mergeView applies a live view event to the view held: its turns replace those with the
// same number, the turns it does not carry are kept, and only the turns its order names
// stay (AC12). Everything else in the event replaces what was held.
export function mergeView(held, ev) {
  if (!held || ev.full) return { ...ev, turns: ev.turns };
  const byN = new Map((held.turns || []).map((t) => [t.n, t]));
  for (const t of ev.turns || []) byN.set(t.n, t);
  const turns = (ev.order || []).map((n) => byN.get(n)).filter(Boolean);
  return { ...held, ...ev, turns };
}

// createSessionPage builds the page once; setView(view) and applyEvent(event) redraw it.
// ctx: { sessionId, callHref(exchangeId), loadTool(id), loadDiff(id), loadContent(id),
// noteOpener(node), openCall(exchangeId, opener) } — callHref links a model call to the call
// details dialog (spec 017 AC5).
export function createSessionPage(ctx = {}) {
  const root = el("div", "cols");
  const toc = el("nav", "toc");
  toc.setAttribute("aria-label", "Turns");
  const tocHead = el("div", "colhead");
  const tocSub = el("span", "sub");
  tocHead.append(el("h2", "", "Turns"), tocSub);
  const tocScroll = el("div", "scroll");
  const tocList = el("ol");
  tocScroll.append(tocList);
  toc.append(tocHead, tocScroll);
  // the main column: the overview, then the tabs, the story first; the page scrolls
  const storyCard = el("div", "storycol");
  const heroSlot = el("div", "hero-slot");
  const story = el("div", "story");
  story.setAttribute("role", "region");
  story.setAttribute("aria-label", "Story");
  story.tabIndex = -1;
  const status = el("div", "page-status");
  root.append(toc, storyCard);

  let view = null;
  let current = 0; // index of the current entry in the turn list
  let pinned = false; // a selected entry stays current until the user scrolls by hand
  const turnEls = new Map(); // turn number -> { hash, node }
  const btwEls = new Map(); // after -> { sig, node }
  const opened = new Set(); // the story's folds the user opened, kept across redraws
  let entries = []; // the turn list's entries: { id, button }

  const page = {
    el: root,
    get view() {
      return view;
    },
  };

  // place scrolls the story to an element id and highlights the row; a turn or between
  // block also becomes the current entry.
  function place(id, { flash = true } = {}) {
    const target = globalThis.document.getElementById ? globalThis.document.getElementById(id) : null;
    if (!target) return false;
    // a place in the story is shown on the Story tab
    if (side && side.selected() !== "story") {
      for (let n = target; n; n = n.parentNode) {
        if (n === story) {
          side.select("story");
          break;
        }
      }
    }
    const section = typeof target.closest === "function" ? target.closest(".turn, .btwentry") : null;
    if (section) setCurrentById(section.id, true);
    reveal(target);
    if (typeof target.scrollIntoView === "function") target.scrollIntoView({ block: target === section ? "start" : "center" });
    if (flash && target.classList) {
      const mark = target === section ? target.querySelector(".th, h2") || target : target;
      mark.classList.remove("flash");
      void mark.offsetWidth;
      mark.classList.add("flash");
    }
    return true;
  }

  const dialog = createDialog({
    getView: () => view,
    loadTool: ctx.loadTool,
    loadDiff: ctx.loadDiff,
    loadContent: ctx.loadContent,
    callHref: ctx.callHref,
    place,
  });
  // the story is the first tab, with no count: the turn list says how many prompts (027 C4)
  const side = createSideCard({
    open: (key, opener) => dialog.open(key, opener),
    callHref: ctx.callHref,
    noteOpener: ctx.noteOpener,
    loadDiff: ctx.loadDiff,
    loadContent: ctx.loadContent,
    lead: { id: "story", label: "Story", count: () => null, el: story },
  });
  // one Preview / Raw switch for every prompt and answer in the story (027 C4)
  const storyBar = el("div", "story-bar");
  const modes = el("div", "mode");
  modes.setAttribute("role", "group");
  modes.setAttribute("aria-label", "Prompts and answers: preview or raw");
  const markMode = () => {
    for (const b of modes.children) b.setAttribute("aria-pressed", String(b.dataset.mode === textMode()));
  };
  for (const [m, word] of [["preview", "Preview"], ["raw", "Raw"]]) {
    const b = button("seg", word, () => {
      setTextMode(m);
      markMode();
    });
    b.dataset.mode = m;
    modes.append(b);
  }
  markMode();
  storyBar.append(el("span", "muted-note", "Prompts and answers as"), modes);
  storyCard.append(heroSlot, side.el);
  root.append(dialog.el);

  // open handles a target from the overview, the story or a list: a tool, file or site
  // opens the dialog; a call opens the call details dialog; a turn, between block or several group
  // is shown in the story (AC2, AC11).
  function open(target, opener) {
    switch (target.kind) {
      case "tool":
      case "file":
      case "site":
        dialog.open(`${target.kind}:${target.id}`, opener);
        return;
      case "call":
        if (ctx.openCall) ctx.openCall(target.id, opener);
        return;
      case "turn":
        place(turnId(target.turn));
        return;
      case "between":
        place(betweenId(target.turn));
        return;
      case "several":
        place(`several-${target.id}`);
        return;
      default:
    }
  }
  const storyCtx = {
    open,
    callHref: ctx.callHref,
    noteOpener: ctx.noteOpener,
    isOpen: (key) => opened.has(key),
    setOpen: (key, open) => (open ? opened.add(key) : opened.delete(key)),
  };

  function setCurrentById(id, pin) {
    const i = entries.findIndex((e) => e.id === id);
    if (i < 0) return;
    current = i;
    if (pin) pinned = true;
    entries.forEach((e, j) => e.button.setAttribute("aria-current", String(j === current)));
  }

  function select(i) {
    if (!entries.length) return;
    current = Math.max(0, Math.min(entries.length - 1, i));
    place(entries[current].id, { flash: false });
    setCurrentById(entries[current].id, true);
  }

  // onScroll marks the entry in view: the last section whose top has passed 30% of the
  // story's height. A selected entry stays marked until the user scrolls by hand, so the
  // last turns can be selected even when they cannot reach the top (AC3).
  function onScroll() {
    if (pinned || !entries.length || typeof story.getBoundingClientRect !== "function") return;
    const own = story.scrollHeight > story.clientHeight + 1;
    const box = own ? story.getBoundingClientRect() : { top: 0, height: globalThis.innerHeight || 0 };
    let id = entries[0].id;
    for (const e of entries) {
      const node = globalThis.document.getElementById(e.id);
      if (node && node.getBoundingClientRect().top <= box.top + box.height * 0.3) id = e.id;
    }
    setCurrentById(id, false);
  }
  story.addEventListener("scroll", onScroll, { passive: true });
  if (typeof globalThis.addEventListener === "function") {
    globalThis.addEventListener("scroll", onScroll, { passive: true });
    for (const ev of ["wheel", "touchmove", "keydown"]) {
      globalThis.addEventListener(ev, (e) => {
        if (ev !== "keydown" || ["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End", " "].includes(e.key)) pinned = false;
      }, { passive: true });
    }
  }

  function drawTurnList() {
    const items = [];
    const next = [];
    const entry = (id, cls) => {
      const b = button(cls, undefined, () => select(next.findIndex((x) => x.id === id)));
      next.push({ id, button: b });
      const li = el("li");
      li.append(b);
      items.push(li);
      return b;
    };
    // an edit between turns is in the story and in Needs a look, not in the turn list (027 C4);
    // the newest turn comes first, as in the story (027 C8)
    for (const t of [...(view.turns || [])].reverse()) {
      const b = entry(turnId(t.n), "toc-item");
      const meta = el("span", "m");
      if (t.background) meta.append(el("span", "", "background task"));
      meta.append(el("span", "", t.hooks_only ? "hooks only" : dur(t.clock_ms ?? 0)));
      const c = t.counts || {};
      if (c.changed) meta.append(el("span", "", `${plural(c.changed, "file")} changed`));
      // marks, not words, so a row's prompt keeps the same room as its neighbours'; the words
      // are the mark's name and tooltip (027 C7a)
      const mark = (cls, glyph, word) => {
        const m = el("span", cls, glyph);
        m.title = word;
        m.setAttribute("aria-label", word);
        meta.append(m);
      };
      if (c.failed) mark("bad", "✗", `${c.failed} failed`);
      if (t.end) mark(t.end.word === "ended by error" ? "bad" : "", t.end.glyph, t.end.word);
      b.append(el("span", "n", t.n ? String(t.n) : "·"), el("span", "p clamp", t.prompt || "(no prompt text)"), meta);
    }
    entries = next;
    tocList.replaceChildren(...items);
    // a background task's turn is no prompt the user typed (018)
    tocSub.textContent = plural((view.turns || []).filter((t) => t.n > 0 && !t.background).length, "prompt");
    if (current >= entries.length) current = Math.max(0, entries.length - 1);
    entries.forEach((e, j) => e.button.setAttribute("aria-current", String(j === current)));
  }

  function heroNode() {
    const o = view.overview || {};
    const box = el("section", "card hero");
    box.setAttribute("aria-label", "Session overview");
    const st = o.status || { glyph: "", word: "" };
    // the badge is for a session still running; an ended one says nothing
    if (st.word === "active") box.append(el("span", "status-pill active", `${st.glyph} ${st.word}`));
    box.append(el("h1", "", o.title || ""));
    const meta = el("div", "meta");
    if (o.from) meta.append(el("span", "", `${clock(o.from, false)}–${clock(o.to, false)}`));
    if (o.folder) {
      const f = el("span", "", o.folder);
      f.title = o.folder_full || o.folder;
      meta.append(f);
    }
    // each turn carries its model chip; the summary names models only when there were several
    if ((o.models || []).length > 1) meta.append(el("span", "", o.models.join(", ")));
    const cl = el("span", "", o.client || "");
    cl.title = `session ${o.key || ""}`;
    meta.append(cl);
    box.append(meta);

    const kpis = el("div", "kpis");
    const kpi = (label, value, sub, tab) => {
      const k = tab ? button("kpi", undefined, () => side.select(tab, true)) : el("div", "kpi");
      k.append(el("span", "l", label), el("span", "v", value), sub);
      kpis.append(k);
    };
    const time = o.time || {};
    kpi("Time", dur(time.clock_ms ?? 0), el("span", "s", `model ${dur(time.model_ms ?? 0)} · tools ${dur(time.tool_ms ?? 0)} · waiting ${dur(time.wait_ms ?? 0)} · between turns ${dur(time.between_ms ?? 0)}`));
    // Files, Commands and Sites are tabs below with their counts and lines (027 C4)
    kpi("Context", num((o.context || {}).last), el("span", "s", "tokens in the last call"), "context");
    box.append(kpis);

    if ((o.needs_look || []).length) {
      const attn = el("div", "attn");
      attn.append(el("h2", "", "Needs a look"));
      const ul = el("ul");
      for (const it of o.needs_look) {
        const li = el("li", it.tone);
        const b = button("", undefined, () => open(it.target, b));
        const ic = el("span", "ic", it.glyph);
        ic.setAttribute("aria-hidden", "true");
        const text = el("span");
        text.append(el("span", "t", it.title), el("span", "s", it.sub));
        b.append(ic, text, el("span", "go", `${it.go} →`));
        li.append(b);
        ul.append(li);
      }
      attn.append(ul);
      if (o.more_look) attn.append(el("p", "muted-note", `and ${o.more_look} more`));
      box.append(attn);
    }

    // the token counts are in the Context tab (027 C4); the cost and side calls stay here
    const foot = el("div", "foot");
    const tk = o.tokens || {};
    const cost = totalCost(tk.cost);
    // no cost to state ("—") means no line
    if (cost.text !== "—") {
      const costLine = el("span", "cost", `${LABEL_LONG} cost: ${cost.text}`);
      costLine.title = `${cost.title}\nSide calls included.`;
      foot.append(costLine);
    }
    const sideCalls = view.side_calls || [];
    if (sideCalls.length) {
      const d = el("details", "side");
      d.append(el("summary", "", plural(sideCalls.length, "side call")));
      const ul = el("ul");
      for (const s of sideCalls) {
        const li = el("li");
        const a = el("a", "linkish", `#${s.n} ${s.name}`);
        a.href = ctx.callHref ? ctx.callHref(s.exchange_id) : "#";
        if (ctx.noteOpener) a.addEventListener("click", () => ctx.noteOpener(a));
        li.append(a, el("span", "muted", ` ${clock(s.at)} · ${dur(s.dur_ms ?? 0)} · ${s.status}`));
        ul.append(li);
      }
      d.append(ul, el("p", "muted small", "The client's own calls: never a turn, never counted as an error."));
      foot.append(d);
    }
    if (foot.children.length) box.append(foot);
    if (o.empty) box.append(el("p", "page-note", o.empty));
    return box;
  }

  // drawStory updates the story by key: a turn whose hash is unchanged keeps its element,
  // a changed turn's element alone is replaced, so a live update never rebuilds the
  // timeline (AC12).
  let hero = null;
  function drawStory() {
    const next = new Map();
    const nextBtw = new Map();
    const between = new Map();
    for (const b of view.between || []) between.set(b.after, b);
    // the story runs newest first (027 C8): it is built oldest first, as the server sends it,
    // so each between-turns entry sits between the right two turns, then read backwards
    const seq = [];
    hero = heroNode();
    heroSlot.replaceChildren(hero);
    const btw = (after) => {
      const b = between.get(after);
      if (!b) return;
      const sig = JSON.stringify(b);
      let k = btwEls.get(after);
      if (!k || k.sig !== sig) k = { sig, node: betweenNode(b, storyCtx) };
      nextBtw.set(after, k);
      seq.push(k.node);
    };
    btw(0);
    for (const t of view.turns || []) {
      let k = turnEls.get(t.n);
      if (!k || k.hash !== t.hash) k = { hash: t.hash, node: turnNode(t, storyCtx) };
      next.set(t.n, k);
      seq.push(k.node);
      if (t.n > 0) btw(t.n);
    }
    const want = [storyBar, status, ...seq.reverse()];
    turnEls.clear();
    for (const [n, k] of next) turnEls.set(n, k);
    btwEls.clear();
    for (const [n, k] of nextBtw) btwEls.set(n, k);
    reconcile(story, want);
  }

  // the page scrolls: the newest turn is at the top (027 C8), so a live session follows it
  // when the window was at the top
  const pageEl = () => globalThis.document && globalThis.document.scrollingElement;
  const atTop = () => {
    const p = pageEl();
    return Boolean(p) && typeof p.scrollTop === "number" && p.scrollTop <= 8;
  };

  page.setView = (next) => {
    const follow = view !== null && atTop();
    view = next;
    if (loader) loader.remove();
    drawTurnList();
    drawStory();
    side.update(view);
    dialog.update(view);
    if (follow) pageEl().scrollTop = 0;
    onScroll();
  };
  page.applyEvent = (ev) => page.setView(mergeView(view, ev));
  let loader = null; // the story's loader, until the view arrives
  page.setStatus = (text, kind = "note") => {
    status.replaceChildren();
    if (text) status.append(el("p", `page-note${kind === "error" ? " error" : ""}`, text));
    // until the view arrives the page keeps its loader, under any note ("building session…")
    loader = !view && kind !== "error" ? skeleton("Loading the session…", 4, "cards") : null;
    if (loader) status.append(loader);
    if (!view) reconcile(story, [status]);
  };
  page.moveTurn = (delta) => select(current + delta);
  page.dialogOpen = () => dialog.isOpen();
  page.turnHeading = () => {
    const e = entries[current];
    const node = e && globalThis.document.getElementById ? globalThis.document.getElementById(e.id) : null;
    return node && typeof node.querySelector === "function" ? node.querySelector("h2") : null;
  };
  page.open = open;
  page.place = place;
  page.side = side;
  page.dialog = dialog;
  page.entries = () => entries;
  page.current = () => current;
  const waiting = el("li");
  waiting.append(skeleton("Loading turns…", 6, "rows"));
  tocList.replaceChildren(waiting);
  page.setStatus("");
  return page;
}
