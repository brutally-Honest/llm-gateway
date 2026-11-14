import test from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";

// In a browser an element's children is an HTMLCollection, not an Array: it has no find,
// some, map or filter. The tests' DOM stub uses arrays, so such a call passes here and
// throws in the browser (the drawer went blank this way, 2026-11-13). Copy it into an
// array first: Array.from(node.children).
test("noArrayMethodsOnDomChildren", () => {
  const web = new URL("../web/", import.meta.url);
  const bad = /\.children\.(find|findIndex|some|every|map|filter|forEach|reduce|flatMap|includes|indexOf|at)\(/;
  for (const f of readdirSync(web).filter((n) => n.endsWith(".js"))) {
    readFileSync(new URL(f, web), "utf8").split("\n").forEach((line, i) => {
      assert.ok(!bad.test(line), `${f}:${i + 1} calls an Array method on .children: ${line.trim()}`);
    });
  }
});
