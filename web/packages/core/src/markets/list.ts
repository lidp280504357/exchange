import type { components } from "../api/gen/market";
import { coinProfile } from "../coins";
import * as dec from "../format/decimal";
import type { TickerData } from "../ws/types";

// The market list both user sites show (design §6.2 /markets, §7.2): spot
// pairs and perpetual contracts as one kind of row, the category filters
// (all, favourites, spot, futures, new, sector tags), the search box and
// the sortable columns. Pure functions over the reference data and the
// tickers, so the lists stay testable and the pages only render.

type TradingPair = components["schemas"]["TradingPair"];
type ContractSpec = components["schemas"]["Contract"];

/** The pair fields the list reads. */
export type PairLike = Pick<
  TradingPair,
  "symbol" | "base_asset" | "quote_asset" | "base_name" | "rank" | "categories" | "price_decimals" | "status" | "listed_at"
> & { reference_symbol?: string | null; base_display_name?: string | null };

/** The contract fields the list reads. */
export type ContractLike = Pick<ContractSpec, "symbol" | "base_asset" | "quote_asset" | "index_symbol" | "tick_size" | "status" | "max_leverage">;

export type MarketKind = "spot" | "perp";

export type MarketRow = {
  /** The pair or contract: BTC-USDT, BTC-USDT-PERP. */
  symbol: string;
  kind: MarketKind;
  base: string;
  quote: string;
  /** The base asset's name from the API (Bitcoin). */
  name: string;
  /** The base asset's market-cap rank; null when unranked. */
  rank: number | null;
  /** Sector tags of the base asset (layer-1, defi ...). */
  categories: readonly string[];
  status: string;
  priceDecimals: number;
  /** When the pair was listed (a contract takes its index pair's); "" when unknown. */
  listedAt: string;
  /** A contract's largest leverage; 0 for spot pairs. */
  maxLeverage: number;
  /** Its prices follow a reference market (a contract: its index pair's), whose ticker times show a stalled feed. */
  reference: boolean;
  /** What the search box matches: symbols and names in both languages, normalized. */
  search: string;
};

/** The ticker of a symbol, if one is known. */
export type TickerOf = (symbol: string) => TickerData | undefined;

/** normalizeQuery lower-cases a query and drops separators, so "btc/usdt", "BTC-USDT" and "btcusdt" match alike. */
export function normalizeQuery(q: string): string {
  return q.toLowerCase().replace(/[\s/_-]+/g, "");
}

function searchText(parts: (string | undefined)[]): string {
  return parts
    .filter((p): p is string => Boolean(p))
    .map(normalizeQuery)
    .join("|");
}

function profileNames(base: string): string[] {
  const p = coinProfile(base);
  return p ? [p.name["zh-CN"], p.name["zh-TW"], p.name.en] : [];
}

/**
 * buildRows turns the pairs and contracts into list rows. Delisted ones
 * are left out; a contract borrows its index pair's name, rank, sectors
 * and listing time.
 */
export function buildRows(pairs: readonly PairLike[], contracts: readonly ContractLike[]): MarketRow[] {
  const rows: MarketRow[] = [];
  for (const p of pairs) {
    if (p.status === "DELISTED") continue;
    rows.push({
      symbol: p.symbol,
      kind: "spot",
      base: p.base_asset,
      quote: p.quote_asset,
      name: p.base_display_name || p.base_name || p.base_asset,
      rank: p.rank,
      categories: p.categories,
      status: p.status,
      priceDecimals: p.price_decimals,
      listedAt: p.listed_at,
      maxLeverage: 0,
      reference: Boolean(p.reference_symbol),
      search: searchText([p.symbol, p.base_asset + p.quote_asset, p.base_asset, p.base_name, p.base_display_name ?? "", ...profileNames(p.base_asset)]),
    });
  }
  for (const c of contracts) {
    if (c.status === "DELISTED") continue;
    const index = pairs.find((p) => p.symbol === c.index_symbol) ?? pairs.find((p) => p.base_asset === c.base_asset);
    const name = index?.base_display_name || index?.base_name || coinProfile(c.base_asset)?.name.en || c.base_asset;
    rows.push({
      symbol: c.symbol,
      kind: "perp",
      base: c.base_asset,
      quote: c.quote_asset,
      name,
      rank: index?.rank ?? null,
      categories: index?.categories ?? [],
      status: c.status,
      priceDecimals: dec.decimalsOf(c.tick_size),
      listedAt: index?.listed_at ?? "",
      maxLeverage: c.max_leverage,
      reference: Boolean(index?.reference_symbol),
      search: searchText([c.symbol, `${c.base_asset}${c.quote_asset}`, `${c.base_asset}${c.quote_asset}perp`, c.base_asset, name, ...profileNames(c.base_asset)]),
    });
  }
  return rows;
}

/** A category of the left rail: fixed ones, or a sector tag ("tag:defi"). */
export type MarketCategory = "all" | "favorites" | "spot" | "futures" | "new" | `tag:${string}`;

const FIXED: readonly string[] = ["all", "favorites", "spot", "futures", "new"];

/** parseCategory reads a category from a URL parameter; anything unknown is "all". */
export function parseCategory(v: string | null | undefined): MarketCategory {
  if (!v) return "all";
  if (FIXED.includes(v)) return v as MarketCategory;
  if (/^tag:[a-z0-9][a-z0-9-]{0,31}$/.test(v)) return v as MarketCategory;
  return "all";
}

/** Days after listing a market counts as new. */
export const NEW_LISTING_DAYS = 30;

/** isNewListing: coming soon (PREPARE) or listed within the last `days` days. */
export function isNewListing(row: Pick<MarketRow, "status" | "listedAt">, now: number, days = NEW_LISTING_DAYS): boolean {
  if (row.status === "PREPARE") return true;
  const at = row.listedAt ? Date.parse(row.listedAt) : NaN;
  return Number.isFinite(at) && now - at <= days * 86_400_000;
}

/** inCategory reports whether a row belongs to a category. */
export function inCategory(row: MarketRow, category: MarketCategory, favorites: ReadonlySet<string>, now: number): boolean {
  switch (category) {
    case "all":
      return true;
    case "favorites":
      return favorites.has(row.symbol);
    case "spot":
      return row.kind === "spot";
    case "futures":
      return row.kind === "perp";
    case "new":
      return isNewListing(row, now);
    default:
      return row.categories.includes(category.slice(4));
  }
}

export type MarketFilter = {
  category: MarketCategory;
  /** The search box: a symbol or a name, in either language. */
  query?: string;
  favorites?: ReadonlySet<string>;
  /** The clock for "new" (epoch ms). */
  now?: number;
};

/** filterRows keeps the rows of a category that match the search. */
export function filterRows(rows: readonly MarketRow[], f: MarketFilter): MarketRow[] {
  const q = normalizeQuery(f.query ?? "");
  const favorites = f.favorites ?? new Set<string>();
  const now = f.now ?? Date.now();
  return rows.filter((r) => inCategory(r, f.category, favorites, now) && (q === "" || r.search.includes(q)));
}

/** categoryTags lists the sector tags of the rows, most used first (then by name). */
export function categoryTags(rows: readonly MarketRow[]): { tag: string; count: number }[] {
  const counts = new Map<string, number>();
  for (const r of rows) for (const c of r.categories) counts.set(c, (counts.get(c) ?? 0) + 1);
  return [...counts.entries()].map(([tag, count]) => ({ tag, count })).sort((a, b) => b.count - a.count || a.tag.localeCompare(b.tag));
}

/** categoryCount counts a category's rows (the numbers on the rail). */
export function categoryCount(rows: readonly MarketRow[], category: MarketCategory, favorites: ReadonlySet<string>, now: number): number {
  let n = 0;
  for (const r of rows) if (inCategory(r, category, favorites, now)) n++;
  return n;
}

/** tagLabel is a readable fallback for a sector tag without a translation: "layer-1" → "Layer 1". */
export function tagLabel(tag: string): string {
  return tag
    .split("-")
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

/** The sortable columns. */
export type SortKey = "rank" | "symbol" | "last" | "change" | "high" | "low" | "turnover" | "volume" | "listed";

export type MarketSort = { key: SortKey; desc: boolean };

const SORT_KEYS: readonly SortKey[] = ["rank", "symbol", "last", "change", "high", "low", "turnover", "volume", "listed"];

/** parseSort reads the sort from URL parameters; null keeps the default order (rank). */
export function parseSort(key: string | null | undefined, dir: string | null | undefined): MarketSort | null {
  if (!key || !SORT_KEYS.includes(key as SortKey)) return null;
  return { key: key as SortKey, desc: dir !== "asc" };
}

/**
 * defaultOrder: rank (unranked last), spot before its contract, USDT
 * quotes first, then the symbol.
 */
export function defaultOrder(a: MarketRow, b: MarketRow): number {
  const ra = a.rank ?? Number.MAX_SAFE_INTEGER;
  const rb = b.rank ?? Number.MAX_SAFE_INTEGER;
  if (ra !== rb) return ra - rb;
  if (a.base !== b.base) return a.base.localeCompare(b.base);
  if (a.kind !== b.kind) return a.kind === "spot" ? -1 : 1;
  if (a.quote !== b.quote) return a.quote === "USDT" ? -1 : b.quote === "USDT" ? 1 : a.quote.localeCompare(b.quote);
  return a.symbol.localeCompare(b.symbol);
}

function tickerField(t: TickerData | undefined, key: SortKey): string | null {
  if (!t) return null;
  switch (key) {
    case "last":
      return t.last;
    case "change":
      return t.change;
    case "high":
      return t.high;
    case "low":
      return t.low;
    case "turnover":
      return t.quote_volume;
    case "volume":
      return t.volume;
    default:
      return null;
  }
}

function validDecimal(v: string | null): v is string {
  return v !== null && dec.isDecimal(v);
}

/**
 * sortRows orders rows by a column. Numbers compare exactly (decimal
 * strings); rows without a value go last in either direction; ties keep
 * the default order.
 */
export function sortRows(rows: readonly MarketRow[], tickerOf: TickerOf, sort: MarketSort | null): MarketRow[] {
  const out = [...rows];
  if (!sort || sort.key === "rank") {
    out.sort(defaultOrder);
    if (sort?.desc) {
      // Ranked rows reversed, unranked ones still last.
      const ranked = out.filter((r) => r.rank !== null).reverse();
      return [...ranked, ...out.filter((r) => r.rank === null)];
    }
    return out;
  }
  const dir = sort.desc ? -1 : 1;
  if (sort.key === "symbol") {
    return out.sort((a, b) => dir * (a.base.localeCompare(b.base) || a.quote.localeCompare(b.quote)) || defaultOrder(a, b));
  }
  if (sort.key === "listed") {
    const at = (r: MarketRow) => (r.listedAt ? Date.parse(r.listedAt) : NaN);
    return out.sort((a, b) => {
      const x = at(a);
      const y = at(b);
      if (Number.isNaN(x) || Number.isNaN(y)) return Number.isNaN(x) === Number.isNaN(y) ? defaultOrder(a, b) : Number.isNaN(x) ? 1 : -1;
      return dir * (x - y) || defaultOrder(a, b);
    });
  }
  const values = new Map(out.map((r) => [r.symbol, tickerField(tickerOf(r.symbol), sort.key)]));
  return out.sort((a, b) => {
    const x = values.get(a.symbol) ?? null;
    const y = values.get(b.symbol) ?? null;
    const okX = validDecimal(x);
    const okY = validDecimal(y);
    if (!okX || !okY) return okX === okY ? defaultOrder(a, b) : okX ? -1 : 1;
    return dir * dec.cmp(x, y) || defaultOrder(a, b);
  });
}

/**
 * marqueeRows picks the home page's strip: the top coins by rank, each by
 * its USDT pair (or first pair); when there are fewer coins than `limit`,
 * the other markets fill in, still by rank.
 */
export function marqueeRows(rows: readonly MarketRow[], limit = 10): MarketRow[] {
  const ordered = [...rows].sort(defaultOrder);
  const picked: MarketRow[] = [];
  const bases = new Set<string>();
  for (const r of ordered) {
    if (picked.length >= limit) break;
    if (r.kind !== "spot" || bases.has(r.base)) continue;
    bases.add(r.base);
    picked.push(r);
  }
  for (const r of ordered) {
    if (picked.length >= limit) break;
    if (!picked.includes(r)) picked.push(r);
  }
  return picked.sort(defaultOrder);
}

export type Overview = { gainers: MarketRow[]; losers: MarketRow[]; turnover: MarketRow[] };

/**
 * rankOverview ranks the home page's three boards the way
 * /v1/market/summary does, on live tickers: USDT spot pairs that are
 * trading and have a price (the open contracts while spot is closed);
 * gainers by change (highest first), losers (lowest first), turnover by
 * quote volume.
 */
export function rankOverview(rows: readonly MarketRow[], tickerOf: TickerOf, limit = 5): Overview {
  const trading = rows.filter((r) => r.status === "TRADING");
  // With spot closed (design 2026-10-07, product line switches §1 #2) the open contracts rank instead, USDT-margined first.
  const pool =
    [
      trading.filter((r) => r.kind === "spot" && r.quote === "USDT"),
      trading.filter((r) => r.kind === "perp" && r.quote === "USDT"),
      trading.filter((r) => r.kind === "perp" && r.quote === "USD"),
    ].find((list) => list.length > 0) ?? [];
  const eligible = pool.filter((r) => validDecimal(tickerOf(r.symbol)?.last ?? null));
  const by = (key: SortKey, desc: boolean) =>
    sortRows(
      eligible.filter((r) => validDecimal(tickerField(tickerOf(r.symbol), key))),
      tickerOf,
      { key, desc },
    ).slice(0, limit);
  return { gainers: by("change", true), losers: by("change", false), turnover: by("turnover", true) };
}

export type Highlights = { hot: MarketRow[]; gainers: MarketRow[]; losers: MarketRow[]; newest: MarketRow[] };

/**
 * rankHighlights picks the markets page's four small boards over every
 * trading market, contracts included: hot (USDT-quoted, by turnover),
 * gainers and losers (by 24-hour change) and the newest listings.
 */
export function rankHighlights(rows: readonly MarketRow[], tickerOf: TickerOf, now: number, limit = 3): Highlights {
  const trading = rows.filter((r) => r.status === "TRADING");
  const withValue = (list: readonly MarketRow[], key: SortKey) => list.filter((r) => validDecimal(tickerField(tickerOf(r.symbol), key)));
  // Turnover compares in USDT; with nothing quoted in USDT (only the coin-margined line open, design 2026-10-07 product line
  // switches §1 #2) the contracts quoted in USD rank instead.
  const usdt = trading.filter((r) => r.quote === "USDT");
  const hot = sortRows(withValue(usdt.length > 0 ? usdt : trading.filter((r) => r.quote === "USD"), "turnover"), tickerOf, { key: "turnover", desc: true });
  const changes = withValue(trading, "change");
  return {
    hot: hot.slice(0, limit),
    gainers: sortRows(changes, tickerOf, { key: "change", desc: true }).slice(0, limit),
    losers: sortRows(changes, tickerOf, { key: "change", desc: false }).slice(0, limit),
    newest: sortRows(
      rows.filter((r) => isNewListing(r, now)),
      tickerOf,
      { key: "listed", desc: true },
    ).slice(0, limit),
  };
}

/** How old a reference ticker may get before the page warns (market.yaml Ticker). */
export const STALE_TICKER_MS = 30_000;

/**
 * staleSymbols lists the trading markets that follow a reference market
 * and whose ticker has not moved for maxAge: the feed has stalled.
 */
export function staleSymbols(rows: readonly MarketRow[], tickerOf: TickerOf, now: number, maxAge = STALE_TICKER_MS): string[] {
  const out: string[] = [];
  for (const r of rows) {
    if (!r.reference || r.status !== "TRADING") continue;
    const at = Date.parse(tickerOf(r.symbol)?.updated_at ?? "");
    if (Number.isFinite(at) && now - at > maxAge) out.push(r.symbol);
  }
  return out;
}
