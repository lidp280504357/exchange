import { useInfiniteQuery, useQuery, useQueryClient, type InfiniteData } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { unwrap, walletApi } from "../api/client";
import { ApiError } from "../api/errors";
import { stepUpHeaders } from "../auth/stepup";
import { useChannel } from "../market/hooks";
import { qk } from "../query/keys";
import { selectSignedIn, useSession } from "../session/store";
import type { PrivatePush } from "../ws/types";
import type { Deposit, WithdrawAddress, Withdrawal } from "./networks";
import { addToBook, applyDepositPush, applyWithdrawalPush, prependItem, removeFromBook, type DepositPush, type Page, type WithdrawalPush } from "./push";

// Wallet data for the deposit and withdrawal pages of both sites (design
// §6.2, §7.2): networks, deposit addresses, the records (kept current by
// the "deposits" and "withdrawals" pushes, never polled), the address book
// and the address check. Every key starts with the private root "wallet",
// so signing out drops them.

export const walletKeys = {
  networks: qk.networks,
  depositAddress: (asset: string, network: string) => ["wallet", "deposit-address", asset, network] as const,
  deposits: qk.deposits,
  withdrawals: qk.withdrawals,
  addressBook: ["wallet", "withdraw-addresses"] as const,
  addressCheck: (network: string, asset: string, address: string) => ["wallet", "address-check", network, asset, address] as const,
};

/**
 * retryServerErrors retries a failed query once when the server or the
 * network failed; a refusal (4xx: not eligible, unknown network) is final.
 */
export function retryServerErrors(failures: number, error: unknown): boolean {
  if (error instanceof ApiError && error.status < 500) return false;
  return failures < 1;
}

/** useDebounced returns value once it has stopped changing for `ms`. */
export function useDebounced<T>(value: T, ms = 400): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setSettled(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return settled;
}

/** useWalletNetworks lists the networks of one asset, or of every asset without one. */
export function useWalletNetworks(asset = "") {
  const signedIn = useSession(selectSignedIn);
  return useQuery({
    queryKey: walletKeys.networks(asset),
    queryFn: () => unwrap(walletApi.GET("/v1/wallet/networks", { params: { query: { asset: asset || undefined } } })),
    select: (r) => r.networks,
    enabled: signedIn,
    staleTime: 60_000,
    retry: retryServerErrors,
  });
}

/**
 * useDepositAddress gets (on first use, assigns) the caller's address for
 * an asset on a network. Refusals (WALLET_NETWORK_DISABLED, USER_NOT_ELIGIBLE,
 * WALLET_UNAVAILABLE) come back at once, without retries.
 */
export function useDepositAddress(asset: string, network: string) {
  const signedIn = useSession(selectSignedIn);
  return useQuery({
    queryKey: walletKeys.depositAddress(asset, network),
    queryFn: () => unwrap(walletApi.GET("/v1/wallet/deposit-address", { params: { query: { asset, network } } })),
    enabled: signedIn && asset !== "" && network !== "",
    staleTime: Number.POSITIVE_INFINITY,
    retry: retryServerErrors,
  });
}

/** useDeposits pages through the caller's deposits, newest first. */
export function useDeposits(pageSize = 20) {
  const signedIn = useSession(selectSignedIn);
  return useInfiniteQuery({
    queryKey: walletKeys.deposits,
    queryFn: ({ pageParam }) =>
      unwrap(walletApi.GET("/v1/wallet/deposits", { params: { query: { limit: pageSize, cursor: pageParam || undefined } } })),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: signedIn,
    staleTime: 30_000,
    retry: retryServerErrors,
  });
}

/** useWithdrawals pages through the caller's withdrawals, newest first. */
export function useWithdrawals(pageSize = 20) {
  const signedIn = useSession(selectSignedIn);
  return useInfiniteQuery({
    queryKey: walletKeys.withdrawals,
    queryFn: ({ pageParam }) =>
      unwrap(walletApi.GET("/v1/wallet/withdrawals", { params: { query: { limit: pageSize, cursor: pageParam || undefined } } })),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: signedIn,
    staleTime: 30_000,
    retry: retryServerErrors,
  });
}

/** useWithdrawAddresses returns the caller's withdrawal address book, newest first. */
export function useWithdrawAddresses() {
  const signedIn = useSession(selectSignedIn);
  return useQuery({
    queryKey: walletKeys.addressBook,
    queryFn: () => unwrap(walletApi.GET("/v1/wallet/withdraw-addresses")),
    select: (r) => r.items,
    enabled: signedIn,
    staleTime: 30_000,
    retry: retryServerErrors,
  });
}

export type AddressCheckInput = {
  network: string;
  address: string;
  /** Picks the network's asset when several share it. */
  asset?: string;
  /** Ask the server only when the local check passed. */
  enabled: boolean;
};

export type AddressVerdict = {
  valid: boolean;
  network: string;
  address_format: "EVM" | "TRON" | "BTC";
  normalized: string | null;
  reason: "ADDRESS_FORMAT" | "ADDRESS_CHECKSUM" | "ADDRESS_NETWORK" | "MEMO_REQUIRED" | "ADDRESS_OWN" | null;
  internal: boolean;
};

export type AddressCheckState = {
  /** The server's verdict on the address as typed now; undefined until it answers. */
  data: AddressVerdict | undefined;
  error: unknown;
  /** No verdict for the current text yet (typing, or the request is out). */
  isPending: boolean;
  isFetching: boolean;
  /** The text changed less than 400 ms ago: the check waits for the typing to stop. */
  settling: boolean;
};

/**
 * useAddressCheck asks the server about an address once the user stops
 * typing (400 ms): its form, its checksum, whether it is the caller's own
 * deposit address (refused) or another user's (an internal transfer
 * without fee). An answer about earlier text is never passed off as one
 * about the current text.
 */
export function useAddressCheck({ network, address, asset = "", enabled }: AddressCheckInput): AddressCheckState {
  const signedIn = useSession(selectSignedIn);
  const typed = address.trim();
  const settled = useDebounced(typed, 400);
  const fresh = settled === typed;
  const q = useQuery({
    queryKey: walletKeys.addressCheck(network, asset, settled),
    queryFn: () =>
      unwrap(walletApi.POST("/v1/wallet/withdraw-addresses/validate", { body: { network, address: settled, asset: asset || undefined } })),
    enabled: signedIn && enabled && network !== "" && settled !== "" && fresh,
    staleTime: 5 * 60_000,
    retry: retryServerErrors,
  });
  const on = enabled && typed !== "";
  return {
    data: on && fresh ? q.data : undefined,
    error: on && fresh ? q.error : null,
    isPending: on && (!fresh || q.isPending),
    isFetching: on && q.isFetching,
    settling: on && !fresh,
  };
}

/**
 * useWalletPushes patches the cached deposits and withdrawals with their
 * pushes the moment they arrive (the private sync reloads them after).
 */
export function useWalletPushes(): void {
  const qc = useQueryClient();
  useChannel<DepositPush>("deposits", (m) => {
    const p = (m as PrivatePush<DepositPush>).data;
    qc.setQueryData<InfiniteData<Page<Deposit>>>(walletKeys.deposits, (data) => applyDepositPush(data, p));
  });
  useChannel<WithdrawalPush>("withdrawals", (m) => {
    const p = (m as PrivatePush<WithdrawalPush>).data;
    qc.setQueryData<InfiniteData<Page<Withdrawal>>>(walletKeys.withdrawals, (data) => applyWithdrawalPush(data, p));
  });
}

/** useWalletActions saves and removes addresses and requests or cancels withdrawals, keeping the caches current. */
export function useWalletActions() {
  const qc = useQueryClient();
  return {
    /** addAddress saves an address to the book (needs a step-up token); it is usable from usable_at. */
    addAddress: async (body: { network: string; address: string; label?: string }, stepUpToken: string): Promise<WithdrawAddress> => {
      const entry = await unwrap(walletApi.POST("/v1/wallet/withdraw-addresses", { params: { header: stepUpHeaders(stepUpToken) }, body }));
      qc.setQueryData<{ items: WithdrawAddress[] }>(walletKeys.addressBook, (book) => addToBook(book, entry));
      return entry;
    },
    removeAddress: async (id: string): Promise<void> => {
      await unwrap(walletApi.DELETE("/v1/wallet/withdraw-addresses/{id}", { params: { path: { id } } }));
      qc.setQueryData<{ items: WithdrawAddress[] }>(walletKeys.addressBook, (book) => removeFromBook(book, id));
    },
    /**
     * requestWithdrawal freezes the amount and fee (needs a step-up token);
     * the idempotency key makes a retry after a lost response count once.
     */
    requestWithdrawal: async (
      body: { asset: string; network: string; address: string; amount: string },
      stepUpToken: string,
      idempotencyKey: string,
    ): Promise<Withdrawal> => {
      const w = await unwrap(
        walletApi.POST("/v1/wallet/withdrawals", { params: { header: { ...stepUpHeaders(stepUpToken), "Idempotency-Key": idempotencyKey } }, body }),
      );
      qc.setQueryData<InfiniteData<Page<Withdrawal>>>(walletKeys.withdrawals, (data) => prependItem(data, w, (x) => x.id));
      return w;
    },
    cancelWithdrawal: async (id: string): Promise<Withdrawal> => {
      const w = await unwrap(walletApi.DELETE("/v1/wallet/withdrawals/{id}", { params: { path: { id } } }));
      qc.setQueryData<InfiniteData<Page<Withdrawal>>>(walletKeys.withdrawals, (data) => prependItem(data, w, (x) => x.id));
      return w;
    },
  };
}
