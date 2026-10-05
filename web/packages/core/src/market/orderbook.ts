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
  /** The step the view is cut at, when cut by OrderBook.fit (perhaps finer than the one asked). */
  step?: string;
  /** The steps offered whose views fill the book (OrderBook.fit), finest first. */
  fits?: string[];
  /**
   * The steps whose views fill the book now (OrderBook.fit): `fits` and,
   * while a finer step is held, those above it that fill without the
   * levels to spare.
   */
  fills?: string[];
};

/** The sides a book shows (the terminal's view mode), which must fill. */
export type BookSides = "both" | "bids" | "asks";

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
   * A level smaller than `minQty` (what the page can show, displayUnit)
   * is folded into the next one away from the spread rather than shown as
   * 0.0000; one at the far end is left out.
   */
  view(depth: number, step = "", minQty = ""): BookView {
    const bids = aggregate(this.bids, depth, step, "down", minQty);
    const asks = aggregate(this.asks, depth, step, "up", minQty);
    const lastBid = bids[bids.length - 1]?.total ?? "0";
    const lastAsk = asks[asks.length - 1]?.total ?? "0";
    const best = { bid: this.bestBid, ask: this.bestAsk };
    const spread = best.bid && best.ask ? sub(best.ask, best.bid) : null;
    return { bids, asks, maxTotal: cmp(lastBid, lastAsk) >= 0 ? lastBid : lastAsk, spread, seq: this.seq };
  }

  /**
   * fit cuts the view at `step`, one of `steps` (finest first), unless that
   * leaves a side shown short of `depth` levels while a finer step fills
   * it: the public book carries 200 levels a side, and in a dense book
   * those span too little of the price for a coarse step (BTC-USDT: 2 to
   * 40 USDT, so 10 never fills, and when it is densest neither does the
   * default 1, five rows a side at 20:20 on 2026-10-05, B71). A book short
   * at every step, a thin one, keeps the step asked. `prev`, the step of
   * the last view, holds a finer step until a coarser one fills with some
   * levels to spare, so the book does not swap scales at each update near
   * the threshold; while it holds, the steps above are not offered. The
   * view reports its step and the steps that fill (all of them while
   * nothing tells: an empty book, a thin one).
   */
  fit(depth: number, step: string, steps: readonly string[], minQty = "", sides: BookSides = "both", prev = ""): BookView {
    // The views are cut deeper by the levels to spare, then to `depth`.
    const spare = depth + Math.max(2, Math.ceil(depth / 5));
    const cuts = new Map<string, BookView>();
    const cut = (s: string) => {
      let v = cuts.get(s);
      if (!v) {
        v = this.view(spare, s, minQty);
        cuts.set(s, v);
      }
      return v;
    };
    const has = (s: string, n: number) => {
      const v = cut(s);
      return (sides === "asks" || v.bids.length >= n) && (sides === "bids" || v.asks.length >= n);
    };
    const shown = (s: string, fits: string[], fills = fits): BookView => ({ ...trim(cut(s), depth), step: s, fits, fills });
    const at = steps.indexOf(step);
    if (at < 0 || (this.bids.length === 0 && this.asks.length === 0)) return shown(step, [...steps]);
    // A coarser step never has more levels, dust folded or not: each step
    // is a multiple of the finer ones (bookSteps) rounded the same way, so
    // its buckets are unions of theirs, and folding closes a level at the
    // first bucket that brings it to minQty, which among the coarser
    // boundaries (some of the finer ones) comes no sooner. The steps that
    // fill are therefore the finest ones, up to `top` (tested on random
    // books in orderbook.test.ts).
    let top = at;
    if (has(step, depth)) while (top + 1 < steps.length && has(steps[top + 1]!, depth)) top++;
    else while (top >= 0 && !has(steps[top]!, depth)) top--;
    if (top < 0) return shown(step, [...steps]);
    let use = Math.min(at, top);
    const was = steps.indexOf(prev);
    if (was >= 0 && was < use) {
      // Back up from a finer step only as far as levels are to spare. While
      // held there, the steps above it are not offered: they fill, but not
      // with the levels to spare that showing them takes now.
      while (use > was && !has(steps[use]!, spare)) use--;
      if (use < Math.min(at, top)) return shown(steps[use]!, steps.slice(0, use + 1), steps.slice(0, top + 1));
    }
    return shown(steps[use]!, steps.slice(0, top + 1));
  }
}

// trim cuts a view to `depth` levels a side, its largest total with them.
function trim(v: BookView, depth: number): BookView {
  if (v.bids.length <= depth && v.asks.length <= depth) return v;
  const bids = v.bids.slice(0, depth);
  const asks = v.asks.slice(0, depth);
  const lastBid = bids[bids.length - 1]?.total ?? "0";
  const lastAsk = asks[asks.length - 1]?.total ?? "0";
  return { ...v, bids, asks, maxTotal: cmp(lastBid, lastAsk) >= 0 ? lastBid : lastAsk };
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

function aggregate(side: Level[], depth: number, step: string, mode: "down" | "up", minQty: string): BookLevel[] {
  const out: BookLevel[] = [];
  let total = "0";
  const places = step ? decimalsOf(step) : -1;
  const dust = (l: BookLevel | undefined) => l !== undefined && minQty !== "" && cmp(l.quantity, minQty) < 0;
  for (const [price, qty] of side) {
    const p = step ? fixed(quantize(price, step, mode), places) : price;
    const lastLevel = out[out.length - 1];
    if (lastLevel && lastLevel.price === p) {
      lastLevel.quantity = add(lastLevel.quantity, qty);
      total = add(total, qty);
      lastLevel.total = total;
      continue;
    }
    // A new price closes the level before it: dust goes into this one.
    const carry = dust(lastLevel) ? out.pop()!.quantity : "0";
    if (out.length === depth) break;
    total = add(total, qty);
    out.push({ price: p, quantity: add(carry, qty), total });
  }
  if (dust(out[out.length - 1])) out.pop();
  return out;
}

/** displayUnit is the smallest amount shown at `decimals` places: 4 → "0.0001", 0 → "1". */
export function displayUnit(decimals: number): string {
  return decimals > 0 ? `0.${"0".repeat(decimals - 1)}1` : "1";
}

// fixed pads a quantized price to the step's decimals ("63210" at 0.1 →
// "63210.0"), so aggregated prices line up.
function fixed(v: string, places: number): string {
  if (places <= 0) return v;
  const [int, frac = ""] = v.split(".");
  return `${int}.${frac.padEnd(places, "0")}`;
}
