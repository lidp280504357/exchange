import {
  assetDecimals,
  coinName,
  dec,
  DEFAULT_CONTRACT,
  formatDecimal,
  routes,
  useAssets,
  useContracts,
  usePairs,
  useSettings,
  type AssetInfo,
} from "@exchange/core";
import { contractFor, tradeSymbolFor } from "@exchange/core/assets/links";
import { useMemo } from "react";

// Helpers every assets page of the mobile site shares: names, decimals and
// switches of the assets, where "trade" goes, and link building. The PC
// site keeps the same helpers in its own pages/assets/parts/meta.ts.

/** Amounts in lists show at most this many decimals (cut, never rounded up). */
export const LIST_DECIMALS = 8;

/** shownDecimals is the decimals an amount of the asset shows with in lists. */
export function shownDecimals(decimals: number): number {
  return Math.min(decimals, LIST_DECIMALS);
}

/**
 * plainAmount shows a rule amount (a minimum, a fee) as it is, without
 * padding zeros, cut down to the asset's decimals: 0.0002, 10, 1,000.5.
 */
export function plainAmount(v: string | null | undefined, decimals: number): string {
  if (!v || !dec.isDecimal(v)) return formatDecimal(v);
  return formatDecimal(dec.normalize(dec.round(v, decimals, "down")));
}

/** withQuery appends search parameters to a path, skipping empty ones. */
export function withQuery(path: string, params: Record<string, string | null | undefined>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) q.set(k, v);
  const s = q.toString();
  return s ? `${path}?${s}` : path;
}

export type AssetMeta = {
  list: AssetInfo[];
  pending: boolean;
  decimals: (asset: string) => number;
  name: (asset: string) => string;
  canDeposit: (asset: string) => boolean;
  canWithdraw: (asset: string) => boolean;
};

/** useAssetMeta reads the assets (GET /v1/market/assets) with their precisions, names and switches. */
export function useAssetMeta(): AssetMeta {
  const q = useAssets();
  const locale = useSettings((s) => s.locale);
  const data = q.data;
  const pending = q.isPending;
  return useMemo(() => {
    const list = data?.assets ?? [];
    const byCode = new Map(list.map((a) => [a.asset_code, a]));
    return {
      list,
      pending,
      decimals: (asset: string) => assetDecimals(list, asset, 8),
      name: (asset: string) => {
        const n = coinName(asset, locale);
        return n !== asset ? n : (byCode.get(asset)?.name ?? asset);
      },
      canDeposit: (asset: string) => {
        const a = byCode.get(asset);
        return Boolean(a?.deposit_enabled && a.networks.some((n) => n.deposit_enabled));
      },
      canWithdraw: (asset: string) => {
        const a = byCode.get(asset);
        return Boolean(a?.withdraw_enabled && a.networks.some((n) => n.withdraw_enabled));
      },
    };
  }, [data, locale, pending]);
}

export type TradeLinks = {
  /** The spot terminal of the asset's market, or null when none trades it. */
  spot: (asset: string) => string | null;
  /** The futures terminal of the asset's perpetual (or the default one). */
  futures: (asset: string) => string;
};

/** useTradeLinks tells where "trade" goes for an asset: its spot pair, or the futures terminal. */
export function useTradeLinks(): TradeLinks {
  const pairs = usePairs().data?.pairs;
  const contracts = useContracts().data?.contracts;
  return useMemo(
    () => ({
      spot: (asset: string): string | null => {
        const s = tradeSymbolFor(asset, pairs ?? []);
        return s ? routes.trade(s) : null;
      },
      futures: (asset: string): string => routes.futures(contractFor(asset, contracts ?? []) ?? DEFAULT_CONTRACT),
    }),
    [pairs, contracts],
  );
}
