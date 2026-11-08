import { THEMES, pickTheme } from "./theme.js";
import { keyBar } from "./keys.js";

// breadcrumb returns the header trail: "Sessions" first (a link back to the list once
// inside a view), then the session title inside a session. The last part has no href.
export function breadcrumb(route, title) {
  const root = { text: "Sessions", href: "#/" };
  switch (route.name) {
    case "sessions":
      return [{ text: "Sessions" }];
    case "session":
      return [root, { text: title || route.id }];
    case "ungrouped":
      return [root, { text: "Ungrouped" }];
    case "exchange":
      return [root, { text: "Exchange" }];
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

// mountShell fills the header and the bottom key bar and returns the updaters the page uses.
export function mountShell({ head, bar, storage, root, theme }) {
  const crumbs = el("nav", "", "crumbs");
  crumbs.setAttribute("aria-label", "Breadcrumb");
  const live = el("span", "", "muted");
  live.setAttribute("role", "status");
  const picker = el("div", "", "picker");
  picker.setAttribute("role", "group");
  picker.setAttribute("aria-label", "Theme");
  const buttons = new Map();
  const mark = (current) => {
    for (const [t, b] of buttons) b.setAttribute("aria-pressed", String(t === current));
  };
  for (const t of [...THEMES].reverse()) {
    const b = el("button", t === "light" ? "Light" : "Dark");
    b.type = "button";
    b.addEventListener("click", () => mark(pickTheme(storage, root, t)));
    buttons.set(t, b);
    picker.append(b);
  }
  mark(theme);
  head.replaceChildren(el("h1", "Gateway"), crumbs, live, picker);
  // The header and the key bar can wrap, so their heights are published for the turn
  // rail, which sticks below the one and stops above the other (F7).
  if (typeof ResizeObserver === "function" && root && root.style && typeof root.style.setProperty === "function") {
    const publish = (name, e) => new ResizeObserver(() => root.style.setProperty(name, `${e.offsetHeight}px`)).observe(e);
    publish("--head-h", head);
    publish("--keybar-h", bar);
  }

  return {
    setRoute(route, title) {
      const frag = document.createDocumentFragment();
      breadcrumb(route, title).forEach((p, i) => {
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
