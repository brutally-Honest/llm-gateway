// Hash routes. A call's URL is #/sessions/{id}/call/{exchangeId} (spec 017 AC5).
import { normalizeKind } from "./filters.js";

const enc = encodeURIComponent;

// parseRoute turns a location hash into { name, id?, call? }; anything else is "unknown".
export function parseRoute(hash) {
  const h = hash || "#/";
  try {
    if (h === "#/" || h === "#") return { name: "sessions", kind: "all", q: "" };
    // the list's state is in the hash query: #/?kind=active&q=some%20text (spec 029)
    if (h.startsWith("#/?")) {
      const p = new URLSearchParams(h.slice(3));
      return { name: "sessions", kind: normalizeKind(p.get("kind")), q: p.get("q") || "" };
    }
    if (h === "#/ungrouped") return { name: "ungrouped" };
    if (h === "#/effects/unassigned") return { name: "unassigned" };
    if (h.startsWith("#/exchanges/")) {
      // #/exchanges/{id}/headers lands on the page's headers (research D43)
      const rest = h.slice(12);
      if (rest.endsWith("/headers")) return { name: "exchange", id: decodeURIComponent(rest.slice(0, -8)), focus: "headers" };
      return { name: "exchange", id: decodeURIComponent(rest) };
    }
    if (h.startsWith("#/sessions/")) {
      const rest = h.slice(11);
      const i = rest.indexOf("/call/");
      if (i < 0) return { name: "session", id: decodeURIComponent(rest), call: null };
      return { name: "session", id: decodeURIComponent(rest.slice(0, i)), call: decodeURIComponent(rest.slice(i + 6)) };
    }
  } catch {
    // malformed escape: fall through
  }
  return { name: "unknown" };
}

// buildRoute is the inverse of parseRoute.
export function buildRoute(r) {
  switch (r.name) {
    case "sessions": {
      // kind is left out when all, q when empty
      const parts = [];
      const kind = normalizeKind(r.kind);
      if (kind !== "all") parts.push(`kind=${enc(kind)}`);
      if (r.q) parts.push(`q=${enc(r.q)}`);
      return parts.length ? `#/?${parts.join("&")}` : "#/";
    }
    case "ungrouped":
      return "#/ungrouped";
    case "unassigned":
      return "#/effects/unassigned";
    case "exchange":
      return `#/exchanges/${enc(r.id)}${r.focus === "headers" ? "/headers" : ""}`;
    case "session":
      return r.call ? `#/sessions/${enc(r.id)}/call/${enc(r.call)}` : `#/sessions/${enc(r.id)}`;
    default:
      return "#/";
  }
}
