import { useQuery } from "@tanstack/react-query";
import { marketApi, unwrap } from "../api/client";
import type { components } from "../api/gen/market";
import { cmp, isDecimal, mul, normalize, sign } from "../format/decimal";
import { rememberAssets, rememberPairs } from "../markets/profiles";
import { qk } from "../query/keys";

// Reference data every trading page needs: the pairs with their rules and
// the assets with their precisions. Both change rarely; one fetch serves
// every page (and the index.html preload warms the pairs).

export type Pair = components["schemas"]["TradingPair"];
export type AssetInfo = components["schemas"]["Asset"];
export type Contract = components["schemas"]["Contract"];

/** usePairs returns the spot pairs (cached for a minute). */
export function usePairs() {
  return useQuery({
    queryKey: qk.pairs,
    queryFn: async () => {
      const data = await unwrap(marketApi.GET("/v1/market/pairs"));
      rememberPairs(data.pairs);
      return data;
    },
    staleTime: 60_000,
    // Display names and logos operators change show within a minute.
    refetchInterval: 60_000,
  });
}

/** pairName is the name the sites show for a pair's base asset: the display name operators set, else the asset's. */
export function pairName(p: Pick<Pair, "base_asset" | "base_name"> & { base_display_name?: string | null }): string {
  return p.base_display_name || p.base_name || p.base_asset;
}

/** usePair finds one pair by symbol (case-insensitive), with the query's state. */
export function usePair(symbol: string) {
  const q = usePairs();
  const s = symbol.toUpperCase();
  return { ...q, pair: q.data?.pairs.find((p) => p.symbol === s) };
}

/** useAssets returns the assets with their precisions (cached for a minute). */
export function useAssets() {
  return useQuery({
    queryKey: qk.assets,
    queryFn: async () => {
      const data = await unwrap(marketApi.GET("/v1/market/assets"));
      rememberAssets(data.assets);
      return data;
    },
    staleTime: 60_000,
    refetchInterval: 60_000,
  });
}

/**
 * useAssetProfiles keeps the asset profiles (display names, introductions,
 * logos) of ../markets/profiles filled on every page: the sites call it
 * once at their root, so a coin icon shows the uploaded logo even on a page
 * that asks for neither the pairs nor the assets.
 */
export function useAssetProfiles(): void {
  usePairs();
  useAssets();
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

/**
 * defaultBookStep is the step a book opens at before the user picks one:
 * the finest step of at least a 100,000th of the price (BTC at 84,000 → 1,
 * ETH at 2,700 → 0.1). At the tick, a reference book's top levels are dust
 * that comes and goes ten times a second. Without a price, the tick.
 */
export function defaultBookStep(steps: readonly string[], price: string | null | undefined): string {
  if (!price || !isDecimal(price) || sign(price) <= 0) return steps[0] ?? "";
  const want = mul(price, "0.00001");
  return steps.find((s) => cmp(s, want) >= 0) ?? steps[steps.length - 1] ?? "";
}
