// Theme: "dark" (default) or "light". The choice lives in localStorage; the OS theme is
// ignored. Storage may be missing, throw, or hold anything, so every access is guarded (E15).
export const THEMES = ["dark", "light"];
const STORAGE_NAME = "theme";

// loadTheme returns the stored theme, or "dark" when storage fails or holds another value.
export function loadTheme(storage) {
  try {
    const v = storage.getItem(STORAGE_NAME);
    return THEMES.includes(v) ? v : "dark";
  } catch {
    return "dark";
  }
}

export function applyTheme(root, theme) {
  root.setAttribute("data-theme", theme);
}

// pickTheme applies the theme at once and stores it; a storage failure only skips the store.
export function pickTheme(storage, root, theme) {
  if (!THEMES.includes(theme)) theme = "dark";
  applyTheme(root, theme);
  try {
    storage.setItem(STORAGE_NAME, theme);
  } catch {
    // unavailable storage: the theme still applies for this page load
  }
  return theme;
}

// initTheme applies the stored (or default) theme and returns it.
export function initTheme(storage, root) {
  const theme = loadTheme(storage);
  applyTheme(root, theme);
  return theme;
}

// Style: the look laid over either theme, "lean" (the default: smooth, round) or "stark" (square,
// mono caps), spec 027 C2. Stored and guarded like the theme; it sets data-style on the root and
// the stylesheet does the rest. Any other stored value, an older style name included, is lean.
export const STYLES = ["lean", "stark"];
const STYLE_STORAGE_NAME = "style";

// loadStyle returns the stored style, or "lean" when storage fails or holds another value.
export function loadStyle(storage) {
  try {
    const v = storage.getItem(STYLE_STORAGE_NAME);
    return STYLES.includes(v) ? v : "lean";
  } catch {
    return "lean";
  }
}

export function applyStyle(root, look) {
  root.setAttribute("data-style", look);
}

// pickStyle applies the style at once and stores it; a storage failure only skips the store.
export function pickStyle(storage, root, look) {
  if (!STYLES.includes(look)) look = "lean";
  applyStyle(root, look);
  try {
    storage.setItem(STYLE_STORAGE_NAME, look);
  } catch {
    // unavailable storage: the style still applies for this page load
  }
  return look;
}

// initStyle applies the stored (or default) style and returns it.
export function initStyle(storage, root) {
  const look = loadStyle(storage);
  applyStyle(root, look);
  return look;
}
