// Builders for timeline entries, shared by the model tests.
let seq = 0;
// call builds a model call that starts n seconds in, so n is also its #n when the calls
// before it are present; the browser derives #n from the start, never from a field.
export function call(id, n, o = {}) {
  const { main = true, side = false, prev = "", ms = 1000, usage, status = 200 } = o;
  return {
    id: `${id}:0`, exchange_id: id, index: 0, start: new Date(Date.UTC(2026, 0, 1, 0, 0, n)).toISOString(),
    kind: "model_call", model: "m", status, duration_ms: ms,
    usage: usage || { has_usage: true, partial: false, input: 10, output: 5, cache_write: 0, cache_read: 0 },
    call: { prev_id: prev, side_call: side, main, carried: 0, new: 1 },
  };
}
export function item(id, kind, extra = {}) {
  return { id: `${id}:${++seq}`, exchange_id: id, index: seq, start: "", kind, ...extra };
}
