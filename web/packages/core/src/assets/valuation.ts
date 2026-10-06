import * as dec from "../format/decimal";

// Valuation of balances in USDT (design §6.2 assets overview), at
// reference prices: USDT counts 1; any other asset goes through its
// <ASSET>-USDT pair's last price, or through BTC (<ASSET>-BTC ×
// BTC-USDT). An asset without a price is valued at 0 and listed, so the
// page can say so. Everything is exact decimal arithmetic; only the
// shares of the distribution ring become floats, for drawing.

export const VALUATION_QUOTE = "USDT";
export const VALUATION_BRIDGE = "BTC";
/** Rows valued below this (USDT) are "small" for the hide-small switch. */
export const SMALL_VALUE = "1";

export type TickerLike = { last: string | null; change?: string | null };
export type Tickers = ReadonlyMap<string, TickerLike>;

function lastPrice(tickers: Tickers, symbol: string): string | null {
  const v = tickers.get(symbol)?.last;
  return v && dec.isDecimal(v) && dec.sign(v) > 0 ? v : null;
}

/** referencePrice is an asset's price in USDT, or null when no pair prices it. */
export function referencePrice(asset: string, tickers: Tickers): string | null {
  if (asset === VALUATION_QUOTE) return "1";
  const direct = lastPrice(tickers, `${asset}-${VALUATION_QUOTE}`);
  if (direct) return direct;
  if (asset === VALUATION_BRIDGE) return null;
  const cross = lastPrice(tickers, `${asset}-${VALUATION_BRIDGE}`);
  const bridge = lastPrice(tickers, `${VALUATION_BRIDGE}-${VALUATION_QUOTE}`);
  return cross && bridge ? dec.mul(cross, bridge) : null;
}

function changeOf(tickers: Tickers, symbol: string): string | null {
  const c = tickers.get(symbol)?.change;
  return lastPrice(tickers, symbol) && c && dec.isDecimal(c) ? c : null;
}

/**
 * referenceChange is the 24-hour change of an asset's USDT price as a
 * ratio ("0.0231" for +2.31%), along the path referencePrice takes: 0 for
 * USDT, the USDT pair's change, or (1 + the BTC pair's) × (1 + BTC-USDT's)
 * − 1; null without one.
 */
export function referenceChange(asset: string, tickers: Tickers): string | null {
  if (asset === VALUATION_QUOTE) return "0";
  if (lastPrice(tickers, `${asset}-${VALUATION_QUOTE}`)) return changeOf(tickers, `${asset}-${VALUATION_QUOTE}`);
  if (asset === VALUATION_BRIDGE) return null;
  const cross = changeOf(tickers, `${asset}-${VALUATION_BRIDGE}`);
  const bridge = changeOf(tickers, `${VALUATION_BRIDGE}-${VALUATION_QUOTE}`);
  return cross !== null && bridge !== null ? dec.sub(dec.mul(dec.add("1", cross), dec.add("1", bridge)), "1") : null;
}

export type DayChange = {
  /** USDT the holdings gained over 24 hours (negative: lost). */
  value: string;
  /** value ÷ their worth 24 hours earlier; null when that was nothing. */
  ratio: string | null;
};

/**
 * dayChange estimates the last 24 hours' profit or loss of what is held
 * now: value × change ÷ (1 + change) for each priced row, at its asset's
 * 24-hour change. There are no daily balance snapshots, so funds that came
 * or went during the day count as held all day: the market's effect on
 * the current holdings, which is how pages label it ("24h").
 */
export function dayChange(rows: readonly Pick<AssetRow, "asset" | "value">[], changeOfAsset: (asset: string) => string | null): DayChange {
  let value = "0";
  let now = "0";
  for (const r of rows) {
    if (r.value === null) continue;
    now = dec.add(now, r.value);
    const c = changeOfAsset(r.asset);
    if (c === null || dec.sign(c) === 0) continue;
    const base = dec.add("1", c);
    if (dec.sign(base) <= 0) continue;
    value = dec.add(value, dec.div(dec.mul(r.value, c), base, 8));
  }
  const before = dec.sub(now, value);
  return { value, ratio: dec.sign(before) > 0 ? dec.div(value, before, 6) : null };
}

export type BalanceLike = { account_type: string; asset: string; available: string; frozen: string; total: string };

export type AccountView = "ALL" | "SPOT" | "FUTURES";

export type AssetRow = {
  asset: string;
  available: string;
  frozen: string;
  total: string;
  /** The USDT price used; null when none. */
  price: string | null;
  /** total × price; null without a price (counted as 0). */
  value: string | null;
};

/** MarginHoldings is what valuing a margin account takes: each coin's net, what it holds less what it owes. */
export type MarginHoldings = { balances: readonly { asset: string; net: string }[] };

export type Portfolio = {
  /** Every account, in USDT: spot, futures and the margin accounts' net. */
  total: string;
  spot: string;
  futures: string;
  /**
   * The margin accounts (cross and isolated): each coin's net at its
   * reference price, as spot and futures are valued (no haircut).
   */
  margin: string;
  /** Assets holding funds that have no price (valued at 0). */
  unpriced: string[];
  rows: Record<AccountView, AssetRow[]>;
  /** The margin accounts' coins, nets merged (negative where more is owed than held). */
  marginRows: { asset: string; value: string | null }[];
};

function row(asset: string, available: string, frozen: string, price: string | null): AssetRow {
  const total = dec.add(available, frozen);
  return { asset, available, frozen, total, price, value: price === null ? null : dec.mul(total, price) };
}

/** byValue sorts rows by value (priced first), then by amount and code. */
function byValue(a: AssetRow, b: AssetRow): number {
  if (a.value !== null && b.value !== null) {
    const c = dec.cmp(b.value, a.value);
    if (c !== 0) return c;
  } else if (a.value !== b.value) {
    return a.value === null ? 1 : -1;
  }
  const t = dec.cmp(b.total, a.total);
  return t !== 0 ? t : a.asset < b.asset ? -1 : a.asset > b.asset ? 1 : 0;
}

/**
 * valuePortfolio values every balance at its reference price: the rows of
 * each account (and of both merged per asset), the account subtotals and
 * the total, and the assets that could not be priced. The margin accounts
 * (margin design 2026-10-06, B102) count with their coins' nets.
 */
export function valuePortfolio(
  balances: readonly BalanceLike[],
  priceOf: (asset: string) => string | null,
  margin: readonly MarginHoldings[] = [],
): Portfolio {
  const prices = new Map<string, string | null>();
  const price = (asset: string) => {
    if (!prices.has(asset)) prices.set(asset, priceOf(asset));
    return prices.get(asset) ?? null;
  };
  const spot: AssetRow[] = [];
  const futures: AssetRow[] = [];
  const merged = new Map<string, { available: string; frozen: string }>();
  for (const b of balances) {
    const r = row(b.asset, b.available, b.frozen, price(b.asset));
    if (b.account_type === "FUTURES") futures.push(r);
    else spot.push(r);
    const m = merged.get(b.asset) ?? { available: "0", frozen: "0" };
    merged.set(b.asset, { available: dec.add(m.available, b.available), frozen: dec.add(m.frozen, b.frozen) });
  }
  const all = [...merged].map(([asset, m]) => row(asset, m.available, m.frozen, price(asset)));
  const sum = (rows: readonly { value: string | null }[]) => rows.reduce((s, r) => (r.value === null ? s : dec.add(s, r.value)), "0");
  const nets = new Map<string, string>();
  for (const a of margin) for (const b of a.balances) nets.set(b.asset, dec.add(nets.get(b.asset) ?? "0", b.net));
  const marginRows = [...nets].map(([asset, net]) => {
    const p = price(asset);
    return { asset, value: p === null ? null : dec.mul(net, p) };
  });
  const spotTotal = sum(spot);
  const futuresTotal = sum(futures);
  const marginTotal = sum(marginRows);
  const unpriced = new Set(all.filter((r) => r.value === null && dec.sign(r.total) > 0).map((r) => r.asset));
  for (const [asset, net] of nets) if (price(asset) === null && dec.sign(net) !== 0) unpriced.add(asset);
  return {
    total: dec.add(dec.add(spotTotal, futuresTotal), marginTotal),
    spot: spotTotal,
    futures: futuresTotal,
    margin: marginTotal,
    unpriced: [...unpriced],
    rows: { ALL: all.sort(byValue), SPOT: spot.sort(byValue), FUTURES: futures.sort(byValue) },
    marginRows,
  };
}

/**
 * accountShare is an account's share of what the accounts hold, as a ratio
 * ("0.25"): of the sum of the parts above zero (a margin net below zero,
 * more owed than held, takes none, as on the mobile "me" card's split bar);
 * null for a part not above zero or when nothing is held (B107).
 */
export function accountShare(part: string, parts: readonly string[]): string | null {
  const base = parts.reduce((s, v) => (dec.sign(v) > 0 ? dec.add(s, v) : s), "0");
  return dec.sign(part) > 0 && dec.sign(base) > 0 ? dec.div(part, base, 4, "half") : null;
}

/**
 * isSmall reports whether a row hides under "hide small balances": an
 * empty balance, or one valued below the threshold. An unpriced balance
 * is never small (its worth is unknown).
 */
export function isSmall(r: Pick<AssetRow, "total" | "value">, threshold: string = SMALL_VALUE): boolean {
  if (dec.sign(r.total) <= 0) return true;
  return r.value !== null && dec.lt(r.value, threshold);
}

/** convertValue expresses a USDT value in another asset of the given price, cut down to decimals. */
export function convertValue(value: string, price: string | null, decimals: number): string | null {
  if (!price || dec.sign(price) <= 0) return null;
  return dec.div(value, price, decimals, "down");
}

export type Slice = {
  /** The asset, or null for the rest ("others"). */
  asset: string | null;
  value: string;
  /** The share of the total, a fraction with 4 decimals ("0.1234"). */
  share: string;
};

/**
 * distribution keeps the `top` most valuable assets and folds the rest into
 * one "others" slice; unpriced and empty rows take no part. The shares are
 * rounded half-up to 4 decimals (they are drawn, not charged).
 */
export function distribution(rows: readonly AssetRow[], top = 5): Slice[] {
  const priced = rows.filter((r): r is AssetRow & { value: string } => r.value !== null && dec.sign(r.value) > 0).sort(byValue);
  const total = priced.reduce((s, r) => dec.add(s, r.value), "0");
  if (dec.sign(total) <= 0) return [];
  const share = (v: string) => dec.div(v, total, 4, "half");
  const head: Slice[] = priced.slice(0, top).map((r) => ({ asset: r.asset, value: r.value, share: share(r.value) }));
  const rest = priced.slice(top).reduce((s, r) => dec.add(s, r.value), "0");
  return dec.sign(rest) > 0 ? [...head, { asset: null, value: rest, share: share(rest) }] : head;
}
