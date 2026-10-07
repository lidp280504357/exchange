import {
  assetDecimals,
  coinName,
  dec,
  DEFAULT_CONTRACT,
  enumLabel,
  formatDecimal,
  i18n,
  routes,
  useAssets,
  useContracts,
  usePairs,
  useSettings,
  type AssetInfo,
} from "@exchange/core";
import { contractFor, tradeSymbolFor } from "@exchange/core/assets/links";
import { futuresLineOf, useOpenProducts } from "@exchange/core/platform/products";
import { useMemo } from "react";

// Helpers every assets page shares: names, decimals and flags of the
// assets, where "trade" goes, and labels of API enums.

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

/**
 * codeLabel names an API enum value: the shared dictionary (enumLabel), or
 * this area's words for the few codes it does not have yet.
 */
export function codeLabel(code: string | null | undefined, kind?: string): string {
  if (!code) return "—";
  const own = `pcAssets.codes.${code}`;
  if (!i18n.exists(`codes.${code}`) && i18n.exists(own)) return i18n.t(own);
  return enumLabel(code, kind);
}

export type AssetMeta = {
  list: AssetInfo[];
  pending: boolean;
  error: unknown;
  refetch: () => void;
  decimals: (asset: string) => number;
  name: (asset: string) => string;
  rank: (asset: string) => number;
  canDeposit: (asset: string) => boolean;
  canWithdraw: (asset: string) => boolean;
};

/** useAssetMeta reads the assets (GET /v1/market/assets) with their precisions, names and switches. */
export function useAssetMeta(): AssetMeta {
  const q = useAssets();
  const locale = useSettings((s) => s.locale);
  const data = q.data;
  const { refetch } = q;
  return useMemo(() => {
    const list = data?.assets ?? [];
    const byCode = new Map(list.map((a) => [a.asset_code, a]));
    return {
      list,
      pending: q.isPending,
      error: q.error,
      refetch: () => void refetch(),
      decimals: (asset: string) => assetDecimals(list, asset, 8),
      name: (asset: string) => {
        const n = coinName(asset, locale);
        return n !== asset ? n : (byCode.get(asset)?.name ?? asset);
      },
      rank: (asset: string) => byCode.get(asset)?.rank ?? Number.MAX_SAFE_INTEGER,
      canDeposit: (asset: string) => {
        const a = byCode.get(asset);
        return Boolean(a?.deposit_enabled && a.networks.some((n) => n.deposit_enabled));
      },
      canWithdraw: (asset: string) => {
        const a = byCode.get(asset);
        return Boolean(a?.withdraw_enabled && a.networks.some((n) => n.withdraw_enabled));
      },
    };
  }, [data, locale, q.isPending, q.error, refetch]);
}

/** useTradeLinks tells where "trade" goes for an asset: its spot pair, or the futures terminal. */
export function useTradeLinks() {
  const pairs = usePairs().data?.pairs;
  const contracts = useContracts().data?.contracts;
  // A closed product line's terminal is no way to trade (design 2026-10-07, product line switches §1 #2).
  const open = useOpenProducts();
  return useMemo(
    () => ({
      spot: (asset: string): string | null => {
        const s = open.spot ? tradeSymbolFor(asset, pairs ?? []) : null;
        return s ? routes.trade(s) : null;
      },
      futures: (asset: string): string | null =>
        open[futuresLineOf(asset)] ? routes.futures(contractFor(asset, contracts ?? []) ?? DEFAULT_CONTRACT) : null,
    }),
    [pairs, contracts, open],
  );
}

/** withQuery appends search parameters to a path, skipping empty ones. */
export function withQuery(path: string, params: Record<string, string | null | undefined>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) q.set(k, v);
  const s = q.toString();
  return s ? `${path}?${s}` : path;
}
