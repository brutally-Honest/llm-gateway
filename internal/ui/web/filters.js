// Pure list and timeline filters over the loaded rows. Nothing here fetches: a filter
// only narrows what is already loaded (AC6, AC14).

export const FILTERS = ["all", "errors", "gaps", "hooks"];

const count = (n) => (typeof n === "number" ? n : 0);

// hasGaps: a tool call in the "? unmatched" state, or a call with missing or partial usage.
function hasGaps(s) {
  const u = s.usage || {};
  return count((s.tool_states || {}).unmatched) > 0 || count(u.missing) > 0 || count(u.partial) > 0;
}

function hooksOnly(s) {
  return Boolean(s.hooks_only) || (Boolean(s.has_hooks) && !s.has_calls);
}

function rowText(s) {
  return [s.title, s.client, s.session_key, s.id, ...(s.models || [])].filter(Boolean).join("\n").toLowerCase();
}

// filterSessions narrows loaded rows by kind (all, errors, gaps, hooks) and text. Errors
// reads main_errors, so side calls never match. The note reads "filtering N loaded" while
// more rows exist, "no sessions match" when nothing does; loadMore says whether to offer
// Load more.
export function filterSessions(rows, { kind = "all", text = "", hasMore = false } = {}) {
  const q = text.trim().toLowerCase();
  const out = rows.filter((s) => {
    if (kind === "errors" && count(s.main_errors) <= 0) return false;
    if (kind === "gaps" && !hasGaps(s)) return false;
    if (kind === "hooks" && !hooksOnly(s)) return false;
    return !q || rowText(s).includes(q);
  });
  const filtering = kind !== "all" || q !== "";
  let note = "";
  if (!out.length && filtering) note = "no sessions match";
  else if (filtering && hasMore) note = `filtering ${rows.length} loaded`;
  return { rows: out, note, loadMore: hasMore };
}

// filterTurns narrows the loaded timeline cards by text (prompt, replies, tool names) and
// by tool name; a card keeps only the tool rows that match the picker.
export function filterTurns(cards, { text = "", tool = "" } = {}) {
  const q = text.trim().toLowerCase();
  const out = [];
  for (const card of cards) {
    const tools = tool ? card.tools.filter((t) => t.name === tool) : card.tools;
    if (tool && !tools.length) continue;
    if (q) {
      const hay = [card.prompt && card.prompt.text, ...card.texts, ...card.tools.map((t) => t.name)]
        .filter(Boolean).join("\n").toLowerCase();
      if (!hay.includes(q)) continue;
    }
    out.push({ ...card, tools });
  }
  return out;
}

// toolNames lists the distinct tool names in the loaded cards, for the picker.
export function toolNames(cards) {
  return [...new Set(cards.flatMap((c) => c.tools.map((t) => t.name).filter(Boolean)))].sort();
}
