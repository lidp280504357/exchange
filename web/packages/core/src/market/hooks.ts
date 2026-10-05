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

export type OrderBookOptions = {
  /** Levels below this are folded away from the spread (OrderBook.view; displayUnit of the quantity decimals). */
  minQty?: string;
  /** Re-render at most once per this many ms (a book a person reads; charts take every frame). */
  every?: number;
};

/**
 * useOrderBook follows a symbol's depth and returns its best `depth`
 * levels per side, merged into steps of `step`; re-renders at most once
 * per frame, or per `every` ms. A throttled book shows the state its
 * throttle last told it about: a render for another reason (the last
 * trade shown above it) does not bring a newer one in between (B67: 7
 * book redraws a second against 4 notices). Tested in the ui package
 * (src/trading/useOrderBook.test.tsx), where React renders in tests.
 */
export function useOrderBook(symbol: string, depth: number, step = "", { minQty = "", every = 0 }: OrderBookOptions = {}): BookView {
  const market = useMarket();
  useEffect(() => (symbol ? market.followDepth(symbol) : undefined), [market, symbol]);
  const key = `depth:${symbol}`;
  const told = useRef<{ key: string; version: number } | null>(null);
  const subscribe = useCallback(
    (fn: () => void) => {
      if (every <= 0) return market.subscribe(key, fn);
      const t = throttle(() => {
        told.current = { key, version: market.version(key) };
        bookNotified();
        fn();
      }, every);
      const off = market.subscribe(key, t.call);
      return () => {
        off();
        t.cancel();
        // Subscribed again (the same book later), it starts from the store,
        // not from a version passed on before.
        told.current = null;
      };
    },
    [market, key, every],
  );
  // Unthrottled, the store's version; throttled, the version the throttle
  // passed on (the store's until it first has, for this book).
  const getSnapshot = useCallback(() => {
    const last = told.current;
    return every > 0 && last?.key === key ? last.version : market.version(key);
  }, [market, key, every]);
  const version = useSyncExternalStore(subscribe, getSnapshot, () => 0);
  // version changes with every applied message (throttled: every message
  // the throttle passed on); the view is cut only when it does.
  return useMemo(() => market.book(symbol).view(depth, step, minQty), [market, symbol, depth, step, minQty, version]);
}

/**
 * bookNotified notes when a throttled book is told to redraw, for the
 * performance check (web/e2e/perf.mjs sets globalThis.__perfBookNotify to
 * an array before the app loads; design §12.1 times the book from here).
 */
function bookNotified(): void {
  const marks = (globalThis as { __perfBookNotify?: number[] }).__perfBookNotify;
  if (Array.isArray(marks)) marks.push(performance.now());
}

// throttle calls fn at most once per ms: at once after a quiet spell, else
// once at the end of the spell, so the last change always shows.
function throttle(fn: () => void, ms: number): { call: () => void; cancel: () => void } {
  let last = 0;
  let timer: ReturnType<typeof setTimeout> | null = null;
  return {
    call: () => {
      const wait = last + ms - Date.now();
      if (wait <= 0) {
        last = Date.now();
        fn();
      } else if (!timer) {
        timer = setTimeout(() => {
          timer = null;
          last = Date.now();
          fn();
        }, wait);
      }
    },
    cancel: () => {
      if (timer) clearTimeout(timer);
      timer = null;
    },
  };
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
