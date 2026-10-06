import type { CandleData } from "@exchange/core";

// Pure helpers of the candle chart, apart from the chart library so the
// package index can export them without loading lightweight-charts.

/** intervalParts splits "15m" into 15 and "m"; units are m h d w M. */
export function intervalParts(interval: string): { n: number; unit: "m" | "h" | "d" | "w" | "M" } | null {
  const m = /^(\d+)([mhdwM])$/.exec(interval);
  return m ? { n: Number(m[1]), unit: m[2] as "m" | "h" | "d" | "w" | "M" } : null;
}

/** What the chart last drew: the series key (symbol|interval), its first and last candles and count. */
export type CandleDataState = { key: string; firstTime: string; firstOpen: string; lastTime: string; len: number };
/** reset: setData and back to now; prepend: older page, view kept; tail/append: series.update. */
export type UpdateMode = "reset" | "prepend" | "tail" | "append";

/** updateMode decides how new candles reach the chart without redrawing it all. */
export function updateMode(prev: CandleDataState | null, key: string, next: CandleData[]): UpdateMode {
  const first = next[0];
  const last = next[next.length - 1];
  if (!prev || !first || !last || prev.key !== key) return "reset";
  const sameStart = prev.firstTime === first.open_time && prev.firstOpen === first.open;
  if (sameStart && next.length === prev.len && last.open_time === prev.lastTime) return "tail";
  if (sameStart && next.length === prev.len + 1 && next[next.length - 2]?.open_time === prev.lastTime) return "append";
  const added = next.length - prev.len;
  if (added > 0 && next[added]?.open_time === prev.firstTime && next[added]?.open === prev.firstOpen && last.open_time === prev.lastTime) return "prepend";
  return "reset";
}

// The candles' price scale leaves room at the top for the legend, which
// sits over the plot: the legend's height (with its 6 px offset and 6 px
// to spare) as a share of the pane, at least the 8 % it always had and at
// most half (B116: on the phone's coin page the legend wraps to three to
// five lines, 58–100 px, over a 232 px pane, and covered the highest
// candles with 8 %).
const LEGEND_SPARE = 12;
const TOP_MIN = 0.08;
const TOP_MAX = 0.5;

/** topMargin is the candles' top scale margin for a legend legendPx high over a pane panePx high. */
export function topMargin(legendPx: number, panePx: number): number {
  if (!(panePx > 0) || !(legendPx > 0)) return TOP_MIN;
  return Math.min(TOP_MAX, Math.max(TOP_MIN, (legendPx + LEGEND_SPARE) / panePx));
}

/** The legend height the chart leaves room for, with what it was measured for. */
export type LegendRoom = { key: string; width: number; legend: number };

/**
 * legendRoom keeps the tallest legend measured since the chart's key
 * (symbol, interval and indicators) or width last changed: the room only
 * grows while the crosshair's values change the legend's wrapping, so the
 * scale does not move under the pointer; a new key or width measures
 * afresh.
 */
export function legendRoom(prev: LegendRoom, key: string, width: number, legend: number): LegendRoom {
  if (prev.key !== key || prev.width !== width) return { key, width, legend };
  return { key, width, legend: Math.max(prev.legend, legend) };
}
