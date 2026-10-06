import type { components } from "../api/gen/market";
import * as dec from "../format/decimal";
import { normalizeQuery, parseSort, sortRows, type MarketRow, type MarketSort, type SortKey, type TickerOf } from "../markets/list";
import type { FuturesOverviewItem } from "./data";

// The contracts in lists (design 2026-10-06 §3.3, §3.4, batch F3): the
// market list's futures category split into USDⓈ-margined and
// coin-margined contracts, with the open interest and funding rate of
// the overview as two more sortable columns, and the rows of the
// futures data overview (/futures/data). Pure functions over the
// reference data, the overview and the tickers.

type ContractSpec = components["schemas"]["Contract"];

/** The two groups of contracts: USDⓈ-margined (linear) and coin-margined (inverse). */
export type MarginGroup = "usdt" | "coin";

/** parseMarginGroup reads the group from the address (margin=coin); anything else is the linear one. */
export function parseMarginGroup(v: string | null | undefined): MarginGroup {
  return v === "coin" ? "coin" : "usdt";
}

/** groupOf is the group of a contract by its margin type (a contract without one is linear, as before G0). */
export function groupOf(c: { margin_type?: string | null } | undefined): MarginGroup {
  return c?.margin_type === "COIN" ? "coin" : "usdt";
}

/**
 * openContracts keeps the contracts the futures terminal can open: none
 * still PREPARE (Binance's perpetuals are listed PREPARE and open in
 * batches; the sites leave them out until then, review EY), and the
 * coin-margined ones only once the terminal's own list (useContracts)
 * has them, so that no list links to a contract the terminal does not
 * know yet (the sites list coin-margined contracts from G4 on).
 */
export function openContracts<C extends { margin_type?: string | null; status?: string }>(all: readonly C[], terminal: readonly { margin_type?: string | null }[]): C[] {
  const coin = terminal.some((c) => c.margin_type === "COIN");
  return all.filter((c) => c.status !== "PREPARE" && (coin || c.margin_type !== "COIN"));
}

/** The sort keys of the futures category: the list's, the open interest (by its USD value) and the funding rate. */
export type FuturesSortKey = SortKey | "oi" | "funding";
export type FuturesSort = { key: FuturesSortKey; desc: boolean };

/** parseFuturesSort reads the futures category's order from the address; null keeps the default. */
export function parseFuturesSort(key: string | null | undefined, dir: string | null | undefined): FuturesSort | null {
  if (key === "oi" || key === "funding") return { key, desc: dir !== "asc" };
  return parseSort(key, dir);
}

/** The overview item of a symbol, if the overview has one. */
export type OverviewOf = (symbol: string) => FuturesOverviewItem | undefined;

function overviewValue(item: FuturesOverviewItem | undefined, key: "oi" | "funding"): string | null {
  const v = key === "oi" ? item?.open_interest_value : item?.funding_rate;
  return v && dec.isDecimal(v) ? v : null;
}

/**
 * sortFuturesRows orders contract rows: by the open interest's USD value
 * or the funding rate from the overview (rows without one last either
 * way, ties in the default order), or as sortRows does.
 */
export function sortFuturesRows(rows: readonly MarketRow[], tickerOf: TickerOf, overviewOf: OverviewOf, sort: FuturesSort | null): MarketRow[] {
  if (!sort || (sort.key !== "oi" && sort.key !== "funding")) return sortRows(rows, tickerOf, sort as MarketSort | null);
  const key = sort.key;
  const base = sortRows(rows, tickerOf, null);
  const rank = new Map(base.map((r, i) => [r.symbol, i]));
  const values = new Map(base.map((r) => [r.symbol, overviewValue(overviewOf(r.symbol), key)]));
  const dir = sort.desc ? -1 : 1;
  return base.sort((a, b) => {
    const x = values.get(a.symbol) ?? null;
    const y = values.get(b.symbol) ?? null;
    if (x === null || y === null) return x === y ? rank.get(a.symbol)! - rank.get(b.symbol)! : x === null ? 1 : -1;
    return dir * dec.cmp(x, y) || rank.get(a.symbol)! - rank.get(b.symbol)!;
  });
}

/** A row of the futures data overview: the overview's figures with the contract's specification. */
export type OverviewRow = FuturesOverviewItem & {
  group: MarginGroup;
  base: string;
  /** USDT, or USD for a coin-margined contract. */
  quote: string;
  settle: string;
  status: string;
  priceDecimals: number;
  qtyDecimals: number;
  /** What the search box matches (normalized). */
  search: string;
};

/**
 * overviewRows joins the overview with the contracts' specifications:
 * a contract the specifications do not list (delisted meanwhile) is left
 * out, and so is a contract the terminal cannot open (see openContracts:
 * pass the open ones). names adds a coin's names to what the search box
 * matches.
 */
export function overviewRows(items: readonly FuturesOverviewItem[], contracts: readonly ContractSpec[], names: (base: string) => readonly string[] = () => []): OverviewRow[] {
  const specs = new Map(contracts.map((c) => [c.symbol, c]));
  const rows: OverviewRow[] = [];
  for (const it of items) {
    const c = specs.get(it.symbol);
    if (!c || c.status === "DELISTED") continue;
    rows.push({
      ...it,
      group: groupOf(c),
      base: c.base_asset,
      quote: c.quote_asset,
      settle: c.settle_asset || c.quote_asset,
      status: c.status,
      priceDecimals: dec.decimalsOf(c.tick_size),
      qtyDecimals: dec.decimalsOf(c.lot_size),
      search: [c.symbol, `${c.base_asset}${c.quote_asset}`, c.base_asset, ...names(c.base_asset)].map(normalizeQuery).join("|"),
    });
  }
  return rows;
}

/** The overview's sort keys. */
export type OverviewSortKey = "symbol" | "mark" | "change" | "volume" | "oi" | "funding";
export type OverviewSort = { key: OverviewSortKey; desc: boolean };

const OVERVIEW_KEYS: readonly OverviewSortKey[] = ["symbol", "mark", "change", "volume", "oi", "funding"];

/** parseOverviewSort reads the overview's order from the address; the default is the open interest, largest first. */
export function parseOverviewSort(key: string | null | undefined, dir: string | null | undefined): OverviewSort {
  if (key && OVERVIEW_KEYS.includes(key as OverviewSortKey)) return { key: key as OverviewSortKey, desc: dir !== "asc" };
  return { key: "oi", desc: true };
}

function overviewField(r: OverviewRow, key: OverviewSortKey): string | null {
  const v = key === "mark" ? r.mark_price : key === "change" ? r.change : key === "volume" ? r.quote_volume : key === "oi" ? r.open_interest_value : key === "funding" ? r.funding_rate : null;
  return v && dec.isDecimal(v) ? v : null;
}

/**
 * filterOverview keeps a group's rows that match the search, and
 * sortOverview orders them: numbers exactly, rows without a value last
 * either way, ties by symbol.
 */
export function filterOverview(rows: readonly OverviewRow[], group: MarginGroup, query = ""): OverviewRow[] {
  const q = normalizeQuery(query);
  return rows.filter((r) => r.group === group && (q === "" || r.search.includes(q)));
}

export function sortOverview(rows: readonly OverviewRow[], sort: OverviewSort): OverviewRow[] {
  const dir = sort.desc ? -1 : 1;
  const out = [...rows];
  if (sort.key === "symbol") return out.sort((a, b) => dir * a.symbol.localeCompare(b.symbol));
  return out.sort((a, b) => {
    const x = overviewField(a, sort.key);
    const y = overviewField(b, sort.key);
    if (x === null || y === null) return x === y ? a.symbol.localeCompare(b.symbol) : x === null ? 1 : -1;
    return dir * dec.cmp(x, y) || a.symbol.localeCompare(b.symbol);
  });
}

export type OverviewTotals = {
  /** The groups' contracts. */
  contracts: number;
  /** The open interest's USD value of the contracts that have one. */
  openInterestValue: string;
  /** The day's USD volume. */
  volume: string;
  /** Contracts whose running funding rate is above, and below, zero. */
  positive: number;
  negative: number;
};

/** overviewTotals sums a group's open interest and volume and counts the funding rates' signs. */
export function overviewTotals(rows: readonly OverviewRow[]): OverviewTotals {
  let oi = "0";
  let volume = "0";
  let positive = 0;
  let negative = 0;
  for (const r of rows) {
    if (r.open_interest_value && dec.isDecimal(r.open_interest_value)) oi = dec.add(oi, r.open_interest_value);
    if (r.quote_volume && dec.isDecimal(r.quote_volume)) volume = dec.add(volume, r.quote_volume);
    if (r.funding_rate && dec.isDecimal(r.funding_rate)) {
      const s = dec.sign(r.funding_rate);
      if (s > 0) positive++;
      else if (s < 0) negative++;
    }
  }
  return { contracts: rows.length, openInterestValue: oi, volume, positive, negative };
}
