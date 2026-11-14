import { COVERAGE, attributionText, changeGlyph, formatBytes, outcomeGlyph } from "./effects.js";
import { formatDuration } from "./model.js";

// The expanded effects list of the unassigned connections page (spec 014 AC15): the coverage
// line, the files, then the connections, each with its attribution. Moved here from the
// 013 drawer's sections when the drawer was retired (spec 017 T018).

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// effectSection is the "Effect" section of one file effect: a diff when the server has an
// earlier version, else the stored content. It fetches on open, through getJSON, and
// shows a loading line until then. The text goes in through textContent only.
export function effectSection(effect, getJSON) {
  const what = effect.has_diff ? "diff" : "content";
  const reason = effect.has_diff ? "what changed since the previous copy or the last commit" : "the content as stored";
  const body = el("pre", "effect-body muted", "loading…");
  const box = el("section", "effect-block");
  box.append(el("h3", "effect-section", "Effect"), el("p", "muted effect-reason", reason), body);
  getJSON(`effects/${effect.id}/${what}`)
    .then((r) => {
      body.className = "effect-body";
      body.textContent = what === "diff" ? r.diff || "no difference" : r.content;
      if (r.truncated) box.append(el("p", "muted", "diff truncated"));
    })
    .catch((e) => {
      body.className = "effect-body error";
      body.textContent = `not available: ${e.message}`;
    });
  return box;
}

// attribution appends an effect's label and tool as one more text cell; nothing when the
// server sent none.
function attribution(li, e, ctx) {
  const t = attributionText(e.attribution, ctx.toolNos);
  if (t) li.append(el("span", `effect-attribution attribution-${e.attribution.label}`, ` · ${t}`));
}

// effectsListNode is an expanded effects list: the coverage line, then the files, then the
// connections. ctx = { isOpen(key), setOpen(key, on), getJSON, redraw, toolNos }: a diff or
// content link opens that effect's section below its row, and stays open across redraws.
// toolNos maps a tool call id to its model call's #n for the attribution text (spec 015).
export function effectsListNode(items, ctx) {
  const box = el("div", "effects-list");
  box.append(el("p", "muted effects-coverage", COVERAGE));
  const files = items.filter((e) => e.kind === "file_change" || e.kind === "file_read");
  const conns = items.filter((e) => e.kind === "net_conn");
  if (files.length) {
    const list = el("ul", "effects-files");
    for (const e of files) {
      const li = el("li", `effect-row effect-${e.kind}`);
      const change = e.kind === "file_read" ? "read" : e.change;
      li.append(el("span", `effect-change change-${change}`, changeGlyph(change)), el("span", "effect-path", ` ${e.path}`));
      if (e.kind === "file_change") li.append(el("span", "muted", ` ${formatBytes(e.size)}`));
      if (e.detail) li.append(el("span", "muted", ` ${e.detail}`));
      attribution(li, e, ctx);
      if (e.has_diff || e.has_copy) {
        const key = `effect:${e.id}`;
        const b = el("button", "link-button", e.has_diff ? "diff" : "content");
        b.type = "button";
        b.setAttribute("aria-expanded", String(ctx.isOpen(key)));
        b.addEventListener("click", () => {
          ctx.setOpen(key, !ctx.isOpen(key));
          ctx.redraw();
        });
        li.append(el("span", "", " "), b);
        if (ctx.isOpen(key)) li.append(effectSection(e, ctx.getJSON));
      }
      list.append(li);
    }
    box.append(list);
  }
  if (conns.length) {
    const list = el("ul", "effects-conns");
    for (const e of conns) {
      const li = el("li", `effect-row effect-net outcome-${(e.outcome || "").replace(/ /g, "-")}`);
      li.append(el("span", "effect-outcome", outcomeGlyph(e.outcome)), el("span", "effect-host", ` ${e.host}:${e.port}`));
      li.append(el("span", "muted", ` ↑ ${formatBytes(e.bytes_up)} ↓ ${formatBytes(e.bytes_down)} · ${formatDuration(e.duration_ms ?? null)} · ${e.via}`));
      if (e.detail) li.append(el("span", "muted", ` ${e.detail}`));
      attribution(li, e, ctx);
      list.append(li);
    }
    box.append(list);
  }
  if (!files.length && !conns.length) box.append(el("p", "muted", "no effects"));
  return box;
}
