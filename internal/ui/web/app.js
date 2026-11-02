import { getJSON, loadAllPages, openStream } from "./api.js";
import { addUnique, liveEffect, upsertSession } from "./entries.js";
import { createStore } from "./store.js";
import { createTimelinePage } from "./timeline-page.js";
import { createSessionsPage, localMidnight, note } from "./sessions-page.js";
import { initTheme } from "./theme.js";
import { parseRoute } from "./route.js";
import { handleKey, isTextInput } from "./keys.js";
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
const store = createStore({ sessions: [], sessionsNext: null, sessionsError: "", ungrouped: [], ungroupedNext: null, ungroupedError: "", ungroupedHead: { items: [], next_after: null }, today: null });
let closeStream = null;
let view = null;
let page = null; // the Sessions page while it is shown
let todayTimer = null;

async function load(kind, after) {
  const path = kind === "sessions" ? "sessions" : "ungrouped";
  try {
    const page = await getJSON(path, { after });
    const s = store.get();
    store.set({
      [kind]: after ? addUnique(s[kind], page.items) : page.items,
      [`${kind}Next`]: page.next_after,
      [`${kind}Error`]: "",
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
      }
    },
    (st) => {
      shell.setLive(liveText(st));
    },
  );
}

let seq = 0;

// showSession loads the summary and every timeline page, then, for an active session,
// opens its stream. A stale load (the route changed meanwhile) is dropped.
async function showSession(id) {
  const mine = ++seq;
  const tl = createTimelinePage();
  app.replaceChildren(tl.el);
  const st = { summary: null, entries: [], error: "", snapshotError: "" };
  const draw = () => tl.render(st);
  draw();
  const set = (patch) => {
    if (mine !== seq) return;
    Object.assign(st, patch);
    draw();
  };
  try {
    const summary = await getJSON(`sessions/${encodeURIComponent(id)}`);
    if (mine !== seq) return;
    set({ summary });
    if (mine === seq) shell.setRoute(parseRoute(location.hash), summary.title || id);
    await loadAllPages(
      (after) => getJSON(`sessions/${encodeURIComponent(id)}/timeline`, { after, limit: 2000 }),
      (p) => {
        if (mine !== seq) return false;
        set({ entries: st.entries.concat(p.entries) });
        return true;
      },
    );
    if (mine !== seq) return;
    if (!summary.active) return;
  } catch (e) {
    set({ error: e.message });
    return;
  }
  // follow new entries only when the page is already at the bottom (AC45)
  const nearBottom = () => {
    const d = document.documentElement;
    return d.scrollHeight - (window.scrollY + window.innerHeight) <= 8;
  };
  const live = (patch) => {
    const follow = nearBottom();
    set(patch);
    if (follow && mine === seq) window.scrollTo(0, document.documentElement.scrollHeight);
  };
  // The snapshot-error state survives the native error event and the reconnect; only the
  // first hooks, exchange or entries event of a later connection (its snapshot) clears it.
  closeStream = openStream(
    (name, data) => {
      const patch = liveEffect(name, data, st.entries);
      if (!patch) return;
      if (name === "snapshot_error") set(patch);
      else live(patch);
    },
    (state) => {
      shell.setLive(liveText(state));
    },
    `sessions/${encodeURIComponent(id)}/stream`,
  );
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
  if (view) view();
  view = null;
  page = null;
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
