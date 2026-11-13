import { button, bytes, clock, dur, el, num, plural } from "./fmt.js";

// The right card (spec 016 AC9, AC10): the session's Files, Commands, Sites and Context
// as four tabs. Every row, count and sentence comes from the view; rows open the details
// dialog, and a model call opens the 013 drawer until 017.
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

// linkRow is a row linking a model call to the 013 drawer.
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
// labelText is an effect's attribution as the story shows it, the label's glyph and word,
// then the tool call or calls it points at: "● named · #10 Edit src/app.js".
const labelText = (e) => (e.mark ? `${e.mark.glyph} ${e.mark.word}` : "— no label") + (e.by ? ` · ${e.by}` : "");

const lines = (a, d) => {
  const s = el("span", "r");
  s.append(el("span", "a", `+${a}`), " ", el("span", "d", `−${d}`));
  return s;
};

function filesPanel(f, ctx) {
  const out = [el("p", "lead", f.coverage)];
  if (f.empty) {
    out.push(el("p", "muted-note pad", f.empty));
    return out;
  }
  out.push(el("h3", "", `Changed · ${(f.changed || []).length}`));
  out.push(rows((f.changed || []).map((it) => {
    const last = it.last || {};
    const [dir, name] = split(it.path);
    const right = it.lines ? lines(it.added, it.removed) : el("span", "r", last.size !== undefined && last.size !== null ? bytes(last.size) : "");
    const t2 = el("span", "t2", `${dirText(dir)} · ${[last.change, last.where].filter(Boolean).join(" ")} · `);
    t2.append(el("span", last.label === "outside" ? "warn" : "", labelText(last)));
    return row(ctx, { key: `file:${it.full_path}`, ic: icon((last.what || {}).glyph || "~"), t1: t1(name), r: right, t2, title: it.full_path });
  })));
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
  if (s.empty) out.push(el("p", "muted-note pad", s.empty));
  else {
    out.push(rows((s.items || []).map((it) => row(ctx, {
      key: `site:${it.host}`, ic: icon(it.harness ? "⚙" : "⇄"), t1: t1(it.port && it.port !== 443 ? `${it.host}:${it.port}` : it.host),
      r: el("span", "r", `↓ ${bytes(it.down ?? 0)}`),
      t2: el("span", "t2", [plural(it.connections, "connection"), it.where, it.who].filter(Boolean).join(" · ")),
    }))));
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
  return rows(list.map((r) => row(ctx, {
    href: ctx.callHref ? ctx.callHref(r.exchange_id) : "#",
    ic: icon("◆"), t1: t1(`#${r.n} · ${num(r.ctx)} tokens`), r: el("span", "r", signedTokens(r.delta)),
    t2: el("span", "t2", [r.turn ? `turn ${r.turn}` : "", r.why].filter(Boolean).join(" · ")),
  })));
}

// chart is the main thread's context as one bar per call, grouped by turn; a bar is
// scaled to its context over the peak.
function chart(c, ctx) {
  const box = el("div", "ctxchart");
  box.setAttribute("role", "group");
  box.setAttribute("aria-label", "Context per main-thread call, by turn");
  const groups = [];
  for (const r of c.main || []) {
    if (!groups.length || groups[groups.length - 1].turn !== (r.turn || 0)) groups.push({ turn: r.turn || 0, rows: [] });
    groups[groups.length - 1].rows.push(r);
  }
  for (const g of groups) {
    const ct = el("div", "ct");
    ct.style.flex = String(g.rows.length);
    const bars = el("div", "bars");
    for (const r of g.rows) {
      const a = el("a");
      a.href = ctx.callHref ? ctx.callHref(r.exchange_id) : "#";
      if (ctx.noteOpener) a.addEventListener("click", () => ctx.noteOpener(a));
      const f = c.peak > 0 && r.ctx !== null && r.ctx !== undefined ? r.ctx / c.peak : 0;
      a.style.transform = `scaleY(${f.toFixed(3)})`;
      a.title = `#${r.n} · ${num(r.ctx)} tokens`;
      a.setAttribute("aria-label", `call #${r.n}, ${num(r.ctx)} tokens`);
      bars.append(a);
    }
    ct.append(bars, el("span", "tl2", g.turn ? `T${g.turn}` : "·"));
    box.append(ct);
  }
  return box;
}

function contextPanel(c, ctx) {
  const out = [el("p", "lead", c.coverage)];
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
    out.push(chart(c, ctx));
    out.push(el("h3", "", `Main thread · ${plural(c.main.length, "call")}`), ctxRows(c.main, ctx));
  }
  for (const th of c.subs || []) out.push(el("h3", "", `${th.label} · its own context`), ctxRows(th.rows || [], ctx));
  if (c.note) out.push(el("p", "tabfoot", c.note));
  return out;
}

const PANELS = { files: filesPanel, commands: commandsPanel, sites: sitesPanel, context: contextPanel };
const SOURCE = { files: (v) => v.files, commands: (v) => v.commands, sites: (v) => v.sites, context: (v) => v.context };

export function createSideCard(ctx = {}) {
  const root = el("aside", "sidebar card");
  root.setAttribute("aria-label", "What this session touched");
  const list = el("div", "tabs");
  list.setAttribute("role", "tablist");
  list.setAttribute("aria-label", "Files, commands, sites and context");
  const scroll = el("div", "scroll");
  root.append(list, scroll);
  let selected = "files";
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

  TABS.forEach((spec, i) => {
    const btn = button("tab", spec.label, () => select(spec.id, true));
    btn.id = `tab-${spec.id}`;
    btn.setAttribute("role", "tab");
    btn.setAttribute("aria-controls", `tp-${spec.id}`);
    const count = el("b");
    btn.append(count);
    // arrow keys move between tabs (roving tabindex)
    btn.addEventListener("keydown", (e) => {
      const k = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: TABS.length - 1 }[e.key];
      if (k === undefined) return;
      if (e.preventDefault) e.preventDefault();
      select(TABS[(k + TABS.length) % TABS.length].id, true);
    });
    list.append(btn);
    tabs.set(spec.id, { btn, count, spec });
    const panel = el("div", "tabp");
    panel.id = `tp-${spec.id}`;
    panel.setAttribute("role", "tabpanel");
    panel.setAttribute("aria-labelledby", btn.id);
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
