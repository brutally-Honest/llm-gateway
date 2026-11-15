import test from "node:test";
import assert from "node:assert/strict";
import { attributionText } from "../web/effects.js";

// spec 015 AC12: the label as glyph and word, then "#n Name" from the model call that asked
// for the tool, the name alone when no captured call asked, every candidate for several;
// nothing when the server sent no label.
test("attributionText", () => {
  const nos = new Map([["t1", 1], ["t2", 2]]);
  assert.equal(attributionText({ label: "named", tool: { id: "t2", name: "Edit" } }, nos), "● named: #2 Edit");
  assert.equal(attributionText({ label: "only_one", tool: { id: "t9", name: "Bash" } }, nos), "◐ only one running: Bash");
  assert.equal(
    attributionText({ label: "several", candidates: [{ id: "t1", name: "Bash" }, { id: "t3", name: "Bash", agent_id: "a1" }] }, nos),
    "◌ several running: #1 Bash, Bash (agent)",
  );
  assert.equal(attributionText({ label: "outside" }, nos), "○ outside any tool call");
  assert.equal(attributionText({ label: "harness" }, nos), "⚙ harness");
  assert.equal(attributionText({ label: "harness", client: "client-a" }, nos), "⚙ harness: client-a");
  assert.equal(attributionText(undefined, nos), "");
});

// spec 017 AC12: the unassigned connections page sits in the sessions list's column under a
// title with labelled counts; each connection's label and outcome show as glyph and word, a
// failure in the failure colour; unassigned file effects follow in their own card; the
// breadcrumb leads back to Sessions.
test("unassignedPageUsesLabelsAndBreadcrumb", async () => {
  const { installDom, textOf, withClass } = await import("./dom-stub.js");
  installDom();
  const { createUnassignedPage } = await import("../web/effects-list.js");
  const { breadcrumb } = await import("../web/shell.js");
  const items = [
    { id: 1, kind: "net_conn", client: "unknown", at: "2026-01-01T00:00:00Z", host: "example.com", port: 443, via: "http", bytes_up: 0, bytes_down: 32, duration_ms: 850, outcome: "ok", detail: "GET http://example.com/" },
    { id: 2, kind: "net_conn", client: "unknown", at: "2026-01-01T00:01:00Z", host: "harness.example", port: 443, via: "connect", bytes_up: 10, bytes_down: 0, duration_ms: 12, outcome: "error", attribution: { label: "harness", client: "client-a" } },
    { id: 3, kind: "file_change", change: "modified", path: "notes.md", size: 20, at: "2026-01-01T00:02:00Z" },
  ];
  const pg = createUnassignedPage({ isOpen: () => false, setOpen() {}, getJSON: async () => ({}), redraw() {} });
  pg.render({ items });
  assert.equal(pg.el.className, "page unassigned-page", "the sessions list's column");
  const head = textOf(withClass(pg.el, "sp-head")[0]).replace(/\s+/g, " ");
  assert.match(head, /Unassigned connections/);
  assert.match(head, /2 connections/);
  assert.match(head, /1 file/);
  const cells = (cls) => withClass(pg.el, cls).filter((c) => c.tagName === "td").map((c) => textOf(c).trim());
  assert.deepEqual(cells("label"), ["○ unassigned", "⚙ harness: client-a"]);
  assert.deepEqual(cells("status"), ["✓ ok", "! error"]);
  assert.equal(withClass(pg.el, "st-failed").length, 1, "the failure is in the failure colour");
  assert.deepEqual(cells("bytes"), ["↑ 0 B · ↓ 32 B", "↑ 10 B · ↓ 0 B"]);
  assert.match(textOf(withClass(pg.el, "effects-files")[0]).replace(/\s+/g, " "), /~ modified notes\.md 20 B/);
  assert.equal(withClass(pg.el, "sp-list").length, 2, "connections and files each in a card");
  assert.deepEqual(breadcrumb({ name: "unassigned" }).map((p) => [p.text, p.href]), [["Sessions", "#/"], ["Unassigned connections", undefined]]);
  pg.render({ items: [], loading: true });
  assert.match(textOf(pg.el), /Loading…/);
});
