import { add, cmp, decimalsOf, isZero, quantize, sub } from "../format/decimal";
import type { DepthData, Level } from "../ws/types";

// OrderBook keeps one symbol's depth outside React (design §4.3): each side
// is an array sorted best first; an update finds each price by binary
// search and inserts, replaces or removes it, O(log n) per level instead
// of rebuilding and resorting the book for every message.

export type BookLevel = { price: string; quantity: string; /** Running total from the best price. */ total: string };

export type BookView = {
  bids: BookLevel[];
  asks: BookLevel[];
  /** The largest running total of the two sides, for depth bars. */
  maxTotal: string;
  /** Best ask − best bid; null with an empty side. */
  spread: string | null;
  seq: number;
};

// Prices compare as decimals; the cache avoids reparsing a price for every
// comparison of a binary search.
function compareAsc(a: string, b: string): number {
  return cmp(a, b);
}

export class OrderBook {
  /** Bids high to low, asks low to high. */
  private bids: Level[] = [];
  private asks: Level[] = [];
  seq = 0;

  /** snapshot replaces the book. */
  snapshot(d: DepthData, seq = 0): void {
    this.bids = [...d.bids].sort((a, b) => compareAsc(b[0], a[0]));
    this.asks = [...d.asks].sort((a, b) => compareAsc(a[0], b[0]));
    this.seq = seq;
  }

  /** update applies changed levels; a quantity of "0" removes the level. */
  update(d: DepthData, seq?: number): void {
    for (const l of d.bids) apply(this.bids, l, -1);
    for (const l of d.asks) apply(this.asks, l, 1);
    if (seq !== undefined) this.seq = seq;
  }

  clear(): void {
    this.bids = [];
    this.asks = [];
    this.seq = 0;
  }

  get bestBid(): string | null {
    return this.bids[0]?.[0] ?? null;
  }

  get bestAsk(): string | null {
    return this.asks[0]?.[0] ?? null;
  }

  /**
   * view returns the best `depth` levels of each side, merged into steps of
   * `step` (a multiple of the tick, e.g. "0.1"; "" keeps the raw prices):
   * bids round down, asks up, so a level never looks better than it is.
   */
  view(depth: number, step = ""): BookView {
    const bids = aggregate(this.bids, depth, step, "down");
    const asks = aggregate(this.asks, depth, step, "up");
    const lastBid = bids[bids.length - 1]?.total ?? "0";
    const lastAsk = asks[asks.length - 1]?.total ?? "0";
    const best = { bid: this.bestBid, ask: this.bestAsk };
    const spread = best.bid && best.ask ? sub(best.ask, best.bid) : null;
    return { bids, asks, maxTotal: cmp(lastBid, lastAsk) >= 0 ? lastBid : lastAsk, spread, seq: this.seq };
  }
}

// apply puts one level into a side sorted by dir (1: ascending asks, -1:
// descending bids).
function apply(side: Level[], level: Level, dir: 1 | -1): void {
  const [price, qty] = level;
  let lo = 0;
  let hi = side.length;
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    const c = dir * compareAsc(side[mid]![0], price);
    if (c < 0) lo = mid + 1;
    else hi = mid;
  }
  const found = lo < side.length && compareAsc(side[lo]![0], price) === 0;
  if (isZero(qty)) {
    if (found) side.splice(lo, 1);
    return;
  }
  if (found) side[lo] = [price, qty];
  else side.splice(lo, 0, [price, qty]);
}

function aggregate(side: Level[], depth: number, step: string, mode: "down" | "up"): BookLevel[] {
  const out: BookLevel[] = [];
  let total = "0";
  const places = step ? decimalsOf(step) : -1;
  for (const [price, qty] of side) {
    const p = step ? fixed(quantize(price, step, mode), places) : price;
    const lastLevel = out[out.length - 1];
    if (lastLevel && lastLevel.price === p) {
      lastLevel.quantity = add(lastLevel.quantity, qty);
      total = add(total, qty);
      lastLevel.total = total;
      continue;
    }
    if (out.length === depth) break;
    total = add(total, qty);
    out.push({ price: p, quantity: qty, total });
  }
  return out;
}

// fixed pads a quantized price to the step's decimals ("63210" at 0.1 →
// "63210.0"), so aggregated prices line up.
function fixed(v: string, places: number): string {
  if (places <= 0) return v;
  const [int, frac = ""] = v.split(".");
  return `${int}.${frac.padEnd(places, "0")}`;
}
