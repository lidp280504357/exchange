import { applySettings, useSettings } from "@exchange/core";
import { useSyncExternalStore } from "react";

// The console is light by default and can switch to dark (design
// 2026-10-02 §6), which takes the user sites' dark tokens; the choice
// stays in this browser.

export type AdminTheme = "light" | "dark";

const KEY = "admin.theme";
const listeners = new Set<() => void>();
let current: AdminTheme = read();

function read(): AdminTheme {
  try {
    return globalThis.localStorage?.getItem(KEY) === "dark" ? "dark" : "light";
  } catch {
    return "light";
  }
}

/** applyTheme mirrors the theme and the shared settings (language, colours of rises) onto <html>. */
export function applyTheme() {
  applySettings(useSettings.getState(), current);
}

export function setTheme(theme: AdminTheme) {
  current = theme;
  try {
    localStorage.setItem(KEY, theme);
  } catch {
    // Private windows may refuse storage; the theme holds for the visit.
  }
  applyTheme();
  for (const fn of listeners) fn();
}

const subscribe = (fn: () => void) => {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
};

/** useTheme follows the console's theme. */
export function useTheme(): AdminTheme {
  return useSyncExternalStore(subscribe, () => current);
}
