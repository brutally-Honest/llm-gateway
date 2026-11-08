// Minimal DOM stubs: just enough for the page builders to run under node. Elements keep
// their children, class names, text and listeners so tests can walk the tree.
export class StubElement {
  constructor(tag) {
    this.tagName = tag;
    this.className = "";
    this.textContent = "";
    this.children = [];
    this.attrs = {};
    this.listeners = {};
    this.open = false;
    this.style = {};
    this.dataset = {};
    this.value = "";
    this.classes = new Set();
    this.classList = { add: (c) => this.classes.add(c), contains: (c) => this.classes.has(c), remove: (c) => this.classes.delete(c) };
  }
  append(...nodes) {
    this.children.push(...nodes);
  }
  prepend(...nodes) {
    this.children.unshift(...nodes);
  }
  replaceChildren(...nodes) {
    this.children = [...nodes];
  }
  setAttribute(k, v) {
    this.attrs[k] = String(v);
  }
  removeAttribute(k) {
    delete this.attrs[k];
  }
  addEventListener(type, fn) {
    (this.listeners[type] ||= []).push(fn);
  }
  focus() {
    globalThis.document.activeElement = this;
  }
  fire(type, event = {}) {
    for (const fn of this.listeners[type] || []) fn(event);
  }
}

export function installDom() {
  globalThis.HTMLElement = StubElement;
  globalThis.customElements = { define() {} };
  globalThis.document = {
    activeElement: null,
    createElement: (tag) => new StubElement(tag),
    createElementNS: (ns, tag) => Object.assign(new StubElement(tag), { namespaceURI: ns }),
  };
}

// all returns every element under root, root included.
export function all(root) {
  return [root, ...root.children.flatMap(all)];
}

export const withClass = (root, cls) => all(root).filter((e) => e.className.split(" ").includes(cls) || e.classes.has(cls));
export const byTag = (root, tag) => all(root).filter((e) => e.tagName === tag);

// textOf is the text of the whole subtree, as a person would read it.
export const textOf = (root) => all(root).map((e) => e.textContent).join(" ");
