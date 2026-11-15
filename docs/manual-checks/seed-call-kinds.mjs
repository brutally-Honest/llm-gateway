// Seeds one session with the call kinds the 017 call details check opens (§2): a plain call,
// a streamed call (server-sent events), a call with a very long request and reply, and an
// errored call. It starts a local stub upstream and sends the calls through a running
// gateway, so they take the real capture path. Node only, no packages.
//
//   node docs/manual-checks/seed-call-kinds.mjs [--gateway URL] [--ui URL] [--stub-port N] [--live] [--ungrouped]
//
// Start the gateway with GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL=http://127.0.0.1:<stub-port>.
// --live then sends one more plain call every 5 s until Ctrl-C, for the live-update step.
// --ungrouped also sends two calls with no session and no known client, one plain and one
// errored, for the ungrouped exchanges page (§3).
import { createServer } from "node:http";
import { randomUUID } from "node:crypto";

const args = { gateway: "http://127.0.0.1:8080", ui: "http://127.0.0.1:8081", "stub-port": "18086", live: false };
for (let i = 2; i < process.argv.length; i++) {
  const a = process.argv[i].replace(/^--/, "");
  if (a === "live") args.live = true;
  else if (a === "ungrouped") args.ungrouped = true;
  else args[a] = process.argv[++i];
}

const STUB_MARK = "x-seed-stub";
const MODEL = "claude-seed-kinds";
const session = randomUUID();
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// The long call: a request message and a reply far over the timeline's cut, so the dialog
// shows them cut short with a link to the raw bodies.
const LONG_LINE = "The quick brown fox jumps over the lazy dog while the build runs again. ";
const longText = (n) => Array.from({ length: n }, (_, i) => `${i + 1}. ${LONG_LINE}`).join("\n");

const usage = (input, out) => ({ input_tokens: input, output_tokens: out, cache_creation_input_tokens: 0, cache_read_input_tokens: 0 });

// reply is the stub's answer for a call's kind.
function reply(kind) {
  switch (kind) {
    case "long":
      return { status: 200, body: message([{ type: "text", text: longText(3000) }], "end_turn", usage(60000, 9000)) };
    case "error":
      return { status: 500, body: { type: "error", error: { type: "api_error", message: "Internal server error" } } };
    default:
      return { status: 200, body: message([{ type: "text", text: `A plain ${kind} reply.` }], "end_turn", usage(120, 12)) };
  }
}

function message(content, stopReason, u) {
  return { id: `msg_${randomUUID()}`, type: "message", role: "assistant", model: MODEL, content, stop_reason: stopReason, stop_sequence: null, usage: u };
}

// sse writes a streamed reply as the provider sends it: start, one text block in deltas,
// the stop reason and usage, stop.
async function sse(res) {
  res.setHeader("content-type", "text/event-stream");
  res.setHeader(STUB_MARK, "1");
  const send = (event, data) => res.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
  send("message_start", { type: "message_start", message: { ...message([], null, usage(150, 0)), stop_reason: null } });
  send("content_block_start", { type: "content_block_start", index: 0, content_block: { type: "text", text: "" } });
  for (const part of ["This reply ", "was streamed ", "in four ", "parts."]) {
    await sleep(150);
    send("content_block_delta", { type: "content_block_delta", index: 0, delta: { type: "text_delta", text: part } });
  }
  send("content_block_stop", { type: "content_block_stop", index: 0 });
  send("message_delta", { type: "message_delta", delta: { stop_reason: "end_turn", stop_sequence: null }, usage: { output_tokens: 9 } });
  send("message_stop", { type: "message_stop" });
  res.end();
}

const stub = createServer((req, res) => {
  const chunks = [];
  req.on("data", (c) => chunks.push(c));
  req.on("end", async () => {
    let body = {};
    try {
      body = JSON.parse(Buffer.concat(chunks).toString() || "{}");
    } catch {
      // answered below as a plain call
    }
    const kind = body.metadata?.user_id ?? "plain";
    if (body.stream) return sse(res);
    const r = reply(kind);
    await sleep(200);
    res.setHeader("content-type", "application/json");
    res.setHeader(STUB_MARK, "1");
    res.statusCode = r.status;
    res.end(JSON.stringify(r.body));
  });
});

const messages = [];

// call sends one call of the session; kind picks the stub's reply, stream asks for SSE.
async function call(kind, prompt, { stream = false } = {}) {
  messages.push({ role: "user", content: [{ type: "text", text: prompt }] });
  const res = await fetch(`${args.gateway}/anthropic/v1/messages`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "user-agent": "claude-cli/2.0.0 (seed)",
      "x-claude-code-session-id": session,
      "x-api-key": "seed-not-a-real-key",
      "anthropic-version": "2023-06-01",
    },
    body: JSON.stringify({ model: MODEL, max_tokens: 1024, stream, metadata: { user_id: kind }, messages }),
  });
  const text = await res.text();
  if (res.headers.get(STUB_MARK) !== "1") {
    console.error(`stopped: a call did not reach the stub (status ${res.status}). Start the gateway with GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL=http://127.0.0.1:${args["stub-port"]}`);
    process.exit(1);
  }
  if (res.status === 200) {
    const replyText = stream ? "This reply was streamed in four parts." : JSON.parse(text).content.map((b) => b.text).join("");
    messages.push({ role: "assistant", content: [{ type: "text", text: replyText }] });
  }
  return res.status;
}

// ungroupedCall sends one call no session claims: no session header, a client the gateway
// does not know. The gateway still captures it, as an ungrouped exchange.
async function ungroupedCall(kind, prompt) {
  const res = await fetch(`${args.gateway}/anthropic/v1/messages`, {
    method: "POST",
    headers: { "content-type": "application/json", "user-agent": "seed-script/1.0", "x-api-key": "seed-not-a-real-key", "anthropic-version": "2023-06-01" },
    body: JSON.stringify({ model: MODEL, max_tokens: 64, metadata: { user_id: kind }, messages: [{ role: "user", content: [{ type: "text", text: prompt }] }] }),
  });
  await res.text();
  return res.status;
}

await new Promise((ok) => stub.listen(Number(args["stub-port"]), "127.0.0.1", ok));
try {
  await call("plain", "Say hello: a plain call.");
  await call("stream", "Stream your answer: a streamed call.", { stream: true });
  await call("long", `Read all of this and answer at length: a very long call.\n${longText(2000)}`);
  await call("error", "This one fails: an errored call.");
  // wait for capture to keep up before printing the link
  for (let i = 0; i < 40; i++) {
    const r = await fetch(`${args.ui}/api/sessions`).then((x) => x.json()).catch(() => ({ items: [] }));
    const s = (r.items || []).find((x) => x.session_key === session);
    if (s && s.model_calls >= 4) {
      console.log(`seeded 4 calls (plain, streamed, long, errored), session key ${session}`);
      console.log(`open: ${args.ui}/#/sessions/${encodeURIComponent(s.id)}`);
      break;
    }
    await sleep(250);
  }
  if (args.ungrouped) {
    const a = await ungroupedCall("plain", "An ungrouped call.");
    const b = await ungroupedCall("error", "An ungrouped call that fails.");
    console.log(`seeded 2 ungrouped exchanges (status ${a}, ${b}): ${args.ui}/#/ungrouped`);
  }
  if (args.live) {
    console.log("--live: one plain call every 5 s; Ctrl-C to stop");
    for (let n = 1; ; n++) {
      await sleep(5000);
      await call("plain", `Live call ${n}.`);
    }
  }
} finally {
  if (!args.live) stub.close();
}
