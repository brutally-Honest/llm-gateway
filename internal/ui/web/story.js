import { button, bytes, clock, dur, el, markText, plural } from "./fmt.js";
import { renderMarkdown } from "./markdown.js";
import { copyButton } from "./tool-card.js";

// The story's turn cards (spec 016 AC4–AC8, 017 AC13–AC18). Every fact comes from the
// server's view: these functions only lay it out. ctx: { open(target, opener),
// callHref(exchangeId), noteOpener(node), isOpen(key), setOpen(key, open) }. A turn shows
// its prompt, its chips and its final answer; a chip opens its panel (the steps, the
// files, the sites…), one at a time; isOpen and setOpen keep what was opened across live
// redraws.

const run = (ctx, target, opener) => {
  if (ctx && ctx.open) ctx.open(target, opener);
};

// callLink is a model call's "#n": it opens the call details dialog (spec 017 AC5).
function callLink(ctx, exchangeId, text, cls) {
  const a = el("a", cls, text);
  a.href = ctx && ctx.callHref ? ctx.callHref(exchangeId) : "#";
  if (ctx && ctx.noteOpener) a.addEventListener("click", () => ctx.noteOpener(a));
  return a;
}

// effectTarget is what an effect's path control opens: a connection's site, a search's
// tool call, else the file.
function effectTarget(e) {
  if (e.kind === "net") return { kind: "site", id: e.host };
  if (e.kind === "search" && e.tool) return { kind: "tool", id: e.tool };
  return { kind: "file", id: e.full_path || e.path };
}

// effectStat is an effect's size: lines for a change whose lines are known, its size
// otherwise; bytes each way for a connection; the read range for a read.
function effectStat(e) {
  const box = el("span", "muted small");
  if (e.kind === "net") {
    box.textContent = `↑ ${bytes(e.up ?? 0)} ↓ ${bytes(e.down ?? 0)}`;
  } else if (e.kind === "change" && e.added >= 0) {
    box.append(el("span", "a", `+${e.added}`), " ", el("span", "d", `−${e.removed}`));
  } else if (e.kind === "change" && e.size !== undefined && e.size !== null) {
    box.textContent = `${bytes(e.size)}${e.no_copy ? ", no copy" : ""}`;
  } else if (e.detail) {
    box.textContent = e.detail;
  }
  return box;
}

// labelBadge is an effect's attribution as glyph and word; its reason on hover.
export function labelBadge(e) {
  const b = el("span", `lbl lbl-${e.label || "none"}`);
  if (e.mark) {
    const g = el("span", "", e.mark.glyph);
    g.setAttribute("aria-hidden", "true");
    b.append(g, ` ${e.mark.word}`);
  } else {
    b.textContent = "— no label";
  }
  if (e.why) b.title = e.why;
  return b;
}

// effectNode is one effect line (AC6): label, what and path, size, Diff or View.
export function effectNode(e, ctx) {
  const li = el("li");
  li.id = `eff-${e.id}`;
  const target = effectTarget(e);
  const what = el("span");
  const w = e.what || { glyph: "", word: "" };
  const path = button("linkish", undefined, () => run(ctx, target, path));
  const code = el("code", "", e.kind === "net" ? `${e.host}:${e.port}` : e.path);
  if (e.kind !== "net" && e.full_path) code.title = e.full_path;
  path.append(code);
  what.append(`${markText(w)} `, path);
  if (e.during) what.append(" ", el("span", "during", `during ${e.during}`));
  li.append(labelBadge(e), what, effectStat(e));
  if (e.kind === "change" && (e.has_diff || e.has_copy)) {
    const view = button("btn sm", e.has_diff ? "Diff" : "View", () => run(ctx, target, view));
    li.append(view);
  } else {
    li.append(el("span"));
  }
  return li;
}

function effectList(list, ctx) {
  const ul = el("ul", "effs");
  for (const e of list) ul.append(effectNode(e, ctx));
  return ul;
}

// toolNode is one tool call as a line (AC5): state, name, agent, summary, lines, run
// time; a failed call's first error line; its effects; the sub-agent it spawned.
export function toolNode(l, ctx) {
  const li = el("li");
  const ok = l.chip === "ok";
  const line = el("div", `tl${l.chip === "failed" ? " failed" : ""}`);
  line.id = `tool-${l.id}`;
  line.append(el("span", `si ${l.chip || ""}`.trim(), markText(l.state, ok)));
  const name = button("linkish nm", l.name, () => run(ctx, { kind: "tool", id: l.id }, name));
  line.append(name);
  if (l.agent) line.append(el("span", "agent", l.agent));
  if (l.summary) line.append(el("code", "", l.summary));
  if (l.added > 0 || l.removed > 0) {
    const ls = el("span", "ls");
    ls.append(el("span", "a", `+${l.added}`), " ", el("span", "d", `−${l.removed}`));
    line.append(ls);
  }
  if (l.dur_ms !== undefined && l.dur_ms !== null) line.append(el("span", "tm", `${l.approx ? "≈" : ""}${dur(l.dur_ms)}`));
  li.append(line);
  if (l.first_error) li.append(el("div", "why", l.first_error));
  if ((l.effects || []).length) li.append(effectList(l.effects, ctx));
  if (l.sub) {
    const box = el("div", "subagent");
    box.setAttribute("role", "group");
    box.setAttribute("aria-label", `Sub-agent ${l.sub.label}`);
    const parts = [`Sub-agent ${l.sub.label}`];
    if (l.dur_ms !== undefined && l.dur_ms !== null) parts.push(dur(l.dur_ms));
    parts.push(plural(l.sub.calls, "model call"));
    box.append(el("div", "say", parts.join(" · ")));
    const ol = el("ol", "steps");
    for (const s of l.sub.steps || []) ol.append(stepNode(s, ctx));
    box.append(ol);
    li.append(box);
  }
  return li;
}

// folds maps a folded body to the function that opens it, so reveal can open the folds
// around any node it is asked to show.
const folds = new WeakMap();

// foldToggle is a stepper item's header button and its body: the button folds and
// unfolds the body. key names the fold for ctx.isOpen and ctx.setOpen; the body starts
// open only when the user opened it before.
function foldToggle(ctx, key, id) {
  const body = el("div", "fold-body");
  body.id = id;
  const set = (open, remember = true) => {
    body.hidden = !open;
    toggle.setAttribute("aria-expanded", String(open));
    if (remember && ctx && ctx.setOpen) ctx.setOpen(key, open);
  };
  const toggle = button("fold", undefined, () => set(body.hidden));
  toggle.setAttribute("aria-controls", id);
  folds.set(body, () => set(true));
  set(Boolean(ctx && ctx.isOpen && ctx.isOpen(key)), false);
  return { toggle, body };
}

// reveal opens every fold around node, so a row the page scrolls to is visible.
export function reveal(node) {
  for (let n = node; n; n = n.parentNode) {
    const open = folds.get(n);
    if (open && n.hidden) open();
  }
}

// peek is a folded step's tools in one line: state glyph, name and summary of each.
function peek(tools) {
  return tools.map((l) => [l.state && l.state.glyph, l.name, l.summary].filter(Boolean).join(" ")).join(" · ");
}

function toolList(list, ctx) {
  const ul = el("ul", "tools");
  for (const l of list) ul.append(toolNode(l, ctx));
  return ul;
}

const capital = (s) => (s ? s[0].toUpperCase() + s.slice(1) : "");

// stepNode is one model call (AC7): #n, what it did and for how long, how many tools it
// ran at once; an errored call's status in red; thinking behind a control.
export function stepNode(s, ctx) {
  const li = el("li", `step${s.errored || (s.tools || []).some((l) => l.chip === "failed") ? " bad" : ""}`);
  const say = el("div", "say");
  say.append(callLink(ctx, s.exchange_id, `#${s.n}`, "linkish cn call-link"));
  const tools = s.tools || [];
  const n = tools.length;
  const then = n === 0 ? "" : n === 1 ? ", then ran:" : `, then ran ${n} at once:`;
  const what = el("span");
  if (s.errored) {
    what.append(el("span", "err-word", `✗ ${s.status || "failed"}`), ` after ${dur(s.dur_ms)}${then}`);
  } else {
    what.textContent = `${capital(s.did)} for ${dur(s.dur_ms)}${then}`;
  }
  const several = s.several || [];
  if (!n && !several.length) {
    say.append(what);
    if (s.thinking) say.append(callLink(ctx, s.exchange_id, "Read the thinking", "linkish"));
    li.append(say);
    return li;
  }
  const { toggle, body } = foldToggle(ctx, `step:${s.exchange_id}`, `steps-${s.exchange_id}`);
  toggle.append(el("span", "chev", "▸"), what);
  if (n) toggle.append(el("span", `peek${tools.some((l) => l.chip === "failed") ? " bad" : ""}`, peek(tools)));
  say.append(toggle);
  if (s.thinking) say.append(callLink(ctx, s.exchange_id, "Read the thinking", "linkish"));
  if (n) body.append(toolList(tools, ctx));
  if (several.length) {
    const g = el("p", "grp", `◌ Not pinned to one tool call: ${(s.several_names || []).join(" and ")} ran together`);
    g.id = `several-${s.exchange_id}`;
    body.append(g, effectList(several, ctx));
  }
  li.append(say, body);
  return li;
}

// foldItem is a stepper item that is not a model call: the hooks' tool calls of a turn,
// or the effects no tool call caused. Its line says what it holds; its body is folded.
function foldItem(ctx, key, title, content, cls = "") {
  const li = el("li", `step ${cls}`.trim());
  const say = el("div", "say");
  const { toggle, body } = foldToggle(ctx, key, `fold-${key.replace(/[^\w-]/g, "-")}`);
  toggle.append(el("span", "chev", "▸"), el("span", "", title));
  say.append(toggle);
  body.append(...content);
  li.append(say, body);
  return li;
}

// remindersNode is the "+N reminders" toggle under the prompt (AC4).
function remindersNode(list) {
  const box = el("div", "reminders");
  const body = el("div", "reminder-list");
  body.hidden = true;
  for (const r of list) body.append(el("pre", "", r));
  const label = `+${plural(list.length, "reminder")}`;
  const t = button("linkish small", label, () => {
    body.hidden = !body.hidden;
    t.setAttribute("aria-expanded", String(!body.hidden));
    t.textContent = body.hidden ? label : `Hide ${plural(list.length, "reminder")}`;
  });
  t.setAttribute("aria-expanded", "false");
  box.append(t, body);
  return box;
}

// The Preview / Raw choice for prompts and answers (017 AC13): Preview by default, the
// last choice kept in this browser. Storage can be missing or blocked; then it is not kept.
const MODE_KEY = "gw.textMode";
function savedMode() {
  try {
    return globalThis.localStorage && globalThis.localStorage.getItem(MODE_KEY) === "raw" ? "raw" : "preview";
  } catch {
    return "preview";
  }
}
function saveMode(m) {
  try {
    if (globalThis.localStorage) globalThis.localStorage.setItem(MODE_KEY, m);
  } catch {
    // not kept
  }
}

// textBlock is a turn's prompt or final answer (AC13, AC18): its label, the Preview / Raw
// switch and the whole text. Only the shown mode is built.
// onMode, if given, runs after a switch between Preview and Raw (their heights differ).
// shared holds the story's text blocks that follow one Preview / Raw switch for the whole
// story (spec 027 C4); a block no longer on the page is dropped when the mode changes.
const shared = new Set();

// textMode is the Preview / Raw choice in use; setTextMode changes it for every shared block
// and keeps it in this browser.
export const textMode = () => savedMode();
export function setTextMode(m) {
  saveMode(m);
  for (const s of [...shared]) {
    if (s.box.isConnected === false) shared.delete(s);
    else s.apply(m);
  }
}

// opts.shared: the block has no switch of its own and follows setTextMode.
export function textBlock(cls, label, text, extra = [], onMode = null, opts = {}) {
  const box = el("section", `tx ${cls}`);
  const head = el("div", "tx-h");
  head.append(el("span", "who", label));
  const sw = el("div", "mode");
  sw.setAttribute("role", "group");
  sw.setAttribute("aria-label", `${label}: preview or raw`);
  const body = el("div", "tx-b");
  const built = {};
  let mode = savedMode();
  const show = () => {
    built[mode] ||= mode === "raw" ? el("pre", "raw", text) : renderMarkdown(text);
    body.replaceChildren(built[mode]);
    for (const b of sw.children) b.setAttribute("aria-pressed", String(b.dataset.mode === mode));
  };
  if (opts.shared) {
    shared.add({ box, apply: (m) => { mode = m; show(); } });
    head.append(copyButton(() => text));
    box.append(head, ...extra, body);
    show();
    return box;
  }
  for (const [m, word] of [["preview", "Preview"], ["raw", "Raw"]]) {
    const b = button("seg", word, () => {
      mode = m;
      saveMode(m);
      show();
      if (onMode) onMode();
    });
    b.dataset.mode = m;
    sw.append(b);
  }
  head.append(sw, copyButton(() => text));
  box.append(head, ...extra, body);
  show();
  return box;
}

// The chips (017 AC14–AC16, amended 2026-11-13): "All" opens the turn's whole stepper in
// order; every other chip opens the same timeline narrowed to its kind, still in order,
// each row keeping the #n of the call it belongs to.

// walkTurn lists a turn's model calls, tool calls and effects in stepper order, each with
// the step that holds it; loose effects (no tool call caused them) come last.
function walkTurn(t) {
  const calls = [];
  const tools = [];
  const effects = [];
  const walkStep = (s) => {
    calls.push(s);
    for (const l of s.tools || []) walkTool(l, s);
    for (const e of s.several || []) effects.push({ e, s, several: true });
  };
  const walkTool = (l, s) => {
    tools.push({ l, s });
    for (const e of l.effects || []) effects.push({ e, l, s });
    for (const sub of (l.sub && l.sub.steps) || []) walkStep(sub);
  };
  (t.steps || []).forEach(walkStep);
  for (const l of t.hook_tools || []) walkTool(l, null);
  for (const e of t.loose || []) effects.push({ e, loose: true });
  calls.sort((a, b) => a.n - b.n);
  const at = (x) => Date.parse(x.e.at) || 0;
  effects.sort((a, b) => at(a) - at(b));
  return { calls, tools, effects };
}

// callTag is the #n of the call a row belongs to, or where it came from without one.
function callTag(ctx, s, fallback) {
  if (s) return callLink(ctx, s.exchange_id, `#${s.n}`, "linkish cn call-link");
  return el("span", "cn muted", fallback);
}

const frows = (items, empty) => {
  const ul = el("ul", "frows");
  ul.append(...items);
  if (!items.length) ul.append(el("li", "muted small", empty));
  return [ul];
};

// callRow is one model call on its own line: #n, sub-agent, what it did and for how long.
function callRow(s, ctx) {
  const li = el("li", `frow${s.errored ? " bad" : ""}`);
  li.append(callTag(ctx, s));
  const what = el("span", "fw");
  if (s.agent) what.append(el("span", "agent", s.agent), " ");
  if (s.errored) what.append(el("span", "err-word", `✗ ${s.status || "failed"}`), ` after ${dur(s.dur_ms)}`);
  else what.append(`${capital(s.did)} for ${dur(s.dur_ms)}`);
  const n = (s.tools || []).length;
  if (n) what.append(el("span", "muted", ` · ${plural(n, "tool call")}`));
  li.append(what);
  if (s.thinking) li.append(callLink(ctx, s.exchange_id, "Read the thinking", "linkish small"));
  return li;
}

// toolRow is one tool call on its own line: #n, state, name, summary, lines, run time.
function toolRow({ l, s }, ctx) {
  const li = el("li", `frow${l.chip === "failed" ? " bad" : ""}`);
  li.append(callTag(ctx, s, "hooks"));
  const line = el("span", "fw");
  line.append(el("span", `si ${l.chip || ""}`.trim(), markText(l.state, l.chip === "ok")), " ");
  const name = button("linkish nm", l.name, () => run(ctx, { kind: "tool", id: l.id }, name));
  line.append(name);
  if (l.agent) line.append(" ", el("span", "agent", l.agent));
  if (l.summary) line.append(" ", el("code", "", l.summary));
  li.append(line);
  const r = el("span", "r");
  if (l.added > 0 || l.removed > 0) r.append(el("span", "a", `+${l.added}`), " ", el("span", "d", `−${l.removed}`), " ");
  if (l.dur_ms !== undefined && l.dur_ms !== null) r.append(el("span", "tm", `${l.approx ? "≈" : ""}${dur(l.dur_ms)}`));
  li.append(r);
  if (l.first_error) li.append(el("div", "why", l.first_error));
  return li;
}

// effectRow is one file change, read or connection on its own line: the #n and tool that
// caused it (or that none did), what happened to which path or host, its size.
function effectRow({ e, l, s, loose, several }, ctx) {
  const li = el("li", "frow");
  li.append(callTag(ctx, s, "○"));
  const what = el("span", "fw");
  if (l) {
    const name = button("linkish nm", l.name, () => run(ctx, { kind: "tool", id: l.id }, name));
    what.append(name, " ");
  } else if (several) {
    what.append(el("span", "muted", "◌ several running "));
  } else if (loose) {
    what.append(el("span", "muted", "not caused by a tool call "));
  }
  const w = e.what || { glyph: "", word: "" };
  what.append(`${w.glyph} `);
  const target = effectTarget(e);
  const path = button("linkish", undefined, () => run(ctx, target, path));
  const code = el("code", "", e.kind === "net" ? (e.port && e.port !== 443 ? `${e.host}:${e.port}` : e.host) : e.path);
  if (e.kind !== "net" && e.full_path) code.title = e.full_path;
  path.append(code);
  what.append(path);
  li.append(what, effectStat(e));
  return li;
}

// stepsPanel is the turn's whole stepper (016 AC5–AC7), the "All" chip's panel: every
// model call with its tool calls folded, and the effects no tool call caused.
function stepsPanel(t, ctx) {
  const hookTools = t.hook_tools || [];
  const ol = el("ol", "steps");
  if (t.hooks_only) {
    ol.append(foldItem(ctx, `hooks:${t.n}`, `The hooks saw: ${plural(hookTools.length, "tool call")}`, [toolList(hookTools, ctx)]));
  } else {
    for (const s of t.steps || []) ol.append(stepNode(s, ctx));
    if (hookTools.length) ol.append(foldItem(ctx, `hooks:${t.n}`, `Seen by hooks only: ${plural(hookTools.length, "tool call")}`, [toolList(hookTools, ctx)]));
  }
  const loose = t.loose || [];
  if (loose.length) ol.append(foldItem(ctx, `loose:${t.n}`, `○ Not caused by a tool call: ${plural(loose.length, "effect")}`, [effectList(loose, ctx)], "loose"));
  return [ol];
}

// CHIPS are a turn's chips in order: what each says and what its panel lists.
const CHIPS = [
  { id: "all", text: () => "All", panel: stepsPanel },
  { id: "calls", text: (c) => c.calls && plural(c.calls, "model call"), panel: (t, ctx) => frows(walkTurn(t).calls.map((s) => callRow(s, ctx)), "No model calls.") },
  { id: "tools", text: (c) => c.tools && plural(c.tools, "tool call"), panel: (t, ctx) => frows(walkTurn(t).tools.map((x) => toolRow(x, ctx)), "No tool calls.") },
  { id: "changed", text: (c) => c.changed && `${plural(c.changed, "file")} changed`, panel: (t, ctx) => frows(walkTurn(t).effects.filter((x) => x.e.kind === "change").map((x) => effectRow(x, ctx)), "No file changes.") },
  { id: "read", text: (c) => c.read && `${c.read} read`, panel: (t, ctx) => frows(walkTurn(t).effects.filter((x) => x.e.kind === "read").map((x) => effectRow(x, ctx)), "No reads.") },
  { id: "searched", text: (c) => c.searched && plural(c.searched, "search", "searches"), panel: (t, ctx) => frows(walkTurn(t).tools.filter((x) => x.l.kind === "search").map((x) => toolRow(x, ctx)), "No searches.") },
  { id: "sites", text: (c) => c.sites && plural(c.sites, "site"), panel: (t, ctx) => frows(walkTurn(t).effects.filter((x) => x.e.kind === "net").map((x) => effectRow(x, ctx)), "No connections.") },
  { id: "failed", bad: true, text: (c) => c.failed && `✗ ${c.failed} failed`, panel: (t, ctx) => frows(walkTurn(t).tools.filter((x) => x.l.chip === "failed").map((x) => toolRow(x, ctx)), "Nothing failed.") },
];

// figureNode draws a short turn's work (027 C4b): one column per model call, its "#n" and
// time on top and the tool calls it asked for under it, columns in order left to right.
// The server picks the turn's figure; a long turn has none here and reads in its All panel,
// one line per call. Every box opens what it names.
function figureNode(t, ctx) {
  if (t.figure !== "boxes") return null;
  const ol = el("ol", "flow");
  ol.setAttribute("aria-label", `Turn ${t.n}, call by call`);
  for (const s of t.steps || []) {
    const tools = s.tools || [];
    const bad = s.errored;
    const col = el("li", "fcol");
    const call = callLink(ctx, s.exchange_id, "", `fbox fcall${bad ? " bad" : ""}`);
    call.append(el("b", "", `#${s.n}`), el("span", "", bad ? `✗ ${s.status || "failed"}` : dur(s.dur_ms)));
    call.title = `Call #${s.n}: ${s.errored ? s.status || "failed" : `${s.did} for ${dur(s.dur_ms)}`}`;
    col.append(call);
    if (s.agent) col.append(el("span", "fagent", s.agent));
    for (const l of tools) {
      const failed = l.chip === "failed";
      const b = button(`fbox ftool${failed ? " bad" : ""}`, undefined, () => run(ctx, { kind: "tool", id: l.id }, b));
      // the state is its glyph alone; its word leads the box's name and tooltip
      const si = el("span", `si ${l.chip || ""}`.trim(), markText(l.state, true));
      si.setAttribute("aria-hidden", "true");
      b.append(si, el("span", "fn", l.name));
      if (l.summary) b.append(el("code", "", l.summary));
      b.setAttribute("aria-label", [l.state && l.state.word, l.name, l.summary].filter(Boolean).join(" "));
      b.title = [l.state && l.state.word, l.name, l.summary, l.sub ? `sub-agent ${l.sub.label} · ${plural(l.sub.calls, "model call")}` : ""].filter(Boolean).join(" · ");
      col.append(b);
    }
    ol.append(col);
  }
  return ol;
}

// chipsNode is a turn's chips and their panels: one open at a time, the selected chip
// marked, the choice kept across live redraws (AC14–AC16, E10). The "All" panel is built
// at once, since "Show in turn" finds rows in it; the others when first opened.
function chipsNode(t, ctx) {
  const counts = t.counts || {};
  const shown = CHIPS.filter((c) => c.id !== "all" && c.text(counts));
  if (!shown.length && !(t.steps || []).length && !(t.hook_tools || []).length) return null;
  const box = el("div", "turn-chips");
  const bar = el("div", "sum");
  bar.setAttribute("role", "group");
  bar.setAttribute("aria-label", `Turn ${t.n}: what it did`);
  // a turn's card says what this strip is: the work between its prompt and its answer (027 C9)
  bar.append(el("span", "l", "How it got there"));
  const holder = el("div", "chip-panels");
  const panels = {};
  const chips = [];
  const key = (id) => `chip:${t.n}:${id}`;
  let selected = null;
  const panelOf = (c) => {
    if (!panels[c.id]) {
      const p = el("div", `chip-panel cp-${c.id}`);
      p.id = `cp-${t.n}-${c.id}`;
      p.hidden = true;
      p.append(...c.panel(t, ctx));
      holder.append(p);
      panels[c.id] = p;
    }
    return panels[c.id];
  };
  const select = (id, remember = true) => {
    selected = id;
    if (id) panelOf(CHIPS.find((c) => c.id === id));
    for (const [name, p] of Object.entries(panels)) p.hidden = name !== id;
    for (const b of chips) {
      const on = b.dataset.chip === id;
      b.setAttribute("aria-expanded", String(on));
      if (on) b.classList.add("on");
      else b.classList.remove("on");
    }
    if (remember && ctx && ctx.setOpen) for (const c of CHIPS) ctx.setOpen(key(c.id), c.id === id);
  };
  for (const c of [CHIPS[0], ...shown]) {
    const b = button(`pill chip${c.bad ? " bad" : ""}`, c.text(counts), () => select(selected === c.id ? null : c.id));
    b.dataset.chip = c.id;
    b.setAttribute("aria-controls", `cp-${t.n}-${c.id}`);
    chips.push(b);
    bar.append(b);
  }
  // a row the page shows (Show in turn) sits in the All panel: open it there
  folds.set(panelOf(CHIPS[0]), () => select("all"));
  const figure = figureNode(t, ctx);
  box.append(bar);
  if (figure) box.append(figure);
  box.append(holder);
  select(CHIPS.map((c) => c.id).find((id) => chips.some((b) => b.dataset.chip === id) && ctx && ctx.isOpen && ctx.isOpen(key(id))) || null, false);
  return box;
}

// turnNode is one turn card (016 AC4, 017 AC13–AC18): the prompt, the chips that open
// the turn's timeline whole or narrowed to one kind, then the final answer.
export function turnNode(t, ctx) {
  const box = el("article", "turn chapter");
  box.id = `turn-${t.n}`;
  box.setAttribute("aria-labelledby", `h-${t.n}`);
  const head = el("header", "th");
  const k = el("div", "k");
  const h = el("h2", "", t.n ? `Turn ${t.n}` : "Before the first prompt");
  h.id = `h-${t.n}`;
  k.append(h, el("span", "", clock(t.at)));
  if (t.background) k.append(el("span", "bg", "background task"));
  if (t.end) k.append(el("span", `end${t.end.word === "ended by error" ? " bad" : ""}`, markText(t.end)));
  const d = el("div", "dur");
  if (t.hooks_only) {
    // a hooks-only turn says it once, in its model chip ("none captured", 027 C4)
    if (t.background) d.append(el("b", "", "—"), el("span", "", "no model call answered it"));
  } else {
    d.append(el("b", "", dur(t.clock_ms ?? 0)), el("span", "", `model ${dur(t.model_ms ?? 0)} · tools ${dur(t.tool_ms ?? 0)} · waiting ${dur(t.wait_ms ?? 0)}`));
  }
  head.append(k, d);
  // the models the turn's calls used, one chip each (spec 027 C4); a turn the hooks alone
  // saw says none was captured
  const mdls = el("div", "mdls");
  if ((t.models || []).length) for (const m of t.models) mdls.append(el("span", "mdl", m));
  else if (t.hooks_only) mdls.append(el("span", "mdl none", "none captured"));
  if (mdls.children.length) head.append(mdls);
  box.append(head);

  const body = el("div", "tb");
  if (t.n) {
    const extra = [];
    if (t.prompt_truncated) extra.push(el("p", "muted small", "The prompt was cut when it was stored."));
    if ((t.reminders || []).length) extra.push(remindersNode(t.reminders));
    // a background turn's text is the client's notice of a finished task, not a prompt (018)
    body.append(textBlock("prompt", t.background ? "Background task" : "Prompt", t.prompt || "(no prompt text)", extra, null, { shared: true }));
  }
  const chips = chipsNode(t, ctx);
  if (chips) body.append(chips);
  if (t.answer) body.append(textBlock("answer", "Final answer", t.answer, [], null, { shared: true }));
  box.append(body);
  return box;
}

// betweenNode is an edit seen between two turns (AC8).
export function betweenNode(b, ctx) {
  const box = el("section", "btwentry");
  box.id = `between-${b.after}`;
  box.setAttribute("aria-label", b.title || "Between turns");
  box.append(el("h2", "", `${b.title} · ${clock(b.at)}`), el("p", "", b.text));
  if ((b.effects || []).length) box.append(effectList(b.effects, ctx));
  return box;
}
