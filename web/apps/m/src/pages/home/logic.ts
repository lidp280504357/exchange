import { routes } from "@exchange/core";
import { defaultOrder, type MarketCategory, type MarketRow, type MarketSort } from "@exchange/core/markets/index";

// Pure pieces of the mobile home, markets and coin pages: which pills the
// markets page offers, the orders its sort sheet lists, and which markets
// a coin page shows. The lists themselves (rows, filters, sorting,
// rankings) come from @exchange/core/markets.

/** coinFromParam reads the coin of /coin/:symbol: "btc" and "BTC-USDT" both mean BTC. */
export function coinFromParam(raw: string | undefined): string {
  return ((raw ?? "").split("-")[0] ?? "").trim().toUpperCase();
}

export type CoinMarkets = {
  /** Every market of the coin, in the list order. */
  markets: MarketRow[];
  /** The market the header prices and the chart draws: its USDT spot pair, else a spot pair, else the first. */
  primary?: MarketRow;
  /** Where "trade" goes: a trading spot pair first, then any trading market. */
  tradeTarget?: MarketRow;
  perp?: MarketRow;
};

/** coinMarkets picks a coin's markets for its page. */
export function coinMarkets(rows: readonly MarketRow[], coin: string): CoinMarkets {
  const markets = rows.filter((r) => r.base === coin).sort(defaultOrder);
  const primary = markets.find((r) => r.kind === "spot" && r.quote === "USDT") ?? markets.find((r) => r.kind === "spot") ?? markets[0];
  const tradeTarget = markets.find((r) => r.kind === "spot" && r.status === "TRADING") ?? markets.find((r) => r.status === "TRADING") ?? primary;
  const perp = markets.find((r) => r.kind === "perp");
  return { markets, primary, tradeTarget, perp };
}

const FIXED_PILLS: readonly MarketCategory[] = ["all", "favorites", "spot", "futures"];

/**
 * categoryPills lists the markets page's pills: all, favourites, spot,
 * futures, then the sector tags; a category a link asked for that is not
 * among them (new listings, a tag no market carries now) joins after the
 * fixed ones, so the filter in use is always visible.
 */
export function categoryPills(current: MarketCategory, tags: readonly string[]): MarketCategory[] {
  const out: MarketCategory[] = [...FIXED_PILLS, ...tags.map((t): MarketCategory => `tag:${t}`)];
  if (!out.includes(current)) out.splice(FIXED_PILLS.length, 0, current);
  return out;
}

export type SortOption = { id: string; sort: MarketSort | null };

/** The orders the sort sheet offers (the address keeps the PC site's sort and dir parameters). */
export const SORT_OPTIONS: readonly SortOption[] = [
  { id: "default", sort: null },
  { id: "changeDesc", sort: { key: "change", desc: true } },
  { id: "changeAsc", sort: { key: "change", desc: false } },
  { id: "turnoverDesc", sort: { key: "turnover", desc: true } },
  { id: "lastDesc", sort: { key: "last", desc: true } },
  { id: "lastAsc", sort: { key: "last", desc: false } },
  { id: "symbolAsc", sort: { key: "symbol", desc: false } },
];

/** sortOptionOf names the option of an order ("default" for none, null for one the sheet does not list). */
export function sortOptionOf(sort: MarketSort | null): string | null {
  if (!sort) return "default";
  return SORT_OPTIONS.find((o) => o.sort !== null && o.sort.key === sort.key && o.sort.desc === sort.desc)?.id ?? null;
}

/** sortParams turns an order into the address's parameters (null removes them). */
export function sortParams(sort: MarketSort | null): { sort: string | null; dir: string | null } {
  return sort ? { sort: sort.key, dir: sort.desc ? "desc" : "asc" } : { sort: null, dir: null };
}

export type Board = "gainers" | "losers";

/** boardPath opens the markets list in a board's order (the home page's "all" link). */
export function boardPath(board: Board): string {
  return `${routes.markets}?sort=change&dir=${board === "gainers" ? "desc" : "asc"}`;
}

/** maxLeverage is the largest contract leverage among the rows, or the fallback without contracts. */
export function maxLeverage(rows: readonly MarketRow[], fallback = 50): number {
  return rows.reduce((m, r) => Math.max(m, r.maxLeverage), 0) || fallback;
}
