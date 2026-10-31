import { getJSON, openStream } from "./api.js";
import { addUnique, upsertSession } from "./entries.js";
import { createStore } from "./store.js";
import { note } from "./session-list.js";
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
  const h = location.hash || "#/";
  if (h === "#/") {
    show("sessions", "session-list");
    startStream();
  } else if (h === "#/ungrouped") {
    show("ungrouped", "ungrouped-list");
  } else {
    app.replaceChildren(note("empty", "Nothing here."));
  }
}

window.addEventListener("hashchange", route);
route();
