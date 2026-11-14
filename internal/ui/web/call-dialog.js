import { button, clock, dur, el, num, signed } from "./fmt.js";
import { textBlock, toolNode } from "./story.js";

// The call details dialog (spec 017 AC5–AC11, AC21): one model call in the 016 dialog shape —
// a native <dialog> opened with showModal(), so the page behind is dimmed and inert. Its top
// (title, facts, the context line and the message counts) stays while the sections scroll.
// The server builds every fact (GET /api/sessions/{id}/calls/{eid}); this only lays them out.
//
// The URL is the page's: nav is { push(route), replace(route), back() } and the dialog asks
// for hash changes through it; the page feeds the route's call id back through sync(). The
// dialog keys on the exchange id, so live data never moves it off its call; its head and
// step buttons are built once, so focus survives every redraw.
// ctx: { nav, loadCall(exchangeId), openTool(toolId, opener), turnHeading(), onMissing() }.

const plainCount = (n) => (n === null || n === undefined ? "—" : String(n));

function fact(label, value, cls = "") {
  const f = el("div", "mfact");
  const v = el("span", `v ${cls}`.trim());
  if (value && typeof value === "object") v.append(value);
  else v.textContent = value === undefined || value === null || value === "" ? "—" : String(value);
  f.append(el("span", "l", label), v);
  return f;
}
function section(title, sub, ...body) {
  const s = el("section", "ms");
  const h = el("h3", "", title);
  if (sub) h.append(el("span", "sub", sub));
  s.append(h, ...body.filter(Boolean));
  return s;
}
function link(href, text, blank = false) {
  const a = el("a", "linkish", text);
  a.href = href;
  if (blank) a.target = "_blank";
  return a;
}

// changeText is the context change since the previous call of its thread, in words.
export function changeText(c) {
  if (c.first) return "first call of its thread";
  if (c.delta === null || c.delta === undefined) return "—";
  return `${signed(c.delta)}${c.prev_n ? ` since #${c.prev_n}` : ""}`;
}

// sawText is the message counts line: carried over and new, or a thread's first call.
export function sawText(s) {
  if (s.carried === null || s.carried === undefined) return "Messages: —";
  if (s.first) return `First call of its thread · ${s.new} new`;
  return `${s.carried} carried over · ${s.new} new`;
}

export function createCallDialog(ctx = {}) {
  const nav = ctx.nav || { push() {}, replace() {}, back() {} };
  const root = el("dialog", "details call-details");
  root.setAttribute("aria-labelledby", "call-title");
  const head = el("header", "mh");
  const tx = el("div", "tx");
  const kick = el("span", "kick", "Model call");
  const title = el("h2", "", "Call");
  title.id = "call-title";
  title.tabIndex = -1;
  const subt = el("span", "subt");
  tx.append(kick, title, subt);
  const prev = button("btn sm", "↑ Previous", () => step(-1));
  const next = button("btn sm", "↓ Next", () => step(1));
  const x = button("x", "×", () => close());
  x.setAttribute("aria-label", "Close call");
  head.append(tx, prev, next, x);
  // the top (facts, context line, message counts) stays; the sections scroll under it
  const top = el("div", "call-top");
  const body = el("div", "mb");
  const foot = el("footer", "mf");
  foot.append(el("span", "muted-note grow", "↑ ↓ step through calls · Esc closes"), button("btn", "Close", () => close()));
  // the dimmed page behind is one button that closes the dialog: it covers the window
  // under the dialog's content, transparent (the ::backdrop dims), and out of the Tab order
  const scrim = el("button", "call-scrim");
  scrim.type = "button";
  scrim.setAttribute("aria-label", "Close call");
  scrim.setAttribute("tabindex", "-1");
  root.append(scrim, head, top, body, foot);

  let sessionId = "";
  let current = ""; // the open call's exchange id, or ""
  let got = { id: "", state: "loading", data: null, error: "" };
  let shown = false;
  let closing = false;
  let opener = null;
  let pendingOpener = null;
  let inApp = false; // opened here (a link or Enter), so Back returns to the session page
  let stacked = false; // another call was opened while one was open: Back would land on it

  scrim.addEventListener("click", () => close());
  // Esc: the browser would close the <dialog> on its own; the URL closes it instead
  root.addEventListener("cancel", (e) => {
    if (e && typeof e.preventDefault === "function") e.preventDefault();
    close();
  });

  function step(delta) {
    const n = got.data ? got.data.nav : null;
    const id = n ? (delta < 0 ? n.prev : n.next) : "";
    if (id) nav.replace({ name: "session", id: sessionId, call: id });
  }
  function close() {
    if (!shown || closing) return;
    closing = true;
    if (inApp && !stacked) nav.back();
    else nav.replace({ name: "session", id: sessionId, call: null });
  }

  function open() {
    const a = globalThis.document.activeElement;
    opener = pendingOpener || (a && a !== globalThis.document.body ? a : null);
    shown = true;
    if (typeof root.showModal === "function" && root.isConnected !== false) {
      try {
        root.showModal();
      } catch {
        root.open = true;
      }
    } else {
      root.open = true;
    }
    // focus starts on Close, as in the 016 dialog; Tab reaches Previous and Next
    if (typeof x.focus === "function") x.focus();
  }

  function hide() {
    if (!shown) return;
    shown = false;
    closing = false;
    if (typeof root.close === "function" && root.open) root.close();
    else root.open = false;
    const target = opener && opener.isConnected !== false ? opener : ctx.turnHeading ? ctx.turnHeading() : null;
    opener = null;
    pendingOpener = null;
    inApp = false;
    stacked = false;
    current = "";
    got = { id: "", state: "loading", data: null, error: "" };
    if (target && typeof target.focus === "function") target.focus();
  }

  // fetch loads a call; keep holds what is shown until the answer lands (a live refresh).
  function fetch(id, keep) {
    if (!keep) got = { id, state: "loading", data: null, error: "" };
    Promise.resolve().then(() => ctx.loadCall(id)).then(
      (data) => {
        if (id !== current) return; // the dialog moved on
        got = { id, state: "ready", data, error: "" };
        draw(true);
      },
      (err) => {
        if (id !== current) return;
        if (err && err.status === 404) {
          hide();
          if (ctx.onMissing) ctx.onMissing();
          return;
        }
        if (keep && got.data) return; // a failed refresh keeps the call shown
        got = { id, state: "error", data: null, error: (err && err.message) || "request failed" };
        draw(true);
      },
    );
  }

  function topNodes(d) {
    const f = d.facts;
    const facts = el("div", "mfacts");
    facts.append(
      fact("Model", f.model),
      fact("Duration", f.dur_ms === null || f.dur_ms === undefined ? "—" : dur(f.dur_ms)),
      fact("Started", f.start ? clock(f.start) : "—"),
      fact("Ended", f.stop.word, f.stop.bad ? "bad" : ""),
      fact("Thread", f.thread.label),
      fact("Context", num(f.ctx)),
    );
    const c = d.context;
    const change = changeText(c);
    const ctxLine = el("p", "call-line", `Context ${num(c.size)}${change === "—" ? "" : ` · ${change}`}`);
    const sawLine = el("p", "call-line call-saw-counts", sawText(d.saw));
    return [facts, ctxLine, sawLine];
  }

  function contextSection(c) {
    const facts = el("div", "mfacts");
    facts.append(
      fact("Size", `${c.partial ? "~" : ""}${num(c.size)}`),
      fact("Change", changeText(c)),
      fact("Input", num(c.input)),
      fact("Cache write", num(c.cache_write)),
      fact("Cache read", num(c.cache_read)),
      fact("Output", num(c.output)),
    );
    return section("Context", "input + cache write + cache read", facts,
      c.why ? el("p", "why2", c.why) : null, c.note ? el("p", "muted-note", c.note) : null);
  }

  function sawSection(d) {
    const s = d.saw;
    if (s.unavailable) return section("What the model saw", "", el("p", "muted-note", s.unavailable), fact("Messages", "—"));
    const ul = el("ul", "call-messages");
    for (const m of s.messages) {
      const li = el("li", m.new ? "is-new" : "is-carried");
      // a message of tool calls or tool results alone has no text to show
      const text = m.text ? el("span", "msg", m.text) : el("span", "msg muted-note", "no text: tool calls or tool results only");
      li.append(el("span", "badge", m.new ? "new" : "carried"), el("span", "role", m.role), text);
      if (m.truncated) li.append(link(d.raw.request || d.raw.page, "cut short — open the raw request", Boolean(d.raw.request)));
      ul.append(li);
    }
    return section("What the model saw", sawText(s), ul);
  }

  // thinkingSection is the thinking flag behind a toggle: the gateway keeps thinking text out
  // of the timeline (016 AC7), so the toggle says where it is (research D37).
  function thinkingSection(d) {
    if (!d.thinking) return null;
    const box = el("div", "call-thinking");
    box.hidden = true;
    box.append(el("p", "muted-note", "This call thought before it answered. The thinking is not shown in the timeline; it is in the raw response."));
    if (d.raw.response) box.append(link(d.raw.response, "Open the raw response", true));
    const t = button("btn sm", "Show thinking", () => {
      box.hidden = !box.hidden;
      t.textContent = box.hidden ? "Show thinking" : "Hide thinking";
      t.setAttribute("aria-expanded", String(!box.hidden));
    });
    t.setAttribute("aria-expanded", "false");
    return section("Thinking", "", t, box);
  }

  function responseSection(d) {
    const r = d.response;
    if (r.unavailable) return section("Response", "", el("p", "muted-note", r.unavailable));
    const cutNote = r.truncated
      ? (d.raw.response ? link(d.raw.response, "cut short — open the raw response", true) : el("p", "muted-note", "Cut short."))
      : null;
    const reply = r.text
      ? textBlock("call-reply", "Response", r.text)
      : el("p", "muted-note", (d.tools || []).length ? "No text in the reply: it asked for the tool calls below." : "No text in the reply.");
    return section("Response", r.stop_reason ? `stop: ${r.stop_reason}` : "", reply, cutNote);
  }

  function toolsSection(d) {
    if (!(d.tools || []).length) return section("Tool calls", "", el("p", "muted-note", "No tool calls asked for."));
    const tctx = {
      open: (target, from) => {
        if (target.kind === "tool" && ctx.openTool) ctx.openTool(target.id, from);
      },
      callHref: ctx.callHref,
      noteOpener: (node) => noteOpener(node),
    };
    const ol = el("ol", "steps call-tools");
    for (const l of d.tools) ol.append(toolNode(l, tctx));
    return section("Tool calls", `${d.tools.length} asked for`, ol);
  }

  function healthSection(h) {
    const out = [fact("Status", h.status)];
    if (h.ok) out.push(el("p", "muted-note", "No disconnect, abort or error."));
    else {
      const ul = el("ul", "call-health");
      for (const i of h.items) ul.append(el("li", `tone-${i.tone}`, i.text));
      out.push(ul);
    }
    return section("Health", "", ...out);
  }

  function rawSection(raw) {
    const box = el("div", "call-raw");
    box.append(raw.request ? link(raw.request, "Raw request body", true) : el("span", "muted-note", raw.request_missing || "Request body: not stored"));
    box.append(raw.response ? link(raw.response, "Raw response stream", true) : el("span", "muted-note", raw.response_missing || "Response body: not stored"));
    box.append(link(raw.headers, "Headers"), link(raw.page, "Full exchange page"));
    return section("Raw", "the stored bodies and headers, unparsed", box);
  }

  // rawFor is the Raw section when the details did not load: the links need only the id.
  function rawFor(id) {
    const e = encodeURIComponent(id);
    return rawSection({ request: `/api/exchanges/${e}/body?part=request`, response: `/api/exchanges/${e}/body?part=response`, headers: `#/exchanges/${e}`, page: `#/exchanges/${e}` });
  }

  // draw lays out the open call; keep holds the body's scroll (a live redraw or a fetch).
  function draw(keep = false) {
    const scroll = body.scrollTop;
    const d = got.data;
    prev.disabled = !(d && d.nav.prev);
    next.disabled = !(d && d.nav.next);
    if (!d) {
      kick.textContent = "Model call";
      title.textContent = "Call";
      subt.textContent = "";
      top.replaceChildren(el("p", "muted-note", got.state === "loading" ? "Loading call…" : `Call details unavailable: ${got.error}`));
      body.replaceChildren(rawFor(got.id));
    } else {
      kick.textContent = d.facts.thread.kind === "side" ? "Side call" : d.turn ? `Model call · turn ${d.turn}` : "Model call";
      title.textContent = `Call #${d.n}`;
      subt.textContent = `${d.nav.index} of ${d.nav.total} · ${d.facts.thread.label}`;
      top.replaceChildren(...topNodes(d));
      body.replaceChildren(...[contextSection(d.context), sawSection(d), thinkingSection(d), responseSection(d), toolsSection(d), healthSection(d.health), rawSection(d.raw)].filter(Boolean));
    }
    if (keep) body.scrollTop = scroll;
  }

  function noteOpener(node) {
    pendingOpener = node;
    if (shown) stacked = true;
    else inApp = true;
  }

  return {
    el: root,
    isOpen: () => shown,
    noteOpener,
    // openCall pushes the dialog's URL for a call (a target from the page, or Enter).
    openCall(callId, from) {
      noteOpener(from);
      nav.push({ name: "session", id: sessionId, call: callId });
    },
    step,
    close,
    // sync follows the route: callId is the call the URL names, or null.
    sync({ sessionId: sid, callId }) {
      sessionId = sid;
      if (!callId) {
        hide();
        return;
      }
      closing = false;
      const moved = callId !== current;
      if (moved) {
        current = callId;
        fetch(callId, false);
      }
      if (!shown) open();
      draw(!moved);
      if (moved) body.scrollTop = 0;
    },
    // refresh redraws the open call from the server after a live update, on the same call.
    refresh() {
      if (shown && current) fetch(current, true);
    },
    callId: () => current,
  };
}
