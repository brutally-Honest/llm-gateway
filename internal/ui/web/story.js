import { el } from "./fmt.js";

// The story's turn cards (spec 016 AC4–AC8). T009 fills them; until then a turn shows
// its heading only.
export function turnNode(t) {
  const box = el("article", "card turn");
  box.id = `turn-${t.n}`;
  box.append(el("h2", "", t.n ? `Turn ${t.n}: ${t.prompt}` : t.prompt));
  return box;
}

export function betweenNode(b) {
  const box = el("section", "card btwentry");
  box.id = `between-${b.after}`;
  box.append(el("h2", "", b.text));
  return box;
}
