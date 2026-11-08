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
