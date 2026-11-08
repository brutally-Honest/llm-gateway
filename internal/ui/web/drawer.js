import { callNumbers, chipOf, formatDuration } from "./model.js";
import { getJSON } from "./api.js";
import { buildSections } from "./drawer-sections.js";
import { chipNode } from "./tool-card.js";

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// stepTarget is the call id `delta` places from id in session order, or "" at either end.
export function stepTarget(ids, id, delta) {
  const i = ids.indexOf(id);
  const j = i + delta;
  return i < 0 || j < 0 || j >= ids.length ? "" : ids[j];
}

// shouldFollow: new entries are scrolled to only with the drawer closed and the view at the bottom.
export function shouldFollow(drawerOpen, atBottom) {
  return !drawerOpen && atBottom;
}

// createDrawer builds the right-side exchange drawer. The URL is the page's: nav is
// { push(route), replace(route), back() } and the drawer asks for hash changes through it;
// the page feeds the route's call id back through sync(). The drawer keys on the exchange id,
// so live data never moves it off its call; its element and step buttons are built once, so
// focus and scroll survive every sync. turnHeading() gives the current turn's heading, the
// place focus falls back to when the opener is gone (E11).
export function createDrawer({ nav, turnHeading, loadDetail = (id) => getJSON(`exchanges/${encodeURIComponent(id)}`) }) {
  const root = el("aside", "drawer");
  root.hidden = true;
  root.setAttribute("aria-label", "Call");
  const head = el("div", "drawer-head");
  const title = el("h2", "drawer-title");
  title.setAttribute("tabindex", "-1");
  const chips = el("span", "drawer-chips");
  const prev = el("button", "", "↑ previous");
  prev.type = "button";
  const next = el("button", "", "↓ next");
  next.type = "button";
  const close = el("button", "", "Close");
  close.type = "button";
  close.setAttribute("aria-label", "Close call");
  head.append(title, chips, prev, next, close);
  const body = el("div", "drawer-body");
  root.append(head, body);

  // detail is the open call's exchange detail: { id, state, data, error }. It is fetched
  // once per call; a failed fetch leaves the sections stating why, and the raw links work.
  let detail = { id: "", state: "loading", data: null, error: "" };
  let lastEntries = [];
  let sessionId = "";
  let ids = [];
  let current = "";
  let pendingOpener = null;
  let opener = null;
  let inApp = false; // opened here (link, bar, Enter), so Back returns to the session page
  let stacked = false; // another call was opened while this one was open: Back would land on that call
  const open = () => !root.hidden;

  const step = (delta) => {
    const id = stepTarget(ids, current, delta);
    if (id) nav.replace({ name: "session", id: sessionId, call: id });
  };
  const closeNow = () => {
    if (!open()) return;
    if (inApp && !stacked) nav.back();
    else nav.replace({ name: "session", id: sessionId, call: null });
  };
  prev.addEventListener("click", () => step(-1));
  next.addEventListener("click", () => step(1));
  close.addEventListener("click", closeNow);

  function hide() {
    if (!open()) return;
    root.hidden = true;
    const target = opener && opener.isConnected !== false ? opener : turnHeading();
    opener = null;
    pendingOpener = null;
    inApp = false;
    stacked = false;
    current = "";
    if (target && typeof target.focus === "function") target.focus();
  }

  function draw(call) {
    body.replaceChildren(...buildSections(call, lastEntries, detail));
  }

  function fetchDetail(call) {
    const id = call.exchange_id;
    detail = { id, state: "loading", data: null, error: "" };
    Promise.resolve().then(() => loadDetail(id)).then(
      (data) => settle({ id, state: "ready", data, error: "" }),
      (err) => settle({ id, state: "error", data: null, error: (err && err.message) || "request failed" }),
    );
  }

  function settle(next) {
    if (next.id !== current) return; // the drawer moved on
    detail = next;
    const call = lastEntries.find((e) => e.kind === "model_call" && e.exchange_id === current);
    if (call) draw(call);
  }

  function show(call, index, toolCalls) {
    if (!open()) {
      const a = globalThis.document.activeElement;
      opener = pendingOpener || (a && a !== globalThis.document.body ? a : null);
      root.hidden = false;
      if (typeof title.focus === "function") title.focus();
    }
    if (current !== call.exchange_id) fetchDetail(call);
    current = call.exchange_id;
    draw(call);
    title.textContent = `Call #${callNumbers(lastEntries).get(call.exchange_id)} · ${formatDuration(call.duration_ms ?? null)}`;
    chips.replaceChildren(...toolCalls.map((t) => chipNode(chipOf(t))));
    prev.disabled = index === 0;
    next.disabled = index === ids.length - 1;
  }

  return {
    el: root,
    isOpen: open,
    // noteOpener remembers the element a click or key opened the drawer from.
    noteOpener(node) {
      pendingOpener = node;
      if (open()) stacked = true;
      else inApp = true;
    },
    // openCall pushes the drawer URL for a call (Enter on a turn).
    openCall(callId, from) {
      this.noteOpener(from);
      nav.push({ name: "session", id: sessionId, call: callId });
    },
    step,
    close: closeNow,
    // sync follows the route: callId is the call the URL names, or null. Returns
    // { missing, index, total }; missing is true once the session is loaded and lacks the call.
    sync({ sessionId: sid, callId, entries, loaded }) {
      sessionId = sid;
      lastEntries = entries;
      const calls = entries.filter((e) => e.kind === "model_call");
      ids = calls.map((c) => c.exchange_id);
      const index = callId ? ids.indexOf(callId) : -1;
      if (index < 0) {
        hide();
        return { missing: Boolean(callId) && loaded, index: 0, total: ids.length };
      }
      show(calls[index], index, entries.filter((e) => e.kind === "tool_call" && e.exchange_id === callId));
      return { missing: false, index: index + 1, total: ids.length };
    },
    // current is the open call's id, or "".
    callId: () => current,
  };
}
