import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { derivativesApi, marketApi, unwrap } from "../api/client";
import type { components } from "../api/gen/derivatives";
import type { components as MarketComponents } from "../api/gen/market";
import { useChannel } from "../market/hooks";
import { qk } from "../query/keys";
import { selectSignedIn, useSession } from "../session/store";
import { channels, type MarkData, type MarketPush } from "../ws/types";
import { contractMath, type ContractMath, type ContractTerms } from "./coinMargined";
import { assetDecimals, useAssets } from "./pairs";

// Perpetual contracts for the futures terminal (api/openapi/derivatives.yaml).
// Every query lives under the "derivatives" root: position, risk, futures
// balance and contract order or fill pushes reload them (query/private.ts,
// debounced), so the terminal never polls. The mark price streams from
// mark-price:{symbol} over a REST snapshot.

export type FuturesAccount = components["schemas"]["FuturesAccount"];
export type ContractSettings = components["schemas"]["ContractSettings"];
export type ContractPosition = components["schemas"]["ContractPosition"];
export type ContractOrder = components["schemas"]["ContractOrder"];
export type ContractFill = components["schemas"]["ContractFill"];
export type ConditionalOrder = components["schemas"]["ConditionalOrder"];
export type FundingPayment = components["schemas"]["FundingPayment"];
export type MarkPrice = MarketComponents["schemas"]["MarkPrice"];

export type NewContractOrder = {
  symbol: string;
  side: "BUY" | "SELL";
  position_side?: "BOTH" | "LONG" | "SHORT";
  type: "LIMIT" | "MARKET";
  price?: string;
  quantity: string;
  reduce_only?: boolean;
  time_in_force?: "GTC" | "IOC" | "FOK" | "POST_ONLY";
};

export type NewConditionalOrder = {
  symbol: string;
  position_side?: "BOTH" | "LONG" | "SHORT";
  kind: "TAKE_PROFIT" | "STOP_LOSS";
  trigger_price: string;
  trigger_by?: "MARK" | "LAST";
  order_type?: "MARKET" | "LIMIT";
  price?: string;
  quantity?: string;
};

export const dk = {
  account: (asset = "USDT") => ["derivatives", "account", asset] as const,
  settings: (symbol: string) => ["derivatives", "settings", symbol] as const,
  positions: (symbol: string) => ["derivatives", "positions", symbol] as const,
  openOrders: (symbol: string) => ["derivatives", "orders", symbol, "ACTIVE"] as const,
  orderHistory: (symbol: string) => ["derivatives", "orders", symbol, ""] as const,
  fills: (symbol: string) => ["derivatives", "fills", symbol] as const,
  conditional: (symbol: string) => ["derivatives", "conditional", symbol] as const,
  funding: (symbol: string) => ["derivatives", "funding", symbol] as const,
};

function useSignedIn() {
  return useSession(selectSignedIn);
}

/**
 * useFuturesAccount is the caller's FUTURES account of a settlement asset
 * at the mark prices: USDT's, or the coin's of a coin-margined contract.
 */
export function useFuturesAccount(asset = "USDT") {
  return useQuery({
    queryKey: dk.account(asset),
    queryFn: () => unwrap(derivativesApi.GET("/v1/derivatives/account", { params: { query: { asset: asset === "USDT" ? undefined : asset } } })),
    enabled: useSignedIn() && asset !== "",
    staleTime: 30_000,
  });
}

/**
 * useContractMath is a contract's order arithmetic (./coinMargined) at its
 * settlement asset's decimals: a coin-margined contract's margin and fees
 * round to the coin's.
 */
export function useContractMath(c: ContractTerms): ContractMath {
  const assets = useAssets();
  const decimals = assetDecimals(assets.data?.assets, c.settle_asset || c.quote_asset);
  return useMemo(() => contractMath(c, decimals), [c, decimals]);
}

/** useContractSettings is the caller's position mode, margin mode and leverage on a contract. */
export function useContractSettings(symbol: string) {
  return useQuery({
    queryKey: dk.settings(symbol),
    queryFn: () => unwrap(derivativesApi.GET("/v1/derivatives/settings/{symbol}", { params: { path: { symbol } } })),
    enabled: useSignedIn() && symbol !== "",
    staleTime: 60_000,
  });
}

/** updateContractSettings changes the margin mode, position mode or leverage. */
export function updateContractSettings(symbol: string, patch: { margin_mode?: "CROSS" | "ISOLATED"; position_mode?: "ONE_WAY" | "HEDGE"; leverage?: number }) {
  return unwrap(derivativesApi.PUT("/v1/derivatives/settings/{symbol}", { params: { path: { symbol } }, body: patch }));
}

/** usePositions returns the caller's open positions (of one contract, or all with ""). */
export function usePositions(symbol = "") {
  return useQuery({
    queryKey: dk.positions(symbol),
    queryFn: () => unwrap(derivativesApi.GET("/v1/derivatives/positions", { params: { query: { symbol: symbol || undefined } } })),
    enabled: useSignedIn(),
    staleTime: 30_000,
  });
}

/** useContractOpenOrders returns the active contract orders. */
export function useContractOpenOrders(symbol = "") {
  return useQuery({
    queryKey: dk.openOrders(symbol),
    queryFn: () =>
      unwrap(derivativesApi.GET("/v1/derivatives/orders", { params: { query: { symbol: symbol || undefined, status: "ACTIVE", limit: 100 } } })),
    enabled: useSignedIn(),
    staleTime: 30_000,
  });
}

/** useContractOrderHistory pages through every contract order, newest first. */
export function useContractOrderHistory(symbol = "", enabled = true) {
  const signedIn = useSignedIn();
  return useInfiniteQuery({
    queryKey: dk.orderHistory(symbol),
    queryFn: ({ pageParam }) =>
      unwrap(derivativesApi.GET("/v1/derivatives/orders", { params: { query: { symbol: symbol || undefined, cursor: pageParam || undefined, limit: 50 } } })),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: signedIn && enabled,
    staleTime: 30_000,
  });
}

/** useContractFills pages through the caller's contract fills. */
export function useContractFills(symbol = "", enabled = true) {
  const signedIn = useSignedIn();
  return useInfiniteQuery({
    queryKey: dk.fills(symbol),
    queryFn: ({ pageParam }) =>
      unwrap(derivativesApi.GET("/v1/derivatives/fills", { params: { query: { symbol: symbol || undefined, cursor: pageParam || undefined, limit: 50 } } })),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: signedIn && enabled,
    staleTime: 30_000,
  });
}

/** useConditionalOrders returns the active take-profit and stop-loss orders. */
export function useConditionalOrders(symbol = "") {
  return useQuery({
    queryKey: dk.conditional(symbol),
    queryFn: () =>
      unwrap(
        derivativesApi.GET("/v1/derivatives/conditional-orders", { params: { query: { symbol: symbol || undefined, status: "ACTIVE", limit: 100 } } }),
      ),
    enabled: useSignedIn(),
    staleTime: 30_000,
  });
}

/** useFundingPayments pages through the caller's funding payments. */
export function useFundingPayments(symbol = "", enabled = true) {
  const signedIn = useSignedIn();
  return useInfiniteQuery({
    queryKey: dk.funding(symbol),
    queryFn: ({ pageParam }) =>
      unwrap(derivativesApi.GET("/v1/derivatives/funding", { params: { query: { symbol: symbol || undefined, cursor: pageParam || undefined, limit: 50 } } })),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: signedIn && enabled,
    staleTime: 60_000,
  });
}

/** placeContractOrder submits a contract order (once per idempotency key). */
export function placeContractOrder(order: NewContractOrder, idempotencyKey: string): Promise<ContractOrder> {
  return unwrap(derivativesApi.POST("/v1/derivatives/orders", { body: order, params: { header: { "Idempotency-Key": idempotencyKey } } }));
}

/** cancelContractOrder asks the engine to cancel one contract order. */
export function cancelContractOrder(orderId: string): Promise<ContractOrder> {
  return unwrap(derivativesApi.DELETE("/v1/derivatives/orders/{order_id}", { params: { path: { order_id: orderId } } }));
}

/** cancelAllContractOrders cancels the active contract orders (of one contract when given). */
export async function cancelAllContractOrders(symbol?: string): Promise<number> {
  const res = await unwrap(derivativesApi.DELETE("/v1/derivatives/orders", { params: { query: { symbol: symbol || undefined } } }));
  return res.requested;
}

/** placeConditionalOrder sets a take-profit or stop-loss on an open position. */
export function placeConditionalOrder(order: NewConditionalOrder): Promise<ConditionalOrder> {
  return unwrap(derivativesApi.POST("/v1/derivatives/conditional-orders", { body: order }));
}

/** cancelConditionalOrder cancels an active take-profit or stop-loss. */
export function cancelConditionalOrder(id: string): Promise<ConditionalOrder> {
  return unwrap(derivativesApi.DELETE("/v1/derivatives/conditional-orders/{conditional_id}", { params: { path: { conditional_id: id } } }));
}

/** adjustPositionMargin adds (positive) or removes (negative) an isolated position's margin. */
export function adjustPositionMargin(symbol: string, amount: string, positionSide: "BOTH" | "LONG" | "SHORT" = "BOTH"): Promise<ContractPosition> {
  return unwrap(
    derivativesApi.POST("/v1/derivatives/positions/{symbol}/margin", { params: { path: { symbol } }, body: { amount, position_side: positionSide } }),
  );
}

/**
 * useMarkPrice follows a contract's mark price, index price and funding
 * estimate: a REST snapshot, then mark-price:{symbol} pushes merged in.
 */
export function useMarkPrice(symbol: string) {
  const qc = useQueryClient();
  const key = qk.markPrice(symbol);
  const q = useQuery({
    queryKey: key,
    queryFn: () => unwrap(marketApi.GET("/v1/market/{symbol}/mark-price", { params: { path: { symbol } } })),
    enabled: symbol !== "",
    staleTime: 30_000,
    // The pushes carry prices only; the degraded flag (reduce-only) comes with the snapshot.
    refetchInterval: 30_000,
  });
  useChannel<MarkData>(symbol ? channels.markPrice(symbol) : null, (m) => {
    const d = (m as MarketPush<MarkData>).data;
    qc.setQueryData<MarkPrice>(key, (cur) => (cur ? { ...cur, ...d } : cur));
  });
  return q;
}
