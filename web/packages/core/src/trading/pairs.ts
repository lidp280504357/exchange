import { useQuery } from "@tanstack/react-query";
import { marketApi, unwrap } from "../api/client";
import type { components } from "../api/gen/market";
import { mul, normalize } from "../format/decimal";
import { qk } from "../query/keys";

// Reference data every trading page needs: the pairs with their rules and
// the assets with their precisions. Both change rarely; one fetch serves
// every page (and the index.html preload warms the pairs).

export type Pair = components["schemas"]["TradingPair"];
export type AssetInfo = components["schemas"]["Asset"];
export type Contract = components["schemas"]["Contract"];

/** usePairs returns the spot pairs (cached for a minute). */
export function usePairs() {
  return useQuery({ queryKey: qk.pairs, queryFn: () => unwrap(marketApi.GET("/v1/market/pairs")), staleTime: 60_000 });
}

/** usePair finds one pair by symbol (case-insensitive), with the query's state. */
export function usePair(symbol: string) {
  const q = usePairs();
  const s = symbol.toUpperCase();
  return { ...q, pair: q.data?.pairs.find((p) => p.symbol === s) };
}

/** useAssets returns the assets with their precisions (cached for a minute). */
export function useAssets() {
  return useQuery({ queryKey: qk.assets, queryFn: () => unwrap(marketApi.GET("/v1/market/assets")), staleTime: 60_000 });
}

/** assetDecimals is an asset's precision, or the fallback while unknown. */
export function assetDecimals(assets: AssetInfo[] | undefined, asset: string, fallback = 8): number {
  return assets?.find((a) => a.asset_code === asset)?.decimals ?? fallback;
}

/** useContracts returns the perpetual contracts (cached for a minute). */
export function useContracts() {
  return useQuery({ queryKey: qk.contracts, queryFn: () => unwrap(marketApi.GET("/v1/market/contracts")), staleTime: 60_000 });
}

/** useContract finds one contract by symbol, with the query's state. */
export function useContract(symbol: string) {
  const q = useContracts();
  const s = symbol.toUpperCase();
  return { ...q, contract: q.data?.contracts.find((c) => c.symbol === s) };
}

/** tradable reports whether new orders are accepted (CANCEL_ONLY still cancels). */
export function tradable(status: string | undefined): boolean {
  return status === "TRADING";
}

/**
 * bookSteps are the aggregation steps an order book offers: the tick and
 * its multiples by 10, 100 and 1000.
 */
export function bookSteps(tickSize: string, count = 4): string[] {
  const out = [normalize(tickSize)];
  for (let i = 1; i < count; i++) out.push(normalize(mul(out[i - 1]!, "10")));
  return out;
}
