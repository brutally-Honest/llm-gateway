import test from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { all, byTag, installDom, textOf, withClass } from "./dom-stub.js";

// The session page renders the server's view (spec 016 AC1, AC3, AC11, AC12): turns, the
// right card's tabs and the details dialog come from a view fixture, with nothing
// computed in the browser.

installDom();
const { createSessionPage } = await import("../web/session-page.js");

const web = new URL("../web/", import.meta.url);

const mark = (glyph, word) => ({ glyph, word });
const eff = (o) => ({ id: 1, kind: "change", change: "modified", at: "2026-11-10T10:00:06Z", what: mark("~", "modified"), path: "src/a.js", full_path: "/w/src/a.js", added: 2, removed: 1, has_diff: true, label: "named", mark: mark("●", "named"), why: "Edit names this file in its input.", tool: "t2", by: "by #4 Edit", where: "turn 1", turn: 1, ...o });
const tool = (o) => ({ id: "t2", name: "Edit", kind: "file", state: mark("✓", "ok"), chip: "ok", summary: "src/a.js", added: 2, removed: 1, dur_ms: 400, call_n: 4, turn: 1, effects: [], ...o });
const step = (n, o) => ({ n, exchange_id: `e${n}`, at: "2026-11-10T10:00:05Z", dur_ms: 1000, did: "wrote", tools: [], ...o });
const turn = (n, o) => ({ n, hash: `h${n}`, prompt: `prompt ${n}`, at: "2026-11-10T10:00:00Z", clock_ms: 7100, model_ms: 4000, tool_ms: 1400, wait_ms: 1700, counts: { tools: 1, changed: 0, read: 0, searched: 0, sites: 0, failed: 0 }, steps: [], ...o });

function fixture() {
  const named = eff();
  const several = eff({ id: 2, path: "dist/out.js", full_path: "/w/dist/out.js", label: "several", mark: mark("◌", "several running"), has_diff: false, has_copy: true, added: -1, removed: -1, size: 2048, tool: "" });
  const harness = eff({ id: 3, kind: "net", host: "telemetry.example", port: 443, up: 10, down: 20, what: mark("⇄", "connected"), label: "harness", mark: mark("⚙", "harness"), during: "#4 Edit", path: "", full_path: "", has_diff: false, tool: "" });
  const outside = eff({ id: 4, path: "notes.md", full_path: "/w/notes.md", label: "outside", mark: mark("○", "outside any tool call"), tool: "", where: "between turns 1 and 2", turn: 0 });
  return {
    version: "v1",
    overview: { title: "Retry fix", title_source: "generated", status: mark("■", "ended"), client: "c", key: "k", models: ["m1"], time: { clock_ms: 9000, model_ms: 5000, tool_ms: 1500 }, files: { changed: 3, added: 2, removed: 1, read: 0 }, commands: { count: 1, longest: "npm test", longest_ms: 1000 }, sites: { count: 1, connections: 1 }, context: { last: 600 }, needs_look: [], tokens: {} },
    side_calls: [{ n: 1, exchange_id: "q", name: "quota probe", at: "2026-11-10T09:59:59Z", dur_ms: 100, status: "ok" }],
    turns: [
      turn(1, {
        reminders: ["be brief"], answer: "Fixed the retry.", answer_call: 5,
        counts: { tools: 3, changed: 2, read: 0, searched: 0, sites: 1, failed: 1 },
        steps: [
          step(3, { thinking: true, tools: [tool({ id: "t1", name: "Bash", kind: "command", state: mark("✗", "failed"), chip: "failed", summary: "npm test", added: 0, removed: 0, dur_ms: 1000, first_error: "Error: 1 failing", call_n: 3 })] }),
          step(4, { tools: [tool({ effects: [named] }), tool({ id: "t3", summary: "src/b.js", added: 1, removed: 0 })], several: [several], several_names: ["#4 Edit src/a.js", "#4 Edit src/b.js"] }),
          step(5, { did: "wrote the answer" }),
        ],
        loose: [harness],
      }),
      turn(2, { prompt: "prompt 2", steps: [step(6, { did: "wrote the answer" })] }),
      turn(3, { hooks_only: true, prompt: "", clock_ms: 0, hook_tools: [tool({ id: "h1", name: "Bash", kind: "command", state: mark("?", "unmatched"), chip: "unmatched", summary: "ls", added: 0, removed: 0, call_n: 0, turn: 3 })] }),
    ],
    between: [{ after: 1, at: "2026-11-10T10:00:09Z", title: "Seen between turns 1 and 2", text: "A change while no tool call was running: most likely your own edit.", effects: [outside] }],
    files: {
      coverage: "Changes seen at hook checkpoints.",
      changed: [
        { path: "src/a.js", full_path: "/w/src/a.js", added: 2, removed: 1, lines: true, last: named, changes: [named], reads: [] },
        { path: "notes.md", full_path: "/w/notes.md", added: 0, removed: 0, lines: false, last: outside, changes: [outside], reads: [] },
      ],
      read: [], searches: [],
    },
    commands: { coverage: "Every shell command.", items: [{ tool_id: "t1", summary: "npm test", state: mark("✗", "failed"), chip: "failed", turn: 1, dur_ms: 1000, did: "nothing changed" }] },
    sites: { coverage: "Through the proxy only.", items: [{ host: "telemetry.example", port: 443, connections: 1, up: 10, down: 20, where: "turn 1", who: "the client itself", harness: true, conns: [harness] }] },
    context: { coverage: "Input + cache write + cache read.", now: 600, first: 100, growth: 500, peak: 600, main: [{ n: 3, exchange_id: "e3", turn: 1, ctx: 100, delta: null, why: "first call" }, { n: 6, exchange_id: "e6", turn: 2, ctx: 600, delta: 500, why: "added #5's reply, your next prompt" }], subs: [] },
  };
}

const toolDetails = {
  id: "t1", name: "Bash", exchange_id: "e3", input: '{"command":"npm test"}', input_truncated: true,
  result: "Error: 1 failing", result_truncated: false, is_error: true, state: "asked+ran+reported", outcome: "failed", chip: "failed",
  input_differs: true, hook_input: '{"command":"npm test --ci"}', hook_omitted_bytes: 42,
  hooks: [{ phase: "pre_tool", name: "before-hook", at: "2026-11-10T10:00:03Z" }, { phase: "post_tool_failed", name: "failed-hook", at: "2026-11-10T10:00:04Z" }],
};

// mount builds the page on a view, with document.getElementById reading the page's tree.
function mount(view = fixture(), extra = {}) {
  const opened = [];
  const page = createSessionPage({
    callHref: (id) => `#/sessions/s1/call/${id}`,
    loadTool: async () => toolDetails,
    loadDiff: async () => ({ path: "src/a.js", diff: "@@ -1 +1 @@\n-old\n+new", truncated: false }),
    loadContent: async () => ({ path: "x", content: "x", valid: true }),
    openCall: (id) => opened.push(id),
    ...extra,
  });
  globalThis.document.getElementById = (id) => all(page.el).find((e) => e.id === id) || null;
  page.setView(view);
  return { page, opened };
}

const byId = (root, id) => all(root).find((e) => e.id === id);
const settle = () => new Promise((r) => setTimeout(r, 0));

test("rendersTurnsTabsAndDialogFromTheView", async () => {
  const { page } = mount();
  const story = textOf(page.el).replace(/\s+/g, " ");
  // turns: heading, times, reminders, pills, steps with their #n links, the answer
  assert.match(story, /Turn 1 .*prompt 1/);
  assert.match(story, /model 4\.0s · tools 1\.4s · waiting 1\.7s/);
  assert.match(story, /\+1 reminder/);
  assert.match(story, /3 tool calls 2 files changed 1 site ✗ 1 failed/);
  const links = withClass(page.el, "call-link").map((a) => a.href);
  assert.deepEqual(links.slice(0, 4), ["#/sessions/s1/call/e3", "#/sessions/s1/call/e4", "#/sessions/s1/call/e5", "#/sessions/s1/call/e6"]);
  assert.match(story, /Read the thinking/);
  assert.match(story, /Final answer Fixed the retry\./);
  // tool lines: state as glyph and word (✓ alone), summary, lines, first error
  const t1 = byId(page.el, "tool-t1");
  assert.match(textOf(t1), /✗ failed Bash npm test/);
  assert.match(textOf(t1.parentNode), /Error: 1 failing/);
  assert.match(textOf(byId(page.el, "tool-t2")).replace(/\s+/g, " "), /✓ Edit src\/a\.js \+2 −1 0\.4s/);
  // effects: under their tool, several after the step, loose with during, between apart
  assert.ok(byId(byId(page.el, "tool-t2").parentNode, "eff-1"), "the named change sits under its tool line");
  assert.match(textOf(byId(page.el, "several-e4")), /#4 Edit src\/a\.js and #4 Edit src\/b\.js ran together/);
  assert.match(textOf(byId(page.el, "eff-3")), /during #4 Edit/);
  const btw = byId(page.el, "between-1");
  assert.match(textOf(btw), /Seen between turns 1 and 2/);
  assert.ok(byId(btw, "eff-4"));
  // hooks-only turn
  assert.match(textOf(byId(page.el, "turn-3")), /No model calls captured\. The hooks saw:.*\? unmatched Bash ls/s);
  // turn list: one entry per turn and one for the between block
  assert.equal(page.entries().length, 4);

  // the right card: counts and rows from the view's lists
  const tabs = withClass(page.el, "tab");
  assert.deepEqual(tabs.map((t) => `${t.textContent}${t.children[0].textContent}`), ["Files2", "Commands1", "Sites1", "Context"]);
  assert.match(textOf(byId(page.el, "tp-files")), /a\.js/);
  tabs[3].fire("click");
  assert.equal(byId(page.el, "tp-context").hidden, false);
  assert.equal(byId(page.el, "tp-files").hidden, true);
  assert.deepEqual(withClass(page.el, "bars").flatMap((b) => b.children.map((a) => a.style.transform)), ["scaleY(0.167)", "scaleY(1.000)"]);

  // the dialog: a tool's details with the hook input and its own omitted-bytes note
  withClass(byId(page.el, "tool-t1"), "nm")[0].fire("click");
  await settle();
  const dlg = page.dialog.el;
  assert.equal(page.dialogOpen(), true);
  const sections = withClass(dlg, "ms");
  const sec = (title) => sections.find((s) => s.children[0].textContent === title);
  assert.match(textOf(sec("Hook input")), /npm test --ci/);
  assert.equal(withClass(sec("Hook input"), "muted-note").map((n) => n.textContent).join(), "… 42 bytes omitted");
  assert.equal(withClass(sec("Hook input"), "truncated").length, 0, "the hook input has no raw link");
  assert.equal(withClass(sec("Input"), "truncated")[0].href, "#/exchanges/e3", "the cut input keeps its raw link");
  assert.match(textOf(sec("Hooks")), /pre_tool before-hook.*post_tool_failed failed-hook/s);
  assert.equal(withClass(sec("Output"), "errbox").length, 1);
  // a related file opens on top; Back returns; Show in turn closes and places the row
  const related = withClass(sec("What it touched"), "linkish");
  assert.equal(related.length, 0, "t1 touched nothing");
  page.dialog.open("file:/w/src/a.js");
  await settle();
  assert.match(textOf(dlg), /Modified in turn 1/);
  assert.equal(withClass(dlg, "diff").length, 1);
  withClass(dlg, "x")[0].fire("click"); // Back
  assert.equal(page.dialog.current(), "tool:t1");
  withClass(dlg, "primary")[0].fire("click");
  assert.equal(page.dialogOpen(), false);

  // closed by the browser, its "close" event not yet fired: the next open starts afresh
  page.dialog.open("tool:t1");
  page.dialog.open("file:/w/src/a.js");
  dlg.open = false;
  page.dialog.open("site:telemetry.example");
  assert.equal(page.dialog.current(), "site:telemetry.example");
  assert.equal(withClass(dlg, "x")[0].hidden, true, "no Back to an item of the closed dialog");
  assert.equal(dlg.open, true);
});

test("clickingTheLastTurnMarksIt", () => {
  const { page } = mount();
  const entries = page.entries();
  const last = entries[entries.length - 1];
  last.button.fire("click");
  assert.equal(last.button.attrs["aria-current"], "true");
  assert.equal(entries.filter((e) => e.button.attrs["aria-current"] === "true").length, 1);
  assert.equal(page.current(), entries.length - 1);
});

test("liveEventReplacesOnlyTheChangedTurn", () => {
  const { page } = mount();
  const before = [1, 2, 3].map((n) => byId(page.el, `turn-${n}`));
  const kept = all(before[0]);
  const v = fixture();
  const changed = { ...v.turns[1], hash: "h2b", answer: "A new answer." };
  page.applyEvent({ ...v, full: false, order: [1, 2, 3], turns: [changed] });
  const after = [1, 2, 3].map((n) => byId(page.el, `turn-${n}`));
  assert.equal(after[0], before[0], "turn 1 is the same element");
  assert.deepEqual(all(after[0]), kept, "nothing inside turn 1 was rebuilt");
  assert.equal(after[2], before[2], "turn 3 is the same element");
  assert.notEqual(after[1], before[1], "turn 2 was replaced");
  assert.match(textOf(after[1]), /A new answer\./);
  // a turn the order drops is removed; the others stay in order
  page.applyEvent({ ...v, full: false, order: [1, 3], turns: [] });
  assert.equal(byId(page.el, "turn-2"), undefined);
  assert.equal(byId(page.el, "turn-1"), before[0]);
});

test("noRemovedBuilderIsImported", () => {
  const files = readdirSync(web).filter((f) => f.endsWith(".js"));
  assert.ok(!files.includes("timeline-page.js"));
  const removed = ["buildTurns", "placeEffects", "railItems", "stripBars", "buildStats", "filterTurns", "toolRowNode", "turnTarget", "countsText", "toolNumbers"];
  for (const f of files) {
    const src = readFileSync(new URL(f, web), "utf8");
    for (const name of removed) assert.ok(!src.includes(name), `${f} still names ${name}`);
  }
  // the session page's modules import no entry model
  for (const f of ["session-page.js", "story.js", "side-card.js", "dialog.js"]) {
    const imports = readFileSync(new URL(f, web), "utf8").match(/from "\.\/[\w-]+\.js"/g) || [];
    for (const i of imports) assert.ok(!/model\.js|effects\.js|filters\.js/.test(i), `${f}: ${i}`);
  }
});

// A 2,000-call session: 200 turns of ten steps, each with a tool line and an effect.
test("twoThousandCallViewRendersUnderBudget", () => {
  const v = fixture();
  v.turns = [];
  let n = 0;
  for (let t = 1; t <= 200; t++) {
    const steps = [];
    for (let s = 0; s < 10; s++) {
      n++;
      steps.push(step(n, { tools: [tool({ id: `t${n}`, effects: [eff({ id: n, tool: `t${n}` })] })] }));
    }
    v.turns.push(turn(t, { steps, answer: "done" }));
  }
  v.between = [];
  const start = performance.now();
  const { page } = mount(v);
  const took = performance.now() - start;
  assert.equal(withClass(page.el, "call-link").length, 2000);
  assert.ok(took < 2000, `rendering 2,000 calls took ${Math.round(took)} ms`);
});
