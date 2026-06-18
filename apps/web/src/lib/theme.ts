// theme.ts — light/dark theme persistence (§13.8.18 Settings).
//
// The app styles dark mode via a `.dark` class on <html> (no theme provider);
// many components read that class. This module is the single writer: it
// resolves the stored preference (or the OS preference for "system"), toggles
// the class, and persists the choice. Settings (t12) flips it; main.tsx
// applies the stored choice once at startup so there's no flash.

export type Theme = "light" | "dark" | "system"

const STORAGE_KEY = "skillfleet.theme"
export const DEFAULT_THEME: Theme = "system"

export function readStoredTheme(): Theme {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v === "light" || v === "dark" || v === "system") return v
  } catch {
    // localStorage unavailable — fall through to the default.
  }
  return DEFAULT_THEME
}

// resolveTheme maps "system" to the OS preference; light/dark pass through.
function resolveTheme(theme: Theme): "light" | "dark" {
  if (theme === "system") {
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"
  }
  return theme
}

// applyTheme toggles the .dark class to match the resolved theme. Safe to call
// repeatedly.
export function applyTheme(theme: Theme): void {
  const resolved = resolveTheme(theme)
  document.documentElement.classList.toggle("dark", resolved === "dark")
}

// setTheme persists the choice and applies it immediately.
export function setTheme(theme: Theme): void {
  try {
    localStorage.setItem(STORAGE_KEY, theme)
  } catch {
    // ignore — still apply for this session.
  }
  applyTheme(theme)
}

// initTheme applies the stored choice at startup and, when on "system", keeps
// following OS changes for the session. Returns a cleanup (unused at startup).
export function initTheme(): void {
  applyTheme(readStoredTheme())
  const mql = window.matchMedia("(prefers-color-scheme: dark)")
  mql.addEventListener("change", () => {
    if (readStoredTheme() === "system") applyTheme("system")
  })
}
