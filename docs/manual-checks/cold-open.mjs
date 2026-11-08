// Cold-opens a session in headless Chrome and prints how long the timeline takes to show,
// for the 013 render check (step 13). It drives Chrome over the DevTools protocol with
// node's built-in WebSocket: Node only, no packages. See 013-web-ui.md.
//
//   node docs/manual-checks/cold-open.mjs [--ui URL] [--session ID] [--runs 3] [--chrome PATH] [--port 9339]
//
// The session defaults to the newest one on the Sessions page. Each run starts a fresh
// Chrome with an empty profile, loads #/sessions/{id} and reports, from navigation start:
// the first turn card, the moment the card count stops changing, the cards and DOM nodes,
// and how many times the turn list was rebuilt. It exits 1 if a run never shows a card.
import { spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { parseArgs } from "node:util";

const { values: args } = parseArgs({
  options: {
    ui: { type: "string", default: "http://127.0.0.1:7199" },
    session: { type: "string", default: "" },
    runs: { type: "string", default: "3" },
    chrome: { type: "string", default: process.env.CHROME || "google-chrome" },
    port: { type: "string", default: "9339" },
    timeout: { type: "string", default: "30000" }, // ms a run may take before it counts as hung
  },
});
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const QUIET_MS = 1500; // the card count must hold this long to count as settled

// WATCH runs in the page before its own scripts. It notes, in performance.now() time
// (0 = navigation start), the first turn card, the last change of the card count, and each
// rebuild of the list (a first card added again).
const WATCH = `(() => {
  const w = (window.__coldOpen = { first: 0, last: 0, cards: 0, rebuilds: 0 });
  new MutationObserver((ms) => {
    for (const m of ms) for (const n of m.addedNodes) {
      if (n.nodeType === 1 && n.matches("section.turn") && n === n.parentNode.firstElementChild) w.rebuilds++;
    }
    const cards = document.querySelectorAll("section.turn").length;
    if (cards && !w.first) w.first = performance.now();
    if (cards !== w.cards) { w.cards = cards; w.last = performance.now(); }
  }).observe(document, { childList: true, subtree: true });
})();`;

async function newestSession() {
  const res = await fetch(`${args.ui}/api/sessions`);
  if (!res.ok) throw new Error(`GET /api/sessions: ${res.status}`);
  const { items } = await res.json();
  if (!items || !items.length) throw new Error("no sessions captured yet");
  return items[0];
}

async function run(id) {
  const port = Number(args.port);
  const dir = mkdtempSync(join(tmpdir(), "cold-open-"));
  // detached, so the whole process group can be killed: google-chrome is often a wrapper
  // script, and killing only its pid leaves the browser running on the port
  const chrome = spawn(args.chrome, ["--headless=new", `--remote-debugging-port=${port}`, `--user-data-dir=${dir}`, "--no-first-run", "--window-size=1400,900", "about:blank"], { stdio: "ignore", detached: true });
  let ws;
  try {
    let targets;
    for (let i = 0; i < 100 && !targets; i++) {
      try {
        targets = await (await fetch(`http://127.0.0.1:${port}/json`)).json();
      } catch {
        await sleep(100);
      }
    }
    if (!targets) throw new Error(`chrome did not open port ${port}`);
    ws = new WebSocket(targets.find((t) => t.type === "page").webSocketDebuggerUrl);
    await new Promise((resolve, reject) => {
      ws.onopen = resolve;
      ws.onerror = reject;
    });
    let next = 0;
    const waiting = new Map();
    ws.onmessage = (m) => {
      const d = JSON.parse(m.data);
      if (waiting.has(d.id)) {
        waiting.get(d.id)(d);
        waiting.delete(d.id);
      }
    };
    // send resolves to the reply, or to null when the page does not answer within ms
    const send = (method, params = {}, ms = 10000) =>
      new Promise((resolve) => {
        const n = ++next;
        waiting.set(n, resolve);
        ws.send(JSON.stringify({ id: n, method, params }));
        setTimeout(() => waiting.delete(n) && resolve(null), ms);
      });
    const read = async () => {
      const r = await send("Runtime.evaluate", { expression: "JSON.stringify(window.__coldOpen || null)", returnByValue: true });
      return r && r.result && r.result.result ? JSON.parse(r.result.result.value) : null;
    };
    await send("Page.enable");
    await send("Page.addScriptToEvaluateOnNewDocument", { source: WATCH });
    await send("Page.navigate", { url: `${args.ui}/#/sessions/${encodeURIComponent(id)}` });
    const start = Date.now();
    let w = null;
    while (Date.now() - start < Number(args.timeout)) {
      await sleep(250);
      w = await read();
      if (w === null) continue; // the page has not answered: keep waiting until the timeout
      if (w.first && Date.now() - start > w.last + QUIET_MS) break;
    }
    const nodes = await send("Runtime.evaluate", { expression: "document.querySelectorAll('*').length", returnByValue: true });
    return {
      hung: !w || !w.first,
      first: w && Math.round(w.first),
      settled: w && Math.round(w.last),
      cards: w && w.cards,
      rebuilds: w && w.rebuilds,
      nodes: nodes && nodes.result ? nodes.result.result.value : null,
    };
  } finally {
    try {
      ws?.close();
    } catch {
      // already closed
    }
    try {
      process.kill(-chrome.pid, "SIGKILL");
    } catch {
      // already gone
    }
    await sleep(300);
    rmSync(dir, { recursive: true, force: true });
  }
}

let id = args.session;
if (!id) {
  const s = await newestSession();
  id = s.id;
  console.log(`session ${s.session_key}: ${s.model_calls} calls, ${s.exchange_count} exchanges`);
}
let failed = false;
for (let i = 1; i <= Number(args.runs); i++) {
  const r = await run(id);
  if (r.hung) {
    failed = true;
    console.log(`run ${i}: no turn card within ${args.timeout} ms`);
    continue;
  }
  console.log(`run ${i}: first turn card ${r.first} ms, settled ${r.settled} ms, ${r.cards} cards, ${r.nodes} nodes, ${r.rebuilds} list rebuilds`);
}
process.exit(failed ? 1 : 0);
