import test from "node:test";
import assert from "node:assert/strict";
import { all, byTag, installDom, textOf, withClass } from "./dom-stub.js";
import { call, item } from "./fixtures.js";

installDom();
const { createTimelinePage } = await import("../web/timeline-page.js");

const summary = { id: "s1", client: "demo-client", models: ["m"], first_at: "2026-01-01T00:00:00Z", last_at: "2026-01-01T00:01:00Z", exchange_count: 2, model_calls: 2, main_errors: 0, active: true, usage: {}, tool_states: {} };
const prompt = (id, text) => item(id, "user_prompt", { role: "user", text });

function page(entries) {
  const p = createTimelinePage();
  p.render({ summary, entries, error: "", snapshotError: "" });
  return p;
}

test("textRenderedAsTextNotHtml", () => {
  const evil = "<img src=x onerror=alert(1)>";
  const p = page([call("a", 1), prompt("a", evil), item("a", "assistant_text", { text: evil })]);
  const pres = byTag(p.el, "pre");
  assert.equal(pres.length, 2);
  for (const pre of pres) assert.equal(pre.textContent, evil);
  assert.equal(all(p.el).filter((e) => e.tagName === "img").length, 0);
  assert.ok(withClass(p.el, "live").length === 1);
});

test("filterNarrowsLoadedTurns", () => {
  const p = page([
    call("a", 1), prompt("a", "alpha task"), item("a", "tool_call", { name: "Bash", tool_call_id: "t1" }),
    call("b", 2, { prev: "a" }), prompt("b", "beta task"), item("b", "tool_call", { name: "Read", tool_call_id: "t2" }),
  ]);
  const heads = () => withClass(p.el, "turn-head").map((h) => h.textContent);
  assert.equal(heads().length, 2);
  const input = byTag(p.el, "input")[0];
  input.value = "beta";
  input.fire("input");
  assert.equal(heads().length, 1);
  assert.match(heads()[0], /^Turn 2/);
  input.value = "";
  const select = byTag(p.el, "select")[0];
  assert.deepEqual(byTag(select, "option").map((o) => o.textContent), ["All tools", "Bash", "Read"]);
  select.value = "Bash";
  select.fire("change");
  assert.equal(heads().length, 1);
  assert.match(heads()[0], /^Turn 1/);
  input.value = "nomatch";
  input.fire("input");
  assert.match(textOf(p.el), /No entries match the filter/);
});

test("hooksStayHiddenUntilToggled", () => {
  const hook = { id: "hook:1", kind: "hook_event", phase: "pre_tool", name: "Bash", tool_call_id: "t1", start: "2026-01-01T00:00:01Z", agent_id: "ag", agent_type: "explore" };
  const p = page([call("a", 1), prompt("a", "go"), item("a", "tool_call", { name: "Bash", tool_call_id: "t1" }), hook]);
  assert.equal(withClass(p.el, "hook-event").length, 0);
  const toggle = byTag(p.el, "button").find((b) => b.textContent === "1 hook");
  toggle.fire("click");
  const lines = withClass(p.el, "hook-event");
  assert.equal(lines.length, 1);
  assert.match(textOf(lines[0]), /agent explore ag/);
});

test("remindersFoldBehindControl", () => {
  const r = "<system-reminder>secret</system-reminder>";
  const p = page([call("a", 1), item("a", "user_prompt", { role: "user", text: `fix ${r}`, body: "fix", reminders: [r] })]);
  assert.doesNotMatch(textOf(p.el).replace(/\+1 reminder/, ""), /secret/);
  byTag(p.el, "button").find((b) => b.textContent === "+1 reminder").fire("click");
  assert.match(textOf(p.el), /secret/);
});

// Esc in the turns filter restores every turn and keeps the current one in view instead of
// jumping back to turn 1 (F4). Chrome's own Esc fires `search`, not `input`.
function threeTurnsFiltered() {
  const p = page([
    call("a", 1), prompt("a", "alpha task"),
    call("b", 2, { prev: "a" }), prompt("b", "beta task"),
    call("c", 3, { prev: "b" }), prompt("c", "gamma task"),
  ]);
  const heads = () => withClass(p.el, "turn-head").map((h) => h.textContent);
  p.moveTurn(2); // turn 3 is current
  const input = byTag(p.el, "input")[0];
  input.value = "beta";
  input.fire("input");
  assert.equal(heads().length, 1);
  const proto = Object.getPrototypeOf(p.el);
  const scrolled = [];
  proto.scrollIntoView = function () {
    scrolled.push(this);
  };
  return { p, input, heads, scrolled, done: () => delete proto.scrollIntoView };
}

test("escInTurnFilterRestoresTurnsAndKeepsCurrentInView", () => {
  const f = threeTurnsFiltered();
  try {
    f.input.fire("keydown", { key: "Escape" });
    assert.equal(f.input.value, "");
    assert.equal(f.heads().length, 3, "every turn shows again");
    const last = f.scrolled.at(-1);
    assert.ok(last, "the current turn is scrolled into view");
    assert.match(withClass(last, "turn-head")[0].textContent, /^Turn 3/);
  } finally {
    f.done();
  }
});

test("chromeSearchEventRestoresTheTurns", () => {
  const f = threeTurnsFiltered();
  try {
    f.input.value = "";
    f.input.fire("search");
    assert.equal(f.heads().length, 3, "every turn shows again");
    assert.match(withClass(f.scrolled.at(-1), "turn-head")[0].textContent, /^Turn 3/);
  } finally {
    f.done();
  }
});

// An error turn's rail item and an errored call's link say so in words, not colour alone,
// as the turn heading does (AC34, 013 manual check F6). The call box already shows a
// visible "✗ error" badge beside the link; the link's own name now says it too.
function erroredPage() {
  return page([call("a", 1), prompt("a", "go"), call("b", 2, { prev: "a", status: 529 })]);
}

test("errorRailItemSaysErrorInWords", () => {
  const p = erroredPage();
  const rail = withClass(p.el, "rail-item");
  assert.equal(rail.length, 1);
  assert.match(textOf(rail[0]), /✗ error/, "the rail item shows the glyph and the word");
});

test("errorCallLinkNameSaysError", () => {
  const p = erroredPage();
  const links = withClass(p.el, "call-link");
  const bad = links.find((a) => a.className.includes("is-error"));
  const good = links.find((a) => !a.className.includes("is-error"));
  assert.match(bad.attrs["aria-label"] || "", /^#2 .*✗ error$/, "the errored link's name keeps its label and says error");
  assert.equal(good.attrs["aria-label"], undefined, "a call without an error keeps its text as its name");
});

// A call link's duration and its context read as separate parts, not "0mscontext 1040"
// (013 manual check, F8): the rendered text of the call box, as a reader sees it.
test("callLinkSeparatesDurationAndContext", () => {
  const usage = { has_usage: true, partial: false, input: 540, output: 5, cache_write: 0, cache_read: 500 };
  const p = page([call("a", 1, { ms: 0, usage }), prompt("a", "go")]);
  const box = withClass(p.el, "call")[0];
  const rendered = all(box).filter((e) => e.children.length === 0).map((e) => e.textContent).join("");
  assert.equal(rendered, "#1 · 0ms · ctx 1,040");
});
