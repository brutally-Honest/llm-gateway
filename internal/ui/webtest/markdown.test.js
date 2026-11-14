import test from "node:test";
import assert from "node:assert/strict";
import { all, byTag, installDom, textOf } from "./dom-stub.js";

// The Markdown renderer for prompts and answers (spec 017 AC13, E7, E8): known blocks and
// spans become elements, every text goes in as text, and links work only for http(s).

installDom();
const { renderMarkdown } = await import("../web/markdown.js");

const tags = (root) => all(root).slice(1).map((e) => e.tagName).filter((t) => t !== "#text");

test("rendersBlocks", () => {
  const md = renderMarkdown("# Title\n\nSome **bold** and *em* and `code`.\n\n- a\n- b\n  - nested\n\n1. one\n2. two\n\n> quoted\n\n```go\nfunc x() {}\n```\n\n---");
  assert.deepEqual(byTag(md, "h1").map((e) => textOf(e).trim()), ["Title"]);
  assert.equal(textOf(byTag(md, "strong")[0]).trim(), "bold");
  assert.equal(textOf(byTag(md, "em")[0]).trim(), "em");
  assert.equal(byTag(md, "code")[0].textContent, "code");
  assert.equal(byTag(md, "ul").length, 2, "a nested list sits inside its item");
  assert.equal(byTag(md, "ol")[0].children.length, 2);
  assert.match(textOf(byTag(md, "blockquote")[0]), /quoted/);
  const pre = byTag(md, "pre")[0];
  assert.equal(pre.children[0].textContent, "func x() {}");
  assert.equal(pre.children[0].dataset.lang, "go");
  assert.equal(byTag(md, "hr").length, 1);
});

test("rendersTables", () => {
  const md = renderMarkdown("| a | b |\n|---|--:|\n| 1 | 2 |\n| 3 | 4 |");
  assert.deepEqual(byTag(md, "th").map((e) => textOf(e).trim()), ["a", "b"]);
  assert.equal(byTag(md, "td").length, 4);
  assert.equal(byTag(md, "td")[1].style.textAlign, "right");
});

test("htmlIsTextNeverMarkup", () => {
  const md = renderMarkdown('<script>alert(1)</script>\n\n<img src=x onerror=alert(1)> **<b>hi</b>**');
  assert.ok(!tags(md).some((t) => ["script", "img", "b"].includes(t)), `tags: ${tags(md)}`);
  assert.match(textOf(md), /<script>alert\(1\)<\/script>/);
  assert.match(textOf(md), /<img src=x onerror=alert\(1\)>/);
});

test("linksOnlyForHttp", () => {
  const md = renderMarkdown("[ok](https://example.com/a) [bad](javascript:alert(1)) [data](data:text/html,x)");
  const links = byTag(md, "a");
  assert.equal(links.length, 1);
  assert.equal(links[0].href, "https://example.com/a");
  assert.equal(links[0].attrs.rel, "noopener noreferrer");
  assert.match(textOf(md), /bad/, "an unsafe link keeps its text");
});

test("unknownMarkupStaysText", () => {
  const text = "a * b * c, 2*3, snake_case_name, **unclosed, `unclosed, [no link]";
  assert.equal(textOf(renderMarkdown(text)).replace(/\s+/g, " ").trim(), text);
});
