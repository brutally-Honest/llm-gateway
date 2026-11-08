import { getJSON, loadAllPages, openStream } from "./api.js";
import { effectsListNode } from "./drawer-sections.js";
import { lastID } from "./effects.js";
import { addUnique, liveBatch, upsertSession } from "./entries.js";
import { createStore } from "./store.js";
import { createTimelinePage } from "./timeline-page.js";
import { createSessionsPage, localMidnight, note } from "./sessions-page.js";
import { initTheme } from "./theme.js";
import { buildRoute, parseRoute } from "./route.js";
import { handleKey, isTextInput } from "./keys.js";
import { shouldFollow } from "./drawer.js";
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
let tpage = null; // the timeline page while a session is shown
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

// LIVE_BATCH_MS is how long a session view gathers stream events before it draws them.
const LIVE_BATCH_MS = 50;

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

// showSession loads the summary and every timeline page, then, for an active session,
// opens its stream. A stale load (the route changed meanwhile) is dropped.
async function showSession(id) {
  const mine = ++seq;
  const tl = createTimelinePage({
    nav: {
      push: (r) => { location.hash = buildRoute(r); },
      replace: (r) => location.replace(buildRoute(r)),
      back: () => history.back(),
    },
    // a call's detail gets the summary's single retry on 504; the drawer shows "loading…"
    // meanwhile (F9)
    loadDetail: (callId) => retryOn504(() => getJSON(`exchanges/${encodeURIComponent(callId)}`), () => {}),
  });
  app.replaceChildren(tl.el);
  const st = { summary: null, entries: [], effects: [], error: "", snapshotError: "", callId: parseRoute(location.hash).call || null, loaded: false };
  tpage = tl;
  sessionSync = { id, apply: (call) => { st.callId = call || null; draw(); } };
  const draw = () => {
    tl.render(st);
    shell.setKeyBar(parseRoute(location.hash), tl.position());
  };
  draw();
  const set = (patch) => {
    if (mine !== seq) return;
    Object.assign(st, patch);
    draw();
  };
  // Effects load apart from the timeline, after the last id held, and redraw only the
  // list: a live effects event never rebuilds the turns (AC14). A pull asked for while one
  // runs is run once more when it ends.
  const EFFECTS_PAGE = 5000;
  let effBusy = false;
  let effAgain = false;
  const pullEffects = async () => {
    if (effBusy) {
      effAgain = true;
      return;
    }
    effBusy = true;
    try {
      do {
        effAgain = false;
        for (;;) {
          const p = await getJSON(`sessions/${encodeURIComponent(id)}/effects`, { after: lastID(st.effects), limit: EFFECTS_PAGE });
          if (mine !== seq) return;
          if (p.effects.length) {
            st.effects = st.effects.concat(p.effects);
            tl.setEffects(st.effects);
          }
          if (p.effects.length < EFFECTS_PAGE) break;
        }
      } while (effAgain);
    } catch {
      // the effects shown stay as they were; the timeline is unaffected
    } finally {
      effBusy = false;
    }
  };
  let held = []; // timeline pages fetched but not yet drawn
  try {
    const waiting = () => set({ building: true });
    const summary = await retryOn504(() => getJSON(`sessions/${encodeURIComponent(id)}`), waiting);
    if (mine !== seq) return;
    set({ summary, building: false });
    pullEffects();
    if (mine === seq) shell.setRoute(parseRoute(location.hash), summary.title || id);
    const fetchPage = (after) => getJSON(`sessions/${encodeURIComponent(id)}/timeline`, { after, limit: 2000 });
    let first = true;
    // Each render rebuilds the whole list, so the first page shows at once and the later
    // pages are held and drawn together with the last one: a bounded number of renders
    // however many pages a session has, not one per page.
    let shown = false;
    await loadAllPages(
      (after) => {
        if (!first) return fetchPage(after); // later pages are not retried
        first = false;
        return retryOn504(() => fetchPage(after), waiting);
      },
      (p) => {
        if (mine !== seq) return false;
        held.push(...p.entries);
        if (shown && p.next_after) return true;
        shown = true;
        const more = held;
        held = [];
        set({ entries: st.entries.concat(more), building: false, loaded: !p.next_after });
        return true;
      },
    );
    if (mine !== seq) return;
    if (!summary.active) return;
  } catch (e) {
    // pages fetched before the failing one still show
    set({ error: e.message, building: false, entries: held.length ? st.entries.concat(held) : st.entries });
    return;
  }
  // follow new entries only when the page is already at the bottom (AC45)
  const nearBottom = () => {
    const d = document.documentElement;
    return d.scrollHeight - (window.scrollY + window.innerHeight) <= 8;
  };
  // Stream events are queued and applied together, with one draw, LIVE_BATCH_MS after the
  // first of a burst: a snapshot sends one event per exchange, and drawing after each one
  // rebuilt the whole list thousands of times and froze the page on an active session.
  // Whether to follow is decided before the burst changes the page, as for one event.
  let queued = [];
  let follow = false;
  const flushLive = () => {
    const events = queued;
    queued = [];
    if (mine !== seq) return;
    const patch = liveBatch(events, st.entries);
    if (!patch) return;
    set(patch);
    // a snapshot error alone never scrolls the view
    const moved = events.some((e) => e.name !== "snapshot_error");
    if (moved && follow && mine === seq) window.scrollTo(0, document.documentElement.scrollHeight);
  };
  // The snapshot-error state survives the native error event and the reconnect; only the
  // first hooks, exchange or entries event of a later connection (its snapshot) clears it.
  closeStream = openStream(
    (name, data) => {
      if (mine !== seq) return;
      if (name === "effects") {
        pullEffects();
        return;
      }
      if (queued.length === 0) {
        follow = shouldFollow(tl.drawerOpen(), nearBottom());
        setTimeout(flushLive, LIVE_BATCH_MS);
      }
      queued.push({ name, data });
    },
    (state) => {
      shell.setLive(liveText(state));
    },
    `sessions/${encodeURIComponent(id)}/stream`,
  );
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
  } else if (tpage && ["turn", "nextError", "nextTool", "hooks"].includes(act.type)) {
    e.preventDefault();
    if (act.type === "turn") tpage.moveTurn(act.delta);
    else if (act.type === "nextError") tpage.nextError();
    else if (act.type === "nextTool") tpage.nextTool();
    else tpage.toggleHooks();
    shell.setKeyBar(parseRoute(location.hash), tpage.position());
  } else if (act.type === "openFirstCall" && tpage) {
    tpage.openFirstCall();
  } else if (tpage && act.type === "call") {
    e.preventDefault();
    tpage.stepCall(act.delta);
  } else if (tpage && act.type === "close") {
    tpage.closeDrawer();
  } else if (tpage && act.type === "backToTurn") {
    tpage.backToTurn();
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
