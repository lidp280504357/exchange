import type { WsClient } from "../ws/client";
import { channels, type DepthData, type MarketPush, type TickerData, type TradeData } from "../ws/types";
import { OrderBook } from "./orderbook";

// MarketStore holds the high-frequency market data outside React (design
// §4.3): order books, the latest trades and every ticker. Messages update
// it as they arrive; subscribers are told at most once per animation frame,
// so ten depth messages a second and a busy tickers channel cost one
// render per frame, not one per message. Components read it through the
// hooks in ./hooks (useSyncExternalStore).

const TRADES_KEPT = 60;

type Notify = () => void;

/** Frame schedules a callback once per animation frame (tests pass a sync one). */
export type Frame = (cb: () => void) => void;

const defaultFrame: Frame = (cb) =>
  typeof requestAnimationFrame === "function" ? void requestAnimationFrame(cb) : void setTimeout(cb, 16);

export class MarketStore {
  private books = new Map<string, OrderBook>();
  private trades = new Map<string, TradeData[]>();
  private tickers = new Map<string, TickerData>();
  private listeners = new Map<string, Set<Notify>>();
  private dirty = new Set<string>();
  private scheduled = false;
  private versions = new Map<string, number>();
  /** Unsubscribers of the channels this store follows, by key. */
  private follows = new Map<string, { count: number; off: () => void }>();

  constructor(
    private ws: WsClient,
    private frame: Frame = defaultFrame,
  ) {}

  /** version of a key changes whenever its data changes (for snapshots). */
  version(key: string): number {
    return this.versions.get(key) ?? 0;
  }

  /** subscribe is useSyncExternalStore's subscribe for one key. */
  subscribe(key: string, fn: Notify): () => void {
    let set = this.listeners.get(key);
    if (!set) {
      set = new Set();
      this.listeners.set(key, set);
    }
    set.add(fn);
    return () => {
      set.delete(fn);
    };
  }

  private changed(key: string) {
    this.versions.set(key, this.version(key) + 1);
    this.dirty.add(key);
    if (this.scheduled) return;
    this.scheduled = true;
    this.frame(() => {
      this.scheduled = false;
      const keys = [...this.dirty];
      this.dirty.clear();
      for (const k of keys) for (const fn of this.listeners.get(k) ?? []) fn();
    });
  }

  // follow subscribes to a channel while anyone reads it (counted).
  private follow(key: string, channel: string, onMessage: (m: MarketPush) => void): () => void {
    const f = this.follows.get(key);
    if (f) {
      f.count++;
    } else {
      this.follows.set(key, { count: 1, off: this.ws.subscribe(channel, (m) => onMessage(m as MarketPush)) });
    }
    return () => {
      const cur = this.follows.get(key);
      if (!cur) return;
      cur.count--;
      if (cur.count > 0) return;
      this.follows.delete(key);
      cur.off(); // the client keeps the channel another 15 s
    };
  }

  /** book returns a symbol's order book (empty until its snapshot). */
  book(symbol: string): OrderBook {
    let b = this.books.get(symbol);
    if (!b) {
      b = new OrderBook();
      this.books.set(symbol, b);
    }
    return b;
  }

  /** followDepth keeps a symbol's book current; returns the release. */
  followDepth(symbol: string): () => void {
    const key = `depth:${symbol}`;
    return this.follow(key, channels.depth(symbol), (m) => {
      const book = this.book(symbol);
      const d = m.data as DepthData;
      if (m.type === "snapshot") book.snapshot(d, m.seq ?? 0);
      else book.update(d, m.seq);
      this.changed(key);
    });
  }

  /** recentTrades returns a symbol's latest trades, newest first. */
  recentTrades(symbol: string): TradeData[] {
    return this.trades.get(symbol) ?? EMPTY_TRADES;
  }

  /** seedTrades puts the REST list (newest first) before the pushes. */
  seedTrades(symbol: string, list: TradeData[]): void {
    const known = new Set((this.trades.get(symbol) ?? []).map((t) => t.trade_id));
    const merged = [...(this.trades.get(symbol) ?? []), ...list.filter((t) => !known.has(t.trade_id))];
    this.trades.set(symbol, merged.slice(0, TRADES_KEPT));
    this.changed(`trades:${symbol}`);
  }

  /** followTrades keeps a symbol's latest trades; returns the release. */
  followTrades(symbol: string): () => void {
    const key = `trades:${symbol}`;
    return this.follow(key, channels.trades(symbol), (m) => {
      const t = m.data as TradeData;
      const list = this.trades.get(symbol) ?? [];
      // A new array keeps useSyncExternalStore snapshots immutable; the
      // ring stays at TRADES_KEPT entries.
      this.trades.set(symbol, [t, ...list.slice(0, TRADES_KEPT - 1)]);
      this.changed(key);
    });
  }

  /** ticker returns a symbol's latest ticker, if any arrived. */
  ticker(symbol: string): TickerData | undefined {
    return this.tickers.get(symbol);
  }

  /** allTickers returns every ticker (a new Map on each change). */
  allTickers(): ReadonlyMap<string, TickerData> {
    return this.tickerSnapshot;
  }

  private tickerSnapshot: ReadonlyMap<string, TickerData> = new Map();

  /** seedTickers puts the REST tickers in before the pushes. */
  seedTickers(list: TickerData[]): void {
    for (const t of list) {
      const cur = this.tickers.get(t.symbol);
      if (!cur || cur.updated_at <= t.updated_at) this.tickers.set(t.symbol, t);
    }
    this.publishTickers(list.map((t) => t.symbol));
  }

  private publishTickers(symbols: string[]) {
    this.tickerSnapshot = new Map(this.tickers);
    this.changed("tickers");
    for (const s of symbols) this.changed(`ticker:${s}`);
  }

  /** followTickers keeps every ticker current (the tickers channel). */
  followTickers(): () => void {
    return this.follow("tickers", channels.tickers, (m) => {
      const list = m.data as TickerData[];
      if (m.type === "snapshot") this.tickers.clear();
      for (const t of list) this.tickers.set(t.symbol, t);
      this.publishTickers(list.map((t) => t.symbol));
    });
  }

  /** followTicker keeps one ticker current (the ticker:{symbol} channel). */
  followTicker(symbol: string): () => void {
    return this.follow(`ticker:${symbol}`, channels.ticker(symbol), (m) => {
      this.tickers.set(symbol, m.data as TickerData);
      this.publishTickers([symbol]);
    });
  }
}

const EMPTY_TRADES: TradeData[] = [];
