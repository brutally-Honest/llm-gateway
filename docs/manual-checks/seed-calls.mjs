// Seeds one session with N captured calls for the 013 render check. It starts a local stub
// upstream and sends the calls through a running gateway, so they take the real capture path.
// See 013-web-ui.md for how to run it. Node only, no packages.
//
//   node docs/manual-checks/seed-calls.mjs [--calls 2000] [--live] [--gateway URL] [--ui URL] [--stub-port N]
//
// Capture never blocks a request: a burst that fills its queue is dropped. So the seed waits
// for capture to keep up, and exits 1 unless the session ends with exactly N calls.
//
// --live keeps sending one call every 2 s after the seed, until Ctrl-C.
import { randomUUID } from "node:crypto";
import { createServer } from "node:http";
import { parseArgs } from "node:util";

const { values: args } = parseArgs({
  options: {
    calls: { type: "string", default: "2000" },
    live: { type: "boolean", default: false },
    gateway: { type: "string", default: "http://127.0.0.1:7197" },
    ui: { type: "string", default: "http://127.0.0.1:7199" },
    "stub-port": { type: "string", default: "7196" },
  },
});
const total = Number(args.calls);
const CALLS_PER_TURN = 10; // a new prompt every 10 calls
const CALLS_PER_THREAD = 100; // a new thread keeps request bodies small, as a compaction does
const ERROR_EVERY = 50; // gives the error marks and error keys something to find
const STUB_MARK = "x-seed-stub";
const MAX_AHEAD = 100; // calls sent but not yet captured; well under the default queue of 256
const CAPTURE_WAIT_MS = 30000;

const stub = createServer((req, res) => {
  const chunks = [];
  req.on("data", (c) => chunks.push(c));
  req.on("end", () => {
    const { metadata } = JSON.parse(Buffer.concat(chunks).toString() || "{}");
    const n = metadata?.seed_call ?? 0;
    res.setHeader("content-type", "application/json");
    res.setHeader(STUB_MARK, "1");
    if (n > 0 && n % ERROR_EVERY === 0) {
      res.statusCode = 529;
      res.end(JSON.stringify({ type: "error", error: { type: "overloaded_error", message: "seeded error" } }));
      return;
    }
    res.end(JSON.stringify(reply(n)));
  });
});

// reply asks for a tool on every call but the last of a turn.
function reply(n) {
  const text = { type: "text", text: `Reply to call ${n}.` };
  const asksTool = n % CALLS_PER_TURN !== 0;
  return {
    id: `msg_seed_${n}`, type: "message", role: "assistant", model: "claude-seed", stop_sequence: null,
    content: asksTool ? [text, { type: "tool_use", id: `toolu_seed_${n}`, name: "Bash", input: { command: `echo ${n}` } }] : [text],
    stop_reason: asksTool ? "tool_use" : "end_turn",
    usage: { input_tokens: 100 + (n % CALLS_PER_THREAD) * 40, output_tokens: 20 + (n % 30), cache_creation_input_tokens: 0, cache_read_input_tokens: n % CALLS_PER_THREAD ? 500 : 0 },
  };
}

const sessionKey = randomUUID();
let messages = [];

// send makes call n and appends what a client would carry into the next one.
async function send(n) {
  const res = await fetch(`${args.gateway}/anthropic/v1/messages`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "user-agent": "claude-cli/seed",
      "x-claude-code-session-id": sessionKey,
      "x-api-key": "seed-not-a-real-key",
      "anthropic-version": "2023-06-01",
    },
    body: JSON.stringify({ model: "claude-seed", max_tokens: 64, metadata: { seed_call: n }, messages }),
  });
  const body = await res.json();
  return { marked: res.headers.get(STUB_MARK) === "1", status: res.status, body };
}

async function step(n) {
  if ((n - 1) % CALLS_PER_THREAD === 0) messages = [];
  if ((n - 1) % CALLS_PER_TURN === 0) messages.push({ role: "user", content: [{ type: "text", text: `Seeded prompt before call ${n} in session ${sessionKey}.` }] });
  const { marked, status, body } = await send(n);
  // Stops at the first call that did not reach this stub: the gateway is using a real upstream.
  if (!marked) {
    console.error(`stopped: call ${n} did not reach the stub (status ${status}). Start the gateway with GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL=http://127.0.0.1:${args["stub-port"]}`);
    process.exit(1);
  }
  if (status !== 200) return;
  messages.push({ role: "assistant", content: body.content });
  const tool = body.content.find((b) => b.type === "tool_use");
  if (tool) messages.push({ role: "user", content: [{ type: "tool_result", tool_use_id: tool.id, content: "ok" }] });
}

// capturedCalls is how many calls of this session the UI API reports. The first prompt holds
// the session key, so the session's title finds it.
async function capturedCalls() {
  const res = await fetch(`${args.ui}/api/sessions`);
  if (!res.ok) throw new Error(`${args.ui}/api/sessions answered ${res.status}`);
  const { items } = await res.json();
  return items.find((s) => s.title?.includes(sessionKey))?.model_calls ?? 0;
}

// waitForCapture waits until at least `want` calls are captured and returns the count it saw.
async function waitForCapture(want) {
  const deadline = Date.now() + CAPTURE_WAIT_MS;
  for (;;) {
    const got = await capturedCalls();
    if (got >= want || Date.now() > deadline) return got;
    await new Promise((r) => setTimeout(r, 100));
  }
}

function failShort(got, want) {
  console.error(`stopped: ${got} of ${want} calls captured after ${CAPTURE_WAIT_MS / 1000} s. Check dropped_queue_full in the gateway log, and use a fresh data directory.`);
  process.exit(1);
}

stub.on("error", (e) => {
  console.error(`stub upstream: ${e.message}`);
  process.exit(1);
});
stub.listen(Number(args["stub-port"]), "127.0.0.1", async () => {
  try {
    for (let n = 1; n <= total; n++) {
      await step(n);
      if (n % MAX_AHEAD === 0) {
        const got = await waitForCapture(n - MAX_AHEAD);
        if (got < n - MAX_AHEAD) failShort(got, n - MAX_AHEAD);
      }
      if (n % 500 === 0) console.log(`${n}/${total} calls`);
    }
    const got = await waitForCapture(total);
    if (got !== total) failShort(got, total);
    console.log(`seeded ${total} calls, session key ${sessionKey}`);
    if (!args.live) {
      stub.close();
      return;
    }
    console.log("sending one call every 2 s, Ctrl-C to stop");
    for (let n = total + 1; ; n++) {
      await step(n);
      await new Promise((r) => setTimeout(r, 2000));
    }
  } catch (e) {
    console.error(`stopped: ${e.message}. Is the gateway running at ${args.gateway}?`);
    process.exit(1);
  }
});
