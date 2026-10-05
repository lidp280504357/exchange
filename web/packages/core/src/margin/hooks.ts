import { useQuery, useQueryClient } from "@tanstack/react-query";
import { marginApi, unwrap } from "../api/client";
import { qk } from "../query/keys";
import { selectSignedIn, useSession } from "../session/store";
import { useEligibility } from "../assets/hooks";
import { retryServerErrors } from "../wallet/hooks";
import { isEmpty, type MarginAccountType, type MarginLoan, type MarginTransfer } from "./math";

// Margin data for the margin pages of both sites (margin design 2026-10-06
// §7): the public terms (assets and pairs), the caller's accounts, loans
// and what they may borrow, and the transfer, borrow and repay actions.
// Accounts are polled every few seconds until the "margin" channel pushes
// them; private keys start with the root "margin", so signing out drops
// them.

export const marginKeys = {
  assets: qk.marginAssets,
  pairs: qk.marginPairs,
  accounts: qk.marginAccounts,
  loans: ["margin", "loans"] as const,
  maxBorrowable: (account: MarginAccountType, symbol: string, asset: string) => ["margin", "max-borrowable", account, symbol, asset] as const,
  all: ["margin"] as const,
};

/** How often the accounts refresh while a page shows them (no pushes yet). */
export const ACCOUNTS_EVERY = 5_000;

/** useMarginAssets lists the margin assets with their pools and rates (public). */
export function useMarginAssets() {
  return useQuery({
    queryKey: marginKeys.assets,
    queryFn: () => unwrap(marginApi.GET("/v1/margin/assets")),
    select: (r) => r.items,
    staleTime: 60_000,
    retry: retryServerErrors,
  });
}

/** useMarginPairs returns the cross account's terms and each pair's isolated terms (public). */
export function useMarginPairs() {
  return useQuery({
    queryKey: marginKeys.pairs,
    queryFn: () => unwrap(marginApi.GET("/v1/margin/pairs")),
    staleTime: 60_000,
    retry: retryServerErrors,
  });
}

/**
 * useMarginAccounts returns the caller's cross account and isolated
 * accounts, refreshed every few seconds unless poll is off.
 */
export function useMarginAccounts({ enabled = true, poll = true }: { enabled?: boolean; poll?: boolean } = {}) {
  const signedIn = useSession(selectSignedIn);
  return useQuery({
    queryKey: marginKeys.accounts,
    queryFn: () => unwrap(marginApi.GET("/v1/margin/accounts")),
    enabled: signedIn && enabled,
    // Without polling (the entry's probe on every assets page) a minute old is fresh enough.
    staleTime: poll ? 2_000 : 60_000,
    refetchInterval: poll ? ACCOUNTS_EVERY : false,
    retry: retryServerErrors,
  });
}

/** useMarginLoans lists the caller's open loans. */
export function useMarginLoans(enabled = true) {
  const signedIn = useSession(selectSignedIn);
  return useQuery({
    queryKey: marginKeys.loans,
    queryFn: () => unwrap(marginApi.GET("/v1/margin/loans")),
    select: (r) => r.items,
    enabled: signedIn && enabled,
    staleTime: 10_000,
    retry: retryServerErrors,
  });
}

/** useMaxBorrowable asks how much of an asset the account may borrow now, and which bound decides it. */
export function useMaxBorrowable(account: MarginAccountType, symbol: string, asset: string, enabled = true) {
  const signedIn = useSession(selectSignedIn);
  return useQuery({
    queryKey: marginKeys.maxBorrowable(account, symbol, asset),
    queryFn: () =>
      unwrap(marginApi.GET("/v1/margin/max-borrowable", { params: { query: { account, asset, symbol: symbol || undefined } } })),
    enabled: signedIn && enabled && asset !== "" && (account === "MARGIN_CROSS" || symbol !== ""),
    staleTime: 5_000,
    retry: retryServerErrors,
  });
}

/** useMarginOpen tells whether margin trading is open to the caller (MARGIN_TRADE: account status, then margin.enabled). */
export function useMarginOpen() {
  const q = useEligibility("MARGIN_TRADE");
  return { open: q.data?.allowed === true, reason: q.data?.reason_code, pending: q.isPending, query: q };
}

/**
 * useMarginEntry tells whether the sites show the way to the margin
 * accounts: margin trading is open to the caller, or they still hold or
 * owe something in a margin account (to repay and move it out once it
 * closed).
 */
export function useMarginEntry(): boolean {
  const { open, pending } = useMarginOpen();
  const accounts = useMarginAccounts({ enabled: !pending && !open, poll: false });
  if (open) return true;
  const d = accounts.data;
  return Boolean(d && (!isEmpty(d.cross) || d.isolated.some((a) => !isEmpty(a))));
}

export type MarginTransferRequest = {
  direction: "IN" | "OUT";
  account: MarginAccountType;
  symbol?: string;
  asset: string;
  amount: string;
};

export type BorrowRequest = { account: MarginAccountType; symbol?: string; asset: string; amount: string };
export type RepayRequest = BorrowRequest;
export type RepayResult = { interest_repaid: string; principal_repaid: string; loan: MarginLoan };

/**
 * useMarginActions returns the transfer, borrow and repay calls, each with
 * the caller's Idempotency-Key (one per intended action: a retry after a
 * lost response acts once). Afterwards the accounts, loans and what may be
 * borrowed reload; the SPOT balances follow by push.
 */
export function useMarginActions() {
  const qc = useQueryClient();
  const settle = () => qc.invalidateQueries({ queryKey: marginKeys.all });
  const header = (key: string) => ({ header: { "Idempotency-Key": key } });
  return {
    transfer: async (body: MarginTransferRequest, idempotencyKey: string): Promise<MarginTransfer> => {
      try {
        return await unwrap(marginApi.POST("/v1/margin/transfer", { params: header(idempotencyKey), body }));
      } finally {
        void settle();
      }
    },
    borrow: async (body: BorrowRequest, idempotencyKey: string): Promise<MarginLoan> => {
      try {
        return await unwrap(marginApi.POST("/v1/margin/borrow", { params: header(idempotencyKey), body }));
      } finally {
        void settle();
      }
    },
    repay: async (body: RepayRequest, idempotencyKey: string): Promise<RepayResult> => {
      try {
        return await unwrap(marginApi.POST("/v1/margin/repay", { params: header(idempotencyKey), body }));
      } finally {
        void settle();
      }
    },
  };
}
