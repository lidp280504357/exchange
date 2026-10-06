import { useQueries, type QueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { marginApi, unwrap } from "../api/client";
import { ApiError } from "../api/errors";
import { accountKeys } from "../assets/hooks";
import { useTerminalPrefs } from "../trading/prefs";
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

/**
 * afterMarginOrder settles what a terminal shows of margin trading after an
 * order: placed on a margin account, its account and what it may borrow
 * reload; refused as MARGIN_DISABLED for AUTO_BORROW (margin.auto_borrow
 * off), the side effect goes back to NONE; refused as MARGIN_DISABLED
 * otherwise, the eligibility reloads and the terminal falls back to SPOT.
 */
export function afterMarginOrder(qc: QueryClient, account: TradeAccount | undefined, err?: unknown): void {
  if (err instanceof ApiError && err.code === "MARGIN_DISABLED") {
    if (err.details.flag === "margin.auto_borrow") useTerminalPrefs.getState().set({ sideEffect: "NONE" });
    else void qc.invalidateQueries({ queryKey: accountKeys.eligibility("MARGIN_TRADE", "") });
    return;
  }
  if (!err && account && account !== "SPOT") void qc.invalidateQueries({ queryKey: marginKeys.all });
}

/** freezeAsset is what an order of a side spends: the quote of a buy, the base of a sell. */
export function freezeAsset(pair: { base_asset: string; quote_asset: string }, side: "BUY" | "SELL"): string {
  return side === "SELL" ? pair.base_asset : pair.quote_asset;
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
 * useMarginSupport tells whether margin trading is open to the caller,
 * which margin accounts the pair trades from and which assets may be
 * borrowed (lends: what may be borrowed of the others is not asked).
 */
export function useMarginSupport(pair: { symbol: string; base_asset: string; quote_asset: string }) {
  const open = useMarginOpen();
  // The terms load only for whom margin trading is open.
  const assets = useMarginAssets(open.open);
  const pairs = useMarginPairs(open.open);
  const support = marginSupport(pair, assets.data, pairs.data?.items);
  const lends = useMemo(() => new Set((assets.data ?? []).filter((a) => a.borrowable).map((a) => a.asset)), [assets.data]);
  return { open: open.open, support, supported: support.cross || support.isolated !== null, lends };
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
  const { open, support, supported, lends } = useMarginSupport(pair);
  const pairs = useMarginPairs(open);
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
      enabled: signedIn && borrow && lends.has(asset),
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
