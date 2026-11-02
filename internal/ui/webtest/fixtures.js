// Builders for timeline entries, shared by the model tests.
let seq = 0;
export function call(id, n, o = {}) {
  const { main = true, side = false, prev = "", ms = 1000, usage, status = 200, thread = 0 } = o;
  return {
    id: `${id}:0`, exchange_id: id, index: 0, start: `2026-01-01T00:00:${String(n).padStart(2, "0")}Z`,
    kind: "model_call", model: "m", status, duration_ms: ms,
    usage: usage || { has_usage: true, partial: false, input: 10, output: 5, cache_write: 0, cache_read: 0 },
    call: { n, thread, prev_id: prev, side_call: side, main, carried: 0, new: 1 },
  };
}
export function item(id, kind, extra = {}) {
  return { id: `${id}:${++seq}`, exchange_id: id, index: seq, start: "", kind, ...extra };
}
