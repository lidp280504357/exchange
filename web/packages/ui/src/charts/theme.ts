import { useEffect, useState } from "react";

// Canvas charts cannot use CSS classes: their colours are read from the
// tokens (CSS variables) at run time, and read again when <html> changes
// theme (data-theme) or rise colour (data-updown).

export type ChartTheme = {
  background: string;
  text: string;
  grid: string;
  border: string;
  crosshair: string;
  labelBackground: string;
  up: string;
  down: string;
  /** Translucent rise and fall (volume bars). */
  upSoft: string;
  downSoft: string;
  /** Indicator lines: --chart-1 … --chart-5. */
  lines: string[];
  fontFamily: string;
};

/** cssVar reads a token from <html>; empty outside a browser. */
export function cssVar(name: string): string {
  const root = globalThis.document?.documentElement;
  if (!root || typeof getComputedStyle !== "function") return "";
  return getComputedStyle(root).getPropertyValue(name).trim();
}

/** readChartTheme reads the chart colours from the current tokens. */
export function readChartTheme(): ChartTheme {
  return {
    background: cssVar("--bg-1"),
    text: cssVar("--fg-3"),
    grid: cssVar("--line-1"),
    border: cssVar("--line-1"),
    crosshair: cssVar("--fg-3"),
    labelBackground: cssVar("--bg-3"),
    up: cssVar("--up"),
    down: cssVar("--down"),
    upSoft: cssVar("--up-soft"),
    downSoft: cssVar("--down-soft"),
    lines: [1, 2, 3, 4, 5].map((i) => cssVar(`--chart-${i}`)),
    fontFamily: cssVar("--font-sans") || "sans-serif",
  };
}

/**
 * useChartTheme returns the chart colours and a new object whenever the
 * page switches theme, rise colour or language (a MutationObserver on
 * <html>; Traditional Chinese has its own font stack).
 */
export function useChartTheme(): ChartTheme {
  const [theme, setTheme] = useState(readChartTheme);
  useEffect(() => {
    const root = globalThis.document?.documentElement;
    if (!root || typeof MutationObserver === "undefined") return;
    const observer = new MutationObserver(() => setTheme(readChartTheme()));
    observer.observe(root, { attributes: true, attributeFilter: ["data-theme", "data-updown", "class", "style", "lang"] });
    return () => observer.disconnect();
  }, []);
  return theme;
}
