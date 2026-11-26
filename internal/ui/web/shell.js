import { STYLES, THEMES, pickStyle, pickTheme } from "./theme.js";
import { keyBar } from "./keys.js";
import { healthBanner, bannerVisible, dismissalStore } from "./health.js";

// breadcrumb returns the header trail: "Sessions" first (the link back to the list once
// inside a view). The page's own heading says where you are, so the trail stops at the
// parent: a session, an ungrouped list or an exchange adds no crumb repeating its heading.
// On the full exchange page, trail = { sessionId, sessionTitle } names the session it
// belongs to; an exchange no session claims sits under Ungrouped (017 AC12). The sessions
// list has no trail at all; every part of a deeper trail is a link. `title` is no longer
// shown and is kept for callers.
export function breadcrumb(route, title, trail = null) {
  const root = { text: "Sessions", href: "#/" };
  switch (route.name) {
    case "sessions":
      return []; // the list's own heading says Sessions and the logo is the home link
    case "exchange":
      if (!trail) return [root];
      if (!trail.sessionId) return [root, { text: "Ungrouped", href: "#/ungrouped" }];
      return [root, { text: trail.sessionTitle || "Session", href: `#/sessions/${encodeURIComponent(trail.sessionId)}` }];
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
  // the dialog itself takes focus on open (focusDialog): a ring on the first option read as
  // a choice
  dlg.tabIndex = -1;
  // a click outside closes it, on the scrim button the other dialogs use (027 C7a)
  const scrim = el("button", "", "call-scrim");
  scrim.type = "button";
  scrim.setAttribute("aria-label", "Close");
  scrim.setAttribute("tabindex", "-1");
  scrim.addEventListener("click", () => dlg.close());
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
    scrim,
    title,
    picker("Theme", themes, theme, (t) => pickTheme(storage, root, t)),
    picker("Style", styles, look, (s) => pickStyle(storage, root, s)),
    foot,
  );
  // Esc closes it too: a modal <dialog> does that by itself
  return dlg;
}

// helpDialog is the header's "?" panel: the current page's shortcuts, the same content the
// bottom key bar used to show. Esc closes it (a modal <dialog> does that itself) and so does
// a click outside, on the scrim button the call dialog uses.
function helpDialog() {
  const dlg = el("dialog", "", "settings help");
  dlg.tabIndex = -1;
  dlg.setAttribute("aria-labelledby", "help-title");
  const scrim = el("button", "", "call-scrim");
  scrim.type = "button";
  scrim.setAttribute("aria-label", "Close");
  scrim.setAttribute("tabindex", "-1");
  scrim.addEventListener("click", () => dlg.close());
  const title = el("h2", "Keyboard shortcuts");
  title.id = "help-title";
  const list = el("div", "", "shortcuts");
  const close = el("button", "Close");
  close.type = "button";
  close.addEventListener("click", () => dlg.close());
  const foot = el("div", "", "settings-foot");
  foot.append(close);
  dlg.append(scrim, title, list, foot);
  return { dlg, list };
}

// openDialog opens a header dialog as a modal and puts focus on the dialog itself, not on
// its first option, so nothing looks chosen by a focus ring (027 C7a).
function openDialog(dlg) {
  dlg.showModal();
  if (typeof dlg.focus === "function") dlg.focus();
}

// mountShell fills the header and returns the updaters the page uses.
export function mountShell({ head, storage, root, theme, look = "lean" }) {
  const crumbs = el("nav", "", "crumbs");
  crumbs.setAttribute("aria-label", "Breadcrumb");
  // the stream's state is a dot; its word is the dot's accessible name and tooltip
  const live = el("span", "", "live-dot");
  live.setAttribute("role", "status");
  const liveWord = el("span", "", "sr-only");
  live.append(liveWord);
  live.hidden = true;
  // the visible note beside the dot while the stream is down; aria-hidden because the dot's
  // word already speaks for it
  const liveNote = el("span", "", "live-note");
  liveNote.setAttribute("aria-hidden", "true");
  liveNote.hidden = true;
  const settings = settingsDialog({ storage, root, theme, look });
  const open = el("button", "⚙", "settings-open");
  open.type = "button";
  open.setAttribute("aria-label", "Settings");
  open.title = "Settings";
  open.setAttribute("aria-haspopup", "dialog");
  open.addEventListener("click", () => openDialog(settings));
  // the page's shortcuts open with the "?" key (no button in the header, 027 C7a)
  const help = helpDialog();
  let bar = { level: "", position: "", shortcuts: [] };
  const showHelp = () => {
    if (!bar.level || help.dlg.open) return;
    const frag = document.createDocumentFragment();
    const lead = el("p", "", "muted");
    lead.append(document.createTextNode(bar.position ? `${bar.level} · ${bar.position}` : bar.level));
    frag.append(lead);
    for (const s of bar.shortcuts) {
      const row = el("div", "", "shortcut");
      row.append(el("kbd", s.key), el("span", s.label));
      frag.append(row);
    }
    help.list.replaceChildren(frag);
    openDialog(help.dlg);
  };
  // the brand is a logo-only home link: no visible text, a name for assistive tech
  const brand = el("a", "", "brand");
  brand.href = "#/";
  brand.setAttribute("aria-label", "LLM Gateway — home");
  brand.title = "Sessions";
  // the capture-health banner: its own full-width row of the header, shown while the gateway
  // reports degraded. Its dismiss button hides it for this tab until the lost count rises or the
  // gateway restarts (spec 047 U11, D1); the dismissal lives in sessionStorage.
  const health = el("div", "", "health-banner");
  health.setAttribute("role", "status");
  health.hidden = true;
  const healthText = el("span", "", "health-text");
  const healthReason = el("span", "", "health-reason");
  const healthLink = el("a", "details", "health-link");
  healthLink.href = "/api/health";
  healthLink.target = "_blank";
  healthLink.rel = "noopener";
  healthLink.setAttribute("aria-label", "capture health details");
  const healthDismiss = el("button", "×", "health-dismiss");
  healthDismiss.type = "button";
  healthDismiss.setAttribute("aria-label", "dismiss capture health banner");
  healthDismiss.title = "dismiss for this tab";
  // the answer on screen, and the dismissal the tab has stored for it
  const dismissal = dismissalStore();
  let shown = null;
  healthDismiss.addEventListener("click", () => {
    if (shown) dismissal.set({ since: shown.since, lost: Number.isFinite(shown.lost) ? shown.lost : 0 });
    health.hidden = true;
  });
  health.append(healthText, healthReason, healthLink, healthDismiss);
  head.replaceChildren(brand, crumbs, live, liveNote, open, settings, help.dlg, health);
  // The header can wrap, so its height is published for the turn rail, which sticks
  // below it (F7).
  if (typeof ResizeObserver === "function" && root && root.style && typeof root.style.setProperty === "function") {
    new ResizeObserver(() => root.style.setProperty("--head-h", `${head.offsetHeight}px`)).observe(head);
  }

  return {
    settingsOpen() {
      return Boolean(settings.open);
    },
    // modalOpen is true while the Settings or the shortcuts dialog is up: the page takes no keys
    modalOpen() {
      return Boolean(settings.open || help.dlg.open);
    },
    openHelp: showHelp,
    setRoute(route, title, trail = null) {
      const frag = document.createDocumentFragment();
      const parts = breadcrumb(route, title, trail);
      parts.forEach((p, i) => {
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
      // the trail stops at a link back: a trailing slash says the page's heading follows
      if (parts.length && parts[parts.length - 1].href) frag.append(el("span", " /", "muted"));
      crumbs.replaceChildren(frag);
    },
    // setLive shows the stream's state as a dot, green while connected ("ok") and red
    // otherwise ("warn", "bad"), in both styles; no text (a page with no stream) shows nothing.
    // note, when given, is a short visible word beside the dot ("reconnecting…"), shown only
    // while there is a state to show.
    setLive(text, tone = "ok", note = "") {
      live.hidden = !text;
      liveWord.textContent = text;
      live.title = text;
      live.className = `live-dot live-${tone}`;
      liveNote.textContent = note;
      liveNote.hidden = !text || !note;
    },
    // setHealth shows or hides the capture-health banner from a parsed /api/health answer.
    setHealth(h) {
      const b = healthBanner(h);
      const dismissed = dismissal.get();
      const visible = bannerVisible(h, dismissed);
      // a degraded answer that still shows is past the stored dismissal (a new restart or a
      // higher count): the dismissal no longer applies, so it is cleared
      if (b.show && dismissed && visible) dismissal.clear();
      if (b.show) shown = h;
      // written only when they change: the banner is a live region, and a rewrite of the same
      // text makes a screen reader announce it again on every poll (spec 047 U12)
      const reason = b.reason ? ` — ${b.reason}` : "";
      if (health.hidden !== !visible) health.hidden = !visible;
      if (healthText.textContent !== b.text) healthText.textContent = b.text;
      if (healthReason.textContent !== reason) healthReason.textContent = reason;
    },
    // setKeyBar records the page's shortcuts for the "?" dialog.
    setKeyBar(route, pos) {
      bar = keyBar(route, pos);
    },
  };
}
