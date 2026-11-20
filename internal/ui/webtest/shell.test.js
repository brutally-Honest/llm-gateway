import test from "node:test";
import assert from "node:assert/strict";
import { breadcrumb } from "../web/shell.js";

test("breadcrumbStartsAtSessions", () => {
  assert.deepEqual(breadcrumb({ name: "sessions" }, ""), [{ text: "Sessions" }]);
  for (const route of [{ name: "ungrouped" }, { name: "exchange", id: "e" }, { name: "session", id: "s", call: null }]) {
    const b = breadcrumb(route, "t");
    assert.deepEqual(b[0], { text: "Sessions", href: "#/" });
  }
});

test("breadcrumbShowsSessionTitle", () => {
  const b = breadcrumb({ name: "session", id: "s1", call: null }, "Fix the bug");
  assert.deepEqual(b, [{ text: "Sessions", href: "#/" }, { text: "Fix the bug" }]);
  assert.equal(breadcrumb({ name: "session", id: "s1", call: null }, "")[1].text, "s1");
});

// The key bar ends with a Help link to the install guide on GitHub, opened in a new tab
// (spec 023 AC29); a level with no bar has no link either.
test("keyBarLinksHelp", async () => {
  const { installDom, StubElement, byTag } = await import("./dom-stub.js");
  installDom();
  Object.assign(document, {
    createDocumentFragment: () => new StubElement("fragment"),
    createTextNode: (text) => Object.assign(new StubElement("text"), { textContent: text }),
  });
  const { mountShell } = await import("../web/shell.js");
  const { HELP_URL } = await import("../web/keys.js");
  const bar = new StubElement("footer");
  const shell = mountShell({ head: new StubElement("header"), bar, storage: null, root: null, theme: "dark" });
  shell.setKeyBar({ name: "sessions" }, { index: 1, total: 2 });
  const links = byTag(bar, "a");
  assert.equal(links.length, 1);
  const help = links[0];
  assert.equal(help.textContent, "Help");
  assert.equal(help.href, HELP_URL);
  assert.equal(HELP_URL, "https://github.com/brutally-Honest/llm-gateway/blob/main/docs/install.md");
  assert.equal(help.target, "_blank");
  assert.equal(help.rel, "noopener noreferrer");
  assert.equal(bar.children.at(-1).children.at(-1), help, "Help is the bar's last item");
  shell.setKeyBar({ name: "session", id: "s", call: null }, null);
  assert.equal(byTag(bar, "a").length, 0);
});
