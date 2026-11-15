import { getBody } from "./api.js";
import { timeElement } from "./entries.js";
import { card, note, pageBox, tableWithHead } from "./sessions-page.js";
import { copyButton } from "./tool-card.js";

// The full exchange page (spec 013, restyled by spec 017 AC12): one stored exchange in the
// sessions list's column — its title and status as glyph and word, its facts as labelled
// cards like the call dialog's, its headers as tables (the Headers link of a call lands on
// them, research D43) and its two bodies, each loaded on first open.

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// fact is one labelled value (a string or a node), in the call dialog's fact card.
function fact(label, value) {
  const f = el("div", "mfact");
  const v = el("span", "v");
  if (value && typeof value === "object") v.append(value);
  else v.textContent = value === undefined || value === null || value === "" ? "—" : String(value);
  f.append(el("span", "l", label), v);
  return f;
}

function facts(...items) {
  const box = el("div", "mfacts");
  box.append(...items);
  return box;
}

function sub(title, id) {
  const h = el("h2", "page-sub", title);
  if (id) {
    h.id = id;
    h.tabIndex = -1;
  }
  return h;
}

// statusOf is the exchange's status as glyph and word: ✓ under 400, ✗ from 400 on or when
// none was recorded.
export function statusOf(x) {
  const word = x.status_word || (x.status ? `status ${x.status}` : "no status");
  return x.status && x.status < 400 ? { key: "ok", text: `✓ ${word}` } : { key: "failed", text: `✗ ${word}` };
}

// parseText is how the stored bodies parsed, as glyph and word.
export function parseText(p) {
  if (p === "ok") return "✓ ok";
  if (p === "failed") return "✗ failed";
  return `– ${(p || "unknown").replace(/_/g, " ")}`;
}

// usageFacts are the call's four counters, each labelled; "—" where the call did not report
// one, never 0; a note when it is not a model call or reported no usage.
function usageFacts(u) {
  if (!u) return [fact("Tokens", "not a model call")];
  if (!u.has_usage) return [fact("Tokens", `no usage (${(u.no_usage || "unknown").replace(/_/g, " ")})`)];
  const n = (v) => (v === null || v === undefined ? "—" : String(v));
  const out = [fact("Input tokens", n(u.input)), fact("Output tokens", n(u.output)), fact("Cache write", n(u.cache_write)), fact("Cache read", n(u.cache_read))];
  if (u.partial) out.push(fact("Usage", "partial"));
  return out;
}

const ms = (v) => (v === null || v === undefined ? "—" : `${Math.round(v)} ms`);
const yesNo = (b) => (b ? "yes" : "no");

function headersTable(h) {
  const names = Object.keys(h || {}).sort();
  if (names.length === 0) return card(note("empty", "No headers stored."));
  const { table, body } = tableWithHead([["Name", "hname"], ["Value", "hvalue"]]);
  for (const n of names) {
    const tr = el("tr");
    tr.append(el("td", "hname", n), el("td", "hvalue", (h[n] || []).join(", ")));
    body.append(tr);
  }
  return card(table);
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

// exchangeTitle is the page's title: the call's number in its session, else "Exchange".
export const exchangeTitle = (x) => (x && x.call_n ? `Call #${x.call_n}` : "Exchange");

// createExchangePage returns { el, render(data) }; data = { exchange, error, focus }, the
// exchange the API's JSON or null while it loads. focus "headers" scrolls to the request
// headers and moves focus there once they are drawn.
export function createExchangePage() {
  const box = pageBox("exchange-page");
  return {
    el: box,
    render({ exchange: x, error = "", focus = "" }) {
      box.replaceChildren();
      if (error) return box.append(card(note("error", error)));
      if (!x) return box.append(card(note("empty", "Loading...")));
      const head = el("div", "sp-head");
      const st = statusOf(x);
      const line = el("p", "stats");
      line.append(el("span", `st st-${st.key}`, st.text), el("code", "xp-path", `${x.method} ${x.path}`));
      head.append(el("h1", "", exchangeTitle(x)), line);

      let session = "none";
      if (x.session_id) {
        session = el("a", "", x.session_title || "this session");
        session.href = `#/sessions/${encodeURIComponent(x.session_id)}`;
      }
      const started = el("span");
      started.append(timeElement(x.started_at));
      const ended = el("span");
      ended.append(timeElement(x.ended_at));
      const reqHead = sub("Request headers", "headers");
      box.append(
        head,
        sub("Summary"),
        facts(fact("Client", x.client || "unknown"), fact("Protocol", x.protocol || "unknown"), fact("Model", x.model || "none recorded"), fact("Stream", yesNo(x.stream)), fact("Parse", parseText(x.parse)), fact("Session", session)),
        facts(...usageFacts(x.usage)),
        sub("Timings"),
        facts(fact("Started", started), fact("Time to first byte", ms(x.ttfb_ms)), fact("Ended", ended), fact("Duration", ms(x.duration_ms))),
        sub("Capture"),
        facts(
          fact("Request truncated", yesNo(x.request_truncated)),
          fact("Response truncated", yesNo(x.response_truncated)),
          fact("Request incomplete", yesNo(x.request_incomplete)),
          fact("Client disconnected", yesNo(x.client_disconnected)),
          fact("Upstream aborted", yesNo(x.upstream_aborted)),
          fact("Gateway error", x.gateway_error || "none"),
        ),
        reqHead,
        headersTable(x.request_headers),
        sub("Response headers"),
        headersTable(x.response_headers),
        sub("Bodies"),
        card(bodySection(x.id, "request", x.request_body_stored, x.request_truncated), bodySection(x.id, "response", x.response_body_stored, x.response_truncated)),
      );
      if (focus === "headers") {
        if (typeof reqHead.scrollIntoView === "function") reqHead.scrollIntoView({ block: "start" });
        if (typeof reqHead.focus === "function") reqHead.focus();
      }
    },
  };
}
