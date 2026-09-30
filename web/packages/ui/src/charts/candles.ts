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
