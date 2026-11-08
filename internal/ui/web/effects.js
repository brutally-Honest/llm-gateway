// Effects (spec 014): pure helpers that place a session's effects in its turns and name
// each status with a glyph and a word. The server computes every fact; this only places
// and labels them.

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

function bucket() {
  return { changed: 0, read: 0, sites: 0, items: [], paths: { changed: new Set(), read: new Set(), sites: new Set() } };
}

function add(b, e) {
  b.items.push(e);
  if (e.kind === "file_change") b.paths.changed.add(e.path);
  else if (e.kind === "file_read") b.paths.read.add(e.path);
  else if (e.kind === "net_conn") b.paths.sites.add(e.host);
  b.changed = b.paths.changed.size;
  b.read = b.paths.read.size;
  b.sites = b.paths.sites.size;
}

const ms = (iso) => {
  const t = Date.parse(iso || "");
  return Number.isNaN(t) ? null : t;
};

// placeEffects puts each effect in the turn whose time range holds it: from the turn's
// start up to the next turn's start. An effect before the first turn goes to `before`;
// one at a turn's start belongs to that turn; one after the last turn's start to the last
// turn. Returns { before, turns } with one bucket per turn, in the turns' order; a bucket
// is { changed, read, sites, items } where the counts are distinct paths and hosts.
export function placeEffects(turns, effects) {
  const out = { before: bucket(), turns: turns.map(() => bucket()) };
  const starts = turns.map((t) => ms(t.start));
  for (const e of effects || []) {
    const at = ms(e.at);
    let i = -1;
    for (let j = 0; j < starts.length; j++) {
      if (starts[j] !== null && at !== null && starts[j] <= at) i = j;
    }
    add(i < 0 ? out.before : out.turns[i], e);
  }
  return out;
}

// countsText is a bucket's folded-row text: "3 changed · 12 read · 2 sites".
export function countsText(b) {
  return `${b.changed} changed · ${b.read} read · ${b.sites} ${b.sites === 1 ? "site" : "sites"}`;
}

// formatBytes is a size in B, KB or MB.
export function formatBytes(n) {
  if (n === null || n === undefined) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

// lastID is the highest effect id held, 0 for none.
export function lastID(effects) {
  let n = 0;
  for (const e of effects || []) if (e.id > n) n = e.id;
  return n;
}
