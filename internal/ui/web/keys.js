// Key bar content and the shortcut reducer. Pure: the page feeds it keyboard events.
export const SHORTCUTS = {
  SESSIONS: [["↑/↓", "select"], ["Enter", "open"], ["/", "filter"]],
  TURNS: [
    ["j/k", "turn"], ["e", "next error"], ["t", "next tool"], ["Enter", "open first call"],
    ["h", "hooks"], ["/", "filter"], ["Backspace", "Sessions"],
  ],
  CALL: [["↑/↓", "call"], ["Esc", "close"], ["t", "back to turn"]],
  // #/ungrouped and #/exchanges/{id}: level SESSIONS, no position, one way out (E17)
  BACK: [["Backspace", "Sessions"]],
};

// keyLevel names the level a route is at and which shortcut set works there.
export function keyLevel(route) {
  switch (route.name) {
    case "sessions":
      return { level: "SESSIONS", set: "SESSIONS", noun: "session" };
    case "session":
      return route.call ? { level: "CALL", set: "CALL", noun: "call #" } : { level: "TURNS", set: "TURNS", noun: "turn" };
    default:
      return { level: "SESSIONS", set: "BACK", noun: "" };
  }
}

// keyBar returns { level, position, shortcuts }; pos is { index, total } or null.
export function keyBar(route, pos) {
  const { level, set, noun } = keyLevel(route);
  let position = "";
  if (noun && pos && pos.total > 0) {
    position = noun.endsWith("#") ? `${noun}${pos.index}/${pos.total}` : `${noun} ${pos.index}/${pos.total}`;
  }
  return { level, position, shortcuts: SHORTCUTS[set].map(([key, label]) => ({ key, label })) };
}

// isTextInput says whether an element takes typed text (shortcuts must not fire there).
export function isTextInput(node) {
  if (!node) return false;
  const tag = String(node.tagName || "").toUpperCase();
  if (tag === "TEXTAREA" || tag === "SELECT") return true;
  if (tag === "INPUT") return !["button", "checkbox", "radio", "submit", "reset"].includes(String(node.type || "text").toLowerCase());
  return Boolean(node.isContentEditable);
}

// turnTarget finds the next turn card after index `from` (wrapping) that has an error
// (kind "error") or a tool (kind "tool"); -1 when none does. Pure: the page scrolls.
export function turnTarget(cards, from, kind) {
  const n = cards.length;
  for (let i = 1; i <= n; i++) {
    const idx = (from + i + n) % n;
    const c = cards[idx];
    if (kind === "error" ? c.errored : c.tools.length > 0) return idx;
  }
  return -1;
}

// handleKey maps a keyboard event ({ key, ctrlKey, metaKey, altKey, inInput }) to an action
// or null. In a text input only Esc acts: it leaves the input.
export function handleKey(route, press) {
  if (press.ctrlKey || press.metaKey || press.altKey) return null;
  const k = press.key;
  if (press.inInput) return k === "Escape" ? { type: "leaveInput" } : null;
  const { set } = keyLevel(route);
  if (set === "SESSIONS") {
    if (k === "ArrowUp") return { type: "select", delta: -1 };
    if (k === "ArrowDown") return { type: "select", delta: 1 };
    if (k === "Enter") return { type: "open" };
    if (k === "/") return { type: "filter" };
  } else if (set === "TURNS") {
    if (k === "j") return { type: "turn", delta: 1 };
    if (k === "k") return { type: "turn", delta: -1 };
    if (k === "e") return { type: "nextError" };
    if (k === "t") return { type: "nextTool" };
    if (k === "Enter") return { type: "openFirstCall" };
    if (k === "h") return { type: "hooks" };
    if (k === "/") return { type: "filter" };
    if (k === "Backspace") return { type: "toSessions" };
  } else if (set === "CALL") {
    if (k === "ArrowUp") return { type: "call", delta: -1 };
    if (k === "ArrowDown") return { type: "call", delta: 1 };
    if (k === "Escape") return { type: "close" };
    if (k === "t") return { type: "backToTurn" };
  } else if (k === "Backspace") {
    return { type: "toSessions" };
  }
  return null;
}
