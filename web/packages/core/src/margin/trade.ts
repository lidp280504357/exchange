import { useQueries } from "@tanstack/react-query";
import { marginApi, unwrap } from "../api/client";
import * as dec from "../format/decimal";
import { selectSignedIn, useSession } from "../session/store";
import { retryServerErrors } from "../wallet/hooks";
import { accountOf } from "./form";
import { marginKeys, useMarginAccounts, useMarginAssets, useMarginOpen, useMarginPairs } from "./hooks";
import { balanceOf, type MarginAccount, type MarginAsset, type MarginPair } from "./math";

// The trade terminals' margin mode (margin design 2026-10-06 §7): which
// accounts a pair can trade from, and what the order form may spend there.

export type TradeAccount = "SPOT" | "MARGIN_CROSS" | "MARGIN_ISOLATED";
export type SideEffect = "NONE" | "AUTO_BORROW" | "AUTO_REPAY";

/** The accounts a pair can trade from besides SPOT. */
export type MarginSupport = { cross: boolean; isolated: MarginPair | null };

/**
 * marginSupport tells whether a pair trades from the cross account (both
 * its coins count as margin there) and from an isolated account of its
 * own (the pair takes isolated accounts).
 */
export function marginSupport(
  pair: { symbol: string; base_asset: string; quote_asset: string },
  assets: Pick<MarginAsset, "asset" | "collateral">[] | undefined,
  pairs: MarginPair[] | undefined,
): MarginSupport {
  const collateral = new Set((assets ?? []).filter((a) => a.collateral).map((a) => a.asset));
  return {
    cross: collateral.has(pair.base_asset) && collateral.has(pair.quote_asset),
    isolated: pairs?.find((p) => p.symbol === pair.symbol && p.isolated) ?? null,
  };
}

/** marginTag is the short label of an order's margin account ("CROSS", "ISOLATED"), null for SPOT. */
export function marginTag(account: string | null | undefined): "CROSS" | "ISOLATED" | null {
  if (account === "MARGIN_CROSS") return "CROSS";
  if (account === "MARGIN_ISOLATED") return "ISOLATED";
  return null;
}

/**
 * spendable is what an order on a margin account may freeze of an asset:
 * the account's free balance and, with AUTO_BORROW, what it may borrow.
 */
export function spendable(owner: Pick<MarginAccount, "balances"> | undefined, asset: string, borrowable: string | undefined, effect: SideEffect): string {
  const free = balanceOf(owner, asset).free;
  if (effect !== "AUTO_BORROW" || !borrowable || !dec.isDecimal(borrowable)) return dec.normalize(free);
  return dec.normalize(dec.add(free, dec.max(borrowable, "0")));
}

/**
 * useMarginSupport tells whether margin trading is open to the caller and
 * which margin accounts the pair trades from.
 */
export function useMarginSupport(pair: { symbol: string; base_asset: string; quote_asset: string }) {
  const open = useMarginOpen();
  const assets = useMarginAssets();
  const pairs = useMarginPairs();
  const support = marginSupport(pair, assets.data, pairs.data?.items);
  return { open: open.open, support, supported: support.cross || support.isolated !== null };
}

/**
 * tradeAccountFor is the account an order goes to: the one chosen, or
 * SPOT where margin trading is not open or the pair has no such account.
 */
export function tradeAccountFor(chosen: TradeAccount, open: boolean, support: MarginSupport): TradeAccount {
  if (!open) return "SPOT";
  if (chosen === "MARGIN_CROSS" && !support.cross) return "SPOT";
  if (chosen === "MARGIN_ISOLATED" && !support.isolated) return "SPOT";
  return chosen;
}

/**
 * useMarginTrade gives a terminal what its margin mode needs for a pair:
 * whether margin trading is open to the caller, the accounts the pair
 * supports, the chosen account's data, and the base and quote the order
 * form may spend (with what may be borrowed under AUTO_BORROW).
 */
export function useMarginTrade(
  pair: { symbol: string; base_asset: string; quote_asset: string },
  account: TradeAccount,
  effect: SideEffect,
) {
  const signedIn = useSession(selectSignedIn);
  const { open, support, supported } = useMarginSupport(pair);
  const pairs = useMarginPairs();
  const margin = account !== "SPOT";
  const accounts = useMarginAccounts({ enabled: margin, poll: margin });
  const symbol = account === "MARGIN_ISOLATED" ? pair.symbol : "";
  const owner = margin ? accountOf(accounts.data, account, symbol) : undefined;
  const borrow = margin && effect === "AUTO_BORROW";
  const [base, quote] = useQueries({
    queries: [pair.base_asset, pair.quote_asset].map((asset) => ({
      queryKey: marginKeys.maxBorrowable(account === "SPOT" ? "MARGIN_CROSS" : account, symbol, asset),
      queryFn: () =>
        unwrap(
          marginApi.GET("/v1/margin/max-borrowable", {
            params: { query: { account: account === "SPOT" ? "MARGIN_CROSS" : account, asset, symbol: symbol || undefined } },
          }),
        ),
      enabled: signedIn && borrow,
      staleTime: 5_000,
      refetchInterval: borrow ? 10_000 : (false as const),
      retry: retryServerErrors,
    })),
  });
  const available =
    margin && signedIn && accounts.data
      ? {
          base: spendable(owner, pair.base_asset, base?.data?.amount, effect),
          quote: spendable(owner, pair.quote_asset, quote?.data?.amount, effect),
        }
      : null;
  return {
    open,
    support,
    supported,
    owner,
    terms: account === "MARGIN_ISOLATED" ? support.isolated : (pairs.data?.cross ?? null),
    available,
    pending: margin && accounts.isPending,
  };
}
