import { STYLES, THEMES, pickStyle, pickTheme } from "./theme.js";
import { keyBar } from "./keys.js";

// breadcrumb returns the header trail: "Sessions" first (a link back to the list once
// inside a view), then the session title inside a session. The last part has no href. On
// the full exchange page, trail = { sessionId, sessionTitle, callN } names the session and
// call it belongs to; an exchange no session claims sits under Ungrouped (017 AC12).
export function breadcrumb(route, title, trail = null) {
  const root = { text: "Sessions", href: "#/" };
  switch (route.name) {
    case "sessions":
      return [{ text: "Sessions" }];
    case "session":
      return [root, { text: title || route.id }];
    case "ungrouped":
      return [root, { text: "Ungrouped" }];
    case "unassigned":
      return [root, { text: "Unassigned connections" }];
    case "exchange":
      if (!trail) return [root, { text: "Exchange" }];
      if (!trail.sessionId) return [root, { text: "Ungrouped", href: "#/ungrouped" }, { text: "Exchange" }];
      return [root, { text: trail.sessionTitle || "Session", href: `#/sessions/${encodeURIComponent(trail.sessionId)}` }, { text: trail.callN ? `Call #${trail.callN}` : "Exchange" }];
    default:
      return [root];
  }
}

function el(tag, text, cls) {
  const e = document.createElement(tag);
  if (text) e.textContent = text;
  if (cls) e.className = cls;
  return e;
}

// picker builds one labelled row of the Settings dialog: a button per choice, the current
// one pressed; pick(choice) applies and stores it and returns what was applied.
function picker(label, choices, current, pick) {
  const row = el("div", "", "setting");
  const id = `setting-${label.toLowerCase()}`;
  const name = el("span", label, "setting-name");
  name.id = id;
  const group = el("div", "", "picker");
  group.setAttribute("role", "group");
  group.setAttribute("aria-labelledby", id);
  const buttons = new Map();
  const mark = (now) => {
    for (const [c, b] of buttons) b.setAttribute("aria-pressed", String(c === now));
  };
  for (const [value, text] of choices) {
    const b = el("button", text);
    b.type = "button";
    b.addEventListener("click", () => mark(pick(value)));
    buttons.set(value, b);
    group.append(b);
  }
  mark(current);
  row.append(name, group);
  return row;
}

// settingsDialog is the header's Settings: the theme (dark or light) and the style (Lean or
// Stark), each applied at once and remembered (spec 027 C2).
function settingsDialog({ storage, root, theme, look }) {
  const dlg = el("dialog", "", "settings");
  dlg.setAttribute("aria-labelledby", "settings-title");
  const title = el("h2", "Settings");
  title.id = "settings-title";
  const themes = [...THEMES].reverse().map((t) => [t, t === "light" ? "Light" : "Dark"]);
  const styles = STYLES.map((s) => [s, s === "stark" ? "Stark" : "Lean"]);
  const close = el("button", "Close");
  close.type = "button";
  close.addEventListener("click", () => dlg.close());
  const foot = el("div", "", "settings-foot");
  foot.append(close);
  dlg.append(
    title,
    picker("Theme", themes, theme, (t) => pickTheme(storage, root, t)),
    picker("Style", styles, look, (s) => pickStyle(storage, root, s)),
    foot,
  );
  // Esc closes it too: a modal <dialog> does that by itself
  return dlg;
}

// mountShell fills the header and the bottom key bar and returns the updaters the page uses.
export function mountShell({ head, bar, storage, root, theme, look = "lean" }) {
  const crumbs = el("nav", "", "crumbs");
  crumbs.setAttribute("aria-label", "Breadcrumb");
  const live = el("span", "", "muted");
  live.setAttribute("role", "status");
  const settings = settingsDialog({ storage, root, theme, look });
  const open = el("button", "⚙ Settings", "settings-open");
  open.type = "button";
  open.setAttribute("aria-haspopup", "dialog");
  open.addEventListener("click", () => settings.showModal());
  head.replaceChildren(el("h1", "Gateway"), crumbs, live, open, settings);
  // The header and the key bar can wrap, so their heights are published for the turn
  // rail, which sticks below the one and stops above the other (F7).
  if (typeof ResizeObserver === "function" && root && root.style && typeof root.style.setProperty === "function") {
    const publish = (name, e) => new ResizeObserver(() => root.style.setProperty(name, `${e.offsetHeight}px`)).observe(e);
    publish("--head-h", head);
    publish("--keybar-h", bar);
  }

  return {
    settingsOpen() {
      return Boolean(settings.open);
    },
    setRoute(route, title, trail = null) {
      const frag = document.createDocumentFragment();
      breadcrumb(route, title, trail).forEach((p, i) => {
        if (i > 0) frag.append(el("span", " / ", "muted"));
        if (p.href) {
          const a = el("a", p.text);
          a.href = p.href;
          frag.append(a);
        } else {
          const s = el("span", p.text);
          s.setAttribute("aria-current", "page");
          frag.append(s);
        }
      });
      crumbs.replaceChildren(frag);
    },
    setLive(text) {
      live.textContent = text;
    },
    setKeyBar(route, pos) {
      const kb = keyBar(route, pos);
      if (!kb.level) {
        bar.replaceChildren(); // no bar on this level; the footer hides when empty
        return;
      }
      const frag = document.createDocumentFragment();
      frag.append(el("strong", kb.level, "level"));
      if (kb.position) frag.append(el("span", kb.position, "position"));
      for (const s of kb.shortcuts) {
        const item = el("span", "", "shortcut");
        item.append(el("kbd", s.key), document.createTextNode(` ${s.label}`));
        frag.append(item);
      }
      bar.replaceChildren(frag);
    },
  };
}
