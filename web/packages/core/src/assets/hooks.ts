import { useInfiniteQuery, useQuery, useQueryClient, type InfiniteData } from "@tanstack/react-query";
import { useEffect } from "react";
import { accountApi, derivativesApi, marketApi, unwrap, userApi } from "../api/client";
import { ApiError } from "../api/errors";
import type { components as AccountSchemas } from "../api/gen/account";
import type { components as DerivativesSchemas } from "../api/gen/derivatives";
import { useMarket, useTickers } from "../market/hooks";
import { qk } from "../query/keys";
import { selectSignedIn, useSession } from "../session/store";
import { prependItem, type Page } from "../wallet/push";
import { retryServerErrors } from "../wallet/hooks";
import type { AccountType } from "./transfer";

// Account data for the assets pages of both sites (design §6.2, §7.2):
// balances (kept current by the balance pushes, query/private.ts), live
// reference prices, the fund flow, transfers, the futures account and the
// eligibility of a feature. Keys start with private roots ("account",
// "derivatives", "user"), so signing out drops them.

export type Balance = AccountSchemas["schemas"]["Balance"];
export type LedgerEntry = AccountSchemas["schemas"]["LedgerEntry"];
export type Transfer = AccountSchemas["schemas"]["Transfer"];
export type FuturesAccount = DerivativesSchemas["schemas"]["FuturesAccount"];
export type Feature = "SPOT_TRADE" | "DERIVATIVES_TRADE" | "DEPOSIT" | "WITHDRAW" | "TRANSFER";

export const accountKeys = {
  balances: qk.balances,
  ledger: (asset: string, type: string) => [...qk.ledger, asset, type] as const,
  transfers: qk.transfers,
  futuresAccount: ["derivatives", "account"] as const,
  eligibility: (feature: Feature, asset: string) => ["user", "eligibility", feature, asset] as const,
};

/**
 * useBalances returns the caller's balances, both accounts. The cache
 * holds the raw response ({ balances }): the balance pushes patch it in
 * place and other pages (the trade terminal) read the same entry.
 */
export function useBalances() {
  const signedIn = useSession(selectSignedIn);
  return useQuery({
    queryKey: accountKeys.balances,
    queryFn: () => unwrap(accountApi.GET("/v1/account/balances")),
    enabled: signedIn,
    staleTime: 30_000,
  });
}

/** availableOf is an account's available balance of an asset ("0" without one). */
export function availableOf(balances: readonly Balance[] | undefined, account: AccountType, asset: string): string {
  return balances?.find((b) => b.account_type === account && b.asset === asset)?.available ?? "0";
}

/**
 * useLiveTickers returns every ticker, live from the tickers channel and
 * seeded by GET /v1/market/tickers (the reference prices of valuations).
 */
export function useLiveTickers() {
  const market = useMarket();
  const tickers = useTickers();
  const rest = useQuery({ queryKey: qk.tickers, queryFn: () => unwrap(marketApi.GET("/v1/market/tickers")), staleTime: 5_000 });
  useEffect(() => {
    if (rest.data) market.seedTickers(rest.data.tickers);
  }, [market, rest.data]);
  return { tickers, pending: rest.isPending && tickers.size === 0, error: tickers.size === 0 ? rest.error : null, refetch: rest.refetch };
}

/** useLedger pages through the fund flow, newest first, by asset and entry type ("" for all). */
export function useLedger(filter: { asset?: string; type?: string } = {}, pageSize = 50) {
  const signedIn = useSession(selectSignedIn);
  const asset = filter.asset ?? "";
  const type = filter.type ?? "";
  return useInfiniteQuery({
    queryKey: accountKeys.ledger(asset, type),
    queryFn: ({ pageParam }) =>
      unwrap(
        accountApi.GET("/v1/account/ledger", {
          params: { query: { asset: asset || undefined, type: type || undefined, limit: pageSize, cursor: pageParam || undefined } },
        }),
      ),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: signedIn,
    staleTime: 30_000,
    retry: retryServerErrors,
  });
}

/** useTransfers pages through the caller's transfers, newest first. */
export function useTransfers(pageSize = 20) {
  const signedIn = useSession(selectSignedIn);
  return useInfiniteQuery({
    queryKey: accountKeys.transfers,
    queryFn: ({ pageParam }) =>
      unwrap(accountApi.GET("/v1/account/transfers", { params: { query: { limit: pageSize, cursor: pageParam || undefined } } })),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: signedIn,
    staleTime: 30_000,
    retry: retryServerErrors,
  });
}

export type TransferRequest = { asset: string; amount: string; from_account_type: AccountType; to_account_type: AccountType };

/**
 * useTransferAction returns a function that moves funds between the
 * accounts. The balances follow by push; the new transfer joins the
 * cached list, and a refused one (kept by the ledger as FAILED) reloads it.
 */
export function useTransferAction() {
  const qc = useQueryClient();
  return async (body: TransferRequest, idempotencyKey: string): Promise<Transfer> => {
    try {
      const t = await unwrap(accountApi.POST("/v1/account/transfers", { params: { header: { "Idempotency-Key": idempotencyKey } }, body }));
      qc.setQueryData<InfiniteData<Page<Transfer>>>(accountKeys.transfers, (data) => prependItem(data, t, (x) => x.transfer_id));
      return t;
    } catch (err) {
      if (err instanceof ApiError && err.details.transfer_id) void qc.invalidateQueries({ queryKey: accountKeys.transfers });
      throw err;
    }
  };
}

/** useFuturesAccount returns the FUTURES account at the mark prices (margin balance, unrealized PnL, transferable). */
export function useFuturesAccount(enabled = true) {
  const signedIn = useSession(selectSignedIn);
  return useQuery({
    queryKey: accountKeys.futuresAccount,
    queryFn: () => unwrap(derivativesApi.GET("/v1/derivatives/account")),
    enabled: signedIn && enabled,
    staleTime: 10_000,
    retry: retryServerErrors,
  });
}

/**
 * useEligibility asks whether the caller may use a feature now (account
 * status, then the feature's switch): { allowed, reason_code }.
 */
export function useEligibility(feature: Feature, asset = "") {
  const signedIn = useSession(selectSignedIn);
  return useQuery({
    queryKey: accountKeys.eligibility(feature, asset),
    queryFn: () => unwrap(userApi.GET("/v1/user/eligibility", { params: { query: { feature, asset: asset || undefined } } })),
    enabled: signedIn,
    staleTime: 60_000,
    retry: retryServerErrors,
  });
}
