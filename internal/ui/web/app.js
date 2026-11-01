import { getJSON, openStream } from "./api.js";
import { addUnique, replaceExchange, upsertSession } from "./entries.js";
import { createStore } from "./store.js";
import { note } from "./session-list.js";
import "./session-view.js";
import "./ungrouped-list.js";

const app = document.getElementById("app");
const liveEl = document.getElementById("live");
const store = createStore({ sessions: [], sessionsNext: null, sessionsError: "", ungrouped: [], ungroupedNext: null, ungroupedError: "" });
let closeStream = null;
let view = null;

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

function stopStream() {
  if (closeStream) closeStream();
  closeStream = null;
  liveEl.textContent = "";
}

function startStream() {
  stopStream();
  closeStream = openStream(
    (name, data) => {
      if (name === "session") store.set({ sessions: upsertSession(store.get().sessions, data) });
    },
    (st) => {
      liveEl.textContent = st === "connected" ? "live" : "live: disconnected, retrying";
    },
  );
}

let seq = 0;

// showSession loads the summary and every timeline page, then, for an active session,
// opens its stream. A stale load (the route changed meanwhile) is dropped.
async function showSession(id) {
  const mine = ++seq;
  const el = document.createElement("session-view");
  app.replaceChildren(el);
  const st = { summary: null, entries: [], error: "", snapshotError: "" };
  const draw = () => el.render(st);
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
    let after;
    do {
      const page = await getJSON(`sessions/${encodeURIComponent(id)}/timeline`, { after, limit: 2000 });
      if (mine !== seq) return;
      set({ entries: st.entries.concat(page.entries) });
      after = page.next_after;
    } while (after);
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
  // first exchange event of a later connection (its snapshot) clears it.
  closeStream = openStream(
    (name, data) => {
      if (name === "snapshot_error") set({ snapshotError: data.error ? data.error.message : "unknown" });
      else if (name === "session") live({ summary: data });
      else live({ entries: replaceExchange(st.entries, data), snapshotError: "" });
    },
    (state) => {
      liveEl.textContent = state === "connected" ? "live" : "live: disconnected, retrying";
    },
    `sessions/${encodeURIComponent(id)}/stream`,
  );
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

function route() {
  if (view) view();
  view = null;
  stopStream();
  seq++;
  const h = location.hash || "#/";
  if (h === "#/") {
    show("sessions", "session-list");
    startStream();
  } else if (h === "#/ungrouped") {
    show("ungrouped", "ungrouped-list");
  } else if (h.startsWith("#/sessions/")) {
    showSession(decodeURIComponent(h.slice("#/sessions/".length)));
  } else {
    app.replaceChildren(note("empty", "Nothing here."));
  }
}

window.addEventListener("hashchange", route);
route();
