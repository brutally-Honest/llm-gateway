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
