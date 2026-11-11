// Seeds one session that holds every case of the 016 session page (specs/016-web-ui-v2,
// mockups/v4-story.html): model calls through the gateway to a local stub upstream (thinking,
// parallel tool calls, a failed Edit, a 500, a quota probe answered 429, a title call, a
// sub-agent thread), hook events posted as Claude Code sends them, real file changes in a
// scratch git repo, connections through the gateway's HTTP proxy (a WebFetch host, two
// parallel commands, a harness host during a long command, one unassigned connection), your
// own edit between two turns and a hooks-only last turn. It prints the session link and the
// line every tool, effect and block must show. See 016-session-page.md. Node only, no packages.
//
//   node docs/manual-checks/seed-session.mjs [--fast] [--live] [--dir PATH] [--gateway URL]
//        [--ui URL] [--http-proxy HOST:PORT] [--stub-port N]
//
// The gateway must forward to the stub: start it with
// GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL=http://127.0.0.1:<stub-port>. --fast shrinks the long
// command from 2 minutes to 5 s and every pause. --live then adds one short turn every 5 s
// until Ctrl-C. The harness step CONNECTs to downloads.claude.ai:443: without the internet
// it is refused, and is still labelled harness. The scratch repo must not be under a temp
// folder or be the home folder (the gateway never watches those); the default is
// ~/llmgw-seed-session/<time>.
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { createServer, request } from "node:http";
import { connect } from "node:net";
import { homedir } from "node:os";
import { join } from "node:path";
import { parseArgs } from "node:util";

const { values: args } = parseArgs({
  options: {
    dir: { type: "string", default: join(homedir(), "llmgw-seed-session", String(Date.now())) },
    fast: { type: "boolean", default: false },
    live: { type: "boolean", default: false },
    gateway: { type: "string", default: "http://127.0.0.1:7197" },
    ui: { type: "string", default: "http://127.0.0.1:7199" },
    "http-proxy": { type: "string", default: "127.0.0.1:7198" },
    "stub-port": { type: "string", default: "7196" },
  },
});
const dir = args.dir;
const fast = args.fast;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
// pause is the gap between steps, long enough for a checkpoint to land so each change falls
// in its own window; run is a tool's running time.
const pause = () => sleep(fast ? 700 : 1500);
const run = (ms) => sleep(fast ? Math.min(ms, 600) : ms);
const LONG_COMMAND_MS = fast ? 5000 : 120000;
const MODEL_MS = fast ? 250 : 1200; // how long the stub takes to answer a model call
const hostPort = (s) => {
  const i = s.lastIndexOf(":");
  return { host: s.slice(0, i), port: Number(s.slice(i + 1)) };
};
const httpProxy = hostPort(args["http-proxy"]);
const STUB_MARK = "x-seed-stub";
const MODEL = "claude-seed-016";

// ---- expectations: printed at the end, the lines the page and the CLI must show ----
const expected = [];
const expect = (where, line) => expected.push(`${where.padEnd(10)} ${line}`);

// ---- the stub upstream: answers each call with the reply the seed scripted for it ----
const replies = new Map(); // seed id -> { status, body, delay }
let replyN = 0;
const stub = createServer((req, res) => {
  const chunks = [];
  req.on("data", (c) => chunks.push(c));
  req.on("end", async () => {
    let id = "";
    try {
      id = JSON.parse(Buffer.concat(chunks).toString() || "{}").metadata?.user_id ?? "";
    } catch {
      // no id: answered below as an unknown call
    }
    const r = replies.get(id) || { status: 400, body: { type: "error", error: { type: "invalid_request_error", message: "no scripted reply" } } };
    await sleep(r.delay ?? MODEL_MS);
    res.setHeader("content-type", "application/json");
    res.setHeader(STUB_MARK, "1");
    res.statusCode = r.status;
    res.end(JSON.stringify(r.body));
  });
});

// ---- hook events, shaped as Claude Code sends them ----
const session = randomUUID();
const other = randomUUID();
let toolN = 0;
const base = (name, sid = session) => ({ hook_event_name: name, session_id: sid, cwd: dir, transcript_path: "" });

async function hook(body) {
  const res = await fetch(`${args.gateway}/_gateway/hooks/claude-code`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`hook ${body.hook_event_name}: HTTP ${res.status}`);
}

const newToolID = () => `toolu_seed016_${++toolN}`;
const agentOf = (agent) => (agent ? { agent_id: agent, agent_type: "general-purpose" } : {});
const pre = (t) => hook({ ...base("PreToolUse"), ...agentOf(t.agent), tool_use_id: t.id, tool_name: t.name, tool_input: t.input });
const post = (t, response = {}) => hook({ ...base("PostToolUse"), ...agentOf(t.agent), tool_use_id: t.id, tool_name: t.name, tool_input: t.input, tool_response: response });
const failed = (t, error) => hook({ ...base("PostToolUseFailure"), ...agentOf(t.agent), tool_use_id: t.id, tool_name: t.name, tool_input: t.input, error });
const promptHook = (text) => hook({ ...base("UserPromptSubmit"), prompt: text });
const stop = () => hook({ ...base("Stop"), stop_hook_active: false });

// ---- model calls through the gateway ----
// A thread is one conversation: the main thread, the sub-agent's, or a side call's.
const thread = () => ({ messages: [], ctx: 0 });
const main = thread();

// usage makes context grow call by call within a thread: the cache read is the previous
// call's context, as Claude Code's prompt caching gives (research D12).
function usage(t, n) {
  const ctx = 3000 + Math.round(JSON.stringify(t.messages).length / 3);
  const input = 180 + (n % 7) * 20;
  const cw = Math.max(0, ctx - t.ctx - input);
  const out = { input_tokens: input, output_tokens: 90 + (n % 11) * 15, cache_creation_input_tokens: cw, cache_read_input_tokens: t.ctx };
  t.ctx = input + cw + t.ctx;
  return out;
}

// call sends one model call on thread t; content is the reply's blocks. A status other than
// 200 answers with an error body. It appends the reply to the thread, as a client would.
async function call(t, content, { status = 200, stopReason, delay } = {}) {
  const id = `seed016-${++replyN}`;
  const tools = content.filter((b) => b.type === "tool_use");
  const body = status === 200
    ? { id: `msg_${id}`, type: "message", role: "assistant", model: MODEL, content, stop_sequence: null,
      stop_reason: stopReason || (tools.length ? "tool_use" : "end_turn"), usage: usage(t, replyN) }
    : { type: "error", error: { type: status === 429 ? "rate_limit_error" : "api_error", message: status === 429 ? "quota probe: rate limited" : "Internal server error" } };
  replies.set(id, { status, body, delay });
  const res = await fetch(`${args.gateway}/anthropic/v1/messages`, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "user-agent": "claude-cli/2.0.0 (seed)",
      "x-claude-code-session-id": session,
      "x-api-key": "seed-not-a-real-key",
      "anthropic-version": "2023-06-01",
    },
    body: JSON.stringify({ model: MODEL, max_tokens: 1024, metadata: { user_id: id }, messages: t.messages }),
  });
  await res.arrayBuffer();
  if (res.headers.get(STUB_MARK) !== "1") {
    console.error(`stopped: a call did not reach the stub (status ${res.status}). Start the gateway with GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL=http://127.0.0.1:${args["stub-port"]}`);
    process.exit(1);
  }
  if (status === 200) t.messages.push({ role: "assistant", content });
  return replyN;
}

const text = (s) => ({ type: "text", text: s });
const thinking = (s) => ({ type: "thinking", thinking: s, signature: "c2VlZA==" });
const use = (t) => ({ type: "tool_use", id: t.id, name: t.name, input: t.input });
const tool = (name, input, agent = "") => ({ id: newToolID(), name, input, agent });
const userText = (t, s) => t.messages.push({ role: "user", content: [text(s)] });
const results = (t, ...rs) => t.messages.push({ role: "user", content: rs.map(([tl, out, isError]) => ({ type: "tool_result", tool_use_id: tl.id, content: out, ...(isError ? { is_error: true } : {}) })) });

async function sideCall(userMsg, content, status = 200) {
  const t = thread();
  userText(t, userMsg);
  return call(t, content, { status });
}

// ---- the scratch repo ----
function git(...a) {
  execFileSync("git", ["-C", dir, ...a], {
    stdio: "ignore",
    env: { ...process.env, GIT_AUTHOR_NAME: "seed", GIT_AUTHOR_EMAIL: "seed@example.com", GIT_COMMITTER_NAME: "seed", GIT_COMMITTER_EMAIL: "seed@example.com" },
  });
}
function write(rel, body) {
  const p = join(dir, rel);
  mkdirSync(join(p, ".."), { recursive: true });
  writeFileSync(p, body);
  return p;
}
const abs = (rel) => join(dir, rel);

const RETRY_V1 = `import { makeKey } from "./keys.js";

export async function charge(client, order) {
  for (let attempt = 0; attempt < 3; attempt++) {
    const key = makeKey();
    try {
      return await client.charge(order, { idempotencyKey: key });
    } catch (e) {
      if (!e.retryable) throw e;
    }
  }
  throw new Error("charge failed after 3 attempts");
}
`;
const RETRY_V2 = `import { makeKey } from "./keys.js";

export async function charge(client, order) {
  // one key for every attempt: the provider dedupes retries by it
  const key = makeKey(order.id);
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      return await client.charge(order, { idempotencyKey: key });
    } catch (e) {
      if (!e.retryable) throw e;
    }
  }
  throw new Error("charge failed after 3 attempts");
}
`;
const APP_V1 = `import { charge } from "./payment/retry.js";

export async function checkout(client, cart) {
  const order = { amount: cart.total };
  return charge(client, order);
}
`;
const APP_V2 = `import { charge } from "./payment/retry.js";

export async function checkout(client, cart) {
  const order = { id: cart.orderId, amount: cart.total };
  return charge(client, order);
}
`;

// ---- a local target for the proxy connections ----
const target = createServer((req, res) => {
  res.setHeader("content-type", "text/plain");
  res.end(`seed target saw ${req.method} ${req.url}\n`);
});

function plainHTTP(path) {
  return new Promise((resolve, reject) => {
    const port = target.address().port;
    const req = request({ host: httpProxy.host, port: httpProxy.port, method: "GET", path: `http://127.0.0.1:${port}${path}`, headers: { host: `127.0.0.1:${port}` } }, (res) => {
      res.resume();
      res.on("end", () => resolve(res.statusCode));
    });
    req.on("error", reject);
    req.end();
  });
}

// connectHost opens a CONNECT tunnel to host:port and closes it at once; any answer is fine.
function connectHost(host, port) {
  return new Promise((resolve) => {
    const c = connect(httpProxy.port, httpProxy.host, () => c.write(`CONNECT ${host}:${port} HTTP/1.1\r\nHost: ${host}:${port}\r\n\r\n`));
    let buf = "";
    c.on("data", (d) => {
      buf += d.toString("latin1");
      if (buf.includes("\r\n\r\n")) {
        c.destroy();
        resolve(buf.split("\r\n")[0]);
      }
    });
    c.on("error", (e) => resolve(`error ${e.message}`));
  });
}

// ---- the session ----
async function seed() {
  await hook({ ...base("SessionStart"), source: "startup" });
  await pause();

  // Side call: Claude Code's quota probe, answered 429. It is no turn and no error (AC2).
  const quota = await sideCall("quota", [], 429);
  expect("side", `#${quota} quota probe · 429, in the "side calls" control, never red`);

  // Turn 1: a command, thinking, the answer, then the title call.
  const p1 = "Run the test suite and tell me what's failing. The checkout tests have been flaky all week and I'd like to know whether it's the retry logic or the fixtures before I touch anything.";
  await promptHook(p1);
  userText(main, `<system-reminder>\nThe user's editor has src/payment/retry.js open.\n</system-reminder>\n${p1}`);
  const npmTest = tool("Bash", { command: "npm test", description: "Run the test suite" });
  const c1 = await call(main, [thinking("Run the suite first; then read the failures."), use(npmTest)]);
  await pre(npmTest);
  await run(2400);
  await post(npmTest, { stdout: "checkout › retries a timed-out charge … FAIL\n3 failing, 11 passing", stderr: "", interrupted: false });
  results(main, [npmTest, "3 failing, 11 passing"]);
  const c2 = await call(main, [text("3 tests fail, all on retries: the payment client sends a second charge when the first times out. The fixtures look fine.")]);
  const title = await sideCall("<session>Run the test suite…</session>\nWrite the title for this session as JSON.", [text('{"title":"Fix flaky checkout tests"}')]);
  await stop();
  expect("title", `"Fix flaky checkout tests" (generated by side call #${title})`);
  expect("turn 1", `heading = the prompt without the reminder; "+1 reminder" toggle; #${c1} "Thought and wrote", Read the thinking; ✓ Bash npm test ~2.4s; #${c2} the answer`);
  await pause();

  // Turn 2: two reads at once, a search, an answer with thinking.
  const p2 = "Read the checkout test and the payment client and find out why a retry double-charges.";
  await promptHook(p2);
  userText(main, p2);
  const rTest = tool("Read", { file_path: abs("test/checkout.test.js") });
  const rRetry = tool("Read", { file_path: abs("src/payment/retry.js") });
  const c3 = await call(main, [use(rTest), use(rRetry)]);
  await pre(rTest);
  await pre(rRetry);
  await run(150);
  await post(rTest, { type: "text", file: { filePath: abs("test/checkout.test.js"), content: "test('retries a timed-out charge', …)\n", numLines: 1, startLine: 1, totalLines: 1 } });
  await post(rRetry, { type: "text", file: { filePath: abs("src/payment/retry.js"), content: RETRY_V1, numLines: 13, startLine: 1, totalLines: 13 } });
  results(main, [rTest, "test(…)"], [rRetry, RETRY_V1]);
  const grep = tool("Grep", { pattern: "makeKey", path: abs("src") });
  const c4 = await call(main, [use(grep)]);
  await pre(grep);
  await run(120);
  await post(grep, { mode: "files_with_matches", numFiles: 2, filenames: [abs("src/payment/retry.js"), abs("src/payment/keys.js")] });
  results(main, [grep, "src/payment/retry.js\nsrc/payment/keys.js"]);
  const c5 = await call(main, [thinking("makeKey() runs inside the loop, so every attempt gets a new key."), text("Found it: retry.js calls makeKey() inside the retry loop, so each attempt gets a fresh idempotency key.")]);
  await stop();
  expect("turn 2", `#${c3} ran 2 at once: ✓ Read test/checkout.test.js, ✓ Read src/payment/retry.js; #${c4} ✓ Grep "makeKey" in src; #${c5} Read the thinking; Files tab: 2 read, 1 search`);
  await pause();

  // Turn 3: a failed Edit, a re-read, two edits that change files.
  const p3 = "Fix the retry so it reuses the idempotency key from the first attempt.";
  await promptHook(p3);
  userText(main, p3);
  const badEdit = tool("Edit", { file_path: abs("src/payment/retry.js"), old_string: "const key = makeKey()\n", new_string: "" });
  const c6 = await call(main, [use(badEdit)]);
  await pre(badEdit);
  await run(100);
  await failed(badEdit, "String to replace not found in file.\nString: const key = makeKey()");
  results(main, [badEdit, "<tool_use_error>String to replace not found in file.</tool_use_error>", true]);
  const reread = tool("Read", { file_path: abs("src/payment/retry.js") });
  const c7 = await call(main, [use(reread)]);
  await pre(reread);
  await run(100);
  await post(reread, { type: "text", file: { filePath: abs("src/payment/retry.js"), content: RETRY_V1, numLines: 13, startLine: 1, totalLines: 13 } });
  results(main, [reread, RETRY_V1]);
  await pause();
  const fixRetry = tool("Edit", { file_path: abs("src/payment/retry.js"), old_string: "export async function charge(client, order) {\n  for (let attempt = 0; attempt < 3; attempt++) {\n    const key = makeKey();", new_string: "export async function charge(client, order) {\n  // one key for every attempt: the provider dedupes retries by it\n  const key = makeKey(order.id);\n  for (let attempt = 0; attempt < 3; attempt++) {" });
  const fixApp = tool("Edit", { file_path: abs("src/app.js"), old_string: "  const order = { amount: cart.total };", new_string: "  const order = { id: cart.orderId, amount: cart.total };" });
  const c8 = await call(main, [use(fixRetry), use(fixApp)]);
  await pre(fixRetry);
  write("src/payment/retry.js", RETRY_V2);
  await run(150);
  await post(fixRetry, { filePath: abs("src/payment/retry.js") });
  await pause();
  await pre(fixApp);
  write("src/app.js", APP_V2);
  await run(150);
  await post(fixApp, { filePath: abs("src/app.js") });
  results(main, [fixRetry, "ok"], [fixApp, "ok"]);
  const c9 = await call(main, [text("Moved makeKey() above the retry loop so every attempt sends the same key, and made app.js pass the order id through. Edit #1 failed because my old_string was out of date; I re-read the file and retried.")]);
  await stop();
  expect("turn 3", `#${c6} ✗ failed Edit src/payment/retry.js, first error line "String to replace not found in file."; #${c7} ✓ Read; #${c8} ran 2 at once: Edit retry.js (+3 −2) with "● named ~ modified src/payment/retry.js", Edit app.js (+1 −1) with "● named ~ modified src/app.js"; #${c9} answer; 1 failure`);
  await pause();

  // Turn 4: one command writes three files it does not name: only one running.
  const p4 = "Regenerate the fixtures with seed 42.";
  await promptHook(p4);
  userText(main, p4);
  const gen = tool("Bash", { command: "node scripts/gen-fixtures.js --seed 42", description: "Regenerate fixtures" });
  const c10 = await call(main, [use(gen)]);
  await pre(gen);
  await run(300);
  write("test/fixtures/orders.json", '[\n  { "id": "ord_42a", "seed": 42 }\n]\n');
  write("test/fixtures/users.json", '[\n  { "id": "usr_42a", "seed": 42 }\n]\n');
  write("test/fixtures/payments.json", '[\n  {\n    "id": "pay_42a",\n    "amount": 1999\n  }\n]\n');
  await run(900);
  await post(gen, { stdout: "wrote 3 fixtures", stderr: "", interrupted: false });
  results(main, [gen, "wrote 3 fixtures"]);
  const c11 = await call(main, [text("Regenerated: orders.json and users.json are new, payments.json changed (seed 42).")]);
  await stop();
  expect("turn 4", `#${c10} ✓ Bash node scripts/gen-fixtures.js --seed 42 with three "◐ only one running" changes: + created test/fixtures/orders.json, users.json, ~ modified payments.json; #${c11} answer`);
  await pause();

  // Turn 5: two commands at once; neither names what changed: several running.
  const p5 = "Add stripe-mock as a dev dependency and rerun the checkout tests.";
  await promptHook(p5);
  userText(main, p5);
  const install = tool("Bash", { command: "npm install -D stripe-mock", description: "Install stripe-mock" });
  const rerun = tool("Bash", { command: "npm test -- checkout", description: "Rerun checkout tests" });
  const c12 = await call(main, [use(install), use(rerun)]);
  await pre(install);
  await pre(rerun);
  await run(300);
  write("package.json", '{\n  "name": "shop-api",\n  "devDependencies": {\n    "stripe-mock": "^0.188.0"\n  }\n}\n');
  await plainHTTP("/registry/stripe-mock");
  await run(900);
  await post(install, { stdout: "added 1 package", stderr: "", interrupted: false });
  await post(rerun, { stdout: "14 passing", stderr: "", interrupted: false });
  results(main, [install, "added 1 package"], [rerun, "14 passing"]);
  const c13 = await call(main, [text("stripe-mock 0.188.0 is installed and the checkout tests pass (14/14).")]);
  await stop();
  expect("turn 5", `#${c12} ran 2 at once: ✓ Bash npm install -D stripe-mock, ✓ Bash npm test -- checkout; then "◌ Not pinned to one command: #${c12} Bash npm install and #${c12} Bash npm test": ~ modified package.json and the 127.0.0.1 connection; #${c13} answer`);
  await pause();

  // Turn 6: a 500, the retry fetches docs, and an unassigned connection while two sessions run.
  const p6 = "Check Stripe's docs on how long an idempotency key stays valid.";
  await promptHook(p6);
  userText(main, p6);
  const err = await call(main, [], { status: 500 });
  const fetchDocs = tool("WebFetch", { url: `http://127.0.0.1:${target.address().port}/docs/idempotency`, prompt: "How long is a key kept?" });
  const c14 = await call(main, [use(fetchDocs)]);
  await pre(fetchDocs);
  await plainHTTP("/docs/idempotency");
  await run(400);
  await post(fetchDocs, { result: "Keys are kept for at least 24 hours." });
  results(main, [fetchDocs, "Keys are kept for at least 24 hours."]);
  await hook({ ...base("SessionStart", other), cwd: undefined, source: "startup" });
  await plainHTTP("/unassigned");
  await hook({ ...base("SessionEnd", other), cwd: undefined, reason: "other" });
  const c15 = await call(main, [text("Keys are kept for at least 24 hours, so reusing one across retries a few seconds apart is safe.")]);
  await stop();
  expect("turn 6", `#${err} errored, status 500 as a word in red, in "Needs a look"; #${c14} ✓ WebFetch 127.0.0.1:…/docs/idempotency with "● named ⇄ 127.0.0.1"; #${c15} answer; Sites tab names 1 unassigned connection with a link`);
  await pause();

  // Turn 7: a skill, then a sub-agent that writes a doc and runs a long command, during
  // which the harness connects to its own host.
  const p7 = "Write the change up as a PDF for review.";
  await promptHook(p7);
  userText(main, p7);
  const skill = tool("Skill", { skill: "pdf" });
  const c16 = await call(main, [use(skill)]);
  await pre(skill);
  await run(200);
  await post(skill, { result: "loaded" });
  results(main, [skill, "Launching skill: pdf"]);
  const agentPrompt = "Write docs/checkout-fix.md describing the retry fix, then render it to docs/checkout-fix.pdf with pandoc.";
  const agent = tool("Agent", { subagent_type: "general-purpose", description: "write the review doc", prompt: agentPrompt });
  const c17 = await call(main, [use(agent)]);
  await pre(agent);
  await hook({ ...base("SubagentStart"), agent_id: "agent_seed016", agent_type: "general-purpose" });
  const sub = thread();
  userText(sub, agentPrompt);
  const writeDoc = tool("Write", { file_path: abs("docs/checkout-fix.md"), content: "# Checkout retry fix\n\nOne idempotency key per charge.\n" }, "agent_seed016");
  const s1 = await call(sub, [use(writeDoc)]);
  await pre(writeDoc);
  write("docs/checkout-fix.md", "# Checkout retry fix\n\nOne idempotency key per charge.\n");
  await run(150);
  await post(writeDoc, { type: "create", filePath: abs("docs/checkout-fix.md") });
  results(sub, [writeDoc, "ok"]);
  await pause();
  const pandoc = tool("Bash", { command: "pandoc docs/checkout-fix.md -o docs/checkout-fix.pdf", description: "Render the PDF" }, "agent_seed016");
  const s2 = await call(sub, [use(pandoc)]);
  await pre(pandoc);
  await run(400);
  const harness = await connectHost("downloads.claude.ai", 443);
  write("docs/checkout-fix.pdf", Buffer.from([0x25, 0x50, 0x44, 0x46, 0x2d, 0x31, 0x2e, 0x37, 0x0a, 0x00, 0xff, 0xfe, 0x00, 0x01]));
  await sleep(LONG_COMMAND_MS);
  await post(pandoc, { stdout: "", stderr: "", interrupted: false });
  results(sub, [pandoc, "ok"]);
  const s3 = await call(sub, [text("Wrote docs/checkout-fix.md and rendered docs/checkout-fix.pdf (2 pages).")]);
  await hook({ ...base("SubagentStop"), agent_id: "agent_seed016", agent_type: "general-purpose", stop_hook_active: false });
  await post(agent, { content: [{ type: "text", text: "Wrote the doc and the PDF." }] });
  results(main, [agent, "Wrote the doc and the PDF."]);
  const c18 = await call(main, [text("Wrote docs/checkout-fix.md and rendered it to docs/checkout-fix.pdf (2 pages).")]);
  await stop();
  expect("turn 7", `#${c16} ✓ Skill pdf; #${c17} ✓ Agent general-purpose · write the review doc, with the sub-agent block under it: #${s1} ✓ Write docs/checkout-fix.md (● named + created), #${s2} ✓ Bash pandoc … (${fast ? "~5s" : "~2m"}) with "● named + created docs/checkout-fix.pdf" (binary), #${s3} reports back; "○ Not caused by a tool call": ⚙ harness downloads.claude.ai (${harness}) "during #${s2} Bash"; #${c18} answer; tool time leaves the Agent span out`);
  await pause();

  // Between turns 7 and 8: your own edit, with no tool running.
  write("notes/review.md", "- check the retry fix with stripe-mock\n");
  await pause();
  expect("between", `"seen between turns 7 and 8": ○ outside + created notes/review.md; a turn-list entry of its own; in "Needs a look"`);

  // Turn 8: hooks only, no model calls captured.
  const p8 = "commit it";
  await promptHook(p8);
  await pause(); // the prompt's checkpoint finds your edit before the command starts
  const commit = tool("Bash", { command: "git commit -am 'fix retry key'", description: "Commit" });
  await pre(commit);
  await run(300);
  await post(commit, { stdout: "[main 1a2b3c4] fix retry key", stderr: "", interrupted: false });
  await stop();
  expect("turn 8", `hooks only: "no model calls captured", ? unmatched Bash git commit -am 'fix retry key' without #n; in "Needs a look"`);
}

// liveTurn adds one short turn: a prompt, a command that writes a file, an answer.
async function liveTurn(n) {
  const p = `Live turn ${n}: touch a file.`;
  await promptHook(p);
  userText(main, p);
  const t = tool("Bash", { command: `echo ${n} > live/${n}.txt` });
  await call(main, [use(t)]);
  await pre(t);
  write(`live/${n}.txt`, `${n}\n`);
  await run(300);
  await post(t, { stdout: "", stderr: "", interrupted: false });
  results(main, [t, "ok"]);
  await call(main, [text(`Wrote live/${n}.txt.`)]);
  await stop();
}

rmSync(dir, { recursive: true, force: true });
mkdirSync(dir, { recursive: true });
git("init", "-q");
write(".gitignore", "node_modules\n");
write("package.json", '{\n  "name": "shop-api"\n}\n');
write("src/payment/retry.js", RETRY_V1);
write("src/payment/keys.js", "export const makeKey = (id) => `${id ?? 'k'}-${Date.now()}`;\n");
write("src/app.js", APP_V1);
write("test/checkout.test.js", "test('retries a timed-out charge', () => {});\n");
write("test/fixtures/payments.json", '[\n  {\n    "id": "pay_8c1",\n    "amount": 1999\n  }\n]\n');
git("add", ".");
git("commit", "-q", "-m", "seed");

stub.on("error", (e) => {
  console.error(`stub upstream: ${e.message}`);
  process.exit(1);
});
await new Promise((r) => stub.listen(Number(args["stub-port"]), "127.0.0.1", r));
await new Promise((r) => target.listen(0, "127.0.0.1", r));

const uiID = Buffer.from(JSON.stringify(["local", "claude-code", session])).toString("base64url");
console.log(`seed repo: ${dir}`);
console.log(`session:   ${session}`);
console.log(`open:      ${args.ui}/#/sessions/${uiID}`);
try {
  await seed();
  if (args.live) {
    console.log("adding one turn every 5 s, Ctrl-C to stop");
    for (let n = 1; ; n++) {
      await sleep(5000);
      await liveTurn(n);
      console.log(`live turn ${n}`);
    }
  }
  await hook({ ...base("SessionEnd"), reason: "other" });
} finally {
  if (!args.live) {
    stub.close();
    target.close();
  }
}
console.log("\nexpected:");
for (const line of expected) console.log(`  ${line}`);
console.log(`\nCLI: bin/gateway timeline -config <cfg> ${session}`);
