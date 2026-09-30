import { createContext, createElement, useCallback, useContext, useEffect, useMemo, useRef, useSyncExternalStore, type ReactNode } from "react";
import type { WsClient, WsStatus } from "../ws/client";
import type { MarketPush, PrivatePush, TickerData, TradeData } from "../ws/types";
import type { BookView } from "./orderbook";
import type { MarketStore } from "./store";

// React access to the WebSocket client and the market store. One provider
// per app, above the router, so the connection and the books outlive page
// changes.

type Live = { ws: WsClient; market: MarketStore };

const LiveContext = createContext<Live | null>(null);

export function LiveProvider({ ws, market, children }: Live & { children: ReactNode }) {
  const value = useMemo(() => ({ ws, market }), [ws, market]);
  return createElement(LiveContext.Provider, { value }, children);
}

function useLive(): Live {
  const live = useContext(LiveContext);
  if (!live) throw new Error("useLive outside LiveProvider");
  return live;
}

export const useWs = () => useLive().ws;
export const useMarket = () => useLive().market;

/** useWsStatus follows the connection (for the offline banner). */
export function useWsStatus(): WsStatus {
  const ws = useWs();
  const subscribe = useCallback((fn: () => void) => ws.onStatus(fn), [ws]);
  return useSyncExternalStore(
    subscribe,
    () => ws.status,
    () => "idle" as WsStatus,
  );
}

/**
 * useChannel calls handler with every message of a channel while the
 * component is mounted (and enabled). The handler may change between
 * renders without resubscribing.
 */
export function useChannel<T = unknown>(channel: string | null, handler: (msg: MarketPush<T> | PrivatePush<T>) => void): void {
  const ws = useWs();
  const ref = useRef(handler);
  ref.current = handler;
  useEffect(() => {
    if (!channel) return;
    return ws.subscribe(channel, (m) => ref.current(m as MarketPush<T> | PrivatePush<T>));
  }, [ws, channel]);
}

/** useSyncing reports whether a depth channel waits for its snapshot. */
export function useSyncing(channel: string): boolean {
  const ws = useWs();
  const subscribe = useCallback((fn: () => void) => ws.onSync((ch) => ch === channel && fn()), [ws, channel]);
  return useSyncExternalStore(
    subscribe,
    () => ws.isSyncing(channel),
    () => false,
  );
}

/**
 * useOrderBook follows a symbol's depth and returns its best `depth`
 * levels per side, merged into steps of `step`; re-renders at most once
 * per frame.
 */
export function useOrderBook(symbol: string, depth: number, step = ""): BookView {
  const market = useMarket();
  useEffect(() => (symbol ? market.followDepth(symbol) : undefined), [market, symbol]);
  const key = `depth:${symbol}`;
  const subscribe = useCallback((fn: () => void) => market.subscribe(key, fn), [market, key]);
  const version = useSyncExternalStore(
    subscribe,
    () => market.version(key),
    () => 0,
  );
  // version changes with every applied message; the view is cut only when
  // a frame's notification re-renders.
  return useMemo(() => market.book(symbol).view(depth, step), [market, symbol, depth, step, version]);
}

/** useTrades follows a symbol's public trades, newest first. */
export function useTrades(symbol: string): TradeData[] {
  const market = useMarket();
  useEffect(() => (symbol ? market.followTrades(symbol) : undefined), [market, symbol]);
  const subscribe = useCallback((fn: () => void) => market.subscribe(`trades:${symbol}`, fn), [market, symbol]);
  return useSyncExternalStore(
    subscribe,
    () => market.recentTrades(symbol),
    () => market.recentTrades(symbol),
  );
}

/** useTicker follows one symbol's ticker. */
export function useTicker(symbol: string): TickerData | undefined {
  const market = useMarket();
  useEffect(() => (symbol ? market.followTicker(symbol) : undefined), [market, symbol]);
  const subscribe = useCallback((fn: () => void) => market.subscribe(`ticker:${symbol}`, fn), [market, symbol]);
  return useSyncExternalStore(
    subscribe,
    () => market.ticker(symbol),
    () => undefined,
  );
}

/** useTickers follows every ticker in one subscription (market lists). */
export function useTickers(): ReadonlyMap<string, TickerData> {
  const market = useMarket();
  useEffect(() => market.followTickers(), [market]);
  const subscribe = useCallback((fn: () => void) => market.subscribe("tickers", fn), [market]);
  return useSyncExternalStore(
    subscribe,
    () => market.allTickers(),
    () => market.allTickers(),
  );
}
