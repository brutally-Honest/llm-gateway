import { getJSON, loadAllPages, openStream } from "./api.js";
import { effectsListNode } from "./drawer-sections.js";
import { lastID } from "./effects.js";
import { addUnique, upsertSession } from "./entries.js";
import { createStore } from "./store.js";
import { createSessionPage } from "./session-page.js";
import { createSessionsPage, localMidnight, note } from "./sessions-page.js";
import { initTheme } from "./theme.js";
import { buildRoute, parseRoute } from "./route.js";
import { handleKey, isTextInput } from "./keys.js";
import { createDrawer } from "./drawer.js";
import { mountShell } from "./shell.js";
import "./exchange-detail.js";
import "./ungrouped-list.js";

const app = document.getElementById("app");
const root = document.documentElement;
let storage = null;
try {
  storage = window.localStorage;
} catch {
  // storage blocked: the theme falls back to dark
}
const shell = mountShell({
  head: document.getElementById("shell-head"),
  bar: document.getElementById("keybar"),
  storage,
  root,
  theme: initTheme(storage, root),
});
const store = createStore({ sessions: [], sessionsNext: null, sessionsError: "", ungrouped: [], ungroupedNext: null, ungroupedError: "", ungroupedHead: { items: [], next_after: null }, today: null, unassigned: 0 });
let closeStream = null;
let view = null;
let page = null; // the Sessions page while it is shown
let tpage = null; // the session page and its drawer while a session is shown
let todayTimer = null;
let sessionSync = null; // the shown session, so a drawer URL change does not reload it

async function load(kind, after) {
  const path = kind === "sessions" ? "sessions" : "ungrouped";
  try {
    const page = await getJSON(path, { after });
    const s = store.get();
    store.set({
      [kind]: after ? addUnique(s[kind], page.items) : page.items,
      [`${kind}Next`]: page.next_after,
      [`${kind}Error`]: "",
      ...(kind === "sessions" && !after ? { unassigned: page.unassigned_connections || 0 } : {}),
    });
  } catch (e) {
    store.set({ [`${kind}Error`]: e.message });
  }
}

async function loadToday() {
  try {
    store.set({ today: await getJSON("today", { since: localMidnight() }) });
  } catch {
    // totals stay as they were; the list itself still works
  }
}

// scheduleToday refetches the totals after a burst of session events settles.
function scheduleToday() {
  if (todayTimer) clearTimeout(todayTimer);
  todayTimer = setTimeout(() => {
    todayTimer = null;
    loadToday();
  }, 500);
}

async function loadUngroupedHead() {
  try {
    const p = await getJSON("ungrouped", { limit: 200 });
    store.set({ ungroupedHead: { items: p.items, next_after: p.next_after } });
  } catch {
    // the link is quiet: without the count it is not shown
  }
}

// liveText is the header status for a stream state.
function liveText(state) {
  if (state === "connected") return "live";
  if (state === "snapshot_error") return "live: snapshot failed, retrying in 30 s";
  return "live: disconnected, retrying";
}

function stopStream() {
  if (closeStream) closeStream();
  closeStream = null;
  shell.setLive("");
}

function startStream() {
  stopStream();
  closeStream = openStream(
    (name, data) => {
      if (name === "session") {
        store.set({ sessions: upsertSession(store.get().sessions, data) });
        scheduleToday();
      } else if (name === "effects") {
        // effect counts move without a summary rebuild (spec 014)
        if (data.unassigned_connections !== undefined) store.set({ unassigned: data.unassigned_connections });
        const s = store.get().sessions.find((x) => x.id === data.session);
        if (s && data.counts) store.set({ sessions: upsertSession(store.get().sessions, { ...s, effects: data.counts }) });
      }
    },
    (st) => {
      shell.setLive(liveText(st));
    },
  );
}

let seq = 0;

// The server answers 504 while it is still building a large session; the build finishes and
// is cached, so one retry after about a second finds it (AC36, AC19). The delay is injectable
// through globalThis.__retryDelay so tests do not wait.
const retryDelay = (ms) => (globalThis.__retryDelay ? globalThis.__retryDelay(ms) : new Promise((r) => setTimeout(r, ms)));

// retryOn504 runs fn; on a 504 it calls onWait, waits and runs fn once more. Any other error,
// and a second 504, reach the caller.
async function retryOn504(fn, onWait) {
  try {
    return await fn();
  } catch (e) {
    if (e.status !== 504) throw e;
    onWait();
    await retryDelay(1000);
    return fn();
  }
}

// showSession shows the session page (spec 016): it loads the server's view of the session
// (one retry on 504 while the build runs), then, for an active session, opens the session's
// view stream, whose events replace only the turns that changed. The 013 drawer stays until
// 017: its call list and entries are loaded only when a call is opened.
async function showSession(id) {
  const mine = ++seq;
  const enc = encodeURIComponent;
  setPage("session");
  const nav = {
    push: (r) => { location.hash = buildRoute(r); },
    replace: (r) => location.replace(buildRoute(r)),
    back: () => history.back(),
  };
  let page = null;
  const drawer = createDrawer({
    nav,
    turnHeading: () => (page ? page.turnHeading() : null),
    // a call's detail gets the view's single retry on 504; the drawer shows "loading…"
    // meanwhile (F9)
    loadDetail: (callId) => retryOn504(() => getJSON(`exchanges/${enc(callId)}`), () => {}),
  });
  page = createSessionPage({
    callHref: (exchangeId) => buildRoute({ name: "session", id, call: exchangeId }),
    noteOpener: (node) => drawer.noteOpener(node),
    openCall: (exchangeId, from) => drawer.openCall(exchangeId, from),
    loadTool: (tid) => getJSON(`sessions/${enc(id)}/tools/${enc(tid)}`),
    loadDiff: (eid) => getJSON(`effects/${enc(eid)}/diff`),
    loadContent: (eid) => getJSON(`effects/${enc(eid)}/content`),
  });
  app.replaceChildren(page.el, drawer.el);
  tpage = { page, drawer };

  // The drawer's entries: loaded on first open, and again after the session changed.
  const calls = { entries: [], state: "none", callId: parseRoute(location.hash).call || null, error: "" };
  const syncDrawer = () => {
    const drawn = drawer.sync({ sessionId: id, callId: calls.callId, entries: calls.entries, loaded: calls.state === "ready" });
    if (drawn.missing) page.setStatus("No such call in this session.", "error");
  };
  const loadEntries = async () => {
    if (calls.state === "loading") return;
    calls.state = "loading";
    const got = [];
    try {
      let first = true;
      const fetchPage = (after) => getJSON(`sessions/${enc(id)}/timeline`, { after, limit: 2000 });
      await loadAllPages(
        (after) => {
          if (!first) return fetchPage(after); // later pages are not retried
          first = false;
          return retryOn504(() => fetchPage(after), () => {});
        },
        (p) => {
          if (mine !== seq) return false;
          got.push(...p.entries);
          return true;
        },
      );
      if (mine !== seq) return;
      calls.entries = got;
      calls.state = "ready";
    } catch (e) {
      if (mine !== seq) return;
      calls.entries = got;
      calls.state = "failed";
      page.setStatus(e.message, "error");
    }
    syncDrawer();
  };
  const followDrawer = () => {
    if (!calls.callId) {
      syncDrawer();
      return;
    }
    if (calls.state === "none" || calls.state === "stale") loadEntries();
    else syncDrawer();
  };
  sessionSync = {
    id,
    apply: (call) => {
      calls.callId = call || null;
      followDrawer();
    },
  };

  let view;
  try {
    view = await retryOn504(() => getJSON(`sessions/${enc(id)}/view`), () => page.setStatus("building session…"));
  } catch (e) {
    if (mine === seq) page.setStatus(e.message, "error");
    return;
  }
  if (mine !== seq) return;
  page.setStatus("");
  page.setView(view);
  shell.setRoute(parseRoute(location.hash), view.overview.title);
  followDrawer();
  if (view.overview.status.word !== "active") return;
  closeStream = openStream(
    (name, data) => {
      if (mine !== seq) return;
      if (name === "snapshot_error") {
        page.setStatus(`Live snapshot failed: ${data.error ? data.error.message : "unknown"}`, "error");
        return;
      }
      if (name !== "view") return;
      page.setStatus("");
      page.applyEvent(data);
      if (calls.state === "ready" || calls.state === "failed") {
        calls.state = "stale";
        if (drawer.isOpen()) loadEntries();
      }
    },
    (state) => shell.setLive(liveText(state)),
    `sessions/${enc(id)}/stream?view=1`,
  );
}

// setPage marks the page shown on <body>, so the stylesheet can give the session page the
// whole window.
function setPage(name) {
  const body = document.body;
  if (body && body.dataset) {
    if (name) body.dataset.page = name;
    else delete body.dataset.page;
  }
}

// showUnassigned lists the connections linked to no session (spec 014, AC15).
async function showUnassigned() {
  const mine = ++seq;
  const box = document.createElement("section");
  box.className = "unassigned-page";
  const h = document.createElement("h1");
  h.textContent = "Unassigned connections";
  const why = note("muted", "Connections made while no session, or more than one, was active.");
  const body = document.createElement("div");
  box.append(h, why, body);
  app.replaceChildren(box);
  const open = new Set();
  let items = [];
  const draw = () => {
    body.replaceChildren(effectsListNode(items, {
      isOpen: (k) => open.has(k),
      setOpen: (k, on) => (on ? open.add(k) : open.delete(k)),
      getJSON,
      redraw: draw,
    }));
  };
  draw();
  try {
    for (;;) {
      const p = await getJSON("effects/unassigned", { after: lastID(items), limit: 5000 });
      if (mine !== seq) return;
      items = items.concat(p.effects);
      draw();
      if (p.effects.length < 5000) break;
    }
  } catch (e) {
    if (mine === seq) body.replaceChildren(note("error", e.message));
  }
}

// showExchange loads one exchange's detail; its bodies load only when their sections open.
async function showExchange(id) {
  const mine = ++seq;
  const el = document.createElement("exchange-detail");
  app.replaceChildren(el);
  el.render({ exchange: null, error: "" });
  try {
    const exchange = await getJSON(`exchanges/${encodeURIComponent(id)}`);
    if (mine === seq) el.render({ exchange, error: "" });
  } catch (e) {
    if (mine === seq) el.render({ exchange: null, error: e.message });
  }
}

function show(kind, tag) {
  const el = document.createElement(tag);
  app.replaceChildren(el);
  const draw = (s) =>
    el.render({
      items: s[kind],
      next_after: s[`${kind}Next`],
      error: s[`${kind}Error`],
      onMore: () => load(kind, store.get()[`${kind}Next`]),
    });
  view = store.subscribe(draw);
  draw(store.get());
  load(kind);
}

function showSessions() {
  page = createSessionsPage({ onMore: () => load("sessions", store.get().sessionsNext) });
  app.replaceChildren(page.el);
  const r = parseRoute(location.hash);
  const draw = (s) => {
    page.update({
      items: s.sessions,
      next_after: s.sessionsNext,
      error: s.sessionsError,
      today: s.today,
      ungrouped: s.ungroupedHead,
      unassigned: s.unassigned,
    });
    shell.setKeyBar(r, page.position());
  };
  view = store.subscribe(draw);
  draw(store.get());
  load("sessions");
  loadUngroupedHead();
  loadToday();
}

function route() {
  const next = parseRoute(location.hash);
  if (sessionSync && next.name === "session" && next.id === sessionSync.id) {
    sessionSync.apply(next.call);
    return;
  }
  sessionSync = null;
  if (view) view();
  view = null;
  page = null;
  tpage = null;
  if (todayTimer) clearTimeout(todayTimer);
  todayTimer = null;
  stopStream();
  setPage("");
  seq++;
  const r = parseRoute(location.hash);
  shell.setRoute(r, "");
  shell.setKeyBar(r, null);
  if (r.name === "sessions") {
    showSessions();
    startStream();
  } else if (r.name === "ungrouped") {
    show("ungrouped", "ungrouped-list");
  } else if (r.name === "session") {
    showSession(r.id);
  } else if (r.name === "exchange") {
    showExchange(r.id);
  } else if (r.name === "unassigned") {
    showUnassigned();
  } else {
    app.replaceChildren(note("empty", "Nothing here."));
  }
}

// Shortcuts act per level (keys.js); the input guard and Esc-to-leave live in handleKey.
window.addEventListener("keydown", (e) => {
  const act = handleKey(parseRoute(location.hash), {
    key: e.key,
    ctrlKey: e.ctrlKey,
    metaKey: e.metaKey,
    altKey: e.altKey,
    inInput: isTextInput(document.activeElement),
  });
  if (!act) return;
  if (act.type === "leaveInput") {
    document.activeElement.blur();
  } else if (act.type === "toSessions") {
    location.hash = "#/";
  } else if (act.type === "select" && page) {
    e.preventDefault();
    shell.setKeyBar(parseRoute(location.hash), page.select(act.delta));
  } else if (act.type === "open" && page) {
    if (page.selectedId()) location.hash = `#/sessions/${encodeURIComponent(page.selectedId())}`;
  } else if (tpage && act.type === "turn") {
    // j/k step through turns; a dialog takes no turn keys (AC13)
    if (tpage.page.dialogOpen() || tpage.drawer.isOpen()) return;
    e.preventDefault();
    tpage.page.moveTurn(act.delta);
  } else if (tpage && act.type === "call") {
    e.preventDefault();
    tpage.drawer.step(act.delta);
  } else if (tpage && act.type === "close") {
    tpage.drawer.close();
  } else if (act.type === "filter") {
    const f = app.querySelector("input");
    if (f) {
      e.preventDefault();
      f.focus();
    }
  }
});

window.addEventListener("hashchange", route);
route();
