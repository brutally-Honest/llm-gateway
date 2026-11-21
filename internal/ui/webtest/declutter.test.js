import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { StubElement, all, byTag, installDom, textOf, withClass } from "./dom-stub.js";

// Spec 027 C7a: a batch of small declutter items in the shell, the session page, the call
// panel, the full exchange page and the ungrouped list. Each test pins one item's new
// behaviour.

installDom();
Object.assign(document, {
  createDocumentFragment: () => new StubElement("fragment"),
  createTextNode: (text) => Object.assign(new StubElement("text"), { textContent: text }),
});
const { mountShell } = await import("../web/shell.js");
const { handleKey } = await import("../web/keys.js");
const { createSessionPage } = await import("../web/session-page.js");
const { createCallDialog } = await import("../web/call-dialog.js");
const { createExchangePage } = await import("../web/exchange-detail.js");
const { createUngroupedPage } = await import("../web/ungrouped-list.js");

const css = readFileSync(new URL("../web/style.css", import.meta.url), "utf8");
const html = readFileSync(new URL("../web/index.html", import.meta.url), "utf8");
const tick = () => new Promise((r) => setTimeout(r, 0));

function shell() {
  const head = new StubElement("header");
  const m = mountShell({ head, storage: null, root: new StubElement("html"), theme: "dark", look: "lean" });
  return { head, m };
}
const find = (root, pred) => all(root).find(pred);

// item 1: the brand is a logo-only home link
test("brandIsALogoOnlyHomeLink", () => {
  const { head } = shell();
  const brand = find(head, (e) => e.className === "brand");
  assert.equal(brand.tagName, "a");
  assert.equal(brand.href, "#/");
  assert.equal(brand.attrs["aria-label"], "LLM Gateway — home");
  assert.equal(brand.title, "Sessions");
  assert.equal(textOf(brand).trim(), "", "no visible text");
  assert.ok(!byTag(head, "h1").length, "no Gateway heading");
  assert.match(css, /:root\[data-style="lean"\] a\.brand \{[^}]*var\(--accent-grad\)/);
  assert.match(css, /:root\[data-style="stark"\] a\.brand \{[^}]*border-radius: 0/);
});

// item 3: the sessions list shows no crumb, a deeper page keeps "Sessions /"
test("sessionsListHasNoBreadcrumbAndDeeperPagesKeepTheWayBack", () => {
  const { head, m } = shell();
  const nav = find(head, (e) => e.tagName === "nav");
  m.setRoute({ name: "sessions" }, "");
  assert.equal(textOf(nav).trim(), "");
  m.setRoute({ name: "session", id: "s" }, "Title");
  assert.equal(textOf(nav).replace(/\s+/g, " ").trim(), "Sessions /");
});

// item 2: the live dot
test("liveIndicatorIsADotWithItsWordAsNameAndTitle", () => {
  const { head, m } = shell();
  const dot = find(head, (e) => e.attrs.role === "status");
  assert.equal(dot.hidden, true, "no stream, nothing shown");
  m.setLive("live", "ok");
  assert.equal(dot.hidden, false);
  assert.equal(dot.className, "live-dot live-ok");
  assert.equal(dot.title, "live");
  assert.equal(textOf(dot).trim(), "live", "the word stays as hidden text");
  assert.ok(withClass(dot, "sr-only").length);
  m.setLive("live: disconnected, retrying", "warn");
  assert.equal(dot.className, "live-dot live-warn");
  m.setLive("");
  assert.equal(dot.hidden, true);
  // the colours are checked in liveDotIsGreenOrRedAndRoundInBothStyles
});

// item 5: Settings is the gear alone, named for assistive tech
test("settingsButtonIsTheGearWithAnAccessibleName", () => {
  const { head } = shell();
  const b = find(head, (e) => e.className === "settings-open");
  assert.equal(b.textContent, "⚙");
  assert.equal(b.attrs["aria-label"], "Settings");
  assert.equal(b.title, "Settings");
});

// item 6: no key bar and no header button; the "?" key opens the page's shortcuts
test("shortcutsLiveInADialogOpenedByTheQuestionMark", () => {
  assert.doesNotMatch(html, /keybar/);
  assert.doesNotMatch(css, /keybar/);
  const { head, m } = shell();
  assert.equal(withClass(head, "help-open").length, 0, "no ? button in the header");
  assert.ok(!find(head, (e) => e.tagName === "button" && e.textContent === "?"));
  const dlg = find(head, (e) => e.tagName === "dialog" && e.className.includes("help"));
  m.setKeyBar({ name: "sessions" }, { index: 2, total: 5 });
  dlg.showModal = () => { dlg.open = true; };
  dlg.close = () => { dlg.open = false; };
  m.openHelp();
  assert.equal(dlg.open, true);
  assert.equal(m.modalOpen(), true);
  const rows = withClass(dlg, "shortcut").map((r) => textOf(r).replace(/\s+/g, " ").trim());
  assert.deepEqual(rows, ["↑/↓ select", "Enter open", "/ filter"]);
  assert.match(textOf(dlg), /session 2\/5/);
  const scrim = withClass(dlg, "call-scrim")[0];
  assert.equal(scrim.tagName, "button", "a click outside lands on a real button");
  scrim.fire("click");
  assert.equal(dlg.open, false, "a click outside closes it");
  // another page's shortcuts replace them
  m.setKeyBar({ name: "ungrouped" }, null);
  m.openHelp();
  assert.deepEqual(withClass(dlg, "shortcut").map((r) => textOf(r).replace(/\s+/g, " ").trim()), ["Backspace Sessions"]);
  dlg.open = false;
  m.setKeyBar({ name: "session", id: "s", call: null }, null);
  m.openHelp();
  assert.equal(dlg.open, false, "no shortcuts, nothing to open");
});

// the stream's dot is green while connected and red otherwise, in both styles, and round
test("liveDotIsGreenOrRedAndRoundInBothStyles", () => {
  const block = (sel) => css.split(sel)[1].split("}")[0];
  for (const theme of [':root, :root[data-theme="dark"] {', ':root[data-theme="light"] {']) {
    assert.match(block(theme), /--live-on: #[0-9a-f]{6};/);
    assert.match(block(theme), /--live-off: #[0-9a-f]{6};/);
  }
  assert.doesNotMatch(css, /data-style="(stark|lean)"\][^{]*\{[^}]*--live-(on|off)/, "no style overrides the dot's colours");
  const dot = css.match(/\.live-dot \{([^}]*)\}/)[1];
  assert.match(dot, /border-radius: 50%/);
  assert.match(dot, /background: var\(--live-on\)/);
  assert.match(css, /\.live-dot\.live-warn, \.live-dot\.live-bad \{[^}]*background: var\(--live-off\)/);
});

// no underline on header links, at rest or on hover
test("headerLinksAreNeverUnderlined", () => {
  assert.match(css, /header#shell-head a, header#shell-head a:hover \{ text-decoration: none; \}/);
});

test("questionMarkKeyAsksForHelpOnEveryPageButNotInAnInput", () => {
  for (const route of [{ name: "sessions" }, { name: "session", id: "s", call: null }, { name: "ungrouped" }]) {
    assert.deepEqual(handleKey(route, { key: "?" }), { type: "help" });
  }
  assert.equal(handleKey({ name: "sessions" }, { key: "?", inInput: true }), null);
  assert.equal(handleKey({ name: "sessions" }, { key: "?", ctrlKey: true }), null);
});

// ---- session page ----
const step = (n) => ({ n, exchange_id: `e${n}`, at: "2026-01-01T00:00:01Z", dur_ms: 1000, did: "wrote", tools: [] });
const turn = (n, o = {}) => ({ n, hash: `h${n}`, prompt: `prompt ${n}`, reminders: [], at: "2026-01-01T00:00:00Z", clock_ms: 1000, counts: { calls: 2, tools: 1 }, steps: [step(n)], hook_tools: [], loose: [], models: ["m1"], ...o });
const view = (o = {}, ov = {}) => ({
  version: "v1",
  overview: { title: "T", status: { glyph: "■", word: "ended" }, client: "c", key: "k", folder: "proj", models: ["m1"], time: {}, files: {}, commands: {}, sites: {}, context: { last: 5 }, needs_look: [], tokens: {}, ...ov },
  side_calls: [], between: [], turns: [turn(1)],
  files: { changed: [], read: [], searches: [], coverage: "" }, commands: { items: [], coverage: "" }, sites: { items: [], coverage: "" }, context: { main: [], subs: [], coverage: "" },
  ...o,
});
const mount = (v) => {
  const page = createSessionPage({ callHref: (id) => `#/c/${id}`, loadTool: async () => ({}), loadDiff: async () => ({}), loadContent: async () => ({}), openCall() {} });
  globalThis.document.getElementById = (id) => all(page.el).find((e) => e.id === id) || null;
  page.setView(v);
  return page;
};

test("summaryBadgeShowsOnlyWhileActive", () => {
  assert.equal(withClass(mount(view()).el, "status-pill").length, 0, "ended: no badge");
  const live = withClass(mount(view({}, { status: { glyph: "●", word: "active" } })).el, "status-pill");
  assert.equal(live.length, 1);
  assert.match(live[0].textContent, /active/);
});

test("summaryMetaNamesModelsOnlyWhenThereAreSeveral", () => {
  const meta = (v) => textOf(withClass(mount(v).el, "meta")[0]);
  const one = meta(view());
  assert.doesNotMatch(one, /m1/);
  assert.match(one, /proj/, "the path stays");
  assert.match(one, /c/, "the client stays");
  assert.match(meta(view({}, { models: ["m1", "m2"] })), /m1, m2/);
});

test("contextCardHasNoArrowButStillOpensTheContextTab", () => {
  const page = mount(view());
  const kpi = byTag(page.el, "button").find((b) => b.className.includes("kpi") && /Context/.test(textOf(b)));
  assert.ok(kpi, "the Context card is still a button");
  assert.doesNotMatch(textOf(kpi), /→/);
});

test("summaryFootHidesAnUnknownCostAndShowsSideCallsAsPlainText", () => {
  const none = mount(view({ side_calls: [{ n: 1, exchange_id: "q", name: "probe", at: "2026-01-01T00:00:00Z", dur_ms: 5, status: "ok" }] }));
  assert.equal(withClass(none.el, "cost").length, 0, "no cost, no line");
  assert.match(textOf(withClass(none.el, "foot")[0]), /1 side call/);
  assert.doesNotMatch(css, /details\.side summary \{[^}]*(border|padding)/, "not styled as a chip");
  const bare = mount(view());
  assert.equal(withClass(bare.el, "foot").length, 0, "nothing at the foot at all");
  const priced = mount(view({}, { tokens: { cost: { priced: 1, usd: 1.5, source: "table" } } }));
  assert.match(textOf(withClass(priced.el, "cost")[0]), /cost: \$1\.50|cost: \$/);
});

test("turnModelChipsHaveNoModelLabel", () => {
  const page = mount(view());
  const mdls = withClass(page.el, "mdls")[0];
  assert.deepEqual(withClass(mdls, "mdl").map((m) => m.textContent), ["m1"]);
  assert.equal(withClass(mdls, "l").length, 0);
  assert.doesNotMatch(textOf(mdls), /Model/);
});

// item 21 was withdrawn: "All" is not a default view but the chip that opens the turn's whole
// stepper, so it stays visible whatever is selected.
test("allChipStaysAsTheWayIntoTheWholeStepper", () => {
  const page = mount(view());
  const chips = withClass(page.el, "chip");
  const all_ = chips.find((b) => b.dataset.chip === "all");
  const calls = chips.find((b) => /model call/.test(b.textContent));
  assert.ok(all_ && calls);
  assert.ok(!all_.hidden, "nothing selected: All opens the whole stepper");
  calls.fire("click");
  assert.ok(!all_.hidden);
  all_.fire("click");
  assert.equal(all_.attrs["aria-expanded"], "true", "All opens it");
});

// ---- call panel ----
function detail(cost) {
  return {
    n: 1, exchange_id: "a", turn: 1,
    facts: { model: "m1", dur_ms: 1000, start: "2026-01-01T00:00:01Z", stop: { word: "end turn" }, thread: { kind: "main", label: "main" }, ctx: 160 },
    context: { size: 160, first: true, input: 1, cache_write: 0, cache_read: 0, output: 1, cost },
    saw: { first: true, carried: 0, new: 1, messages: [{ role: "user", text: "hi", new: true }] }, thinking: false,
    response: { text: "ok", stop_reason: "end turn" }, tools: [], health: { status: "200 OK", ok: true, items: [] },
    raw: { request: "/api/exchanges/a/body?part=request", response: "/api/exchanges/a/body?part=response", headers: "#/exchanges/a", page: "#/exchanges/a" },
    nav: { index: 1, total: 1, prev: "", next: "" },
  };
}
async function callText(cost) {
  const d = createCallDialog({ nav: { push() {}, replace() {}, back() {} }, loadCall: async () => detail(cost) });
  d.sync({ sessionId: "s", callId: "a" });
  await tick();
  return { d, text: textOf(d.el) };
}

test("callTopLineHidesAnUnknownCostAndRawHasNoNote", async () => {
  const unknown = await callText({ status: "no_usage", reason: "cut" });
  assert.doesNotMatch(unknown.text, /API-eq/);
  assert.doesNotMatch(unknown.text, /unparsed/);
  assert.match(unknown.text, /Raw/);
  const known = await callText({ status: "priced", usd: 0.5, source: "table" });
  assert.match(known.text, /API-eq\. cost/);
});

// ---- full exchange page ----
const base = { id: "x", method: "POST", path: "/v1/m", protocol: "p", client: "c", model: "m", stream: false, parse: "ok", status: 200, status_word: "200 OK", started_at: "2026-01-01T00:00:00Z", ended_at: "2026-01-01T00:00:01Z", ttfb_ms: 1, duration_ms: 2, request_body_stored: true, response_body_stored: true, request_headers: { A: ["1"], B: ["2"] }, response_headers: { C: ["3"] }, usage: null };
const facts = (x) => {
  const pg = createExchangePage();
  pg.render({ exchange: { ...base, ...x } });
  return { pg, labels: withClass(pg.el, "mfact").map((f) => withClass(f, "l")[0].textContent) };
};

test("exchangeCaptureShowsOnlyWhenSomethingIsWrong", () => {
  const ok = facts({});
  assert.match(textOf(ok.pg.el), /Captured in full/);
  assert.ok(!byTag(ok.pg.el, "h2").some((h) => h.textContent === "Capture"));
  assert.ok(!ok.labels.some((l) => /truncated|incomplete|disconnected|aborted|Gateway error/.test(l)));
  for (const [k, v, label] of [["request_truncated", true, "Request truncated"], ["response_truncated", true, "Response truncated"], ["request_incomplete", true, "Request incomplete"], ["client_disconnected", true, "Client disconnected"], ["upstream_aborted", true, "Upstream aborted"], ["gateway_error", "boom", "Gateway error"]]) {
    const bad = facts({ [k]: v });
    assert.doesNotMatch(textOf(bad.pg.el), /Captured in full/, k);
    assert.ok(bad.labels.includes(label), k);
    assert.ok(byTag(bad.pg.el, "h2").some((h) => h.textContent === "Capture"), k);
  }
});

test("exchangeSummaryTilesShowStreamAndParseOnlyWhenNotable", () => {
  const plain = facts({});
  assert.ok(!plain.labels.includes("Stream") && !plain.labels.includes("Parse"));
  const streamed = facts({ stream: true });
  assert.ok(streamed.labels.includes("Stream"));
  assert.ok(!streamed.labels.includes("Parse"));
  const bad = facts({ parse: "failed" });
  assert.ok(bad.labels.includes("Parse") && !bad.labels.includes("Stream"));
});

test("exchangeHeadersAreFoldedBehindDetails", () => {
  const { pg } = facts({});
  const ds = byTag(pg.el, "details").filter((d) => d.className === "hdrs");
  assert.deepEqual(ds.map((d) => textOf(byTag(d, "summary")[0])), ["Request headers (2)", "Response headers (1)"]);
  assert.ok(ds.every((d) => !d.open), "closed by default");
  assert.ok(ds.every((d) => byTag(d, "table").length === 1));
});

// ---- ungrouped list ----
test("ungroupedClientColumnIsDroppedWhenEveryClientIsUnknown", () => {
  const item = (id, client) => ({ id, client, models: ["m"], first_at: "2026-01-01T00:00:00Z", errors: 0, title: "t", usage: {} });
  const headsOf = (items) => {
    const pg = createUngroupedPage();
    pg.render({ items });
    return { heads: byTag(pg.el, "th").map((h) => h.textContent), cells: byTag(withClass(pg.el, "sp-list")[0], "tr").slice(1).map((r) => byTag(r, "td").length) };
  };
  const none = headsOf([item("1", "unknown"), item("2", "")]);
  assert.ok(!none.heads.includes("Client"));
  assert.deepEqual(none.cells, [5, 5]);
  const some = headsOf([item("1", "unknown"), item("2", "client-a")]);
  assert.ok(some.heads.includes("Client"));
  assert.deepEqual(some.cells, [6, 6]);
});

// the browser tab keeps its name, Gateway, and carries the funnel logo
test("browserTabIsNamedAndCarriesTheLogo", () => {
  assert.match(html, /<title>Gateway<\/title>/);
  assert.match(html, /<link rel="icon" href="favicon\.svg" type="image\/svg\+xml">/);
  const icon = readFileSync(new URL("../web/favicon.svg", import.meta.url), "utf8");
  const mark = readFileSync(new URL("../web/logo.svg", import.meta.url), "utf8");
  // the same funnel as the header mark: its three lanes, gate and exit
  for (const part of ["M3 5l7 5.5M3 12h7M3 19l7-5.5", 'x="10.5" y="8" width="3" height="8"', "M14.5 12h6.5"]) {
    assert.ok(icon.includes(part) && mark.includes(part), `both carry ${part}`);
  }
});

// the call panel's scrim is in the panel's own layer, so the panel repaints its background
// over it: the panel is never dimmed by its own scrim
test("callPanelIsNotDimmedByItsOwnScrim", () => {
  const m = css.match(/dialog\.call-details::after \{([^}]*)\}/);
  assert.ok(m, "the panel repaints its background");
  for (const d of ["content: \"\"", "position: absolute", "inset: 0", "z-index: -1", "background: inherit"]) assert.ok(m[1].includes(d), d);
});

// Lean's input and cache read differ in the context bar (its model colour is its accent)
test("leanContextBarTellsInputFromCacheRead", () => {
  assert.match(css, /:root\[data-style="lean"\] \.ctx-bar \.cr, :root\[data-style="lean"\] \.sw-cr \.l::before \{ background: var\(--muted\); \}/);
});

// the settings and shortcuts dialogs take focus themselves, so no option shows a ring on open
test("dialogsFocusThemselvesOnOpen", () => {
  const { head } = shell();
  const dialogs = byTag(head, "dialog");
  assert.equal(dialogs.length, 2, "settings and shortcuts");
  for (const d of dialogs) assert.equal(d.tabIndex, -1, `${d.className} can take focus`);
  const settings = dialogs.find((d) => d.className === "settings");
  settings.showModal = () => { settings.open = true; };
  let focused = null;
  settings.focus = () => { focused = settings; };
  find(head, (e) => e.className === "settings-open").fire("click");
  assert.equal(settings.open, true);
  assert.equal(focused, settings, "focus lands on the dialog, not its first option");
});
