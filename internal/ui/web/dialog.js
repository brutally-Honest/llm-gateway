import { el } from "./fmt.js";

// The one details dialog (spec 016 AC11). T011 fills it.
export function createDialog() {
  const root = el("dialog", "details");
  return { el: root, open() {}, update() {}, isOpen: () => false, close() {} };
}
