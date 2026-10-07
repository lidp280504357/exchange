import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { platformApi, unwrap } from "../api/client";
import type { components } from "../api/gen/platform";
import { DEFAULT_CONTRACT, DEFAULT_SYMBOL, routes } from "../routes";

// The product lines operators open and close (design 2026-10-07, product
// line switches §1): spot, USDT-margined and coin-margined contracts. A
// closed line leaves the sites' menus, lists and search, its trading pages
// say it is not open, and holders keep a way to wind down.

export type ProductLine = "spot" | "usdt_m" | "coin_m";

// A perpetual contract (BTC-USDT-PERP), as query/private's isContract says:
// the top bars read the lines, and that module stays out of the first load.
const isContract = (symbol: string) => symbol.endsWith("-PERP");
export type PlatformProducts = components["schemas"]["PlatformProducts"];

export const PRODUCT_LINES: readonly ProductLine[] = ["spot", "usdt_m", "coin_m"];

/** Which lines are open. */
export type OpenProducts = Readonly<Record<ProductLine, boolean>>;

/**
 * Every line open: what the sites go by until the switches are read, and
 * when they cannot be (the server keeps a switch it has no row for open
 * too), so a failing request never hides a product.
 */
export const ALL_OPEN: OpenProducts = { spot: true, usdt_m: true, coin_m: true };

export const productsKey = ["platform", "products"] as const;

/** useProducts reads the switches, again every minute (a change shows within one). */
export function useProducts() {
  return useQuery({
    queryKey: productsKey,
    queryFn: () => unwrap(platformApi.GET("/v1/platform/products")),
    staleTime: 30_000,
    refetchInterval: 60_000,
  });
}

/** openProducts reads which lines are open; a line missing from the answer is open. */
export function openProducts(p: PlatformProducts | undefined): OpenProducts {
  if (!p) return ALL_OPEN;
  return { spot: p.spot?.enabled !== false, usdt_m: p.usdt_m?.enabled !== false, coin_m: p.coin_m?.enabled !== false };
}

/** useOpenProducts is which lines are open now (all of them until known). */
export function useOpenProducts(): OpenProducts {
  const q = useProducts();
  return useMemo(() => openProducts(q.data), [q.data]);
}

/** allOpen is whether nothing is closed (the lists and menus are as they were). */
export function allOpen(open: OpenProducts): boolean {
  return open.spot && open.usdt_m && open.coin_m;
}

/**
 * productOf names a market's line: a pair is spot; a contract quoted in USD
 * (BTC-USD-PERP, settled in the coin) is coin-margined, one quoted in USDT
 * USDT-margined.
 */
export function productOf(symbol: string): ProductLine {
  if (!isContract(symbol)) return "spot";
  return symbol.split("-")[1] === "USD" ? "coin_m" : "usdt_m";
}

/** isOpen is whether a market's line is open. */
export function isOpen(symbol: string, open: OpenProducts): boolean {
  return open[productOf(symbol)];
}

/**
 * productOfPath names the line of a trading page's address (/trade/BTC-USDT,
 * /futures/BTC-USD-PERP); null for any other page (/futures/data is the
 * futures data overview, not a terminal).
 */
export function productOfPath(path: string): ProductLine | null {
  const m = /^\/(trade|futures)\/([^/?#]+)/.exec(path);
  if (!m) return null;
  const symbol = decodeURIComponent(m[2] ?? "");
  if (m[1] === "trade") return "spot";
  return isContract(symbol) ? productOf(symbol) : null;
}

/**
 * terminalLine names the line of a terminal: the spot terminal's is spot, a
 * futures terminal's its contract's; null when the symbol is no contract
 * (the terminal says it does not know it).
 */
export function terminalLine(kind: "trade" | "futures", symbol: string): ProductLine | null {
  if (kind === "trade") return "spot";
  return isContract(symbol) ? productOf(symbol) : null;
}

/** useTradeEntry is tradeEntry with the switches as read now. */
export function useTradeEntry(last?: string | null): string {
  return tradeEntry(last, useOpenProducts());
}

/** The coin-margined contract a trade entry opens before any was visited. */
const DEFAULT_COIN_CONTRACT = "BTC-USD-PERP";

/**
 * tradeEntry is where a "trade" entry leads: the trading page visited last
 * while its line is open, else the first open line's default (spot, then
 * USDT-margined, then coin-margined); with every line closed, the last one
 * (its page says so).
 */
export function tradeEntry(last: string | null | undefined, open: OpenProducts): string {
  const line = last ? productOfPath(last) : null;
  if (last && line && open[line]) return last;
  if (open.spot) return routes.trade(DEFAULT_SYMBOL);
  if (open.usdt_m) return routes.futures(DEFAULT_CONTRACT);
  if (open.coin_m) return routes.futures(DEFAULT_COIN_CONTRACT);
  return last ?? routes.trade(DEFAULT_SYMBOL);
}
