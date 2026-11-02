// Pure model over timeline entries: chips, turns, call links and the stats row. The
// browser never classifies by message text; every fact comes from the API fields.

const CHIPS = {
  ok: { key: "ok", glyph: "✓", text: "ok" },
  failed: { key: "failed", glyph: "✗", text: "failed" },
  "not-run": { key: "not-run", glyph: "⊘", text: "not run" },
  unmatched: { key: "unmatched", glyph: "?", text: "unmatched" },
  pending: { key: "pending", glyph: "…", text: "pending" },
  "no-hooks": { key: "no-hooks", glyph: "–", text: "no hooks" },
};

// chipOf maps a tool call's { state, reason, outcome } to its chip: key, glyph, text and
// the full state, reason and outcome as the hover title. A ran-not-reported call stays
// pending even in an ended session (E14).
export function chipOf(call) {
  const c = call || {};
  let key;
  switch (c.state) {
    case "asked+ran+reported":
      key = c.outcome === "failed" ? "failed" : "ok";
      break;
    case "asked-not-run":
      key = "not-run";
      break;
    case "ran-not-asked-or-not-captured":
      key = "unmatched";
      break;
    case "pending":
    case "ran-not-reported":
      key = "pending";
      break;
    default:
      key = "no-hooks";
  }
  const title = [c.state || "no harness data", c.reason, c.outcome].filter(Boolean).join(": ");
  return { ...CHIPS[key], title };
}

// formatDuration renders milliseconds as 850ms, 1.2s or 2m05s; "—" when unknown.
export function formatDuration(ms) {
  if (ms === null || ms === undefined) return "—";
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  const s = Math.round(ms / 1000);
  return `${Math.floor(s / 60)}m${String(s % 60).padStart(2, "0")}s`;
}

function agentLabel(e) {
  if (!e.agent_id && !e.agent_type) return "";
  return `agent ${[e.agent_type, e.agent_id].filter(Boolean).join(" ")}`;
}

function newCard(kind, number, prompt) {
  return { kind, number, prompt, texts: [], tools: [], calls: [], counts: { calls: 0, tools: 0 }, durationMs: 0, errored: false, hooksOnly: false };
}

function isErrored(modelCall, group) {
  return group.some((e) => e.kind === "error") || (modelCall.status || 0) >= 400;
}

function toolRow(e, results, callN) {
  const r = results.get(e.tool_call_id);
  return {
    id: e.tool_call_id, name: e.name, chip: chipOf(e), agent: agentLabel(e),
    resultError: Boolean(r && r.is_error), callN,
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
    const tools = g.filter((e) => e.kind === "tool_call").map((e) => toolRow(e, results, facts.n));
    const label = g.map(agentLabel).find(Boolean) || "";
    card.calls.push({
      n: facts.n, exchangeId: mc.exchange_id, durationMs: mc.duration_ms ?? null,
      label: `#${facts.n} · ${formatDuration(mc.duration_ms ?? null)}`,
      model: mc.model || "", errored, errorWord: errored ? "error" : "", side: Boolean(facts.side_call),
      agent: label, link: `#${facts.n}`,
    });
    card.tools.push(...tools);
    for (const e of g) if (e.kind === "assistant_text" && e.text) card.texts.push(e.text);
    card.counts.calls++;
    card.counts.tools += tools.length;
    card.durationMs += mc.duration_ms || 0;
    if (errored) card.errored = true;
  }
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
      cards.push(open);
      continue;
    }
    if (!e.tool_call_id) continue;
    let card = open;
    if (!card) {
      if (!before) {
        before = newCard("before", null, null);
        before.hooksOnly = true;
        cards.unshift(before);
      }
      card = before;
    }
    let row = rows.get(e.tool_call_id);
    if (!row) {
      row = { id: e.tool_call_id, name: e.name, phases: new Set(), agent: "", callN: null };
      rows.set(e.tool_call_id, row);
      card.tools.push(row);
      card.counts.tools++;
    }
    row.phases.add(e.phase);
    row.agent = row.agent || agentLabel(e);
    row.chip = hookChip(row.phases);
  }
  return { cards, turns: cards.filter((c) => c.kind === "turn"), note: "no model calls were captured" };
}

function hookChip(phases) {
  let call;
  if (phases.has("post_tool_failed")) call = { state: "asked+ran+reported", outcome: "failed" };
  else if (phases.has("post_tool")) call = { state: "asked+ran+reported", outcome: "succeeded" };
  else if (phases.has("permission_denied")) call = { state: "asked-not-run", reason: "denied" };
  else call = { state: "pending" };
  return chipOf(call);
}

// contextOf is a call's context: input + cache write + cache read. A call without
// complete usage borrows the nearest earlier call on its prev_id chain that has it.
export function contextOf(call, byId) {
  let fallback = null;
  for (let c = call, seen = 0; c && seen < 10000; c = byId.get((c.call || {}).prev_id), seen++) {
    const u = c.usage;
    if (!u || !u.has_usage || u.input === null || u.input === undefined) continue;
    const value = u.input + (u.cache_write || 0) + (u.cache_read || 0);
    if (!u.partial) return { value, partial: c !== call, from: c.exchange_id };
    fallback = fallback || { value, partial: true, from: c.exchange_id };
  }
  return fallback;
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
    context: last ? contextOf(last, byId) : null,
    input: usage.input ? usage.input.value : null,
    output: usage.output ? usage.output.value : null,
  };
}
