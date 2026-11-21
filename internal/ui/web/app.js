import { getJSON, openStream } from "./api.js";
import { createUnassignedPage } from "./effects-list.js";
import { lastID } from "./effects.js";
import { addUnique, upsertSession } from "./entries.js";
import { createStore } from "./store.js";
import { createSessionPage } from "./session-page.js";
import { createSessionsPage, localMidnight, note, UNASSIGNED_SHOWN } from "./sessions-page.js";
import { initStyle, initTheme } from "./theme.js";
import { buildRoute, parseRoute } from "./route.js";
import { handleKey, isTextInput } from "./keys.js";
import { createCallDialog } from "./call-dialog.js";
import { mountShell } from "./shell.js";
import { createExchangePage } from "./exchange-detail.js";
import { createUngroupedPage } from "./ungrouped-list.js";

const app = document.getElementById("app");
const root = document.documentElement;
let storage = null;
try {
  storage = window.localStorage;
} catch {
  // storage blocked: the theme falls back to dark, the style to lean
}
const shell = mountShell({
  head: document.getElementById("shell-head"),
  bar: document.getElementById("keybar"),
  storage,
  root,
  theme: initTheme(storage, root),
  look: initStyle(storage, root),
});
const store = createStore({ sessions: [], sessionsNext: null, sessionsError: "", ungrouped: [], ungroupedNext: null, ungroupedError: "", ungroupedHead: { items: [], next_after: null }, today: null, unassigned: 0, unassignedItems: [] });
let closeStream = null;
let view = null;
let page = null; // the Sessions page while it is shown
let tpage = null; // the session page and its call dialog while a session is shown
let todayTimer = null;
let sessionSync = null; // the shown session, so a call URL change does not reload it

async function load(kind, after) {
  const path = kind === "sessions" ? "sessions" : "ungrouped";
  try {
    const page = await getJSON(path, { after });
    const s = store.get();
    store.set({
      [kind]: after ? addUnique(s[kind], page.items) : page.items,
      [`${kind}Next`]: page.next_after,
      [`${kind}Error`]: "",
      [`${kind}Loaded`]: true,
      ...(kind === "sessions" && !after ? { unassigned: page.unassigned_connections || 0 } : {}),
    });
    if (kind === "sessions" && !after) loadUnassigned(page.unassigned_connections || 0);
  } catch (e) {
    store.set({ [`${kind}Error`]: e.message });
  }
}

// loadUnassigned reads the first unassigned connections for the list's own section
// (spec 017 AC2); none are read when there are none.
async function loadUnassigned(count) {
  if (!count) {
    store.set({ unassignedItems: [] });
    return;
  }
  try {
    const p = await getJSON("effects/unassigned", { limit: UNASSIGNED_SHOWN });
    store.set({ unassignedItems: p.effects || [] });
  } catch {
    // the section still says how many there are and links to the full page
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
// view stream, whose events replace only the turns that changed. A call named in the URL
// opens in the call details dialog (spec 017 AC5), which the server fills one call at a time.
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
  let missingNoted = false; // the "no such call" note is up; the next call URL clears it
  const calls = createCallDialog({
    nav,
    // a call's details get the view's single retry on 504; the dialog shows "Loading call…"
    // meanwhile (F9)
    loadCall: (callId) => retryOn504(() => getJSON(`sessions/${enc(id)}/calls/${enc(callId)}`), () => {}),
    // a tool the call asked for opens its 016 dialog over this one (research D39)
    openTool: (toolId, from) => {
      if (!page) return;
      if (page.dialogOpen()) page.dialog.close();
      page.dialog.open(`tool:${toolId}`, from);
    },
    callHref: (exchangeId) => buildRoute({ name: "session", id, call: exchangeId }),
    turnHeading: () => (page ? page.turnHeading() : null),
    onMissing: () => {
      if (!page) return;
      missingNoted = true;
      page.setStatus("No such call in this session.", "error");
    },
  });
  page = createSessionPage({
    callHref: (exchangeId) => buildRoute({ name: "session", id, call: exchangeId }),
    noteOpener: (node) => calls.noteOpener(node),
    openCall: (exchangeId, from) => calls.openCall(exchangeId, from),
    loadTool: (tid) => getJSON(`sessions/${enc(id)}/tools/${enc(tid)}`),
    loadDiff: (eid) => getJSON(`effects/${enc(eid)}/diff`),
    loadContent: (eid) => getJSON(`effects/${enc(eid)}/content`),
  });
  app.replaceChildren(page.el, calls.el);
  tpage = { page, calls };

  // followCall opens, moves or closes the call dialog to the call the URL names. The call
  // dialog replaces an open tool dialog (one item dialog at a time, research D39).
  let callId = parseRoute(location.hash).call || null;
  const followCall = () => {
    if (missingNoted) {
      missingNoted = false;
      page.setStatus("");
    }
    if (callId && page.dialogOpen()) page.dialog.close();
    calls.sync({ sessionId: id, callId });
  };
  sessionSync = {
    id,
    apply: (call) => {
      callId = call || null;
      followCall();
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
  followCall();
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
      calls.refresh(); // the open call stays; its details are fetched again (AC10)
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

// showUnassigned lists the connections linked to no session (spec 014 AC15, 017 AC12).
async function showUnassigned() {
  const mine = ++seq;
  const open = new Set();
  let state = { items: [], loading: true, error: "" };
  const pg = createUnassignedPage({
    isOpen: (k) => open.has(k),
    setOpen: (k, on) => (on ? open.add(k) : open.delete(k)),
    getJSON,
    redraw: () => pg.render(state),
  });
  app.replaceChildren(pg.el);
  pg.render(state);
  try {
    let items = [];
    for (;;) {
      const p = await getJSON("effects/unassigned", { after: lastID(items), limit: 5000 });
      if (mine !== seq) return;
      items = items.concat(p.effects);
      if (p.effects.length < 5000) break;
    }
    state = { items, loading: false, error: "" };
  } catch (e) {
    if (mine !== seq) return;
    state = { items: [], loading: false, error: e.message };
  }
  pg.render(state);
}

// showExchange loads one exchange's detail; its bodies load only when their sections open.
// Once it lands, the breadcrumb names its session and call, or Ungrouped (017 AC12).
async function showExchange(r) {
  const mine = ++seq;
  const pg = createExchangePage();
  app.replaceChildren(pg.el);
  pg.render({ exchange: null });
  try {
    const exchange = await getJSON(`exchanges/${encodeURIComponent(r.id)}`);
    if (mine !== seq) return;
    shell.setRoute(r, "", { sessionId: exchange.session_id || "", sessionTitle: exchange.session_title || "", callN: exchange.call_n || 0 });
    pg.render({ exchange, focus: r.focus || "" });
  } catch (e) {
    if (mine === seq) pg.render({ exchange: null, error: e.message });
  }
}

function show(kind, pg) {
  app.replaceChildren(pg.el);
  const draw = (s) =>
    pg.render({
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
      loaded: Boolean(s.sessionsLoaded),
      today: s.today,
      ungrouped: s.ungroupedHead,
      unassigned: s.unassigned,
      unassignedItems: s.unassignedItems,
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
  // a new page starts at its top, not at the last page's scroll (seen in the 017 slice 3
  // check); the exchange page's headers route scrolls to them once drawn
  if (typeof window.scrollTo === "function") window.scrollTo(0, 0);
  shell.setRoute(r, "");
  shell.setKeyBar(r, null);
  if (r.name === "sessions") {
    showSessions();
    startStream();
  } else if (r.name === "ungrouped") {
    show("ungrouped", createUngroupedPage());
  } else if (r.name === "session") {
    showSession(r.id);
  } else if (r.name === "exchange") {
    showExchange(r);
  } else if (r.name === "unassigned") {
    showUnassigned();
  } else {
    app.replaceChildren(note("empty", "Nothing here."));
  }
}

// Shortcuts act per level (keys.js); the input guard and Esc-to-leave live in handleKey.
window.addEventListener("keydown", (e) => {
  if (shell.settingsOpen()) return; // the Settings dialog takes no page keys
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
    if (tpage.page.dialogOpen() || tpage.calls.isOpen()) return;
    e.preventDefault();
    tpage.page.moveTurn(act.delta);
  } else if (tpage && act.type === "call") {
    // a tool dialog opened over the call takes the keys until it closes
    if (tpage.page.dialogOpen()) return;
    e.preventDefault();
    tpage.calls.step(act.delta);
  } else if (tpage && act.type === "close") {
    if (tpage.page.dialogOpen()) return;
    tpage.calls.close();
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
