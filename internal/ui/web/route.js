// Hash routes. A drawer URL is #/sessions/{id}/call/{exchangeId}.
const enc = encodeURIComponent;

// parseRoute turns a location hash into { name, id?, call? }; anything else is "unknown".
export function parseRoute(hash) {
  const h = hash || "#/";
  try {
    if (h === "#/" || h === "#") return { name: "sessions" };
    if (h === "#/ungrouped") return { name: "ungrouped" };
    if (h.startsWith("#/exchanges/")) return { name: "exchange", id: decodeURIComponent(h.slice(12)) };
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
    case "sessions":
      return "#/";
    case "ungrouped":
      return "#/ungrouped";
    case "exchange":
      return `#/exchanges/${enc(r.id)}`;
    case "session":
      return r.call ? `#/sessions/${enc(r.id)}/call/${enc(r.call)}` : `#/sessions/${enc(r.id)}`;
    default:
      return "#/";
  }
}
