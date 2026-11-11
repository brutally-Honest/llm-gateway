import { el } from "./fmt.js";

// The right card's Files, Commands, Sites and Context tabs (spec 016 AC9, AC10). T010
// fills them.
export function createSideCard() {
  const root = el("aside", "sidebar card");
  root.setAttribute("aria-label", "What this session touched");
  return { el: root, update() {}, select() {} };
}
