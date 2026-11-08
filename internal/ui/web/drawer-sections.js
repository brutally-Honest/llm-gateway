import { agentLabel, callNumbers, contextOf, contextPlot, deltaFor } from "./model.js";
import { fitContextPlot } from "./plot.js";

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

const num = (v) => (v === null || v === undefined ? "—" : String(v));
const why = (reason) => String(reason || "").replace(/_/g, " ");

function fact(label, value) {
  const row = el("div", "drawer-fact");
  row.append(el("span", "muted", `${label}: `), el("span", "", value));
  return row;
}

// section is a heading, a line saying why the section is there, and its body.
function section(title, reason, ...body) {
  const box = el("section", "drawer-block");
  const h = el("h3", "drawer-section", title);
  box.append(h, el("p", "muted drawer-reason", reason), ...body);
  return box;
}

// detailNote is the line a section shows when the exchange detail is not here.
function detailNote(detail) {
  if (detail.state === "loading") return "loading…";
  return `detail unavailable: ${detail.error}`;
}

function contextSection(call, entries, detail) {
  const byId = new Map(entries.filter((e) => e.kind === "model_call").map((e) => [e.exchange_id, e]));
  const ctx = contextOf(call);
  const u = call.usage || {};
  const body = [];
  const size = ctx.value === null ? "—" : `${ctx.partial ? "~" : ""}${ctx.value}`;
  body.push(fact("Context size", size));
  if (ctx.value === null) body.push(el("p", "muted", ctx.partial ? "some counters were not reported" : "no usage reported"));
  body.push(fact("Input", num(u.input)), fact("Cache write", num(u.cache_write)), fact("Cache read", num(u.cache_read)));
  const d = deltaFor(call, byId);
  if (d.kind === "first") body.push(fact("Change since previous call", "first call"));
  else if (d.kind === "none") body.push(fact("Change since previous call", "—"));
  else {
    const from = callNumbers(entries).get(d.from);
    body.push(fact("Change since previous call", `${d.value > 0 ? "+" : ""}${d.value}${from ? ` since #${from}` : ""}`));
  }
  const plot = contextPlot(entries);
  if (!plot.note) body.push(fitContextPlot((width) => contextPlot(entries, width), call.exchange_id));
  const here = plot.points.some((p) => p.exchangeId === call.exchange_id);
  body.push(el("p", "muted", plot.note || (here ? "this call is marked" : "this call is not on the main thread plot")));
  return section("Context", "how full the context was, and how it moved", ...body);
}

function messagesSection(detail) {
  const reason = "what the model saw, carried over from the previous call or new";
  if (!detail.data) return section("What the model saw", reason, el("p", "muted", detailNote(detail)), fact("Messages", "—"));
  const x = detail.data;
  const body = [];
  if (x.thread_new) body.push(el("p", "", "starts a new thread"));
  if (x.messages_unavailable) {
    body.push(el("p", "muted", why(x.messages_unavailable)), fact("Messages", "—"));
    return section("What the model saw", reason, ...body);
  }
  body.push(el("p", "", `${x.carried} carried over · ${x.new_count} new`));
  const list = el("ul", "drawer-messages");
  for (const m of x.messages) {
    const li = el("li", m.new ? "is-new" : "is-carried");
    li.append(el("span", "badge", m.new ? "new" : "carried"), el("span", "muted", ` ${m.role} `), el("span", "", m.truncated ? `${m.text}…` : m.text));
    list.append(li);
  }
  body.push(list);
  return section("What the model saw", reason, ...body);
}

function responseSection(call, detail) {
  const reason = "the reply, assembled from the stored stream";
  if (!detail.data) return section("Response", reason, el("p", "muted", detailNote(detail)), fact("Output tokens", num((call.usage || {}).output)));
  const r = detail.data.response || {};
  const body = [];
  if (r.unavailable) body.push(el("p", "muted", why(r.unavailable)), fact("Response", "—"));
  else body.push(el("pre", "drawer-response", r.text || ""));
  body.push(fact("Stop reason", r.stop_reason || "—"), fact("Output tokens", num((call.usage || {}).output)));
  return section("Response", reason, ...body);
}

// chainSteps is a tool call's asked, ran and reported steps from its state.
export function chainSteps(t) {
  const asked = { label: "asked", glyph: "✓", text: "asked" };
  switch (t.state) {
    case "asked+ran+reported":
      return [asked, { label: "ran", glyph: "✓", text: "ran" }, { label: "reported", glyph: t.outcome === "failed" ? "✗" : "✓", text: t.outcome === "failed" ? "reported failed" : "reported" }];
    case "ran-not-reported":
      return [asked, { label: "ran", glyph: "✓", text: "ran" }, { label: "reported", glyph: "—", text: "reported missing" }];
    case "asked-not-run":
      return [asked, { label: "ran", glyph: "⊘", text: t.reason ? `not run (${t.reason})` : "not run" }, { label: "reported", glyph: "—", text: "not reported" }];
    case "pending":
      return [asked, { label: "ran", glyph: "…", text: "ran pending" }, { label: "reported", glyph: "…", text: "reported pending" }];
    case "ran-not-asked-or-not-captured":
      return [{ label: "asked", glyph: "?", text: "asked not captured" }, { label: "ran", glyph: "✓", text: "ran" }, { label: "reported", glyph: "?", text: "reported unknown" }];
    default:
      return [asked, { label: "ran", glyph: "—", text: "ran unknown" }, { label: "reported", glyph: "—", text: "reported unknown" }];
  }
}

function toolsSection(call, entries, detail) {
  const reason = "what the model asked for, and whether it ran and was reported";
  const mine = entries.filter((e) => e.kind === "tool_call" && e.exchange_id === call.exchange_id);
  let tools = mine;
  if (detail.data && detail.data.response_tool_call_ids.length) {
    const byTc = new Map(entries.filter((e) => e.kind === "tool_call").map((e) => [e.tool_call_id, e]));
    tools = detail.data.response_tool_call_ids.map((id) => byTc.get(id)).filter(Boolean);
  }
  if (!tools.length) return section("Tools", reason, el("p", "muted", "no tool use in this call"));
  const list = el("ul", "drawer-chain");
  for (const t of tools) {
    const li = el("li", "chain-row");
    li.append(el("strong", "", t.name || "tool"));
    const label = agentLabel(t);
    if (label) li.append(el("span", "badge harness", label));
    for (const s of chainSteps(t)) li.append(el("span", `chain-step step-${s.label}`, ` ${s.glyph} ${s.text}`));
    list.append(li);
  }
  return section("Tools", reason, list);
}

function healthSection(call, detail) {
  const reason = "gateway disconnect, abort and error facts";
  if (!detail.data) return section("Health", reason, el("p", "muted", detailNote(detail)));
  const x = detail.data;
  return section("Health", reason,
    fact("Status", x.status ? String(x.status) : "—"),
    fact("Client disconnected", x.client_disconnected ? "yes" : "no"),
    fact("Upstream aborted", x.upstream_aborted ? "yes" : "no"),
    fact("Gateway error", x.gateway_error || "none"),
    fact("Session", x.session_id || "—"));
}

function link(href, text) {
  const a = el("a", "", text);
  a.href = href;
  return a;
}

// rawSection links the raw bodies, headers and the full page. The links need only the id,
// so they work when the detail did not load.
function rawSection(call, detail) {
  const id = encodeURIComponent(call.exchange_id);
  const x = detail.data;
  const body = el("div", "drawer-links");
  const part = (name, stored) => {
    if (x && !stored) return el("span", "muted", `${name} body: not stored`);
    const a = link(`/api/exchanges/${id}/body?part=${name}`, `Raw ${name} body`);
    a.target = "_blank";
    return a;
  };
  body.append(part("request", x ? x.request_body_stored : true), part("response", x ? x.response_body_stored : true),
    link(`#/exchanges/${id}`, "All headers"), link(`#/exchanges/${id}`, "Full page"));
  return section("Raw", "the stored bodies and headers, unparsed", body);
}

// buildSections is the drawer body for one call: call is its model_call entry, detail is
// { state: "loading" | "ready" | "error", data, error }, entries the session's timeline.
export function buildSections(call, entries, detail) {
  return [
    contextSection(call, entries, detail),
    messagesSection(detail),
    responseSection(call, detail),
    toolsSection(call, entries, detail),
    healthSection(call, detail),
    rawSection(call, detail),
  ];
}
