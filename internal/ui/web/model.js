import { compareEntries } from "./entries.js";

// Pure model over timeline entries: chips, turns, call links and the stats row. The
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

function newCard(kind, number, prompt) {
  return {
    kind, number, prompt: withReminders(prompt), texts: [], tools: [], calls: [], hooks: [],
    counts: { calls: 0, tools: 0 }, durationMs: 0, errored: false, hooksOnly: false, start: "", heading: "",
  };
}

// withReminders reads the prompt's split from the server: `body` is what the user wrote
// and `reminders` the injected blocks (AC16). An entry without them, a client with no
// rules or a hook prompt, is shown whole as its body (E20).
function withReminders(prompt) {
  return prompt ? { ...prompt, body: prompt.body ?? prompt.text ?? "", reminders: prompt.reminders ?? [] } : prompt;
}

// hooksLabel is the toggle's text: "1 hook" or "N hooks".
export function hooksLabel(n) {
  return `${n} ${n === 1 ? "hook" : "hooks"}`;
}

const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

// headingOf is a card's heading: its name, counts and total duration.
function headingOf(card) {
  const name = card.kind === "turn" ? `Turn ${card.number}` : card.kind === "before" ? "Before first prompt" : "Side calls";
  const parts = [name];
  if (card.counts.calls > 0) parts.push(plural(card.counts.calls, "call", "calls"));
  parts.push(plural(card.counts.tools, "tool", "tools"));
  if (card.counts.calls > 0) parts.push(formatDuration(card.durationMs));
  return parts.join(" · ");
}

// placeHooks gives each hook line to the card holding its tool, else to the last card that
// started before it, else to the first card.
function placeHooks(cards, hooks) {
  if (!cards.length) return;
  const byTool = new Map();
  for (const c of cards) for (const t of c.tools) if (t.id) byTool.set(t.id, c);
  for (const h of hooks) {
    let card = h.tool_call_id ? byTool.get(h.tool_call_id) : null;
    if (!card) {
      card = cards[0];
      for (const c of cards) if (c.start && c.start <= (h.start || "") && (!card.start || c.start >= card.start)) card = c;
    }
    card.hooks.push(h);
  }
}

function isErrored(modelCall, group) {
  return group.some((e) => e.kind === "error") || (modelCall.status || 0) >= 400;
}

function toolRow(e, results, callN) {
  const r = results.get(e.tool_call_id);
  return {
    id: e.tool_call_id, name: e.name, chip: chipOf(e), agent: agentLabel(e),
    resultError: Boolean(r && r.is_error), callN, call: e, result: r || null,
  };
}

function isRealPrompt(e) {
  return e.kind === "user_prompt" && (Boolean((e.text || "").trim()) || Boolean(e.truncated));
}

// buildTurns groups entries into cards: one numbered turn per main-thread prompt, an
// unnumbered "before first prompt" card, and unnumbered "side calls" cards. Side calls
// never start a turn; a call of another thread joins the open turn. A session with hook
// events and no calls gets turns from its prompt hooks (AC18). Returns { cards, turns }.
export function buildTurns(entries) {
  const hasCalls = entries.some((e) => e.kind === "model_call");
  if (!hasCalls && entries.some((e) => e.kind === "hook_event")) return hookTurns(entries);

  const numbers = callNumbers(entries);
  const groups = [];
  const byEx = new Map();
  for (const e of entries) {
    if (e.kind === "hook_event") continue;
    let g = byEx.get(e.exchange_id);
    if (!g) {
      g = [];
      byEx.set(e.exchange_id, g);
      groups.push(g);
    }
    g.push(e);
  }
  const results = new Map();
  for (const e of entries) if (e.kind === "tool_result" && e.tool_call_id) results.set(e.tool_call_id, e);

  const cards = [];
  let number = 0;
  let open = null;
  let before = null;
  let side = null;
  const start = (prompt) => {
    open = newCard("turn", ++number, prompt);
    side = null;
    cards.push(open);
    return open;
  };
  const beforeCard = () => {
    if (!before) {
      before = newCard("before", null, null);
      cards.unshift(before);
    }
    return before;
  };

  for (const g of groups) {
    const mc = g.find((e) => e.kind === "model_call");
    if (!mc) continue;
    const facts = mc.call || {};
    const n = numbers.get(mc.exchange_id);
    const prompt = facts.main && !facts.side_call ? g.find(isRealPrompt) : null;
    let card;
    if (facts.side_call) {
      if (!open) card = beforeCard();
      else {
        if (!side) {
          side = newCard("side", null, null);
          cards.push(side);
        }
        card = side;
      }
    } else if (prompt) {
      card = start(prompt);
    } else {
      card = open || beforeCard();
    }
    const errored = isErrored(mc, g);
    const tools = g.filter((e) => e.kind === "tool_call").map((e) => toolRow(e, results, n));
    const label = g.map(agentLabel).find(Boolean) || "";
    if (!card.start || (mc.start || "") < card.start) card.start = mc.start || "";
    card.calls.push({
      n, exchangeId: mc.exchange_id, durationMs: mc.duration_ms ?? null,
      label: `#${n} · ${formatDuration(mc.duration_ms ?? null)}`,
      model: mc.model || "", errored, errorWord: errored ? "error" : "", side: Boolean(facts.side_call),
      agent: label, link: `#${n}`, context: contextOf(mc),
    });
    card.tools.push(...tools);
    for (const e of g) if (e.kind === "assistant_text" && e.text) card.texts.push(e.text);
    card.counts.calls++;
    card.counts.tools += tools.length;
    card.durationMs += mc.duration_ms || 0;
    if (errored) card.errored = true;
  }
  for (const c of cards) c.heading = headingOf(c);
  placeHooks(cards, entries.filter((e) => e.kind === "hook_event"));
  return { cards, turns: cards.filter((c) => c.kind === "turn") };
}

// hookTurns builds cards from hook events alone: no calls, so no links and no drawer.
function hookTurns(entries) {
  const cards = [];
  let number = 0;
  let open = null;
  let before = null;
  const rows = new Map();
  for (const e of entries) {
    if (e.kind !== "hook_event") continue;
    if (e.phase === "prompt") {
      open = newCard("turn", ++number, { text: e.text || "", id: e.id });
      open.hooksOnly = true;
      open.start = e.start || ""; // effects are placed in turns by this time (spec 014)
      cards.push(open);
      continue;
    }
    let card = open;
    if (!card) {
      if (!before) {
        before = newCard("before", null, null);
        before.hooksOnly = true;
        cards.unshift(before);
      }
      card = before;
    }
    card.hooks.push(e);
    if (!e.tool_call_id) continue;
    let row = rows.get(e.tool_call_id);
    if (!row) {
      row = { id: e.tool_call_id, name: e.name, agent: "", callN: null };
      rows.set(e.tool_call_id, row);
      card.tools.push(row);
      card.counts.tools++;
    }
    row.agent = row.agent || agentLabel(e);
    row.chip = chipOf(e);
  }
  for (const c of cards) c.heading = headingOf(c);
  return { cards, turns: cards.filter((c) => c.kind === "turn"), note: "no model calls were captured" };
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

// borrowedContext is a call's context, or the nearest earlier call's on its prev_id
// chain when it has none; the stats row uses it for the last main call.
function borrowedContext(call, byId) {
  let fallback = null;
  for (let c = call, seen = 0; c && seen < 10000; c = byId.get((c.call || {}).prev_id), seen++) {
    const ctx = contextOf(c);
    if (ctx.value === null) continue;
    const u = c.usage;
    if (!u.partial) return { value: ctx.value, partial: c !== call, from: c.exchange_id };
    fallback = fallback || { value: ctx.value, partial: true, from: c.exchange_id };
  }
  return fallback;
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

// railItems is one rail entry per numbered turn: prompt first line, tool chip counts, call
// count and duration. Every turn is listed, whatever the Show options hide (AC9).
export function railItems(cards) {
  return cards.filter((c) => c.kind === "turn").map((c) => {
    const p = c.prompt ? (c.prompt.body ?? c.prompt.text ?? "") : "";
    const line = p.split("\n").find((l) => l.trim()) || "";
    const counts = new Map();
    for (const t of c.tools) if (t.chip) counts.set(t.chip.key, (counts.get(t.chip.key) || 0) + 1);
    const chips = CHIP_KEYS.filter((k) => counts.has(k)).map((k) => ({ ...chipOf({ chip: k }), count: counts.get(k) }));
    return {
      number: c.number, prompt: line.length > 80 ? `${line.slice(0, 80)}…` : line || "(no prompt text)",
      chips, calls: c.counts.calls, durationMs: c.durationMs, duration: formatDuration(c.durationMs), errored: c.errored,
    };
  });
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

export const MIN_BAR_WIDTH = 6;
export const MAX_BAR_WIDTH = 48;
// An error bar is wide enough to show its ✗ glyph (AC34: never colour alone).
export const ERROR_BAR_WIDTH = 14;

// stripBars is one bar per call in start order: width by duration (never under
// MIN_BAR_WIDTH, so the strip scrolls rather than hides calls), marked by turn, side
// calls muted and named on hover (AC11, E13).
export function stripBars(entries) {
  const { cards } = buildTurns(entries);
  const turnOf = new Map();
  for (const card of cards) {
    card.calls.forEach((c, i) => turnOf.set(c.exchangeId, { number: card.number, first: i === 0 && card.kind === "turn" }));
  }
  const calls = entries.filter((e) => e.kind === "model_call");
  const numbers = callNumbers(entries);
  const tools = new Map();
  for (const e of entries) if (e.kind === "tool_call") tools.set(e.exchange_id, [...(tools.get(e.exchange_id) || []), e.name]);
  const maxMs = Math.max(1, ...calls.map((e) => e.duration_ms || 0));
  return calls.map((e) => {
    const f = e.call || {};
    const n = numbers.get(e.exchange_id);
    const t = turnOf.get(e.exchange_id) || { number: null, first: false };
    const side = Boolean(f.side_call);
    const names = tools.get(e.exchange_id) || [];
    const status = e.status || 0;
    const errored = status >= 400;
    const title = [`#${n}`, t.number ? `turn ${t.number}` : "", side ? "side call" : "", errored ? "error" : "", status ? `status ${status}` : "",
      formatDuration(e.duration_ms ?? null), names.length ? `tool ${names.join(", ")}` : ""].filter(Boolean).join(" · ");
    return {
      n, exchangeId: e.exchange_id, turn: t.number, turnStart: t.first, side, muted: side,
      errored, status, durationMs: e.duration_ms ?? null,
      width: Math.max(MIN_BAR_WIDTH, errored ? ERROR_BAR_WIDTH : 0, Math.round(((e.duration_ms || 0) / maxMs) * MAX_BAR_WIDTH)),
      tools: names, title,
    };
  });
}

// buildStats is the session stats row. Calls and tokens count every call, side calls
// included; errors come from the summary's main_errors, never errors (AC10, AC38, AC39).
export function buildStats(entries, summary) {
  const calls = entries.filter((e) => e.kind === "model_call");
  const byId = new Map(calls.map((e) => [e.exchange_id, e]));
  const mains = calls.filter((e) => e.call && e.call.main);
  const last = mains[mains.length - 1];
  const s = summary || {};
  const usage = s.usage || {};
  return {
    calls: s.model_calls ?? calls.length,
    sideCalls: calls.filter((e) => e.call && e.call.side_call).length,
    modelTimeMs: calls.reduce((t, e) => t + (e.duration_ms || 0), 0),
    toolStates: s.tool_states || {},
    errors: s.main_errors ?? 0,
    context: last ? borrowedContext(last, byId) : null,
    input: usage.input ? usage.input.value : null,
    output: usage.output ? usage.output.value : null,
  };
}
