// Theme helpers — single source of truth for the dark/light toggle.
//
// The palette is defined entirely as CSS custom properties in src/index.css
// (:root = light, .dark = dark); flipping a single class on <html> swaps every
// token in one paint. We persist the user's explicit choice in localStorage
// and fall back to the OS `prefers-color-scheme` (with dark as the ultimate
// default to preserve the app's original look).

export type Theme = "light" | "dark";

export const THEME_STORAGE_KEY = "mon.theme";

function isTheme(value: unknown): value is Theme {
  return value === "light" || value === "dark";
}

/** Read the stored preference, falling back to OS preference, then dark. */
export function resolveInitialTheme(): Theme {
  if (typeof window === "undefined") return "dark";
  try {
    const stored = window.localStorage.getItem(THEME_STORAGE_KEY);
    if (isTheme(stored)) return stored;
  } catch {
    // localStorage may be unavailable (private mode, sandboxed iframe).
  }
  if (window.matchMedia?.("(prefers-color-scheme: light)").matches) return "light";
  return "dark";
}

/** Apply a theme to <html> by toggling the `dark` class. Pure DOM op. */
export function applyTheme(theme: Theme) {
  if (typeof document === "undefined") return;
  document.documentElement.classList.toggle("dark", theme === "dark");
}

/** Persist a theme choice. Swallows quota / access errors silently. */
export function persistTheme(theme: Theme) {
  try {
    window.localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // Best-effort persistence; toggle still works for the session.
  }
}
