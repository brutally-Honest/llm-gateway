import { button, bytes, clock, dur, el, markText, plural } from "./fmt.js";

// The story's turn cards (spec 016 AC4–AC8). Every fact comes from the server's view:
// these functions only lay it out. ctx: { open(target, opener), callHref(exchangeId),
// noteOpener(node) }.

const run = (ctx, target, opener) => {
  if (ctx && ctx.open) ctx.open(target, opener);
};

// callLink is a model call's "#n": it opens the 013 drawer until 017 ships.
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

function toolList(list, ctx) {
  const ul = el("ul", "tools");
  for (const l of list) ul.append(toolNode(l, ctx));
  return ul;
}

const capital = (s) => (s ? s[0].toUpperCase() + s.slice(1) : "");

// stepNode is one model call (AC7): #n, what it did and for how long, how many tools it
// ran at once; an errored call's status in red; thinking behind a control.
export function stepNode(s, ctx) {
  const li = el("li", "step");
  const say = el("div", "say");
  say.append(callLink(ctx, s.exchange_id, `#${s.n}`, "linkish cn call-link"));
  const n = (s.tools || []).length;
  const then = n === 0 ? "" : n === 1 ? ", then ran:" : `, then ran ${n} at once:`;
  if (s.errored) {
    say.append(el("span", "err-word", `✗ ${s.status || "failed"}`), el("span", "", `after ${dur(s.dur_ms)}${then}`));
  } else {
    say.append(el("span", "", `${capital(s.did)} for ${dur(s.dur_ms)}${then}`));
  }
  if (s.thinking) say.append(callLink(ctx, s.exchange_id, "Read the thinking", "linkish"));
  li.append(say);
  if (n) li.append(toolList(s.tools, ctx));
  if ((s.several || []).length) {
    const g = el("p", "grp", `◌ Not pinned to one tool call: ${(s.several_names || []).join(" and ")} ran together`);
    g.id = `several-${s.exchange_id}`;
    li.append(g, effectList(s.several, ctx));
  }
  return li;
}

function pills(c, box) {
  const add = (text, cls = "pill") => box.append(el("span", cls, text));
  if (c.tools) add(plural(c.tools, "tool call"));
  if (c.changed) add(`${plural(c.changed, "file")} changed`);
  if (c.read) add(`${c.read} read`);
  if (c.searched) add(plural(c.searched, "search", "searches"));
  if (c.sites) add(plural(c.sites, "site"));
  if (c.failed) add(`✗ ${c.failed} failed`, "pill bad");
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

// turnNode is one turn card (AC4–AC7).
export function turnNode(t, ctx) {
  const box = el("article", "card turn");
  box.id = `turn-${t.n}`;
  box.setAttribute("aria-labelledby", `h-${t.n}`);
  const head = el("header", "th");
  const k = el("div", "k");
  k.append(el("b", "", t.n ? `Turn ${t.n}` : "Before the first prompt"), el("span", "", clock(t.at)));
  if ((t.models || []).length) k.append(el("span", "mdl", t.models.join(", ")));
  if (t.end) k.append(el("span", `end${t.end.word === "ended by error" ? " bad" : ""}`, markText(t.end)));
  const d = el("div", "dur");
  if (t.hooks_only) {
    d.append(el("b", "", "—"), el("span", "", "no model calls captured"));
  } else {
    d.append(el("b", "", dur(t.clock_ms ?? 0)), el("span", "", `model ${dur(t.model_ms ?? 0)} · tools ${dur(t.tool_ms ?? 0)} · waiting ${dur(t.wait_ms ?? 0)}`));
  }
  const h = el("h2", "", t.prompt || "(no prompt text)");
  h.id = `h-${t.n}`;
  head.append(k, d, h);
  if (t.prompt_truncated) head.append(el("p", "muted small", "The prompt was cut when it was stored."));
  if ((t.reminders || []).length) head.append(remindersNode(t.reminders));
  box.append(head);

  const body = el("div", "tb");
  const sum = el("div", "sum");
  pills(t.counts || {}, sum);
  if (sum.children.length) body.append(sum);
  const hookTools = t.hook_tools || [];
  if (t.hooks_only) {
    const ol = el("ol", "steps");
    const li = el("li", "step");
    li.append(el("div", "say", "No model calls captured. The hooks saw:"));
    if (hookTools.length) li.append(toolList(hookTools, ctx));
    ol.append(li);
    body.append(ol);
  } else {
    const ol = el("ol", "steps");
    for (const s of t.steps || []) ol.append(stepNode(s, ctx));
    body.append(ol);
    if (hookTools.length) body.append(el("p", "grp", "Seen by hooks only"), toolList(hookTools, ctx));
  }
  if ((t.loose || []).length) body.append(el("p", "grp", "○ Not caused by a tool call"), effectList(t.loose, ctx));
  if (t.answer) {
    const a = el("div", "answer");
    a.append(el("span", "who", "Final answer"), t.answer);
    body.append(a);
  }
  box.append(body);
  return box;
}

// betweenNode is an edit seen between two turns (AC8).
export function betweenNode(b, ctx) {
  const box = el("section", "card btwentry");
  box.id = `between-${b.after}`;
  box.setAttribute("aria-label", b.title || "Between turns");
  box.append(el("h2", "", `${b.title} · ${clock(b.at)}`), el("p", "", b.text));
  if ((b.effects || []).length) box.append(effectList(b.effects, ctx));
  return box;
}
