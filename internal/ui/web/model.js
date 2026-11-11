import { compareEntries } from "./entries.js";

// Pure model over timeline entries for the 013 drawer: chips, call numbers and context. The
// browser never classifies by message text; every fact comes from the API fields.

// callNumbers maps each model call's exchange id to its #n: its position among the
// session's calls in start order, counting every thread, with the exchange id breaking a
// tie. The server sends only that order key, so a late commit renumbers the calls here
// without resending them. Links and drawer URLs stay keyed by exchange id.
export function callNumbers(entries) {
  const calls = entries.filter((e) => e.kind === "model_call").sort(compareEntries);
  return new Map(calls.map((e, i) => [e.exchange_id, i + 1]));
}

const CHIPS = {
  ok: { key: "ok", glyph: "✓", text: "ok" },
  failed: { key: "failed", glyph: "✗", text: "failed" },
  "not-run": { key: "not-run", glyph: "⊘", text: "not run" },
  unmatched: { key: "unmatched", glyph: "?", text: "unmatched" },
  pending: { key: "pending", glyph: "…", text: "pending" },
  "no-hooks": { key: "no-hooks", glyph: "–", text: "no hooks" },
};

// CHIP_KEYS lists the chips in display order.
export const CHIP_KEYS = Object.keys(CHIPS);

// chipOf is display only: it looks up the glyph and text of the chip the server set on
// a tool call (call.chip) and builds the hover title from the call's full state, reason
// and outcome. It never derives a chip; a missing or unknown chip reads as no hooks.
export function chipOf(call) {
  const c = call || {};
  const base = CHIPS[c.chip] || CHIPS["no-hooks"];
  let title = [c.state, c.reason, c.outcome].filter(Boolean).join(": ") || base.text;
  // A hook-seen call carries the outcome its hooks reported, not a matched one.
  if (c.kind === "hook_event") {
    title = [c.state, c.reason].filter(Boolean).join(": ") || base.text;
    if (c.outcome) title += ` · hooks: ${c.outcome}`;
  }
  return { ...base, title };
}

// formatDuration renders milliseconds as 850ms, 1.2s or 2m05s; "—" when unknown.
export function formatDuration(ms) {
  if (ms === null || ms === undefined) return "—";
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  const s = Math.round(ms / 1000);
  return `${Math.floor(s / 60)}m${String(s % 60).padStart(2, "0")}s`;
}

// agentLabel is the agent label of a call, tool or hook line, or "" for the main agent.
export function agentLabel(e) {
  if (!e.agent_id && !e.agent_type) return "";
  return `agent ${[e.agent_type, e.agent_id].filter(Boolean).join(" ")}`;
}

const reported = (v) => v !== null && v !== undefined;

// contextOf is one call's context: input + cache write + cache read. Any of the three
// unreported gives value null (shown "—", never 0), flagged partial when some were
// reported (E4, E5).
export function contextOf(call) {
  const u = (call && call.usage) || {};
  const parts = [u.input, u.cache_write, u.cache_read];
  const have = parts.filter(reported).length;
  if (have === 3) return { value: u.input + u.cache_write + u.cache_read, partial: false };
  return { value: null, partial: have > 0 };
}

const isMain = (e) => e.kind === "model_call" && Boolean(e.call) && e.call.main && !e.call.side_call;

// contextSeries is the trend of main-thread calls: side and sub-agent calls are left
// out, and a call without a context value has no point (never 0). With no main calls
// there is a note instead (E22).
export function contextSeries(entries) {
  const mains = entries.filter(isMain);
  if (!mains.length) return { points: [], note: "no main-thread calls to plot" };
  const numbers = callNumbers(entries);
  const points = [];
  for (const e of mains) {
    const ctx = contextOf(e);
    if (ctx.value !== null) points.push({ n: numbers.get(e.exchange_id), exchangeId: e.exchange_id, value: ctx.value });
  }
  return { points, note: points.length ? "" : "no main-thread call reported a context" };
}

// deltaFor is a call's change in context: kind "first" for the first call of a thread,
// "none" when the call has no context, else the change from the nearest earlier call on
// its prev_id chain that has a context (E4, E6).
export function deltaFor(call, byId) {
  const prevId = (call.call || {}).prev_id;
  if (!prevId) return { kind: "first", value: null };
  const cur = contextOf(call);
  if (cur.value === null) return { kind: "none", value: null };
  let c = byId.get(prevId);
  for (let seen = 0; c && seen < 10000; c = byId.get((c.call || {}).prev_id), seen++) {
    const ctx = contextOf(c);
    if (ctx.value !== null) return { kind: "delta", value: cur.value - ctx.value, from: c.exchange_id };
  }
  return { kind: "none", value: null };
}

export const PLOT_WIDTH = 800;
export const PLOT_HEIGHT = 60;
const PLOT_PAD_X = 20;
const PLOT_TOP = 8;
const PLOT_BOTTOM = 52;

// contextPlot is the context line as a polyline with one dot per main-thread call
// (AC12), in a box `width` wide and PLOT_HEIGHT high: calls are spread evenly left to right and
// the height runs from the smallest context (bottom) to the largest (top). Side and
// sub-agent calls are left out, as in contextSeries; E22's note stands when there is
// no point to plot.
export function contextPlot(entries, width = PLOT_WIDTH) {
  const { points, note } = contextSeries(entries);
  if (!points.length) return { note, width, points: [], polyline: "", label: "" };
  const values = points.map((p) => p.value);
  const lo = Math.min(...values);
  const hi = Math.max(...values);
  const step = points.length > 1 ? Math.max(0, width - 2 * PLOT_PAD_X) / (points.length - 1) : 0;
  const plotted = points.map((p, i) => {
    const x = points.length > 1 ? PLOT_PAD_X + i * step : width / 2;
    const y = hi === lo ? (PLOT_TOP + PLOT_BOTTOM) / 2 : PLOT_BOTTOM - ((p.value - lo) / (hi - lo)) * (PLOT_BOTTOM - PLOT_TOP);
    return { ...p, x: Number(x.toFixed(1)), y: Number(y.toFixed(1)), title: `#${p.n} · context ${p.value}` };
  });
  const n = plotted.length;
  return {
    note: "",
    width,
    points: plotted,
    polyline: plotted.map((p) => `${p.x},${p.y}`).join(" "),
    label: `Context per main-thread call, ${values[0]} to ${values[n - 1]} tokens over ${n} ${n === 1 ? "call" : "calls"}`,
  };
}
