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
const eff = (o) => ({ id: 1, kind: "change", change: "modified", at: "2026-11-10T10:00:06Z", what: mark("~", "modified"), path: "src/a.js", full_path: "/w/src/a.js", added: 2, removed: 1, has_diff: true, label: "named", mark: mark("●", "named"), why: "Edit names this file in its input.", tool: "t2", by: "#4 Edit src/a.js", where: "turn 1", turn: 1, ...o });
const tool = (o) => ({ id: "t2", name: "Edit", kind: "file", state: mark("✓", "ok"), chip: "ok", summary: "src/a.js", added: 2, removed: 1, dur_ms: 400, call_n: 4, turn: 1, effects: [], ...o });
const step = (n, o) => ({ n, exchange_id: `e${n}`, at: "2026-11-10T10:00:05Z", dur_ms: 1000, did: "wrote", tools: [], ...o });
const turn = (n, o) => ({ n, hash: `h${n}`, prompt: `prompt ${n}`, at: "2026-11-10T10:00:00Z", clock_ms: 7100, model_ms: 4000, tool_ms: 1400, wait_ms: 1700, counts: { calls: 1, tools: 1, changed: 0, read: 0, searched: 0, sites: 0, failed: 0 }, steps: [], ...o });

function fixture() {
  const named = eff();
  const several = eff({ id: 2, path: "dist/out.js", full_path: "/w/dist/out.js", label: "several", mark: mark("◌", "several running"), has_diff: false, has_copy: true, added: -1, removed: -1, size: 2048, tool: "", by: "#4 Edit src/a.js, #4 Edit src/b.js" });
  const harness = eff({ id: 3, kind: "net", host: "telemetry.example", port: 443, up: 10, down: 20, what: mark("⇄", "connected"), label: "harness", mark: mark("⚙", "harness"), during: "#4 Edit", path: "", full_path: "", has_diff: false, tool: "", by: "" });
  const outside = eff({ id: 4, path: "notes.md", full_path: "/w/notes.md", label: "outside", mark: mark("○", "outside any tool call"), tool: "", by: "", where: "between turns 1 and 2", turn: 0 });
  return {
    version: "v1",
    overview: { title: "Retry fix", title_source: "generated", status: mark("■", "ended"), client: "c", key: "k", models: ["m1"], time: { clock_ms: 9000, model_ms: 5000, tool_ms: 1500, wait_ms: 1000, between_ms: 1500 }, files: { changed: 3, added: 2, removed: 1, read: 0 }, commands: { count: 1, longest: "pandoc docs/checkout-fix.md -o docs/checkout-fix.pdf --pdf-engine=xelatex", longest_ms: 5400 }, sites: { count: 1, connections: 1 }, context: { last: 600 }, needs_look: [], tokens: {} },
    side_calls: [{ n: 1, exchange_id: "q", name: "quota probe", at: "2026-11-10T09:59:59Z", dur_ms: 100, status: "ok" }],
    turns: [
      turn(1, {
        reminders: ["be brief"], answer: "Fixed the retry.", answer_call: 5,
        counts: { calls: 3, tools: 3, changed: 2, read: 0, searched: 0, sites: 1, failed: 1 },
        steps: [
          step(3, { thinking: true, tools: [tool({ id: "t1", name: "Bash", kind: "command", state: mark("✗", "failed"), chip: "failed", summary: "npm test", added: 0, removed: 0, dur_ms: 1000, first_error: "Error: 1 failing", call_n: 3 })] }),
          step(4, { tools: [tool({ effects: [named] }), tool({ id: "t3", summary: "src/b.js", added: 1, removed: 0 })], several: [several], several_names: ["#4 Edit src/a.js", "#4 Edit src/b.js"] }),
          step(5, { did: "wrote the answer" }),
        ],
        loose: [harness],
      }),
      turn(2, { prompt: "prompt 2", models: ["m1", "m2"], steps: [step(6, { did: "wrote the answer" })] }),
      turn(3, { hooks_only: true, prompt: "", clock_ms: 0, counts: { calls: 0, tools: 1, changed: 0, read: 0, searched: 0, sites: 0, failed: 0 }, hook_tools: [tool({ id: "h1", name: "Bash", kind: "command", state: mark("?", "unmatched"), chip: "unmatched", summary: "ls", added: 0, removed: 0, call_n: 0, turn: 3 })] }),
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
  const longest = all(page.el).find((e) => (e.className || "").split(" ").includes("one-line"));
  assert.equal(longest.textContent, "longest 5.4s · pandoc docs/checkout-fix.md -o docs/checkout-fix.pdf --pdf-engine=xelatex", "the whole command, cut by CSS only");
  assert.equal(longest.title, "pandoc docs/checkout-fix.md -o docs/checkout-fix.pdf --pdf-engine=xelatex");
  assert.match(story, /Time 9\.0s model 5\.0s · tools 1\.5s · waiting 1\.0s · between turns 1\.5s/, "the Time card splits the clock into four parts");
  assert.match(story, /\+1 reminder/);
  assert.match(story, /All 3 model calls 3 tool calls 2 files changed 1 site ✗ 1 failed/);
  const links = withClass(page.el, "call-link").map((a) => a.href);
  assert.deepEqual(links.slice(0, 4), ["#/sessions/s1/call/e3", "#/sessions/s1/call/e4", "#/sessions/s1/call/e5", "#/sessions/s1/call/e6"]);
  assert.match(story, /Read the thinking/);
  assert.match(story, /Final answer Fixed the retry\./, "the answer has no switch of its own; its copy control is an icon (027 C4)");
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
  assert.match(textOf(byId(page.el, "turn-3")), /The hooks saw:.*\? unmatched Bash ls/s);
  assert.doesNotMatch(textOf(byId(page.el, "turn-3")), /No model calls captured/, "said once, by the model chip (027 C4)");
  // turn list: one entry per turn; the between block is in the story, not the list (027 C4)
  assert.equal(page.entries().length, 3);

  // the tabs (027 C4: the story first, then the old right card) with counts and rows from the view
  const tabs = withClass(page.el, "tab");
  assert.deepEqual(tabs.map((t) => `${t.textContent}${t.children[0].textContent}`), ["Story", "Files2", "Commands1", "Sites1", "Context"]);
  assert.equal(byId(page.el, "tp-story").hidden, false, "the story is the tab shown first");
  assert.ok(byId(byId(page.el, "tp-story"), "turn-1"), "the turns are in the Story tab");
  // a changed file in the Files tab is its name and its lines alone; the folder, turn and
  // attribution are in its dialog, the full path on hover (research 2026-11-13)
  const fileRows = withClass(byId(page.el, "tp-files"), "row").filter((r) => r.className.split(" ").includes("file"));
  const rows = fileRows.map((r) => textOf(r).replace(/\s+/g, " ").trim());
  assert.deepEqual(rows, ["▸ a.js +2 −1", "▸ notes.md"]);
  assert.equal(fileRows[0].title, "/w/src/a.js");
  assert.match(textOf(byId(page.el, "eff-1")).replace(/\s+/g, " "), /● named/);
  assert.match(textOf(byId(page.el, "eff-4")).replace(/\s+/g, " "), /○ outside any tool call/);
  tabs[4].fire("click");
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

// shown is true when no node from n up to the page is hidden.
function shown(n) {
  for (; n; n = n.parentNode) if (n.hidden) return false;
  return true;
}

// A turn shows its prompt, its chips and its answer. "All" opens the whole stepper in
// order; every other chip opens the timeline narrowed to its kind, still in order, each
// row with its #n; one panel at a time (017 AC14, AC16 as amended 2026-11-13, E10).
test("chipsOpenOnePanelAtATime", () => {
  const { page } = mount();
  const t1 = byId(page.el, "turn-1");
  const chip = (id) => withClass(t1, "chip").find((b) => b.dataset.chip === id);
  const rowsOf = (id) => withClass(byId(t1, `cp-1-${id}`), "frow").map((r) => textOf(r).replace(/\s+/g, " ").trim());
  assert.ok(shown(withClass(t1, "prompt")[0]), "the prompt is shown");
  assert.ok(shown(withClass(t1, "answer")[0]), "the answer is shown");
  assert.deepEqual(withClass(t1, "chip").map((b) => b.textContent), ["All", "3 model calls", "3 tool calls", "2 files changed", "1 site", "✗ 1 failed"]);
  assert.equal(shown(byId(t1, "cp-1-all")), false, "no steps until a chip is clicked");
  assert.equal(chip("all").attrs["aria-expanded"], "false");

  // All: the whole stepper, each step's tool lines folded
  chip("all").fire("click");
  assert.ok(shown(byId(t1, "cp-1-all")));
  assert.ok(chip("all").classes.has("on"), "the chip is marked selected");
  assert.equal(withClass(byId(t1, "cp-1-all"), "call-link").length, 3);
  const body = byId(t1, "steps-e3");
  assert.equal(body.hidden, true, "a step's tool lines start folded");
  const toggle = withClass(t1, "fold").find((b) => b.attrs["aria-controls"] === "steps-e3");
  assert.match(textOf(toggle), /✗ Bash npm test/, "the folded line names its tools");
  assert.equal(byId(t1, "steps-e5"), undefined, "a step with nothing under it has no fold");
  assert.ok(body.parentNode.className.split(" ").includes("bad"), "a step whose tool failed is marked on the stepper");
  const loose = withClass(t1, "fold").find((b) => /Not caused by a tool call/.test(textOf(b)));
  assert.equal(loose.attrs["aria-expanded"], "false", "the effects no tool call caused are folded in All");
  toggle.fire("click");
  assert.equal(body.hidden, false);

  // model calls: one line per call, in order, no tool lines
  chip("calls").fire("click");
  assert.equal(shown(byId(t1, "cp-1-all")), false, "another chip closes the open panel");
  assert.deepEqual(rowsOf("calls"), ["#3 Wrote for 1.0s · 1 tool call Read the thinking", "#4 Wrote for 1.0s · 2 tool calls", "#5 Wrote the answer for 1.0s"]);
  // tool calls: every tool call in order with its #n
  chip("tools").fire("click");
  assert.deepEqual(rowsOf("tools"), ["#3 ✗ failed Bash npm test 1.0s Error: 1 failing", "#4 ✓ Edit src/a.js +2 −1 0.4s", "#4 ✓ Edit src/b.js +1 −0 0.4s"]);
  // files: each change in order with its #n and tool; several-running marked
  chip("changed").fire("click");
  assert.deepEqual(rowsOf("changed"), ["#4 Edit ~ src/a.js +2 −1", "#4 ◌ several running ~ dist/out.js 2.0 KB"]);
  // sites: a connection no tool call caused says so
  chip("sites").fire("click");
  assert.deepEqual(rowsOf("sites"), ["○ not caused by a tool call ⇄ telemetry.example ↑ 10 B ↓ 20 B"]);
  // failed: only the failed tool calls
  chip("failed").fire("click");
  assert.deepEqual(rowsOf("failed"), ["#3 ✗ failed Bash npm test 1.0s Error: 1 failing"]);
  chip("failed").fire("click");
  assert.equal(shown(byId(t1, "cp-1-failed")), false, "the selected chip closes its own panel");
  assert.equal(withClass(t1, "chip").filter((b) => b.classes.has("on")).length, 0);

  // a live event rebuilding turn 1 keeps its chip and its opened step
  chip("all").fire("click");
  const v = fixture();
  page.applyEvent({ ...v, full: false, order: [1, 2, 3], turns: [{ ...v.turns[0], hash: "h1b" }] });
  const again = byId(page.el, "turn-1");
  assert.notEqual(again, t1);
  assert.ok(shown(byId(again, "cp-1-all")));
  assert.equal(withClass(again, "chip").find((b) => b.dataset.chip === "all").attrs["aria-expanded"], "true");
  assert.equal(byId(again, "steps-e3").hidden, false);
  assert.equal(byId(again, "steps-e4").hidden, true);
});

// A hooks-only turn has no model-calls chip; its chips open what the hooks saw.
test("hooksOnlyTurnOpensItsHookTools", () => {
  const { page } = mount();
  const t3 = byId(page.el, "turn-3");
  assert.deepEqual(withClass(t3, "chip").map((b) => b.textContent), ["All", "1 tool call"]);
  withClass(t3, "chip")[1].fire("click");
  assert.deepEqual(withClass(byId(t3, "cp-3-tools"), "frow").map((r) => textOf(r).replace(/\s+/g, " ").trim()), ["hooks ? unmatched Bash ls 0.4s"]);
  withClass(t3, "chip")[0].fire("click");
  assert.match(textOf(byId(t3, "cp-3-all")), /The hooks saw: 1 tool call/);
});

// The prompt and the answer render as Markdown by default, with the raw text a click away;
// the choice is kept in the browser (017 AC13). Spec 027 C4: one switch above the story sets
// it for every prompt and answer; the cards have no switch of their own.
test("promptAndAnswerSwitchBetweenPreviewAndRaw", () => {
  const store = new Map();
  globalThis.localStorage = { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => store.set(k, v) };
  try {
    const v = fixture();
    v.turns[0].answer = "**Done.**\n\n- one\n- two";
    const { page } = mount(v);
    const answer = withClass(byId(page.el, "turn-1"), "answer")[0];
    const md = withClass(answer, "md")[0];
    assert.ok(md, "Preview by default");
    assert.equal(textOf(byTag(md, "strong")[0]).trim(), "Done.");
    assert.equal(byTag(md, "li").length, 2);
    assert.equal(withClass(answer, "seg").length, 0, "no switch on the card");
    const bar = withClass(page.el, "story-bar")[0];
    const raw = withClass(bar, "seg").find((b) => b.dataset.mode === "raw");
    raw.fire("click");
    assert.equal(raw.attrs["aria-pressed"], "true");
    assert.equal(withClass(answer, "raw")[0].textContent, "**Done.**\n\n- one\n- two", "Raw is the stored text as is");
    assert.equal(withClass(answer, "md").length, 0);
    const prompt = withClass(byId(page.el, "turn-2"), "prompt")[0];
    assert.ok(withClass(prompt, "raw")[0], "every prompt and answer follows the one switch");
    assert.equal(store.get("gw.textMode"), "raw", "the choice is kept");
    const later = mount(v).page;
    assert.ok(withClass(withClass(byId(later.el, "turn-1"), "answer")[0], "raw")[0], "a new page starts in the kept mode");
  } finally {
    delete globalThis.localStorage;
  }
});

// A long prompt is shown whole, with no fold (017 AC18 as amended 2026-11-13).
test("longPromptIsShownWhole", () => {
  const v = fixture();
  v.turns[0].prompt = Array.from({ length: 30 }, (_, i) => `line ${i}`).join("\n");
  const { page } = mount(v);
  const prompt = withClass(byId(page.el, "turn-1"), "prompt")[0];
  assert.ok(!withClass(prompt, "tx-b")[0].classes.has("clamp"));
  assert.equal(withClass(prompt, "more").length, 0);
  assert.match(textOf(prompt), /line 29/);
});

// The Sites tab is each host's name alone, like the Files tab (017 AC17).
test("sitesTabListsHostsOnly", () => {
  const { page } = mount();
  const rows = withClass(byId(page.el, "tp-sites"), "row").map((r) => textOf(r).replace(/\s+/g, " ").trim());
  assert.deepEqual(rows, ["telemetry.example"]);
});

// "Show in turn" opens the folds around the row it shows.
test("showInTurnOpensTheFoldAroundTheRow", async () => {
  const { page } = mount();
  assert.equal(byId(page.el, "steps-e4").hidden, true);
  page.dialog.open("file:/w/src/a.js");
  await settle();
  withClass(page.dialog.el, "primary")[0].fire("click");
  assert.equal(byId(page.el, "steps-e4").hidden, false, "the step holding the change opened");
  assert.ok(shown(byId(page.el, "eff-1")));
});

// Each turn names the models its calls used, from the view (AC4 as amended).
test("turnHeaderNamesItsModels", () => {
  const { page } = mount();
  // spec 027 C4: one chip per model under the turn's header; a turn the hooks alone saw says
  // none was captured; a turn with neither has no model row
  const head = withClass(byId(page.el, "turn-2"), "th")[0];
  assert.deepEqual(withClass(head, "mdl").map((m) => m.textContent), ["m1", "m2"]);
  assert.equal(withClass(withClass(byId(page.el, "turn-1"), "th")[0], "mdl").length, 0, "no models, no tag");
  assert.equal(withClass(withClass(byId(page.el, "turn-1"), "th")[0], "mdls").length, 0, "no model row");
  const hooks = withClass(withClass(byId(page.el, "turn-3"), "th")[0], "mdl");
  assert.deepEqual(hooks.map((m) => [m.textContent, m.className]), [["none captured", "mdl none"]]);
});

// The Context chart is one bar per turn: its height the context at the turn's last call,
// a darker cap what the turn added; a bar shows the turn's calls in the list, and labels
// thin out past 24 turns (Karthik 2026-11-13).
test("contextChartIsOneBarPerTurn", () => {
  const v = fixture();
  v.context.main = [
    { n: 3, exchange_id: "e3", turn: 1, ctx: 100, delta: null, why: "first call" },
    { n: 4, exchange_id: "e4", turn: 1, ctx: 300, delta: 200, why: "" },
    { n: 6, exchange_id: "e6", turn: 2, ctx: 600, delta: 300, why: "" },
  ];
  const { page } = mount(v);
  const bars = withClass(page.el, "tbar");
  assert.deepEqual(bars.map((b) => b.style.transform), ["scaleY(0.500)", "scaleY(1.000)"], "one bar per turn, at its last call");
  assert.equal(withClass(bars[0], "cap")[0].style.flexGrow, "200", "turn 1 added 200 of its 300");
  assert.equal(withClass(bars[0], "rest")[0].style.flexGrow, "100");
  assert.equal(withClass(bars[1], "cap").length, 0, "a one-call turn has no cap");
  assert.match(bars[0].attrs["aria-label"], /Turn 1: 300 tokens at its end, \+200 inside the turn/);
  assert.deepEqual(withClass(page.el, "tl2").map((l) => l.textContent), ["T1", "T2"]);
  const scrolled = [];
  const rows = byTag(byId(page.el, "tp-context"), "li").filter((li) => li.dataset.turn);
  for (const li of rows) li.scrollIntoView = () => scrolled.push(li.dataset.turn);
  bars[1].fire("click");
  assert.deepEqual(scrolled, ["2"], "a bar shows its turn's calls in the list");

  v.context.main = Array.from({ length: 50 }, (_, i) => ({ n: i + 1, exchange_id: `x${i}`, turn: i + 1, ctx: 100 + i, delta: 1, why: "" }));
  const many = withClass(mount(v).page.el, "tl2").map((l) => l.textContent).filter(Boolean);
  assert.ok(many.length <= 26, `labels thinned: ${many.length}`);
  assert.equal(many[0], "T1");
  assert.equal(many.at(-1), "T50");
});

// A tool that asked the user questions shows them in its dialog with the picked option
// marked and the notes, before the raw result and input (2026-11-13).
test("toolDialogShowsQuestionsAndAnswers", async () => {
  const det = { ...toolDetails, questions: [
    { header: "Layout", text: "Which layout?", options: [{ label: "Cards", description: "one card each" }, { label: "Table" }], answer: "Table", notes: "keep it short" },
    { header: "Loose", text: "Where?", options: [{ label: "A" }, { label: "B" }], answer: "", notes: "like the stepper" },
  ] };
  const { page } = mount(fixture(), { loadTool: async () => det });
  withClass(byId(page.el, "tool-t1"), "nm")[0].fire("click");
  await settle();
  const dlg = page.dialog.el;
  const qa = withClass(dlg, "qa")[0];
  assert.ok(qa, "a Questions section");
  const items = qa.children;
  assert.equal(textOf(items[0].children[0]).trim(), "Layout");
  const opts = withClass(items[0], "qa-opts")[0].children;
  assert.deepEqual(opts.map((o) => o.className), ["", "on"], "the picked option is marked");
  assert.match(textOf(items[0]), /Notes: keep it short/);
  assert.match(textOf(items[1]), /No option picked\..*Notes: like the stepper/s);
});

// 017 AC5, AC15 (T018): every place a model call is named opens the call details dialog
// through the call's URL, noting the link it was opened from: a step's #n, a chip row, a
// Context row, a tool dialog's "Asked by", and a "needs a look" target.
test("everyCallLinkOpensTheDialog", async () => {
  const noted = [];
  const { page, opened } = mount(fixture(), { noteOpener: (node) => noted.push(node) });
  const href = (id) => `#/sessions/s1/call/${id}`;
  const t1 = byId(page.el, "turn-1");

  // a step's #n
  const chip = (id) => withClass(t1, "chip").find((b) => b.dataset.chip === id);
  chip("all").fire("click");
  const step = withClass(byId(t1, "cp-1-all"), "call-link")[0];
  assert.equal(step.href, href("e3"));
  step.fire("click");
  assert.equal(noted.at(-1), step);

  // a chip row's #n (AC15)
  chip("calls").fire("click");
  const chipRow = withClass(byId(t1, "cp-1-calls"), "call-link")[1];
  assert.equal(chipRow.href, href("e4"));
  chipRow.fire("click");
  assert.equal(noted.at(-1), chipRow);

  // a Context row (a Context bar keeps AC20 and opens no call)
  const ctxRows = byTag(byId(page.el, "tp-context"), "a").filter((a) => a.href.startsWith("#/sessions/"));
  assert.deepEqual(ctxRows.map((a) => a.href), [href("e3"), href("e6")]);
  ctxRows[1].fire("click");
  assert.equal(noted.at(-1), ctxRows[1]);
  assert.equal(byTag(withClass(page.el, "bars")[0], "a").length, 0, "a bar is not a call link");

  // a tool dialog's "Asked by"
  withClass(byId(page.el, "tool-t1"), "nm")[0].fire("click");
  await settle();
  const asked = byTag(page.dialog.el, "a").find((a) => /model call #3/.test(a.textContent));
  assert.equal(asked.href, href("e3"));

  // a target naming a call (needs a look, AC2)
  const from = globalThis.document.createElement("button");
  page.open({ kind: "call", id: "e5" }, from);
  assert.deepEqual(opened, ["e5"]);
});

// 018 AC9: a background task's turn says so in its header, in the turn list and on its
// text, and the turn list counts it as no prompt.
test("backgroundTurnIsMarkedAndNotCountedAsAPrompt", () => {
  const v = fixture();
  v.turns = [turn(1), turn(2, { background: true, prompt: "<task-notification>done</task-notification>" })];
  const { page } = mount(v);
  const t2 = byId(page.el, "turn-2");
  assert.equal(withClass(withClass(t2, "th")[0], "bg")[0].textContent, "background task");
  assert.equal(withClass(t2, "who")[0].textContent, "Background task");
  assert.equal(withClass(byId(page.el, "turn-1"), "who")[0].textContent, "Prompt");
  assert.equal(withClass(withClass(byId(page.el, "turn-1"), "th")[0], "bg").length, 0, "a typed prompt has no tag");
  assert.ok(withClass(page.el, "toc-item").some((e) => textOf(e).includes("background task")));
  assert.equal(withClass(page.el, "sub").find((e) => /prompt/.test(e.textContent)).textContent, "1 prompt");
});

// spec 027 C4: a prompt and an answer each have a copy icon, last in their header row, that
// copies the text as it was stored, says so for a moment, and says when the browser refuses.
test("promptAndAnswerCopyTheirText", async () => {
  const { page } = mount();
  const copied = [];
  const realNav = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: { clipboard: { writeText: async (t) => { copied.push(t); } } } });
  try {
    const turn = byId(page.el, "turn-1");
    const [promptCopy, answerCopy] = withClass(turn, "copy");
    assert.equal(promptCopy.textContent, "", "an icon, no word");
    assert.equal(promptCopy.attrs["aria-label"], "Copy");
    assert.equal(promptCopy.parentNode.children.at(-1), promptCopy, "the icon ends its header row");
    promptCopy.fire("click");
    await settle();
    assert.deepEqual(copied, ["prompt 1"]);
    assert.equal(promptCopy.attrs["aria-label"], "Copied");
    assert.equal(promptCopy.dataset.state, "copied");
    answerCopy.fire("click");
    await settle();
    assert.equal(copied[1], "Fixed the retry.");
    Object.defineProperty(globalThis, "navigator", { configurable: true, value: { clipboard: { writeText: async () => { throw new Error("denied"); } } } });
    const [again] = withClass(byId(page.el, "turn-2"), "copy");
    again.fire("click");
    await settle();
    assert.equal(again.attrs["aria-label"], "Copy failed");
    assert.equal(again.dataset.state, "failed");
  } finally {
    if (realNav) Object.defineProperty(globalThis, "navigator", realNav);
    else delete globalThis.navigator;
  }
});

// spec 027 C4: until the view arrives the page shows loaders, in the story and in the turn
// list, read out as statuses; a note ("building session…") keeps the loader, an error
// replaces it, and the view replaces both.
test("sessionPageShowsLoadersUntilItsView", () => {
  const page = createSessionPage({});
  const loaders = () => withClass(page.el, "loading");
  assert.equal(loaders().length, 2, "one in the story, one in the turn list");
  assert.ok(loaders().every((l) => l.attrs.role === "status"));
  assert.match(textOf(page.el), /Loading the session…/);
  assert.match(textOf(page.el), /Loading turns…/);
  page.setStatus("building session…");
  assert.match(textOf(page.el), /building session…\s+Loading the session…/);
  page.setStatus("boom", "error");
  assert.doesNotMatch(textOf(page.el), /Loading the session…/);
  page.setStatus("");
  page.setView(fixture());
  assert.equal(loaders().length, 0, "the view replaces the loaders");
});
