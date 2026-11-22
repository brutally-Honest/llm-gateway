// The list's filter kinds. The server filters and searches (GET /api/sessions takes kind and
// q, spec 029); the browser only names the kinds and reads one back from the URL.

export const FILTERS = ["all", "look", "active", "hooks"];

// normalizeKind reads a kind from the URL: an unknown or empty one is "all".
export function normalizeKind(k) {
  return FILTERS.includes(k) ? k : "all";
}
