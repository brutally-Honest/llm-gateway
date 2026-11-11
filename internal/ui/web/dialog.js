import { button, bytes, clock, dur, el, markText, plural } from "./fmt.js";
import { hookInputEntry, omittedNote, prettyJSON } from "./entries.js";
import { labelBadge } from "./story.js";
import { copyButton } from "./tool-card.js";

// The one details dialog (spec 016 AC11): a file, a tool call (a command is one) or a site,
// in one shape — kicker, title, labelled facts, why in words, the diff, content, output or
// input in full width, related links with Back, and "Show in turn N". Facts come from the
// view; a tool's input and result, and a change's diff or content, are fetched on open.
// ctx: { getView(), loadTool(id), loadDiff(effectId), loadContent(effectId), callHref(exchangeId),
// place(elementId) }.

// findTool is a tool line in the view with where it sits: its turn and the step that asked.
export function findTool(view, id) {
  const walk = (steps, turn) => {
    for (const s of steps || []) {
      for (const l of s.tools || []) {
        if (l.id === id) return { line: l, turn, step: s };
        const sub = l.sub ? walk(l.sub.steps, turn) : null;
        if (sub) return sub;
      }
    }
    return null;
  };
  for (const t of (view && view.turns) || []) {
    const hit = walk(t.steps, t.n);
    if (hit) return hit;
    for (const l of t.hook_tools || []) if (l.id === id) return { line: l, turn: t.n, step: null };
  }
  return null;
}

const where = (e) => e.where || (e.turn ? `turn ${e.turn}` : "");
const cap = (s) => (s ? s[0].toUpperCase() + s.slice(1) : "");

function fact(l, v) {
  const f = el("div", "mfact");
  const val = el("span", "v");
  if (v && typeof v === "object") val.append(v);
  else val.textContent = v === undefined || v === null || v === "" ? "—" : String(v);
  f.append(el("span", "l", l), val);
  return f;
}
const facts = (...list) => {
  const box = el("div", "mfacts");
  box.append(...list.filter(Boolean));
  return box;
};
function section(title, sub, ...body) {
  const s = el("section", "ms");
  const h = el("h3", "", title);
  if (sub) {
    const span = el("span", "sub");
    span.append(...(Array.isArray(sub) ? sub : [sub]));
    h.append(span);
  }
  s.append(h, ...body.filter(Boolean));
  return s;
}
const lines = (a, d) => {
  const s = el("span");
  s.append(el("span", "a", `+${a}`), " ", el("span", "d", `−${d}`));
  return s;
};
function block(text, cls = "") {
  const box = el("div", cls);
  const pre = el("pre", "", text);
  box.append(copyButton(() => text), pre);
  return box;
}
function rawLink(exchangeId, text = "cut short — open the raw exchange") {
  const a = el("a", "truncated", text);
  a.href = `#/exchanges/${encodeURIComponent(exchangeId)}`;
  return a;
}

// diffNode renders a unified diff: added and removed lines marked by glyph and colour.
function diffNode(text, path) {
  const box = el("div", "diff");
  box.setAttribute("role", "group");
  box.setAttribute("aria-label", `diff of ${path}`);
  for (const line of String(text || "").split("\n")) {
    if (line.startsWith("+++") || line.startsWith("---")) continue;
    const k = line.startsWith("@@") ? "hunk" : line[0] === "+" ? "add" : line[0] === "-" ? "del" : "ctx";
    const row = el("div", k);
    const g = el("span", "dg", k === "add" ? "+" : k === "del" ? "−" : " ");
    g.setAttribute("aria-hidden", "true");
    row.append(g, k === "hunk" ? line : line.slice(1));
    box.append(row);
  }
  return box;
}

export function createDialog(ctx = {}) {
  const root = el("dialog", "details");
  root.setAttribute("aria-labelledby", "dlg-title");
  const head = el("header", "mh");
  const back = button("x", "←", () => goBack());
  back.setAttribute("aria-label", "Back");
  const tx = el("div", "tx");
  const kick = el("span", "kick");
  const title = el("h2");
  title.id = "dlg-title";
  const subt = el("span", "subt");
  tx.append(kick, title, subt);
  const x = button("x", "×", () => close());
  x.setAttribute("aria-label", "Close");
  head.append(back, tx, x);
  const body = el("div", "mb");
  const foot = el("footer", "mf");
  const closeBtn = button("btn", "Close", () => close());
  const goBtn = button("btn primary", "", () => goto && showInTurn(goto.id));
  foot.append(el("span", "muted-note grow", "Esc closes"), closeBtn, goBtn);
  root.append(head, body, foot);

  let stack = []; // keys, the current one last: "file:<full path>", "tool:<id>", "site:<host>"
  let opener = null;
  let goto = null;
  let shown = false;
  const lineSigs = new Map(); // "tool:<id>" -> its line as last drawn
  const fetched = new Map(); // "tool:<id>" | "diff:<id>" | "content:<id>" -> { state, data, error }

  const view = () => (ctx.getView ? ctx.getView() : null) || {};

  // load fetches once per key and redraws when it lands, if still on screen.
  function load(key, fn) {
    const have = fetched.get(key);
    if (have) return have;
    const entry = { state: "loading", data: null, error: "" };
    fetched.set(key, entry);
    Promise.resolve().then(fn).then(
      (data) => Object.assign(entry, { state: "ready", data }),
      (err) => Object.assign(entry, { state: "error", error: (err && err.message) || "request failed" }),
    ).then(() => {
      if (shown) draw(true);
    });
    return entry;
  }
  const loading = (entry, what) => (entry.state === "loading" ? el("p", "muted-note", `Loading ${what}…`) : el("p", "muted-note", `${cap(what)} unavailable: ${entry.error}`));

  // related is a link inside the dialog to another file, site or tool: it pushes, Back pops.
  function related(key, text, code = true) {
    const b = button("linkish", undefined, () => push(key));
    if (code) b.append(el("code", "", text));
    else b.textContent = text;
    return b;
  }
  function callLink(exchangeId, text) {
    const a = el("a", "linkish", text);
    a.href = ctx.callHref ? ctx.callHref(exchangeId) : "#";
    return a;
  }
  const showBtn = (id, label) => button("btn sm", `${label} →`, () => showInTurn(id));

  function whyNode(e) {
    const w = el("div", "why2");
    w.append(labelBadge(e), el("span", "", e.why || "No attribution was recorded for this effect."));
    if (e.tool) w.append(related(`tool:${e.tool}`, e.by || "Open the tool call", false));
    return w;
  }

  function changeBody(e, path) {
    if (e.has_diff) {
      const d = load(`diff:${e.id}`, () => ctx.loadDiff(e.id));
      if (d.state !== "ready") return loading(d, "diff");
      const box = el("div");
      box.append(d.data.diff ? diffNode(d.data.diff, path) : el("p", "muted-note", "No difference."));
      if (d.data.truncated) box.append(el("p", "muted-note", "The diff is cut short."));
      return box;
    }
    if (e.has_copy) {
      const c = load(`content:${e.id}`, () => ctx.loadContent(e.id));
      if (c.state !== "ready") return loading(c, "content");
      if (c.data.valid === false) return el("p", "muted-note", "Binary content: not shown.");
      const box = el("div");
      box.append(el("p", "muted-note", "No earlier copy: the full content."), block(c.data.content || ""));
      return box;
    }
    if (e.change === "deleted") return el("p", "muted-note", "The file was deleted.");
    return el("p", "muted-note", "No copy of this version is kept, so there is no diff.");
  }

  function fileModel(full) {
    const v = view();
    const f = v.files || {};
    const item = (f.changed || []).find((x) => x.full_path === full);
    const read = (f.read || []).find((x) => x.full_path === full);
    if (!item && !read) return null;
    const path = (item || read).path;
    const changes = item ? item.changes || [] : [];
    const reads = item && (item.reads || []).length ? item.reads : read ? read.reads || [] : [];
    const last = item ? item.last || changes[changes.length - 1] || {} : {};
    const out = [facts(
      fact("Changed", changes.length ? plural(changes.length, "time") : "never"),
      item && item.lines ? fact("Lines", lines(item.added, item.removed)) : null,
      item && last.size !== undefined && last.size !== null ? fact("Size now", bytes(last.size)) : null,
      fact("Read", reads.length ? plural(reads.length, "time") : "never"),
    )];
    for (const e of changes) {
      out.push(section(`${cap(e.change)} in ${where(e)}`, [`${clock(e.at)} `, showBtn(`eff-${e.id}`, `Show in ${where(e)}`)], whyNode(e), changeBody(e, path)));
    }
    if (reads.length) {
      const ul = el("ul", "mlist");
      for (const e of reads) {
        const li = el("li");
        li.append(el("b", "", cap(where(e))), el("span", "muted", [e.detail, clock(e.at)].filter(Boolean).join(" · ")));
        if (e.tool) li.append(related(`tool:${e.tool}`, e.by || "the tool call", false));
        li.append(showBtn(`eff-${e.id}`, "Show"));
        ul.append(li);
      }
      out.push(section("Reads", "", ul));
    }
    const first = changes[0] || reads[0];
    return {
      kick: changes.length ? "File changed" : "File read", title: path, code: true, subt: full, body: out,
      goto: first ? { id: `eff-${first.id}`, label: `Show in ${where(first)}` } : null,
    };
  }

  function touched(list) {
    if (!(list || []).length) return el("p", "muted-note", "Nothing seen on disk or on the network.");
    const ul = el("ul", "mlist");
    for (const e of list) {
      const li = el("li");
      const target = e.kind === "net" ? `site:${e.host}` : `file:${e.full_path || e.path}`;
      li.append(labelBadge(e), el("span", "", markText(e.what)), related(target, e.kind === "net" ? `${e.host}:${e.port}` : e.path));
      if (e.kind === "change" && e.added >= 0) li.append(lines(e.added, e.removed));
      else if (e.kind === "net") li.append(el("span", "muted", `↑ ${bytes(e.up ?? 0)} ↓ ${bytes(e.down ?? 0)}`));
      ul.append(li);
    }
    return ul;
  }

  function toolModel(id) {
    const hit = findTool(view(), id);
    if (!hit) return null;
    const l = hit.line;
    const d = load(`tool:${id}`, () => ctx.loadTool(id));
    const det = d.state === "ready" ? d.data : null;
    const isCmd = l.kind === "command";
    const status = el("span", `st st-${l.chip}`, markText(l.state));
    const asked = hit.step ? callLink(hit.step.exchange_id, `model call #${hit.step.n}`) : "seen by hooks only";
    const out = [facts(
      fact("Result", status),
      fact("Ran for", l.dur_ms === undefined || l.dur_ms === null ? "—" : `${l.approx ? "≈" : ""}${dur(l.dur_ms)}`),
      fact("Asked by", asked),
      l.agent ? fact("Agent", l.agent) : null,
    )];
    if (l.first_error) out.push(el("p", "why", l.first_error));
    const hooks = [];
    if (det) {
      hooks.push(facts(fact("State", det.state || l.state.word), fact("Reason", det.reason), fact("Outcome", det.outcome)));
      if ((det.hooks || []).length) {
        const ul = el("ul", "mlist");
        for (const h of det.hooks) ul.append(el("li", "", `${clock(h.at)} · ${h.phase}${h.name ? ` ${h.name}` : ""}`));
        hooks.push(ul);
      } else {
        hooks.push(el("p", "muted-note", "No hook saw this call."));
      }
    } else {
      hooks.push(loading(d, "details"));
    }
    out.push(section("Hooks", "", ...hooks));
    out.push(section("What it touched", "", touched(l.effects)));
    if (l.sub) out.push(section(`Sub-agent ${l.sub.label}`, "", el("p", "muted-note", `${plural(l.sub.calls, "model call")}; its tool calls carry their own effects.`)));
    if (det) {
      const result = det.result === null || det.result === undefined ? null : det.result;
      const res = result === null ? el("p", "muted-note", "No result was captured.") : block(result, det.is_error ? "errbox" : "");
      if (result !== null && det.result_truncated && det.result_exchange_id) res.append(rawLink(det.result_exchange_id));
      out.push(section(isCmd ? "Output" : "Result", "", res));
      const input = block(prettyJSON(det.input || ""));
      if (det.input_truncated && det.exchange_id) input.append(rawLink(det.exchange_id));
      out.push(section("Input", "", input));
      if (det.input_differs) {
        const call = hookInputEntry(det);
        const hook = block(det.hook_input || "", "hook-input");
        // the note follows the hook input's own truncation; it has no raw link
        if (call.truncated) hook.append(el("p", "muted-note", omittedNote(det.hook_omitted_bytes)));
        out.push(section("Hook input", "differs from the model's", hook));
      }
    }
    const kickParts = [isCmd ? "Command" : "Tool call"];
    if (hit.turn) kickParts.push(`turn ${hit.turn}`);
    if (l.agent) kickParts.push(`sub-agent ${l.agent}`);
    return {
      kick: kickParts.join(" · "), title: isCmd && l.summary ? `$ ${l.summary}` : [l.name, l.summary].filter(Boolean).join(" "), code: isCmd, subt: "",
      body: out, goto: { id: `tool-${l.id}`, label: hit.turn ? `Show in turn ${hit.turn}` : "Show" },
    };
  }

  function siteModel(host) {
    const s = ((view().sites || {}).items || []).find((x) => x.host === host);
    if (!s) return null;
    const conns = s.conns || [];
    const up = conns.reduce((a, e) => a + (e.up || 0), 0);
    const out = [facts(fact("Connections", s.connections), fact("Downloaded", bytes(s.down ?? 0)), fact("Uploaded", bytes(s.up ?? up)), fact("Port", s.port), fact("Who", s.who)),
      el("p", "muted-note", "HTTPS: the gateway sees host, bytes and timing, never the content.")];
    for (const e of conns) {
      out.push(section(`Connection in ${where(e) || "this session"}`, [`${clock(e.at)} `, showBtn(`eff-${e.id}`, `Show in ${where(e) || "the story"}`)], whyNode(e),
        facts(fact("Outcome", markText(e.what)), fact("Open for", e.dur_ms === undefined || e.dur_ms === null ? "—" : dur(e.dur_ms)), fact("Bytes", `↑ ${bytes(e.up ?? 0)} ↓ ${bytes(e.down ?? 0)}`))));
    }
    const first = conns[0];
    return {
      kick: "Site", title: s.host, code: false, subt: `${plural(conns.length || s.connections, "connection")} in this session`, body: out,
      goto: first ? { id: `eff-${first.id}`, label: `Show in ${where(first) || "the story"}` } : null,
    };
  }

  function model(key) {
    const i = key.indexOf(":");
    const kind = key.slice(0, i);
    const id = key.slice(i + 1);
    if (kind === "file") return fileModel(id);
    if (kind === "tool") return toolModel(id);
    if (kind === "site") return siteModel(id);
    return null;
  }

  // draw renders the current key; keep holds the body's scroll (a live update or a fetch).
  function draw(keep = false) {
    const key = stack[stack.length - 1];
    const m = model(key) || { kick: "Details", title: key.slice(key.indexOf(":") + 1), subt: "", body: [el("p", "muted-note", "This item is no longer in the session view.")], goto: null };
    const top = body.scrollTop;
    back.hidden = stack.length < 2;
    kick.textContent = m.kick;
    title.replaceChildren(m.code ? el("code", "", m.title) : m.title);
    subt.textContent = m.subt || "";
    subt.hidden = !m.subt;
    body.replaceChildren(...m.body);
    goto = m.goto;
    goBtn.hidden = !goto;
    goBtn.textContent = goto ? `${goto.label} →` : "";
    if (keep) body.scrollTop = top;
  }

  function push(key) {
    stack.push(key);
    draw();
    body.scrollTop = 0;
  }
  function goBack() {
    if (stack.length < 2) return;
    stack.pop();
    draw();
  }

  function finish() {
    if (!shown) return;
    shown = false;
    stack = [];
    const o = opener;
    opener = null;
    if (o && o.isConnected !== false && typeof o.focus === "function") o.focus();
  }
  root.addEventListener("close", finish);
  function close() {
    if (!shown) return;
    if (typeof root.close === "function" && root.open) root.close();
    else root.open = false;
    finish();
  }

  // showInTurn closes the dialog, then scrolls the story to the row and highlights it.
  function showInTurn(id) {
    close();
    if (ctx.place) ctx.place(id);
  }

  return {
    el: root,
    // open shows a key; from inside an open dialog it is a related link (Back returns).
    open(key, from) {
      if (shown) {
        push(key);
        return;
      }
      stack = [key];
      opener = from || null;
      shown = true;
      draw();
      if (typeof root.showModal === "function" && root.isConnected !== false) {
        try {
          root.showModal();
        } catch {
          root.open = true;
        }
      } else {
        root.open = true;
      }
      body.scrollTop = 0;
      if (typeof x.focus === "function") x.focus();
    },
    // update keeps an open dialog on its item: a live view redraws it in place, and a
    // tool call whose line changed (it finished, or its result arrived) is fetched again.
    update() {
      if (!shown) return;
      const key = stack[stack.length - 1];
      if (key.startsWith("tool:")) {
        const hit = findTool(view(), key.slice(5));
        const lineSig = hit ? JSON.stringify(hit.line) : "";
        if (lineSigs.has(key) && lineSigs.get(key) !== lineSig) fetched.delete(key);
        lineSigs.set(key, lineSig);
      }
      draw(true);
    },
    isOpen: () => shown,
    close,
    current: () => stack[stack.length - 1] || "",
  };
}
