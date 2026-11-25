// The merge rule for a list row that arrives while an older one of the same id is held (spec
// 043 AC5, D10). A row has two parts, each with its own store version: the cheap part (what a
// pending row carries, read at `row_version`) and the facts (read at `version`). Equal
// versions are not "older", so a fill-in at the same version still lands.

const FIELDS = ["exchanges", "max_row", "parsed", "hooks", "max_hook"];

// CHEAP are the keys a pending row carries (besides list.folder and list.folder_full).
const CHEAP = ["id", "session_key", "client", "first_at", "last_at", "exchange_count", "active", "hooks_only", "has_calls", "effects", "row_version"];

// olderThan is Go's versionAfter turned round: a is strictly older than b when every field
// is <= and not all are equal. A missing version is never older.
export function olderThan(a, b) {
  if (!a || !b) return false;
  let equal = true;
  for (const f of FIELDS) {
    const x = a[f] ?? 0;
    const y = b[f] ?? 0;
    if (x > y) return false;
    if (x < y) equal = false;
  }
  return !equal;
}

// mergeRow folds an incoming row into the held one of the same id:
//   cheap part: taken from the incoming row unless its row_version is strictly older;
//   facts: taken from the incoming row when it has facts whose version is not strictly older;
//     an incoming pending row never removes held facts (they are kept, shown as filled).
// A row without row_version (an older server) is taken as it comes. When nothing changed the
// held object is returned, so the page finds nothing to write.
export function mergeRow(held, incoming) {
  if (!held) return incoming;
  if (!incoming.row_version) return incoming;
  const cheapIn = !olderThan(incoming.row_version, held.row_version);
  const heldFacts = !held.pending;
  const factsIn = !incoming.pending && !(heldFacts && olderThan(incoming.version, held.version));
  const cheap = cheapIn ? incoming : held;
  const facts = factsIn ? incoming : heldFacts ? held : null;
  let out;
  if (!facts) {
    out = cheap;
  } else if (facts === cheap) {
    out = facts;
  } else {
    out = { ...facts };
    for (const k of CHEAP) if (k in cheap) out[k] = cheap[k];
    else delete out[k];
    const c = cheap.list || {};
    out.list = { ...(facts.list || {}), folder: c.folder, folder_full: c.folder_full };
    if (!c.folder) delete out.list.folder;
    if (!c.folder_full) delete out.list.folder_full;
    delete out.pending;
  }
  return out === held || JSON.stringify(out) === JSON.stringify(held) ? held : out;
}

// mergeRows folds a list answer's rows into the held ones, in the answer's order: a row held
// is merged with its incoming one, a new row is taken. Rows the answer does not have are
// dropped (the answer is the page).
export function mergeRows(held, incoming) {
  const byID = new Map(held.map((r) => [r.id, r]));
  return incoming.map((r) => mergeRow(byID.get(r.id), r));
}
