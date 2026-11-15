// API-equivalent cost, formatting only (spec 020): the server prices every call and sends
// exact decimal USD strings; this rounds them for display and words the labels. It is the
// one module that shows money (it replaces 013 AC35's "no money", research D12), and every
// amount it writes is labelled API-equivalent.

export const LABEL = "API-eq.";
export const LABEL_LONG = "API-equivalent";

// explain is the hover that says what the amount is: what the tokens would cost at the
// provider's published API prices, not what was charged.
export function explain(source) {
  const who = source ? `${source}'s` : "the provider's";
  return `${LABEL_LONG}: what these tokens would cost at ${who} published API prices — not what was charged.`;
}

// roundDecimal rounds an exact decimal string ("0.0452108") to places, half up, as a
// [whole, fraction] pair of digit strings. Done on the digits, so no float rounding.
function roundDecimal(s, places) {
  const [w, f = ""] = String(s).split(".");
  const digits = BigInt((w || "0") + f.padEnd(places + 1, "0").slice(0, places + 1));
  const scaled = (digits + 5n) / 10n; // half up on the first dropped digit
  const str = scaled.toString().padStart(places + 1, "0");
  return [str.slice(0, str.length - places) || "0", str.slice(str.length - places)];
}

// usd writes an exact decimal string as dollars: to cents by default ("$1.23"), "< $0.01"
// for an amount above zero that rounds below a cent, "—" when there is none.
export function usd(s, places = 2) {
  if (s === null || s === undefined || s === "") return "—";
  const [w, f] = roundDecimal(s, places);
  if (/^0*$/.test(w + f) && /[1-9]/.test(String(s))) return `< $0.${"1".padStart(places, "0")}`;
  return `$${BigInt(w).toLocaleString("en-US")}${places ? `.${f}` : ""}`;
}

const n = (v) => Number(v || 0);
const calls = (k) => (k === 1 ? "1 call" : `${k} calls`);

// totalCost is a total's cell: { text, title }. A partial total is "≥ $1.23" and its hover
// says how many calls were left out and why; a total with no priced call but calls left
// out is "—".
export function totalCost(t) {
  if (!t) return { text: "—", title: "" };
  const left = n(t.no_price) + n(t.no_usage);
  const lines = [explain(t.source)];
  if (n(t.no_price)) lines.push(`${calls(n(t.no_price))} left out: no price for ${(t.no_price_models || []).join(", ")}.`);
  if (n(t.no_usage)) lines.push(`${calls(n(t.no_usage))} left out: no usage counts (cut before the reply reported them).`);
  if (n(t.at_least)) lines.push(`${calls(n(t.at_least))} may have used more: a reply cut short or a count not reported.`);
  if (n(t.estimated)) lines.push(`${calls(n(t.estimated))} estimated: no 5m/1h cache-write split reported, so every cache write is at the 5-minute rate.`);
  if (n(t.errored)) lines.push(`${calls(n(t.errored))} failed with no usage and cost nothing.`);
  if (!n(t.priced) && left) return { text: "—", title: lines.join("\n") };
  return { text: `${t.partial ? "≥ " : ""}${usd(t.usd)}`, title: lines.join("\n") };
}

// callCost is one call's cost: { text, title }. A call with no price reads "— no price for
// <model>"; one with no usage "—" with the reason in the hover.
export function callCost(c) {
  if (!c) return { text: "—", title: "" };
  switch (c.status) {
    case "priced": {
      const notes = [explain(c.source)];
      if (c.estimated) notes.push("Estimated: no 5m/1h cache-write split reported, so every cache write is at the 5-minute rate.");
      if (c.at_least) notes.push("At least: the reply was cut short or a count was not reported.");
      return { text: `${c.at_least ? "≥ " : ""}${usd(c.usd)}${c.estimated ? " (estimated)" : ""}`, title: notes.join("\n") };
    }
    case "no_price":
      return { text: `— ${c.reason}`, title: `${explain(c.source)}\nThe price table has no row for this model at the call's time, so it is left out of every total.` };
    case "errored":
      return { text: "—", title: `No usage: ${c.reason}. Nothing was billed.` };
    default:
      return { text: "—", title: `No usage: ${c.reason}. Left out of totals.` };
  }
}

const KINDS = { input: "Input", cache_write_5m: "Cache write 5m", cache_write_1h: "Cache write 1h", cache_read: "Cache read", output: "Output" };

// costLines is a priced call's breakdown, one row per token kind: tokens × $/MTok = amount,
// the amount to four places so the arithmetic can be read (research D11).
export function costLines(c) {
  if (!c || c.status !== "priced") return [];
  return (c.lines || []).map((l) => ({
    kind: KINDS[l.kind] || l.kind,
    tokens: Number(l.tokens).toLocaleString("en-US"),
    rate: `$${l.usd_per_mtok} / MTok`,
    amount: usd(l.usd, 4),
  }));
}
