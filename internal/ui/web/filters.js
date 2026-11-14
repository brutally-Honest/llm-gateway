// Pure list filters over the loaded rows. Nothing here fetches: a filter only narrows what
// is already loaded (spec 017 AC4), and reads only flags and counts the server sent.

export const FILTERS = ["all", "look", "active", "hooks"];

function hooksOnly(s) {
  return Boolean(s.hooks_only) || (Boolean(s.has_hooks) && !s.has_calls);
}

// needsLook is the server's count of the session's "Needs a look" items (spec 017 AC2).
function needsLook(s) {
  const n = (s.list || {}).needs_look;
  return typeof n === "number" && n > 0;
}

function rowText(s) {
  const l = s.list || {};
  return [s.title, s.client, s.session_key, s.id, l.folder, l.folder_full, ...(s.models || [])].filter(Boolean).join("\n").toLowerCase();
}

// filterSessions narrows loaded rows by kind (all, look, active, hooks) and text. The note
// reads "filtering N loaded" while a filter is on and more rows exist (when nothing
// matches, the list itself says so, not the note, D34); loadMore says whether to offer Load more, which a filter never hides (E6).
export function filterSessions(rows, { kind = "all", text = "", hasMore = false } = {}) {
  const q = text.trim().toLowerCase();
  const out = rows.filter((s) => {
    if (kind === "look" && !needsLook(s)) return false;
    if (kind === "active" && !s.active) return false;
    if (kind === "hooks" && !hooksOnly(s)) return false;
    return !q || rowText(s).includes(q);
  });
  const filtering = kind !== "all" || q !== "";
  const note = filtering && hasMore ? `filtering ${rows.length} loaded` : "";
  return { rows: out, note, loadMore: hasMore };
}
