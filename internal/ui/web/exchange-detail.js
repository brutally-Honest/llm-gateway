import { getBody } from "./api.js";
import { counterText, timeElement } from "./entries.js";
import { note, tableWithHead } from "./session-list.js";
import { copyButton } from "./tool-card.js";

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// fact adds a label and a value (a string or a node) to a definition list.
function fact(dl, label, value) {
  const dd = el("dd");
  dd.append(value);
  dl.append(el("dt", "muted", label), dd);
}

function section(title, ...nodes) {
  const s = el("section", "detail");
  s.append(el("h2", "", title), ...nodes);
  return s;
}

function usageLine(u) {
  if (!u) return "not a model call";
  if (!u.has_usage) return `no usage (${u.no_usage || "unknown"})`;
  const parts = [
    `in ${counterText(u.input)}`,
    `out ${counterText(u.output)}`,
    `cache write ${counterText(u.cache_write)}`,
    `cache read ${counterText(u.cache_read)}`,
  ];
  if (u.partial) parts.push("partial");
  return parts.join(" · ");
}

function headersTable(h) {
  const names = Object.keys(h || {}).sort();
  if (names.length === 0) return note("empty", "No headers stored.");
  const { table, body } = tableWithHead([["Name"], ["Value"]]);
  for (const n of names) {
    const tr = el("tr");
    tr.append(el("td", "", n), el("td", "", (h[n] || []).join(", ")));
    body.append(tr);
  }
  return table;
}

// bodySection is closed until opened. The raw body is fetched on the first open only, and
// a body the metadata says is not stored is never requested.
function bodySection(id, part, stored, captureTruncated) {
  const title = part === "request" ? "Request body" : "Response body";
  const d = el("details", "body");
  d.append(el("summary", "", title));
  const holder = el("div");
  d.append(holder);
  if (!stored) {
    holder.append(note("empty", "This body was not stored."));
    return d;
  }
  if (captureTruncated) holder.append(el("p", "muted", "Capture cut this body; the stored bytes are partial."));
  let loaded = false;
  d.addEventListener("toggle", async () => {
    if (!d.open || loaded) return;
    loaded = true;
    const notice = el("p", "muted", "Loading...");
    holder.append(notice);
    try {
      const { text, truncated } = await getBody(id, part);
      notice.remove();
      holder.append(copyButton(() => text), el("pre", "", text));
      if (truncated) holder.append(el("p", "muted", "Truncated: only the first 5 MiB are shown."));
    } catch (e) {
      notice.remove();
      if (e.code === "body_not_stored") {
        holder.append(note("empty", "This body was not stored."));
      } else {
        // a failed load may be retried by opening the section again
        loaded = false;
        holder.append(note("error", e.message));
      }
    }
  });
  return d;
}

// ExchangeDetail renders data = { exchange, error }; exchange is the API's exchange JSON or
// null while it loads. Bodies are loaded only when their section is opened.
export class ExchangeDetail extends HTMLElement {
  render(data) {
    this.replaceChildren();
    if (data.error) return this.append(note("error", data.error));
    const x = data.exchange;
    if (!x) return this.append(note("empty", "Loading..."));

    const head = el("section", "totals");
    head.append(el("strong", "", `${x.method} ${x.path}`));
    head.append(el("span", x.status >= 400 ? "badge err" : "", x.status ? `status ${x.status}` : "status unknown"));

    const summary = el("dl", "facts");
    fact(summary, "Client", x.client || "unknown");
    fact(summary, "Protocol", x.protocol || "unknown");
    fact(summary, "Stream", x.stream ? "yes" : "no");
    fact(summary, "Parse", x.parse || "unknown");
    fact(summary, "Model", x.model || "none recorded");
    fact(summary, "Usage", usageLine(x.usage));
    if (x.session_id) {
      const a = el("a", "", x.session_id);
      a.href = `#/sessions/${encodeURIComponent(x.session_id)}`;
      fact(summary, "Session", a);
    } else {
      fact(summary, "Session", "none");
    }

    const timings = el("dl", "facts");
    const started = el("span");
    started.append(timeElement(x.started_at));
    fact(timings, "Started", started);
    fact(timings, "Time to first byte", x.ttfb_ms === null || x.ttfb_ms === undefined ? "unknown" : `${Math.round(x.ttfb_ms)} ms`);
    const ended = el("span");
    ended.append(timeElement(x.ended_at));
    fact(timings, "Ended", ended);
    fact(timings, "Duration", `${Math.round(x.duration_ms)} ms`);

    const capture = el("dl", "facts");
    for (const [key, label] of [
      ["request_truncated", "Request truncated"],
      ["response_truncated", "Response truncated"],
      ["request_incomplete", "Request incomplete"],
      ["client_disconnected", "Client disconnected"],
      ["upstream_aborted", "Upstream aborted"],
    ]) {
      fact(capture, label, x[key] ? "yes" : "no");
    }
    fact(capture, "Gateway error", x.gateway_error || "none");

    this.append(
      head,
      section("Summary", summary),
      section("Timings", timings),
      section("Capture", capture),
      section("Request headers", headersTable(x.request_headers)),
      section("Response headers", headersTable(x.response_headers)),
      section(
        "Bodies",
        bodySection(x.id, "request", x.request_body_stored, x.request_truncated),
        bodySection(x.id, "response", x.response_body_stored, x.response_truncated),
      ),
    );
  }
}

customElements.define("exchange-detail", ExchangeDetail);
