import { button, bytes, clock, dur, el, num, plural, skeleton } from "./fmt.js";
import { diffNode } from "./dialog.js";

// The right card (spec 016 AC9, AC10): the session's Files, Commands, Sites and Context
// as four tabs. Every row, count and sentence comes from the view; rows open the details
// dialog, and a model call opens the call details dialog (spec 017 AC5).
// ctx: { open(key, opener), callHref(exchangeId), noteOpener(node) }.

const TABS = [
  { id: "files", label: "Files", count: (v) => (v.files.changed || []).length },
  { id: "commands", label: "Commands", count: (v) => (v.commands.items || []).length },
  { id: "sites", label: "Sites", count: (v) => (v.sites.items || []).length },
  { id: "context", label: "Context", count: () => null },
];

// split is a display path as its folder and name: "src/app.js" -> ["src/", "app.js"].
function split(p) {
  const s = String(p || "");
  const i = s.replace(/\/$/, "").lastIndexOf("/");
  return i < 0 ? ["", s] : [s.slice(0, i + 1), s.slice(i + 1)];
}
const dirText = (d) => d || "project root";

function icon(glyph, cls = "ic") {
  const s = el("span", cls, glyph);
  s.setAttribute("aria-hidden", "true");
  return s;
}

// linkRow is a row linking a model call to the call details dialog.
function linkRow(ctx, href) {
  const a = el("a", "row");
  a.href = href;
  if (ctx.noteOpener) a.addEventListener("click", () => ctx.noteOpener(a));
  return a;
}

// row is one list row: a button opening `key` in the dialog, or a link to a call.
function row(ctx, { key, href, ic, t1, r, t2, title }) {
  const li = el("li");
  const b = href !== undefined ? linkRow(ctx, href) : button("row", undefined, () => ctx.open && ctx.open(key, b));
  if (title) b.title = title;
  b.append(ic, t1, r || el("span", "r"), t2);
  li.append(b);
  return li;
}

const rows = (items) => {
  const ul = el("ul", "rows");
  ul.append(...items);
  return ul;
};
const t1 = (text, code = false) => {
  const s = el("span", "t1");
  if (code) s.append(el("code", "", text));
  else s.textContent = text;
  return s;
};

const lines = (a, d) => {
  const s = el("span", "r");
  s.append(el("span", "a", `+${a}`), " ", el("span", "d", `−${d}`));
  return s;
};

// sumLine is a tab's one-line summary, under its lead (027 C4: it was the overview's card).
function sumLine(...parts) {
  const p = el("p", "sum");
  p.append(...parts);
  return p;
}

const cap = (s) => (s ? s[0].toUpperCase() + s.slice(1) : "");
const where = (e) => e.where || (e.turn ? `turn ${e.turn}` : "");

// changeBody is one change of a file, filled once loaded: its diff, else its content when
// no earlier copy was kept, else why there is none (the dialog's file view says the same).
function changeBody(e, path, ctx) {
  const box = el("div", "change-body");
  const note = (t) => box.replaceChildren(el("p", "muted-note", t));
  const fail = (err) => note(`Could not load it: ${(err && err.message) || "request failed"}`);
  if (e.has_diff && ctx.loadDiff) {
    box.replaceChildren(skeleton("Loading the diff…", 4, "lines"));
    Promise.resolve().then(() => ctx.loadDiff(e.id)).then((d) => {
      box.replaceChildren(d.diff ? diffNode(d.diff, path) : el("p", "muted-note", "No difference."));
      if (d.truncated) box.append(el("p", "muted-note", "The diff is cut short."));
    }, fail);
  } else if (e.has_copy && ctx.loadContent) {
    box.replaceChildren(skeleton("Loading the content…", 4, "lines"));
    Promise.resolve().then(() => ctx.loadContent(e.id)).then((c) => {
      if (c.valid === false) return note("Binary content: not shown.");
      box.replaceChildren(el("p", "muted-note", "No earlier copy: the full content."), el("pre", "content", c.content || ""));
    }, fail);
  } else if (e.change === "deleted") {
    note("The file was deleted.");
  } else {
    note("No copy of this version is kept, so there is no diff.");
  }
  return box;
}

// fileRow is a changed file: its name and its lines added and removed (its size when the
// lines are not known), the full path on hover (research 2026-11-13); it expands in place to
// its changes and their diffs (027 C4), and "Details" opens its dialog. `scope` keeps the
// same file in two turn groups apart.
function fileRow(it, ctx, scope) {
  const last = it.last || {};
  const [, name] = split(it.path);
  const right = it.lines ? lines(it.added, it.removed) : el("span", "r", last.size !== undefined && last.size !== null ? bytes(last.size) : "");
  const li = el("li", "file-item");
  const body = el("div", "file-changes");
  const key = `${scope}:${it.full_path}`;
  body.id = `fc-${key}`;
  let filled = false;
  const set = (open) => {
    body.hidden = !open;
    b.setAttribute("aria-expanded", String(open));
    if (open) ctx.openFiles.add(key);
    else ctx.openFiles.delete(key);
    if (!open || filled) return;
    filled = true;
    for (const e of it.changes && it.changes.length ? it.changes : [last]) {
      const head = el("div", "change-head");
      head.append(el("span", "", `${cap(e.change)}${where(e) ? ` in ${where(e)}` : ""}`), el("span", "muted", e.at ? clock(e.at) : ""));
      if (e.by) head.append(el("span", "muted", e.by));
      body.append(head, changeBody(e, it.path, ctx));
    }
    const more = button("linkish small", "Details", () => ctx.open && ctx.open(`file:${it.full_path}`, more));
    body.append(more);
  };
  const b = button("row file", undefined, () => set(body.hidden));
  b.title = it.full_path;
  b.setAttribute("aria-controls", body.id);
  const chev = el("span", "chev", "▸");
  chev.setAttribute("aria-hidden", "true");
  b.append(chev, t1(name), right);
  li.append(b, body);
  set(ctx.openFiles.has(key));
  return li;
}

function filesPanel(f, ctx) {
  const out = [el("p", "lead", f.coverage)];
  if (f.sum) out.push(sumLine(el("span", "a", `+${f.sum.added ?? 0}`), " ", el("span", "d", `−${f.sum.removed ?? 0}`), ` lines · ${f.sum.read ?? 0} read`));
  if (f.empty) {
    out.push(el("p", "muted-note pad", f.empty));
    return out;
  }
  // Changed is by file or by turn (027 C5): the server sends both, the switch only picks one
  const head = el("h3", "with-seg", `Changed · ${(f.changed || []).length}`);
  const box = el("div", "changed");
  const draw = () => {
    if (ctx.filesBy.mode === "turn") {
      // newest turn first, like the story (027 newest-first)
      const groups = [...(f.by_turn || [])].reverse();
      box.replaceChildren(...groups.flatMap((g) => [
        el("h4", "fgroup", `${g.label} · ${plural((g.files || []).length, "file")}`),
        rows((g.files || []).map((it) => fileRow(it, ctx, `t${g.turn || 0}-${g.label}`))),
      ]));
    } else {
      box.replaceChildren(rows((f.changed || []).map((it) => fileRow(it, ctx, "all"))));
    }
  };
  if ((f.by_turn || []).length > 1) {
    const seg = el("div", "seg-group");
    seg.setAttribute("role", "group");
    seg.setAttribute("aria-label", "Show changed files");
    const opts = [["file", "By file"], ["turn", "By turn"]].map(([mode, text]) => {
      const b = button("seg", text, () => {
        ctx.filesBy.mode = mode;
        for (const [m, o] of opts) o.setAttribute("aria-pressed", String(m === mode));
        draw();
      });
      return [mode, b];
    });
    for (const [m, o] of opts) {
      o.setAttribute("aria-pressed", String(m === ctx.filesBy.mode));
      seg.append(o);
    }
    head.append(seg);
  } else {
    ctx.filesBy.mode = "file";
  }
  draw();
  out.push(head, box);
  out.push(el("h3", "", `Read · ${(f.read || []).length}`));
  out.push(rows((f.read || []).map((it) => {
    const [dir, name] = split(it.path);
    return row(ctx, {
      key: `file:${it.full_path}`, ic: icon("→"), t1: t1(name), r: el("span", "r", it.count > 1 ? `${it.count}×` : ""),
      t2: el("span", "t2", [dirText(dir), it.where].filter(Boolean).join(" · ")), title: it.full_path,
    });
  })));
  out.push(el("h3", "", `Searched · ${(f.searches || []).length}`));
  out.push(rows((f.searches || []).map((it) => row(ctx, {
    key: `tool:${it.tool_id}`, ic: icon("⌕"), t1: t1(it.summary || it.name, Boolean(it.summary)), r: el("span", "r", it.name),
    t2: el("span", "t2", [it.where, it.agent && `sub-agent ${it.agent}`].filter(Boolean).join(" · ")),
  }))));
  return out;
}

function commandsPanel(c, ctx) {
  const out = [el("p", "lead", c.coverage)];
  if (c.sum) {
    // the longest command on one line, cut with an ellipsis; the whole command on hover
    const longest = el("span", "one-line", c.sum.longest ? `longest ${dur(c.sum.longest_ms)} · ${c.sum.longest}` : "none ran");
    if (c.sum.longest) longest.title = c.sum.longest;
    out.push(sumLine(longest));
  }
  if (c.empty) {
    out.push(el("p", "muted-note pad", c.empty));
    return out;
  }
  out.push(rows((c.items || []).map((it) => {
    const ok = it.chip === "ok";
    const ic = el("span", `ic ${ok ? "ok" : it.chip === "failed" ? "bad" : ""}`.trim(), it.state.glyph);
    ic.setAttribute("aria-label", it.state.word);
    const time = it.dur_ms === undefined || it.dur_ms === null ? "—" : `${it.approx ? "≈" : ""}${dur(it.dur_ms)}`;
    const t2 = [it.turn ? `turn ${it.turn}` : "", it.did, it.hooks_only ? "seen by hooks only" : ""].filter(Boolean).join(" · ");
    return row(ctx, { key: `tool:${it.tool_id}`, ic, t1: t1(it.summary || "(no command text)", Boolean(it.summary)), r: el("span", "r", time), t2: el("span", "t2", t2) });
  })));
  return out;
}

function sitesPanel(s, ctx) {
  const out = [el("p", "lead", s.coverage)];
  if (s.sum) out.push(sumLine(plural(s.sum.connections ?? 0, "connection")));
  if (s.empty) out.push(el("p", "muted-note pad", s.empty));
  else {
    // a site is its host alone, like a file's row; connections, bytes, turns and tools
    // are in its dialog (017 AC17)
    out.push(rows((s.items || []).map((it) => {
      const li = el("li");
      const b = button("row file site", undefined, () => ctx.open && ctx.open(`site:${it.host}`, b));
      b.append(t1(it.port && it.port !== 443 ? `${it.host}:${it.port}` : it.host), el("span", "r"));
      li.append(b);
      return li;
    })));
  }
  const u = s.unassigned;
  if (u && u.count) {
    const p = el("p", "tabfoot", "Not in this session: ");
    const a = el("a", "", plural(u.count, "unassigned connection"));
    a.href = "#/effects/unassigned";
    p.append(a, ` to ${u.host} at ${clock(u.at)}${u.label ? `, ${u.label}` : ""}.`);
    out.push(p);
  }
  return out;
}

const signedTokens = (n) => (n === null || n === undefined ? "—" : `${n >= 0 ? "+" : "−"}${num(Math.abs(n))}`);

function ctxRows(list, ctx) {
  const ul = rows(list.map((r) => {
    const li = row(ctx, {
      href: ctx.callHref ? ctx.callHref(r.exchange_id) : "#",
      ic: icon("◆"), t1: t1(`#${r.n} · ${num(r.ctx)} tokens`), r: el("span", "r", signedTokens(r.delta)),
      t2: el("span", "t2", [r.turn ? `turn ${r.turn}` : "", r.why].filter(Boolean).join(" · ")),
    });
    li.dataset.turn = String(r.turn || 0);
    return li;
  }));
  return ul;
}

// LABELS is how many turn labels fit under the chart; past that every k-th is shown, the
// first and last always.
const LABELS = 24;

// chart is the main thread's context as one bar per turn (Karthik 2026-11-13, replacing
// one bar per call): the bar is the context at the turn's last call over the peak, its
// darker cap what the turn added since its first call. A bar shows that turn's calls in
// the list below.
function chart(c, ctx, pick) {
  const box = el("div", "ctxchart");
  box.setAttribute("role", "group");
  box.setAttribute("aria-label", "Context at the end of each turn");
  const groups = [];
  for (const r of c.main || []) {
    if (!groups.length || groups[groups.length - 1].turn !== (r.turn || 0)) groups.push({ turn: r.turn || 0, rows: [] });
    groups[groups.length - 1].rows.push(r);
  }
  const every = Math.ceil(groups.length / LABELS);
  groups.forEach((g, i) => {
    const known = g.rows.filter((r) => r.ctx !== null && r.ctx !== undefined);
    const last = known.length ? known[known.length - 1].ctx : null;
    const first = known.length ? known[0].ctx : null;
    const ct = el("div", "ct");
    const bars = el("div", "bars");
    const name = g.turn ? `Turn ${g.turn}` : "Before the first prompt";
    const grew = last !== null && first !== null ? last - first : null;
    const b = button("tbar", undefined, () => pick(g.turn));
    b.style.transform = `scaleY(${(c.peak > 0 && last !== null ? last / c.peak : 0).toFixed(3)})`;
    if (grew > 0 && last > 0) {
      // the cap and the rest share the bar's height in proportion to their tokens
      const cap = el("span", "cap");
      cap.style.flexGrow = String(grew);
      const rest = el("span", "rest");
      rest.style.flexGrow = String(last - grew);
      b.append(cap, rest);
    }
    b.setAttribute("aria-label", `${name}: ${num(last)} tokens at its end${grew ? `, ${signedTokens(grew)} inside the turn` : ""}`);
    bars.append(b);
    const label = i === 0 || i === groups.length - 1 || i % every === 0 ? (g.turn ? `T${g.turn}` : "·") : "";
    ct.append(bars, el("span", "tl2", label));
    box.append(ct);
  });
  return box;
}

function contextPanel(c, ctx) {
  const out = [el("p", "lead", c.coverage)];
  const tk = c.tokens;
  if (tk) out.push(sumLine(`Tokens: ${num(tk.input)} input · ${num(tk.output)} output · ${num(tk.cache_write)} cache write · ${num(tk.cache_read)} cache read${tk.incomplete ? " (some calls did not report)" : ""}`));
  const facts = el("div", "mfacts ctxfacts");
  const fact = (l, v) => {
    const f = el("div", "mfact");
    f.append(el("span", "l", l), el("span", "v", v));
    facts.append(f);
  };
  fact("Now", num(c.now));
  fact("First call", num(c.first));
  fact("Grew by", signedTokens(c.growth));
  out.push(facts);
  if ((c.main || []).length) {
    const list = ctxRows(c.main, ctx);
    const pick = (turn) => {
      const li = Array.from(list.children).find((x) => x.dataset.turn === String(turn || 0));
      if (!li) return;
      if (typeof li.scrollIntoView === "function") li.scrollIntoView({ block: "start", behavior: "smooth" });
      if (li.classList) {
        li.classList.remove("flash");
        void li.offsetWidth;
        li.classList.add("flash");
      }
    };
    out.push(chart(c, ctx, pick));
    out.push(el("h3", "", `Main thread · ${plural(c.main.length, "call")}`), list);
  }
  for (const th of c.subs || []) out.push(el("h3", "", `${th.label} · its own context`), ctxRows(th.rows || [], ctx));
  if (c.note) out.push(el("p", "tabfoot", c.note));
  return out;
}

const PANELS = { files: filesPanel, commands: commandsPanel, sites: sitesPanel, context: contextPanel };
// each tab draws its part of the view and, under its lead, the overview's summary of it (027 C4)
const ov = (v) => v.overview || {};
const SOURCE = {
  files: (v) => v.files && { ...v.files, sum: ov(v).files },
  commands: (v) => v.commands && { ...v.commands, sum: ov(v).commands },
  sites: (v) => v.sites && { ...v.sites, sum: ov(v).sites },
  context: (v) => v.context && { ...v.context, tokens: ov(v).tokens },
};

// ctx.lead, when given, is a first tab the page owns: { id, label, count(view), el }. Its
// panel is ctx.lead.el, never redrawn here (spec 027 C4: the story is the first tab).
export function createSideCard(ctx = {}) {
  // the files expanded in place, and by file or by turn, stay as they were when a live update
  // redraws the Files tab
  ctx = { ...ctx, openFiles: new Set(), filesBy: { mode: "file" } };
  const lead = ctx.lead || null;
  const specs = lead ? [lead, ...TABS] : TABS;
  const root = el(lead ? "section" : "aside", lead ? "sesstabs" : "sidebar card");
  root.setAttribute("aria-label", lead ? "The session" : "What this session touched");
  const list = el("div", "tabs");
  list.setAttribute("role", "tablist");
  list.setAttribute("aria-label", lead ? `${lead.label}, files, commands, sites and context` : "Files, commands, sites and context");
  const scroll = el("div", lead ? "tabpanels" : "scroll");
  root.append(list, scroll);
  let selected = lead ? lead.id : "files";
  const tabs = new Map();
  const panels = new Map();
  const sigs = new Map();

  function select(id, focus = false) {
    if (!tabs.has(id)) return;
    selected = id;
    for (const [tid, t] of tabs) {
      const on = tid === id;
      t.btn.setAttribute("aria-selected", String(on));
      t.btn.tabIndex = on ? 0 : -1;
      panels.get(tid).hidden = !on;
      if (on && focus && typeof t.btn.focus === "function") t.btn.focus();
    }
  }

  specs.forEach((spec, i) => {
    const btn = button("tab", spec.label, () => select(spec.id, true));
    btn.id = `tab-${spec.id}`;
    btn.setAttribute("role", "tab");
    btn.setAttribute("aria-controls", `tp-${spec.id}`);
    const count = el("b");
    btn.append(count);
    // arrow keys move between tabs (roving tabindex)
    btn.addEventListener("keydown", (e) => {
      const k = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: specs.length - 1 }[e.key];
      if (k === undefined) return;
      if (e.preventDefault) e.preventDefault();
      select(specs[(k + specs.length) % specs.length].id, true);
    });
    list.append(btn);
    tabs.set(spec.id, { btn, count, spec });
    const panel = el("div", "tabp");
    panel.id = `tp-${spec.id}`;
    panel.setAttribute("role", "tabpanel");
    panel.setAttribute("aria-labelledby", btn.id);
    if (spec === lead) panel.append(lead.el);
    scroll.append(panel);
    panels.set(spec.id, panel);
  });
  select(selected);

  return {
    el: root,
    // update redraws a tab only when its part of the view changed, so a live update keeps
    // focus and scroll inside unchanged tabs.
    update(view) {
      if (!view) return;
      for (const [id, t] of tabs) {
        if (t.spec === lead) {
          const n = lead.count(view);
          t.count.textContent = n === null || n === undefined ? "" : String(n);
          continue;
        }
        const data = SOURCE[id](view) || {};
        const n = t.spec.count({ ...view, files: view.files || {}, commands: view.commands || {}, sites: view.sites || {} });
        t.count.textContent = n === null ? "" : String(n);
        const sig = JSON.stringify(data);
        if (sigs.get(id) === sig) continue;
        sigs.set(id, sig);
        panels.get(id).replaceChildren(...PANELS[id](data, ctx));
      }
    },
    select,
    selected: () => selected,
  };
}
