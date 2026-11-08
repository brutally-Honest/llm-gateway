// Seeds one Claude Code-shaped session with file, read and network effects for the 014
// manual check. It makes a scratch git repo, posts hook events to a running gateway as
// Claude Code would, changes files between them (a .env and a 500-file burst included) and
// makes connections through both proxy listeners. See 014-effects.md. Node only, no packages.
//
//   node docs/manual-checks/seed-effects.mjs [--dir PATH] [--pause MS] [--gateway URL] [--ui URL]
//                                            [--http-proxy HOST:PORT] [--socks-proxy HOST:PORT]
//
// The scratch repo must not be under a temp folder or be the home folder: the gateway never
// watches those. The default is ~/llmgw-seed-effects/<time>.
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
    dir: { type: "string", default: join(homedir(), "llmgw-seed-effects", String(Date.now())) },
    pause: { type: "string", default: "1500" },
    gateway: { type: "string", default: "http://127.0.0.1:7197" },
    "http-proxy": { type: "string", default: "127.0.0.1:7198" },
    "socks-proxy": { type: "string", default: "127.0.0.1:7195" },
    ui: { type: "string", default: "http://127.0.0.1:7199" },
  },
});
const dir = args.dir;
const pause = Number(args.pause);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const hostPort = (s) => {
  const i = s.lastIndexOf(":");
  return { host: s.slice(0, i), port: Number(s.slice(i + 1)) };
};
const httpProxy = hostPort(args["http-proxy"]);
const socksProxy = hostPort(args["socks-proxy"]);

// ---- hook events, shaped as Claude Code sends them ----

const session = randomUUID();
const other = randomUUID();
let toolN = 0;

async function hook(body) {
  const res = await fetch(`${args.gateway}/_gateway/hooks/claude-code`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`hook ${body.hook_event_name}: HTTP ${res.status}`);
}

const base = (name, sid = session) => ({ hook_event_name: name, session_id: sid, cwd: dir, transcript_path: "" });

async function prompt(text) {
  await hook({ ...base("UserPromptSubmit"), prompt: text });
}

async function tool(name, input, response) {
  const id = `toolu_seed_${++toolN}`;
  await hook({ ...base("PreToolUse"), tool_use_id: id, tool_name: name, tool_input: input });
  await hook({ ...base("PostToolUse"), tool_use_id: id, tool_name: name, tool_input: input, tool_response: response });
}

async function stop() {
  await hook({ ...base("Stop"), stop_hook_active: false });
}

// ---- the scratch repo ----

function git(...a) {
  execFileSync("git", ["-C", dir, ...a], {
    stdio: "ignore",
    env: { ...process.env, GIT_AUTHOR_NAME: "seed", GIT_AUTHOR_EMAIL: "seed@example.com", GIT_COMMITTER_NAME: "seed", GIT_COMMITTER_EMAIL: "seed@example.com" },
  });
}

function write(rel, text) {
  const p = join(dir, rel);
  mkdirSync(join(p, ".."), { recursive: true });
  writeFileSync(p, text);
  return p;
}

// ---- a local target for the proxy connections ----

const target = createServer((req, res) => {
  res.setHeader("content-type", "text/plain");
  res.end(`seed target saw ${req.method} ${req.url}\n`);
});
await new Promise((r) => target.listen(0, "127.0.0.1", r));
const targetPort = target.address().port;

// plainHTTP sends GET http://127.0.0.1:<port><path> through the HTTP proxy.
function plainHTTP(path) {
  return new Promise((resolve, reject) => {
    const req = request({ host: httpProxy.host, port: httpProxy.port, method: "GET", path: `http://127.0.0.1:${targetPort}${path}`,
      headers: { host: `127.0.0.1:${targetPort}`, "proxy-authorization": "Basic c2VlZDpzZWNyZXQ=" } }, (res) => {
      res.resume();
      res.on("end", () => resolve(res.statusCode));
    });
    req.on("error", reject);
    req.end();
  });
}

// reader hands out the bytes a socket receives, waiting for as many as asked.
function reader(c) {
  let buf = Buffer.alloc(0);
  let wake = null;
  let ended = false;
  c.on("data", (d) => {
    buf = Buffer.concat([buf, d]);
    if (wake) wake();
  });
  c.on("end", () => {
    ended = true;
    if (wake) wake();
  });
  const more = () => new Promise((r) => { wake = r; });
  return {
    async take(n) {
      while (buf.length < n && !ended) await more();
      const out = buf.subarray(0, n);
      buf = buf.subarray(n);
      return out;
    },
    async until(text) {
      while (!buf.toString("latin1").includes(text) && !ended) await more();
      const i = buf.toString("latin1").indexOf(text);
      const out = buf.subarray(0, i < 0 ? buf.length : i + text.length);
      buf = buf.subarray(out.length);
      return out.toString("latin1");
    },
    async rest() {
      while (!ended) await more();
      return buf;
    },
  };
}

function dial(p) {
  return new Promise((resolve, reject) => {
    const c = connect(p.port, p.host, () => resolve(c));
    c.on("error", reject);
  });
}

// getThrough sends one GET through an open tunnel and waits for the server to close it.
async function getThrough(c, r, host) {
  c.write(`GET /through-tunnel HTTP/1.1\r\nHost: ${host}\r\nConnection: close\r\n\r\n`);
  await r.rest();
  c.destroy();
  return "ok";
}

// connectTo opens a CONNECT tunnel to 127.0.0.1:port through the HTTP proxy.
async function connectTo(port) {
  const c = await dial(httpProxy);
  const r = reader(c);
  c.write(`CONNECT 127.0.0.1:${port} HTTP/1.1\r\nHost: 127.0.0.1:${port}\r\n\r\n`);
  const head = await r.until("\r\n\r\n");
  if (!head.startsWith("HTTP/1.1 200")) {
    c.destroy();
    return `refused (${head.split("\r\n")[0]})`;
  }
  return getThrough(c, r, `127.0.0.1:${port}`);
}

// socksTo opens a SOCKS5 tunnel to localhost:port, by name, through the SOCKS5 proxy.
async function socksTo(port) {
  const c = await dial(socksProxy);
  const r = reader(c);
  c.write(Buffer.from([5, 1, 0]));
  const greet = await r.take(2);
  if (greet[1] !== 0) throw new Error("SOCKS5: no-auth method refused");
  const host = Buffer.from("localhost");
  c.write(Buffer.concat([Buffer.from([5, 1, 0, 3, host.length]), host, Buffer.from([port >> 8, port & 255])]));
  const rep = await r.take(10);
  if (rep[1] !== 0) {
    c.destroy();
    return `failed (reply ${rep[1]})`;
  }
  return getThrough(c, r, `localhost:${port}`);
}

// ---- the seeded session ----

rmSync(dir, { recursive: true, force: true });
mkdirSync(dir, { recursive: true });
git("init", "-q");
write(".gitignore", ".env\n");
write("README.md", "# seed repo\n\nA scratch repo for the 014 effects check.\n");
write("src/app.js", "export function greet(name) {\n  return `hello ${name}`;\n}\n");
git("add", ".");
git("commit", "-q", "-m", "seed");

const uiID = Buffer.from(JSON.stringify(["local", "claude-code", session])).toString("base64url");
console.log(`seed repo: ${dir}`);
console.log(`session:   ${session}`);
console.log(`open:      ${args.ui}/#/sessions/${uiID}`);

// The first hook with a working folder records the baseline; nothing is reported for it.
await hook({ ...base("SessionStart"), source: "startup" });
await sleep(pause);

// A second active session makes the next connection unassigned (AC10).
await hook({ ...base("SessionStart", other), cwd: undefined, source: "startup" });
console.log(`unassigned plain HTTP: ${await plainHTTP("/unassigned?token=should-not-be-stored")}`);
await hook({ ...base("SessionEnd", other), cwd: undefined, reason: "other" });
await sleep(pause);

// Turn 1: a read, a write and an edit of a committed file.
await prompt("Read the app, add a test and change the greeting.");
await tool("Read", { file_path: join(dir, "src/app.js"), offset: 1, limit: 3 },
  { type: "text", file: { filePath: join(dir, "src/app.js"), content: "export function greet(name) {\n  return `hello ${name}`;\n}\n", numLines: 3, startLine: 1, totalLines: 3 } });
await tool("Grep", { pattern: "greet", output_mode: "content" }, { mode: "content", numFiles: 1, filenames: [], content: "src/app.js:1:export function greet(name) {" });
write("src/app.test.js", "import { greet } from './app.js';\nconsole.assert(greet('x') === 'hi x');\n");
await tool("Write", { file_path: join(dir, "src/app.test.js"), content: "…" }, { type: "create", filePath: join(dir, "src/app.test.js") });
write("src/app.js", "export function greet(name) {\n  return `hi ${name}`;\n}\n");
await tool("Edit", { file_path: join(dir, "src/app.js"), old_string: "hello", new_string: "hi" }, { filePath: join(dir, "src/app.js") });
await stop();
await sleep(pause);

// Turn 2: a .env change (git-ignored, still seen) and connections through both proxies.
await prompt("Set the API token in .env and fetch the docs.");
write(".env", "API_TOKEN=seed-not-a-real-secret\n");
await tool("Bash", { command: "echo API_TOKEN=… > .env" }, { stdout: "", stderr: "", interrupted: false });
console.log(`plain HTTP:  ${await plainHTTP("/docs/page?token=should-not-be-stored")}`);
console.log(`CONNECT:     ${await connectTo(targetPort)}`);
console.log(`SOCKS5:      ${await socksTo(targetPort)}`);
const closed = createServer();
await new Promise((r) => closed.listen(0, "127.0.0.1", r));
const closedPort = closed.address().port;
await new Promise((r) => closed.close(r));
console.log(`CONNECT to a closed port: ${await connectTo(closedPort)}`);
await tool("Bash", { command: "curl https://example.com" }, { stdout: "", stderr: "", interrupted: false });
await stop();
await sleep(pause);

// Turn 3: a 500-file burst behind one folded row, then a delete.
await prompt("Generate the fixtures, then remove the readme.");
for (let i = 0; i < 500; i++) write(`fixtures/f${String(i).padStart(3, "0")}.txt`, `fixture ${i}\n`);
rmSync(join(dir, "README.md"));
await tool("Bash", { command: "node gen-fixtures.js && rm README.md" }, { stdout: "500 files", stderr: "", interrupted: false });
await stop();
await sleep(pause);

target.close();
console.log("seeded: 3 turns; expect the counts in 014-effects.md");
