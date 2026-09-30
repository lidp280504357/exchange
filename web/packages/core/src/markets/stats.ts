import * as dec from "../format/decimal";
import type { MarketRow, TickerOf } from "./list";

// Headline figures of the home page (design §6.2, rolled in with CountUp):
// the 24-hour turnover of the USDT markets, how many markets are listed
// and the largest leverage, computed exactly on decimal strings.

export type CompactParts = {
  /** The value in `suffix` units, at most `decimals` places (exact decimal). */
  value: string;
  /** "亿", "万" in Chinese; "B", "M", "K" in English; "" when small. */
  suffix: string;
};

const UNITS: Record<"zh" | "en", [string, string][]> = {
  zh: [
    ["1000000000000", "万亿"],
    ["100000000", "亿"],
    ["10000", "万"],
  ],
  en: [
    ["1000000000000", "T"],
    ["1000000000", "B"],
    ["1000000", "M"],
    ["1000", "K"],
  ],
};

/**
 * compactParts splits a large amount into a scaled value and its unit,
 * exactly and rounded half up: 2088349849.68 → 20.88 亿 (Chinese) or
 * 2.09 B (English). CountUp can roll the value while the unit stays.
 */
export function compactParts(v: string, locale: string, decimals = 2): CompactParts {
  if (!dec.isDecimal(v)) return { value: "0", suffix: "" };
  const units = UNITS[locale.startsWith("zh") ? "zh" : "en"];
  const abs = dec.abs(v);
  for (const [size, suffix] of units) {
    if (dec.gte(abs, size)) return { value: dec.div(v, size, decimals, "half"), suffix };
  }
  return { value: dec.round(v, decimals, "half"), suffix: "" };
}

/** usdtTurnover sums the 24-hour quote volume of the USDT spot pairs. */
export function usdtTurnover(rows: readonly MarketRow[], tickerOf: TickerOf): string {
  let sum = "0";
  for (const r of rows) {
    if (r.kind !== "spot" || r.quote !== "USDT") continue;
    const q = tickerOf(r.symbol)?.quote_volume;
    if (q && dec.isDecimal(q)) sum = dec.add(sum, q);
  }
  return sum;
}

export type Headline = {
  /** 24-hour turnover of the USDT spot pairs, in USDT. */
  turnover: string;
  spot: number;
  perps: number;
  /** Largest contract leverage (0 without contracts). */
  maxLeverage: number;
  /** Coins with a market. */
  coins: number;
};

/** headline computes the home page's figures from the rows and tickers. */
export function headline(rows: readonly MarketRow[], tickerOf: TickerOf): Headline {
  let spot = 0;
  let perps = 0;
  let maxLeverage = 0;
  const coins = new Set<string>();
  for (const r of rows) {
    coins.add(r.base);
    if (r.kind === "spot") spot++;
    else {
      perps++;
      maxLeverage = Math.max(maxLeverage, r.maxLeverage);
    }
  }
  return { turnover: usdtTurnover(rows, tickerOf), spot, perps, maxLeverage, coins: coins.size };
}
