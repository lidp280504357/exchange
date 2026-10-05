import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { tradingApi, unwrap } from "../api/client";
import type { components } from "../api/gen/trading";
import { qk } from "../query/keys";
import { selectSignedIn, useSession } from "../session/store";

// The caller's spot orders and fills (api/openapi/trading.yaml). The
// orders and fills pushes keep these queries current (query/private.ts):
// an order changes in place, a new one joins the open orders, a fill is
// prepended, so pages refetch only on a resync.

export type Order = components["schemas"]["Order"];
export type Fill = components["schemas"]["Fill"];
export type NewOrder = {
  symbol: string;
  side: "BUY" | "SELL";
  type: "LIMIT" | "MARKET";
  price?: string;
  quantity?: string;
  quote_amount?: string;
  time_in_force?: "GTC" | "IOC" | "FOK" | "POST_ONLY";
  /** The account the order trades from (SPOT when absent) and a margin order's side effect. */
  account?: "SPOT" | "MARGIN_CROSS" | "MARGIN_ISOLATED";
  side_effect?: "NONE" | "AUTO_BORROW" | "AUTO_REPAY";
};

const ACTIVE = new Set(["NEW", "OPEN", "PARTIALLY_FILLED"]);

/** isActive reports whether an order can still fill (or be canceled). */
export function isActive(status: string): boolean {
  return ACTIVE.has(status);
}

/** useOpenOrders returns the active orders of a pair ("" for all), newest first. */
export function useOpenOrders(symbol = "") {
  const signedIn = useSession(selectSignedIn);
  return useQuery({
    queryKey: qk.orders(symbol, "ACTIVE"),
    queryFn: () =>
      unwrap(tradingApi.GET("/v1/orders", { params: { query: { status: "ACTIVE", symbol: symbol || undefined, limit: 200 } } })),
    enabled: signedIn,
    staleTime: 30_000,
  });
}

/**
 * useOrderHistory pages through every order of a pair ("" for all),
 * newest first; the page shows the finished ones (isActive is false).
 */
export function useOrderHistory(symbol = "", enabled = true) {
  const signedIn = useSession(selectSignedIn);
  return useInfiniteQuery({
    queryKey: qk.orders(symbol, ""),
    queryFn: ({ pageParam }) =>
      unwrap(tradingApi.GET("/v1/orders", { params: { query: { symbol: symbol || undefined, cursor: pageParam || undefined, limit: 50 } } })),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: signedIn && enabled,
    staleTime: 30_000,
  });
}

/** useFills pages through the caller's fills of a pair ("" for all), newest first. */
export function useFills(symbol = "", enabled = true) {
  const signedIn = useSession(selectSignedIn);
  return useInfiniteQuery({
    queryKey: qk.fills(symbol),
    queryFn: ({ pageParam }) =>
      unwrap(tradingApi.GET("/v1/fills", { params: { query: { symbol: symbol || undefined, cursor: pageParam || undefined, limit: 50 } } })),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: signedIn && enabled,
    staleTime: 30_000,
  });
}

/**
 * placeOrder submits an order. The idempotency key makes a retried click
 * (or a lost response) place it once.
 */
export function placeOrder(order: NewOrder, idempotencyKey: string): Promise<Order> {
  return unwrap(tradingApi.POST("/v1/orders", { body: order, params: { header: { "Idempotency-Key": idempotencyKey } } }));
}

/** cancelOrder asks the engine to cancel one order (CANCELED arrives by push). */
export function cancelOrder(orderId: string): Promise<Order> {
  return unwrap(tradingApi.DELETE("/v1/orders/{order_id}", { params: { path: { order_id: orderId } } }));
}

/** cancelAllOrders cancels every active order, of one pair when given; returns how many were asked. */
export async function cancelAllOrders(symbol?: string): Promise<number> {
  const res = await unwrap(tradingApi.DELETE("/v1/orders", { params: { query: { symbol: symbol || undefined } } }));
  return res.requested;
}

/** newIdempotencyKey is a fresh key for one submission. */
export function newIdempotencyKey(): string {
  return globalThis.crypto?.randomUUID?.() ?? `k-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
