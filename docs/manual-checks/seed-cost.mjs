// Seeds one session for the 020 cost check (§3): a call priced with the 5m/1h split, one
// whose usage has no split (estimated), one on a model the price table has no row for
// (left out, total partial), and one that fails with a 429 (no usage, costs nothing). It
// starts a local stub upstream and sends the calls through a running throwaway gateway, so
// they take the real capture path. Node only, no packages.
//
//   node docs/manual-checks/seed-cost.mjs [--gateway URL] [--ui URL] [--stub-port N]
//
// Start the gateway with GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL=http://127.0.0.1:<stub-port>.
import { createServer } from "node:http";
import { randomUUID } from "node:crypto";

const args = { gateway: "http://127.0.0.1:17297", ui: "http://127.0.0.1:17299", "stub-port": "17296" };
for (let i = 2; i < process.argv.length; i++) args[process.argv[i].replace(/^--/, "")] = process.argv[++i];

const STUB_MARK = "x-seed-stub";
const PRICED = "claude-opus-5-5";
const NO_PRICE = "claude-seed-no-price";
const session = randomUUID();
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// The usage each kind of call reports. Opus 5.5 at $4 / $20 / $0.20 read / $5 5m / $8 1h:
//   split:    1,000 in, 2,000 out, 10,000 1h writes, 100,000 reads
//             = 0.004 + 0.04 + 0.08 + 0.02 = $0.144
//   no split: 1,000 in, 1,000 out, 10,000 writes at 5m, 0 reads
//             = 0.004 + 0.02 + 0.05 = $0.074 (estimated)
const USAGE = {
  split: { input_tokens: 1000, output_tokens: 2000, cache_creation_input_tokens: 10000, cache_read_input_tokens: 100000,
    cache_creation: { ephemeral_5m_input_tokens: 0, ephemeral_1h_input_tokens: 10000 }, service_tier: "standard", speed: "standard", inference_geo: "global" },
  nosplit: { input_tokens: 1000, output_tokens: 1000, cache_creation_input_tokens: 10000, cache_read_input_tokens: 0 },
  noprice: { input_tokens: 5000, output_tokens: 500, cache_creation_input_tokens: 0, cache_read_input_tokens: 0,
    cache_creation: { ephemeral_5m_input_tokens: 0, ephemeral_1h_input_tokens: 0 } },
};

const stub = createServer((req, res) => {
  const chunks = [];
  req.on("data", (c) => chunks.push(c));
  req.on("end", () => {
    const body = JSON.parse(Buffer.concat(chunks).toString() || "{}");
    const kind = body.metadata?.user_id;
    res.setHeader("content-type", "application/json");
    res.setHeader(STUB_MARK, "1");
    if (kind === "error") {
      res.setHeader("retry-after", "1");
      res.writeHead(429);
      res.end(JSON.stringify({ type: "error", error: { type: "rate_limit_error", message: "seeded 429" } }));
      return;
    }
    res.writeHead(200);
    res.end(JSON.stringify({ id: `msg_${randomUUID()}`, type: "message", role: "assistant", model: body.model,
      content: [{ type: "text", text: `A ${kind} reply.` }], stop_reason: "end_turn", stop_sequence: null, usage: USAGE[kind] }));
  });
});

const messages = [];
async function call(kind, model, prompt) {
  messages.push({ role: "user", content: [{ type: "text", text: prompt }] });
  const res = await fetch(`${args.gateway}/anthropic/v1/messages`, {
    method: "POST",
    headers: { "content-type": "application/json", "user-agent": "claude-cli/2.0.0 (seed)", "x-claude-code-session-id": session,
      "x-api-key": "seed-not-a-real-key", "anthropic-version": "2023-06-01" },
    body: JSON.stringify({ model, max_tokens: 1024, metadata: { user_id: kind }, messages }),
  });
  const text = await res.text();
  if (res.headers.get(STUB_MARK) !== "1") {
    console.error(`stopped: a call did not reach the stub (status ${res.status}). Start the gateway with GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL=http://127.0.0.1:${args["stub-port"]}`);
    process.exit(1);
  }
  if (res.status === 200) messages.push({ role: "assistant", content: JSON.parse(text).content });
  return res.status;
}

await new Promise((ok) => stub.listen(Number(args["stub-port"]), "127.0.0.1", ok));
try {
  await call("split", PRICED, "Price me with the split.");
  await call("nosplit", PRICED, "Price me without a split.");
  await call("noprice", NO_PRICE, "A model with no price.");
  await call("error", PRICED, "This one fails.");
  for (let i = 0; i < 40; i++) {
    const r = await fetch(`${args.ui}/api/sessions`).then((x) => x.json()).catch(() => ({ items: [] }));
    const s = (r.items || []).find((x) => x.session_key === session);
    if (s && s.model_calls >= 4) {
      console.log(`seeded 4 calls (split, no split, no price, 429), session key ${session}`);
      console.log(`cost: ${JSON.stringify(s.usage.cost)}`);
      console.log(`open: ${args.ui}/#/sessions/${encodeURIComponent(s.id)}`);
      break;
    }
    await sleep(250);
  }
} finally {
  stub.close();
}
