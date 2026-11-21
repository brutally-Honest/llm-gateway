import { timeElement } from "./entries.js";
import { COVERAGE, attributionText, changeGlyph, formatBytes, outcomeGlyph } from "./effects.js";
import { formatDuration, skeleton } from "./fmt.js";
import { card, note, pageBox, pageHead, startText, tableWithHead, unassignedRow } from "./sessions-page.js";

// The unassigned connections page (spec 014 AC15, restyled by spec 017 AC12): the coverage
// line, then the connections as a table and any unassigned file effects as a list, in the
// sessions list's column and cards, each status and label as glyph and word.

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

// fileList lists file effects, each with its change as glyph and word, its path, size and
// label; a diff or content button opens that effect's section below its row, and stays open
// across redraws. ctx = { isOpen(key), setOpen(key, on), getJSON, redraw }.
export function fileList(files, ctx) {
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
  return list;
}

// connectionRow is one unassigned connection as the page's table shows it: the sessions
// list's row (client, host, bytes, time) plus its label and outcome as glyph and word, its
// duration and request. A connection with no label is "○ unassigned" (research D45).
export function connectionRow(e) {
  const r = unassignedRow(e);
  return {
    ...r,
    label: attributionText(e.attribution) || "○ unassigned",
    outcome: outcomeGlyph(e.outcome),
    outcomeKey: e.outcome === "ok" ? "ok" : e.outcome === "refused" || e.outcome === "error" ? "failed" : "",
    dur: formatDuration(e.duration_ms ?? null),
    request: e.detail || e.via || "—",
  };
}

// unassignedStats are the page's labelled counts: connections, and files when any.
export function unassignedStats(items) {
  const conns = items.filter((e) => e.kind === "net_conn").length;
  const files = items.length - conns;
  return [[conns, conns === 1 ? "connection" : "connections"], [files ? files : null, files === 1 ? "file" : "files"]];
}

function td(tr, text, cls) {
  const c = el("td", cls);
  if (text !== null) c.textContent = text;
  tr.append(c);
  return c;
}

// createUnassignedPage returns { el, render(data) }; data = { items, loading, error } and
// ctx = { isOpen, setOpen, getJSON, redraw } for the file rows' sections.
export function createUnassignedPage(ctx) {
  const box = pageBox("unassigned-page");
  return {
    el: box,
    render({ items = [], loading = false, error = "" }) {
      box.replaceChildren(pageHead("Unassigned connections", loading || error ? [] : unassignedStats(items)));
      box.append(note("muted", "Connections made while no session, or more than one, was active."), note("muted effects-coverage", COVERAGE));
      if (error) return box.append(card(note("error", error)));
      if (loading) return box.append(card(skeleton("Loading…", 4, "rows")));
      const conns = items.filter((e) => e.kind === "net_conn");
      const files = items.filter((e) => e.kind === "file_change" || e.kind === "file_read");
      if (!conns.length && !files.length) return box.append(card(note("empty", "No unassigned connections.")));
      if (conns.length) {
        const { table, body } = tableWithHead([["Time", "time"], ["Label", "label"], ["Client", "src"], ["Host", "host"], ["Outcome", "status"], ["Bytes", "bytes"], ["Duration", "dur"], ["Request", "request"]]);
        for (const e of conns) {
          const r = connectionRow(e);
          const tr = document.createElement("tr");
          const t = timeElement(r.at);
          t.textContent = startText(r.at);
          td(tr, null, "time").append(t);
          td(tr, r.label, "label");
          td(tr, r.client, "src");
          td(tr, r.host, "host");
          td(tr, null, "status").append(el("span", r.outcomeKey ? `st st-${r.outcomeKey}` : "st", r.outcome));
          td(tr, r.bytes, "bytes");
          td(tr, r.dur, "dur");
          td(tr, r.request, "request");
          body.append(tr);
        }
        box.append(card(table));
      }
      if (files.length) {
        const h = el("h2", "page-sub", "Files");
        box.append(h, card(fileList(files, ctx)));
      }
    },
  };
}
