import { dec, OrderBook, type CandleData, type Level, type TradeData } from "@exchange/core";

// Fake market data for the stories (not exported by the package): a
// BTC-like pair with tick 0.1 and lot 0.0001, generated with integer
// arithmetic so every value is an exact decimal string.

/** rng is a small seeded generator (0 ≤ n < 1). */
export function rng(seed = 1): () => number {
  let s = seed;
  return () => {
    s = (s * 1103515245 + 12345) % 2147483648;
    return s / 2147483648;
  };
}

/** price turns a count of 0.1 ticks into a price string (632145 → "63214.5"). */
export function price(ticks: number): string {
  return dec.div(String(ticks), "10", 1);
}

/** lots turns a count of 0.0001 lots into a quantity string. */
export function lots(n: number): string {
  return dec.div(String(Math.max(1, Math.round(n))), "10000", 4);
}

function qty(rand: () => number): string {
  return lots(rand() ** 3 * 40_000 + 10);
}

export type FakeMarket = { book: OrderBook; mid: number; seq: number; rand: () => number };

/** fakeMarket builds a book of `levels` levels a side around 63,214.5. */
export function fakeMarket(levels = 80, seed = 7): FakeMarket {
  const rand = rng(seed);
  const mid = 632_145;
  const bids: Level[] = [];
  const asks: Level[] = [];
  for (let i = 1; i <= levels; i++) {
    bids.push([price(mid - i), qty(rand)]);
    asks.push([price(mid + i), qty(rand)]);
  }
  const book = new OrderBook();
  book.snapshot({ bids, asks }, 1);
  return { book, mid, seq: 1, rand };
}

/**
 * tick moves the fake market: a few levels near the top change or vanish,
 * and now and then the mid price walks a tick or two.
 */
export function tick(m: FakeMarket): void {
  const { rand } = m;
  if (rand() < 0.3) m.mid += Math.round((rand() - 0.5) * 6);
  const bids: Level[] = [];
  const asks: Level[] = [];
  for (let k = 0; k < 6; k++) {
    const d = 1 + Math.floor(rand() ** 2 * 30);
    bids.push([price(m.mid - d), rand() < 0.12 ? "0" : qty(rand)]);
    asks.push([price(m.mid + d), rand() < 0.12 ? "0" : qty(rand)]);
  }
  // Keep the book uncrossed after a mid move: nothing on the wrong side of the mid.
  const top = m.book.view(40);
  const midPrice = price(m.mid);
  for (const l of top.asks) if (dec.lte(l.price, midPrice)) asks.push([l.price, "0"]);
  for (const l of top.bids) if (dec.gte(l.price, midPrice)) bids.push([l.price, "0"]);
  m.book.update({ bids, asks }, ++m.seq);
}

let tradeNumber = 1000;

/** fakeTrade returns a trade near the mid price. */
export function fakeTrade(m: FakeMarket, at = Date.now()): TradeData {
  const buy = m.rand() < 0.5;
  const p = price(m.mid + (buy ? 1 : -1) * Math.floor(m.rand() * 3));
  const q = lots(m.rand() ** 4 * 20_000 + 1);
  tradeNumber++;
  return {
    trade_id: `trade-${tradeNumber}`,
    trade_number: tradeNumber,
    price: p,
    quantity: q,
    quote_quantity: dec.mul(p, q),
    taker_side: buy ? "BUY" : "SELL",
    executed_at: new Date(at).toISOString(),
  };
}

/** fakeTrades returns n trades, newest first, one every ~0.7 s. */
export function fakeTrades(m: FakeMarket, n: number, end = Date.parse("2026-09-30T10:13:12Z")): TradeData[] {
  return Array.from({ length: n }, (_, i) => fakeTrade(m, end - i * 700));
}

const HOUR = 3_600_000;

/** fakeCandles returns n hourly candles ending before `end`, oldest first (a random walk). */
export function fakeCandles(n: number, end = Date.parse("2026-09-30T10:00:00Z"), seed = 3, stepMs = HOUR): CandleData[] {
  const rand = rng(seed);
  let close = 600_000 + Math.floor(rand() * 40_000);
  const out: CandleData[] = [];
  for (let i = n - 1; i >= 0; i--) {
    const open = close;
    close = Math.max(10_000, open + Math.round((rand() - 0.49) * 4_000));
    const high = Math.max(open, close) + Math.round(rand() * 1_500);
    const low = Math.min(open, close) - Math.round(rand() * 1_500);
    const volume = lots(rand() * 3_000_000 + 50_000);
    out.push({
      open_time: new Date(end - i * stepMs).toISOString(),
      open: price(open),
      high: price(high),
      low: price(low),
      close: price(close),
      volume,
      quote_volume: dec.mul(volume, price(close)),
      trade_count: Math.floor(rand() * 5000),
      closed: i > 0,
    });
  }
  return out;
}
