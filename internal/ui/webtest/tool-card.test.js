import test from "node:test";
import assert from "node:assert/strict";
import { pendingBadgeShown } from "../web/entries.js";

// Minimal DOM stubs: just enough for ToolCard.render to run under node. Elements keep
// their children, class names, text and listeners so the tests can walk the tree.
class StubElement {
  constructor() {
    this.className = "";
    this.textContent = "";
    this.children = [];
    this.classes = new Set();
    this.classList = { add: (c) => this.classes.add(c), contains: (c) => this.classes.has(c) };
    this.listeners = {};
    this.open = false;
  }
  append(...nodes) {
    this.children.push(...nodes);
  }
  replaceChildren(...nodes) {
    this.children = [...nodes];
  }
  addEventListener(type, fn) {
    (this.listeners[type] ||= []).push(fn);
  }
}

globalThis.HTMLElement = StubElement;
globalThis.customElements = { define() {} };
globalThis.document = {
  createElement(tag) {
    const el = new StubElement();
    el.tagName = tag;
    return el;
  },
};

const { ToolCard } = await import("../web/tool-card.js");

function render(data) {
  const card = new ToolCard();
  card.render({ prettyInput: "{}", isOpen: () => false, setOpen() {}, ...data });
  return card;
}

// all returns every element under root, root included.
function all(root) {
  return [root, ...root.children.flatMap(all)];
}

// withClass finds the elements under root that carry the class name.
function withClass(root, cls) {
  return all(root).filter((e) => e.className === cls || e.classes.has(cls));
}

// section finds the <details> under the card whose summary has the label.
function section(card, label) {
  return card.children.find((c) => c.tagName === "details" && c.children.find((s) => s.tagName === "summary" && s.textContent === label));
}

test("the pending badge follows pendingBadgeShown's answer", () => {
  const cases = [
    [{ id: "c", name: "Bash" }, null],
    [{ id: "c", name: "Bash", state: "pending" }, null],
    [{ id: "c", name: "Bash" }, { id: "r", text: "ok" }],
  ];
  for (const [call, result] of cases) {
    const shown = withClass(render({ call, result }), "badge pending").length > 0;
    assert.equal(shown, pendingBadgeShown(call, result), JSON.stringify({ call, result }));
  }
  assert.equal(withClass(render({ call: { id: "c", name: "Bash" }, result: null }), "badge pending").length, 1);
});

test("the omitted-bytes note is appended to the hook section with its text", () => {
  const call = { id: "c", name: "Bash", input_differs: true, hook_input: "{}", hook_omitted_bytes: 42 };
  const hook = section(render({ call, result: null }), "hook input");
  const notes = withClass(hook, "muted");
  assert.equal(notes.length, 1);
  assert.equal(notes[0].textContent, "… 42 bytes omitted");
});

test("the hook section takes its truncation from the hook input, not the call input", () => {
  const notes = (call) => withClass(section(render({ call, result: null }), "hook input"), "muted");
  // the call input is cut short but the hook input is whole: no note
  assert.equal(notes({ id: "c", name: "Bash", input_differs: true, truncated: true, hook_omitted_bytes: 0 }).length, 0);
  // the hook input is cut short and the call input is whole: the note shows
  assert.equal(notes({ id: "c", name: "Bash", input_differs: true, truncated: false, hook_omitted_bytes: 5 }).length, 1);
});

test("the hook section has no raw link, while the input section keeps its own", () => {
  const card = render({
    call: { id: "c", name: "Bash", exchange_id: "ex", input_differs: true, hook_input: "{}", hook_omitted_bytes: 5, truncated: true },
    result: null,
  });
  assert.equal(withClass(section(card, "hook input"), "truncated").length, 0);
  assert.equal(withClass(section(card, "input"), "truncated").length, 1);
});
