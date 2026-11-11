// Effects (spec 014): pure helpers that name each effect status with a glyph and a word,
// for the drawer and the unassigned list. The server computes every fact; this only
// labels them.

// COVERAGE is the line at the top of every effects list: what is and is not seen (AC15).
export const COVERAGE =
  "Files: changes seen at the agent's hook checkpoints, plus the agent's own reads. Network: traffic through the gateway proxy only; with the sandbox on, all Bash commands; MCP servers only if they use the proxy.";

const CHANGE = { created: "+ created", modified: "~ modified", deleted: "− deleted", read: "→ read" };
const OUTCOME = { ok: "✓ ok", refused: "✕ refused", error: "! error", "closed early": "… closed early" };

// changeGlyph is a file effect's change as glyph and word; a read is "→ read".
export function changeGlyph(change) {
  return CHANGE[change] || `? ${change || "unknown"}`;
}

// outcomeGlyph is a connection's outcome as glyph and word.
export function outcomeGlyph(outcome) {
  return OUTCOME[outcome] || `? ${outcome || "unknown"}`;
}

// formatBytes is a size in B, KB or MB.
export function formatBytes(n) {
  if (n === null || n === undefined) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

const ATTRIBUTION = {
  named: "● named",
  only_one: "◐ only one running",
  several: "◌ several running",
  outside: "○ outside any tool call",
  harness: "⚙ harness",
};

// toolText is one tool call as text: "#n Name" when toolNos (tool call id -> its model
// call's #n) knows it, else the name alone; a sub-agent's call adds "(agent)".
export function toolText(t, toolNos) {
  const n = toolNos && toolNos.get(t.id);
  return `${n ? `#${n} ` : ""}${t.name || "tool"}${t.agent_id ? " (agent)" : ""}`;
}

// attributionText is an effect's label as glyph and word, then its tool or candidates
// (spec 015 AC12), or for an unassigned harness connection the client whose harness list
// holds the host; "" when the server sent no label. The browser never derives a label.
export function attributionText(a, toolNos) {
  if (!a || !a.label) return "";
  const word = ATTRIBUTION[a.label] || `? ${a.label}`;
  if (a.tool) return `${word}: ${toolText(a.tool, toolNos)}`;
  if (a.candidates && a.candidates.length) return `${word}: ${a.candidates.map((t) => toolText(t, toolNos)).join(", ")}`;
  if (a.client) return `${word}: ${a.client}`;
  return word;
}

// lastID is the highest effect id held, 0 for none.
export function lastID(effects) {
  let n = 0;
  for (const e of effects || []) if (e.id > n) n = e.id;
  return n;
}
