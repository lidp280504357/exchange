import * as dec from "../format/decimal";
import { formatAmount, formatCompact, formatDecimal, formatPercent, formatPrice } from "../format/number";
import { formatTime } from "../format/time";
import type { FuturesDataPoint, FuturesMetric, FuturesPeriod } from "./data";

// What the data panels draw for each statistic (design 2026-10-06 §3.3):
// the points as numbers for the chart (exact strings kept for the text),
// the chart's form, the units of the values and how they read. Pure, so
// both sites draw the same thing and the tests pin it down.

/** A point of a series: its time (epoch ms), its values as numbers (for drawing) and as received (for text). */
export type ChartPoint = { t: number; v: Record<string, number>; raw: Record<string, string> };

/**
 * chartPoints turns the API's points into chart points, oldest first: a
 * point without a valid time is dropped, and of two with one time the
 * later in the list stays.
 */
export function chartPoints(points: readonly FuturesDataPoint[]): ChartPoint[] {
  const byTime = new Map<number, ChartPoint>();
  for (const p of points) {
    const t = Date.parse(p.time);
    if (!Number.isFinite(t)) continue;
    const v: Record<string, number> = {};
    const raw: Record<string, string> = {};
    for (const [k, s] of Object.entries(p.values)) {
      if (typeof s !== "string" || !dec.isDecimal(s)) continue;
      v[k] = dec.toNumber(s);
      raw[k] = s;
    }
    byTime.set(t, { t, v, raw });
  }
  return [...byTime.values()].sort((a, b) => a.t - b.t);
}

/**
 * The chart forms (one y axis each; the other figures of a point are in
 * its tooltip and the table): a line for a level (the open interest over
 * a wash, the basis alone: a wash under a value that may be negative would
 * read as a size), columns from zero coloured by sign (funding rates), long over
 * short shares stacked to 100% (the long/short ratios), and buys above
 * zero mirrored by sells below (the takers' volume).
 */
export type MetricForm =
  | { kind: "line"; key: string; area?: boolean }
  | { kind: "columns"; key: string }
  | { kind: "share"; up: string; down: string }
  | { kind: "mirror"; up: string; down: string };

export const METRIC_FORMS: Record<FuturesMetric, MetricForm> = {
  open_interest: { kind: "line", key: "open_interest", area: true },
  top_long_short_account: { kind: "share", up: "long", down: "short" },
  top_long_short_position: { kind: "share", up: "long", down: "short" },
  long_short_account: { kind: "share", up: "long", down: "short" },
  taker_ratio: { kind: "mirror", up: "buy_vol", down: "sell_vol" },
  basis: { kind: "line", key: "basis" },
  funding: { kind: "columns", key: "funding_rate" },
};

/**
 * What a value is: a quantity in the contract's unit (the base asset, or
 * contracts of a coin-margined one), a USD value, a share of 1, a ratio, a
 * price, or a rate (a fraction shown as a percentage to 4 places).
 */
export type ValueUnit = "qty" | "usd" | "share" | "ratio" | "price" | "rate";

/** The values of each statistic, in the order a tooltip and the table list them. */
export const METRIC_VALUES: Record<FuturesMetric, readonly { key: string; unit: ValueUnit }[]> = {
  open_interest: [
    { key: "open_interest", unit: "qty" },
    { key: "open_interest_value", unit: "usd" },
  ],
  top_long_short_account: [
    { key: "long_short_ratio", unit: "ratio" },
    { key: "long", unit: "share" },
    { key: "short", unit: "share" },
  ],
  top_long_short_position: [
    { key: "long_short_ratio", unit: "ratio" },
    { key: "long", unit: "share" },
    { key: "short", unit: "share" },
  ],
  long_short_account: [
    { key: "long_short_ratio", unit: "ratio" },
    { key: "long", unit: "share" },
    { key: "short", unit: "share" },
  ],
  taker_ratio: [
    { key: "buy_sell_ratio", unit: "ratio" },
    { key: "buy_vol", unit: "qty" },
    { key: "sell_vol", unit: "qty" },
  ],
  basis: [
    { key: "basis", unit: "price" },
    { key: "basis_rate", unit: "rate" },
    { key: "futures_price", unit: "price" },
    { key: "index_price", unit: "price" },
  ],
  funding: [
    { key: "funding_rate", unit: "rate" },
    { key: "mark_price", unit: "price" },
  ],
};

/** How a chart's axis reads its ticks. */
export type AxisStyle = "compact" | "percent" | "plain";

export const METRIC_AXIS: Record<FuturesMetric, AxisStyle> = {
  open_interest: "compact",
  top_long_short_account: "percent",
  top_long_short_position: "percent",
  long_short_account: "percent",
  taker_ratio: "compact",
  basis: "plain",
  funding: "percent",
};

export type FormatContext = {
  /** The contract's price decimals (its tick). */
  priceDecimals: number;
  /** Decimals of a quantity: the lot's (0 for contracts). */
  qtyDecimals: number;
  locale: string;
};

/**
 * formatValue renders a value of a statistic: quantities grouped at the
 * lot's decimals (compact from a million), USD values compact, shares and
 * rates as percentages, ratios as received, prices at the tick.
 */
export function formatValue(unit: ValueUnit, raw: string | undefined, ctx: FormatContext): string {
  if (raw === undefined) return "—";
  switch (unit) {
    case "qty":
      return dec.isDecimal(raw) && dec.gte(dec.abs(raw), "1000000") ? formatCompact(raw, ctx.locale) : formatAmount(raw, ctx.qtyDecimals);
    case "usd":
      return formatCompact(raw, ctx.locale);
    case "share":
      return formatPercent(raw, 2, false);
    case "ratio":
      return formatDecimal(raw, { decimals: 2, rounding: "half" });
    case "price":
      return formatPrice(raw, ctx.priceDecimals);
    case "rate":
      return formatPercent(raw, 4);
  }
}

/** windowChange is how much a value moved over the points shown, as a fraction (null without two points or from zero). */
export function windowChange(points: readonly ChartPoint[], key: string): string | null {
  const first = points.find((p) => p.raw[key] !== undefined)?.raw[key];
  const last = points.findLast((p) => p.raw[key] !== undefined)?.raw[key];
  if (first === undefined || last === undefined || points.length < 2 || dec.isZero(first)) return null;
  return dec.div(dec.sub(last, first), dec.abs(first), 8, "half");
}

/**
 * axisTime labels a point on a chart's time axis: the clock for periods
 * within a day, month and day with the clock for 4 hours and funding, the
 * date alone for days.
 */
export function axisTime(t: number, period: FuturesPeriod | "funding", locale: string, timeZone?: string): string {
  if (period === "1d") return formatTime(t, "date", locale, timeZone).slice(5);
  if (period === "4h" || period === "funding") return formatTime(t, "monthDay", locale, timeZone);
  return formatTime(t, "time", locale, timeZone);
}

/** pointTime is a point's time in its tooltip and table row: the date alone for days. */
export function pointTime(t: number, period: FuturesPeriod | "funding", locale: string, timeZone?: string): string {
  return formatTime(t, period === "1d" ? "date" : "datetime", locale, timeZone);
}
